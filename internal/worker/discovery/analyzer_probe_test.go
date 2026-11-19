package discovery

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

var scVersion = regexp.MustCompile(`^staticcheck ([0-9]{4}\.[0-9.]+ \([0-9.]+\))$`)

func parseSC(b []byte) (string, bool) {
	m := scVersion.FindStringSubmatch(strings.TrimSpace(string(b)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

func scProbe(exe string) pack.ToolProbe {
	return pack.ToolProbe{Tool: "staticcheck", Analyzers: []string{"go.staticcheck"}, Ecosystems: []string{"go"}, Executable: exe, Args: []string{"-version"}, ParseVersion: parseSC}
}

func fakeTool(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	if err := os.WriteFile(p, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func analyzerTool(t *testing.T, d *envdoc.Document, tool string) *envdoc.AnalyzerTool {
	t.Helper()
	for i := range d.Environment.AnalyzerTools {
		if d.Environment.AnalyzerTools[i].Tool == tool {
			return &d.Environment.AnalyzerTools[i]
		}
	}
	return nil
}

// docs/ANALYZERS.md discovery hook: env.discover probes every configured analyzer tool
// of the snapshot's ecosystems with its version argv through the executor
// (explicit environment, scratch cwd, timeout); unconfigured ones stay
// not_probed and nothing is searched in PATH.
func TestAnalyzerToolProbes(t *testing.T) {
	goExe, sc := fakeGo(t, 0o755), fakeTool(t, "staticcheck")
	reply := map[string]executor.Result{}
	runner := &fakeRunner{result: func(s executor.Spec) executor.Result {
		if s.Executable == goExe {
			return exited(0, "go version go1.27.4 linux/amd64\n")
		}
		return reply["staticcheck"]
	}}
	reply["staticcheck"] = exited(0, "staticcheck 2026.2.2 (0.8.2)\n")
	j := probeJob(t)
	res, d := runEnv(t, j, ProbeObserver{GoExecutable: goExe, Analyzers: []pack.ToolProbe{scProbe(sc)}}, runner)
	if res.Status != domain.TaskSucceeded || d == nil {
		t.Fatalf("%+v", res)
	}
	at := analyzerTool(t, d, "staticcheck")
	if at == nil || at.Status != envdoc.ObsObserved || at.Version == nil || *at.Version != "2026.2.2 (0.8.2)" || !slices.Equal(at.Analyzers, []string{"go.staticcheck"}) || at.Execution == nil || *at.Execution != "staticcheck.version" {
		t.Fatalf("analyzer tool %+v", at)
	}
	var names []string
	for _, e := range res.Executions {
		names = append(names, e.Name)
	}
	if !slices.Contains(names, "staticcheck.version") || len(runner.specs) != 2 {
		t.Fatalf("executions %v specs %d", names, len(runner.specs))
	}
	spec := runner.specs[1]
	resolved, err := nativepath.Resolve(sc)
	if err != nil {
		t.Fatal(err)
	}
	// PATH is the directory of the resolved executable (Windows temp dirs
	// may resolve to another spelling than t.TempDir returns).
	wantEnv := map[string]string{"PATH": filepath.Dir(resolved), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			wantEnv["SystemRoot"] = v
		}
	}
	if spec.Executable != sc || !slices.Equal(spec.Args, []string{"-version"}) || spec.Timeout <= 0 || !strings.HasPrefix(spec.Dir, j.Scratch) && !strings.HasPrefix(spec.Dir, filepath.Dir(spec.TempParent)) {
		t.Fatalf("probe spec %+v", spec)
	}
	if len(spec.Env) != len(wantEnv) {
		t.Fatalf("probe env %v", spec.Env)
	}
	for k, v := range wantEnv {
		if spec.Env[k] != v {
			t.Fatalf("probe env %v", spec.Env)
		}
	}

	// Unconfigured: not_probed, never started.
	runner.specs = nil
	_, d = runEnv(t, j, ProbeObserver{GoExecutable: goExe, Analyzers: []pack.ToolProbe{scProbe("")}}, runner)
	if at := analyzerTool(t, d, "staticcheck"); at == nil || at.Status != envdoc.ObsNotProbed || at.Version != nil || at.Execution != nil || len(runner.specs) != 1 {
		t.Fatalf("unconfigured %+v specs %d", at, len(runner.specs))
	}

	// Failures are recorded, never an observed version.
	for name, c := range map[string]struct {
		r      executor.Result
		detail string
		status domain.TaskStatus
	}{
		"exit non-zero": {exited(2, "usage\n"), DetailExitNonZero, domain.TaskSucceeded},
		"devel build":   {exited(0, "staticcheck (devel, v0.0.0-00010101000000-000000000000)\n"), DetailOutputUnrecognized, domain.TaskSucceeded},
		"timeout":       {executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupConfirmed, Process: executor.ProcessIdentity{PID: 9}}, DetailTimedOut, domain.TaskFailed},
	} {
		reply["staticcheck"] = c.r
		res, d := runEnv(t, j, ProbeObserver{GoExecutable: goExe, Analyzers: []pack.ToolProbe{scProbe(sc)}}, runner)
		if res.Status != c.status {
			t.Fatalf("%s: status %s", name, res.Status)
		}
		if d == nil {
			continue
		}
		at := analyzerTool(t, d, "staticcheck")
		if at == nil || at.Status != envdoc.ObsProbeFailed || at.Version != nil || at.Detail == nil || *at.Detail != c.detail {
			t.Fatalf("%s: %+v", name, at)
		}
	}

	// A missing configured file is not_found without starting anything.
	runner.specs = nil
	_, d = runEnv(t, j, ProbeObserver{GoExecutable: goExe, Analyzers: []pack.ToolProbe{scProbe(filepath.Join(t.TempDir(), "absent"))}}, runner)
	if at := analyzerTool(t, d, "staticcheck"); at == nil || at.Status != envdoc.ObsNotFound || len(runner.specs) != 1 {
		t.Fatalf("missing %+v", at)
	}
}

// A snapshot without Go needs no Go analyzer tool: nothing is recorded or run.
func TestAnalyzerToolProbesFollowEcosystems(t *testing.T) {
	never := &fakeRunner{result: func(executor.Spec) executor.Result { t.Fatal("must not run"); return executor.Result{} }}
	j := jobOf(t, map[string]string{"pyproject.toml": "[project]\nname = \"x\"\nrequires-python = \">=3.10\"\n", "m.py": "print(1)\n"}, domain.Scope{Kind: "full"})
	res, d := runEnv(t, j, ProbeObserver{Analyzers: []pack.ToolProbe{scProbe(fakeTool(t, "staticcheck"))}}, never)
	if res.Status != domain.TaskSucceeded || d == nil || len(d.Environment.AnalyzerTools) != 0 {
		t.Fatalf("python snapshot: %+v %+v", res, d)
	}
}

// With CODEYE_TEST_STATICCHECK the real binary is probed through the W05
// executor.
func TestAnalyzerToolProbeRealStaticcheck(t *testing.T) {
	sc := os.Getenv("CODEYE_TEST_STATICCHECK")
	if sc == "" {
		t.Skip("set CODEYE_TEST_STATICCHECK to the staticcheck binary")
	}
	res, d := runEnv(t, probeJob(t), ProbeObserver{Analyzers: []pack.ToolProbe{scProbe(sc)}}, &executor.Executor{})
	at := analyzerTool(t, d, "staticcheck")
	if res.Status != domain.TaskSucceeded || at == nil || at.Status != envdoc.ObsObserved || at.Version == nil || !strings.HasPrefix(*at.Version, "2026.") {
		t.Fatalf("real staticcheck probe %+v %+v", res, at)
	}
}
