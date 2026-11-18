package cpp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// Defaults for one execution.
const (
	DefaultTimeout     = 30 * time.Minute
	DefaultStdoutLimit = 32 << 20
	DefaultStderrLimit = 32 << 20
	probeTimeout       = 30 * time.Second
	maxSnapshotRead    = 64 << 20
)

// Config selects an operator-installed tool. Nothing is searched in PATH
// and nothing is downloaded.
type Config struct {
	// Executable is the absolute tool binary (--cppcheck, --clang-tidy).
	Executable  string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
	// BatchSize bounds the translation units of one clang-tidy execution
	// (default DefaultBatchSize); cppcheck runs all units in one execution
	// so that its whole-program checks see every unit.
	BatchSize int
}

// tool is a validated operator executable.
type tool struct {
	cfg      Config
	resolved string
}

func newTool(name string, cfg Config) (tool, error) {
	if !filepath.IsAbs(cfg.Executable) {
		return tool{}, errors.New(name + " executable must be an absolute path")
	}
	resolved, err := nativepath.Resolve(cfg.Executable)
	if err != nil {
		return tool{}, errors.New(name + " executable cannot be resolved")
	}
	if st, err := os.Stat(resolved); err != nil || !st.Mode().IsRegular() {
		return tool{}, errors.New(name + " executable is not a regular file")
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
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	return tool{cfg: cfg, resolved: resolved}, nil
}

// env is the complete explicit environment of every execution: PATH is the
// directory of the resolved tool only (neither tool runs a subprocess, and
// no PATH entry of the agent leaks in), LANG fixes message text. HOME and
// TMP are the executor's per-run directories. No CPATH, C_INCLUDE_PATH,
// CCC_OVERRIDE_OPTIONS or other compiler variable reaches the tools.
func (t tool) env() map[string]string {
	env := map[string]string{"PATH": filepath.Dir(t.resolved), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	return env
}

// probe runs `<tool> <args>` in a fresh directory under dir and parses the
// version; a tool whose probe fails is not used.
func (t tool) probe(ctx context.Context, runner executor.Runner, dir string, args []string, parse func([]byte) (string, bool)) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("probe dir must be absolute")
	}
	work, err := os.MkdirTemp(dir, "cpp-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	ws := filepath.Join(work, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		return "", err
	}
	res := runner.Run(ctx, executor.Spec{Executable: t.cfg.Executable, Args: args, WorkspaceRoot: ws, Dir: ws, TempParent: work,
		Env: t.env(), Timeout: probeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	if res.Cleanup == executor.CleanupUncertain {
		return "", errors.New("version probe cleanup uncertain")
	}
	if res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 || res.Stdout.Truncated {
		return "", fmt.Errorf("version probe did not succeed: %s %s", res.Status, res.Reason)
	}
	v, ok := parse(res.Stdout.Data)
	if !ok {
		return "", errors.New("version output not recognized")
	}
	return v, nil
}

// session is the per-task execution context: task scratch, the workspace
// roots, masking and redaction.
type session struct {
	job       analysis.Job
	t         tool
	taskDir   string
	execDir   string
	tmpDir    string
	workDir   string // CODEYE-written tool inputs (compile DB, file list)
	cwdDir    string // the empty cwd of every execution
	workspace string
	roots     []string
	redact    []string
	mask      func(string) string
}

func failed(reason string, limitations ...string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason, Limitations: append([]string{}, limitations...)}
}

func newSession(job analysis.Job, key string, t tool, placeholder string) (*session, *analysis.TaskResult) {
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		r := failed(analysis.ReasonHandlerError, "invalid_job_paths")
		return nil, &r
	}
	taskDir := filepath.Join(job.Scratch, "analyzers", key)
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		r := failed(analysis.ReasonHandlerError, "scratch_unavailable")
		return nil, &r
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		r := failed(analysis.ReasonHandlerError, "scratch_unavailable")
		return nil, &r
	}
	s := &session{job: job, t: t, taskDir: taskDir, execDir: filepath.Join(taskDir, "exec"), tmpDir: filepath.Join(taskDir, "tmp"),
		workDir: filepath.Join(taskDir, "work"), cwdDir: filepath.Join(taskDir, "cwd"), workspace: job.Snapshot.WorkspaceDir}
	for _, d := range []string{s.execDir, s.tmpDir, s.workDir, s.cwdDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			r := failed(analysis.ReasonHandlerError, "scratch_unavailable")
			return nil, &r
		}
	}
	s.roots = []string{s.workspace}
	if r, err := filepath.EvalSymlinks(s.workspace); err == nil && r != s.workspace {
		s.roots = append(s.roots, r)
	}
	// Workspace roots are redacted in the recorded argv and the captured
	// streams as given, JSON-escaped and XML-escaped (cppcheck writes paths
	// in XML attributes); the scratch input and temp directories are
	// redacted too because arguments name files there (--project=,
	// --file-list=, -p). The empty cwd is not: it is recorded as a node path
	// ("[REDACTED]"), never as a workspace directory.
	s.redact = govet.RedactForms(s.roots)
	for _, r := range s.roots {
		if x := xmlEscape(r); x != r {
			s.redact = append(s.redact, x)
		}
	}
	s.redact = append(s.redact, s.workDir, s.tmpDir)
	base := govet.Masker(s.roots, "", "", "", job.Scratch, job.Snapshot.Root)
	toolPaths := []string{t.resolved, t.cfg.Executable, filepath.Dir(t.resolved)}
	if prefix := filepath.Dir(filepath.Dir(t.resolved)); strings.Count(filepath.ToSlash(prefix), "/") >= 3 {
		toolPaths = append(toolPaths, prefix)
	}
	sort.SliceStable(toolPaths, func(a, b int) bool { return len(toolPaths[a]) > len(toolPaths[b]) })
	s.mask = func(v string) string {
		for _, p := range toolPaths {
			if p != "" {
				v = strings.ReplaceAll(v, p, placeholder)
			}
		}
		return base(v)
	}
	return s, nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// spec is the execution of the tool with args. The cwd is an empty task
// scratch directory, not the workspace: cppcheck 2.22 reads std.cfg and
// --library files from its cwd before its installation (measured), so a
// repository std.cfg would replace the tool's library configuration. Every
// input path is absolute. The snapshot is protected, temp/home go to task
// scratch.
func (s *session) spec(args []string, timeout time.Duration) executor.Spec {
	return executor.Spec{Executable: s.t.cfg.Executable, Args: args, WorkspaceRoot: s.cwdDir, Dir: s.cwdDir,
		ProtectedRoots: []string{s.job.Snapshot.SnapshotDir}, TempParent: s.tmpDir, Env: s.t.env(), Timeout: timeout,
		StdoutLimit: s.t.cfg.StdoutLimit, StderrLimit: s.t.cfg.StderrLimit, Redact: s.redact}
}

// run executes one step and records its streams.
// clean, when set, rewrites the result before anything of it is stored
// (sanitize.go).
func (s *session) run(ctx context.Context, runner executor.Runner, name string, spec executor.Spec, clean func(*executor.Result)) (*executor.Result, analysis.Execution, error) {
	res := runner.Run(ctx, spec)
	if clean != nil {
		clean(&res)
	}
	e, err := analysis.WriteExecution(s.execDir, name, res)
	return &res, e, err
}

// inside reports a tool path in the workspace copy ("[REDACTED]/x" after
// redaction, an absolute workspace path, or workspace-relative without "..").
func (s *session) inside(p string) bool {
	_, ok := govet.StripWorkspace(p, ".", s.roots, true)
	return ok
}

// write finishes the envelope and the task result.
func (s *session) write(doc obsdoc.Document, executions []analysis.Execution) analysis.TaskResult {
	res := analysis.TaskResult{Status: doc.Outcome.TaskStatus, Limitations: doc.Limitations, Executions: executions}
	if doc.Outcome.Reason != nil {
		res.Reason = *doc.Outcome.Reason
	}
	art, err := pack.WriteEnvelope(s.job, doc)
	if err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: executions, Limitations: []string{"observations_write_failed"}}
	}
	res.Artifacts = []analysis.ArtifactFile{art}
	return res
}

// readSnapshot reads a snapshot file through an os.Root (no escape).
func readSnapshot(snapshotDir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(snapshotDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSnapshotRead+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSnapshotRead {
		return nil, errors.New("file too large")
	}
	return b, nil
}

// readDBs reads the compile DBs from the snapshot after checking them
// against the manifest (a DB that differs is an integrity failure).
func readDBs(job analysis.Job, l layout) (map[string][]byte, []obsdoc.ConfigFile, bool) {
	data := map[string][]byte{}
	var files []obsdoc.ConfigFile
	for _, db := range l.DBs {
		b, err := readSnapshot(job.Snapshot.SnapshotDir, db)
		if err != nil || obsdoc.SHA256Hex(b) != l.Files[db].SHA256 {
			return nil, nil, false
		}
		data[db] = b
		files = append(files, obsdoc.ConfigFile{Path: db, SHA256: l.Files[db].SHA256})
	}
	sort.Slice(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	return data, files, true
}

// drift compares the C/C++ inputs and compile DBs in the workspace after the
// run with the manifest; a file that differs or is missing is drift.
func drift(workspace string, files map[string]source.Entry) map[string]bool {
	out := map[string]bool{}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		for p := range files {
			if IsCppFile(p) || filepath.Base(p) == CompileDBName {
				out[p] = true
			}
		}
		return out
	}
	defer root.Close()
	for p, e := range files {
		if !IsCppFile(p) && filepath.Base(p) != CompileDBName {
			continue
		}
		f, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			out[p] = true
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, maxSnapshotRead+1))
		f.Close()
		if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
			out[p] = true
		}
	}
	return out
}

// resolver maps tool positions to snapshot locations (1-based line, 1-based
// byte column, measured for both tools: a tab counts one byte, a multibyte
// character its UTF-8 length).
type resolver struct {
	files       map[string]source.Entry
	drift       map[string]bool
	snapshotDir string
	roots       []string
	cache       map[string]lineInfo
}

type lineInfo struct {
	n   int
	sha string
	ok  bool
}

func (rs *resolver) lines(rel string) lineInfo {
	if v, ok := rs.cache[rel]; ok {
		return v
	}
	var li lineInfo
	if b, err := readSnapshot(rs.snapshotDir, rel); err == nil {
		li.n = bytes.Count(b, []byte{'\n'})
		if len(b) > 0 && b[len(b)-1] != '\n' {
			li.n++
		}
		li.sha, li.ok = obsdoc.SHA256Hex(b), true
	}
	rs.cache[rel] = li
	return li
}

// rel maps a tool path (redacted "[REDACTED]/x", absolute workspace path or
// workspace-relative) to a snapshot path.
func (rs *resolver) rel(file string) (string, bool) {
	rel, ok := govet.StripWorkspace(file, ".", rs.roots, true)
	if !ok || rel == "." {
		return "", false
	}
	return rel, true
}

func (rs *resolver) resolve(file string, line, col int) (*string, *obsdoc.Location, string) {
	if file == "" || line < 1 {
		return nil, nil, obsdoc.LocationNoPosition
	}
	rel, ok := rs.rel(file)
	if !ok {
		return nil, nil, obsdoc.LocationOutsideSnapshot
	}
	relp := rel
	entry, ok := rs.files[rel]
	if !ok {
		return &relp, nil, obsdoc.LocationMissingFile
	}
	if rs.drift[rel] {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	li := rs.lines(rel)
	if !li.ok || li.sha != entry.SHA256 {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	if line > max(li.n, 1) || col < 0 {
		return &relp, nil, obsdoc.LocationLineOutOfRange
	}
	return &relp, &obsdoc.Location{Path: rel, StartLine: line, StartCol: col, EndLine: line, EndCol: col, ColumnUnit: "byte", BlobSHA256: entry.SHA256}, obsdoc.LocationResolved
}

func exitedWith(r *executor.Result, codes ...int) bool {
	if r == nil || r.Status != executor.StatusExited || r.ExitCode == nil {
		return false
	}
	for _, c := range codes {
		if *r.ExitCode == c {
			return true
		}
	}
	return false
}

func streamsComplete(r *executor.Result) bool {
	return r != nil && !r.Stdout.Truncated && !r.Stdout.Incomplete && !r.Stderr.Truncated && !r.Stderr.Incomplete
}

func stoppable(r *executor.Result) bool {
	return r.Cleanup == executor.CleanupUncertain || r.Status == executor.StatusCancelled || r.Status == executor.StatusTimedOut ||
		r.Status == executor.StatusRejected || r.Status == executor.StatusUnsupported || r.Status == executor.StatusStartFailed
}

// outcome collects the first failure by precedence.
type outcome struct {
	status domain.TaskStatus
	reason string
	class  string
}

func (o *outcome) fail(s domain.TaskStatus, reason, class string) {
	if o.reason == "" {
		o.status, o.reason, o.class = s, reason, class
	}
}

// executionFailures applies the precedence shared with W19: cleanup,
// cancellation, timeout, then executor refusals.
func (o *outcome) executionFailures(results []*executor.Result, cancelled bool) {
	for _, r := range results {
		if r != nil && r.Cleanup == executor.CleanupUncertain {
			o.fail(domain.TaskFailed, ReasonCleanupUncertain, obsdoc.ExitExecutionError)
		}
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusCancelled {
			o.fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
		}
	}
	if cancelled {
		o.fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusTimedOut {
			o.fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
		}
	}
	for _, r := range results {
		if r == nil {
			continue
		}
		switch r.Status {
		case executor.StatusUnsupported:
			o.fail(domain.TaskUnsupported, ReasonExecutionUnsupported, obsdoc.ExitNotRun)
		case executor.StatusStartFailed:
			o.fail(domain.TaskFailed, ReasonToolStartFailed, obsdoc.ExitNotRun)
		case executor.StatusRejected:
			o.fail(domain.TaskFailed, ReasonExecutionRejected, obsdoc.ExitNotRun)
		}
	}
}

// finish applies the outcome to doc: success classes follow the active
// observations, a failure never claims processed files, not_run carries none.
func (o *outcome) finish(doc *obsdoc.Document, processed []string, partial bool, parsedOK bool) {
	status, reason, class := o.status, o.reason, o.class
	if reason == "" {
		status, class = domain.TaskSucceeded, obsdoc.ExitSuccessWithoutFindings
		if doc.Active() > 0 {
			class = obsdoc.ExitSuccessWithFindings
		}
		partial = false
		doc.Coverage.ProcessedPaths = processed
	}
	switch {
	case class == obsdoc.ExitNotRun:
		doc.Observations, partial = []obsdoc.Observation{}, false
		doc.Coverage.ProcessedPaths = []string{}
	case reason != "" && status == domain.TaskUnsupported:
		doc.Coverage.ProcessedPaths = []string{}
	case reason != "":
		doc.Coverage.ProcessedPaths = nil
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: status, ExitClass: class, PartialParse: partial}
	if reason != "" {
		doc.Outcome.Reason = ptr(reason)
	}
	doc.Parse.OK = reason == "" && parsedOK
}

// notRun ends a task that needs no process with the pack envelope shape of
// the analyzer (its rule pack), unsupported or failed, processing nothing.
func notRun(s *session, doc obsdoc.Document, status domain.TaskStatus, reason, class string) analysis.TaskResult {
	doc.Coverage.ProcessedPaths = []string{}
	doc.Observations = []obsdoc.Observation{}
	doc.Outcome = obsdoc.Outcome{TaskStatus: status, Reason: ptr(reason), ExitClass: class}
	doc.Limitations = append(doc.Limitations, reason)
	return s.write(doc, nil)
}
