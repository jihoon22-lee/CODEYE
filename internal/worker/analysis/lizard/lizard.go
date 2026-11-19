// Package lizard is the complexity adapter (docs/ANALYZERS.md). It runs the
// operator-installed lizard natively through the executor runner in the attempt
// workspace copy over the planned source files and records functions whose
// cyclomatic complexity reaches the rule pack threshold as metric
// observations: maintainability information, never findings or defects.
// lizard owns complexity only; duplication is jscpd's (its -Eduplicate
// extension is never enabled).
package lizard

import (
	"context"
	"errors"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Identity.
const (
	AnalyzerID     = "any.lizard"
	Name           = "lizard"
	Tool           = "lizard"
	AdapterVersion = "1"
	ParserID       = "lizard-xml"
	ParserVersion  = "1"
	// RulePackID/Version name the CODEYE selection below.
	RulePackID      = "codeye-structure-lizard"
	RulePackVersion = "1"
	// TestedSeries is the lizard release series the parser is pinned to.
	TestedSeries = "1.26"
)

// CCNThreshold is the inclusive cyclomatic complexity a function must reach
// to be recorded: 16, i.e. lizard's own default warning "CCN > 15". It is a
// maintainability signal, not evidence of a defect.
const CCNThreshold = 16

// Metric and rule of the recorded observations.
const (
	RuleCCN   = "cyclomatic_complexity"
	MetricCCN = "cyclomatic_complexity"
)

// Selection is the rule-pack part of the argv and the threshold CODEYE
// applies. --xml is the machine format (cppncss: bare function names with
// NCSS and CCN, and every analyzed file); the CSV format is not used because
// its long_name column copies parameter lists, default values included.
// --no-gitignore keeps a repository .gitignore from changing the input. In
// XML mode lizard exits 0 whatever its own thresholds say (measured with
// 1.26.1), so CODEYE applies CCNThreshold to the measured values.
var Selection = []string{"--xml", "--no-gitignore", "cyclomatic_complexity>=16"}

// Extensions are the source extensions lizard 1.26 reads among CODEYE's
// languages (measured with lizard_languages.get_reader_for): C/C++, Python,
// JS/TS, Go and Rust. .c++ .hxx .h++ .pyi .mts .cts have no reader.
var Extensions = map[string]bool{
	".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".h": true, ".hh": true, ".hpp": true,
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".jsx": true, ".ts": true, ".tsx": true,
	".go": true, ".rs": true,
}

// Execution names inside the task, in run order.
const (
	execVersion = "lizard.version"
	execRun     = "lizard.run"
)

var versionPattern = regexp.MustCompile(`^([0-9]{1,4})\.([0-9]{1,4})\.([0-9]{1,4})$`)

// ParseVersion reads `lizard --version` stdout ("1.26.1\n").
func ParseVersion(stdout []byte) (string, bool) {
	v := strings.TrimSpace(string(stdout))
	if strings.Count(string(stdout), "\n") > 1 || !versionPattern.MatchString(v) {
		return "", false
	}
	return v, true
}

// VersionTested reports the pinned series.
func VersionTested(v string) bool { return anypack.SeriesTested(v, TestedSeries) }

// Descriptor is lizard's pack identity: an extra-depth analyzer of every
// supported language (ecosystem any).
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Ecosystem: "any", Required: false, Tool: Tool, VersionArgs: []string{"--version"}, ParseVersion: ParseVersion}
}

// Eligible reports a file lizard measures: a supported extension.
func Eligible(p string) bool { return Extensions[strings.ToLower(path.Ext(p))] }

// Plan is the tool-independent planner: one task "lizard" over the
// supported source files in scope outside data directories.
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return anypack.Plan(job, Descriptor(), "structure: cyclomatic complexity (lizard)", anypack.Files(job, Eligible, true)), nil
}

// Config selects the operator-installed lizard. Nothing is searched in PATH.
type Config struct {
	Executable  string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Analyzer is the configured lizard adapter.
type Analyzer struct {
	cfg  Config
	tool *anypack.Tool
}

var _ pack.Analyzer = (*Analyzer)(nil)

// New validates the configuration without running anything.
func New(cfg Config) (*Analyzer, error) {
	t, err := anypack.NewTool(cfg.Executable, nil)
	if err != nil {
		return nil, errors.New("lizard: " + err.Error())
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = anypack.DefaultTimeout
	}
	if cfg.StdoutLimit <= 0 {
		cfg.StdoutLimit = anypack.DefaultStdoutLimit
	}
	if cfg.StderrLimit <= 0 {
		cfg.StderrLimit = anypack.DefaultStderrLimit
	}
	return &Analyzer{cfg: cfg, tool: t}, nil
}

// Descriptor implements pack.Analyzer.
func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor() }

// Plan implements pack.Analyzer.
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}

// Probe runs `lizard --version` outside any source tree.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.tool.Probe(ctx, runner, dir, []string{"--version"}, ParseVersion)
}

// env: lizard is a Python script with an absolute interpreter line; the
// Python variables keep the run independent of the operator's environment
// and stop it from writing bytecode next to the tool.
func (a *Analyzer) env() map[string]string {
	return a.tool.Env(map[string]string{"PYTHONDONTWRITEBYTECODE": "1", "PYTHONNOUSERSITE": "1", "PYTHONIOENCODING": "utf-8"})
}

// RunArgs is the lizard argv after the executable: the selection and the
// CODEYE-written file list (scratch, outside the snapshot). Explicitly
// listed files are analyzed as given (no duplicate-content filter, no
// .gitignore). A repository whitelizard.txt only filters lizard's warning
// output, which XML mode does not print (measured).
func RunArgs(list string) []string {
	return []string{"--xml", "--no-gitignore", "--input_file", list}
}

// Run executes the lizard task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	r, failed := anypack.Start(ctx, job, task.Key, Plan)
	if failed != nil {
		return *failed
	}
	files := anypack.Files(job, Eligible, true)
	in := assembleInput{TaskKey: r.Task.Spec.Key, ManifestDigest: job.Snapshot.Digest, Patterns: r.Task.Patterns, Files: files, Excluded: anypack.ExcludedDataDirFiles(job, Eligible)}
	var list strings.Builder
	for _, f := range files {
		list.WriteString(f.Path + "\n")
	}
	listPath, err := r.WriteFile("files.txt", []byte(list.String()))
	if err != nil {
		in.ScratchFailed = true
	}
	// lizard reads the listed files only; a link on the way would lead out
	// of the copy, so none may be there.
	in.Linked = !anypack.LinkFreeFiles(r.Workspace, files)
	if !in.ScratchFailed && !in.Linked {
		env := a.env()
		in.Version = r.Exec(ctx, runner, execVersion, r.Spec(a.tool, env, anypack.ProbeTimeout, 64<<10, 64<<10, "--version"))
		if !anypack.Stoppable(in.Version) && anypack.ExitedZero(in.Version) {
			if _, ok := ParseVersion(in.Version.Stdout.Data); ok {
				in.Run = r.Exec(ctx, runner, execRun, r.Spec(a.tool, env, a.cfg.Timeout, a.cfg.StdoutLimit, a.cfg.StderrLimit, RunArgs(listPath)...))
			}
		}
	}
	in.Cancelled = r.Cancelled
	in.Executions = r.Executions
	in.Drift = anypack.Drift(r.Workspace, files)
	in.Resolver = anypack.NewResolver(job.Snapshot.SnapshotDir, files, in.Drift)
	return r.Finish(assemble(in))
}
