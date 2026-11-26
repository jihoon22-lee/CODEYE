package jsts_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
)

// TC-326: a module with its own lockfile under the root install root
// resolves packages through both trees this attempt installed; the
// enclosing root's tree is accepted only with its own usable record.
func TestPreparedTreesAcceptTheEnclosingInstallRoot(t *testing.T) {
	fx := packtest.NewFixture(t, "", map[string]string{
		"package.json":            `{"name":"root"}` + "\n",
		"package-lock.json":       "{}\n",
		"src/a.ts":                "export const a = 1;\n",
		"bench/package.json":      `{"name":"bench"}` + "\n",
		"bench/package-lock.json": "{}\n",
		"bench/tsconfig.json":     "{}\n",
		"bench/b.ts":              "export const b = 1;\n",
	}, domain.Scope{})
	job := fx.Job
	roots, err := jsts.InstallRoots(job.Snapshot.Manifest, job.Scope)
	if err != nil || len(roots) != 2 || roots[0].Dir != "." || roots[1].Dir != "bench" {
		t.Fatalf("roots %+v %v", roots, err)
	}
	write := func(r jsts.InstallRoot, installed string) {
		key := depdoc.TaskKey("javascript", r.Suffix)
		want := jsts.Expect(job)
		want.TaskKey = key
		rec := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key,
			Module: depdoc.Module{ID: "javascript:" + r.Dir, Ecosystem: "javascript", Path: r.Dir}, Manager: depdoc.Manager{Name: "npm"}, Pinned: true,
			State: depdoc.StatePrepared, TaskStatus: "succeeded", Cache: depdoc.Cache{Kind: depdoc.CacheAttempt}, InstalledDirs: []string{installed},
			DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
		b, err := depdoc.Marshal(rec, want)
		if err != nil {
			t.Fatal(err)
		}
		p := depdoc.Path(job.Scratch, key)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ws := job.Snapshot.WorkspaceDir
	for _, d := range []string{"node_modules", "bench/node_modules"} {
		if err := os.Mkdir(filepath.Join(ws, filepath.FromSlash(d)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(roots[1], "bench/node_modules")
	dirs, _, ok := jsts.PreparedTrees(job, "bench")
	if !ok || !slices.Equal(dirs, []string{"bench/node_modules"}) || !jsts.ForeignNodeModulesExcept(ws, "bench", dirs) {
		t.Fatalf("without the root's record: %v %v", dirs, ok)
	}
	write(roots[0], "node_modules")
	dirs, _, ok = jsts.PreparedTrees(job, "bench")
	if !ok || !slices.Equal(dirs, []string{"bench/node_modules", "node_modules"}) || jsts.ForeignNodeModulesExcept(ws, "bench", dirs) {
		t.Fatalf("with the root's record: %v %v", dirs, ok)
	}
	// The root module does not take the nested root's tree.
	if dirs, _, _ := jsts.PreparedTrees(job, "."); !slices.Equal(dirs, []string{"node_modules"}) {
		t.Fatalf("root trees %v", dirs)
	}
}
