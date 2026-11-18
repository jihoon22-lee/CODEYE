// Package eslint is the ESLint part of the W18 JS/TS pack (docs/43). It has
// two analyzers on the one operator tool (--eslint):
//
//   - javascript.eslint, the ecosystem's baseline (required): syntactic
//     rules on every JS/TS module. It needs no dependency, no tsconfig and
//     no type information, so it can finish honestly on a snapshot, which
//     never holds node_modules.
//   - javascript.eslint_typed, extra depth: typescript-eslint's type-aware
//     rules (promises, any at boundaries) on TS modules with a tsconfig.json.
//     A missing dependency makes type information incomplete; the run then
//     fails with the gap recorded and withholds every type-aware diagnostic
//     (measured: they follow from the missing types, docs/43 §4).
//
// Both run the operator's eslint entry script through the shared node
// toolchain with a CODEYE-managed configuration in task scratch,
// --no-config-lookup and --no-inline-config: no repository JavaScript
// (eslint.config.*, plugins, node_modules) is loaded, and no task executes
// repository code.
package eslint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Identity.
const (
	AnalyzerID      = "javascript.eslint"
	Name            = "eslint"
	TypedAnalyzerID = "javascript.eslint_typed"
	TypedName       = "eslint-typed"
	Tool            = "eslint"
	AdapterVersion  = "1"
	// RulePackID/Version name the baseline selection (baselineConfig).
	RulePackID      = "codeye-js-eslint"
	RulePackVersion = "1"
	// TypedRulePackID/Version name the type-aware selection (typedConfig).
	TypedRulePackID      = "codeye-ts-eslint-typed"
	TypedRulePackVersion = "1"
)

// Plugin packages loaded by the managed configuration from the directory
// that holds the eslint package (the operator's tool node_modules), and the
// release series the rule selection was measured with.
const (
	pkgESLintJS   = "@eslint/js"
	pkgTSESLint   = "typescript-eslint"
	pkgTypeScript = "typescript"
)

var testedPlugins = map[string]string{pkgESLintJS: "10.0.", pkgTSESLint: "8.73.", pkgTypeScript: "6.0."}

// Execution names inside one task, in run order.
const (
	execVersion = "eslint.version"
	execCheck   = "eslint.check"
)

// Defaults for one execution.
const (
	DefaultTimeout     = 15 * time.Minute
	DefaultStdoutLimit = 64 << 20
	DefaultStderrLimit = 4 << 20
)

func descriptor(id, name string, required bool) pack.Descriptor {
	return pack.Descriptor{ID: id, Name: name, Ecosystem: jsts.Ecosystem, Required: required, Tool: Tool, VersionArgs: []string{"--version"}, ParseVersion: ParseVersion,
		Toolchains: []string{"node"}, Interpreter: "node"}
}

// Descriptor is the baseline analyzer's identity: required, run by node.
func Descriptor() pack.Descriptor { return descriptor(AnalyzerID, Name, true) }

// TypedDescriptor is the type-aware analyzer's identity: extra depth.
func TypedDescriptor() pack.Descriptor { return descriptor(TypedAnalyzerID, TypedName, false) }

// Plan is the baseline planner: one task per JS/TS module with source in
// scope, keyed eslint (root), eslint.m01, ... A snapshot without JS/TS
// source plans nothing.
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan(job, false)
}

// TypedPlan is the type-aware planner: one task per module with a
// tsconfig.json and TypeScript source in scope, keyed eslint-typed, ...
func TypedPlan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan(job, true)
}

func plan(job analysis.Job, typed bool) ([]pack.Task, error) {
	mods, err := jsts.Modules(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	name, title := Name, "eslint "
	if typed {
		name, title = TypedName, "eslint type-aware "
	}
	var out []pack.Task
	for _, m := range mods {
		n := len(m.Files)
		if typed {
			if !m.TSConfig || len(m.TSFiles) == 0 {
				continue
			}
			n = len(m.TSFiles)
		}
		out = append(out, pack.Task{Spec: analysis.TaskSpec{Key: jsts.Key(name, m), Required: !typed, Title: title + strings.Join(m.Patterns, " ") + " in " + m.Dir,
			Stage: domain.StageStaticAnalysis}, Module: m.Dir, Patterns: append([]string{}, m.Patterns...), FilesInScope: &n})
	}
	return out, nil
}

// Config selects the operator-installed tool. Nothing is searched in PATH
// and nothing is downloaded.
type Config struct {
	// Script is the absolute eslint entry script (eslint/bin/eslint.js).
	Script string
	// Node is the validated shared node toolchain.
	Node        pack.Toolchain
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// plugins are the packages the managed configuration loads.
type plugins struct {
	dir                            string // the node_modules holding eslint
	eslintJS, tseslint, typescript jsts.PackageInfo
}

// Analyzer is a configured ESLint adapter (baseline or type-aware).
type Analyzer struct {
	typed   bool
	cfg     Config
	script  jsts.Script
	plugins plugins
}

var _ pack.Analyzer = (*Analyzer)(nil)

func newAnalyzer(cfg Config, typed bool) (*Analyzer, error) {
	s, err := jsts.NewScript(cfg.Script, "eslint", cfg.Node)
	if err != nil {
		return nil, err
	}
	p := plugins{dir: filepath.Dir(s.Package)}
	for _, x := range []struct {
		name string
		dst  *jsts.PackageInfo
	}{{pkgESLintJS, &p.eslintJS}, {pkgTSESLint, &p.tseslint}, {pkgTypeScript, &p.typescript}} {
		info, ok := jsts.ReadPackage(filepath.Join(p.dir, filepath.FromSlash(x.name)))
		if !ok || info.Name != x.name || info.Entry == "" {
			return nil, errors.New("eslint needs " + x.name + " installed beside it in the same node_modules")
		}
		*x.dst = info
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
	return &Analyzer{typed: typed, cfg: cfg, script: s, plugins: p}, nil
}

// New validates the baseline configuration without running anything.
func New(cfg Config) (*Analyzer, error) { return newAnalyzer(cfg, false) }

// NewTyped validates the type-aware configuration without running anything.
func NewTyped(cfg Config) (*Analyzer, error) { return newAnalyzer(cfg, true) }

// Descriptor implements pack.Analyzer.
func (a *Analyzer) Descriptor() pack.Descriptor {
	if a.typed {
		return TypedDescriptor()
	}
	return Descriptor()
}

// Plan implements pack.Analyzer.
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if a.typed {
		return TypedPlan(ctx, job)
	}
	return Plan(ctx, job)
}

// Probe runs `<node> eslint.js --version` under dir; an unrecognized version
// is a failed probe (the parser is pinned to a release format).
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	out, err := a.script.Probe(ctx, runner, dir, "eslint", "--version")
	if err != nil {
		return "", err
	}
	v, ok := ParseVersion(out)
	if !ok {
		return "", errors.New("eslint version output not recognized")
	}
	return v, nil
}

func failed(reason string, limitations ...string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason, Limitations: append([]string{}, limitations...)}
}

// Selection is the recorded rule selection of a task: rule set names with
// the measured plugin versions (a plugin upgrade changes the rule context)
// and the flags that turn off repository configuration and in-source
// directives.
func (a *Analyzer) Selection() []string {
	p := a.plugins
	if a.typed {
		return []string{pkgTSESLint + "@" + p.tseslint.Version + " recommended-type-checked-only (TS files, without eslint-recommended)",
			"codeye/program-diagnostics", "parser " + pkgTypeScript + "@" + p.typescript.Version, "parserOptions.project module tsconfig.json",
			"--no-config-lookup", "--no-inline-config", "--suppressions-location empty"}
	}
	return []string{pkgESLintJS + "@" + p.eslintJS.Version + " recommended (JS and TS files)", pkgTSESLint + "@" + p.tseslint.Version + " recommended (TS files)",
		"no-undef off", "parser " + pkgTypeScript + "@" + p.typescript.Version, "jsx in JS files", "--no-config-lookup", "--no-inline-config", "--suppressions-location empty"}
}

// CheckArgs is the lint argv after the script: the managed configuration and
// an empty suppressions file in task scratch, no config lookup, no inline
// configuration, json to stdout (the envelope binds observations to the
// recorded stdout stream), then the module-relative paths.
func CheckArgs(configPath, suppressionsPath string, patterns []string) []string {
	args := []string{"--no-config-lookup", "-c", configPath, "--no-inline-config", "--suppressions-location", suppressionsPath,
		"--no-warn-ignored", "--no-error-on-unmatched-pattern", "--format", "json"}
	for _, p := range patterns {
		if strings.HasPrefix(p, "-") {
			p = "./" + p
		}
		args = append(args, p)
	}
	return args
}

// Run executes one planned task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	mods, err := jsts.Modules(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return failed(analysis.ReasonHandlerError, "plan_failed")
	}
	tasks, err := plan(job, a.typed)
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
	var mod jsts.Module
	for _, m := range mods {
		if m.Dir == pt.Module {
			mod = m
		}
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
	for _, d := range []string{"exec", "cfg", "tmp"} {
		dirs[d] = filepath.Join(taskDir, d)
		if err := os.Mkdir(dirs[d], 0o700); err != nil {
			return failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	files := jsts.Included(job.Snapshot.Manifest)
	workspace := job.Snapshot.WorkspaceDir
	roots := jsts.Roots(workspace)
	moduleAbs := filepath.Join(workspace, filepath.FromSlash(mod.Dir))
	in := assembleInput{Typed: a.typed, TaskKey: pt.Spec.Key, ManifestDigest: job.Snapshot.Digest, Module: mod, Patterns: pt.Patterns, Files: files,
		Selection: a.Selection(), Plugins: a.plugins, NodeVersion: a.cfg.Node.Version, ModuleName: jsts.ModuleName(job.Snapshot.SnapshotDir, files, mod.Dir)}
	in.Expected = mod.Files
	if a.typed {
		in.Expected = mod.TSFiles
	}
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
	watched := append([]string{}, in.Expected...)
	if a.typed {
		cfgPath := jsts.Join(mod.Dir, "tsconfig.json")
		chain, err := jsts.ReadChain(job.Snapshot.SnapshotDir, files, cfgPath)
		if err != nil {
			in.Integrity = true
			return write(assemble(in))
		}
		in.Chain = chain
		for _, f := range chain.Files {
			watched = append(watched, f.Path)
		}
		switch {
		case chain.Outside:
			in.Policy = ReasonConfigOutside
			return write(assemble(in))
		case jsts.ForeignNodeModules(workspace, mod.Dir):
			in.Policy = ReasonForeignNodeModules
			return write(assemble(in))
		}
	}
	in.Roots = roots
	in.Mask = jsts.Masker(roots, a.script, a.plugins.dir, job.Scratch, job.Snapshot.Root)
	configPath := filepath.Join(dirs["cfg"], "eslint.config.cjs")
	body := baselineConfig(a.plugins, mod)
	if a.typed {
		body = typedConfig(a.plugins, mod, moduleAbs)
	}
	suppressions := filepath.Join(dirs["cfg"], "eslint-suppressions.json")
	if analysis.WriteFileExclusive(configPath, []byte(body)) != nil || analysis.WriteFileExclusive(suppressions, []byte("{}\n")) != nil {
		return failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	protected := []string{job.Snapshot.SnapshotDir}
	spec := func(args []string) executor.Spec {
		return executor.Spec{Executable: a.script.Node.Executable, Args: a.script.Argv(args...), WorkspaceRoot: workspace, Dir: moduleAbs,
			ProtectedRoots: protected, TempParent: dirs["tmp"], Env: a.script.Env(), Timeout: a.cfg.Timeout,
			StdoutLimit: a.cfg.StdoutLimit, StderrLimit: a.cfg.StderrLimit, Redact: jsts.RedactForms(roots)}
	}
	steps := []struct {
		name string
		args []string
		dst  **executor.Result
	}{
		{execVersion, []string{"--version"}, &in.Version},
		{execCheck, CheckArgs(configPath, suppressions, pt.Patterns), &in.Check},
	}
	writeFailed := false
	for _, s := range steps {
		if ctx.Err() != nil {
			in.Cancelled = true
			break
		}
		res := runner.Run(ctx, spec(s.args))
		*s.dst = &res
		e, err := jsts.WriteExecution(dirs["exec"], s.name, res)
		if err != nil {
			writeFailed = true
			break
		}
		in.Executions = append(in.Executions, e)
		if jsts.Stoppable(&res) {
			break
		}
		// An unidentified eslint is never run on the code: the parser is
		// pinned to the release's output format.
		if s.name == execVersion {
			if _, ok := ParseVersion(res.Stdout.Data); !ok || !jsts.ExitedZero(&res) {
				break
			}
		}
	}
	in.Drift = jsts.Drift(workspace, files, watched)
	in.Resolver = &jsts.Resolver{SnapshotDir: job.Snapshot.SnapshotDir, ModuleDir: mod.Dir, Roots: roots, Files: files, Drift: in.Drift}
	doc := assemble(in)
	if writeFailed {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: in.Executions, Limitations: doc.Limitations}
	}
	res := write(doc)
	res.Executions = in.Executions
	return res
}
