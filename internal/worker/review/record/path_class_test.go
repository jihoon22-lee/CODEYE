package record

import "testing"

// TC-301: the class of a file that is not shipped code follows the test
// naming rules and the default secondary segments; every default segment
// has a class, and benchmark directories are a class of their own without
// becoming a secondary segment (the review priority does not change).
func TestPathClass(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"svc/handler.go", ""},
		{"svc/handler_test.go", PathTest},
		{"pkg/tests/x.py", PathTest},
		{"src/test/java/A.java", PathTest},
		{"web/src/a.test.ts", PathTest},
		{"testdata/in.go", PathTest},
		{"svc/fixtures/a.go", PathTest},
		{"pkg/mocks/m.go", PathTest},
		{"examples/demo/main.go", PathExample},
		{"docs/samples/a.py", PathExample},
		{"vendor/x/y.go", PathVendored},
		{"web/node_modules/a/index.js", PathVendored},
		{"internal/generated/api.go", PathGenerated},
		{"web/dist/app.js", PathGenerated},
		{"svc/build/out.go", ""}, // Go packages named build are source
		{"benchmarks/run.go", PathBenchmark},
		{"crates/a/benches/b.rs", PathBenchmark},
		{"Vendor/x.go", PathVendored},
		{"README.md", ""},
		{"test", ""},
	} {
		if got := PathClass(c.path); got != c.want {
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
