package jsts_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
)

// A lockfile serves the modules under it; a module under no lockfile is
// installed from the outermost package.json directory above it.
func TestInstallRootsFromTheManifest(t *testing.T) {
	fx := packtest.NewFixture(t, "", map[string]string{
		"package.json":              `{"name":"root","workspaces":["packages/*"]}` + "\n",
		"pnpm-lock.yaml":            "lockfileVersion: '9.0'\n",
		"package-lock.json":         "{}\n",
		"tsconfig.json":             "{}\n",
		"src/a.ts":                  "export const a = 1;\n",
		"packages/app/package.json": `{"name":"app"}` + "\n",
		"packages/app/src/b.ts":     "export const b = 1;\n",
		"tools/x/package.json":      `{"name":"x"}` + "\n",
		"tools/x/yarn.lock":         "# yarn lockfile v1\n",
		"tools/x/c.js":              "module.exports = 1;\n",
		"web/package.json":          `{"name":"web"}` + "\n",
		"web/sub/package.json":      `{"name":"sub"}` + "\n",
		"web/sub/d.js":              "module.exports = 1;\n",
		"web/e.js":                  "module.exports = 1;\n",
	}, domain.Scope{})
	roots, err := jsts.InstallRoots(fx.Job.Snapshot.Manifest, fx.Job.Scope)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		dir, suffix string
		locks       []string
		mods        []string
	}
	var got []row
	for _, r := range roots {
		var mods []string
		for _, m := range r.Modules {
			mods = append(mods, m.Dir)
		}
		got = append(got, row{r.Dir, r.Suffix, r.Lockfiles, mods})
	}
	want := []row{
		{".", "", []string{"pnpm-lock.yaml", "package-lock.json"}, []string{".", "packages/app", "web", "web/sub"}},
		{"tools/x", ".m01", []string{"tools/x/yarn.lock"}, []string{"tools/x"}},
	}
	if len(got) != len(want) {
		t.Fatalf("roots %+v", got)
	}
	for i := range want {
		if got[i].dir != want[i].dir || got[i].suffix != want[i].suffix || !slices.Equal(got[i].locks, want[i].locks) || !slices.Equal(got[i].mods, want[i].mods) {
			t.Fatalf("root %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if r, ok := jsts.InstallRootOf(roots, "packages/app"); !ok || r.Dir != "." {
		t.Fatalf("install root of packages/app = %+v %v", r, ok)
	}
	// Without a lockfile the outermost package.json directory installs.
	fx = packtest.NewFixture(t, "", map[string]string{
		"web/package.json":     `{"name":"web"}` + "\n",
		"web/sub/package.json": `{"name":"sub"}` + "\n",
		"web/sub/d.js":         "module.exports = 1;\n",
		"web/e.js":             "module.exports = 1;\n",
	}, domain.Scope{})
	roots, err = jsts.InstallRoots(fx.Job.Snapshot.Manifest, fx.Job.Scope)
	if err != nil || len(roots) != 1 || roots[0].Dir != "web" || roots[0].Suffix != ".m01" || len(roots[0].Lockfiles) != 0 || len(roots[0].Modules) != 2 {
		t.Fatalf("roots without a lockfile = %+v %v", roots, err)
	}
}

// PreparedTrees reads exactly the trees the task recorded, and
// ForeignNodeModulesExcept accepts them only as real directories.
func TestPreparedTreesAndAcceptedNodeModules(t *testing.T) {
	fx := packtest.NewFixture(t, "", map[string]string{
		"package.json":      `{"name":"root"}` + "\n",
		"package-lock.json": "{}\n",
		"tsconfig.json":     "{}\n",
		"src/a.ts":          "export const a = 1;\n",
	}, domain.Scope{})
	job := fx.Job
	if _, _, ok := jsts.PreparedTrees(job, "."); ok {
		t.Fatal("trees without a record")
	}
	key := depdoc.TaskKey("javascript", "")
	want := jsts.Expect(job)
	want.TaskKey = key
	rec := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key,
		Module: depdoc.Module{ID: "javascript:.", Ecosystem: "javascript", Path: "."}, Manager: depdoc.Manager{Name: "npm"}, Pinned: true,
		State: depdoc.StatePrepared, TaskStatus: "succeeded", Cache: depdoc.Cache{Kind: depdoc.CacheAttempt}, InstalledDirs: []string{"node_modules"},
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
	dirs, lim, ok := jsts.PreparedTrees(job, ".")
	if !ok || lim != jsts.LimDependenciesPrepared || !slices.Equal(dirs, []string{"node_modules"}) {
		t.Fatalf("prepared trees = %v %s %v", dirs, lim, ok)
	}
	ws := job.Snapshot.WorkspaceDir
	if jsts.ForeignNodeModulesExcept(ws, ".", dirs) {
		t.Fatal("no node_modules yet is foreign")
	}
	nm := filepath.Join(ws, "node_modules")
	if err := os.Mkdir(nm, 0o700); err != nil {
		t.Fatal(err)
	}
	if jsts.ForeignNodeModulesExcept(ws, ".", dirs) {
		t.Fatal("the recorded tree is refused")
	}
	if !jsts.ForeignNodeModulesExcept(ws, ".", nil) {
		t.Fatal("an unrecorded tree is accepted")
	}
	// A tree above the module that the record does not list is refused.
	sub := filepath.Join(ws, "src", "node_modules")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if !jsts.ForeignNodeModulesExcept(ws, "src", dirs) {
		t.Fatal("an unlisted tree at the module is accepted")
	}
	if runtime.GOOS == "windows" {
		return // a directory link needs privileges there
	}
	if err := os.Remove(nm); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), nm); err != nil {
		t.Fatal(err)
	}
	if !jsts.ForeignNodeModulesExcept(ws, ".", dirs) {
		t.Fatal("a recorded name that is a link is accepted")
	}
}
