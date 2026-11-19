// Package anypack holds the parts the analyzers of ecosystem "any"
// share (lizard complexity, jscpd duplication, gitleaks secrets; docs/ANALYZERS.md):
// selecting the snapshot files of the run scope, planning one task over
// them, starting the operator tool through the executor with an explicit
// environment, resolving tool positions against the snapshot manifest and
// checking that the attempt workspace still matches it. Nothing here runs a
// process except through the executor.Runner it is given.
package anypack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// ModuleDir is the module of every task of these analyzers: the snapshot root.
const ModuleDir = "."

// Defaults for one execution.
const (
	DefaultTimeout     = 15 * time.Minute
	DefaultStdoutLimit = 32 << 20
	DefaultStderrLimit = 4 << 20
	ProbeTimeout       = 30 * time.Second
)

// Task failure reasons shared by these adapters.
const (
	ReasonCleanupUncertain     = "cleanup_uncertain"
	ReasonCancelled            = "cancelled"
	ReasonTimeout              = "timeout"
	ReasonExecutionUnsupported = "execution_unsupported"
	ReasonToolStartFailed      = "tool_start_failed"
	ReasonExecutionRejected    = "execution_rejected"
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonSnapshotIntegrity    = "snapshot_integrity_failed"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonNoAnalyzableFiles    = "no_analyzable_files"
	// ReasonWorkspaceLink: the attempt workspace holds a symbolic link or
	// special file where a tool would read; nothing is started (a tool must
	// never follow a link out of the copy).
	ReasonWorkspaceLink = "workspace_link_refused"
)

// InScope reports whether a snapshot path is covered by the scope ("full"
// or a path list; a scope path covers itself and everything below it). It
// is the rule pack.SourceFiles applies.
func InScope(p string, scope domain.Scope) bool {
	if scope.Kind == "" || scope.Kind == "full" {
		return true
	}
	for _, s := range scope.Paths {
		s = cleanScope(s)
		if s == "." || p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

func cleanScope(s string) string {
	s = strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/")
	if s == "" {
		return "."
	}
	return path.Clean(s)
}

// DataDir reports a path segment treated as data or vendored code (hidden
// directories, testdata, vendor, node_modules), as pack.SourceFiles does.
func DataDir(seg string) bool {
	return pack.DataDir(seg)
}

// UnderDataDir reports a path with a data directory segment above it.
func UnderDataDir(p string) bool {
	return pack.UnderDataDir(p)
}

// Files lists the included regular files of the manifest in the job scope
// that keep accepts, sorted by path. With skipDataDirs, files below a data
// directory are left out (structure and duplication tools measure the
// project's own source).
func Files(job analysis.Job, keep func(string) bool, skipDataDirs bool) []source.Entry {
	var out []source.Entry
	for _, e := range job.Snapshot.Manifest.Entries {
		if e.Kind != "file" || e.ExcludedReason != "" || e.SHA256 == "" || !InScope(e.Path, job.Scope) || domain.ValidateEvidencePath(e.Path) != nil {
			continue
		}
		if skipDataDirs && UnderDataDir(e.Path) {
			continue
		}
		if keep == nil || keep(e.Path) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out
}

// DataDirectories names the data directory segments of DataDir, recorded
// as the exclusion of the structure and duplication tasks.
const DataDirectories = "hidden,testdata,vendor,node_modules"

// Units and limitation of the files a structure task leaves out because
// they are below a data directory.
const (
	UnitExcludedDataDir = "excluded_data_directory"
	LimDataDirsExcluded = "data_directories_excluded"
)

// ExcludedDataDirFiles counts the included files in scope that keep
// accepts but Files(job, keep, true) leaves out (below a data directory).
func ExcludedDataDirFiles(job analysis.Job, keep func(string) bool) int {
	return len(Files(job, keep, false)) - len(Files(job, keep, true))
}

// RecordExclusions writes the data directory exclusion of a structure task
// into doc: the excluded segments, the number of files left out and the
// limitation when there are any.
func RecordExclusions(doc *obsdoc.Document, excluded int) []string {
	doc.Target.Properties["excluded_directories"] = DataDirectories
	if excluded == 0 {
		return nil
	}
	doc.Coverage.Units[UnitExcludedDataDir] = excluded
	return []string{LimDataDirsExcluded}
}

// LanguageInScope reports whether source of a supported language ecosystem
// is in scope. The "any" analyzers extend a code review: a snapshot without
// supported source keeps planning no analyzer task (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백).
func LanguageInScope(job analysis.Job) bool {
	return len(pack.InScope(job.Snapshot.Manifest, job.Scope)) > 0
}

// Roots are the scope roots: ["."] for a full scope, else the scope paths,
// cleaned, sorted, without those another one already covers.
func Roots(scope domain.Scope) []string {
	if scope.Kind == "" || scope.Kind == "full" {
		return []string{"."}
	}
	var list []string
	for _, s := range scope.Paths {
		list = append(list, cleanScope(s))
	}
	sort.Strings(list)
	list = slices.Compact(list)
	var out []string
	for _, s := range list {
		covered := false
		for _, o := range out {
			if o == "." || s == o || strings.HasPrefix(s, o+"/") {
				covered = true
			}
		}
		if !covered {
			out = append(out, s)
		}
	}
	if slices.Contains(out, ".") {
		return []string{"."}
	}
	return out
}

// Plan is the one task of an "any" analyzer: key d.Name, module ".", the
// scope roots as patterns and the number of selected files. No task is
// planned when no supported language source or no selected file is in
// scope.
func Plan(job analysis.Job, d pack.Descriptor, title string, files []source.Entry) []pack.Task {
	if !LanguageInScope(job) {
		return nil
	}
	return PlanTask(job, d, title, files)
}

// PlanTask is Plan without the language gate, for an analyzer with its own
// planning rule (the secrets analyzer, gitleaks.Plan). No task is planned
// when no file is selected.
func PlanTask(job analysis.Job, d pack.Descriptor, title string, files []source.Entry) []pack.Task {
	if len(files) == 0 {
		return nil
	}
	n := len(files)
	return []pack.Task{{Spec: analysis.TaskSpec{Key: d.Name, Required: d.Required, Title: title, Stage: domain.StageStaticAnalysis},
		Module: ModuleDir, Patterns: Roots(job.Scope), FilesInScope: &n}}
}

// Tool is an operator-installed tool: its absolute path and, for a script,
// the validated interpreter toolchain that runs it (docs/ANALYZERS.md#공유-interpreter-toolchain).
type Tool struct {
	Executable  string
	Resolved    string
	Interpreter *pack.Toolchain
}

// NewTool validates the tool path without running anything.
func NewTool(exe string, interpreter *pack.Toolchain) (*Tool, error) {
	if !filepath.IsAbs(exe) {
		return nil, errors.New("tool executable must be an absolute path")
	}
	resolved, err := nativepath.Resolve(exe)
	if err != nil {
		return nil, errors.New("tool executable cannot be resolved")
	}
	if st, err := os.Stat(resolved); err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("tool executable is not a regular file")
	}
	if interpreter != nil && (!filepath.IsAbs(interpreter.Executable) || !filepath.IsAbs(interpreter.Dir)) {
		return nil, errors.New("interpreter toolchain is not validated")
	}
	return &Tool{Executable: exe, Resolved: resolved, Interpreter: interpreter}, nil
}

// Command is the executable and argv of one run: <interpreter> <tool> args
// for a script, else <tool> args.
func (t *Tool) Command(args ...string) (string, []string) {
	if t.Interpreter != nil {
		return t.Interpreter.Executable, append([]string{t.Executable}, args...)
	}
	return t.Executable, append([]string{}, args...)
}

// PathDir is the only PATH entry of the tool's children: the interpreter
// directory for a script, else the resolved tool's directory.
func (t *Tool) PathDir() string {
	if t.Interpreter != nil {
		return t.Interpreter.Dir
	}
	return filepath.Dir(t.Resolved)
}

// Env is the complete explicit environment of a tool run: PATH (one
// directory), LANG=C.UTF-8 (SystemRoot on Windows) and the adapter's extras.
func (t *Tool) Env(extra map[string]string) map[string]string {
	env := map[string]string{"PATH": t.PathDir(), "LANG": "C.UTF-8"}
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

// Masked are the node paths of the tool, replaced in recorded commands and
// captured streams.
func (t *Tool) Masked() []string {
	out := []string{t.Executable, t.Resolved}
	if t.Interpreter != nil {
		out = append(out, t.Interpreter.Executable, t.Interpreter.Dir)
	}
	return out
}

// Probe runs the version argv in a fresh directory under dir (outside any
// source tree) and returns the parsed version. A tool whose probe fails is
// not used.
func (t *Tool) Probe(ctx context.Context, runner executor.Runner, dir string, args []string, parse func([]byte) (string, bool)) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("probe dir must be absolute")
	}
	work, err := os.MkdirTemp(dir, "any-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	ws := filepath.Join(work, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		return "", err
	}
	exe, argv := t.Command(args...)
	res := runner.Run(ctx, executor.Spec{Executable: exe, Args: argv, WorkspaceRoot: ws, Dir: ws, TempParent: work, Env: t.Env(nil),
		Timeout: ProbeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10, Redact: t.Masked()})
	if res.Cleanup == executor.CleanupUncertain {
		return "", errors.New("version probe cleanup uncertain")
	}
	if res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 {
		return "", fmt.Errorf("version probe did not succeed: %s %s", res.Status, res.Reason)
	}
	v, ok := parse(res.Stdout.Data)
	if !ok {
		return "", errors.New("version output not recognized")
	}
	return v, nil
}

// Run is one task in progress: its scratch directories, the workspace it
// runs in and the executions recorded so far.
type Run struct {
	Job  analysis.Job
	Task pack.Task
	// Dir is the task scratch directory (analyzers/<key>); ExecDir holds the
	// captured streams, Tmp the executor's per-run directories.
	Dir, ExecDir, Tmp string
	Workspace         string
	// Roots are the workspace path forms (resolved too) that are masked.
	Roots      []string
	Executions []analysis.Execution
	// WriteFailed: a stream could not be stored; Cancelled: the context
	// ended before an execution started.
	WriteFailed, Cancelled bool
}

// Start re-derives the planned task of key and creates its scratch layout.
// It returns a failed result instead when the task is not planned or the
// job paths are unusable.
func Start(ctx context.Context, job analysis.Job, key string, plan pack.PlanFunc) (*Run, *analysis.TaskResult) {
	fail := func(lim string) (*Run, *analysis.TaskResult) {
		return nil, &analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Limitations: []string{lim}}
	}
	tasks, err := plan(ctx, job)
	if err != nil {
		return fail("plan_failed")
	}
	var pt *pack.Task
	for i := range tasks {
		if tasks[i].Spec.Key == key {
			pt = &tasks[i]
		}
	}
	if pt == nil {
		return fail(pack.ReasonUnknownTask)
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return fail("invalid_job_paths")
	}
	dir := filepath.Join(job.Scratch, "analyzers", key)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fail("scratch_unavailable")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fail("scratch_unavailable")
	}
	r := &Run{Job: job, Task: *pt, Dir: dir, ExecDir: filepath.Join(dir, "exec"), Tmp: filepath.Join(dir, "tmp"), Workspace: job.Snapshot.WorkspaceDir}
	for _, d := range []string{r.ExecDir, r.Tmp} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return fail("scratch_unavailable")
		}
	}
	r.Roots = []string{r.Workspace}
	if real, err := filepath.EvalSymlinks(r.Workspace); err == nil && real != r.Workspace {
		r.Roots = append(r.Roots, real)
	}
	return r, nil
}

// Mkdir creates a directory in the task scratch.
func (r *Run) Mkdir(name string) (string, error) {
	p := filepath.Join(r.Dir, name)
	return p, os.Mkdir(p, 0o700)
}

// WriteFile writes a CODEYE-managed input file (configuration, file list,
// report template) into the task scratch, outside the snapshot.
func (r *Run) WriteFile(name string, data []byte) (string, error) {
	p := filepath.Join(r.Dir, name)
	return p, analysis.WriteFileExclusive(p, data)
}

// Spec is the executor spec of one tool run in the workspace root: argv,
// cwd, explicit environment, timeout and output limits. The snapshot is a
// protected root; workspace, scratch and tool paths are masked in the
// recorded command and the captured streams.
func (r *Run) Spec(t *Tool, env map[string]string, timeout time.Duration, stdoutLimit, stderrLimit int64, args ...string) executor.Spec {
	exe, argv := t.Command(args...)
	redact := append(govet.RedactForms(r.Roots), govet.RedactForms([]string{r.Job.Scratch})...)
	redact = append(redact, t.Masked()...)
	return executor.Spec{Executable: exe, Args: argv, WorkspaceRoot: r.Workspace, Dir: r.Workspace, ProtectedRoots: []string{r.Job.Snapshot.SnapshotDir},
		TempParent: r.Tmp, Env: env, Timeout: timeout, StdoutLimit: stdoutLimit, StderrLimit: stderrLimit, Redact: redact}
}

// Exec runs spec as execution name and stores its streams. It returns nil
// without starting anything when the context already ended (Cancelled) or
// a previous stream could not be stored.
func (r *Run) Exec(ctx context.Context, runner executor.Runner, name string, spec executor.Spec) *executor.Result {
	if r.WriteFailed {
		return nil
	}
	if ctx.Err() != nil {
		r.Cancelled = true
		return nil
	}
	res := runner.Run(ctx, spec)
	e, err := analysis.WriteExecution(r.ExecDir, name, res)
	if err != nil {
		r.WriteFailed = true
		return &res
	}
	r.Executions = append(r.Executions, e)
	return &res
}

// Finish writes the envelope and returns the task result. A stream that
// could not be stored fails the task as a handler error.
func (r *Run) Finish(doc obsdoc.Document) analysis.TaskResult {
	if r.WriteFailed {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: r.Executions, Limitations: append([]string{"execution_write_failed"}, doc.Limitations...)}
	}
	res := analysis.TaskResult{Status: doc.Outcome.TaskStatus, Limitations: doc.Limitations, Executions: r.Executions}
	if doc.Outcome.Reason != nil {
		res.Reason = *doc.Outcome.Reason
	}
	art, err := pack.WriteEnvelope(r.Job, doc)
	if err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: r.Executions, Limitations: []string{"observations_write_failed"}}
	}
	res.Artifacts = []analysis.ArtifactFile{art}
	return res
}

// Outcome accumulates the task outcome: the first failure recorded wins.
type Outcome struct {
	Status domain.TaskStatus
	Reason string
	Class  string
}

// Fail records a failure unless one is already recorded.
func (o *Outcome) Fail(s domain.TaskStatus, reason, class string) {
	if o.Reason == "" {
		o.Status, o.Reason, o.Class = s, reason, class
	}
}

// Failed reports a recorded failure.
func (o *Outcome) Failed() bool { return o.Reason != "" }

// Executions applies the execution precedence: cleanup uncertain, then
// cancellation, timeout and processes that never ran.
func (o *Outcome) Executions(cancelled bool, results ...*executor.Result) {
	for _, r := range results {
		if r != nil && r.Cleanup == executor.CleanupUncertain {
			o.Fail(domain.TaskFailed, ReasonCleanupUncertain, obsdoc.ExitExecutionError)
		}
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusCancelled {
			o.Fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
		}
	}
	if cancelled {
		o.Fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusTimedOut {
			o.Fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
		}
	}
	for _, r := range results {
		if r == nil {
			continue
		}
		switch r.Status {
		case executor.StatusUnsupported:
			o.Fail(domain.TaskUnsupported, ReasonExecutionUnsupported, obsdoc.ExitNotRun)
		case executor.StatusStartFailed:
			o.Fail(domain.TaskFailed, ReasonToolStartFailed, obsdoc.ExitNotRun)
		case executor.StatusRejected:
			o.Fail(domain.TaskFailed, ReasonExecutionRejected, obsdoc.ExitNotRun)
		}
	}
}

// Apply writes the outcome into doc: success class from the active
// observations, not_run clears what nothing could have produced, and a
// failed run never claims processed files.
func (o *Outcome) Apply(doc *obsdoc.Document, parsedRecords bool) {
	status, class := o.Status, o.Class
	if o.Reason == "" {
		status, class = domain.TaskSucceeded, obsdoc.ExitSuccessWithoutFindings
		if doc.Active() > 0 {
			class = obsdoc.ExitSuccessWithFindings
		}
	}
	partial := false
	switch {
	case class == obsdoc.ExitNotRun:
		doc.Observations = []obsdoc.Observation{}
		doc.Coverage.ProcessedPaths = []string{}
	case o.Reason != "":
		doc.Coverage.ProcessedPaths = nil
		partial = parsedRecords
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: status, ExitClass: class, PartialParse: partial}
	if o.Reason != "" {
		r := o.Reason
		doc.Outcome.Reason = &r
	}
}

// RelPath maps a tool path (masked workspace path "[REDACTED]/a/b",
// workspace absolute or workspace-relative) to a snapshot path.
func (r *Run) RelPath(p string) (string, bool) {
	rel, ok := govet.StripWorkspace(p, ModuleDir, r.Roots, true)
	if !ok || rel == "." {
		return "", false
	}
	return rel, true
}

// Resolver maps tool positions to snapshot locations of the selected files.
type Resolver struct {
	files       map[string]source.Entry
	drift       map[string]bool
	snapshotDir string
	cache       map[string]lineInfo
}

type lineInfo struct {
	lines int
	sha   string
	ok    bool
}

// NewResolver resolves against files (the task's selected files) and the
// read-only snapshot; drifted workspace files never resolve.
func NewResolver(snapshotDir string, files []source.Entry, drift []string) *Resolver {
	r := &Resolver{files: map[string]source.Entry{}, drift: map[string]bool{}, snapshotDir: snapshotDir, cache: map[string]lineInfo{}}
	for _, f := range files {
		r.files[f.Path] = f
	}
	for _, d := range drift {
		r.drift[d] = true
	}
	return r
}

func (r *Resolver) info(rel string) lineInfo {
	if v, ok := r.cache[rel]; ok {
		return v
	}
	var v lineInfo
	if b, err := govet.ReadSnapshotFile(r.snapshotDir, rel); err == nil {
		n := bytes.Count(b, []byte{'\n'})
		if len(b) > 0 && b[len(b)-1] != '\n' {
			n++
		}
		v = lineInfo{lines: n, sha: obsdoc.SHA256Hex(b), ok: true}
	}
	r.cache[rel] = v
	return v
}

// Resolve returns the observation place of rel lines start..end (1-based)
// with byte columns (0 = not recorded).
func (r *Resolver) Resolve(rel string, start, end, startCol, endCol int) (*string, *obsdoc.Location, string) {
	if rel == "" || start < 1 {
		return nil, nil, obsdoc.LocationNoPosition
	}
	p := rel
	entry, ok := r.files[rel]
	if !ok {
		return &p, nil, obsdoc.LocationMissingFile
	}
	if r.drift[rel] {
		return &p, nil, obsdoc.LocationSourceDrift
	}
	v := r.info(rel)
	if !v.ok || v.sha != entry.SHA256 {
		return &p, nil, obsdoc.LocationSourceDrift
	}
	if end < start {
		end = start
	}
	if end == start && endCol < startCol {
		endCol = startCol
	}
	if start > max(v.lines, 1) || end > max(v.lines, 1) || startCol < 0 || endCol < 0 {
		return &p, nil, obsdoc.LocationLineOutOfRange
	}
	return &p, &obsdoc.Location{Path: rel, StartLine: start, StartCol: startCol, EndLine: end, EndCol: endCol, ColumnUnit: "byte", BlobSHA256: entry.SHA256}, obsdoc.LocationResolved
}

// Drift lists the selected files whose workspace copy no longer matches
// the manifest (changed or missing) after the tool ran.
func Drift(workspace string, files []source.Entry) []string {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		out := make([]string, 0, len(files))
		for _, f := range files {
			out = append(out, f.Path)
		}
		return out
	}
	defer root.Close()
	var out []string
	for _, f := range files {
		b, err := root.ReadFile(filepath.FromSlash(f.Path))
		if err != nil || obsdoc.SHA256Hex(b) != f.SHA256 {
			out = append(out, f.Path)
		}
	}
	return out
}

// TextState classifies a snapshot file for tools that silently skip
// content they cannot read as text.
type TextState int

const (
	// TextUTF8: valid UTF-8 without NUL bytes.
	TextUTF8 TextState = iota
	// TextNotUTF8: not valid UTF-8.
	TextNotUTF8
	// TextBinary: valid UTF-8 holding a NUL byte.
	TextBinary
	// TextUnreadable: the snapshot file could not be read or does not match
	// its manifest SHA-256.
	TextUnreadable
)

// ClassifyText reads a snapshot file (bound to its manifest SHA-256) and
// classifies its bytes.
func ClassifyText(snapshotDir string, e source.Entry) TextState {
	b, err := govet.ReadSnapshotFile(snapshotDir, e.Path)
	if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
		return TextUnreadable
	}
	switch {
	case !utf8.Valid(b):
		return TextNotUTF8
	case bytes.IndexByte(b, 0) >= 0:
		return TextBinary
	}
	return TextUTF8
}

// LinkFreeTree reports whether the workspace below each root holds only
// directories and regular files. The snapshot never copies a link
// (link_not_followed), so anything else means the copy was changed.
func LinkFreeTree(workspace string, roots []string) bool {
	return LinkFreeTreeExcept(workspace, roots, nil)
}

// LinkFreeTreeExcept is LinkFreeTree that does not descend into the listed
// snapshot-relative directories when each is a real directory: the
// node_modules trees this attempt's dependency tasks installed
// (depdoc.InstalledTrees), whose package links are the package manager's.
func LinkFreeTreeExcept(workspace string, roots, skip []string) bool {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[filepath.Join(workspace, filepath.FromSlash(s))] = true
	}
	for _, r := range roots {
		err := filepath.WalkDir(filepath.Join(workspace, filepath.FromSlash(r)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && skipped[p] {
				return filepath.SkipDir
			}
			if t := d.Type(); t&fs.ModeSymlink != 0 || !(t.IsDir() || t.IsRegular()) {
				return errLink
			}
			return nil
		})
		if err != nil {
			return false
		}
	}
	return true
}

// InstalledTrees are the node_modules directories this attempt's
// deps.javascript tasks installed (usable records only, validated again).
func InstalledTrees(job analysis.Job) []string {
	files := map[string]string{}
	for _, e := range job.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			files[e.Path] = e.SHA256
		}
	}
	return depdoc.InstalledTrees(job.Scratch, depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		Lookup: func(p string) (string, bool) { s, ok := files[p]; return s, ok }})
}

// LinkFreeFiles reports whether each listed workspace file is a regular
// file reached through directories only (no link on the way).
func LinkFreeFiles(workspace string, files []source.Entry) bool {
	seen := map[string]bool{}
	for _, f := range files {
		segs := strings.Split(f.Path, "/")
		for i := range segs {
			rel := strings.Join(segs[:i+1], "/")
			if seen[rel] {
				continue
			}
			fi, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(rel)))
			if err != nil {
				return false
			}
			last := i == len(segs)-1
			if (last && !fi.Mode().IsRegular()) || (!last && !fi.IsDir()) {
				return false
			}
			seen[rel] = true
		}
	}
	return true
}

var errLink = errors.New("workspace link")

// ExecutionsOf summarizes the recorded executions for the envelope.
func ExecutionsOf(list []analysis.Execution) []obsdoc.Execution {
	out := make([]obsdoc.Execution, 0, len(list))
	for _, e := range list {
		out = append(out, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	return out
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// Paths returns the paths of files.
func Paths(files []source.Entry) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}
