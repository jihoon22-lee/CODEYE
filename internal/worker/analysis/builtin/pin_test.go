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

// docs/39 §3: with only --go configured, the composite's go-vet tasks are
// the W08 handler's (same keys, specs and v0.1 document bytes); staticcheck
// adds its own tasks after them and never changes go vet's.
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
		if pack.Owns(govet.HandlerName, p.Key) {
			vet = append(vet, p)
		} else {
			other = append(other, p)
		}
	}
	if !reflect.DeepEqual(vet, want) || len(other) != 2 || other[0].Key != "staticcheck" || other[1].Key != "staticcheck.m01" || plan[0].Key != "go-vet" {
		t.Fatalf("plan %+v, go vet alone %+v", plan, want)
	}
	for _, spec := range want {
		got := c.Run(ctx, j1, spec, &executor.Executor{})
		exp := direct.Run(ctx, j2, spec, &executor.Executor{})
		if got.Status != exp.Status || got.Reason != exp.Reason || !slices.Equal(got.Limitations, exp.Limitations) || len(got.Artifacts) != 1 || len(exp.Artifacts) != 1 || len(got.Executions) != len(exp.Executions) {
			t.Fatalf("%s: composite %+v, go vet alone %+v", spec.Key, got, exp)
		}
		a, _ := os.ReadFile(got.Artifacts[0].Path)
		b, _ := os.ReadFile(exp.Artifacts[0].Path)
		if string(a) != string(b) || !strings.Contains(string(a), `"kind":"go_vet_observations"`) || got.Artifacts[0].MediaType != exp.Artifacts[0].MediaType {
			t.Fatalf("%s: go vet document differs through the composite", spec.Key)
		}
	}
	// The staticcheck tasks finish unsupported without starting a process.
	res := c.Run(ctx, j1, other[0], failingRunner{})
	if res.Status != domain.TaskUnsupported || res.Reason != pack.ReasonToolNotConfigured || len(res.Executions) != 0 {
		t.Fatalf("staticcheck not configured: %+v", res)
	}
}
