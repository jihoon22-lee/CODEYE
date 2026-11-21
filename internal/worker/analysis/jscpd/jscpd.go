// Package jscpd is the duplication adapter (docs/ANALYZERS.md). It runs the
// operator-installed jscpd entry script with the shared node toolchain
// (<node> <run-jscpd.js> ..., PATH = the node directory only; docs/ANALYZERS.md#공유-interpreter-toolchain)
// in the attempt workspace copy over the planned source files and
// records each duplicated block that reaches the rule pack thresholds as a
// metric observation: maintainability information, never a finding or a
// defect. jscpd owns duplication only: its complexity, dead-code, semantic,
// history and git modes are never enabled, and complexity is lizard's.
package jscpd

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Identity.
const (
	AnalyzerID      = "any.jscpd"
	Name            = "jscpd"
	Tool            = "jscpd"
	AdapterVersion  = "1"
	ParserID        = "jscpd-xcode"
	ParserVersion   = "1"
	RulePackID      = "codeye-duplication-jscpd"
	RulePackVersion = "2"
	TestedSeries    = "5.4"
	// Interpreter is the shared toolchain that runs the entry script.
	Interpreter = "node"
)

// Thresholds of rule pack v1: a block is a clone when it has at least
// MinTokens tokens and MinLines lines in both places (jscpd's own defaults
// are 50 tokens and 5 lines). Duplication is a maintainability signal, not
// evidence of a defect.
const (
	MinLines  = 10
	MinTokens = 100
	Mode      = "mild"
)

// MaxFileBytes: jscpd silently skips files over its 1 MiB default size
// limit and files that are not valid UTF-8 (measured with 5.4.3), so CODEYE
// never hands it such a file and records them as skipped instead.
const MaxFileBytes = 1_000_000

// Rule and metric of the recorded observations.
const (
	RuleDuplicate   = "duplicated_code"
	MetricDuplicate = "duplicated_lines"
)

// Selection is the rule pack: the settings of the CODEYE-written --config
// file that replaces any repository .jscpd.json, .config/jscpd.json or
// package.json "jscpd" key (measured: none of them is read with --config).
var Selection = []string{"minLines=10", "minTokens=100", "mode=mild", "noGitignore=true", "reporters=xcode"}

// Execution names inside the task, in run order.
const (
	execVersion = "jscpd.version"
	execRun     = "jscpd.run"
)

var versionPattern = regexp.MustCompile(`^jscpd ([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4})$`)

// ParseVersion reads `jscpd --version` stdout ("jscpd 5.4.3\n").
func ParseVersion(stdout []byte) (string, bool) {
	if strings.Count(string(stdout), "\n") > 1 {
		return "", false
	}
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// VersionTested reports the pinned series.
func VersionTested(v string) bool { return pack.SeriesTested(v, TestedSeries) }

// Descriptor is jscpd's pack identity: an extra-depth analyzer of every
// supported language, run by the node toolchain.
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Ecosystem: "any", Required: false, Tool: Tool, VersionArgs: []string{"--version"}, ParseVersion: ParseVersion,
		Toolchains: []string{Interpreter}, Interpreter: Interpreter, Purpose: obsdoc.PurposeMetrics}
}

// Extensions are CODEYE's language source extensions; jscpd 5.4.3 maps
// each to a format (measured).
var Extensions = map[string]bool{
	".go": true, ".py": true, ".pyi": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".rs": true, ".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".c++": true, ".h": true, ".hh": true, ".hpp": true, ".hxx": true, ".h++": true,
}

// Eligible reports a file jscpd compares.
func Eligible(p string) bool { return Extensions[strings.ToLower(path.Ext(p))] }

// Plan is the tool-independent planner: one task "jscpd" over the source
// files in scope outside data directories.
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, _ := anypack.StructureFiles(job, Eligible)
	return anypack.Plan(job, Descriptor(), "structure: duplicated code (jscpd)", files), nil
}

// Config selects the jscpd entry script (run-jscpd.js) and the validated
// node toolchain handle.
type Config struct {
	Executable  string
	Node        pack.Toolchain
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Analyzer is the configured jscpd adapter.
type Analyzer struct {
	cfg  Config
	tool *anypack.Tool
}

var _ pack.Analyzer = (*Analyzer)(nil)

// New validates the configuration without running anything. The tool path
// must be the package's JavaScript entry script, never a shell or .cmd shim.
func New(cfg Config) (*Analyzer, error) {
	if cfg.Node.Name != Interpreter || cfg.Node.Executable == "" {
		return nil, errors.New("jscpd needs the validated node toolchain")
	}
	if ext := strings.ToLower(filepath.Ext(cfg.Executable)); ext != ".js" && ext != ".cjs" && ext != ".mjs" {
		return nil, errors.New("jscpd tool path must be its JavaScript entry script (run-jscpd.js)")
	}
	node := cfg.Node
	t, err := anypack.NewTool(cfg.Executable, &node)
	if err != nil {
		return nil, errors.New("jscpd: " + err.Error())
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

// Probe runs `<node> run-jscpd.js --version` outside any source tree.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.tool.Probe(ctx, runner, dir, []string{"--version"}, ParseVersion)
}

// env: JSCPD_NO_TIPS silences promotional output; the cache directories
// stay in task scratch (only the never-enabled semantic mode uses them).
func (a *Analyzer) env(cache string) map[string]string {
	return a.tool.Env(map[string]string{"JSCPD_NO_TIPS": "1", "JSCPD_CACHE_DIR": cache, "XDG_CACHE_HOME": cache})
}

// config is the CODEYE-written jscpd configuration (task scratch).
type config struct {
	Path        []string `json:"path"`
	MinLines    int      `json:"minLines"`
	MinTokens   int      `json:"minTokens"`
	Mode        string   `json:"mode"`
	Reporters   []string `json:"reporters"`
	Output      string   `json:"output"`
	Absolute    bool     `json:"absolute"`
	NoGitignore bool     `json:"noGitignore"`
	NoTips      bool     `json:"noTips"`
	NoColors    bool     `json:"noColors"`
}

// ConfigFile renders the configuration for absolute file paths.
func ConfigFile(files []string, output string) ([]byte, error) {
	return json.Marshal(config{Path: files, MinLines: MinLines, MinTokens: MinTokens, Mode: Mode, Reporters: []string{"xcode"}, Output: output,
		Absolute: true, NoGitignore: true, NoTips: true, NoColors: true})
}

// RunArgs is the argv after the entry script.
func RunArgs(configPath string) []string { return []string{"--config", configPath} }

// Run executes the jscpd task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	r, failed := anypack.Start(ctx, job, task.Key, Plan)
	if failed != nil {
		return *failed
	}
	files, thirdParty := anypack.StructureFiles(job, Eligible)
	in := assembleInput{TaskKey: r.Task.Spec.Key, ManifestDigest: job.Snapshot.Digest, Patterns: r.Task.Patterns, Files: files, Skipped: map[string]string{},
		Excluded: anypack.ExcludedDataDirFiles(job, Eligible), ThirdParty: thirdParty}
	var abs []string
	for _, f := range files {
		switch {
		case f.Size != nil && *f.Size > MaxFileBytes:
			in.Skipped[f.Path] = SkipTooLarge
			continue
		}
		switch anypack.ClassifyText(job.Snapshot.SnapshotDir, f) {
		case anypack.TextNotUTF8:
			in.Skipped[f.Path] = SkipNotUTF8
			continue
		case anypack.TextUnreadable:
			in.Integrity = true
		}
		in.Listed = append(in.Listed, f.Path)
		abs = append(abs, filepath.Join(r.Workspace, filepath.FromSlash(f.Path)))
	}
	out, err := r.Mkdir("out")
	if err != nil {
		in.ScratchFailed = true
	}
	cache, err := r.Mkdir("cache")
	if err != nil {
		in.ScratchFailed = true
	}
	// jscpd reads the listed files only; a link on the way would lead out
	// of the copy, so none may be there.
	in.Linked = !anypack.LinkFreeFiles(r.Workspace, files)
	var cfgPath string
	if !in.ScratchFailed && !in.Integrity && !in.Linked && len(abs) > 0 {
		body, err := ConfigFile(abs, out)
		if err == nil {
			cfgPath, err = r.WriteFile("jscpd.json", body)
		}
		if err != nil {
			in.ScratchFailed = true
		}
	}
	if !in.ScratchFailed && !in.Integrity && !in.Linked && len(abs) > 0 {
		env := a.env(cache)
		in.Version = r.Exec(ctx, runner, execVersion, r.Spec(a.tool, env, anypack.ProbeTimeout, 64<<10, 64<<10, "--version"))
		if !pack.Stoppable(in.Version) && pack.ExitedZero(in.Version) {
			if _, ok := ParseVersion(in.Version.Stdout.Data); ok {
				in.Run = r.Exec(ctx, runner, execRun, r.Spec(a.tool, env, a.cfg.Timeout, a.cfg.StdoutLimit, a.cfg.StderrLimit, RunArgs(cfgPath)...))
			}
		}
	}
	in.Cancelled = r.Cancelled
	in.Executions = r.Executions
	in.Drift = anypack.Drift(r.Workspace, files)
	in.Resolver = anypack.NewResolver(job.Snapshot.SnapshotDir, files, in.Drift)
	return r.Finish(assemble(in))
}

// Skip reasons (coverage unit states of files CODEYE did not hand to jscpd).
const (
	SkipTooLarge = "skipped_too_large"
	SkipNotUTF8  = "skipped_not_utf8"
)
