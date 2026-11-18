package discovery

import (
	"context"
	"reflect"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func ids(ms []envdoc.Module) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func moduleByID(t *testing.T, ms []envdoc.Module, id string) envdoc.Module {
	t.Helper()
	for _, m := range ms {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("module %s missing in %v", id, ids(ms))
	return envdoc.Module{}
}

func full() envdoc.Scope { s, _ := normalScope(domain.Scope{Kind: "full"}); return s }

func TestDetectModulesNestedScopingAndSkips(t *testing.T) {
	m := manifestOf(
		"go.mod", "package.json", "svc/a/go.mod", "svc/a/inner/go.mod", "svc/a/inner/deep/go.mod", "svc/a/web/package.json",
		"testdata/go.mod", "vendor/x/go.mod", "_tools/go.mod", ".github/package.json", "vendor/lib/package.json",
		"CMakeLists.txt", "src/CMakeLists.txt", "src/lib/Makefile", "native/meson.build",
		"py/pyproject.toml", "py/requirements-dev.txt", "py/.python-version", "rs/Cargo.toml", "rs/rust-toolchain.toml",
	)
	mods, lims := DetectModules(m, full())
	got := ids(mods)
	expect := []string{"cpp:.", "go:.", "javascript:.", "python:py", "rust:rs", "go:svc/a", "go:svc/a/inner", "go:svc/a/inner/deep", "javascript:svc/a/web", "javascript:vendor/lib"}
	if !reflect.DeepEqual(got, expect) {
		t.Fatalf("modules = %v\nwant      %v", got, expect)
	}
	if r := moduleByID(t, mods, "go:."); !reflect.DeepEqual(r.ExcludedPaths, []string{"svc/a"}) {
		t.Fatalf("root excludes %v, want only the outermost nested Go module", r.ExcludedPaths)
	}
	if a := moduleByID(t, mods, "go:svc/a"); !reflect.DeepEqual(a.ExcludedPaths, []string{"svc/a/inner"}) {
		t.Fatalf("svc/a excludes %v", a.ExcludedPaths)
	}
	// A nested JavaScript package does not split the Go module.
	if js := moduleByID(t, mods, "javascript:."); !reflect.DeepEqual(js.ExcludedPaths, []string{"svc/a/web", "vendor/lib"}) {
		t.Fatalf("js root excludes %v", js.ExcludedPaths)
	}
	// Nested C/C++ build files are merged into the outermost module.
	if c := moduleByID(t, mods, "cpp:."); !reflect.DeepEqual(c.Manifests, []string{"CMakeLists.txt", "native/meson.build", "src/CMakeLists.txt", "src/lib/Makefile"}) || len(c.ExcludedPaths) != 0 {
		t.Fatalf("cpp root = %+v", c)
	}
	if py := moduleByID(t, mods, "python:py"); !reflect.DeepEqual(py.Manifests, []string{"py/.python-version", "py/pyproject.toml", "py/requirements-dev.txt"}) {
		t.Fatalf("python = %+v", py)
	}
	if !reflect.DeepEqual(uniqueSorted(lims), []string{LimNestedBuildFiles}) {
		t.Fatalf("limitations %v", lims)
	}
}

func TestDetectModulesIgnoresExcludedEntries(t *testing.T) {
	m := manifestOf("go.mod")
	size := int64(3)
	m.Entries = append(m.Entries, source.Entry{Path: "linked/go.mod", Kind: "symlink", Size: &size, ExcludedReason: "link_not_followed"})
	mods, _ := DetectModules(m, full())
	if !reflect.DeepEqual(ids(mods), []string{"go:."}) {
		t.Fatalf("excluded manifest became a module: %v", ids(mods))
	}
}

func TestModuleScopeFiltering(t *testing.T) {
	m := manifestOf("go.mod", "svc/a/go.mod", "svc/b/go.mod", "svc/a/a.go", "svc/README.md", "docs/x.md")
	cases := map[string]struct {
		scope domain.Scope
		in    []string
	}{
		"empty kind is full": {domain.Scope{}, []string{"go:.", "go:svc/a", "go:svc/b"}},
		"full":               {domain.Scope{Kind: "full"}, []string{"go:.", "go:svc/a", "go:svc/b"}},
		"nested module dir":  {domain.Scope{Kind: "paths", Paths: []string{"svc/a"}}, []string{"go:svc/a"}},
		"file in module":     {domain.Scope{Kind: "paths", Paths: []string{"svc/a/a.go"}}, []string{"go:svc/a"}},
		"parent of modules":  {domain.Scope{Kind: "paths", Paths: []string{"svc"}}, []string{"go:.", "go:svc/a", "go:svc/b"}},
		"root-owned dir":     {domain.Scope{Kind: "paths", Paths: []string{"docs"}}, []string{"go:."}},
		"two paths":          {domain.Scope{Kind: "paths", Paths: []string{"svc/b", "docs/x.md"}}, []string{"go:.", "go:svc/b"}},
	}
	for name, c := range cases {
		s, ok := normalScope(c.scope)
		if !ok {
			t.Fatalf("%s: scope rejected", name)
		}
		mods, _ := DetectModules(m, s)
		in := []string{}
		for _, mod := range mods {
			if mod.InScope {
				in = append(in, mod.ID)
			}
		}
		if !reflect.DeepEqual(in, c.in) {
			t.Fatalf("%s: in scope %v, want %v", name, in, c.in)
		}
	}
	for _, bad := range []domain.Scope{{Kind: "modules"}, {Kind: "paths"}, {Kind: "paths", Paths: []string{"../x"}}, {Kind: "paths", Paths: []string{"a", "a"}}} {
		if _, ok := normalScope(bad); ok {
			t.Fatalf("scope %+v accepted", bad)
		}
	}
	if s, _ := normalScope(domain.Scope{Kind: "paths", Paths: []string{"b", "a"}}); !reflect.DeepEqual(s.Paths, []string{"a", "b"}) {
		t.Fatalf("scope paths not sorted: %v", s.Paths)
	}
}

func TestEnvironmentScopeLeavesOutOfScopeModulesUnread(t *testing.T) {
	m := manifestOf("go.mod", "svc/b/go.mod", "web/package.json")
	read := map[string]int{}
	reader := func(e source.Entry) ([]byte, error) {
		read[e.Path]++
		switch e.Path {
		case "go.mod":
			return []byte("module r\n\ngo 1.22\n"), nil
		case "svc/b/go.mod":
			return []byte("module b\n\ngo 1.30\n"), nil
		}
		return []byte(`{"engines":{"node":">=18"}}`), nil
	}
	in := Input{Manifest: m, Digest: domain.Digest([]byte("x")), Scope: domain.Scope{Kind: "paths", Paths: []string{"svc/b"}}, RunID: domain.NewID(), AttemptID: domain.NewID()}
	var asked []string
	doc, _, err := Environment(in, reader, func(tools []string) Observed {
		asked = tools
		return StaticObserver{Versions: map[string]string{"go": "1.27.4", "node": "20.1.0"}}.Observe(context.Background(), analysis.Job{}, tools, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, map[string]int{"svc/b/go.mod": 1}) || !reflect.DeepEqual(asked, []string{"go"}) {
		t.Fatalf("read %v asked %v", read, asked)
	}
	e := doc.Environment
	if len(e.Requirements) != 1 || len(e.Verdicts) != 1 || e.Verdicts[0].Module != "go:svc/b" || e.Verdicts[0].Status != envdoc.VerdictMismatch {
		t.Fatalf("environment %+v", e)
	}
	if _, err := envdoc.Marshal(doc, in.Expect(envdoc.KindEnvironment)); err != nil {
		t.Fatal(err)
	}
	in.Scope = domain.Scope{Kind: "modules"}
	if _, _, err := Environment(in, reader, nil); err != errScope {
		t.Fatalf("unsupported scope: %v", err)
	}
}
