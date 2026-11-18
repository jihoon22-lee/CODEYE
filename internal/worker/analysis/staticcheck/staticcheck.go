// Package staticcheck is the W19 Staticcheck adapter of the Go pack. It runs
// the operator-installed staticcheck binary natively through the W05 runner
// in the attempt workspace copy, offline and with the same build context as
// go vet (GOTOOLCHAIN=local, GOPROXY=off, GOENV=off, GOWORK=off, GOFLAGS
// unset, CGO_ENABLED=0), and records the result in the generic observations
// envelope (obsdoc). Staticcheck is a separate engine: it does not run go
// vet, and the CODEYE rule selection drops the checks whose construct an
// enabled go vet analyzer reports in the same run (docs/39 §6).
package staticcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Identity.
const (
	AnalyzerID     = "go.staticcheck"
	Name           = "staticcheck"
	Tool           = "staticcheck"
	AdapterVersion = "1"
	// RulePackID/Version name the CODEYE check selection below.
	RulePackID      = "codeye-go-staticcheck"
	RulePackVersion = "1"
)

// Checks is the CODEYE selection passed with -checks (it overrides the
// repository's staticcheck.conf "checks"; with ST* disabled no other
// staticcheck.conf option applies). SA* are the correctness, concurrency,
// testing, dead-code and performance checks, U1000 is unused code. S1*
// (simplifications) and ST* (style) are not run (docs/06 §7). The excluded SA
// checks report the same construct as an enabled go vet analyzer (measured
// with staticcheck 2026.2.2 and go1.27.4 on one fixture, docs/39 §6):
// SA1014 unmarshal, SA1017 sigchanyzer, SA2000 waitgroup, SA2002
// testinggoroutine, SA4021 appends, SA5009 printf.
const Checks = "SA*,U1000,-SA1014,-SA1017,-SA2000,-SA2002,-SA4021,-SA5009"

// VetOverlap maps each excluded check to the go vet analyzer that reports
// the same construct.
var VetOverlap = map[string]string{"SA1014": "unmarshal", "SA1017": "sigchanyzer", "SA2000": "waitgroup", "SA2002": "testinggoroutine", "SA4021": "appends", "SA5009": "printf"}

// Execution names inside one staticcheck task, in run order.
const (
	execGoVersion = "go.version"
	execVersion   = "staticcheck.version"
	execModEdit   = "go.modedit"
	execList      = "go.list"
	execCheck     = "staticcheck.check"
)

// Defaults for one execution (as go vet).
const (
	DefaultTimeout     = 15 * time.Minute
	DefaultStdoutLimit = 32 << 20
	DefaultStderrLimit = 4 << 20
	probeTimeout       = 30 * time.Second
)

// Descriptor is staticcheck's pack identity: an extra-depth (not required)
// Go analyzer.
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Ecosystem: "go", Required: false, Tool: Tool, VersionArgs: []string{"-version"}, ParseVersion: ParseVersion}
}

// Plan is the tool-independent planner: one task per Go module, keyed
// staticcheck, staticcheck.m01, ... in go vet's module order. A snapshot or
// scope without Go packages plans nothing.
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan(job)
}

// plan is deterministic for a manifest and scope; Run re-derives it.
func plan(job analysis.Job) ([]pack.Task, error) {
	mods, err := govet.PlanModules(job.Snapshot.Manifest, job.Scope, Name, "staticcheck")
	if err != nil {
		return nil, err
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	modules := govet.ModuleDirs(files)
	out := make([]pack.Task, 0, len(mods))
	for _, m := range mods {
		n := len(govet.GoFilesInScope(files, modules, m.ModuleDir, m.Patterns))
		out = append(out, pack.Task{Spec: analysis.TaskSpec{Key: m.Key, Required: false, Title: m.Title, Stage: domain.StageStaticAnalysis},
			Module: m.ModuleDir, Patterns: m.Patterns, FilesInScope: &n})
	}
	return out, nil
}

// Config selects the operator-installed tools. Nothing is searched in PATH
// and nothing is downloaded.
type Config struct {
	// Executable is the absolute staticcheck binary.
	Executable string
	// Go is the configured go toolchain (the go-vet handler): staticcheck
	// loads packages through `go list`, so it needs the same go command.
	Go          *govet.Handler
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Analyzer is the configured staticcheck adapter.
type Analyzer struct {
	cfg      Config
	resolved string
}

var _ pack.Analyzer = (*Analyzer)(nil)

// New validates the configuration without running anything.
func New(cfg Config) (*Analyzer, error) {
	if cfg.Go == nil {
		return nil, errors.New("staticcheck needs the configured go toolchain")
	}
	if !filepath.IsAbs(cfg.Executable) {
		return nil, errors.New("staticcheck executable must be an absolute path")
	}
	resolved, err := nativepath.Resolve(cfg.Executable)
	if err != nil {
		return nil, errors.New("staticcheck executable cannot be resolved")
	}
	if st, err := os.Stat(resolved); err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("staticcheck executable is not a regular file")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.StdoutLimit <= 0 {
		cfg.StdoutLimit = DefaultStdoutLimit
	}
	if cfg.StderrLimit <= 0 {
		cfg.StderrLimit = DefaultStderrLimit
	}
	return &Analyzer{cfg: cfg, resolved: resolved}, nil
}

// Descriptor implements pack.Analyzer.
func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor() }

// Plan implements pack.Analyzer.
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}

// env is the complete explicit environment: go vet's (PATH is the go
// binary directory only, so `go list` resolves to the configured go) plus
// staticcheck's own cache in task scratch.
func (a *Analyzer) env(gocache, gopath, modcache, cache string) map[string]string {
	env := a.cfg.Go.Env(gocache, gopath, modcache)
	env["STATICCHECK_CACHE"] = cache
	return env
}

// Probe runs `staticcheck -version` in a fresh directory under dir (outside
// any source tree). An analyzer whose probe fails is not used.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("probe dir must be absolute")
	}
	work, err := os.MkdirTemp(dir, "staticcheck-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	for _, d := range []string{"gocache", "gopath", "cache", "ws"} {
		if err := os.Mkdir(filepath.Join(work, d), 0o700); err != nil {
			return "", err
		}
	}
	ws := filepath.Join(work, "ws")
	res := runner.Run(ctx, executor.Spec{Executable: a.cfg.Executable, Args: []string{"-version"}, WorkspaceRoot: ws, Dir: ws, TempParent: work,
		Env:     a.env(filepath.Join(work, "gocache"), filepath.Join(work, "gopath"), filepath.Join(work, "gopath", "pkg", "mod"), filepath.Join(work, "cache")),
		Timeout: probeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	if res.Cleanup == executor.CleanupUncertain {
		return "", errors.New("staticcheck version probe cleanup uncertain")
	}
	if res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 {
		return "", fmt.Errorf("staticcheck version probe did not succeed: %s %s", res.Status, res.Reason)
	}
	v, ok := ParseVersion(res.Stdout.Data)
	if !ok {
		return "", errors.New("staticcheck version output not recognized")
	}
	return v, nil
}

func failed(reason string, limitations ...string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason, Limitations: append([]string{}, limitations...)}
}

// Run executes one staticcheck task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	tasks, err := plan(job)
	if err != nil {
		return failed(analysis.ReasonHandlerError, "plan_failed")
	}
	var pt *pack.Task
	for i := range tasks {
		if tasks[i].Spec.Key == task.Key {
			pt = &tasks[i]
		}
	}
	if pt == nil {
		return failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return failed(analysis.ReasonHandlerError, "invalid_job_paths")
	}
	taskDir := filepath.Join(job.Scratch, "analyzers", pt.Spec.Key)
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		return failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		return failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	dirs := map[string]string{}
	for _, d := range []string{"exec", "gocache", "gopath", "cache", "tmp"} {
		dirs[d] = filepath.Join(taskDir, d)
		if err := os.Mkdir(dirs[d], 0o700); err != nil {
			return failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	modules := govet.ModuleDirs(files)
	in := assembleInput{TaskKey: pt.Spec.Key, ManifestDigest: job.Snapshot.Digest, ModuleDir: pt.Module, Patterns: pt.Patterns, Files: files,
		ScopeGoFiles: govet.GoFilesInScope(files, modules, pt.Module, pt.Patterns)}
	write := func(doc obsdoc.Document) analysis.TaskResult {
		res := analysis.TaskResult{Status: doc.Outcome.TaskStatus, Limitations: doc.Limitations}
		if doc.Outcome.Reason != nil {
			res.Reason = *doc.Outcome.Reason
		}
		art, err := pack.WriteEnvelope(job, doc)
		if err != nil {
			return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Limitations: []string{"observations_write_failed"}}
		}
		res.Artifacts = []analysis.ArtifactFile{art}
		return res
	}

	// go.mod must match the manifest before anything reads it.
	gomod := files[govet.JoinRel(pt.Module, "go.mod")]
	gomodBytes, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, gomod.Path)
	if err != nil || obsdoc.SHA256Hex(gomodBytes) != gomod.SHA256 {
		in.Integrity = true
		return write(assemble(in))
	}
	for p := range files {
		switch {
		case path.Base(p) == "go.work" && underDir(pt.Module, dirOf(p)):
			in.ExtraLimitations = append(in.ExtraLimitations, "go_work_ignored")
		case p == govet.JoinRel(pt.Module, "vendor/modules.txt"):
			in.ExtraLimitations = append(in.ExtraLimitations, "vendor_directory_used")
		}
	}
	goh := a.cfg.Go
	modcache := goh.ModCache()
	if modcache == "" {
		modcache = filepath.Join(dirs["gopath"], "pkg", "mod")
		in.ExtraLimitations = append(in.ExtraLimitations, "no_offline_module_cache")
	}
	workspace := job.Snapshot.WorkspaceDir
	roots := []string{workspace}
	if r, err := filepath.EvalSymlinks(workspace); err == nil && r != workspace {
		roots = append(roots, r)
	}
	in.WorkspaceRoots = roots
	mask := govet.Masker(roots, goh.GoRoot(), goh.ResolvedGo(), modcache, job.Scratch, job.Snapshot.Root)
	toolPaths := []string{a.resolved, a.cfg.Executable}
	in.Mask = func(s string) string {
		for _, p := range toolPaths {
			s = replaceAll(s, p, "$STATICCHECK")
		}
		return mask(s)
	}
	protected := []string{job.Snapshot.SnapshotDir}
	if goh.ModCache() != "" {
		protected = append(protected, goh.ModCache())
	}
	env := a.env(dirs["gocache"], dirs["gopath"], modcache, dirs["cache"])
	spec := func(exe string, args ...string) executor.Spec {
		return executor.Spec{Executable: exe, Args: args, WorkspaceRoot: workspace, Dir: filepath.Join(workspace, filepath.FromSlash(pt.Module)),
			ProtectedRoots: protected, TempParent: dirs["tmp"], Env: env, Timeout: a.cfg.Timeout,
			StdoutLimit: a.cfg.StdoutLimit, StderrLimit: a.cfg.StderrLimit, Redact: govet.RedactForms(roots)}
	}
	goExe := goh.Executable()
	steps := []struct {
		name string
		exe  string
		args []string
		dst  **executor.Result
	}{
		{execGoVersion, goExe, []string{"version"}, &in.GoVersion},
		{execVersion, a.cfg.Executable, []string{"-version"}, &in.Version},
		{execModEdit, goExe, []string{"mod", "edit", "-json"}, &in.ModEdit},
		{execList, goExe, append([]string{"list", "-e", "-json=" + govet.ListFields}, pt.Patterns...), &in.List},
		{execCheck, a.cfg.Executable, CheckArgs(pt.Patterns), &in.Check},
	}
	writeFailed := false
	for _, s := range steps {
		if ctx.Err() != nil {
			in.Cancelled = true
			break
		}
		res := runner.Run(ctx, spec(s.exe, s.args...))
		*s.dst = &res
		e, err := analysis.WriteExecution(dirs["exec"], s.name, res)
		if err != nil {
			writeFailed = true
			break
		}
		in.Executions = append(in.Executions, e)
		if stoppable(&res) {
			break
		}
		// An unidentified go or staticcheck is never run on the code: the
		// parser is pinned to the version's output format.
		if s.name == execGoVersion {
			if _, _, ok := govet.ParseGoVersion(res.Stdout.Data); !ok || !exitedZero(&res) {
				break
			}
		}
		if s.name == execVersion {
			if _, ok := ParseVersion(res.Stdout.Data); !ok || !exitedZero(&res) {
				break
			}
		}
		if s.name == execModEdit {
			_, targets, ok := govet.ParseModEdit(res.Stdout.Data)
			if !ok || !exitedZero(&res) || len(govet.EscapingReplaces(targets, pt.Module)) > 0 {
				break
			}
		}
	}
	in.Drift = govet.WorkspaceDrift(workspace, files, modules, pt.Module)
	snapshotDir := job.Snapshot.SnapshotDir
	in.Lines = func(rel string) (int, string, bool) {
		b, err := govet.ReadSnapshotFile(snapshotDir, rel)
		if err != nil {
			return 0, "", false
		}
		n := bytes.Count(b, []byte{'\n'})
		if len(b) > 0 && b[len(b)-1] != '\n' {
			n++
		}
		return n, obsdoc.SHA256Hex(b), true
	}
	doc := assemble(in)
	if writeFailed {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: in.Executions, Limitations: doc.Limitations}
	}
	res := write(doc)
	res.Executions = in.Executions
	return res
}

// CheckArgs is the staticcheck argv after the executable.
func CheckArgs(patterns []string) []string {
	return append([]string{"-f", "json", "-checks", Checks, "-tests=true"}, patterns...)
}

func stoppable(r *executor.Result) bool {
	return r.Cleanup == executor.CleanupUncertain || r.Status == executor.StatusCancelled || r.Status == executor.StatusTimedOut ||
		r.Status == executor.StatusRejected || r.Status == executor.StatusUnsupported || r.Status == executor.StatusStartFailed
}

func exitedZero(r *executor.Result) bool {
	return r != nil && r.Status == executor.StatusExited && r.ExitCode != nil && *r.ExitCode == 0
}

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

func underDir(p, dir string) bool {
	return dir == "." || p == dir || len(p) > len(dir) && p[:len(dir)+1] == dir+"/"
}
