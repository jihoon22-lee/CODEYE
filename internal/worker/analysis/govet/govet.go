// Package govet is the built-in Go vet analysis handler (W08). It runs
// `go version`, `go list -e -json` and `go vet -json` natively through the
// supplied W05 executor.Runner inside the attempt workspace clone of a W04
// snapshot, offline (GOPROXY=off, GOTOOLCHAIN=local, GOENV=off, GOWORK=off,
// CGO_ENABLED=0), and turns raw output into an observations document without
// ever turning unreadable output into "no findings".
package govet

import (
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Handler identity.
const (
	HandlerName    = "go-vet"
	HandlerVersion = "1"
	Capability     = "analysis:deep-review"
)

// Defaults for one execution.
const (
	DefaultTimeout     = 15 * time.Minute
	DefaultStdoutLimit = 32 << 20
	DefaultStderrLimit = 4 << 20
	probeTimeout       = 30 * time.Second
	maxSnapshotFile    = 16 << 20
)

// Config selects the operator-installed toolchain. Nothing is searched in PATH
// and nothing is downloaded.
type Config struct {
	// GoExecutable is the absolute path of the go command.
	GoExecutable string
	// GoModCache is an optional absolute, operator-prepared offline module
	// cache (read only for the tool). Empty means an empty per-task cache, so
	// modules with dependencies fail as dependency_unavailable.
	GoModCache  string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Handler implements analysis.Handler for go vet.
type Handler struct {
	cfg        Config
	resolvedGo string
	goroot     string
	modcache   string
}

var _ analysis.Handler = (*Handler)(nil)

// New validates the configuration without running anything.
func New(cfg Config) (*Handler, error) {
	if !filepath.IsAbs(cfg.GoExecutable) {
		return nil, errors.New("go executable must be an absolute path")
	}
	resolved, err := nativepath.Resolve(cfg.GoExecutable)
	if err != nil {
		return nil, errors.New("go executable cannot be resolved")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("go executable is not a regular file")
	}
	goroot := filepath.Dir(filepath.Dir(resolved))
	if st, err := os.Stat(filepath.Join(goroot, "src", "runtime")); err != nil || !st.IsDir() {
		return nil, errors.New("go executable is not inside a GOROOT (bin/go)")
	}
	h := &Handler{cfg: cfg, resolvedGo: resolved, goroot: goroot}
	if cfg.GoModCache != "" {
		if !filepath.IsAbs(cfg.GoModCache) {
			return nil, errors.New("go module cache must be an absolute path")
		}
		mc, err := nativepath.Resolve(cfg.GoModCache)
		if err != nil {
			return nil, errors.New("go module cache cannot be resolved")
		}
		if st, err := os.Stat(mc); err != nil || !st.IsDir() {
			return nil, errors.New("go module cache is not a directory")
		}
		h.modcache = mc
	}
	if h.cfg.Timeout <= 0 {
		h.cfg.Timeout = DefaultTimeout
	}
	if h.cfg.StdoutLimit <= 0 {
		h.cfg.StdoutLimit = DefaultStdoutLimit
	}
	if h.cfg.StderrLimit <= 0 {
		h.cfg.StderrLimit = DefaultStderrLimit
	}
	return h, nil
}

// Info returns the handler identity.
func (h *Handler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: HandlerName, Version: HandlerVersion, Capability: Capability}
}

// Plan derives one required task per Go module from the manifest only.
func (h *Handler) Plan(ctx context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planned, err := planTasks(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	out := make([]analysis.TaskSpec, 0, len(planned))
	for _, p := range planned {
		out = append(out, analysis.TaskSpec{Key: p.Key, Required: true, Title: p.Title, Stage: domain.StageStaticAnalysis})
	}
	return out, nil
}

// env is the complete explicit environment of every go execution.
func (h *Handler) env(gocache, gopath, modcache string) map[string]string {
	env := map[string]string{
		"PATH":        filepath.Dir(h.resolvedGo),
		"LANG":        "C.UTF-8",
		"GOENV":       "off",
		"GOWORK":      "off",
		"GOTOOLCHAIN": "local",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOVCS":       "*:off",
		"GOFLAGS":     "",
		"CGO_ENABLED": "0",
		"GOCACHE":     gocache,
		"GOPATH":      gopath,
		"GOMODCACHE":  modcache,
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	return env
}

// Probe runs `go version` in dir (an existing absolute writable directory
// that is not source) and returns the reported version. A handler whose
// probe fails must not be advertised.
func (h *Handler) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("probe dir must be absolute")
	}
	work, err := os.MkdirTemp(dir, "go-vet-probe-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	for _, d := range []string{"gocache", "gopath", "ws"} {
		if err := os.Mkdir(filepath.Join(work, d), 0o700); err != nil {
			return "", err
		}
	}
	modcache := h.modcache
	if modcache == "" {
		modcache = filepath.Join(work, "gopath", "pkg", "mod")
	}
	ws := filepath.Join(work, "ws")
	res := runner.Run(ctx, executor.Spec{Executable: h.cfg.GoExecutable, Args: []string{"version"}, WorkspaceRoot: ws, Dir: ws,
		TempParent: work, Env: h.env(filepath.Join(work, "gocache"), filepath.Join(work, "gopath"), modcache), Timeout: probeTimeout,
		StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	if res.Cleanup == executor.CleanupUncertain {
		return "", errors.New("go version probe cleanup uncertain")
	}
	if !exitedZero(&res) {
		return "", fmt.Errorf("go version probe did not succeed: %s %s", res.Status, res.Reason)
	}
	v, _, ok := parseVersion(res.Stdout.Data)
	if !ok {
		return "", errors.New("go version output not recognized")
	}
	return v, nil
}

func failed(reason string, limitations ...string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason, Limitations: append([]string{}, limitations...)}
}

// ObservationsPath is where the go-vet task key writes its observations
// artifact in attempt scratch. Later roles of the same attempt (review-r1
// interpret) read it from there and validate it again.
func ObservationsPath(scratch, key string) string {
	return filepath.Join(scratch, "go-vet", key, "observations.json")
}

// Run executes one go-vet task.
func (h *Handler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	planned, err := planTasks(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return failed(analysis.ReasonHandlerError, "plan_failed")
	}
	var pt *plannedTask
	for i := range planned {
		if planned[i].Key == task.Key {
			pt = &planned[i]
		}
	}
	if pt == nil {
		return failed(analysis.ReasonHandlerError, "unknown_task")
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return failed(analysis.ReasonHandlerError, "invalid_job_paths")
	}
	taskDir := filepath.Join(job.Scratch, "go-vet", pt.Key)
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		return failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		return failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	files := includedFiles(job.Snapshot.Manifest)
	modules := moduleDirs(files)
	in := assembleInput{TaskKey: pt.Key, ManifestDigest: job.Snapshot.Digest, Module: ModuleRef{Dir: pt.ModuleDir}, Patterns: pt.Patterns, Files: files}
	writeDoc := func(doc Document) (analysis.ArtifactFile, error) {
		raw, err := marshalDocument(doc)
		if err != nil {
			return analysis.ArtifactFile{}, err
		}
		p := ObservationsPath(job.Scratch, pt.Key)
		if err := analysis.WriteFileExclusive(p, raw); err != nil {
			return analysis.ArtifactFile{}, err
		}
		return analysis.ArtifactFile{Path: p, Role: analysis.RoleObservations, MediaType: analysis.MediaObservations}, nil
	}

	if pt.Unsupported != "" {
		doc := assemble(in)
		doc.Outcome = Outcome{TaskStatus: domain.TaskUnsupported, Reason: ptr(analysis.ReasonAnalysisUnsupported), ExitClass: ExitNotRun}
		doc.Limitations = uniqueSorted(append(doc.Limitations, pt.Unsupported))
		doc.Parse.OK = false
		art, err := writeDoc(doc)
		if err != nil {
			return failed(analysis.ReasonHandlerError, "observations_write_failed")
		}
		return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: analysis.ReasonAnalysisUnsupported, Artifacts: []analysis.ArtifactFile{art}, Limitations: []string{pt.Unsupported}}
	}

	in.ScopeGoFiles = goFilesInScope(files, modules, pt.ModuleDir, pt.Patterns)
	// go.mod must match the manifest, and a filesystem replace may not point
	// outside the snapshot: the native go command would read that host path.
	gomod := files[joinRel(pt.ModuleDir, "go.mod")]
	gomodBytes, err := readSnapshotFile(job.Snapshot.SnapshotDir, gomod.Path)
	if err != nil || sha256Hex(gomodBytes) != gomod.SHA256 {
		doc := assemble(in)
		doc.Outcome = Outcome{TaskStatus: domain.TaskFailed, Reason: ptr(ReasonSnapshotIntegrity), ExitClass: ExitNotRun}
		doc.Parse.OK = false
		art, werr := writeDoc(doc)
		if werr != nil {
			return failed(analysis.ReasonHandlerError, "observations_write_failed")
		}
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: ReasonSnapshotIntegrity, Artifacts: []analysis.ArtifactFile{art}, Limitations: doc.Limitations}
	}
	in.Module.Path = modulePath(gomodBytes) // replaced by the go command's parse below
	for p := range files {
		switch {
		case path.Base(p) == "go.work" && underDir(pt.ModuleDir, dirOf(p)):
			in.ExtraLimitations = append(in.ExtraLimitations, "go_work_ignored")
		case p == joinRel(pt.ModuleDir, "vendor/modules.txt"):
			in.ExtraLimitations = append(in.ExtraLimitations, "vendor_directory_used")
		}
	}

	dirs := map[string]string{}
	for _, d := range []string{"exec", "gocache", "gopath", "tmp"} {
		dirs[d] = filepath.Join(taskDir, d)
		if err := os.Mkdir(dirs[d], 0o700); err != nil {
			return failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	modcache := h.modcache
	if modcache == "" {
		modcache = filepath.Join(dirs["gopath"], "pkg", "mod")
		in.ExtraLimitations = append(in.ExtraLimitations, "no_offline_module_cache")
	}
	workspace := job.Snapshot.WorkspaceDir
	roots := []string{workspace}
	if r, err := filepath.EvalSymlinks(workspace); err == nil && r != workspace {
		roots = append(roots, r)
	}
	in.WorkspaceRoots, in.Windows = roots, runtime.GOOS == "windows"
	redact := redactForms(roots)
	// GOROOT is not a protected root: W05 refuses executables inside one.
	protected := []string{job.Snapshot.SnapshotDir}
	if h.modcache != "" {
		protected = append(protected, h.modcache)
	}
	spec := func(args ...string) executor.Spec {
		return executor.Spec{
			Executable: h.cfg.GoExecutable, Args: args,
			WorkspaceRoot: workspace, Dir: filepath.Join(workspace, filepath.FromSlash(pt.ModuleDir)),
			ProtectedRoots: protected, TempParent: dirs["tmp"],
			Env: h.env(dirs["gocache"], dirs["gopath"], modcache), Timeout: h.cfg.Timeout,
			StdoutLimit: h.cfg.StdoutLimit, StderrLimit: h.cfg.StderrLimit, Redact: redact,
		}
	}
	in.Mask = masker(roots, h.goroot, h.resolvedGo, modcache, job.Scratch, job.Snapshot.Root)

	var executions []analysis.Execution
	run := func(name string, args ...string) (*executor.Result, bool) {
		if ctx.Err() != nil {
			in.Cancelled = true
			return nil, false
		}
		res := runner.Run(ctx, spec(args...))
		e, err := analysis.WriteExecution(dirs["exec"], name, res)
		if err != nil {
			return &res, false
		}
		executions = append(executions, e)
		return &res, true
	}
	stoppable := func(r *executor.Result) bool {
		return r.Cleanup == executor.CleanupUncertain || r.Status == executor.StatusCancelled || r.Status == executor.StatusTimedOut ||
			r.Status == executor.StatusRejected || r.Status == executor.StatusUnsupported || r.Status == executor.StatusStartFailed
	}
	writeFailed := false
	steps := []struct {
		name string
		args []string
		dst  **executor.Result
	}{
		{execVersion, []string{"version"}, &in.Version},
		{execModEdit, []string{"mod", "edit", "-json"}, &in.ModEdit},
		{execList, append([]string{"list", "-e", "-json=" + listFields}, pt.Patterns...), &in.List},
		{execVet, append([]string{"vet", "-json"}, pt.Patterns...), &in.Vet},
	}
	for _, s := range steps {
		res, ok := run(s.name, s.args...)
		if res == nil {
			break
		}
		*s.dst = res
		if !ok {
			writeFailed = true
			break
		}
		if stoppable(res) {
			break
		}
		if s.name == execModEdit {
			// go.mod as the go command reads it: refuse before list/vet if a
			// directory replace leaves the snapshot (the native go command
			// would read that host path), or if it cannot be read.
			_, targets, ok := parseModEdit(res.Stdout.Data)
			if !ok || !exitedZero(res) || len(escapingReplaces(targets, pt.ModuleDir, in.Windows)) > 0 {
				break
			}
		}
	}

	in.Drift = workspaceDrift(workspace, files, modules, pt.ModuleDir)
	snapshotDir := job.Snapshot.SnapshotDir
	in.Lines = func(rel string) (int, string, bool) {
		b, err := readSnapshotFile(snapshotDir, rel)
		if err != nil {
			return 0, "", false
		}
		n := bytes.Count(b, []byte{'\n'})
		if len(b) > 0 && b[len(b)-1] != '\n' {
			n++
		}
		return n, sha256Hex(b), true
	}
	doc := assemble(in)
	result := analysis.TaskResult{Status: doc.Outcome.TaskStatus, Executions: executions, Limitations: doc.Limitations}
	if doc.Outcome.Reason != nil {
		result.Reason = *doc.Outcome.Reason
	}
	if writeFailed {
		result.Status, result.Reason = domain.TaskFailed, analysis.ReasonHandlerError
		return result
	}
	art, err := writeDoc(doc)
	if err != nil {
		result.Status, result.Reason = domain.TaskFailed, analysis.ReasonHandlerError
		return result
	}
	result.Artifacts = []analysis.ArtifactFile{art}
	return result
}

// masker replaces node-absolute toolchain/cache/scratch prefixes in tool
// text with stable placeholders, longest first, and makes redacted
// workspace paths relative.
func masker(workspaceRoots []string, goroot, goexe, modcache, scratch, snapshotRoot string) func(string) string {
	type pair struct{ from, to string }
	var pairs []pair
	for _, w := range workspaceRoots {
		pairs = append(pairs, pair{w + string(filepath.Separator), ""}, pair{w + "/", ""}, pair{w, "."})
	}
	add := func(p, to string) {
		if p == "" {
			return
		}
		pairs = append(pairs, pair{p, to})
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			pairs = append(pairs, pair{r, to})
		}
	}
	add(goexe, "$GO")
	add(goroot, "$GOROOT")
	add(modcache, "$GOMODCACHE")
	add(scratch, "$SCRATCH")
	add(snapshotRoot, "$SNAPSHOT")
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

// redactForms returns the workspace roots as given and as they appear inside
// JSON strings (backslashes and, with HTML escaping, <, >, & are escaped), so
// W05 redaction also hits go list/go vet JSON output.
func redactForms(roots []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, r := range roots {
		add(r)
		for _, html := range []bool{true, false} {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(html)
			if enc.Encode(r) == nil {
				quoted := strings.TrimSpace(buf.String())
				add(quoted[1 : len(quoted)-1])
			}
		}
	}
	return out
}

func readSnapshotFile(snapshotDir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(snapshotDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readRootFile(root, rel)
}

func readRootFile(root *os.Root, rel string) ([]byte, error) {
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSnapshotFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSnapshotFile {
		return nil, errors.New("file too large")
	}
	return b, nil
}

// workspaceDrift compares the module's Go inputs in the workspace after the
// run with the manifest hashes. A file that differs or is missing is drift.
func workspaceDrift(workspace string, files map[string]source.Entry, modules []string, mod string) map[string]bool {
	drift := map[string]bool{}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		for p := range files {
			drift[p] = true
		}
		return drift
	}
	defer root.Close()
	for p, e := range files {
		base := path.Base(p)
		if !strings.HasSuffix(p, ".go") && base != "go.mod" && base != "go.sum" {
			continue
		}
		if owner, ok := owningModule(p, modules); !ok || owner != mod {
			continue
		}
		b, err := readRootFile(root, p)
		if err != nil || sha256Hex(b) != e.SHA256 {
			drift[p] = true
		}
	}
	return drift
}
