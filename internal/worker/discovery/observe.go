package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Probe failure details (observation detail codes).
const (
	DetailExitNonZero        = "exit_nonzero"
	DetailTimedOut           = "timed_out"
	DetailCancelled          = "cancelled"
	DetailStartFailed        = "start_failed"
	DetailCleanupUncertain   = "cleanup_uncertain"
	DetailOutputUnrecognized = "output_unrecognized"
	DetailNotExecutable      = "not_executable"
	// DetailToolchainNotConfigured: the tool runs under a shared toolchain
	// (docs/ANALYZERS.md#도구-경로) the operator did not configure; nothing was started.
	DetailToolchainNotConfigured = "toolchain_not_configured"
	// DetailToolchainUnavailable: the configured toolchain path is not an
	// executable file; nothing was started.
	DetailToolchainUnavailable = "toolchain_unavailable"

	// ExecGoVersion is the execution name of the Go probe.
	ExecGoVersion = "go.version"
	probeTimeout  = 30 * time.Second
)

// Observed is the result of observing the node's tools for one attempt.
type Observed struct {
	Observations []envdoc.Observation
	// AnalyzerTools are the analyzer tool observations (docs/ANALYZERS.md) of the
	// ecosystems the requested runtime tools belong to.
	AnalyzerTools []envdoc.AnalyzerTool
	// Needed are runtime tools observed beyond the requested ones: shared
	// toolchains (docs/ANALYZERS.md#도구-경로) that an analyzer of a requested ecosystem
	// needs. Environment records them like the modules' runtimes.
	Needed     []string
	Executions []analysis.Execution
	// Incomplete is set when a probe execution did not exit cleanly (timeout,
	// cancellation, refused start, uncertain cleanup, truncated output). The
	// worker never lets such a task claim success, so env.discover reports
	// failed and still uploads its artifact.
	Incomplete bool
	Cancelled  bool
	// Err is a handler error (e.g. scratch not writable).
	Err error
}

// Observer reports what the node running this attempt has for each tool.
// Implementations probe only operator-configured executables; they never
// search PATH and never reuse an observation of another host.
type Observer interface {
	Observe(ctx context.Context, job analysis.Job, tools []string, runner executor.Runner) Observed
}

// ProbeObserver probes operator-configured tools through the attempt's
// executor.Runner. Of the runtimes Go (the agent's --go executable, the same
// one the go-vet handler uses) and the shared toolchains (--node) are
// configurable; every other runtime is not_probed. Analyzers are the analyzer tool probes of the worker build
// (builtin.Probes): a configured tool is probed with its version argv, an
// unconfigured one is not_probed. Toolchains are the shared toolchain probes
// (builtin.ToolchainProbes, e.g. node): a toolchain is a runtime
// observation like go, probed with its own version argv when requested or
// when an analyzer of a requested ecosystem needs it (then it is listed in
// Observed.Needed), and not_probed when the operator configured no path.
type ProbeObserver struct {
	GoExecutable string
	Analyzers    []pack.ToolProbe
	Toolchains   []pack.ToolProbe
	Timeout      time.Duration
}

// ecosystemOf maps a runtime tool to the ecosystem whose modules need it
// (envdoc.PrimaryTools inverted).
func ecosystemsOf(tools []string) map[string]bool {
	out := map[string]bool{"any": true}
	for eco, primary := range envdoc.PrimaryTools {
		for _, t := range primary {
			if slices.Contains(tools, t) {
				out[eco] = true
			}
		}
	}
	return out
}

// observeAnalyzers probes every analyzer tool relevant to the requested
// ecosystems, in tool order.
func (p ProbeObserver) observeAnalyzers(ctx context.Context, job analysis.Job, tools []string, runner executor.Runner, out *Observed) {
	ecos := ecosystemsOf(tools)
	for _, probe := range p.Analyzers {
		relevant := false
		for _, e := range probe.Ecosystems {
			relevant = relevant || ecos[e]
		}
		if !relevant || len(probe.Analyzers) == 0 {
			continue
		}
		t := envdoc.AnalyzerTool{Tool: probe.Tool, Analyzers: sortedUnique(probe.Analyzers), Status: envdoc.ObsNotProbed}
		if probe.Executable != "" {
			o, exec, incomplete, cancelled, err := p.probeTool(ctx, job, probe, runner, envdoc.AnalyzerToolVersionValid)
			if err != nil {
				out.Err = err
				return
			}
			t.Status, t.Version, t.Detail, t.Execution = o.Status, o.Version, o.Detail, o.Execution
			if exec != nil {
				out.Executions = append(out.Executions, *exec)
			}
			out.Incomplete = out.Incomplete || incomplete
			out.Cancelled = out.Cancelled || cancelled
		}
		out.AnalyzerTools = append(out.AnalyzerTools, t)
	}
	sort.Slice(out.AnalyzerTools, func(a, b int) bool { return out.AnalyzerTools[a].Tool < out.AnalyzerTools[b].Tool })
}

// probeTool runs one tool's version argv in fresh attempt scratch with an
// explicit minimal environment: PATH is the directory of the program that is
// started (the tool, or the interpreter toolchain of a tool that names one,
// which is started as <interpreter> <tool path> <args>, never through the
// script's #! line). valid checks the parsed version text.
func (p ProbeObserver) probeTool(ctx context.Context, job analysis.Job, probe pack.ToolProbe, runner executor.Runner, valid func(string) bool) (o envdoc.Observation, exec *analysis.Execution, incomplete, cancelled bool, err error) {
	tool := probe.Tool
	if !filepath.IsAbs(probe.Executable) {
		return probeFailed(tool, DetailStartFailed, nil), nil, false, false, nil
	}
	resolved, rerr := nativepath.Resolve(probe.Executable)
	if rerr != nil {
		return envdoc.Observation{Tool: tool, Status: envdoc.ObsNotFound}, nil, false, false, nil
	}
	st, serr := os.Stat(resolved)
	if serr != nil || !st.Mode().IsRegular() {
		return envdoc.Observation{Tool: tool, Status: envdoc.ObsNotFound}, nil, false, false, nil
	}
	exe, args, pathDir := probe.Executable, append([]string{}, probe.Args...), filepath.Dir(resolved)
	if probe.Interpreter != "" {
		if probe.InterpreterExecutable == "" {
			return probeFailed(tool, DetailToolchainNotConfigured, nil), nil, false, false, nil
		}
		ires, ok := executableFile(probe.InterpreterExecutable)
		if !ok {
			return probeFailed(tool, DetailToolchainUnavailable, nil), nil, false, false, nil
		}
		exe, args, pathDir = probe.InterpreterExecutable, append([]string{probe.Executable}, args...), filepath.Dir(ires)
	} else if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		return probeFailed(tool, DetailNotExecutable, nil), nil, false, false, nil
	}
	if !filepath.IsAbs(job.Scratch) {
		return o, nil, false, false, errors.New("scratch must be absolute")
	}
	work := filepath.Join(job.Scratch, "discovery", tool+"-probe")
	ws := filepath.Join(work, "ws")
	for _, d := range []string{ws, filepath.Join(work, "tmp")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return o, nil, false, false, err
		}
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = probeTimeout
	}
	var protected []string
	if job.Snapshot.SnapshotDir != "" {
		protected = []string{job.Snapshot.SnapshotDir}
	}
	env := map[string]string{"PATH": pathDir, "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	name := tool + ".version"
	res := runner.Run(ctx, executor.Spec{Executable: exe, Args: args, WorkspaceRoot: ws, Dir: ws,
		ProtectedRoots: protected, TempParent: filepath.Join(work, "tmp"), Env: env, Timeout: timeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	e, werr := analysis.WriteExecution(filepath.Join(work, "exec"), name, res)
	if werr != nil {
		return o, nil, false, false, werr
	}
	return classifyProbe(tool, res, &name, func(b []byte) (string, bool) {
		v, ok := probe.ParseVersion(b)
		return v, ok && valid(v)
	}), &e, probeIncomplete(res), res.Status == executor.StatusCancelled, nil
}

// executableFile resolves an absolute path to an executable regular file.
func executableFile(p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	resolved, err := nativepath.Resolve(p)
	if err != nil {
		return "", false
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0) {
		return "", false
	}
	return resolved, true
}

func notProbed(tool string) envdoc.Observation {
	return envdoc.Observation{Tool: tool, Status: envdoc.ObsNotProbed}
}

func probeFailed(tool, detail string, exec *string) envdoc.Observation {
	return envdoc.Observation{Tool: tool, Status: envdoc.ObsProbeFailed, Detail: &detail, Execution: exec}
}

// Observe implements Observer.
func (p ProbeObserver) Observe(ctx context.Context, job analysis.Job, tools []string, runner executor.Runner) Observed {
	var out Observed
	// Shared toolchains needed by an analyzer of a requested ecosystem are
	// observed even when no module declares the runtime.
	ecos := ecosystemsOf(tools)
	toolchains := map[string]pack.ToolProbe{}
	all := append([]string{}, tools...)
	for _, tc := range p.Toolchains {
		if tc.Tool == "go" || !envdoc.IsRuntimeTool(tc.Tool) {
			continue // go keeps --go; a toolchain is always a runtime tool
		}
		toolchains[tc.Tool] = tc
		if slices.Contains(tools, tc.Tool) {
			continue
		}
		for _, e := range tc.Ecosystems {
			if ecos[e] {
				out.Needed = append(out.Needed, tc.Tool)
				all = append(all, tc.Tool)
				break
			}
		}
	}
	out.Needed = sortedUnique(out.Needed)
	for _, tool := range sortedUnique(all) {
		var (
			o                     envdoc.Observation
			exec                  *analysis.Execution
			incomplete, cancelled bool
			err                   error
		)
		tc, isToolchain := toolchains[tool]
		switch {
		case tool == "go" && p.GoExecutable != "":
			o, exec, incomplete, cancelled, err = p.probeGo(ctx, job, runner)
		case isToolchain && tc.Executable != "":
			o, exec, incomplete, cancelled, err = p.probeTool(ctx, job, tc, runner, func(v string) bool { return envdoc.ComparableObservation(tool, v) })
		default:
			out.Observations = append(out.Observations, notProbed(tool))
			continue
		}
		if err != nil {
			out.Err = err
			return out
		}
		out.Observations = append(out.Observations, o)
		if exec != nil {
			out.Executions = append(out.Executions, *exec)
		}
		out.Incomplete = out.Incomplete || incomplete
		out.Cancelled = out.Cancelled || cancelled
	}
	p.observeAnalyzers(ctx, job, tools, runner, &out)
	return out
}

func sortedUnique(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	var u []string
	for i, s := range out {
		if i == 0 || out[i-1] != s {
			u = append(u, s)
		}
	}
	return u
}

// goEnv is the complete probe environment: offline, no toolchain switching,
// no user configuration file, caches in attempt scratch.
func goEnv(resolved, work string) map[string]string {
	env := map[string]string{
		"PATH": filepath.Dir(resolved), "LANG": "C.UTF-8",
		"GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOVCS": "*:off",
		"GOFLAGS": "", "CGO_ENABLED": "0",
		"GOCACHE":    filepath.Join(work, "gocache"),
		"GOPATH":     filepath.Join(work, "gopath"),
		"GOMODCACHE": filepath.Join(work, "gopath", "pkg", "mod"),
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	return env
}

// probeGo runs `go version` in a fresh scratch directory (never the snapshot,
// workspace or a module directory) with GOTOOLCHAIN=local.
func (p ProbeObserver) probeGo(ctx context.Context, job analysis.Job, runner executor.Runner) (o envdoc.Observation, exec *analysis.Execution, incomplete, cancelled bool, err error) {
	if !filepath.IsAbs(p.GoExecutable) {
		return probeFailed("go", DetailStartFailed, nil), nil, false, false, nil
	}
	resolved, rerr := nativepath.Resolve(p.GoExecutable)
	if rerr != nil {
		return envdoc.Observation{Tool: "go", Status: envdoc.ObsNotFound}, nil, false, false, nil
	}
	st, serr := os.Stat(resolved)
	if serr != nil || !st.Mode().IsRegular() {
		return envdoc.Observation{Tool: "go", Status: envdoc.ObsNotFound}, nil, false, false, nil
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		return probeFailed("go", DetailNotExecutable, nil), nil, false, false, nil
	}
	if !filepath.IsAbs(job.Scratch) {
		return o, nil, false, false, errors.New("scratch must be absolute")
	}
	work := filepath.Join(job.Scratch, "discovery", "go-probe")
	ws := filepath.Join(work, "ws")
	for _, d := range []string{ws, filepath.Join(work, "gocache"), filepath.Join(work, "gopath"), filepath.Join(work, "tmp")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return o, nil, false, false, err
		}
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = probeTimeout
	}
	var protected []string
	if job.Snapshot.SnapshotDir != "" {
		protected = []string{job.Snapshot.SnapshotDir}
	}
	env := goEnv(resolved, work)
	if err := govet.DisableTelemetry(env, filepath.Join(work, "config")); err != nil {
		return o, nil, false, false, err
	}
	res := runner.Run(ctx, executor.Spec{Executable: p.GoExecutable, Args: []string{"version"}, WorkspaceRoot: ws, Dir: ws,
		ProtectedRoots: protected, TempParent: filepath.Join(work, "tmp"), Env: env, Timeout: timeout,
		StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	e, werr := analysis.WriteExecution(filepath.Join(work, "exec"), ExecGoVersion, res)
	if werr != nil {
		return o, nil, false, false, werr
	}
	name := ExecGoVersion
	return classifyGoProbe(res, &name), &e, probeIncomplete(res), res.Status == executor.StatusCancelled, nil
}

// probeIncomplete mirrors the worker rule that a succeeded task may only
// carry cleanly exited, complete executions.
func probeIncomplete(r executor.Result) bool {
	return r.Status != executor.StatusExited || r.ExitCode == nil || r.Cleanup == executor.CleanupUncertain || r.Stdout.Incomplete || r.Stdout.Truncated
}

// classifyGoProbe turns one `go version` result into an observation.
func classifyGoProbe(r executor.Result, exec *string) envdoc.Observation {
	return classifyProbe("go", r, exec, ParseGoVersion)
}

// classifyProbe turns one version probe result into an observation.
func classifyProbe(tool string, r executor.Result, exec *string, parse func([]byte) (string, bool)) envdoc.Observation {
	switch {
	case r.Cleanup == executor.CleanupUncertain:
		return probeFailed(tool, DetailCleanupUncertain, exec)
	case r.Status == executor.StatusTimedOut:
		return probeFailed(tool, DetailTimedOut, exec)
	case r.Status == executor.StatusCancelled:
		return probeFailed(tool, DetailCancelled, exec)
	case r.Status != executor.StatusExited || r.ExitCode == nil:
		return probeFailed(tool, DetailStartFailed, exec)
	case *r.ExitCode != 0:
		return probeFailed(tool, DetailExitNonZero, exec)
	case r.Stdout.Truncated || r.Stdout.Incomplete:
		return probeFailed(tool, DetailOutputUnrecognized, exec)
	}
	v, ok := parse(r.Stdout.Data)
	if !ok {
		return probeFailed(tool, DetailOutputUnrecognized, exec)
	}
	return envdoc.Observation{Tool: tool, Status: envdoc.ObsObserved, Version: &v, Execution: exec}
}

// ParseGoVersion reads "go version go1.27.4 linux/amd64" and returns
// "1.27.4". Development builds and anything go/version cannot order are
// rejected.
func ParseGoVersion(stdout []byte) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != "go" || f[1] != "version" || !strings.HasPrefix(f[2], "go") {
		return "", false
	}
	v := strings.TrimPrefix(f[2], "go")
	if !envdoc.ComparableObservation("go", v) {
		return "", false
	}
	return v, true
}

// StaticObserver reports fixed versions without running anything (tests and
// fixtures). Its observations name no execution, so the report decoder,
// which requires an observed version to be backed by a recorded probe,
// rejects them in a published run. Tools absent from Versions are not_probed.
type StaticObserver struct{ Versions map[string]string }

// Observe implements Observer.
func (s StaticObserver) Observe(_ context.Context, _ analysis.Job, tools []string, _ executor.Runner) Observed {
	var out Observed
	for _, tool := range sortedUnique(tools) {
		v, ok := s.Versions[tool]
		if !ok {
			out.Observations = append(out.Observations, notProbed(tool))
			continue
		}
		if !envdoc.ComparableObservation(tool, v) {
			out.Observations = append(out.Observations, probeFailed(tool, DetailOutputUnrecognized, nil))
			continue
		}
		vv := v
		out.Observations = append(out.Observations, envdoc.Observation{Tool: tool, Status: envdoc.ObsObserved, Version: &vv})
	}
	return out
}
