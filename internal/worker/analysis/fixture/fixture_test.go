//go:build codeye_fixture && linux

package fixture

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func job(t *testing.T, mode string) analysis.Job {
	base := t.TempDir()
	snap, ws, scratch := filepath.Join(base, "snapshot"), filepath.Join(base, "workspace"), filepath.Join(base, "scratch")
	for _, d := range []string{snap, ws, scratch} {
		os.MkdirAll(d, 0700)
	}
	if mode != "" {
		os.WriteFile(filepath.Join(snap, ModeFile), []byte(mode+"\n"), 0600)
	}
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x"), 0600)
	return analysis.Job{Snapshot: source.Snapshot{Root: base, SnapshotDir: snap, WorkspaceDir: ws}, Scratch: scratch}
}

func runAll(t *testing.T, ctx context.Context, j analysis.Job) []analysis.TaskResult {
	h := New()
	tasks, err := h.Plan(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	var out []analysis.TaskResult
	for _, task := range tasks {
		out = append(out, h.Run(ctx, j, task, &executor.Executor{}))
	}
	return out
}

func TestFixtureModes(t *testing.T) {
	ctx := context.Background()
	if r := runAll(t, ctx, job(t, "")); len(r) != 1 || r[0].Status != domain.TaskSucceeded || len(r[0].Artifacts) != 1 {
		t.Fatalf("succeed %+v", r)
	}
	if r := runAll(t, ctx, job(t, "fail")); r[0].Status != domain.TaskFailed {
		t.Fatalf("fail %+v", r)
	}
	if r := runAll(t, ctx, job(t, "partial")); len(r) != 2 || r[0].Status != domain.TaskSucceeded || r[1].Status != domain.TaskFailed {
		t.Fatalf("partial %+v", r)
	}
	if r := runAll(t, ctx, job(t, "unsupported")); r[0].Status != domain.TaskUnsupported || r[0].Reason != "analysis_unsupported" {
		t.Fatalf("unsupported %+v", r)
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r := runAll(t, cctx, job(t, "sleep"))
	if r[0].Status != domain.TaskCancelled || len(r[0].Executions) != 1 || r[0].Executions[0].Result.Cleanup != executor.CleanupConfirmed {
		t.Fatalf("sleep %+v", r)
	}
}
