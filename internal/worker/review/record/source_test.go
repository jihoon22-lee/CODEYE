package record

import (
	"strings"
	"testing"
)

// Excerpt kinds by path, for every review ecosystem. Go keeps
// its rules (a directory never makes a Go test).
func TestSourceKinds(t *testing.T) {
	cases := map[string]string{
		"main.go": "code", "pkg/x_test.go": "test", "tests/x.go": "code", "X.GO": "code",
		"app/store.py": "code", "app/stubs.pyi": "code", "tests/helpers.py": "test", "app/test_store.py": "test", "app/store_test.py": "test", "conftest.py": "test",
		"src/queue.ts": "code", "src/queue.test.ts": "test", "src/queue.spec.js": "test", "src/__tests__/q.tsx": "test", "test/q.mjs": "test", "src/index.d.ts": "code",
		"src/lib.rs": "code", "src/parse/tests.rs": "test", "tests/it.rs": "test", "benches/b.rs": "code",
		"src/ring.cc": "code", "include/ring.h": "code", "tests/ring.cc": "test", "src/ring_test.cc": "test", "src/ring_unittest.cpp": "test", "src/test_ring.c": "test", "src/ring-test.cxx": "test",
		"README.md": "", "package.json": "", "Cargo.toml": "", "src/main.java": "", "Makefile": "",
	}
	for p, want := range cases {
		if got := SourceKind(p); got != want {
			t.Errorf("SourceKind(%q) = %q, want %q", p, got, want)
		}
	}
	manifests := map[string]string{
		"go.mod": "go", "sub/pyproject.toml": "python", "setup.cfg": "python", "web/package.json": "javascript", "tsconfig.json": "javascript",
		"crate/Cargo.toml": "rust", "CMakeLists.txt": "cpp", "build/compile_commands.json": "", "compile_commands.json": "",
		"Makefile": "", "go.sum": "", "package-lock.json": "", "requirements.txt": "", "setup.py": "",
	}
	for p, want := range manifests {
		if got := ManifestEcosystem(p); got != want {
			t.Errorf("ManifestEcosystem(%q) = %q, want %q", p, got, want)
		}
	}
}

// Both decoders accept excerpts of every review ecosystem with the kind
// their path allows and reject a kind the path does not allow.
func TestExcerptKindsOfOtherLanguages(t *testing.T) {
	snapshot, sha := strings.Repeat("7", 64), strings.Repeat("a", 64)
	lookup := func(string) (string, bool) { return sha, true }
	ex := func(p, kind, origin string, end int) Excerpt {
		return Excerpt{ID: ExcerptID(snapshot, p, sha, 1, end), Path: p, SHA256: sha, Start: 1, End: end, Kind: kind, Origin: origin}
	}
	legacy := []struct {
		e  Excerpt
		ok bool
	}{
		{ex("app/store.py", "code", "context", 30), true},
		{ex("tests/test_store.py", "test", "context", 10), true},
		{ex("tests/test_store.py", "code", "context", 10), true}, // as a Go test file, code is accepted
		{ex("pyproject.toml", "config", "context", 4), true},
		{ex("src/lib.rs", "code", "", 3), true},
		{ex("app/store.py", "test", "context", 30), false},
		{ex("README.md", "code", "context", 3), false},
		{ex("Makefile", "config", "context", 3), false},
		{ex("app/store.py", "config", "context", 3), false},
	}
	for _, c := range legacy {
		if err := checkExcerpt(c.e, snapshot, lookup); (err == nil) != c.ok {
			t.Errorf("legacy %s %s: %v", c.e.Path, c.e.Kind, err)
		}
	}
	role := []struct {
		e  Excerpt
		ok bool
	}{
		{ex("src/queue.ts", "code", "context", 40), true},
		{ex("src/queue.test.ts", "test", "context", 14), true},
		{ex("src/ring.cc", "code", "observation", 21), true},
		{ex("tests/it.rs", "test", "observation", 8), true},
		{ex("src/queue.test.ts", "code", "context", 14), false},
		{ex("src/queue.ts", "test", "context", 40), false},
		{ex("package.json", "code", "context", 8), false},
	}
	for _, c := range role {
		if err := checkRoleExcerpt(c.e, snapshot, lookup); (err == nil) != c.ok {
			t.Errorf("role %s %s/%s: %v", c.e.Path, c.e.Kind, c.e.Origin, err)
		}
	}
}
