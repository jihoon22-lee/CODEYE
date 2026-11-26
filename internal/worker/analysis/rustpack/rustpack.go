// Package rustpack is the Rust pack: cargo check (the baseline analyzer)
// and the optional cargo clippy and cargo doc checks run natively through the executor runner in the
// attempt workspace copy, offline, with the real toolchain binaries the
// operator named (never a rustup proxy, never PATH), and record the result in
// the generic observations envelope (obsdoc). All three can execute repository code
// (build scripts, proc macros, repository Cargo configuration), so every task
// is pack.Task.RepositoryCode=true and runs only under the node operator's
// repository-code grant (docs/ANALYZERS.md#저장소-코드-실행-허가, docs/ANALYZERS.md).
package rustpack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Identity.
const (
	CheckAnalyzerID  = "rust.cargocheck"
	CheckName        = "cargo-check"
	CheckTool        = "cargo"
	ClippyAnalyzerID = "rust.clippy"
	ClippyName       = "clippy"
	ClippyTool       = "clippy"
	AdapterVersion   = "1"
)

// Defaults for one execution.
const (
	DefaultTimeout     = 30 * time.Minute
	DefaultStdoutLimit = 64 << 20
	DefaultStderrLimit = 4 << 20
	probeTimeout       = 30 * time.Second
	maxDepInfoFiles    = 20000
	maxDepInfoBytes    = 128 << 20
)

// CheckDescriptor is cargo check's pack identity: the baseline (required)
// Rust analyzer. cargo is the rustc front door of every Rust project, so it is
// the tool a Rust node can be expected to have; clippy is a rustup component
// that may be missing.
func CheckDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: CheckAnalyzerID, Name: CheckName, Ecosystem: "rust", Required: true, Tool: CheckTool, VersionArgs: []string{"--version"}, ParseVersion: ParseCargoVersion}
}

// DocDescriptor checks documentation links in the active workspace build.
func DocDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: "rust.rustdoc", Name: "cargo-doc", Ecosystem: "rust", Tool: CheckTool, VersionArgs: []string{"--version"}, ParseVersion: ParseCargoVersion}
}

func DocPlan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return planTasks(ctx, KindDoc, job)
}

// ClippyDescriptor is clippy's pack identity: an extra-depth analyzer.
func ClippyDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: ClippyAnalyzerID, Name: ClippyName, Ecosystem: "rust", Required: false, Tool: ClippyTool, VersionArgs: []string{"--version"}, ParseVersion: ParseClippyVersion}
}

func (k Kind) descriptor() pack.Descriptor {
	if k == KindDoc {
		return DocDescriptor()
	}
	if k == KindClippy {
		return ClippyDescriptor()
	}
	return CheckDescriptor()
}

func (k Kind) title() string {
	if k == KindDoc {
		return "cargo doc"
	}
	if k == KindClippy {
		return "cargo clippy"
	}
	return "cargo check"
}

// CheckPlan and ClippyPlan are the tool-independent planners.
func CheckPlan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return planTasks(ctx, KindCheck, job)
}

func ClippyPlan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return planTasks(ctx, KindClippy, job)
}

func planTasks(ctx context.Context, k Kind, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := k.descriptor()
	pr, err := planModules(job.Snapshot.Manifest, job.Scope, d.Name, k.title())
	if err != nil {
		return nil, err
	}
	if pr.Placeholder != "" {
		if k != KindCheck {
			return nil, nil // the baseline analyzer shows the gap once
		}
		n := pack.SourceFiles(job.Snapshot.Manifest, job.Scope)["rust"]
		return []pack.Task{{Spec: analysis.TaskSpec{Key: d.Name, Required: true, Title: k.title() + " (" + pr.Placeholder + ")", Stage: domain.StageStaticAnalysis},
			Module: ".", Patterns: []string{}, FilesInScope: &n}}, nil
	}
	out := make([]pack.Task, 0, len(pr.Modules))
	for _, m := range pr.Modules {
		n := len(m.Files)
		out = append(out, pack.Task{Spec: analysis.TaskSpec{Key: m.Key, Required: d.Required, Title: m.Title, Stage: domain.StageStaticAnalysis},
			Module: m.Dir, Patterns: m.Patterns, FilesInScope: &n, RepositoryCode: true})
	}
	return out, nil
}

// Config selects the operator-installed tools. Nothing is searched in PATH
// and nothing is downloaded.
type Config struct {
	// Cargo is the absolute cargo binary of a toolchain's bin directory (not
	// the rustup proxy). rustc is the binary next to it.
	Cargo string
	// Clippy is the absolute cargo-clippy binary of the same bin directory
	// (clippy analyzer only).
	Clippy string
	// ToolPath is the operator's --toolchain-path list. It is searched once,
	// at construction, for the system linker `cc` that rustc needs to link
	// build scripts and proc macros; the absolute result is passed as the
	// target linker. The cargo runs (repository code under the grant) also
	// get its directories on PATH after the toolchain bin directory: build
	// scripts compile C through the cc crate, which spawns `cc` and `ar`
	// by name, and the compiler driver finds `as` on PATH.
	ToolPath string
	// DependencyCacheRoot is the node's shared dependency cache. When this
	// attempt's deps.rust task fetched the module's crates there (its record
	// says so), the task reads that CARGO_HOME instead of an empty scratch
	// one; it stays offline.
	DependencyCacheRoot string
	Timeout             time.Duration
	StdoutLimit         int64
	StderrLimit         int64
}

// Analyzer is the configured cargo check or clippy adapter.
type Analyzer struct {
	kind   Kind
	cfg    Config
	tcDir  string // resolved toolchain bin directory
	rustc  string // absolute rustc next to cargo
	linker string // absolute cc from ToolPath, "" if none
	// toolDirs are the existing absolute ToolPath directories, appended to
	// the child PATH of the cargo runs.
	toolDirs []string
	cargo    string // cargo as the operator named it
	toolExe  string // executable of the main execution
}

var _ pack.Analyzer = (*Analyzer)(nil)

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func regularExecutable(p string) bool {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0
}

// FindLinker returns the first absolute `cc` in the operator's tool path list
// ("" if none, and always "" on Windows where the MSVC linker needs its own
// environment).
func FindLinker(toolPath string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	for _, dir := range filepath.SplitList(toolPath) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p := filepath.Join(dir, "cc"); regularExecutable(p) {
			return p
		}
	}
	return ""
}

// BuildToolDirs are the existing absolute directories of the operator's tool
// path list in list order, without the toolchain bin directory and
// duplicates. Build scripts find the system C toolchain there.
func BuildToolDirs(toolPath, toolchainDir string) []string {
	seen := map[string]bool{filepath.Clean(toolchainDir): true}
	var out []string
	for _, dir := range filepath.SplitList(toolPath) {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// New validates the configuration without running anything.
func New(kind Kind, cfg Config) (*Analyzer, error) {
	if !filepath.IsAbs(cfg.Cargo) {
		return nil, errors.New("cargo executable must be an absolute path")
	}
	resolved, err := nativepath.Resolve(cfg.Cargo)
	if err != nil {
		return nil, errors.New("cargo executable cannot be resolved")
	}
	if strings.TrimSuffix(strings.ToLower(filepath.Base(resolved)), ".exe") != "cargo" {
		// A rustup proxy resolves to rustup: it would pick the toolchain from
		// the repository's rust-toolchain file and may download one.
		return nil, errors.New("cargo executable must resolve to a cargo binary (a rustup proxy is not accepted)")
	}
	if !regularExecutable(resolved) {
		return nil, errors.New("cargo executable is not an executable file")
	}
	dir := filepath.Dir(resolved)
	rustc := filepath.Join(dir, exeName("rustc"))
	if !regularExecutable(rustc) {
		return nil, errors.New("rustc is not next to cargo")
	}
	if _, err := os.Lstat(filepath.Join(dir, exeName("rustup"))); err == nil {
		return nil, errors.New("cargo is in a rustup proxy directory; name the toolchain's own bin directory")
	}
	if kind == KindDoc && !regularExecutable(filepath.Join(dir, exeName("rustdoc"))) {
		return nil, errors.New("rustdoc is not next to cargo")
	}
	a := &Analyzer{kind: kind, cfg: cfg, tcDir: dir, rustc: rustc, cargo: cfg.Cargo, toolExe: cfg.Cargo, linker: FindLinker(cfg.ToolPath), toolDirs: BuildToolDirs(cfg.ToolPath, dir)}
	if kind == KindClippy {
		if !filepath.IsAbs(cfg.Clippy) {
			return nil, errors.New("clippy executable must be an absolute path")
		}
		cr, err := nativepath.Resolve(cfg.Clippy)
		if err != nil {
			return nil, errors.New("clippy executable cannot be resolved")
		}
		if strings.TrimSuffix(strings.ToLower(filepath.Base(cr)), ".exe") != "cargo-clippy" || !regularExecutable(cr) {
			return nil, errors.New("clippy executable must be the cargo-clippy binary")
		}
		if filepath.Dir(cr) != dir {
			return nil, errors.New("cargo-clippy must be in the same toolchain bin directory as cargo")
		}
		if !regularExecutable(filepath.Join(dir, exeName("clippy-driver"))) {
			return nil, errors.New("clippy-driver is not next to cargo-clippy")
		}
		a.toolExe = cfg.Clippy
	}
	if cfg.Timeout <= 0 {
		a.cfg.Timeout = DefaultTimeout
	}
	if cfg.StdoutLimit <= 0 {
		a.cfg.StdoutLimit = DefaultStdoutLimit
	}
	if cfg.StderrLimit <= 0 {
		a.cfg.StderrLimit = DefaultStderrLimit
	}
	return a, nil
}

// Descriptor implements pack.Analyzer.
func (a *Analyzer) Descriptor() pack.Descriptor { return a.kind.descriptor() }

// Plan implements pack.Analyzer.
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return planTasks(ctx, a.kind, job)
}

// env is the complete explicit environment. Nothing is inherited: PATH is the
// toolchain bin directory first (so `cargo` finds `rustc` and a `cargo-clippy`
// of the same toolchain), then the operator's tool path directories for the
// programs build scripts spawn by name (cc, ar, as, pkg-config), CARGO_HOME and the target directory are task
// scratch, the build is offline, the build target is pinned to the host and
// no rustc wrapper from the repository's Cargo configuration runs. host
// is "" until `rustc -vV` has been read.
func (a *Analyzer) env(home, target, host string) map[string]string {
	env := map[string]string{
		"PATH":                     strings.Join(append([]string{a.tcDir}, a.toolDirs...), string(os.PathListSeparator)),
		"LANG":                     "C.UTF-8",
		"CARGO_HOME":               home,
		"CARGO_TARGET_DIR":         target,
		"RUSTC":                    a.rustc,
		"CARGO":                    a.cargo,
		"CARGO_NET_OFFLINE":        "true",
		"CARGO_TERM_COLOR":         "never",
		"CARGO_TERM_PROGRESS_WHEN": "never",
		"CARGO_INCREMENTAL":        "0",
		"RUSTC_WRAPPER":            "",
		"RUSTC_WORKSPACE_WRAPPER":  "",
	}
	if a.kind == KindDoc {
		env["RUSTDOC"] = filepath.Join(a.tcDir, exeName("rustdoc"))
		env["RUSTDOCFLAGS"] = "-W rustdoc::broken_intra_doc_links --cap-lints warn"
	}
	if host != "" {
		env["CARGO_BUILD_TARGET"] = host
		if a.linker != "" {
			env["CARGO_TARGET_"+strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(host))+"_LINKER"] = a.linker
		}
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	return env
}

// Probe runs the tool's version command in a fresh directory under dir
// (outside any source tree). An analyzer whose probe fails is not used.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("probe dir must be absolute")
	}
	work, err := os.MkdirTemp(dir, a.kind.descriptor().Name+"-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	for _, d := range []string{"home", "target", "ws"} {
		if err := os.Mkdir(filepath.Join(work, d), 0o700); err != nil {
			return "", err
		}
	}
	ws := filepath.Join(work, "ws")
	env := a.env(filepath.Join(work, "home"), filepath.Join(work, "target"), "")
	run := func(exe string, args ...string) (executor.Result, error) {
		res := runner.Run(ctx, executor.Spec{Executable: exe, Args: args, WorkspaceRoot: ws, Dir: ws, TempParent: work,
			Env: maps.Clone(env), Timeout: probeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
		if res.Cleanup == executor.CleanupUncertain {
			return res, errors.New("version probe cleanup uncertain")
		}
		if res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 {
			return res, fmt.Errorf("version probe did not succeed: %s %s", res.Status, res.Reason)
		}
		return res, nil
	}
	rc, err := run(a.rustc, "-vV")
	if err != nil {
		return "", err
	}
	if _, ok := ParseRustcVV(rc.Stdout.Data); !ok {
		return "", errors.New("rustc version output not recognized")
	}
	cg, err := run(a.cargo, "--version")
	if err != nil {
		return "", err
	}
	v, ok := ParseCargoVersion(cg.Stdout.Data)
	if !ok {
		return "", errors.New("cargo version output not recognized")
	}
	if a.kind == KindClippy {
		cl, err := run(a.toolExe, "--version")
		if err != nil {
			return "", err
		}
		if v, ok = ParseClippyVersion(cl.Stdout.Data); !ok {
			return "", errors.New("clippy version output not recognized")
		}
	}
	return v, nil
}

// Run executes one cargo check or clippy task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	d := a.kind.descriptor()
	pr, err := planModules(job.Snapshot.Manifest, job.Scope, d.Name, a.kind.title())
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	if pr.Placeholder != "" {
		tasks, err := planTasks(ctx, a.kind, job)
		if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != task.Key {
			return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
		}
		return pack.NotRun(job, d, tasks[0], pr.Placeholder)
	}
	var mod *moduleTask
	for i := range pr.Modules {
		if pr.Modules[i].Key == task.Key {
			mod = &pr.Modules[i]
		}
	}
	if mod == nil {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return pack.Failed(analysis.ReasonHandlerError, "invalid_job_paths")
	}
	taskDir := filepath.Join(job.Scratch, "analyzers", mod.Key)
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	dirs := map[string]string{}
	for _, n := range []string{"exec", "cargo-home", "target", "tmp"} {
		dirs[n] = filepath.Join(taskDir, n)
		if err := os.Mkdir(dirs[n], 0o700); err != nil {
			return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	in := assembleInput{Kind: a.kind, TaskKey: mod.Key, ManifestDigest: job.Snapshot.Digest, ModuleDir: mod.Dir, Patterns: mod.Patterns, Files: files, ScopeFiles: mod.Files, Linker: a.linker != "", ToolPath: len(a.toolDirs) > 0}
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
	// The module manifest must match the snapshot before cargo reads it.
	root := files[govet.JoinRel(mod.Dir, "Cargo.toml")]
	rootBytes, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, root.Path)
	if err != nil || obsdoc.SHA256Hex(rootBytes) != root.SHA256 {
		in.Integrity = true
		return write(assemble(in))
	}
	for p := range files {
		if path.Base(p) == "rust-toolchain.toml" || path.Base(p) == "rust-toolchain" {
			if pack.UnderDir(p, mod.Dir) || pack.UnderDir(mod.Dir, pack.DirOf(p)) {
				// Real toolchain binaries are named by the operator; the
				// repository's choice is only recorded (docs/ANALYZERS.md#rust-환경).
				in.ExtraLimitations = append(in.ExtraLimitations, "rust_toolchain_file_ignored")
			}
		}
	}
	workspace := job.Snapshot.WorkspaceDir
	roots := []string{workspace}
	if r, err := filepath.EvalSymlinks(workspace); err == nil && r != workspace {
		roots = append(roots, r)
	}
	in.WorkspaceRoots = roots
	in.Mask = maskerFor(roots, map[string]string{taskDir: "$SCRATCH", a.tcDir: "$TOOLCHAIN", a.linker: "$CC", job.Snapshot.Root: "$SNAPSHOT"})
	protected := []string{job.Snapshot.SnapshotDir}
	redact := govet.RedactForms(roots)
	for _, p := range []string{taskDir, a.tcDir} {
		redact = append(redact, govet.RedactForms([]string{p})...)
	}
	moduleDir := filepath.Join(workspace, filepath.FromSlash(mod.Dir))
	spec := func(env map[string]string, exe string, args ...string) executor.Spec {
		return executor.Spec{Executable: exe, Args: args, WorkspaceRoot: workspace, Dir: moduleDir, ProtectedRoots: protected, TempParent: dirs["tmp"],
			Env: maps.Clone(env), Timeout: a.cfg.Timeout, StdoutLimit: a.cfg.StdoutLimit, StderrLimit: a.cfg.StderrLimit, Redact: redact}
	}
	cargoHome := dirs["cargo-home"]
	if dep, ok := a.preparedCrates(job, mod.Key, d.Name, mod.Dir); ok {
		ready := true
		if dep.lockfile != "" {
			// The module has no Cargo.lock; the one deps.rust generated
			// pins what it fetched, and --locked reads it.
			remove, err := placeLockfile(dep.lockfile, moduleDir)
			if err != nil {
				ready = false
			} else {
				defer remove()
			}
		}
		if ready {
			cargoHome, in.Dependencies = dep.home, dep.limitation
		}
	}
	env := a.env(cargoHome, dirs["target"], "")
	type step struct {
		name string
		exe  string
		args []string
		dst  **executor.Result
	}
	steps := []step{{execRustc, a.rustc, []string{"-vV"}, &in.Rustc}, {execCargo, a.cargo, []string{"--version"}, &in.Cargo}}
	if a.kind == KindClippy {
		steps = append(steps, step{execClippyV, a.toolExe, []string{"--version"}, &in.ClippyV}, step{execClippy, a.toolExe, ClippyArgs(), &in.Main})
	} else if a.kind == KindDoc {
		steps = append(steps, step{execDoc, a.cargo, DocArgs(), &in.Main})
	} else {
		steps = append(steps, step{execCheck, a.toolExe, CheckArgs(), &in.Main})
	}
	writeFailed := false
	for _, s := range steps {
		if ctx.Err() != nil {
			in.Cancelled = true
			break
		}
		res := runner.Run(ctx, spec(env, s.exe, s.args...))
		*s.dst = &res
		e, err := analysis.WriteExecution(dirs["exec"], s.name, res)
		if err != nil {
			writeFailed = true
			break
		}
		in.Executions = append(in.Executions, e)
		if pack.Stoppable(&res) {
			break
		}
		// An unidentified tool is never run on the code: the parser is pinned
		// to the tested versions' output format.
		ok := false
		switch s.name {
		case execRustc:
			var r Rustc
			if r, ok = ParseRustcVV(res.Stdout.Data); ok && pack.ExitedZero(&res) {
				env = a.env(cargoHome, dirs["target"], r.Host)
			}
		case execCargo:
			_, ok = ParseCargoVersion(res.Stdout.Data)
		case execClippyV:
			_, ok = ParseClippyVersion(res.Stdout.Data)
		default:
			ok = true
		}
		if !ok || (!pack.ExitedZero(&res) && s.name != a.kind.main()) {
			break
		}
	}
	// A build that failed only in test, bench or example targets is run
	// again without those kinds, so the library and binaries are analysed
	// (decision A69). The retry has its own target directory: the failed
	// targets left dep-info files that would read as processed sources.
	if (a.kind == KindCheck || a.kind == KindClippy) && !writeFailed && !in.Cancelled && ctx.Err() == nil {
		if kinds := ExcludableFailure(in.Main); kinds != nil {
			retryTarget := filepath.Join(taskDir, "target-retry")
			if err := os.Mkdir(retryTarget, 0o700); err != nil {
				writeFailed = true
			} else {
				retryEnv := maps.Clone(env)
				retryEnv["CARGO_TARGET_DIR"] = retryTarget
				dirs["target"] = retryTarget
				res := runner.Run(ctx, spec(retryEnv, a.toolExe, a.kind.RetryArgs(kinds)...))
				e, err := analysis.WriteExecution(dirs["exec"], a.kind.retryName(), res)
				if err != nil {
					writeFailed = true
				} else {
					in.Executions = append(in.Executions, e)
					in.First, in.Main, in.ExcludedKinds = in.Main, &res, kinds
				}
			}
		}
	}
	// The workspace copy must still be the snapshot: a build script may write
	// into its working directory.
	in.Drift = workspaceDrift(workspace, files, mod.Dir)
	snapshotDir := job.Snapshot.SnapshotDir
	in.Source = func(rel string) ([]byte, bool) {
		b, err := govet.ReadSnapshotFile(snapshotDir, rel)
		return b, err == nil
	}
	if in.Main != nil && in.Main.Status == executor.StatusExited {
		in.DepInfo, in.DepInfoOK = readDepInfo(dirs["target"], roots, mod.Dir, files)
	}
	cfgFiles, known, external := ruleConfig(a.kind, files, mod.Dir, workspace, in.Source)
	in.ConfigFiles, in.ConfigKnown = cfgFiles, known
	if external {
		in.ExtraLimitations = append(in.ExtraLimitations, "external_cargo_config_present")
	}
	if len(mod.Patterns) != 1 || mod.Patterns[0] != "." {
		patterns := mod.Patterns
		in.OutOfScope = func(rel string) bool { return !scopeCovers(pack.RelTo(rel, mod.Dir), patterns) }
	}
	doc := assemble(in)
	if writeFailed {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: in.Executions, Limitations: doc.Limitations}
	}
	res := write(doc)
	res.Executions = in.Executions
	return res
}

// maskerFor replaces node paths in tool text: workspace paths become
// relative, other known roots become placeholders. Empty roots are ignored.
func maskerFor(roots []string, others map[string]string) func(string) string {
	type pair struct{ from, to string }
	var pairs []pair
	for _, w := range roots {
		pairs = append(pairs, pair{w + string(filepath.Separator), ""}, pair{w + "/", ""}, pair{w, "."})
	}
	for p, to := range others {
		if p == "" {
			continue
		}
		pairs = append(pairs, pair{p, to})
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			pairs = append(pairs, pair{r, to})
		}
	}
	sort.SliceStable(pairs, func(a, b int) bool { return len(pairs[a].from) > len(pairs[b].from) })
	return func(s string) string {
		s = strings.ReplaceAll(s, analysis.RedactedMarker+"/", "")
		if runtime.GOOS == "windows" {
			s = strings.ReplaceAll(s, analysis.RedactedMarker+`\`, "")
		}
		for _, p := range pairs {
			s = strings.ReplaceAll(s, p.from, p.to)
		}
		return s
	}
}

// driftSuffix are the module inputs whose change after the run is drift.
func driftInput(p string) bool {
	base := path.Base(p)
	switch {
	case strings.HasSuffix(p, ".rs"), base == "Cargo.toml", base == "Cargo.lock", base == "clippy.toml", base == ".clippy.toml":
		return true
	}
	return (base == "config.toml" || base == "config") && path.Base(pack.DirOf(p)) == ".cargo"
}

// workspaceDrift compares the module's inputs in the workspace with the
// manifest after the run.
func workspaceDrift(workspace string, files map[string]source.Entry, mod string) map[string]bool {
	drift := map[string]bool{}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		for p := range files {
			if pack.UnderDir(p, mod) && driftInput(p) {
				drift[p] = true
			}
		}
		return drift
	}
	defer root.Close()
	for p, e := range files {
		if !pack.UnderDir(p, mod) || !driftInput(p) {
			continue
		}
		b, err := readRootFile(root, p)
		if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
			drift[p] = true
		}
	}
	return drift
}

func readRootFile(root *os.Root, rel string) ([]byte, error) {
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// readDepInfo collects the source files rustc listed in the dep-info files of
// the task's fresh target directory (every unit this run built). ok is false
// when the walk failed or hit a limit: the processed set is then unknown.
func readDepInfo(targetDir string, roots []string, mod string, files map[string]source.Entry) ([]string, bool) {
	var out []string
	n, total := 0, 0
	err := filepath.WalkDir(targetDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".d") {
			return nil
		}
		n++
		if n > maxDepInfoFiles {
			return errors.New("too many dep-info files")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		total += len(b)
		if total > maxDepInfoBytes {
			return errors.New("dep-info too large")
		}
		for _, f := range ParseDepInfo(b) {
			rel, ok := govet.StripWorkspace(f, mod, roots, true)
			if !ok || rel == "." {
				continue
			}
			if _, listed := files[rel]; listed && strings.HasSuffix(rel, ".rs") {
				out = append(out, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, false
	}
	return out, true
}

var lintsSection = regexp.MustCompile(`(?m)^\s*(?:\[(?:workspace\.)?lints\b|(?:workspace\.)?lints\b)`)

// ruleConfig lists the repository files that choose which diagnostics the
// tool reports (docs/ANALYZERS.md#rust-규칙-문맥과-억제): Cargo configuration of the module directory and
// its ancestors, clippy.toml files (clippy only) and the Cargo.toml files
// that carry lint tables. known is false when the list is too long or cargo
// can also read configuration outside the snapshot: the directories above the
// workspace copy are checked, since cargo and clippy search ancestors of the
// working directory.
func ruleConfig(k Kind, files map[string]source.Entry, mod, workspace string, read func(string) ([]byte, bool)) (cfg []obsdoc.ConfigFile, known, external bool) {
	set := map[string]string{}
	add := func(p string) { set[p] = files[p].SHA256 }
	for dir := mod; ; dir = pack.DirOf(dir) {
		for _, n := range []string{".cargo/config.toml", ".cargo/config"} {
			if p := govet.JoinRel(dir, n); files[p].SHA256 != "" {
				add(p)
			}
		}
		if dir == "." {
			break
		}
	}
	for p := range files {
		base := path.Base(p)
		switch {
		case k == KindClippy && (base == "clippy.toml" || base == ".clippy.toml") && (pack.UnderDir(p, mod) || pack.UnderDir(mod, pack.DirOf(p))):
			add(p)
		case base == "Cargo.toml" && pack.UnderDir(p, mod):
			if b, ok := read(p); ok && lintsSection.Match(b) {
				add(p)
			}
		}
	}
	probe := []string{".cargo/config.toml", ".cargo/config"}
	if k == KindClippy {
		probe = append(probe, "clippy.toml", ".clippy.toml")
	}
	// Cargo uses the process's real working directory: walk the ancestors of
	// the workspace as named and as the symlinks resolve.
	starts := []string{filepath.Dir(workspace)}
	if r, err := filepath.EvalSymlinks(workspace); err == nil && filepath.Dir(r) != starts[0] {
		starts = append(starts, filepath.Dir(r))
	}
	for _, start := range starts {
		for dir := start; ; {
			for _, n := range probe {
				if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(n))); err == nil || !errors.Is(err, fs.ErrNotExist) {
					external = true
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cfg = []obsdoc.ConfigFile{}
	for _, p := range paths {
		cfg = append(cfg, obsdoc.ConfigFile{Path: p, SHA256: set[p]})
	}
	return cfg, !external && len(cfg) <= 256, external
}
