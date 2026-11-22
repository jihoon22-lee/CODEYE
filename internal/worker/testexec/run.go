// Package testexec runs existing repository tests on the attempt workspace.
package testexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const (
	Capability              = "analysis:with-tests"
	runName                 = "test.run"
	isolationName           = "test.isolation"
	commandTimeout          = 10 * time.Minute
	toolUnavailable         = "test_tool_unavailable"
	dependenciesUnavailable = "test_dependencies_unavailable"
	isolationUnavailable    = "test_isolation_unavailable"
)

type Config struct {
	Go, GoModCache, Node, Npm, Python, Cargo, ToolPath, DependencyCacheRoot string
	CMake, CTest, Java, Maven, Gradle, MavenRepository, GradleHome, Nextest string
	GoTestRun                                                               string
	GoRace                                                                  bool
}

type Handler struct {
	base     analysis.Handler
	tools    Config
	goTool   *govet.Handler
	rustTool *rustpack.Analyzer
}

func New(base analysis.Handler, tools Config) *Handler {
	g, _ := govet.New(govet.Config{GoExecutable: tools.Go, GoModCache: tools.GoModCache, DependencyCacheRoot: tools.DependencyCacheRoot})
	r, _ := rustpack.New(rustpack.KindCheck, rustpack.Config{Cargo: tools.Cargo, ToolPath: tools.ToolPath, DependencyCacheRoot: tools.DependencyCacheRoot})
	tools.Node, tools.Npm = regularPath(tools.Node, true), regularPath(tools.Npm, false)
	// Python discovers its virtual environment from the invoked interpreter path.
	if regularPath(tools.Python, true) == "" {
		tools.Python = ""
	}
	return &Handler{base: base, tools: tools, goTool: g, rustTool: r}
}

func (h *Handler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "with-tests", Version: "1", Capability: Capability}
}

func (h *Handler) Plan(ctx context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	base, err := h.base.Plan(ctx, job)
	if err != nil {
		return nil, err
	}
	p, err := plans(job)
	if err != nil {
		return nil, err
	}
	at := len(base)
	if job.AllowModelReview {
		for i, spec := range base {
			if spec.Key == "review.verify" {
				at = i
				break
			}
		}
	}
	result := make([]analysis.TaskSpec, 0, len(base)+len(p))
	result = append(result, base[:at]...)
	for _, t := range p {
		result = append(result, t.spec)
	}
	return append(result, base[at:]...), nil
}

func (h *Handler) Run(ctx context.Context, job analysis.Job, spec analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	if !strings.HasPrefix(spec.Key, "test.") {
		return h.base.Run(ctx, job, spec, runner)
	}
	if ctx.Err() != nil {
		return analysis.TaskResult{Status: domain.TaskCancelled, Reason: pack.ReasonCancelled}
	}
	if spec.Key == noEntries {
		return unsupported("test_entries_unavailable")
	}
	if !job.AllowRepositoryCode {
		if job.RepositoryCodeWithheld {
			return unsupported(pack.ReasonRepositoryCodeNotOffered)
		}
		return unsupported(pack.ReasonRepositoryCodeNotAllowed)
	}
	list, err := plans(job)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError)
	}
	for _, p := range list {
		if p.spec.Key == spec.Key {
			return h.run(ctx, job, p, runner)
		}
	}
	return pack.Failed(pack.ReasonUnknownTask)
}

func unsupported(reason string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: reason}
}

func (h *Handler) run(ctx context.Context, job analysis.Job, p testPlan, runner executor.Runner) analysis.TaskResult {
	if p.unsupportedScope {
		return unsupported("test_scope_unsupported")
	}
	scratch := filepath.Join(job.Scratch, "tests", p.spec.Key)
	if err := os.MkdirAll(filepath.Dir(scratch), 0o700); err != nil {
		return pack.Failed("scratch_unavailable")
	}
	if err := os.Mkdir(scratch, 0o700); err != nil {
		return pack.Failed("scratch_unavailable")
	}
	key, _, _ := strings.Cut(strings.TrimPrefix(p.spec.Key, "test."), ".")
	workspace := job.Snapshot.WorkspaceDir
	dir := filepath.Join(workspace, filepath.FromSlash(p.dir))
	var before []preparation
	var spec executor.Spec
	var reason string
	if key == "cmake" {
		spec, before, reason = h.cmakeCommand(scratch, dir)
	} else {
		spec, reason = h.command(job, p, key, scratch, dir)
	}
	if reason == "scratch_unavailable" {
		return pack.Failed(reason)
	}
	if reason != "" {
		return unsupported(reason)
	}
	fullSpec := func(spec executor.Spec) executor.Spec {
		spec.WorkspaceRoot, spec.Dir, spec.TempParent = workspace, dir, scratch
		spec.ProtectedRoots = []string{job.Snapshot.SnapshotDir}
		if spec.Timeout == 0 {
			spec.Timeout = commandTimeout
		}
		spec.StdoutLimit, spec.StderrLimit = 8<<20, 2<<20
		spec.Redact = append(spec.Redact, govet.RedactForms([]string{workspace, job.Snapshot.SnapshotDir, job.Scratch, spec.Executable, h.tools.GoModCache, h.tools.DependencyCacheRoot})...)
		return spec
	}
	spec = fullSpec(spec)
	var executions []analysis.Execution
	run := func(name string, spec executor.Spec) (executor.Result, error) {
		res := runner.Run(ctx, spec)
		out, err := analysis.WriteExecution(scratch, name, res)
		if err == nil {
			executions = append(executions, out)
		}
		return res, err
	}
	startedPath := filepath.Join(scratch, "started")
	probe, isolated, ok := h.isolate(spec, startedPath)
	if !ok {
		return unsupported(isolationUnavailable)
	}
	isolation, err := run(isolationName, probe)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError)
	}
	if status, reason, _ := pack.ExecutionOutcome([]*executor.Result{&isolation}, ctx.Err() != nil); reason != "" {
		return analysis.TaskResult{Status: status, Reason: reason, Executions: executions}
	}
	if !pack.ExitedZero(&isolation) || !pack.StreamsComplete(&isolation) {
		return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: isolationUnavailable, Executions: executions}
	}
	for _, step := range before {
		marker := filepath.Join(scratch, step.name+".started")
		_, command, _ := h.isolate(fullSpec(step.spec), marker)
		res, err := run(step.name, command)
		out := analysis.TaskResult{Executions: executions}
		if testLaunched(marker) {
			out.Limitations = append(out.Limitations, pack.LimRepositoryCodeExecuted)
		}
		if err != nil {
			out.Status, out.Reason = domain.TaskFailed, analysis.ReasonHandlerError
			return out
		}
		if status, reason, _ := pack.ExecutionOutcome([]*executor.Result{&res}, ctx.Err() != nil); reason != "" {
			out.Status, out.Reason = status, reason
			return out
		}
		if !pack.ExitedZero(&res) || !pack.StreamsComplete(&res) {
			out.Status, out.Reason = domain.TaskFailed, "test_preparation_failed"
			return out
		}
	}
	var goModulePath string
	if key == "go" {
		probe := spec
		probe.Args, probe.Timeout = []string{"mod", "edit", "-json"}, 20*time.Second
		res, err := run("test.go-module", probe)
		if err != nil {
			return pack.Failed(analysis.ReasonHandlerError)
		}
		if status, reason, _ := pack.ExecutionOutcome([]*executor.Result{&res}, ctx.Err() != nil); reason != "" {
			return analysis.TaskResult{Status: status, Reason: reason, Executions: executions}
		}
		modulePath, replaces, parsed := govet.ParseModEdit(res.Stdout.Data)
		goModulePath = modulePath
		if !pack.ExitedZero(&res) || !pack.StreamsComplete(&res) || !parsed {
			return analysis.TaskResult{Status: domain.TaskFailed, Reason: "test_command_failed", Executions: executions}
		}
		if len(govet.EscapingReplaces(replaces, p.dir)) > 0 {
			return analysis.TaskResult{Status: domain.TaskSkippedPolicy, Reason: "replace_outside_snapshot", Executions: executions}
		}
	}
	var reportsBefore []javaReport
	if key == "maven" || key == "gradle" {
		var ok bool
		reportsBefore, ok = javaReports(workspace, p)
		if !ok {
			return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: "test_results_unavailable", Executions: executions}
		}
	}
	res, err := run(runName, isolated)
	launched := testLaunched(startedPath)
	out := analysis.TaskResult{Executions: executions}
	if launched || len(before) > 0 {
		out.Limitations = append(out.Limitations, pack.LimRepositoryCodeExecuted)
	}
	if isJavaScript(key) && p.runner == "" {
		out.Limitations = append(out.Limitations, "test_script_reported_exit_only")
	}
	var results testResults
	if launched && err == nil && ctx.Err() == nil && res.Status == executor.StatusExited && res.Cleanup == executor.CleanupConfirmed && pack.StreamsComplete(&res) {
		var name string
		if key == "maven" || key == "gradle" {
			name, results = key, javaResults(workspace, p, reportsBefore)
		} else {
			name, results = gatherResults(p, key, scratch, res)
		}
		if results.hasTests() {
			linkResultCases(ctx, job, p, name, goModulePath, res.Stdout.Data, results.cases)
			if artifact, ok := resultArtifact(job, p, scratch, name, res, results); ok {
				out.Artifacts = append(out.Artifacts, artifact)
			} else {
				out.Status, out.Reason = domain.TaskFailed, analysis.ReasonHandlerError
				return out
			}
		}
	}
	switch state, reason, _ := pack.ExecutionOutcome([]*executor.Result{&res}, ctx.Err() != nil); {
	case reason != "":
		out.Status, out.Reason = state, reason
	case err != nil:
		out.Status, out.Reason = domain.TaskFailed, analysis.ReasonHandlerError
	case !launched:
		out.Status, out.Reason = domain.TaskUnsupported, isolationUnavailable
	case !pack.StreamsComplete(&res):
		out.Status, out.Reason = domain.TaskFailed, "output_truncated"
	case p.runner == "pytest" && strings.Contains(string(res.Stderr.Data), "No module named pytest"):
		out.Status, out.Reason = domain.TaskUnsupported, toolUnavailable
	case p.runner == "pytest" && pack.ExitCode(&res) == 5:
		out.Status, out.Reason = domain.TaskUnsupported, "no_tests_executed"
	case p.runner == "ctest" && results.confirmed && !results.hasTests() && pack.ExitCode(&res) == 8:
		out.Status, out.Reason = domain.TaskUnsupported, "no_tests_executed"
	case !pack.ExitedZero(&res):
		out.Status, out.Reason = domain.TaskFailed, "test_command_failed"
		if missingDependencies(key, res) {
			out.Reason = dependenciesUnavailable
		}
	case results.counts.Failed > 0:
		out.Status, out.Reason = domain.TaskFailed, "test_command_failed"
	case key != "go" && key != "rust" && p.runner != "unittest" && !results.confirmed && !results.hasTests():
		out.Status, out.Reason = domain.TaskUnsupported, "test_results_unavailable"
	case !results.hasTests() || results.counts.Passed == 0:
		out.Status, out.Reason = domain.TaskUnsupported, "no_tests_executed"
	default:
		out.Status = domain.TaskSucceeded
	}
	if key == "go" && out.Status == domain.TaskFailed && (out.Reason == "test_command_failed" || out.Reason == dependenciesUnavailable) &&
		pack.ExitCode(&res) > 0 && res.Cleanup == executor.CleanupConfirmed {
		if artifact, ok := goFailureArtifact(ctx, job, p, scratch, goModulePath, res); ok {
			out.Artifacts = append(out.Artifacts, artifact)
		}
	}
	return out
}

func testLaunched(marker string) bool {
	info, err := os.Lstat(marker)
	return err == nil && info.Mode().IsRegular()
}

func isJavaScript(key string) bool { return strings.HasPrefix(key, "javascript-") }

func regularPath(p string, executable bool) string {
	if !filepath.IsAbs(p) {
		return ""
	}
	r, err := nativepath.Resolve(p)
	if err != nil {
		return ""
	}
	fi, err := os.Stat(r)
	if err != nil || !fi.Mode().IsRegular() || executable && runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return r
}

func missingDependencies(key string, res executor.Result) bool {
	combined := string(res.Stderr.Data) + "\n" + string(res.Stdout.Data)
	switch key {
	case "go":
		return strings.Contains(combined, "module lookup disabled by GOPROXY=off")
	case "rust", "rust-nextest":
		return strings.Contains(combined, "no matching package named") && strings.Contains(combined, "offline")
	case "python":
		return strings.Contains(combined, "ModuleNotFoundError:")
	case "maven", "gradle":
		return strings.Contains(combined, "offline mode") && (strings.Contains(combined, "not been downloaded") ||
			strings.Contains(combined, "No cached version") || strings.Contains(combined, "has not been cached"))
	default:
		return false
	}
}

func dependencyExpect(job analysis.Job) depdoc.Expect {
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	return depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		Lookup: func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }}
}
