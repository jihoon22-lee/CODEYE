package builtin

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gograph"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// snapshotJobs acquires one snapshot and clones two attempt workspaces with
// separate scratch directories.
func snapshotJobs(t *testing.T, files map[string]string) (analysis.Job, analysis.Job) {
	t.Helper()
	src, data := t.TempDir(), t.TempDir()
	for rel, body := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	snap, err := source.AcquireLocal(ctx, src, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	jobs := [2]analysis.Job{}
	for i, ws := range []string{"ws1", "ws2"} {
		loaded, err := source.Load(ctx, snap.Root, snap.Digest)
		if err != nil {
			t.Fatal(err)
		}
		loaded.WorkspaceDir = filepath.Join(data, ws)
		if err := source.CloneWorkspace(ctx, loaded, loaded.WorkspaceDir); err != nil {
			t.Fatal(err)
		}
		jobs[i] = analysis.Job{RunID: "r", AttemptID: "a", Epoch: 1, Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, Snapshot: loaded, Scratch: t.TempDir()}
	}
	return jobs[0], jobs[1]
}

// docs/ANALYZERS.md#go-vet: with only --go configured, the composite's go-vet tasks are
// the go vet handler's (same keys, specs, diagnostics and package graph). Staticcheck
// and gofmt add their own tasks after them.
func TestGoVetUnchangedInComposite(t *testing.T) {
	goExe := goPath(t)
	ctx := context.Background()
	c, _, err := Composite(ctx, Config{GoExecutable: goExe, ProbeDir: t.TempDir()}, &executor.Executor{})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := govet.New(govet.Config{GoExecutable: goExe})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"go.mod": "module example.test/pin\n\ngo 1.22\n", "a/a.go": "package a\n\nimport \"fmt\"\n\nfunc F() { fmt.Printf(\"%d\", \"x\") }\n",
		"b/go.mod": "module example.test/b\n\ngo 1.22\n", "b/b.go": "package b\n"}
	j1, j2 := snapshotJobs(t, files)
	want, err := direct.Plan(ctx, j1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.Plan(ctx, j1)
	if err != nil {
		t.Fatal(err)
	}
	var vet, other []analysis.TaskSpec
	for _, p := range plan {
		if anyTask(p.Key) {
			continue
		}
		if pack.Owns(govet.HandlerName, p.Key) {
			vet = append(vet, p)
		} else {
			other = append(other, p)
		}
	}
	if !reflect.DeepEqual(vet, want) || len(other) != 5 || other[0].Key != "staticcheck" || other[1].Key != "staticcheck.m01" || other[2].Key != "gofmt" || other[2].Required || other[3].Key != "errcheck" || other[4].Key != "errcheck.m01" || plan[0].Key != "go-vet" {
		t.Fatalf("plan %+v, go vet alone %+v", plan, want)
	}
	for _, spec := range want {
		got := c.Run(ctx, j1, spec, &executor.Executor{})
		exp := direct.Run(ctx, j2, spec, &executor.Executor{})
		if got.Status != exp.Status || got.Reason != exp.Reason || !slices.Equal(got.Limitations, exp.Limitations) || len(got.Artifacts) != 2 || len(exp.Artifacts) != 2 || len(got.Executions) != len(exp.Executions) {
			t.Fatalf("%s: composite %+v, go vet alone %+v", spec.Key, got, exp)
		}
		for i, media := range []string{analysis.MediaObservations, gograph.MediaType} {
			a, erra := os.ReadFile(got.Artifacts[i].Path)
			b, errb := os.ReadFile(exp.Artifacts[i].Path)
			if erra != nil || errb != nil || string(a) != string(b) || got.Artifacts[i].MediaType != media || exp.Artifacts[i].MediaType != media {
				t.Fatalf("%s: go vet artifact %s differs through the composite (%v, %v)", spec.Key, media, erra, errb)
			}
			if i == 0 && !strings.Contains(string(a), `"kind":"go_vet_observations"`) {
				t.Fatalf("%s: go vet document identity lost", spec.Key)
			}
		}
	}
	// The staticcheck tasks finish unsupported without starting a process.
	res := c.Run(ctx, j1, other[0], failingRunner{})
	if res.Status != domain.TaskUnsupported || res.Reason != pack.ReasonToolNotConfigured || len(res.Executions) != 0 {
		t.Fatalf("staticcheck not configured: %+v", res)
	}
}
