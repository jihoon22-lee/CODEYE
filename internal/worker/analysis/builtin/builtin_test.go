package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
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

func TestHandlersAdvertiseOnlyProbedToolchains(t *testing.T) {
	ctx := context.Background()
	if hs, missing := Handlers(ctx, Config{}, &executor.Executor{}); len(hs) != 0 || len(missing) != 1 || missing[0].Reason != "not_configured" {
		t.Fatalf("unconfigured: %d %+v", len(hs), missing)
	}
	if hs, missing := Handlers(ctx, Config{GoExecutable: "relative/go"}, &executor.Executor{}); len(hs) != 0 || missing[0].Reason != "invalid_config" {
		t.Fatalf("invalid: %d %+v", len(hs), missing)
	}
	goExe := goPath(t)
	probe := t.TempDir()
	if hs, missing := Handlers(ctx, Config{GoExecutable: goExe, ProbeDir: probe}, failingRunner{}); len(hs) != 0 || missing[0].Reason != "probe_failed" {
		t.Fatalf("probe failure advertised: %d %+v", len(hs), missing)
	}
	hs, missing := Handlers(ctx, Config{GoExecutable: goExe, ProbeDir: probe}, &executor.Executor{})
	if len(hs) != 1 || len(missing) != 0 {
		t.Fatalf("real probe: %d %+v", len(hs), missing)
	}
	reg, err := analysis.NewRegistry(hs...)
	if err != nil || len(reg.Infos()) != 1 || reg.Infos()[0].Capability != "analysis:deep-review" {
		t.Fatalf("registry: %v %+v", err, reg)
	}
	entries, _ := os.ReadDir(probe)
	if len(entries) != 0 {
		t.Fatalf("probe left files: %v", entries)
	}
}
