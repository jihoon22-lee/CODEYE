package lizard

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Fixture sources stay generated strings: the replay tests use the same
// files as the captures in testdata/ (taken by real_test.go with
// CODEYE_W21_CAPTURE), with anypacktest.Placeholder where the real test
// plants a generated secret as a default argument value.

// branches writes n "if" statements of the language (CCN 1+n).
func branches(n int, line string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, line, i, i)
	}
	return b.String()
}

// pyComplex: classify (line 4, CCN 17, a default argument holding token),
// simple (CCN 1) and forgiven (CCN 17 under "# #lizard forgive").
func pyComplex(token string) string {
	return "\"\"\"W21 lizard fixture.\"\"\"\n\n\ndef classify(value, token=\"" + token + "\"):\n" +
		branches(16, "    if value == %d:\n        return %d\n") + "    return len(token)\n\n\n" +
		"def simple(a, b):\n    return a + b\n\n\n" +
		"def forgiven(value):\n    # #lizard forgive\n" + branches(16, "    if value == %d:\n        return %d\n") + "    return 0\n"
}

// goComplex: Route (line 3, CCN 17).
const goComplexHead = "package route\n\n"

func goComplex() string {
	return goComplexHead + "func Route(v int) int {\n" + branches(16, "\tif v == %d {\n\t\treturn %d\n\t}\n") + "\treturn 0\n}\n"
}

const (
	jsSimple = "export function add(a, b) {\n  return a + b;\n}\n"
	// latin1 is not UTF-8: lizard reports it on stderr and measures nothing.
	latin1C = "/* caf\xe9 */\nint main(void) { return 0; }\n"
)

// fixtureFiles is the measured snapshot; vendor/ and testdata/ are data
// directories the plan leaves out, README.md has no reader.
func fixtureFiles(token string, withLatin1 bool) map[string][]byte {
	files := map[string][]byte{
		"src/complex.py":      []byte(pyComplex(token)),
		"src/route/route.go":  []byte(goComplex()),
		"web/add.js":          []byte(jsSimple),
		"vendor/dep/dep.py":   []byte(pyComplex("vendored")),
		"testdata/gen.py":     []byte(pyComplex("data")),
		"README.md":           []byte("# fixture\n"),
		"whitelizard.txt":     []byte("classify\n"),
		".gitignore":          []byte("src/\n"),
		"src/route/go.mod.md": []byte("not source\n"),
	}
	if withLatin1 {
		files["native/latin1.c"] = []byte(latin1C)
	}
	return files
}

// testLizard returns the operator-installed lizard named by
// CODEYE_TEST_LIZARD, or skips: CI has no lizard and the adapter never
// searches PATH or installs a tool.
func testLizard(t *testing.T) string {
	t.Helper()
	p := os.Getenv("CODEYE_TEST_LIZARD")
	if p == "" {
		t.Skip("set CODEYE_TEST_LIZARD to the lizard executable (1.26.x) to run real-tool tests")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("lizard not found: %v", err)
	}
	return p
}
