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
		"test.js": "test", "src/test.ts": "test", "src/index.test-d.ts": "test",
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

// The decoder accepts excerpts of every review ecosystem with the kind
// their path allows and rejects a kind the path does not allow.
func TestExcerptKindsOfOtherLanguages(t *testing.T) {
	snapshot, sha := strings.Repeat("7", 64), strings.Repeat("a", 64)
	lookup := func(string) (string, bool) { return sha, true }
	ex := func(p, kind, origin string, end int) Excerpt {
		return Excerpt{ID: ExcerptID(snapshot, p, sha, 1, end), Path: p, SHA256: sha, Start: 1, End: end, Kind: kind, Origin: origin}
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
		{ex("src/test.js", "code", "context", 14), true}, // Legacy evidence from before this test name was recognized.
		{ex("src/queue.test-d.ts", "code", "context", 14), true},
		{ex("src/queue.test-d.ts", "test", "context", 14), true},
		{ex("src/queue.ts", "test", "context", 40), false},
		{ex("package.json", "code", "context", 8), false},
	}
	for _, c := range role {
		if err := checkExcerpt(c.e, snapshot, lookup); (err == nil) != c.ok {
			t.Errorf("role %s %s/%s: %v", c.e.Path, c.e.Kind, c.e.Origin, err)
		}
	}
}

func TestPythonDocstringEvidenceRequiresSeparateImplementation(t *testing.T) {
	const implementation = "module/src/example/values.py"
	const page = "module/docs/api.md"
	doc := Excerpt{Path: implementation, Kind: "document", Start: 10, End: 21, DirectivePath: page}
	if !doc.ValidDirectivePath() || doc.DocumentScope() != "module" {
		t.Fatalf("docstring did not inherit its authored page's scope: %+v", doc)
	}
	for _, p := range []string{"another/docs/api.md", "../docs/api.md", "module/docs/api.rst", implementation} {
		bad := doc
		bad.DirectivePath = p
		if bad.ValidDirectivePath() {
			t.Errorf("accepted invalid directive path %q", p)
		}
	}
	comparison := ContractComparison{ContractIndex: 1, AppliesTo: "module", Reason: "구현은 기본값을 반환한다.",
		Status: "match", DocumentEvidenceIDs: []string{"doc"}, CodeEvidenceIDs: []string{"runtime", "test"}}
	catalog := map[string]Excerpt{
		"doc":     doc,
		"runtime": {Path: implementation, Kind: "code", Start: 1, End: 23},
		"test":    {Path: "module/tests/test_values.py", Kind: "test", Start: 1, End: 5},
	}
	if comparison.Valid(catalog) {
		t.Fatal("documentation and static test expectations sufficed as implementation")
	}
	catalog["runtime"] = Excerpt{Path: implementation, Kind: "code", Start: 22, End: 33}
	if !comparison.Valid(catalog) {
		t.Fatal("independent implementation could not support the comparison")
	}
}
