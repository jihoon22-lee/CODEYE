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

// The production deep-review handler is discovery → go vet → review roles.
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
	if err != nil || len(hs) != 1 || len(missing) != 0 {
		t.Fatalf("handlers %d missing %v err %v", len(hs), missing, err)
	}
	if info := hs[0].Info(); info.Name != "go-vet" || info.Capability != "analysis:deep-review" {
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
	}
	want := []string{"env.discover=environment", "docs.discover=documents", "go-vet=static_analysis", "review.context=documents", "review.investigate=code_review", "review.verify=code_review"}
	if len(keys) != len(want) {
		t.Fatalf("plan %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("plan %v, want %v", keys, want)
		}
	}
}

// --review-pack review-r1 (via the agent's Hello) composes discovery → go vet
// → the five R1 roles; an unknown pack is refused, never replaced by v1.
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
	want := "env.discover=environment docs.discover=documents go-vet=static_analysis review.environment=environment review.documents=documents review.investigate=code_review review.interpret=code_review review.verify=code_review"
	if strings.Join(keys, " ") != want {
		t.Fatalf("plan %v", keys)
	}
}
