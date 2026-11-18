package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
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

	// ExecGoVersion is the execution name of the Go probe.
	ExecGoVersion = "go.version"
	probeTimeout  = 30 * time.Second
)

// Observed is the result of observing the node's tools for one attempt.
type Observed struct {
	Observations []envdoc.Observation
	Executions   []analysis.Execution
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
// executor.Runner. Only Go is configurable today (the agent's --go
// executable, the same one the go-vet handler uses); every other tool is
// not_probed.
type ProbeObserver struct {
	GoExecutable string
	Timeout      time.Duration
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
	for _, tool := range sortedUnique(tools) {
		if tool != "go" || p.GoExecutable == "" {
			out.Observations = append(out.Observations, notProbed(tool))
			continue
		}
		o, exec, incomplete, cancelled, err := p.probeGo(ctx, job, runner)
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
	res := runner.Run(ctx, executor.Spec{Executable: p.GoExecutable, Args: []string{"version"}, WorkspaceRoot: ws, Dir: ws,
		ProtectedRoots: protected, TempParent: filepath.Join(work, "tmp"), Env: goEnv(resolved, work), Timeout: timeout,
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
	switch {
	case r.Cleanup == executor.CleanupUncertain:
		return probeFailed("go", DetailCleanupUncertain, exec)
	case r.Status == executor.StatusTimedOut:
		return probeFailed("go", DetailTimedOut, exec)
	case r.Status == executor.StatusCancelled:
		return probeFailed("go", DetailCancelled, exec)
	case r.Status != executor.StatusExited || r.ExitCode == nil:
		return probeFailed("go", DetailStartFailed, exec)
	case *r.ExitCode != 0:
		return probeFailed("go", DetailExitNonZero, exec)
	case r.Stdout.Truncated || r.Stdout.Incomplete:
		return probeFailed("go", DetailOutputUnrecognized, exec)
	}
	v, ok := ParseGoVersion(r.Stdout.Data)
	if !ok {
		return probeFailed("go", DetailOutputUnrecognized, exec)
	}
	return envdoc.Observation{Tool: "go", Status: envdoc.ObsObserved, Version: &v, Execution: exec}
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
