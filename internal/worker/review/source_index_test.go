package review

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestAttemptSourceIndexKeepsVerificationAndScreening(t *testing.T) {
	project := testCallProjects()["Go"]
	job := treeJob(t, project.tree)
	ctx := WithAttemptSourceIndex(t.Context(), job)
	stage := &roleStage{ctx: ctx, j: job}
	if stage.sourceIndex() == nil {
		t.Fatal("no index bound to the attempt")
	}
	caller := stage.entries()[project.caller]
	anchor := record.Excerpt{Path: caller.Path, SHA256: caller.SHA256, Kind: "code", Start: 1, End: len(strings.Split(project.tree[caller.Path], "\n"))}
	anchors := []record.Excerpt{anchor}
	uncached := &roleStage{ctx: t.Context(), j: job}
	want, err := uncached.sourceCallContext(".", anchors, nil, 2, 4000)
	if err != nil || len(want) == 0 {
		t.Fatalf("without index got %v, %v", want, err)
	}
	for range 2 {
		got, err := stage.sourceCallContext(".", anchors, nil, 2, 4000)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("indexed call evidence changed: got %v, want %v, err %v", got, want, err)
		}
	}
	later := &roleStage{ctx: ctx, j: job, screen: secretScreen{withheld: map[string]bool{project.callee: true}}}
	if got, err := later.sourceCallContext(".", anchors, nil, 2, 4000); len(got) > 0 || err != nil {
		t.Fatalf("index bypassed the current secrets screen: %v, %v", got, err)
	}

	changed := &roleStage{ctx: ctx, j: job}
	changed.j.Epoch++
	if changed.sourceIndex() != nil {
		t.Fatal("reused the index under another lease epoch")
	}
	changed.j = job
	changed.j.Snapshot.Digest = strings.Repeat("a", 64)
	if changed.sourceIndex() != nil {
		t.Fatal("reused the index with a different manifest")
	}

	// A cached credential classification only follows a fresh hash check.
	callee := stage.entries()[project.callee]
	path := filepath.Join(job.Snapshot.SnapshotDir, callee.Path)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("X", int(*callee.Size))), 0600); err != nil {
		t.Fatal(err)
	}
	again := &roleStage{ctx: ctx, j: job}
	if _, _, err := again.readSource(callee); err == nil {
		t.Fatal("modified bytes passed the cached source check")
	}
}
