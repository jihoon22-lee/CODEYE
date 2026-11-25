package record

import (
	"encoding/json"
	"os"
	"testing"
)

// TC-301: PathClass names only what is certainly not shipped code: a test
// file by its language's naming convention, a well-known root (src/test of
// a Java module, testdata, __tests__, examples, benchmarks and a tests
// directory that is not inside a source tree) and a vendored directory. A
// directory named test, demo, gen or build somewhere inside a source tree is
// not certain and stays shipped code, whatever the old rule said.
func TestPathClass(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"svc/handler.go", ""},
		{"svc/handler_test.go", PathTest},
		{"pkg/tests/test_x.py", PathTest},
		{"src/test/java/A.java", PathTest},
		{"web/src/a.test.ts", PathTest},
		{"web/src/b.spec.js", PathTest},
		{"pkg/conftest.py", PathTest},
		{"crates/a/tests/b.rs", PathTest},
		{"tests/it.rs", PathTest},
		{"test/util.py", PathTest},
		{"src/app/__tests__/x.js", PathTest},
		{"testdata/in.go", PathTest},
		{"svc/testdata/a.go", PathTest},
		{"examples/demo/main.go", PathExample},
		{"docs/examples/a.py", PathExample},
		{"vendor/x/y.go", PathVendored},
		{"web/node_modules/a/index.js", PathVendored},
		{"Vendor/x.go", PathVendored},
		{"benchmarks/run.go", PathBenchmark},
		{"crates/a/benches/b.rs", PathBenchmark},
		{"svc/build/out.go", ""},
		{"README.md", ""},
		{"test", ""},
		// Reproduced guesses of the earlier rule: shipped code.
		{"internal/gen/gen.go", ""},
		{"cmd/gen/main.go", ""},
		{"src/main/java/org/foo/test/Util.java", ""},
		{"django/test/client.py", ""},
		{"src/demo/main.go", ""},
		{"pkg/build/x.py", ""},
		{"web/dist/app.js", ""},
		{"internal/generated/api.go", ""},
		{"svc/fixtures/a.go", ""},
		{"pkg/mocks/m.go", ""},
		{"src/examples/a.go", ""},
		{"lib/tests/helper.py", ""},
	} {
		if got := PathClass(c.path); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
}

// The earlier rule stays for the reports of builder 23, which recorded the
// class it gave.
func TestLegacyPathClassIsTheEarlierRule(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"svc/fixtures/a.go", PathTest},
		{"pkg/mocks/m.go", PathTest},
		{"internal/generated/api.go", PathGenerated},
		{"web/dist/app.js", PathGenerated},
		{"django/test/client.py", PathTest},
		{"svc/build/out.go", ""},
		{"crates/a/benches/b.rs", PathBenchmark},
	} {
		if got := LegacyPathClass(c.path); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
	for _, seg := range DefaultSecondary.Segments {
		if !contains(PathClasses, pathClassOfSegment[seg]) {
			t.Errorf("secondary segment %s has no path class", seg)
		}
	}
	if DefaultSecondary.Match("benchmarks/run.go") != "" {
		t.Error("benchmarks became a secondary segment: the review priority would change")
	}
}

// The table scripts/bundle_checks/review.py checks its copy of the rule
// against: one definition of the class, two readers.
func TestPathClassSharedTable(t *testing.T) {
	b, err := os.ReadFile("testdata/path_class.json")
	if err != nil {
		t.Fatal(err)
	}
	var table []struct{ Path, Class string }
	if err := json.Unmarshal(b, &table); err != nil || len(table) == 0 {
		t.Fatal(err)
	}
	for _, c := range table {
		if got := PathClass(c.Path); got != c.Class {
			t.Errorf("%s: %q, want %q", c.Path, got, c.Class)
		}
	}
}
