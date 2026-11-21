// Package testexec runs existing repository tests on the attempt workspace.
package testexec

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	tools.Node, tools.Npm, tools.Python = regularPath(tools.Node, true), regularPath(tools.Npm, false), regularPath(tools.Python, true)
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
	for _, t := range p {
		base = append(base, t.spec)
	}
	return base, nil
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
	spec, reason := h.command(job, p, key, scratch, dir)
	if reason == "scratch_unavailable" {
		return pack.Failed(reason)
	}
	if reason != "" {
		return unsupported(reason)
	}
	spec.WorkspaceRoot, spec.Dir, spec.TempParent = workspace, dir, scratch
	spec.ProtectedRoots = []string{job.Snapshot.SnapshotDir}
	spec.Timeout, spec.StdoutLimit, spec.StderrLimit = commandTimeout, 8<<20, 2<<20
	spec.Redact = append(spec.Redact, govet.RedactForms([]string{workspace, job.Snapshot.SnapshotDir, job.Scratch, spec.Executable, h.tools.GoModCache, h.tools.DependencyCacheRoot})...)
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
		_, replaces, parsed := govet.ParseModEdit(res.Stdout.Data)
		if !pack.ExitedZero(&res) || !pack.StreamsComplete(&res) || !parsed {
			return analysis.TaskResult{Status: domain.TaskFailed, Reason: "test_command_failed", Executions: executions}
		}
		if len(govet.EscapingReplaces(replaces, p.dir)) > 0 {
			return analysis.TaskResult{Status: domain.TaskSkippedPolicy, Reason: "replace_outside_snapshot", Executions: executions}
		}
	}
	res, err := run(runName, isolated)
	info, markerErr := os.Lstat(startedPath)
	launched := markerErr == nil && info.Mode().IsRegular()
	out := analysis.TaskResult{Executions: executions}
	if launched {
		out.Limitations = append(out.Limitations, pack.LimRepositoryCodeExecuted)
	}
	if isJavaScript(key) {
		out.Limitations = append(out.Limitations, "test_script_reported_exit_only")
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
	case !pack.ExitedZero(&res):
		out.Status, out.Reason = domain.TaskFailed, "test_command_failed"
		if missingDependencies(key, res) {
			out.Reason = dependenciesUnavailable
		}
	case !testsPassed(key, res):
		out.Status, out.Reason = domain.TaskUnsupported, "no_tests_executed"
	default:
		out.Status = domain.TaskSucceeded
	}
	return out
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

var (
	pythonSummary    = regexp.MustCompile(`(?m)^Ran ([1-9][0-9]*) tests? in .+$`)
	pythonAllSkipped = regexp.MustCompile(`(?m)^OK \(skipped=([1-9][0-9]*)\)\r?$`)
	rustPassed       = regexp.MustCompile(`(?m)^test result: ok\. [1-9][0-9]* passed;`)
)

func testsPassed(key string, res executor.Result) bool {
	switch key {
	case "go":
		for line := range bytes.SplitSeq(res.Stdout.Data, []byte{'\n'}) {
			var event struct{ Action, Test string }
			if json.Unmarshal(line, &event) == nil && event.Action == "pass" && event.Test != "" {
				return true
			}
		}
		return false
	case "python":
		count := pythonSummary.FindSubmatch(res.Stderr.Data)
		skipped := pythonAllSkipped.FindSubmatch(res.Stderr.Data)
		return count != nil && (skipped == nil || !bytes.Equal(count[1], skipped[1]))
	case "rust":
		return rustPassed.Match(res.Stdout.Data)
	default:
		return isJavaScript(key)
	}
}

func missingDependencies(key string, res executor.Result) bool {
	combined := string(res.Stderr.Data) + "\n" + string(res.Stdout.Data)
	switch key {
	case "go":
		return strings.Contains(combined, "module lookup disabled by GOPROXY=off")
	case "rust":
		return strings.Contains(combined, "no matching package named") && strings.Contains(combined, "offline")
	case "python":
		return strings.Contains(combined, "ModuleNotFoundError:")
	default:
		return false
	}
}

func dependencyExpect(job analysis.Job) depdoc.Expect {
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	return depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		Lookup: func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }}
}
