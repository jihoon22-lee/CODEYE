package builtin

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func versionLine(b []byte) (string, bool) { s := strings.TrimSpace(string(b)); return s, s != "" }

// stubAnalyzer plans one task (repository code when code is set) and
// succeeds without starting anything.
type stubAnalyzer struct {
	d    pack.Descriptor
	code bool
}

func (s stubAnalyzer) Descriptor() pack.Descriptor { return s.d }

func (s stubAnalyzer) Plan(context.Context, analysis.Job) ([]pack.Task, error) {
	return []pack.Task{{Spec: analysis.TaskSpec{Key: s.d.Name, Required: s.d.Required, Title: s.d.Name, Stage: domain.StageStaticAnalysis}, Module: ".", RepositoryCode: s.code}}, nil
}

func (s stubAnalyzer) Run(context.Context, analysis.Job, analysis.TaskSpec, executor.Runner) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

// fakeCatalog is a baseline analyzer that needs nothing and a JavaScript
// analyzer whose tool script runs under the node toolchain. built records
// the handles the JS build function received (nil = never called).
type fakeCatalog struct {
	code  bool
	built map[string]pack.Toolchain
	calls int
}

func (f *fakeCatalog) entries() []entry {
	base := pack.Descriptor{ID: "go.fakebase", Name: "fakebase", Ecosystem: "go", Required: true, Tool: "fakebase", VersionArgs: []string{"-v"}, ParseVersion: versionLine}
	js := pack.Descriptor{ID: "javascript.fakelint", Name: "fakelint", Ecosystem: "javascript", Tool: "fakelint", VersionArgs: []string{"--version"}, ParseVersion: versionLine,
		Toolchains: []string{"node"}, Interpreter: "node"}
	return []entry{
		{desc: base, plan: stubAnalyzer{d: base}.Plan, build: func(context.Context, Config, executor.Runner, *govet.Handler, map[string]pack.Toolchain) built {
			return built{analyzer: stubAnalyzer{d: base}}
		}},
		{desc: js, plan: stubAnalyzer{d: js, code: f.code}.Plan, build: func(_ context.Context, _ Config, _ executor.Runner, _ *govet.Handler, tc map[string]pack.Toolchain) built {
			f.calls++
			f.built = tc
			return built{analyzer: stubAnalyzer{d: js, code: f.code}}
		}},
	}
}

// nodeRunner answers the node version probe and records every spec.
type nodeRunner struct {
	specs  []executor.Spec
	result executor.Result
}

func (r *nodeRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	r.specs = append(r.specs, s)
	return r.result
}

func exitedWith(code int, stdout string) executor.Result {
	return executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed, Stdout: executor.Stream{Data: []byte(stdout), TotalBytes: int64(len(stdout))}}
}

func fakeNode(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "node")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	if err := os.WriteFile(p, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func stubJob(t *testing.T) analysis.Job {
	t.Helper()
	src, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := source.AcquireLocal(context.Background(), src, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := source.Load(context.Background(), snap.Root, snap.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: "r", AttemptID: "a", Epoch: 1, Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, Snapshot: loaded, Scratch: t.TempDir()}
}

// runFakelint runs the JS analyzer's task through the composite.
func runFakelint(t *testing.T, c *pack.Composite, allowCode bool) analysis.TaskResult {
	t.Helper()
	j := stubJob(t)
	j.AllowRepositoryCode = allowCode
	return c.Run(context.Background(), j, analysis.TaskSpec{Key: "fakelint"}, nil)
}

// docs/39 §2.4 (TC-101): an analyzer that needs the node toolchain is built
// only with a configured node that passed its startup version probe, and
// then receives the validated handle; otherwise it is the NotConfigured
// stand-in. Precedence: tool, toolchain not configured, toolchain
// unavailable; the repository-code grant precedes all of them.
func TestToolchainGatesAnalyzer(t *testing.T) {
	ctx := context.Background()
	node, lint := fakeNode(t), filepath.Join(t.TempDir(), "fakelint.js")
	ok := &nodeRunner{result: exitedWith(0, "v24.18.0\n")}
	for _, c := range []struct {
		name    string
		tools   map[string]string
		runner  *nodeRunner
		code    bool
		missing []string
		reason  string // "" = built
		probes  int
	}{
		{"tool not configured", map[string]string{"node": node}, ok, false, []string{"fakelint:not_configured"}, pack.ReasonToolNotConfigured, 1},
		{"tool before toolchain", map[string]string{}, ok, false, []string{"fakelint:not_configured"}, pack.ReasonToolNotConfigured, 0},
		{"toolchain not configured", map[string]string{"fakelint": lint}, ok, false, []string{"fakelint:toolchain_not_configured"}, pack.ReasonToolchainNotConfigured, 0},
		{"relative toolchain", map[string]string{"fakelint": lint, "node": "bin/node"}, ok, false, []string{"node:invalid_config", "fakelint:toolchain_unavailable"}, pack.ReasonToolUnavailable, 0},
		{"missing toolchain", map[string]string{"fakelint": lint, "node": filepath.Join(t.TempDir(), "node")}, ok, false, []string{"node:invalid_config", "fakelint:toolchain_unavailable"}, pack.ReasonToolUnavailable, 0},
		{"probe exit", map[string]string{"fakelint": lint, "node": node}, &nodeRunner{result: exitedWith(1, "")}, false, []string{"node:probe_failed", "fakelint:toolchain_unavailable"}, pack.ReasonToolUnavailable, 1},
		{"pre-release", map[string]string{"fakelint": lint, "node": node}, &nodeRunner{result: exitedWith(0, "v25.0.0-rc.1\n")}, false, []string{"node:probe_failed", "fakelint:toolchain_unavailable"}, pack.ReasonToolUnavailable, 1},
		{"cleanup uncertain", map[string]string{"fakelint": lint, "node": node}, &nodeRunner{result: executor.Result{Status: executor.StatusExited, ExitCode: new(int), Cleanup: executor.CleanupUncertain, Stdout: executor.Stream{Data: []byte("v24.18.0\n")}}}, false, []string{"node:probe_failed", "fakelint:toolchain_unavailable"}, pack.ReasonToolUnavailable, 1},
		{"grant first", map[string]string{"fakelint": lint}, ok, true, []string{"fakelint:toolchain_not_configured"}, pack.ReasonRepositoryCodeNotAllowed, 0},
		{"configured", map[string]string{"fakelint": lint, "node": node}, ok, false, nil, "", 1},
	} {
		c.runner.specs = nil
		f := &fakeCatalog{code: c.code}
		comp, missing, err := compose(ctx, Config{Tools: c.tools, ProbeDir: t.TempDir()}, c.runner, f.entries())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		// A toolchain is never an unknown tool.
		if got := reasons(missing); !slices.Equal(got, c.missing) {
			t.Fatalf("%s: missing %v", c.name, got)
		}
		for _, m := range missing {
			if strings.Contains(m.Detail, string(filepath.Separator)) {
				t.Fatalf("%s: detail names a path: %q", c.name, m.Detail)
			}
		}
		if len(c.runner.specs) != c.probes {
			t.Fatalf("%s: %d probes", c.name, len(c.runner.specs))
		}
		r := runFakelint(t, comp, false)
		if c.reason == "" {
			if r.Status != domain.TaskSucceeded || f.calls != 1 {
				t.Fatalf("%s: not built: %+v calls %d", c.name, r, f.calls)
			}
			continue
		}
		if f.calls != 0 || r.Status != domain.TaskUnsupported || r.Reason != c.reason {
			t.Fatalf("%s: %+v calls %d", c.name, r, f.calls)
		}
		if c.code && !slices.Contains(r.Limitations, pack.ReasonToolchainNotConfigured) {
			t.Fatalf("%s: second reason lost: %v", c.name, r.Limitations)
		}
	}
}

// The validated handle is the operator path, the resolved directory (the
// only PATH entry) and the parsed version; the probe ran once through the
// executor with the tool-probe environment and left nothing behind.
func TestToolchainHandleAndProbeSpec(t *testing.T) {
	node, lint, probe := fakeNode(t), filepath.Join(t.TempDir(), "fakelint.js"), t.TempDir()
	r := &nodeRunner{result: exitedWith(0, "v24.18.0\n")}
	f := &fakeCatalog{}
	if _, missing, err := compose(context.Background(), Config{Tools: map[string]string{"fakelint": lint, "node": node}, ProbeDir: probe}, r, f.entries()); err != nil || len(missing) != 0 {
		t.Fatalf("%v %+v", err, missing)
	}
	resolved, err := nativepath.Resolve(node)
	if err != nil {
		t.Fatal(err)
	}
	want := pack.Toolchain{Name: "node", Executable: node, Dir: filepath.Dir(resolved), Version: "24.18.0"}
	if len(f.built) != 1 || f.built["node"] != want {
		t.Fatalf("handles %+v want %+v", f.built, want)
	}
	if len(r.specs) != 1 {
		t.Fatalf("specs %d", len(r.specs))
	}
	s := r.specs[0]
	wantEnv := map[string]string{"PATH": filepath.Dir(resolved), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			wantEnv["SystemRoot"] = v
		}
	}
	if s.Executable != node || !slices.Equal(s.Args, []string{"--version"}) || s.Timeout != 30*time.Second || s.StdoutLimit != 64<<10 || !strings.HasPrefix(s.Dir, probe) || len(s.Env) != len(wantEnv) {
		t.Fatalf("spec %+v", s)
	}
	for k, v := range wantEnv {
		if s.Env[k] != v {
			t.Fatalf("env %v", s.Env)
		}
	}
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probe left %v", entries)
	}
}

func TestParseNodeVersion(t *testing.T) {
	for in, want := range map[string]string{"v24.18.0\n": "24.18.0", "v24.18.0": "24.18.0", " v18.0.10 \r\n": "18.0.10"} {
		if v, ok := parseNodeVersion([]byte(in)); !ok || v != want || !envdoc.ComparableObservation("node", v) {
			t.Fatalf("%q: %q %v", in, v, ok)
		}
	}
	for _, in := range []string{"", "24.18.0", "v24.18", "v24.18.0-rc.1", "v24.18.0\nv25.0.0", "node v24.18.0", "v24.18.0.1", "v12345.0.0"} {
		if v, ok := parseNodeVersion([]byte(in)); ok {
			t.Fatalf("%q accepted as %q", in, v)
		}
	}
}

// Toolchain names are reserved: never an analyzer tool, always an
// env.discover runtime tool, and every toolchain a catalog descriptor needs
// is in the table (otherwise its analyzer could never be built).
func TestToolchainNamesAreReserved(t *testing.T) {
	if got := ToolchainFlags(); !slices.Equal(got, []string{"node"}) {
		t.Fatalf("toolchain flags %v", got)
	}
	table := ToolchainFlags()
	for _, n := range table {
		if slices.Contains(ToolFlags(), n) || n == "go" || !envdoc.IsRuntimeTool(n) {
			t.Fatalf("toolchain %q collides or is not a runtime tool", n)
		}
	}
	for _, e := range catalog() {
		if err := e.desc.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, n := range e.desc.Toolchains {
			if !slices.Contains(table, n) {
				t.Fatalf("%s needs unknown toolchain %q", e.desc.ID, n)
			}
		}
	}
	// A configured node is known to the real catalog: never unknown_tool.
	_, missing := Handlers(context.Background(), Config{Tools: map[string]string{"node": filepath.Join(t.TempDir(), "absent")}}, &executor.Executor{})
	if slices.Contains(reasons(missing), "node:unknown_tool") || !slices.Contains(reasons(missing), "node:invalid_config") {
		t.Fatalf("node in the real catalog: %+v", missing)
	}
	probes := ToolchainProbes(Config{Tools: map[string]string{"node": "/n"}})
	if len(probes) != 1 || probes[0].Tool != "node" || probes[0].Executable != "/n" || !slices.Equal(probes[0].Args, []string{"--version"}) || probes[0].ParseVersion == nil || probes[0].Analyzers == nil {
		t.Fatalf("toolchain probes %+v", probes)
	}
	if p := ToolchainProbes(Config{}); p[0].Executable != "" {
		t.Fatalf("unconfigured toolchain has an executable: %+v", p)
	}
}

// The discovery probes of a catalog with an interpreted tool: the tool probe
// carries the interpreter path, and the toolchain probe names the analyzers
// (and their ecosystems) that need it.
func TestProbesCarryToolchains(t *testing.T) {
	f := &fakeCatalog{}
	cfg := Config{Tools: map[string]string{"fakelint": "/l/fakelint.js", "node": "/n/node"}}
	var lint *pack.ToolProbe
	for _, p := range probesOf(cfg, f.entries()) {
		if p.Tool == "fakelint" {
			lint = &p
		}
	}
	if lint == nil || lint.Executable != "/l/fakelint.js" || lint.Interpreter != "node" || lint.InterpreterExecutable != "/n/node" {
		t.Fatalf("tool probe %+v", lint)
	}
	tc := toolchainProbesOf(cfg, f.entries())
	if len(tc) != 1 || tc[0].Executable != "/n/node" || !slices.Equal(tc[0].Analyzers, []string{"javascript.fakelint"}) || !slices.Equal(tc[0].Ecosystems, []string{"javascript"}) {
		t.Fatalf("toolchain probe %+v", tc)
	}
	if p := probesOf(Config{Tools: map[string]string{"fakelint": "/l/fakelint.js"}}, f.entries()); p[1].Interpreter != "node" || p[1].InterpreterExecutable != "" {
		t.Fatalf("unset interpreter %+v", p)
	}
}

// Descriptor rules for toolchains: sorted unique, never go or the tool
// itself, and an interpreter must be one of them.
func TestDescriptorToolchainRules(t *testing.T) {
	base := pack.Descriptor{ID: "javascript.fakelint", Name: "fakelint", Ecosystem: "javascript", Tool: "fakelint", VersionArgs: []string{"--version"}, ParseVersion: versionLine}
	for name, mut := range map[string]func(*pack.Descriptor){
		"go":              func(d *pack.Descriptor) { d.Toolchains = []string{"go"} },
		"self":            func(d *pack.Descriptor) { d.Toolchains = []string{"fakelint"} },
		"unsorted":        func(d *pack.Descriptor) { d.Toolchains = []string{"python", "node"} },
		"duplicate":       func(d *pack.Descriptor) { d.Toolchains = []string{"node", "node"} },
		"bad name":        func(d *pack.Descriptor) { d.Toolchains = []string{"Node"} },
		"interpreter out": func(d *pack.Descriptor) { d.Toolchains, d.Interpreter = []string{"node"}, "python" },
		"no toolchains":   func(d *pack.Descriptor) { d.Interpreter = "node" },
	} {
		d := base
		mut(&d)
		if d.Validate() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	d := base
	d.Toolchains, d.Interpreter = []string{"node"}, "node"
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

var nodeVersionOut = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// With CODEYE_TEST_NODE the real node binary is probed through the W05
// executor at startup and its handle reaches the build function.
func TestToolchainProbeRealNode(t *testing.T) {
	node := os.Getenv("CODEYE_TEST_NODE")
	if node == "" {
		t.Skip("set CODEYE_TEST_NODE to the node binary")
	}
	probe := t.TempDir()
	f := &fakeCatalog{}
	_, missing, err := compose(context.Background(), Config{Tools: map[string]string{"fakelint": filepath.Join(t.TempDir(), "fakelint.js"), "node": node}, ProbeDir: probe}, &executor.Executor{}, f.entries())
	if err != nil || len(missing) != 0 {
		t.Fatalf("%v %+v", err, missing)
	}
	h := f.built["node"]
	if h.Executable != node || !nodeVersionOut.MatchString(h.Version) || !filepath.IsAbs(h.Dir) || !envdoc.ComparableObservation("node", h.Version) {
		t.Fatalf("real node handle %+v", h)
	}
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probe left %v", entries)
	}
}
