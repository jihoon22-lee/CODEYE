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
// (go vet, then staticcheck and the Python pack planned unsupported when
// their tools are not given; the secrets task because go.mod is model input
// the environment role may quote) → review roles. The go-vet and review task
// specs are unchanged.
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
	if err != nil || len(hs) != 1 || strings.Join(missing, ",") != "staticcheck:not_configured,cppcheck:not_configured,clang-tidy:not_configured,ruff:not_configured,mypy:not_configured,pyright:not_configured,bandit:not_configured,eslint:not_configured,tsc:not_configured,eslint-typed:not_configured,cargo-check:not_configured,clippy:not_configured,"+structureNotConfigured {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
	if info := hs[0].Info(); info.Name != "analyzers" || info.Capability != "analysis:deep-review" {
		t.Fatalf("identity %+v", info)
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
	keys := []string{}
	for _, p := range plan {
		keys = append(keys, p.Key+"="+string(p.Stage))
		if p.Key == "staticcheck" && p.Required {
			t.Fatal("staticcheck is extra depth, not required")
		}
	}
	// No pack in the hello: the default pack, review-r1, plans its five roles.
	want := []string{"env.discover=environment", "docs.discover=documents", "go-vet=static_analysis", "staticcheck=static_analysis", "gitleaks=static_analysis", "review.environment=environment", "review.documents=documents", "review.investigate=code_review", "review.interpret=code_review", "review.verify=code_review"}
	if len(keys) != len(want) {
		t.Fatalf("plan %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("plan %v, want %v", keys, want)
		}
	}
}

// --review-pack (via the agent's Hello) selects the pack: review-r1 composes
// discovery → go vet → the five roles, the legacy review-v1 its three
// roles; an unknown pack is refused, never replaced by the default.
func TestWorkerHandlersSelectReviewPack(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExe += ".exe"
	}
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	if _, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewPack: "review-r2"}, &executor.Executor{}); err == nil {
		t.Fatal("unknown review pack accepted")
	}
	for _, n := range []int{14, 208, -1} {
		if _, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewMaxCalls: n}, &executor.Executor{}); err == nil {
			t.Fatalf("call ceiling %d accepted", n)
		}
	}
	hs, _, err := workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewPack: "review-r1"}, &executor.Executor{})
	if err != nil || len(hs) != 1 {
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
	hs, _, err = workerHandlers(context.Background(), &workerproto.Hello{GoExecutable: goExe, ScratchRoot: t.TempDir(), ReviewPack: "review-v1"}, &executor.Executor{})
	if err != nil || len(hs) != 1 {
		t.Fatal(err)
	}
	plan, err = hs[0].Plan(context.Background(), analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	keys = keys[:0]
	for _, p := range plan {
		keys = append(keys, p.Key+"="+string(p.Stage))
	}
	if want := "env.discover=environment docs.discover=documents go-vet=static_analysis staticcheck=static_analysis gitleaks=static_analysis review.context=documents review.investigate=code_review review.verify=code_review"; strings.Join(keys, " ") != want {
		t.Fatalf("legacy plan %v", keys)
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
	if err != nil || len(hs) != 1 || strings.Join(missing, ",") != want {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
}
