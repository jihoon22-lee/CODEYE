// Package python is the Python analyzer pack (docs/ANALYZERS.md). It runs the
// operator-installed ruff (the required baseline), mypy or pyright (type
// checking) and bandit (security) natively through the executor runner in the
// attempt workspace copy and records each task in the generic observations
// envelope (obsdoc). No task executes repository-controlled code: every tool
// gets a CODEYE-managed configuration that replaces the repository's, no
// project interpreter or virtual environment is used, nothing is installed
// and nothing is downloaded. Unresolved imports and missing stubs are
// counted as coverage gaps, never as defects of the code.
package python

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Ecosystem is the envdoc ecosystem of every analyzer of this pack.
const Ecosystem = "python"

// Defaults for one execution.
const (
	DefaultTimeout     = 15 * time.Minute
	DefaultStdoutLimit = 32 << 20
	DefaultStderrLimit = 4 << 20
	probeTimeout       = 30 * time.Second
)

// Task failure reasons shared by the Python adapters (outcome.reason).
const (
	ReasonCleanupUncertain     = "cleanup_uncertain"
	ReasonCancelled            = "cancelled"
	ReasonTimeout              = "timeout"
	ReasonExecutionUnsupported = "execution_unsupported"
	ReasonToolStartFailed      = "tool_start_failed"
	ReasonExecutionRejected    = "execution_rejected"
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonNotRun               = "tool_not_run"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonExitMismatch         = "exit_status_mismatch"
	// ReasonSyntaxErrors: mypy stopped at a syntax error it could not be run
	// past (the file is not one of the listed files, or the rerun bound was
	// reached). Other unparsable files are a limitation, not a failure.
	ReasonSyntaxErrors = "source_syntax_errors"
	// ReasonUnsafeModuleName: a nested module directory name holds a
	// character the tool's exclusion list would split or expand (",", glob
	// metacharacters); the task refuses instead of mis-excluding files.
	ReasonUnsafeModuleName  = "nested_module_name_unsupported"
	ReasonSuppressionDiff   = "suppression_diff_inconsistent"
	ReasonProcessedMismatch = "processed_files_unverified"
	ReasonNoFilesProcessed  = "no_files_processed"
	ReasonConfigUnavailable = "tool_config_unavailable"
)

// File unit states (coverage.units). Import gaps are counted in the same
// map, by the diagnostics that report them (docs/ANALYZERS.md#타입-검사기-선택).
const (
	UnitAnalyzedClean        = "analyzed_clean"
	UnitAnalyzedWithFindings = "analyzed_with_findings"
	UnitSyntaxError          = "syntax_error"
	UnitNotAnalyzed          = "not_analyzed"
	UnitImportUnresolved     = "import_unresolved"
	UnitStubMissing          = "stub_missing"
	UnitImportSourceMissing  = "import_source_missing"
)

// Limitation codes shared by the adapters.
const (
	LimImportsUnresolved    = "imports_unresolved"
	LimStubsMissing         = "type_stubs_missing"
	LimImportSourcesMissing = "import_sources_missing"
	LimFilesNotProcessed    = "planned_files_not_processed"
	LimSuppressed           = "source_suppressed_observations"
	LimToolVersionUntested  = "tool_version_untested"
	LimStderrOutput         = "tool_stderr_output"
	LimUnattributed         = "unattributed_observation_unit"
	LimNoThirdParty         = "third_party_packages_not_installed"
	LimDefaultContext       = "single_target_python_version"
	// LimBothTypeCheckers: the operator selected both type checkers; where
	// both run, their findings on one construct are separate observations
	// (docs/ANALYZERS.md#타입-검사기-선택).
	LimBothTypeCheckers = "python_type_checkers_both_selected"
	// LimOutsideDropped: the tool reported diagnostics or errors for files
	// outside the attempt workspace (its own stubs, a path a configuration
	// pointed to); they are dropped with their text and only counted.
	LimOutsideDropped = "diagnostics_outside_workspace_dropped"
	// LimUnparsable: files the tool could not parse or read were left out
	// (coverage unit syntax_error); the task reports everything else it
	// analyzed (docs/ANALYZERS.md#python-결과-판정).
	LimUnparsable = "unparsable_files_not_analyzed"
	// PropUnparsableFiles is the target property that names those files
	// (bandit reports them without a position, so no observation does).
	PropUnparsableFiles = "unparsable_files"
	// LimMypyRerunsTimedOut: the task's time limit ran out before mypy could
	// be rerun without the unparsable files found so far.
	LimMypyRerunsTimedOut = "mypy_reruns_time_limit_reached"
)

// Config selects one operator-installed tool. Nothing is searched in PATH
// and nothing is downloaded.
type Config struct {
	// Executable is the absolute tool path (--<tool>). For pyright it is the
	// package's JS entry script, run by the node toolchain.
	Executable string
	// Node is the validated shared node toolchain (pyright only).
	Node *pack.Toolchain
	// BothTypeCheckers is set on mypy and pyright when the operator selected
	// both (--mypy, --pyright and --python-both-type-checkers): their tasks
	// then carry LimBothTypeCheckers. It records the selection, not that the
	// other checker is usable; that task reports its own outcome (docs/ANALYZERS.md#타입-검사기-선택).
	BothTypeCheckers bool
	// DependencyCacheRoot is the node's shared dependency cache (hello
	// dependencies.cache_root). When a deps.python task of the attempt
	// prepared the module's third-party packages, mypy and pyright read them
	// (docs/ANALYZERS.md#python-의존성-받기); "" or no record keeps today's
	// path. Interpreter is the operator's Python (--python) mypy asks for
	// the prepared search paths.
	DependencyCacheRoot string
	Interpreter         string
	Timeout             time.Duration
	StdoutLimit         int64
	StderrLimit         int64
}

// base is what every Python adapter shares: identity, the resolved tool and
// the masks of node paths the tool may print.
type base struct {
	desc     pack.Descriptor
	cfg      Config
	resolved string
	// envRoot is the virtual environment of a Python script tool (from its
	// absolute #! interpreter), masked in messages.
	envRoot string
}

func newBase(d pack.Descriptor, cfg Config) (base, error) {
	if !filepath.IsAbs(cfg.Executable) {
		return base{}, fmt.Errorf("%s path must be absolute", d.Tool)
	}
	resolved, err := nativepath.Resolve(cfg.Executable)
	if err != nil {
		return base{}, fmt.Errorf("%s path cannot be resolved", d.Tool)
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.Mode().IsRegular() {
		return base{}, fmt.Errorf("%s path is not a regular file", d.Tool)
	}
	b := base{desc: d, cfg: cfg, resolved: resolved}
	if d.Interpreter != "" {
		if cfg.Node == nil || cfg.Node.Executable == "" || cfg.Node.Dir == "" {
			return base{}, fmt.Errorf("%s needs the validated %s toolchain", d.Tool, d.Interpreter)
		}
		// The interpreter runs the package's entry script, never a shell or
		// .cmd shim (docs/ANALYZERS.md#공유-interpreter-toolchain).
		if ext := strings.ToLower(filepath.Ext(resolved)); ext != ".js" && ext != ".cjs" && ext != ".mjs" {
			return base{}, fmt.Errorf("%s path must be the package's JS entry script", d.Tool)
		}
	} else if runtime.GOOS != "windows" {
		if st.Mode().Perm()&0o111 == 0 {
			return base{}, fmt.Errorf("%s path is not executable", d.Tool)
		}
		root, err := scriptEnvRoot(resolved)
		if err != nil {
			return base{}, err
		}
		b.envRoot = root
	}
	if b.cfg.Timeout <= 0 {
		b.cfg.Timeout = DefaultTimeout
	}
	if b.cfg.StdoutLimit <= 0 {
		b.cfg.StdoutLimit = DefaultStdoutLimit
	}
	if b.cfg.StderrLimit <= 0 {
		b.cfg.StderrLimit = DefaultStderrLimit
	}
	return b, nil
}

// scriptEnvRoot reads the #! line of a script tool (mypy and bandit are
// console scripts of their own virtual environment). The interpreter must
// be absolute: a #!/usr/bin/env line would look the interpreter up in PATH,
// which the pack never relies on. A binary has no #! line.
func scriptEnvRoot(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", errors.New("tool cannot be read")
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return "", nil
	}
	if !strings.HasPrefix(line, "#!") {
		return "", nil
	}
	fields := strings.Fields(strings.TrimSpace(line[2:]))
	if len(fields) == 0 || !filepath.IsAbs(fields[0]) || filepath.Base(fields[0]) == "env" {
		return "", errors.New("tool script interpreter must be an absolute path, not #!/usr/bin/env")
	}
	// The tool's own virtual environment (bin/python under a directory with
	// pyvenv.cfg) is masked in messages; a system interpreter's prefix
	// (/usr) is too short to mask safely.
	root := filepath.Dir(filepath.Dir(fields[0]))
	if st, err := os.Stat(filepath.Join(root, "pyvenv.cfg")); err != nil || !st.Mode().IsRegular() {
		return "", nil
	}
	return root, nil
}

// Descriptor implements pack.Analyzer.
func (b *base) Descriptor() pack.Descriptor { return b.desc }

// Plan implements pack.Analyzer.
func (b *base) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return planFor(b.desc)(ctx, job)
}

// command is the executable and argv of one tool run: the tool itself, or
// <node> <entry script> for an interpreter tool.
func (b *base) command(args []string) (string, []string) {
	if b.desc.Interpreter != "" {
		return b.cfg.Node.Executable, append([]string{b.resolved}, args...)
	}
	return b.cfg.Executable, append([]string{}, args...)
}

// pathDir is the only PATH entry of a tool child: the interpreter's
// directory for an interpreter tool, else the resolved tool's directory.
func (b *base) pathDir() string {
	if b.desc.Interpreter != "" {
		return b.cfg.Node.Dir
	}
	return filepath.Dir(b.resolved)
}

// env is the complete explicit environment of a tool child. Python script
// tools get no bytecode writes, no user site-packages and no script or cwd
// entry on sys.path.
func (b *base) env(extra map[string]string) map[string]string {
	env := map[string]string{"PATH": b.pathDir(), "LANG": "C.UTF-8"}
	if b.desc.Interpreter == "" && b.desc.Tool != RuffTool {
		env["PYTHONDONTWRITEBYTECODE"] = "1"
		env["PYTHONNOUSERSITE"] = "1"
		env["PYTHONSAFEPATH"] = "1"
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// Probe runs `<tool> --version` in a fresh directory under dir (outside any
// source tree). An analyzer whose probe fails is not used.
func (b *base) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("probe dir must be absolute")
	}
	work, err := os.MkdirTemp(dir, b.desc.Name+"-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	ws := filepath.Join(work, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		return "", err
	}
	exe, args := b.command(b.desc.VersionArgs)
	res := runner.Run(ctx, executor.Spec{Executable: exe, Args: args, WorkspaceRoot: ws, Dir: ws, TempParent: work,
		Env: b.env(nil), Timeout: probeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	switch {
	case res.Cleanup == executor.CleanupUncertain:
		return "", fmt.Errorf("%s version probe cleanup uncertain", b.desc.Tool)
	case res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0:
		return "", fmt.Errorf("%s version probe did not succeed: %s %s", b.desc.Tool, res.Status, res.Reason)
	}
	v, ok := b.desc.ParseVersion(res.Stdout.Data)
	if !ok {
		return "", fmt.Errorf("%s version output not recognized", b.desc.Tool)
	}
	return v, nil
}

// runCtx is one task run: its plan, scratch, the executions so far and the
// facts the assembler reads.
type runCtx struct {
	ctx        context.Context
	job        analysis.Job
	b          *base
	runner     executor.Runner
	p          *planned
	taskDir    string
	dirs       map[string]string
	files      map[string]source.Entry
	workspace  string
	roots      []string
	moduleAbs  string
	mask       func(string) string
	target     pyTarget
	extraEnv   map[string]string
	executions []analysis.Execution
	results    map[string]*executor.Result
	stopped    bool
	cancelled  bool
	writeFail  bool
	// refused is the controlled reason a task did not run its tool on the
	// code (a configuration it cannot write, a path it cannot pass safely).
	refused string
	drift   map[string]bool
	texts   *texts
	// checks are the names of a tool's check executions in run order when
	// it runs more than one (mypy's reruns without unparsable files).
	checks []string
	// deadline, when set, bounds every execution that starts after it is
	// set (mypy's reruns share the task's one time limit).
	deadline time.Time
	// budgetSpent: a run that the tool needed was not started because the
	// deadline had passed.
	budgetSpent bool
	// deps is the third-party package tree this attempt prepared for the
	// module (type checkers only); nil runs as without one.
	deps *prepared
	// depsUsed: the checker was given deps; depsUnused: deps was set but
	// the checker could not read it on this node.
	depsUsed, depsUnused bool
}

// thirdPartyLimitations are the limitations of a type checker about the
// third-party packages it read: none installed, the prepared tree, or a
// prepared tree it could not use.
func (rc *runCtx) thirdPartyLimitations() []string {
	switch {
	case rc.depsUsed:
		return []string{rc.deps.limitation()}
	case rc.depsUnused:
		return []string{LimNoThirdParty, LimPreparedNotUsed}
	}
	return []string{LimNoThirdParty}
}

// toolRun is the tool-specific part of a task: the executions after the
// version probe and the envelope.
type toolRun interface {
	execute(rc *runCtx)
	assemble(rc *runCtx) obsdoc.Document
}

// run is the shared task skeleton: re-derive the plan, prepare task
// scratch, identify the tool, run the tool, check the workspace, assemble
// and write the envelope.
func (b *base) run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner, t toolRun) analysis.TaskResult {
	ps, err := planTasks(b.desc, job)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	p := findPlanned(ps, task.Key)
	if p == nil {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	if p.Module.Placeholder {
		return pack.NotRun(job, b.desc, p.Task, ReasonNoAnalyzableFiles)
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return pack.Failed(analysis.ReasonHandlerError, "invalid_job_paths")
	}
	taskDir := filepath.Join(job.Scratch, "analyzers", p.Task.Spec.Key)
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	rc := &runCtx{ctx: ctx, job: job, b: b, runner: runner, p: p, taskDir: taskDir, dirs: map[string]string{}, files: includedFiles(job.Snapshot.Manifest),
		workspace: job.Snapshot.WorkspaceDir, results: map[string]*executor.Result{}, extraEnv: map[string]string{}}
	for _, d := range []string{"exec", "tmp", "cache", "cfg", "report"} {
		rc.dirs[d] = filepath.Join(taskDir, d)
		if err := os.Mkdir(rc.dirs[d], 0o700); err != nil {
			return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	rc.roots = []string{rc.workspace}
	if r, err := filepath.EvalSymlinks(rc.workspace); err == nil && r != rc.workspace {
		rc.roots = append(rc.roots, r)
	}
	rc.moduleAbs = filepath.Join(rc.workspace, filepath.FromSlash(p.Module.Dir))
	if b.desc.ID == MypyID || b.desc.ID == PyrightID {
		rc.deps = preparedTree(job, b.cfg, p.Task.Spec.Key, b.desc.Name, p.Module.Dir)
	}
	rc.mask = b.masker(rc.roots, job.Scratch, job.Snapshot.Root)
	if rc.deps != nil {
		inner := rc.mask
		tree := rc.deps.tree
		rc.mask = func(s string) string {
			s = strings.ReplaceAll(s, tree, "$DEPS")
			if t := filepath.ToSlash(tree); t != tree {
				s = strings.ReplaceAll(s, t, "$DEPS")
			}
			return inner(s)
		}
	}
	rc.target = resolveTarget(job.Snapshot.SnapshotDir, rc.files, p.Module.Dir)
	rc.texts = &texts{snapshotDir: job.Snapshot.SnapshotDir, cache: map[string]*fileText{}}

	if r := rc.exec(b.desc.Name+".version", b.desc.VersionArgs, nil); r != nil {
		// An unidentified tool is never run on the code: the parsers are
		// pinned to the tested version's output format.
		if _, ok := b.desc.ParseVersion(r.Stdout.Data); ok && pack.ExitedZero(r) {
			t.execute(rc)
		}
	}
	rc.drift = workspaceDrift(rc.workspace, rc.files, p.Module.Dir)
	doc := t.assemble(rc)
	if rc.writeFail {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: rc.executions, Limitations: doc.Limitations}
	}
	res := analysis.TaskResult{Status: doc.Outcome.TaskStatus, Limitations: doc.Limitations, Executions: rc.executions}
	if doc.Outcome.Reason != nil {
		res.Reason = *doc.Outcome.Reason
	}
	art, err := pack.WriteEnvelope(job, doc)
	if err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: rc.executions, Limitations: []string{"observations_write_failed"}}
	}
	res.Artifacts = []analysis.ArtifactFile{art}
	return res
}

// exec runs one tool execution in the module directory of the attempt
// workspace and records it. It returns nil when nothing was run (a previous
// execution stopped the task, or the job was cancelled).
func (rc *runCtx) exec(name string, args []string, stdin []byte) *executor.Result {
	if rc.stopped || rc.writeFail {
		return nil
	}
	if rc.ctx.Err() != nil {
		rc.cancelled, rc.stopped = true, true
		return nil
	}
	timeout := rc.b.cfg.Timeout
	if !rc.deadline.IsZero() {
		left := time.Until(rc.deadline)
		if left <= 0 {
			rc.budgetSpent, rc.stopped = true, true
			return nil
		}
		timeout = min(timeout, left)
	}
	exe, argv := rc.b.command(args)
	redact := append(govet.RedactForms(rc.roots), rc.taskDir)
	protected := []string{rc.job.Snapshot.SnapshotDir}
	if rc.deps != nil {
		// The prepared tree is read only, like the snapshot.
		redact = append(redact, rc.deps.tree)
		protected = append(protected, rc.deps.tree)
	}
	spec := executor.Spec{Executable: exe, Args: argv, WorkspaceRoot: rc.workspace, Dir: rc.moduleAbs, ProtectedRoots: protected,
		TempParent: rc.dirs["tmp"], Env: rc.b.env(rc.extraEnv), Stdin: stdin, Timeout: timeout,
		StdoutLimit: rc.b.cfg.StdoutLimit, StderrLimit: rc.b.cfg.StderrLimit, Redact: redact}
	res := rc.runner.Run(rc.ctx, spec)
	e, err := analysis.WriteExecution(rc.dirs["exec"], name, res)
	if err != nil {
		rc.writeFail, rc.stopped = true, true
		return nil
	}
	rc.executions = append(rc.executions, e)
	rc.results[name] = &e.Result
	if pack.Stoppable(&e.Result) {
		rc.stopped = true
	}
	return &e.Result
}

// masker replaces node paths (workspace, scratch, snapshot root, the tool,
// its directory and virtual environment, the node toolchain) in tool text.
func (b *base) masker(roots []string, scratch, snapshotRoot string) func(string) string {
	inner := govet.Masker(roots, "", "", "", scratch, snapshotRoot)
	tag := "$" + strings.ToUpper(b.desc.Tool)
	type pair struct{ from, to string }
	var pairs []pair
	add := func(p, to string) {
		if p == "" || p == "." || p == string(filepath.Separator) {
			return
		}
		pairs = append(pairs, pair{p, to})
		if s := filepath.ToSlash(p); s != p {
			pairs = append(pairs, pair{s, to})
		}
	}
	add(b.cfg.Executable, tag)
	add(b.resolved, tag)
	add(filepath.Dir(b.resolved), tag+"_DIR")
	add(b.envRoot, tag+"_ENV")
	if b.cfg.Node != nil {
		add(b.cfg.Node.Executable, "$NODE")
		add(b.cfg.Node.Dir, "$NODE_DIR")
		// The package directory of the entry script (bundled typeshed).
		add(filepath.Dir(b.resolved), tag+"_DIR")
	}
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].from) > len(pairs[j].from) })
	return func(s string) string {
		s = inner(s)
		for _, p := range pairs {
			s = strings.ReplaceAll(s, p.from, p.to)
		}
		return s
	}
}

// workspaceDrift compares the module's Python inputs (sources and module
// manifests) in the workspace after the run with the manifest hashes.
func workspaceDrift(workspace string, files map[string]source.Entry, moduleDir string) map[string]bool {
	drift := map[string]bool{}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		for p := range files {
			if pack.UnderDir(p, moduleDir) {
				drift[p] = true
			}
		}
		return drift
	}
	defer root.Close()
	for p, e := range files {
		if !pack.UnderDir(p, moduleDir) || !(isSource(p) || moduleManifest(filepath.Base(p))) {
			continue
		}
		b, err := root.ReadFile(filepath.FromSlash(p))
		if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
			drift[p] = true
		}
	}
	return drift
}

// writeLines writes a newline-terminated list to a new scratch file.
func writeLines(p string, lines []string) error {
	var buf bytes.Buffer
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	return analysis.WriteFileExclusive(p, buf.Bytes())
}
