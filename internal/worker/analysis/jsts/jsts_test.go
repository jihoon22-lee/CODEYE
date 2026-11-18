package jsts_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func monorepo(t *testing.T) jststest.Fixture {
	files := jststest.MonorepoFiles(t, 4)
	files["packages/app/testdata/x/package.json"] = "{}\n"
	files["packages/app/node_modules/dep/index.js"] = "module.exports = 1;\n"
	files["packages/app/.cache/c.js"] = "1;\n"
	return jststest.NewFixture(t, "", files, domain.Scope{})
}

// PackageModules restates W15's JavaScript module discovery (discovery
// imports the analyzer catalog, so jsts cannot import discovery); this
// pins the two together.
func TestPackageModulesMatchesDiscovery(t *testing.T) {
	fx := monorepo(t)
	dirs, nested := jsts.PackageModules(jsts.Included(fx.Job.Snapshot.Manifest))
	mods, _ := discovery.DetectModules(fx.Job.Snapshot.Manifest, envdoc.Scope{Kind: "full"})
	var want []string
	wantNested := map[string][]string{}
	for _, m := range mods {
		if m.Ecosystem == envdoc.EcoJavaScript {
			want = append(want, m.Path)
			wantNested[m.Path] = m.ExcludedPaths
		}
	}
	if !slices.Equal(dirs, want) {
		t.Fatalf("modules %v, discovery %v", dirs, want)
	}
	for _, d := range dirs {
		if !slices.Equal(nested[d], wantNested[d]) {
			t.Fatalf("nested %s: %v, discovery %v", d, nested[d], wantNested[d])
		}
	}
}

func TestModulesPlanAndScope(t *testing.T) {
	fx := monorepo(t)
	mods, err := jsts.Modules(fx.Job.Snapshot.Manifest, domain.Scope{Kind: "full"})
	if err != nil || len(mods) != 3 {
		t.Fatalf("%v %+v", err, mods)
	}
	root, app, lib := mods[0], mods[1], mods[2]
	if root.Dir != "." || root.Implicit || jsts.Key("eslint", root) != "eslint" || !slices.Equal(root.Files, []string{"scripts/release.mjs"}) ||
		!slices.Equal(root.Nested, []string{"packages/app", "packages/lib"}) {
		t.Fatalf("root %+v", root)
	}
	// node_modules, hidden and testdata directories hold no project source.
	if app.Dir != "packages/app" || jsts.Key("tsc", app) != "tsc.m01" || !app.TSConfig || len(app.Files) != 4 || len(app.TSFiles) != 4 || !slices.Equal(app.Patterns, []string{"."}) {
		t.Fatalf("app %+v", app)
	}
	if lib.TSConfig || jsts.Key("eslint", lib) != "eslint.m02" || len(lib.TSFiles) != 0 {
		t.Fatalf("lib %+v", lib)
	}
	mods, _ = jsts.Modules(fx.Job.Snapshot.Manifest, domain.Scope{Kind: "paths", Paths: []string{"packages/app/src", "packages/lib/index.js", "README.md"}})
	if len(mods) != 2 || !slices.Equal(mods[0].Patterns, []string{"src"}) || !slices.Equal(mods[1].Patterns, []string{"index.js"}) || mods[0].Index != 1 {
		t.Fatalf("scoped %+v", mods)
	}
	if _, err := jsts.Modules(fx.Job.Snapshot.Manifest, domain.Scope{Kind: "diff"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	// Without a root package.json the root is implicit.
	fx2 := jststest.NewFixture(t, "", map[string]string{"a.js": "1;\n", "pkg/package.json": "{}\n", "pkg/b.ts": "1;\n"}, domain.Scope{})
	mods, _ = jsts.Modules(fx2.Job.Snapshot.Manifest, domain.Scope{})
	if len(mods) != 2 || !mods[0].Implicit || !slices.Equal(mods[0].Nested, []string{"pkg"}) {
		t.Fatalf("implicit %+v", mods)
	}
}

func TestResolverColumns(t *testing.T) {
	fx := jststest.NewFixture(t, "", map[string]string{"package.json": "{}\n", "a.ts": "\ufeffconst 한 = \"😀\"; x;\n"}, domain.Scope{})
	files := jsts.Included(fx.Job.Snapshot.Manifest)
	r := &jsts.Resolver{SnapshotDir: fx.Job.Snapshot.SnapshotDir, ModuleDir: ".", Roots: jsts.Roots(fx.Job.Snapshot.WorkspaceDir), Files: files}
	// UTF-16 column 14 is "x" (the BOM is not a column): 한 is 1 unit/3
	// bytes, 😀 is 2 units/4 bytes.
	p, loc, state := r.Resolve("[REDACTED]/a.ts", 1, 14, 1, 15)
	if p == nil || *p != "a.ts" || state != "resolved" || loc.ColumnUnit != "byte" || loc.StartCol != 18 || loc.EndCol != 19 {
		t.Fatalf("%v %+v %s", p, loc, state)
	}
	if _, _, state := r.Resolve("[REDACTED]/a.ts", 0, 0, 0, 0); state == "resolved" {
		t.Fatal("line 0 resolved")
	}
}

func TestReadChainJSONCAndOptions(t *testing.T) {
	fx := jststest.NewFixture(t, "", map[string]string{
		"tsconfig.base.json":     "{\n  // comment, \"quoted // not a comment\"\n  \"compilerOptions\": {\"strict\": true, \"paths\": {\"@/*\": [\"./src/*\"],},},\n}\n",
		"app/tsconfig.json":      "{\"extends\": \"../tsconfig.base.json\", \"include\": [\"src/**/*\"], \"references\": [{\"path\": \"./node\"}]}\n",
		"app/node/tsconfig.json": "{\"extends\": [\"@tsconfig/node20\"], \"compilerOptions\": {\"outDir\": \"./dist\"}}\n",
		"app/src/a.ts":           "1;\n",
	}, domain.Scope{})
	files := jsts.Included(fx.Job.Snapshot.Manifest)
	c, err := jsts.ReadChain(fx.Job.Snapshot.SnapshotDir, files, "app/tsconfig.json")
	if err != nil || c.Outside || c.Unreadable || len(c.WriteOptions) != 0 || len(c.Files) != 3 || !slices.Equal(c.References, []string{"app/node/tsconfig.json"}) ||
		!slices.Equal(c.Packages, []string{"@tsconfig/node20"}) {
		t.Fatalf("%v %+v", err, c)
	}
	if reason, _ := jsts.ChainPolicy(c, fx.Job.Snapshot.WorkspaceDir); reason != "" {
		t.Fatalf("policy %s", reason)
	}
	// A tampered snapshot file is an integrity failure.
	if err := os.WriteFile(filepath.Join(fx.Job.Snapshot.SnapshotDir, "tsconfig.base.json"), []byte("{}"), 0o644); err == nil {
		if _, err := jsts.ReadChain(fx.Job.Snapshot.SnapshotDir, files, "app/tsconfig.json"); err != jsts.ErrIntegrity {
			t.Fatalf("integrity %v", err)
		}
	}
}

func TestPathsLeaveWorkspace(t *testing.T) {
	ws := t.TempDir()
	if jsts.PathsLeaveWorkspace(ws, []string{"src", "missing/deeper"}) {
		t.Fatal("inside paths reported")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(ws, "out")); err != nil {
		t.Skip(err)
	}
	if !jsts.PathsLeaveWorkspace(ws, []string{"out/x"}) {
		t.Fatal("symlink outside not reported")
	}
}

func TestEffectiveAndHostPathText(t *testing.T) {
	w, o, err := jsts.CheckEffective([]byte(`{"compilerOptions":{"noEmit":true,"incremental":false,"composite":false,"outDir":"./dist","rootDir":"../.."},"files":["./a.ts"]}`), "app")
	if err != nil || len(w) != 0 || !slices.Equal(o, []string{"rootDir"}) {
		t.Fatalf("%v %v %v", w, o, err)
	}
	if got := jsts.WithholdHostPaths("File '/etc/passwd' and 'C:\\x\\y' and '$WORKSPACE/a.ts' and 'zod'"); strings.Contains(got, "/etc") || strings.Contains(got, `C:\`) ||
		!strings.Contains(got, "'zod'") || !strings.Contains(got, "$WORKSPACE") {
		t.Fatalf("%s", got)
	}
	if got := jsts.OptionLimitations([]string{"tsBuildInfoFile", "outDir"}); !slices.Equal(got, []string{"tsconfig_option_ts_build_info_file", "tsconfig_option_out_dir"}) {
		t.Fatalf("%v", got)
	}
}

func TestStripJSONCKeepsStrings(t *testing.T) {
	in := "{\"a\": \"x // y /* z */\", /* c */ \"b\": [1, 2,], // tail\n}"
	if got := string(jsts.StripJSONC([]byte(in))); !strings.Contains(got, "\"x // y /* z */\"") || strings.Contains(got, "tail") || strings.Contains(got, ",]") {
		t.Fatalf("%q", got)
	}
}

func TestScriptValidation(t *testing.T) {
	eslint, tscJS, node := jststest.FakeTools(t)
	// The package directory is derived from the resolved script path; on
	// Windows that also expands 8.3 short names (RUNNER~1) in temp paths.
	resolved, err := filepath.EvalSymlinks(eslint)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := jsts.NewScript(eslint, "eslint", node); err != nil || s.Package != filepath.Dir(filepath.Dir(resolved)) || s.Env()["PATH"] != node.Dir {
		t.Fatalf("%v %+v", err, s)
	}
	for _, c := range []struct {
		path, name string
	}{{"relative/eslint.js", "eslint"}, {tscJS, "eslint"}, {filepath.Join(t.TempDir(), "missing.js"), "eslint"}} {
		if _, err := jsts.NewScript(c.path, c.name, node); err == nil {
			t.Fatalf("accepted %s as %s", c.path, c.name)
		}
	}
	shim := filepath.Join(filepath.Dir(eslint), "eslint.sh")
	jststest.WriteTree(t, filepath.Dir(shim), map[string]string{"eslint.sh": "#!/bin/sh\n", "wrapper": "#!/bin/sh\nexec node x\n"})
	for _, p := range []string{shim, filepath.Join(filepath.Dir(shim), "wrapper")} {
		if _, err := jsts.NewScript(p, "eslint", node); err == nil {
			t.Fatalf("accepted shim %s", p)
		}
	}
	_ = source.Manifest{}
}
