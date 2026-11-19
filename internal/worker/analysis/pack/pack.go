// Package pack is the analyzer-pack contract of R1 (docs/ANALYZERS.md). A language pack
// (W16 C/C++, W17 Python, W18 JS/TS, W19 Go, W20 Rust, W21 structure and
// secrets) contributes Analyzers; the Composite turns an ordered list of them
// into the one analysis.Handler of the deep-review capability. Planning reads
// only the snapshot manifest and scope, so an analyzer whose tool the operator
// did not configure still plans its tasks and reports them as unsupported.
package pack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Composite identity: the deep-review handler of a node.
const (
	CompositeName    = "analyzers"
	CompositeVersion = "1"
	Capability       = "analysis:deep-review"
)

// Task reasons of the pack contract (TaskResult.Reason and outcome.reason).
const (
	// ReasonToolNotConfigured: the operator gave no path for the tool.
	ReasonToolNotConfigured = "tool_not_configured"
	// ReasonToolUnavailable: a path was given but the tool failed its
	// startup validation or version probe.
	ReasonToolUnavailable = "tool_unavailable"
	// ReasonToolchainNotConfigured: the tool needs a language toolchain
	// (e.g. staticcheck needs --go) that is not configured.
	ReasonToolchainNotConfigured = "toolchain_not_configured"
	// ReasonUnknownTask: Run was given a key the analyzer did not plan.
	ReasonUnknownTask = "unknown_task"
	// ReasonRepositoryCodeNotAllowed: the task would execute
	// repository-controlled code and the job carries no repository-code
	// grant (docs/ANALYZERS.md#저장소-코드-실행-허가). It takes precedence over the not-configured
	// reasons, which stay visible as limitations.
	ReasonRepositoryCodeNotAllowed = "repository_code_not_allowed"
	// LimRepositoryCodeExecuted marks a task that ran processes under the
	// repository-code grant.
	LimRepositoryCodeExecuted = "repository_code_executed"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	toolPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	// reserved task keys and prefixes belong to discovery and review roles.
	reservedKeys = []string{"env.discover", "docs.discover"}
)

// Descriptor is the static identity of an analyzer. It never depends on the
// node's tools.
type Descriptor struct {
	// ID is the analyzer id recorded in observations ("go.vet",
	// "go.staticcheck"); see obsdoc.AnalyzerIDPattern (no hyphen).
	ID string
	// Name owns task keys: a key belongs to this analyzer iff it equals
	// Name or starts with Name+"." (never a bare prefix). It is also the
	// scratch directory name.
	Name string
	// Ecosystem is an envdoc ecosystem (cpp, go, javascript, python, rust)
	// or "any".
	Ecosystem string
	// Required marks the baseline analyzer of its ecosystem: its tasks are
	// required_for_verdict. Extra-depth analyzers are not required.
	Required bool
	// Tool is the operator tool name; it is also the agent flag name
	// (--<tool>). "go" is the existing --go toolchain.
	Tool string
	// VersionArgs is the argv (after the executable) that prints the tool
	// version; ParseVersion reads its stdout. Both drive the startup probe
	// and the env.discover probe.
	VersionArgs  []string
	ParseVersion func(stdout []byte) (version string, ok bool)
	// Toolchains are the shared toolchains (builtin's toolchain table, e.g.
	// "node") the tool needs, sorted unique. builtin builds the analyzer only
	// when each one is configured (--<toolchain>) and passed its startup
	// probe, and passes the validated handles; otherwise the analyzer is the
	// NotConfigured stand-in (docs/ANALYZERS.md#도구-경로). The go toolchain is not listed:
	// --go stays its own setting.
	Toolchains []string
	// Interpreter, when set, is the toolchain (one of Toolchains) that runs
	// the tool: the tool's absolute --<tool> path is a script started as
	// <interpreter> <tool path> <args>, with PATH set to the interpreter's
	// directory only, never through the script's #! line.
	Interpreter string
}

// Validate checks a descriptor.
func (d Descriptor) Validate() error {
	switch {
	case !obsdoc.AnalyzerIDPattern.MatchString(d.ID) || len(d.ID) > 64:
		return fmt.Errorf("pack: analyzer id %q is invalid", d.ID)
	case !namePattern.MatchString(d.Name):
		return fmt.Errorf("pack: analyzer %s task name %q is invalid", d.ID, d.Name)
	case !slices.Contains(obsdoc.Ecosystems, d.Ecosystem):
		return fmt.Errorf("pack: analyzer %s ecosystem %q is invalid", d.ID, d.Ecosystem)
	case !toolPattern.MatchString(d.Tool) || d.ParseVersion == nil:
		return fmt.Errorf("pack: analyzer %s tool or version parser is invalid", d.ID)
	case d.Interpreter != "" && !slices.Contains(d.Toolchains, d.Interpreter):
		return fmt.Errorf("pack: analyzer %s interpreter %q is not one of its toolchains", d.ID, d.Interpreter)
	}
	for i, t := range d.Toolchains {
		if !toolPattern.MatchString(t) || t == "go" || t == d.Tool || (i > 0 && d.Toolchains[i-1] >= t) {
			return fmt.Errorf("pack: analyzer %s toolchains must be sorted unique names other than go and its tool", d.ID)
		}
	}
	return nil
}

// Toolchain is a validated shared toolchain (docs/ANALYZERS.md#도구-경로): the operator's
// absolute path (--<name>), the directory of the resolved executable (the
// only PATH entry a child of the toolchain gets) and the version its startup
// probe read. builtin passes one per Descriptor.Toolchains entry.
type Toolchain struct {
	Name       string
	Executable string
	Dir        string
	Version    string
}

// Owns reports whether a task key belongs to the analyzer named name.
func Owns(name, key string) bool { return key == name || strings.HasPrefix(key, name+".") }

// Task is one planned analyzer task with what it targets, so an analyzer that
// cannot run still reports its scope.
type Task struct {
	Spec analysis.TaskSpec
	// Module is the snapshot-relative module directory ("." = root).
	Module string
	// Patterns are the tool scope arguments relative to Module.
	Patterns []string
	// FilesInScope is the number of manifest files the task would hand to
	// the tool; nil when the analyzer cannot count them from the manifest.
	FilesInScope *int
	// RepositoryCode declares that running the task executes
	// repository-controlled code: build scripts, configuration programs,
	// compiler plugins, proc macros, configure steps (docs/ANALYZERS.md#저장소-코드-실행-허가). Plan
	// derives it from the manifest and scope only, so the NotConfigured
	// stand-in declares it identically, and Run never executes more than
	// Plan declared (no fallback from a declared-safe mode to a configure
	// step). One tool may plan tasks of both kinds. Composite.Run refuses a
	// declared task when the job has no repository-code grant.
	RepositoryCode bool
}

// Analyzer is what a pack implements for one tool.
type Analyzer interface {
	Descriptor() Descriptor
	// Plan derives the analyzer's tasks from job.Snapshot.Manifest and
	// job.Scope only. It must not need the tool: NotConfigured reuses it.
	// Every task key is owned by Descriptor().Name, Required equals
	// Descriptor().Required and Stage is static_analysis. An analyzer plans
	// no task when its ecosystem has no module in scope.
	Plan(ctx context.Context, job analysis.Job) ([]Task, error)
	// Run executes one planned task, starting processes only via runner,
	// and writes its envelope to obsdoc.ArtifactPath(job.Scratch, key)
	// (go-vet keeps its v0.1 document).
	Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult
}

// PlanFunc is an analyzer's tool-independent planner.
type PlanFunc func(ctx context.Context, job analysis.Job) ([]Task, error)

// Composite runs several analyzers as one handler: plans are concatenated in
// member order, every task key is owned by exactly one member, and Run is
// dispatched to the owner. After the members it plans one gap task per
// language ecosystem with source in scope and no member (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백).
type Composite struct {
	members []Analyzer
}

var _ analysis.Handler = (*Composite)(nil)

// NewComposite validates the members (in plan order).
func NewComposite(members ...Analyzer) (*Composite, error) {
	if len(members) == 0 {
		return nil, errors.New("pack: composite needs a member")
	}
	ids, names := map[string]bool{}, []string{}
	for _, m := range members {
		if m == nil {
			return nil, errors.New("pack: nil analyzer")
		}
		d := m.Descriptor()
		if err := d.Validate(); err != nil {
			return nil, err
		}
		if ids[d.ID] {
			return nil, fmt.Errorf("pack: duplicate analyzer %s", d.ID)
		}
		for _, n := range names {
			if Owns(n, d.Name) || Owns(d.Name, n) {
				return nil, fmt.Errorf("pack: analyzer names %q and %q overlap", n, d.Name)
			}
		}
		if strings.HasPrefix(d.Name, "review") || Owns(d.Name, "env.discover") || Owns(d.Name, "docs.discover") || Owns(d.Name, GapName) || Owns(GapName, d.Name) {
			return nil, fmt.Errorf("pack: analyzer name %q is reserved", d.Name)
		}
		ids[d.ID] = true
		names = append(names, d.Name)
	}
	return &Composite{members: append([]Analyzer(nil), members...)}, nil
}

// Info is the deep-review handler identity. Each task's envelope records
// the analyzer, adapter and parser that produced it.
func (c *Composite) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: CompositeName, Version: CompositeVersion, Capability: Capability}
}

// Descriptors returns the member descriptors in plan order.
func (c *Composite) Descriptors() []Descriptor {
	out := make([]Descriptor, len(c.members))
	for i, m := range c.members {
		out[i] = m.Descriptor()
	}
	return out
}

// PlanTasks returns every member's tasks in member order, validated.
func (c *Composite) PlanTasks(ctx context.Context, job analysis.Job) ([]Task, error) {
	var out []Task
	seen := map[string]bool{}
	for _, m := range c.members {
		d := m.Descriptor()
		tasks, err := m.Plan(ctx, job)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.ID, err)
		}
		for _, t := range tasks {
			k := t.Spec.Key
			switch {
			case !analysis.ValidTaskKey(k) || !Owns(d.Name, k) || seen[k] || slices.Contains(reservedKeys, k) || strings.HasPrefix(k, "review."):
				return nil, fmt.Errorf("pack: analyzer %s planned key %q it does not own", d.ID, k)
			case t.Spec.Required != d.Required:
				return nil, fmt.Errorf("pack: analyzer %s task %s required flag differs from its descriptor", d.ID, k)
			case t.Spec.Stage != domain.StageStaticAnalysis:
				return nil, fmt.Errorf("pack: analyzer %s task %s is not a static_analysis task", d.ID, k)
			case t.Module != "." && domain.ValidateEvidencePath(t.Module) != nil:
				return nil, fmt.Errorf("pack: analyzer %s task %s module is invalid", d.ID, k)
			}
			seen[k] = true
			out = append(out, t)
		}
	}
	return append(out, c.gaps(job)...), nil
}

// gaps are the gap tasks of job: one per language ecosystem with source in
// scope and no member analyzer (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백). A member of ecosystem "any"
// (structure, secrets) never fills a language gap. An ecosystem that has a
// member is that member's to plan, including a visible placeholder when its
// source cannot be analyzed.
func (c *Composite) gaps(job analysis.Job) []Task {
	covered := map[string]bool{}
	for _, m := range c.members {
		covered[m.Descriptor().Ecosystem] = true
	}
	files := SourceFiles(job.Snapshot.Manifest, job.Scope)
	var out []Task
	for _, eco := range InScope(job.Snapshot.Manifest, job.Scope) {
		if covered[eco] {
			continue
		}
		n := files[eco]
		out = append(out, Task{Spec: analysis.TaskSpec{Key: GapName + "." + eco, Required: false, Title: "static analysis gap: no " + eco + " analyzer in this build",
			Stage: domain.StageStaticAnalysis}, Module: ".", Patterns: []string{}, FilesInScope: &n})
	}
	return out
}

// Plan implements analysis.Handler.
func (c *Composite) Plan(ctx context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tasks, err := c.PlanTasks(ctx, job)
	if err != nil {
		return nil, err
	}
	out := make([]analysis.TaskSpec, len(tasks))
	for i, t := range tasks {
		out[i] = t.Spec
	}
	return out, nil
}

// Owner returns the member owning key.
func (c *Composite) Owner(key string) (Analyzer, bool) {
	for _, m := range c.members {
		if Owns(m.Descriptor().Name, key) {
			return m, true
		}
	}
	return nil, false
}

func handlerFailure(limitation string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Limitations: []string{limitation}}
}

func findTask(tasks []Task, key string) *Task {
	for i := range tasks {
		if tasks[i].Spec.Key == key {
			return &tasks[i]
		}
	}
	return nil
}

// Run dispatches to the owning analyzer. The repository-code grant is
// enforced here, once, before dispatch: the declaration is re-derived from
// the owner's plan (never taken from the TaskSpec handed back by the
// worker), and a key the plan does not produce fails closed.
func (c *Composite) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	if Owns(GapName, task.Key) {
		if t := findTask(c.gaps(job), task.Key); t != nil {
			return NotRun(job, gapDescriptor(t.Spec.Key[len(GapName)+1:]), *t, ReasonAnalyzerPackMissing)
		}
		return handlerFailure(ReasonUnknownTask)
	}
	m, ok := c.Owner(task.Key)
	if !ok {
		return handlerFailure(ReasonUnknownTask)
	}
	tasks, err := m.Plan(ctx, job)
	if err != nil {
		return handlerFailure("plan_failed")
	}
	t := findTask(tasks, task.Key)
	if t == nil {
		return handlerFailure(ReasonUnknownTask)
	}
	if !t.RepositoryCode {
		return m.Run(ctx, job, task, runner)
	}
	if !job.AllowRepositoryCode {
		// Policy before tooling: without the grant nothing is started, even
		// for an analyzer whose tool is also missing; that reason stays
		// visible as a limitation.
		var also []string
		if nc, ok := m.(notConfigured); ok {
			also = append(also, nc.reason)
		}
		return NotRun(job, m.Descriptor(), *t, ReasonRepositoryCodeNotAllowed, also...)
	}
	r := m.Run(ctx, job, task, runner)
	if len(r.Executions) > 0 {
		r.Limitations = append(r.Limitations, LimRepositoryCodeExecuted)
	}
	return r
}

// notConfigured plans like the analyzer it stands for and reports every
// task as unsupported without running anything.
type notConfigured struct {
	d      Descriptor
	plan   PlanFunc
	reason string
}

// NotConfigured stands in for an analyzer whose tool cannot run on this node.
// Its tasks are planned exactly like the real analyzer's (so the gap is
// visible in the plan, the stage and the report) and finish unsupported with
// reason (ReasonToolNotConfigured, ReasonToolUnavailable or
// ReasonToolchainNotConfigured) and an envelope that records nothing ran.
func NotConfigured(d Descriptor, plan PlanFunc, reason string) (Analyzer, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if plan == nil || !slices.Contains([]string{ReasonToolNotConfigured, ReasonToolUnavailable, ReasonToolchainNotConfigured}, reason) {
		return nil, errors.New("pack: not-configured analyzer needs a planner and a known reason")
	}
	return notConfigured{d: d, plan: plan, reason: reason}, nil
}

func (n notConfigured) Descriptor() Descriptor { return n.d }

func (n notConfigured) Plan(ctx context.Context, job analysis.Job) ([]Task, error) {
	return n.plan(ctx, job)
}

// NotRunAdapter is the adapter identity of envelopes written for analyzers
// that could not run on the node.
var NotRunAdapter = obsdoc.Component{ID: "codeye-not-run", Version: "1"}

func (n notConfigured) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	tasks, err := n.plan(ctx, job)
	if err != nil {
		return handlerFailure("plan_failed")
	}
	t := findTask(tasks, task.Key)
	if t == nil {
		return handlerFailure(ReasonUnknownTask)
	}
	return NotRun(job, n.d, *t, n.reason)
}

// NotRun ends planned task t of analyzer d without starting a process: the
// task is unsupported with reason, and its envelope records exit class
// not_run, no execution, no observation and processed_paths=[] (a known
// zero). Further limitation codes (e.g. a second reason) are recorded with
// reason.
func NotRun(job analysis.Job, d Descriptor, t Task, reason string, limitations ...string) analysis.TaskResult {
	doc := obsdoc.New(obsdoc.Analyzer{ID: d.ID, Ecosystem: d.Ecosystem, Required: d.Required}, NotRunAdapter, NotRunAdapter, t.Spec.Key, job.Snapshot.Digest)
	doc.Module.Dir = t.Module
	if doc.Module.Dir == "" {
		doc.Module.Dir = "."
	}
	doc.Patterns = append([]string{}, t.Patterns...)
	doc.Tool.Name = d.Tool
	doc.RulePack = obsdoc.RulePack{ID: "none", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigNone, SourceSuppressions: obsdoc.SuppressNone, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskUnsupported, Reason: &reason, ExitClass: obsdoc.ExitNotRun}
	doc.Coverage.FilesInScope = t.FilesInScope
	doc.Coverage.ProcessedPaths = []string{}
	doc.Limitations = append([]string{reason}, limitations...)
	art, err := WriteEnvelope(job, doc)
	if err != nil {
		return handlerFailure("observations_write_failed")
	}
	return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: reason, Artifacts: []analysis.ArtifactFile{art}, Limitations: append([]string{reason}, limitations...)}
}

// Expect returns the obsdoc expectation for a task of job (manifest lookup
// of included files).
func Expect(job analysis.Job, key string) obsdoc.Expect {
	index := map[string]string{}
	for _, e := range job.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			index[e.Path] = e.SHA256
		}
	}
	return obsdoc.Expect{TaskKey: key, Snapshot: job.Snapshot.Digest, Lookup: func(p string) (string, bool) { s, ok := index[p]; return s, ok }}
}

// WriteEnvelope finishes, validates and writes doc to
// obsdoc.ArtifactPath(job.Scratch, doc.TaskKey) (created exclusively) and
// returns the observations artifact.
func WriteEnvelope(job analysis.Job, doc obsdoc.Document) (analysis.ArtifactFile, error) {
	if !filepath.IsAbs(job.Scratch) {
		return analysis.ArtifactFile{}, errors.New("pack: scratch must be absolute")
	}
	doc.Finish()
	b, err := obsdoc.Marshal(doc, Expect(job, doc.TaskKey))
	if err != nil {
		return analysis.ArtifactFile{}, err
	}
	p := obsdoc.ArtifactPath(job.Scratch, doc.TaskKey)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return analysis.ArtifactFile{}, err
	}
	if err := analysis.WriteFileExclusive(p, b); err != nil {
		return analysis.ArtifactFile{}, err
	}
	return analysis.ArtifactFile{Path: p, Role: analysis.RoleObservations, MediaType: analysis.MediaObservations}, nil
}

// ToolProbe is the env.discover version probe of one operator tool (docs/ANALYZERS.md
// discovery hook). Executable "" means the operator did not configure it;
// the observation is then not_probed.
//
// A tool whose descriptor names an Interpreter is probed as
// <InterpreterExecutable> <Executable> <Args> with PATH set to the
// interpreter's directory; with the interpreter not configured the probe
// fails (toolchain_not_configured) without starting anything. The same type
// describes a shared toolchain's own probe (builtin.ToolchainProbes), which
// env.discover records as a runtime observation.
type ToolProbe struct {
	Tool         string
	Analyzers    []string // analyzer ids using the tool, sorted
	Ecosystems   []string // ecosystems of those analyzers ("any" included)
	Executable   string
	Args         []string
	ParseVersion func([]byte) (string, bool)
	// Interpreter is the toolchain name that runs the tool ("" = the tool
	// is executed directly); InterpreterExecutable is its operator path.
	Interpreter           string
	InterpreterExecutable string
}
