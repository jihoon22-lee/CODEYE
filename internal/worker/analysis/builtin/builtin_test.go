package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func goPath(t *testing.T) string {
	if p := os.Getenv("CODEYE_TEST_GO"); p != "" {
		return p
	}
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	//lint:ignore SA1019 the test needs the toolchain that built it, without PATH search
	p := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("go toolchain not found (set CODEYE_TEST_GO): %v", err)
	}
	return p
}

type failingRunner struct{}

func (failingRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	return executor.Result{Status: executor.StatusStartFailed, Cleanup: executor.CleanupNotStarted}
}

func reasons(missing []Unavailable) []string {
	var out []string
	for _, m := range missing {
		out = append(out, m.Name+":"+m.Reason)
	}
	return out
}

func TestHandlersAdvertiseOnlyProbedToolchains(t *testing.T) {
	ctx := context.Background()
	if hs, missing := Handlers(ctx, Config{}, &executor.Executor{}); len(hs) != 0 || !slices.Equal(reasons(missing), []string{"go-vet:not_configured", "staticcheck:not_configured", "cargo-check:not_configured", "clippy:not_configured"}) {
		t.Fatalf("unconfigured: %d %+v", len(hs), missing)
	}
	if hs, missing := Handlers(ctx, Config{GoExecutable: "relative/go"}, &executor.Executor{}); len(hs) != 0 || missing[0].Reason != "invalid_config" {
		t.Fatalf("invalid: %d %+v", len(hs), missing)
	}
	// staticcheck alone is extra depth: without a baseline nothing is advertised.
	if hs, missing := Handlers(ctx, Config{Tools: map[string]string{"staticcheck": filepath.Join(t.TempDir(), "staticcheck")}}, &executor.Executor{}); len(hs) != 0 ||
		!slices.Equal(reasons(missing), []string{"go-vet:not_configured", "staticcheck:toolchain_not_configured", "cargo-check:not_configured", "clippy:not_configured"}) {
		t.Fatalf("staticcheck without go: %d %+v", len(hs), missing)
	}
	goExe := goPath(t)
	probe := t.TempDir()
	if hs, missing := Handlers(ctx, Config{GoExecutable: goExe, ProbeDir: probe}, failingRunner{}); len(hs) != 0 || missing[0].Reason != "probe_failed" {
		t.Fatalf("probe failure advertised: %d %+v", len(hs), missing)
	}
	hs, missing := Handlers(ctx, Config{GoExecutable: goExe, ProbeDir: probe}, &executor.Executor{})
	if len(hs) != 1 || !slices.Equal(reasons(missing), []string{"staticcheck:not_configured", "cargo-check:not_configured", "clippy:not_configured"}) {
		t.Fatalf("real probe: %d %+v", len(hs), missing)
	}
	reg, err := analysis.NewRegistry(hs...)
	if err != nil || len(reg.Infos()) != 1 || reg.Infos()[0].Capability != "analysis:deep-review" || reg.Infos()[0].Name != pack.CompositeName {
		t.Fatalf("registry: %v %+v", err, reg)
	}
	c, ok := hs[0].(*pack.Composite)
	if !ok {
		t.Fatalf("deep-review handler is %T", hs[0])
	}
	var ids []string
	for _, d := range c.Descriptors() {
		ids = append(ids, d.ID)
	}
	if !slices.Equal(ids, []string{"go.vet", "go.staticcheck", "rust.cargocheck", "rust.clippy"}) {
		t.Fatalf("catalog order %v", ids)
	}
	// A configured staticcheck path that is not a file is invalid: the
	// analyzer stays a member and its tasks report tool_unavailable.
	hs, missing = Handlers(ctx, Config{GoExecutable: goExe, ProbeDir: probe, Tools: map[string]string{"staticcheck": filepath.Join(t.TempDir(), "missing")}}, &executor.Executor{})
	if len(hs) != 1 || !slices.Equal(reasons(missing), []string{"staticcheck:invalid_config", "cargo-check:not_configured", "clippy:not_configured"}) {
		t.Fatalf("invalid staticcheck: %d %+v", len(hs), missing)
	}
	// An unknown tool of a newer agent is reported, never run.
	_, missing = Handlers(ctx, Config{GoExecutable: goExe, ProbeDir: probe, Tools: map[string]string{"ruff-next": "/x"}}, &executor.Executor{})
	if !slices.Contains(reasons(missing), "ruff-next:unknown_tool") {
		t.Fatalf("unknown tool: %+v", missing)
	}
	entries, _ := os.ReadDir(probe)
	if len(entries) != 0 {
		var tree []string
		_ = filepath.Walk(probe, func(p string, fi os.FileInfo, err error) error {
			if fi != nil {
				tree = append(tree, p[len(probe):]+" "+fi.Mode().String())
			}
			return nil
		})
		t.Fatalf("probe left files: %v (tree %v)", entries, tree)
	}
}

func TestToolFlagsAndProbes(t *testing.T) {
	if got := ToolFlags(); !slices.Equal(got, []string{"cargo", "clippy", "staticcheck"}) {
		t.Fatalf("flags %v", got)
	}
	probes := Probes(Config{GoExecutable: "/go", Tools: map[string]string{"staticcheck": "/sc", "cargo": "/tc/cargo", "clippy": "/tc/cargo-clippy"}})
	if len(probes) != 3 || probes[0].Tool != "cargo" || probes[1].Tool != "clippy" || probes[2].Tool != "staticcheck" {
		t.Fatalf("probes %+v", probes)
	}
	sc := probes[2]
	if sc.Executable != "/sc" || !slices.Equal(sc.Analyzers, []string{"go.staticcheck"}) || !slices.Equal(sc.Args, []string{"-version"}) || !slices.Equal(sc.Ecosystems, []string{"go"}) {
		t.Fatalf("probes %+v", probes)
	}
	if v, ok := sc.ParseVersion([]byte("staticcheck 2026.2.2 (0.8.2)\n")); !ok || v != "2026.2.2 (0.8.2)" {
		t.Fatalf("version %q %v", v, ok)
	}
	// The Rust pack (W20): cargo and clippy are probed directly, in the rust ecosystem.
	if c := probes[0]; c.Executable != "/tc/cargo" || !slices.Equal(c.Analyzers, []string{"rust.cargocheck"}) || !slices.Equal(c.Args, []string{"--version"}) || !slices.Equal(c.Ecosystems, []string{"rust"}) || c.Interpreter != "" {
		t.Fatalf("cargo probe %+v", c)
	}
	if v, ok := probes[0].ParseVersion([]byte("cargo 1.99.1 (5f94df478 2026-08-27)\n")); !ok || v != "1.99.1 (5f94df478 2026-08-27)" {
		t.Fatalf("cargo version %q %v", v, ok)
	}
	if c := probes[1]; c.Executable != "/tc/cargo-clippy" || !slices.Equal(c.Analyzers, []string{"rust.clippy"}) {
		t.Fatalf("clippy probe %+v", c)
	}
	for _, p := range Probes(Config{}) {
		if p.Executable != "" {
			t.Fatalf("unconfigured tool has an executable: %+v", p)
		}
	}
}

// With --staticcheck set (CODEYE_TEST_STATICCHECK), the real probe makes the
// adapter a member and nothing is reported missing.
func TestHandlersProbeRealStaticcheck(t *testing.T) {
	sc := os.Getenv("CODEYE_TEST_STATICCHECK")
	if sc == "" {
		t.Skip("set CODEYE_TEST_STATICCHECK to the staticcheck binary")
	}
	hs, missing := Handlers(context.Background(), Config{GoExecutable: goPath(t), ProbeDir: t.TempDir(), Tools: map[string]string{"staticcheck": sc}}, &executor.Executor{})
	if len(hs) != 1 || len(missing) != 0 {
		t.Fatalf("staticcheck configured: %d %+v", len(hs), missing)
	}
}
