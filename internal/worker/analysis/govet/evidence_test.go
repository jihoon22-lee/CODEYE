package govet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// TestRealTargetEvidence runs the adapter against a real project tree.
// It is skipped unless CODEYE_GOVET_TARGET (an isolated source copy),
// CODEYE_GOVET_OUT (evidence dir) and optionally CODEYE_GOVET_MODCACHE are set.
// It records outcomes as observed, including failures.
func TestRealTargetEvidence(t *testing.T) {
	target, out := os.Getenv("CODEYE_GOVET_TARGET"), os.Getenv("CODEYE_GOVET_OUT")
	if target == "" || out == "" {
		t.Skip("CODEYE_GOVET_TARGET/CODEYE_GOVET_OUT not set (manual evidence run)")
	}
	modcache := os.Getenv("CODEYE_GOVET_MODCACHE")
	ctx := context.Background()
	base := t.TempDir()
	data, scratch := filepath.Join(base, "data"), filepath.Join(base, "scratch")
	for _, d := range []string{data, scratch, out} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	srcBefore := packtest.TreeHash(t, target)
	mcBefore := ""
	if modcache != "" {
		mcBefore = packtest.TreeHash(t, modcache)
	}
	snap, err := source.AcquireLocal(ctx, target, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	loaded, err := source.Load(ctx, snap.Root, snap.Digest)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ws := filepath.Join(data, "attempt-workspace")
	if err := source.CloneWorkspace(ctx, loaded, ws); err != nil {
		t.Fatalf("clone: %v", err)
	}
	loaded.WorkspaceDir = ws
	snapBefore := packtest.TreeHash(t, loaded.SnapshotDir)
	job := analysis.Job{RunID: "evidence-run", AttemptID: "evidence-attempt", Epoch: 1, Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, Snapshot: loaded, Scratch: scratch}
	h := newHandler(t, Config{GoModCache: modcache})
	tasks, err := h.Plan(ctx, job)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	type taskEvidence struct {
		Key, Status, Reason string
		Seconds             float64
		Executions          []ExecSummary
		Document            json.RawMessage
	}
	var ev []taskEvidence
	for _, task := range tasks {
		start := time.Now()
		res := h.Run(ctx, job, task, &executor.Executor{})
		te := taskEvidence{Key: task.Key, Status: string(res.Status), Reason: res.Reason, Seconds: time.Since(start).Seconds()}
		if len(res.Artifacts) == 1 {
			raw, _ := os.ReadFile(res.Artifacts[0].Path)
			te.Document = raw
			assertNoNodePaths(t, raw, target, base, modcache, h.goroot)
		}
		ev = append(ev, te)
	}
	summary := map[string]any{
		"manifest_digest":     loaded.Digest,
		"manifest_entries":    len(loaded.Manifest.Entries),
		"source_unchanged":    packtest.TreeHash(t, target) == srcBefore,
		"snapshot_unchanged":  packtest.TreeHash(t, loaded.SnapshotDir) == snapBefore,
		"modcache_configured": modcache != "",
		"modcache_unchanged":  modcache == "" || packtest.TreeHash(t, modcache) == mcBefore,
		"tasks":               ev,
	}
	raw, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	name := "evidence-no-modcache.json"
	if modcache != "" {
		name = "evidence-modcache.json"
	}
	if err := os.WriteFile(filepath.Join(out, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if !summary["source_unchanged"].(bool) || !summary["snapshot_unchanged"].(bool) || !summary["modcache_unchanged"].(bool) {
		t.Fatalf("an input changed: %s", raw)
	}
}
