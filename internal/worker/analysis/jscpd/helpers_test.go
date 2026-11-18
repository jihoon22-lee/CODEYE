package jscpd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
)

// Fixture sources: the replay tests use the same files as the captures in
// testdata/ (taken by real_test.go with CODEYE_W21_CAPTURE), with
// anypacktest.Placeholder where the real test plants a generated secret
// inside the duplicated block.

// block is a 17-line function of more than 100 tokens.
func block(name, token string) string {
	return "function " + name + "(items, token) {\n" +
		"  const key = \"" + token + "\";\n" +
		"  let total = 0;\n" +
		"  for (const item of items) {\n" +
		"    if (item.active && item.value > 10) {\n" +
		"      total += item.value * 2;\n" +
		"    } else if (item.value < 0) {\n" +
		"      total -= item.value;\n" +
		"    } else {\n" +
		"      total += 1;\n" +
		"    }\n" +
		"  }\n" +
		"  const label = \"total:\" + total + \":\" + key.length;\n" +
		"  console.log(label, token);\n" +
		"  return { total, label, count: items.length };\n" +
		"}\n" +
		"module.exports = { " + name + " };\n"
}

// fixtureFiles: src/a.js and lib/b.js share the block; src/ignored.js holds
// it between jscpd:ignore markers; vendor/ is a data directory the plan
// leaves out; native/latin1.c is not UTF-8 and big.js is over 1 MiB
// (neither is handed to jscpd); small.py is under the thresholds.
func fixtureFiles(token string) map[string][]byte {
	big := "// generated\n" + strings.Repeat("const x = 1; // padding padding padding padding padding\n", 20000)
	return map[string][]byte{
		"src/a.js":         []byte(block("alpha", token)),
		"lib/b.js":         []byte(block("alpha", token)),
		"src/ignored.js":   []byte("/* jscpd:ignore-start */\n" + block("alpha", token) + "/* jscpd:ignore-end */\n"),
		"vendor/copy.js":   []byte(block("alpha", token)),
		"native/latin1.c":  []byte("/* caf\xe9 */\nint main(void) { return 0; }\n"),
		"gen/big.js":       []byte(big),
		"tools/small.py":   []byte("def f():\n    return 1\n"),
		".jscpd.json":      []byte(`{"minLines": 1, "minTokens": 1, "ignore": ["**/*.js"]}`),
		"package.json":     []byte(`{"name": "fixture", "jscpd": {"threshold": 0}}`),
		"README.md":        []byte("# fixture\n"),
		"docs/notes.md":    []byte(block("alpha", token)),
		"src/types/a.d.ts": []byte("export declare const x: number;\n"),
	}
}

func noCloneFiles() map[string][]byte {
	return map[string][]byte{"src/one.js": []byte(block("alpha", "x")), "tools/small.py": []byte("def f():\n    return 1\n")}
}

// testTools returns the jscpd entry script (CODEYE_TEST_JSCPD) and the node
// toolchain handle (CODEYE_TEST_NODE), or skips.
func testTools(t *testing.T) (string, pack.Toolchain) {
	t.Helper()
	script, node := os.Getenv("CODEYE_TEST_JSCPD"), os.Getenv("CODEYE_TEST_NODE")
	if script == "" || node == "" {
		t.Skip("set CODEYE_TEST_JSCPD (run-jscpd.js of jscpd 5.4.x) and CODEYE_TEST_NODE to run real-tool tests")
	}
	for _, p := range []string{script, node} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("tool not found: %v", err)
		}
	}
	return script, pack.Toolchain{Name: "node", Executable: node, Dir: filepath.Dir(node), Version: "24"}
}
