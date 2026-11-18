package discovery

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// nodeProbe is the build's node toolchain probe for path ("" = unset).
func nodeProbe(t *testing.T, path string) pack.ToolProbe {
	t.Helper()
	cfg := builtin.Config{}
	if path != "" {
		cfg.Tools = map[string]string{"node": path}
	}
	for _, p := range builtin.ToolchainProbes(cfg) {
		if p.Tool == "node" {
			return p
		}
	}
	t.Fatal("no node toolchain probe")
	return pack.ToolProbe{}
}

func verdictOf(d *envdoc.Document, module, tool string) *envdoc.Verdict {
	for i, v := range d.Environment.Verdicts {
		if v.Module == module && v.Tool == tool {
			return &d.Environment.Verdicts[i]
		}
	}
	return nil
}

// docs/39 §2.4: a configured shared toolchain is a runtime observation like
// go, probed with its version argv through the executor (PATH is its own
// directory); unset it stays not_probed and nothing is started.
func TestToolchainObservedAsRuntime(t *testing.T) {
	goExe, node := fakeGo(t, 0o755), fakeTool(t, "node")
	runner := &fakeRunner{result: func(s executor.Spec) executor.Result {
		if s.Executable == goExe {
			return exited(0, "go version go1.30.1 linux/amd64\n")
		}
		return exited(0, "v24.18.0\n")
	}}
	j := probeJob(t) // go 1.30 and engines.node >=18
	res, d := runEnv(t, j, ProbeObserver{GoExecutable: goExe, Toolchains: []pack.ToolProbe{nodeProbe(t, node)}}, runner)
	if res.Status != domain.TaskSucceeded || d == nil {
		t.Fatalf("%+v", res)
	}
	o := observation(t, d, "node")
	if o.Status != envdoc.ObsObserved || o.Version == nil || *o.Version != "24.18.0" || o.Execution == nil || *o.Execution != "node.version" {
		t.Fatalf("node %+v", o)
	}
	if v := verdictOf(d, "javascript:.", "node"); v == nil || v.Status != envdoc.VerdictSatisfied {
		t.Fatalf("node verdict %+v", v)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("specs %d", len(runner.specs))
	}
	var spec executor.Spec
	for _, s := range runner.specs {
		if s.Executable == node {
			spec = s
		}
	}
	resolved, err := nativepath.Resolve(node)
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := map[string]string{"PATH": filepath.Dir(resolved), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			wantEnv["SystemRoot"] = v
		}
	}
	if !slices.Equal(spec.Args, []string{"--version"}) || spec.Timeout <= 0 || len(spec.Env) != len(wantEnv) {
		t.Fatalf("node spec %+v", spec)
	}
	for k, v := range wantEnv {
		if spec.Env[k] != v {
			t.Fatalf("node env %v", spec.Env)
		}
	}

	// Unset: not_probed, never started.
	runner.specs = nil
	_, d = runEnv(t, j, ProbeObserver{GoExecutable: goExe, Toolchains: []pack.ToolProbe{nodeProbe(t, "")}}, runner)
	if o := observation(t, d, "node"); o.Status != envdoc.ObsNotProbed || o.Version != nil || len(runner.specs) != 1 {
		t.Fatalf("unset node %+v specs %d", o, len(runner.specs))
	}
	if !slices.Contains(d.Limitations, LimToolNotProbed) {
		t.Fatalf("limitations %v", d.Limitations)
	}

	// A version node cannot have printed is never observed.
	_, d = runEnv(t, j, ProbeObserver{GoExecutable: goExe, Toolchains: []pack.ToolProbe{nodeProbe(t, node)}}, &fakeRunner{result: func(s executor.Spec) executor.Result {
		if s.Executable == goExe {
			return exited(0, "go version go1.30.1 linux/amd64\n")
		}
		return exited(0, "v24.18.0-nightly\n")
	}})
	if o := observation(t, d, "node"); o.Status != envdoc.ObsProbeFailed || o.Version != nil || detail(o) != DetailOutputUnrecognized {
		t.Fatalf("nightly %+v", o)
	}
}

// A toolchain that an analyzer of an in-scope ecosystem needs is observed
// even when no module declares that runtime (Observed.Needed); it adds an
// observation, never a verdict. Other ecosystems do not pull it in.
func TestToolchainNeededByAnalyzerEcosystem(t *testing.T) {
	node := fakeTool(t, "node")
	runner := &fakeRunner{result: func(executor.Spec) executor.Result { return exited(0, "v24.18.0\n") }}
	goOnly := jobOf(t, map[string]string{"go.mod": "module x\n\ngo 1.30\n", "a.go": "package x\n"}, domain.Scope{Kind: "full"})
	for _, c := range []struct {
		name     string
		ecos     []string
		observed bool
	}{
		{"any", []string{"any"}, true},
		{"python only", []string{"python"}, false},
		{"go", []string{"go"}, true},
	} {
		runner.specs = nil
		p := nodeProbe(t, node)
		p.Analyzers, p.Ecosystems = []string{"x.fake"}, c.ecos
		res, d := runEnv(t, goOnly, ProbeObserver{Toolchains: []pack.ToolProbe{p}}, runner)
		if res.Status != domain.TaskSucceeded || d == nil {
			t.Fatalf("%s: %+v", c.name, res)
		}
		var found *envdoc.Observation
		for i := range d.Environment.Observations {
			if d.Environment.Observations[i].Tool == "node" {
				found = &d.Environment.Observations[i]
			}
		}
		if c.observed != (found != nil) || (found != nil && found.Status != envdoc.ObsObserved) || len(runner.specs) != map[bool]int{true: 1, false: 0}[c.observed] {
			t.Fatalf("%s: node %+v specs %d", c.name, found, len(runner.specs))
		}
		for _, v := range d.Environment.Verdicts {
			if v.Tool == "node" {
				t.Fatalf("%s: node verdict for a go module %+v", c.name, v)
			}
		}
	}
}

// docs/39 §2.4: a tool run by an interpreter toolchain is probed as
// <interpreter> <tool path> <args> with PATH set to the interpreter's
// directory, never through the script's #! line; without the interpreter
// nothing is started.
func TestInterpreterToolProbe(t *testing.T) {
	node := fakeTool(t, "node")
	script := filepath.Join(t.TempDir(), "fakelint.js")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := func(interp string) pack.ToolProbe {
		return pack.ToolProbe{Tool: "fakelint", Analyzers: []string{"javascript.fakelint"}, Ecosystems: []string{"javascript"}, Executable: script, Args: []string{"--version"},
			ParseVersion: func(b []byte) (string, bool) { return parseSC([]byte("staticcheck " + string(b))) }, Interpreter: "node", InterpreterExecutable: interp}
	}
	runner := &fakeRunner{result: func(executor.Spec) executor.Result { return exited(0, "2026.1.0 (1.0.0)\n") }}
	j := probeJob(t)
	_, d := runEnv(t, j, ProbeObserver{Analyzers: []pack.ToolProbe{probe(node)}}, runner)
	at := analyzerTool(t, d, "fakelint")
	if at == nil || at.Status != envdoc.ObsObserved || at.Version == nil || *at.Version != "2026.1.0 (1.0.0)" || len(runner.specs) != 1 {
		t.Fatalf("interpreted tool %+v specs %d", at, len(runner.specs))
	}
	resolved, err := nativepath.Resolve(node)
	if err != nil {
		t.Fatal(err)
	}
	s := runner.specs[0]
	if s.Executable != node || !slices.Equal(s.Args, []string{script, "--version"}) || s.Env["PATH"] != filepath.Dir(resolved) {
		t.Fatalf("interpreter spec %+v", s)
	}
	for detailWant, interp := range map[string]string{DetailToolchainNotConfigured: "", DetailToolchainUnavailable: filepath.Join(t.TempDir(), "absent")} {
		runner.specs = nil
		_, d := runEnv(t, j, ProbeObserver{Analyzers: []pack.ToolProbe{probe(interp)}}, runner)
		at := analyzerTool(t, d, "fakelint")
		if at == nil || at.Status != envdoc.ObsProbeFailed || at.Detail == nil || *at.Detail != detailWant || at.Execution != nil || len(runner.specs) != 0 {
			t.Fatalf("%s: %+v specs %d", detailWant, at, len(runner.specs))
		}
	}
}

var nodeObserved = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// With CODEYE_TEST_NODE the real node is observed through the W05 executor
// and compared with engines.node.
func TestToolchainProbeRealNode(t *testing.T) {
	node := os.Getenv("CODEYE_TEST_NODE")
	if node == "" {
		t.Skip("set CODEYE_TEST_NODE to the node binary")
	}
	res, d := runEnv(t, probeJob(t), ProbeObserver{Toolchains: []pack.ToolProbe{nodeProbe(t, node)}}, &executor.Executor{})
	if res.Status != domain.TaskSucceeded || d == nil {
		t.Fatalf("%+v", res)
	}
	o := observation(t, d, "node")
	if o.Status != envdoc.ObsObserved || o.Version == nil || !nodeObserved.MatchString(*o.Version) {
		t.Fatalf("real node %+v", o)
	}
	if v := verdictOf(d, "javascript:.", "node"); v == nil || v.Status != envdoc.VerdictSatisfied {
		t.Fatalf("real node verdict %+v", v)
	}
}
