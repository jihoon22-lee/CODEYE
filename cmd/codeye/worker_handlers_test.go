package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// The production deep-review handler is discovery → the analyzer composite
// (Go analysis, structure metrics, and secrets screening) → review roles.
func TestWorkerHandlersComposeDiscovery(t *testing.T) {
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	hs, missing, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir()}, &executor.Executor{})
	if err != nil || len(hs) != 2 || strings.Join(missing, ",") != "staticcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,cargo-check:not_configured,clippy:not_configured,"+structureNotConfigured {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
	if info := hs[0].Info(); info.Name != "analyzers" || info.Capability != "analysis:deep-review" {
		t.Fatalf("identity %+v", info)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := source.AcquireLocal(context.Background(), src, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := hs[0].Plan(context.Background(), analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range plan {
		if strings.HasPrefix(p.Key, "review.investigate.s") || strings.HasPrefix(p.Key, "review.verify.s") {
			continue
		}
		keys = append(keys, p.Key+"="+string(p.Stage))
		if (p.Key == "staticcheck" || p.Key == "gofmt") && p.Required {
			t.Fatalf("%s must be optional", p.Key)
		}
	}
	want := []string{"env.discover=environment", "docs.discover=documents", "go-vet=static_analysis", "staticcheck=static_analysis", "gofmt=static_analysis", "lizard=static_analysis", "jscpd=static_analysis", "gitleaks=static_analysis", "review.environment=environment", "review.documents=documents", "review.investigate=code_review", "review.interpret=code_review", "review.verify=code_review", "review.scope=code_review"}
	if len(keys) != len(want) {
		t.Fatalf("plan %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("plan %v, want %v", keys, want)
		}
	}
	if hs[1].Info().Capability != "analysis:with-tests" {
		t.Fatalf("with-tests capability %+v", hs[1].Info())
	}
	withTests, err := hs[1].Plan(context.Background(), analysis.Job{Profile: "with-tests", Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil || len(withTests) != len(plan)+1 || withTests[len(plan)].Key != "test.go" || withTests[len(plan)].Stage != domain.StageTest || !withTests[len(plan)].Required {
		t.Fatalf("with-tests plan %v, error %v", withTests, err)
	}
}

// The worker composes discovery → go vet → the five review roles; a call
// ceiling out of range is refused, never replaced by the default.
func TestWorkerHandlersComposeReview(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExe += ".exe"
	}
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	for _, n := range []int{14, 208, -1} {
		if _, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewMaxCalls: n}, &executor.Executor{}); err == nil {
			t.Fatalf("call ceiling %d accepted", n)
		}
	}
	hs, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir()}, &executor.Executor{})
	if err != nil || len(hs) != 2 {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := source.AcquireLocal(context.Background(), src, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := hs[0].Plan(context.Background(), analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range plan {
		keys = append(keys, p.Key+"="+string(p.Stage))
	}
	want := "env.discover=environment docs.discover=documents go-vet=static_analysis staticcheck=static_analysis gitleaks=static_analysis review.environment=environment review.documents=documents review.investigate=code_review review.interpret=code_review review.verify=code_review"
	if strings.Join(keys, " ") != want {
		t.Fatalf("plan %v", keys)
	}
}

// The shared node toolchain (--node) travels in the hello's analyzer_tools
// under its reserved name: it is probed at startup, never an unknown tool.
// With CODEYE_TEST_NODE the real node passes and adds no missing code.
func TestWorkerHandlersKnowTheNodeToolchain(t *testing.T) {
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	node, want := filepath.Join(t.TempDir(), "absent-node"), "node:invalid_config,staticcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,cargo-check:not_configured,clippy:not_configured,"+structureNotConfigured
	if p := os.Getenv("CODEYE_TEST_NODE"); p != "" {
		node, want = p, "staticcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,cargo-check:not_configured,clippy:not_configured,"+structureNotConfigured
	}
	hs, missing, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), AnalyzerTools: map[string]string{"node": node}}, &executor.Executor{})
	if err != nil || len(hs) != 2 || strings.Join(missing, ",") != want {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
}
