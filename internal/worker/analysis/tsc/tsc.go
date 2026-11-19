// Package tsc is the TypeScript compiler part of the JS/TS pack
// (docs/ANALYZERS.md): an extra-depth analyzer (javascript.tsc) that type-checks each
// JS/TS module with a tsconfig.json by running the operator's tsc entry
// script through the shared node toolchain, in the attempt workspace copy,
// with --noEmit and no incremental or composite outputs, so nothing is
// written. tsc reads tsconfig as data and never loads the compiler plugins it
// lists (measured, docs/ANALYZERS.md#저장소-코드와-tsconfig-검사): the task executes no repository code.
//
// A snapshot never holds node_modules. A program with an unresolved module or
// type package is therefore the normal case; such a run fails with the gap
// recorded and withholds every diagnostic, because type errors in it can
// follow from the missing types alone (measured, docs/ANALYZERS.md#의존성-공백).
package tsc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
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
	AnalyzerID     = "javascript.tsc"
	Name           = "tsc"
	Tool           = "tsc"
	AdapterVersion = "1"
	// RulePackID/Version name the CODEYE flags below on top of the
	// repository's tsconfig (honored).
	RulePackID      = "codeye-ts-tsc"
	RulePackVersion = "1"
	// MaxReferences bounds the referenced projects of a solution-style
	// tsconfig checked one by one.
	MaxReferences = 8
)

// Execution names inside one task (references add .rNN).
const (
	execVersion = "tsc.version"
	execConfig  = "tsc.config"
	execCheck   = "tsc.check"
)

// Defaults for one execution.
const (
	DefaultTimeout     = 15 * time.Minute
	DefaultStdoutLimit = 32 << 20
	DefaultStderrLimit = 4 << 20
)

// Flags are the CODEYE overrides of the check. --noEmit with incremental and
// composite off writes nothing (measured: --noEmit alone still writes
// tsconfig.tsbuildinfo for incremental or composite projects).
// --ignoreDeprecations 6.0 keeps TypeScript 6 deprecation errors, which stop
// the whole check at the options stage, from hiding the program; the options
// still function in 6.0. The three lint-like checks are off because the
// baseline ESLint reports the same constructs (@typescript-eslint/no-unused-vars,
// no-fallthrough): one construct, one engine (docs/ANALYZERS.md#jsts-규칙-문맥과-억제).
var Flags = []string{"--noEmit", "--pretty", "false", "--incremental", "false", "--composite", "false", "--ignoreDeprecations", "6.0",
	"--noUnusedLocals", "false", "--noUnusedParameters", "false", "--noFallthroughCasesInSwitch", "false"}

// CheckArgs is the check argv after the script for a module-relative
// tsconfig path. The path is always "./"-prefixed: a repository-derived
// argv element never starts with "-" (tsc takes no "--" end of options,
// TS5023, measured).
func CheckArgs(project string) []string {
	if !strings.HasPrefix(project, "./") {
		project = "./" + project
	}
	return append(append([]string{}, Flags...), "-p", project)
}

// ConfigArgs prints the effective configuration of the same command.
func ConfigArgs(project string) []string { return append(CheckArgs(project), "--showConfig") }

// Selection is the recorded rule selection.
var Selection = []string{"--noEmit", "--incremental false", "--composite false", "--ignoreDeprecations 6.0", "--noUnusedLocals false",
	"--noUnusedParameters false", "--noFallthroughCasesInSwitch false", "-p tsconfig.json", "solution references checked one by one"}

// Descriptor is tsc's pack identity: extra depth, run by node.
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Ecosystem: jsts.Ecosystem, Required: false, Tool: Tool, VersionArgs: []string{"--version"}, ParseVersion: ParseVersion,
		Toolchains: []string{"node"}, Interpreter: "node"}
}

// Plan is the tool-independent planner: one task per JS/TS module with a
// tsconfig.json and source in scope, keyed tsc (root), tsc.m01, ... (the
// module numbering the ESLint tasks use). How many files the tsconfig
// selects is known only at run time (FilesInScope nil).
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan(job)
}

func plan(job analysis.Job) ([]pack.Task, error) {
	mods, err := jsts.Modules(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	var out []pack.Task
	for _, m := range mods {
		if !m.TSConfig {
			continue
		}
		out = append(out, pack.Task{Spec: analysis.TaskSpec{Key: jsts.Key(Name, m), Required: false, Title: "tsc -p tsconfig.json in " + m.Dir,
			Stage: domain.StageStaticAnalysis}, Module: m.Dir, Patterns: []string{"tsconfig.json"}})
	}
	return out, nil
}

// Config selects the operator-installed tool.
type Config struct {
	// Script is the absolute tsc entry script (typescript/bin/tsc).
	Script      string
	Node        pack.Toolchain
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Analyzer is the configured tsc adapter.
type Analyzer struct {
	cfg    Config
	script jsts.Script
}

var _ pack.Analyzer = (*Analyzer)(nil)

// New validates the configuration without running anything.
func New(cfg Config) (*Analyzer, error) {
	s, err := jsts.NewScript(cfg.Script, "typescript", cfg.Node)
	if err != nil {
		return nil, err
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
	return &Analyzer{cfg: cfg, script: s}, nil
}

// Descriptor implements pack.Analyzer.
func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor() }

// Plan implements pack.Analyzer.
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}

// Probe runs `<node> tsc --version` under dir.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	out, err := a.script.Probe(ctx, runner, dir, "tsc", "--version")
	if err != nil {
		return "", err
	}
	v, ok := ParseVersion(out)
	if !ok {
		return "", errors.New("tsc version output not recognized")
	}
	return v, nil
}

// Run executes one planned task: version, the effective configuration, then
// the check of the tsconfig, or of each of its local referenced projects
// when it is a solution-style config that selects no file itself.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	mods, err := jsts.Modules(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	tasks, err := plan(job)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	var pt *pack.Task
	for i := range tasks {
		if tasks[i].Spec.Key == task.Key {
			pt = &tasks[i]
		}
	}
	if pt == nil {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	var mod jsts.Module
	for _, m := range mods {
		if m.Dir == pt.Module {
			mod = m
		}
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return pack.Failed(analysis.ReasonHandlerError, "invalid_job_paths")
	}
	taskDir := filepath.Join(job.Scratch, "analyzers", pt.Spec.Key)
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	dirs := map[string]string{}
	for _, d := range []string{"exec", "tmp"} {
		dirs[d] = filepath.Join(taskDir, d)
		if err := os.Mkdir(dirs[d], 0o700); err != nil {
			return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	files := jsts.Included(job.Snapshot.Manifest)
	workspace := job.Snapshot.WorkspaceDir
	roots := jsts.Roots(workspace)
	in := assembleInput{TaskKey: pt.Spec.Key, ManifestDigest: job.Snapshot.Digest, Module: mod, Files: files, NodeVersion: a.cfg.Node.Version,
		ModuleName: jsts.ModuleName(job.Snapshot.SnapshotDir, files, mod.Dir)}
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
	rootConfig := jsts.Join(mod.Dir, "tsconfig.json")
	chain, err := jsts.ReadChain(job.Snapshot.SnapshotDir, files, rootConfig)
	if err != nil {
		in.Integrity = true
		return write(assemble(in))
	}
	in.Chains = []jsts.ConfigChain{chain}
	// The node_modules this attempt's deps.javascript task installed are the
	// module's dependency code; any other node_modules on the way up is not.
	trees, lim, prepared := jsts.PreparedTrees(job, mod.Dir)
	if prepared {
		in.Dependencies = lim
	}
	if in.Policy, in.PolicyOptions = jsts.ChainPolicy(chain, workspace); in.Policy == "" && jsts.ForeignNodeModulesExcept(workspace, mod.Dir, trees) {
		in.Policy = ReasonForeignNodeModules
	}
	if in.Policy != "" {
		return write(assemble(in))
	}
	in.Roots = roots
	in.Mask = jsts.Masker(roots, a.script, filepath.Dir(a.script.Package), job.Scratch, job.Snapshot.Root)
	in.Resolver = &jsts.Resolver{SnapshotDir: job.Snapshot.SnapshotDir, ModuleDir: mod.Dir, Roots: roots, Files: files}
	spec := func(args []string) executor.Spec {
		return executor.Spec{Executable: a.script.Node.Executable, Args: a.script.Argv(args...), WorkspaceRoot: workspace,
			Dir: filepath.Join(workspace, filepath.FromSlash(mod.Dir)), ProtectedRoots: []string{job.Snapshot.SnapshotDir}, TempParent: dirs["tmp"],
			Env: a.script.Env(), Timeout: a.cfg.Timeout, StdoutLimit: a.cfg.StdoutLimit, StderrLimit: a.cfg.StderrLimit, Redact: jsts.RedactForms(roots)}
	}
	writeFailed := false
	// run starts one execution and reports whether the task may go on.
	run := func(name string, args []string) (*executor.Result, bool) {
		if ctx.Err() != nil {
			in.Cancelled = true
			return nil, false
		}
		res := runner.Run(ctx, spec(args))
		e, err := jsts.WriteExecution(dirs["exec"], name, res)
		if err != nil {
			writeFailed = true
			return &res, false
		}
		in.Executions = append(in.Executions, e)
		return &res, !pack.Stoppable(&res)
	}
	var ok bool
	if in.Version, ok = run(execVersion, []string{"--version"}); ok {
		if _, recognized := ParseVersion(in.Version.Stdout.Data); recognized && pack.ExitedZero(in.Version) {
			projects := []project{{Config: rootConfig, Arg: "tsconfig.json"}}
			for i := 0; i < len(projects) && !writeFailed; i++ {
				p := &projects[i]
				suffix := ""
				if p.Ref > 0 {
					suffix = fmt.Sprintf(".r%02d", p.Ref)
				}
				var res *executor.Result
				if res, ok = run(execConfig+suffix, ConfigArgs(p.Arg)); res == nil || !ok {
					p.ConfigRun = res
					break
				}
				p.ConfigRun = res
				shown, parsed := parseShowConfig(res.Stdout.Data)
				if !pack.ExitedZero(res) || !parsed {
					// The config did not load: its stdout holds the
					// diagnostics; no check runs under unknown options.
					break
				}
				p.Shown = shown
				// The effective configuration is checked before tsc
				// reads a source: no write option, no host path.
				w, o, err := jsts.CheckEffective(res.Stdout.Data, path.Dir(p.Config))
				_, outsideRoots := p.rootFiles()
				switch {
				case err != nil:
					in.Policy, in.PolicyOptions = ReasonWriteOption, []string{"show_config_unreadable"}
				case len(w) > 0:
					in.Policy, in.PolicyOptions = ReasonWriteOption, w
				case len(o) > 0:
					in.Policy, in.PolicyOptions = ReasonConfigOutside, o
				case outsideRoots > 0:
					in.Policy = ReasonFilesOutside
				}
				if in.Policy != "" {
					break
				}
				if p.Ref == 0 && (!shown.HasFiles || len(shown.Files) == 0) && len(chain.References) > 0 {
					// Solution-style config: check the referenced
					// projects of this module one by one.
					in.Solution = true
					n := 0
					for _, r := range chain.References {
						if _, known := files[r]; !known || !mod.Owns(r) {
							in.ForeignReferences++
							continue
						}
						if n == MaxReferences {
							in.ReferencesDropped++
							continue
						}
						n++
						rc, err := jsts.ReadChain(job.Snapshot.SnapshotDir, files, r)
						if err != nil {
							in.Integrity = true
							return write(assemble(in))
						}
						if in.Policy, in.PolicyOptions = jsts.ChainPolicy(rc, workspace); in.Policy != "" {
							return write(assemble(in))
						}
						in.Chains = append(in.Chains, rc)
						projects = append(projects, project{Config: r, Arg: jsts.Rel(r, mod.Dir), Ref: n})
					}
					continue
				}
				if p.CheckRun, ok = run(execCheck+suffix, CheckArgs(p.Arg)); !ok {
					break
				}
			}
			in.Projects = projects
		}
	}
	var watched []string
	watched = append(watched, mod.Files...)
	for _, c := range in.Chains {
		for _, f := range c.Files {
			watched = append(watched, f.Path)
		}
	}
	in.Drift = jsts.Drift(workspace, files, watched)
	in.Resolver.Drift = in.Drift
	doc := assemble(in)
	if writeFailed {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: in.Executions, Limitations: doc.Limitations}
	}
	res := write(doc)
	res.Executions = in.Executions
	return res
}

// project is one tsconfig the task loads: the module's own, or a local
// referenced project of a solution-style config (Ref > 0).
type project struct {
	Config    string // snapshot path
	Arg       string // module-relative -p argument
	Ref       int
	ConfigRun *executor.Result
	CheckRun  *executor.Result
	Shown     shownConfig
}

// rootFiles maps the shown root files (relative to the config directory) to
// snapshot paths; false for one outside the snapshot.
func (p project) rootFiles() ([]string, int) {
	dir := path.Dir(p.Config)
	var out []string
	outside := 0
	for _, f := range p.Shown.Files {
		j := path.Clean(path.Join(dir, f))
		if j == ".." || len(j) >= 3 && j[:3] == "../" || path.IsAbs(j) {
			outside++
			continue
		}
		out = append(out, j)
	}
	return out, outside
}
