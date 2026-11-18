package discovery

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func fakeGo(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "go")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	if err := os.WriteFile(p, []byte("fake"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func probeJob(t *testing.T) analysis.Job {
	return jobOf(t, map[string]string{"go.mod": "module x\n\ngo 1.30\n", "package.json": `{"engines":{"node":">=18"}}`}, domain.Scope{Kind: "full"})
}

func runEnv(t *testing.T, j analysis.Job, obs Observer, r executor.Runner) (analysis.TaskResult, *envdoc.Document) {
	t.Helper()
	j.Scratch = t.TempDir()
	h, err := Wrap(noopHandler{}, Config{Observer: obs})
	if err != nil {
		t.Fatal(err)
	}
	res := h.Run(context.Background(), j, Tasks()[0], r)
	if len(res.Artifacts) == 0 {
		return res, nil
	}
	b, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := envdoc.Decode(b, input(j).Expect(envdoc.KindEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Executions {
		if !strings.HasPrefix(e.StdoutPath, j.Scratch) {
			t.Fatalf("execution stream outside scratch: %s", e.StdoutPath)
		}
	}
	return res, &d
}

func observation(t *testing.T, d *envdoc.Document, tool string) envdoc.Observation {
	t.Helper()
	for _, o := range d.Environment.Observations {
		if o.Tool == tool {
			return o
		}
	}
	t.Fatalf("no %s observation", tool)
	return envdoc.Observation{}
}

func detail(o envdoc.Observation) string {
	if o.Detail == nil {
		return ""
	}
	return *o.Detail
}

func TestProbeStatuses(t *testing.T) {
	goExe := fakeGo(t, 0o755)
	never := &fakeRunner{result: func(executor.Spec) executor.Result { t.Fatal("must not run"); return executor.Result{} }}
	cases := []struct {
		name       string
		obs        Observer
		runner     *fakeRunner
		status     domain.TaskStatus
		reason     string
		goStatus   string
		goDetail   string
		goVersion  string
		goVerdict  string
		executions int
	}{
		{name: "unconfigured", obs: ProbeObserver{}, runner: never, status: domain.TaskSucceeded, goStatus: envdoc.ObsNotProbed, goVerdict: envdoc.VerdictUnknown},
		{name: "nil observer", obs: nil, runner: never, status: domain.TaskSucceeded, goStatus: envdoc.ObsNotProbed, goVerdict: envdoc.VerdictUnknown},
		{name: "missing binary", obs: ProbeObserver{GoExecutable: filepath.Join(t.TempDir(), "absent", "go")}, runner: never, status: domain.TaskSucceeded, goStatus: envdoc.ObsNotFound, goVerdict: envdoc.VerdictUnknown},
		{name: "observed", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result { return exited(0, "go version go1.27.4 linux/amd64\n") }},
			status: domain.TaskSucceeded, goStatus: envdoc.ObsObserved, goVersion: "1.27.4", goVerdict: envdoc.VerdictMismatch, executions: 1},
		{name: "non-zero exit", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result { return exited(2, "") }},
			status: domain.TaskSucceeded, goStatus: envdoc.ObsProbeFailed, goDetail: DetailExitNonZero, goVerdict: envdoc.VerdictUnknown, executions: 1},
		{name: "unparsable", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result { return exited(0, "go version devel go1.28-abc linux/amd64\n") }},
			status: domain.TaskSucceeded, goStatus: envdoc.ObsProbeFailed, goDetail: DetailOutputUnrecognized, goVerdict: envdoc.VerdictUnknown, executions: 1},
		{name: "timeout", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result {
			return executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupConfirmed, Process: executor.ProcessIdentity{PID: 9}}
		}}, status: domain.TaskFailed, reason: ReasonProbeIncomplete, goStatus: envdoc.ObsProbeFailed, goDetail: DetailTimedOut, goVerdict: envdoc.VerdictUnknown, executions: 1},
		{name: "start refused", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result {
			return executor.Result{Status: executor.StatusRejected, Reason: "executable_not_regular", Cleanup: executor.CleanupNotStarted}
		}}, status: domain.TaskFailed, reason: ReasonProbeIncomplete, goStatus: envdoc.ObsProbeFailed, goDetail: DetailStartFailed, goVerdict: envdoc.VerdictUnknown, executions: 1},
		{name: "cleanup uncertain", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result {
			r := exited(0, "go version go1.27.4 linux/amd64\n")
			r.Cleanup = executor.CleanupUncertain
			return r
		}}, status: domain.TaskFailed, reason: ReasonProbeIncomplete, goStatus: envdoc.ObsProbeFailed, goDetail: DetailCleanupUncertain, goVerdict: envdoc.VerdictUnknown, executions: 1},
		{name: "truncated output", obs: ProbeObserver{GoExecutable: goExe}, runner: &fakeRunner{result: func(executor.Spec) executor.Result {
			r := exited(0, "go version go1.27.4 linux/amd64\n")
			r.Stdout.Truncated = true
			return r
		}}, status: domain.TaskFailed, reason: ReasonProbeIncomplete, goStatus: envdoc.ObsProbeFailed, goDetail: DetailOutputUnrecognized, goVerdict: envdoc.VerdictUnknown, executions: 1},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			name       string
			obs        Observer
			runner     *fakeRunner
			status     domain.TaskStatus
			reason     string
			goStatus   string
			goDetail   string
			goVersion  string
			goVerdict  string
			executions int
		}{name: "not executable", obs: ProbeObserver{GoExecutable: fakeGo(t, 0o644)}, runner: never, status: domain.TaskSucceeded, goStatus: envdoc.ObsProbeFailed, goDetail: DetailNotExecutable, goVerdict: envdoc.VerdictUnknown})
	}
	j := probeJob(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.runner.specs = nil
			res, d := runEnv(t, j, c.obs, c.runner)
			if res.Status != c.status || res.Reason != c.reason || len(res.Executions) != c.executions || d == nil {
				t.Fatalf("result %s/%s executions=%d doc=%v", res.Status, res.Reason, len(res.Executions), d != nil)
			}
			o := observation(t, d, "go")
			if o.Status != c.goStatus || detail(o) != c.goDetail || (c.goVersion != "" && *o.Version != c.goVersion) {
				t.Fatalf("go observation %+v", o)
			}
			if c.executions > 0 && (o.Execution == nil || *o.Execution != ExecGoVersion || res.Executions[0].Name != ExecGoVersion) {
				t.Fatalf("probe execution not named: %+v", o)
			}
			// Node is never configured: not_probed, never another host's value.
			if n := observation(t, d, "node"); n.Status != envdoc.ObsNotProbed || n.Version != nil {
				t.Fatalf("node observation %+v", n)
			}
			for _, v := range d.Environment.Verdicts {
				switch v.Tool {
				case "go":
					if v.Status != c.goVerdict {
						t.Fatalf("go verdict %+v", v)
					}
				case "node":
					if v.Status != envdoc.VerdictUnknown || *v.Reason != envdoc.ReasonNotObserved {
						t.Fatalf("node verdict %+v", v)
					}
				}
			}
			for _, s := range c.runner.specs {
				if s.Executable != goExe || len(s.Args) != 1 || s.Args[0] != "version" || s.Env["GOTOOLCHAIN"] != "local" || s.Env["GOPROXY"] != "off" || s.Env["GOENV"] != "off" || s.Timeout <= 0 {
					t.Fatalf("probe spec %+v", s)
				}
				if strings.HasPrefix(s.Dir, j.Snapshot.Root) || len(s.ProtectedRoots) != 1 || s.ProtectedRoots[0] != j.Snapshot.SnapshotDir {
					t.Fatalf("probe ran in the snapshot or unprotected: %+v", s)
				}
			}
		})
	}
}

func TestProbeCancelled(t *testing.T) {
	j := probeJob(t)
	r := &fakeRunner{result: func(executor.Spec) executor.Result {
		return executor.Result{Status: executor.StatusCancelled, Cleanup: executor.CleanupConfirmed, Process: executor.ProcessIdentity{PID: 3}}
	}}
	res, _ := runEnv(t, j, ProbeObserver{GoExecutable: fakeGo(t, 0o755)}, r)
	if res.Status != domain.TaskCancelled || len(res.Executions) != 1 {
		t.Fatalf("cancelled probe: %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, _ := Wrap(noopHandler{}, Config{})
	for _, task := range Tasks() {
		if res := h.Run(ctx, j, task, nil); res.Status != domain.TaskCancelled {
			t.Fatalf("%s with cancelled ctx: %+v", task.Key, res)
		}
	}
}

func TestParseGoVersion(t *testing.T) {
	for in, want := range map[string]string{
		"go version go1.27.4 linux/amd64\n":                "1.27.4",
		"go version go1.22rc1 windows/amd64\r\n":           "1.22rc1",
		"go version go1.27.4 X:boringcrypto linux/amd64\n": "1.27.4",
		"go version devel go1.28-abc linux/amd64\n":        "",
		"go1.27.4\n": "",
		"":           "",
	} {
		got, ok := ParseGoVersion([]byte(in))
		if got != want || ok != (want != "") {
			t.Errorf("ParseGoVersion(%q) = %q %v", in, got, ok)
		}
	}
}

// The real executor runs the operator-configured go binary offline in scratch.
func TestProbeRealGo(t *testing.T) {
	goExe := testGo(t)
	j := probeJob(t)
	res, d := runEnv(t, j, ProbeObserver{GoExecutable: goExe}, &executor.Executor{})
	if res.Status != domain.TaskSucceeded || d == nil {
		t.Fatalf("%+v", res)
	}
	o := observation(t, d, "go")
	if o.Status != envdoc.ObsObserved || !envdoc.ComparableObservation("go", *o.Version) || (os.Getenv("CODEYE_TEST_GO") == "" && !strings.HasPrefix(runtime.Version(), "go"+*o.Version)) {
		t.Fatalf("real go observation %+v (runtime %s)", o, runtime.Version())
	}
}
