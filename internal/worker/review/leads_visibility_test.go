package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// reasonCase is one definition lead whose reason the visibility rule
// decides: the anchor line in path, the symbol and the withheld files.
type reasonCase struct {
	name     string
	tree     map[string]string
	path     string
	line     string
	symbol   string
	withheld []string
	// reason is the expected reason; "" expects the lead resolved into
	// resolvedPath.
	reason       string
	resolvedPath string
}

func runReasonCases(t *testing.T, cases []reasonCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, c.tree)
			s := leadStage(t, j)
			if len(c.withheld) > 0 {
				w := map[string]bool{}
				for _, p := range c.withheld {
					w[p] = true
				}
				s.screen = secretScreen{state: screenApplied, withheld: w}
			}
			window, anchor := leadWindow(t, s, c.path, c.line, 0)
			l := resolveOne(t, s, record.LeadNeedDefinition, c.symbol, window, anchor)
			if c.reason == "" {
				if !l.Resolved() || l.Targets[0].Path != c.resolvedPath {
					t.Fatalf("lead %+v", l)
				}
				return
			}
			if l.Resolved() || l.Reason != c.reason {
				t.Fatalf("want %s, lead %+v", c.reason, l)
			}
		})
	}
}

// A withheld file names the reason of an unresolved definition lead only
// when the anchor language lets that file define the name the call uses:
// Go's package of the call (the anchor's for a method of a field or of a
// call's result), an import (or wildcard import) of the name for Python,
// JavaScript and Rust, an include for C. A builtin name of the
// anchor's language never takes the withheld reason, and a project
// definition of a builtin's name is resolved like any other.
func TestLeadReasonFollowsVisibility(t *testing.T) {
	runReasonCases(t, reasonCases())
}

// reasonCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func reasonCases() []reasonCase {
	goMod := "module example.org/app\n\ngo 1.23\n"
	secretGo := "package app\n\nconst token = \"x\"\n"
	return []reasonCase{
		{name: "Go builtin beside a withheld file of its package", tree: map[string]string{"go.mod": goMod,
			"app/use.go": "package app\n\nfunc Use(b []byte) int {\n\treturn len(b)\n}\n", "app/secret.go": secretGo},
			path: "app/use.go", line: "return len(b)", symbol: "len", withheld: []string{"app/secret.go"}, reason: record.LeadDefinitionMissing},
		{name: "Go call of its package beside a withheld file of the package", tree: map[string]string{"go.mod": goMod,
			"app/use.go": "package app\n\nfunc Use(b []byte) int {\n\treturn count(b)\n}\n", "app/secret.go": secretGo},
			path: "app/use.go", line: "return count(b)", symbol: "count", withheld: []string{"app/secret.go"}, reason: record.LeadWithheld},
		{name: "Go call beside a withheld file of another package", tree: map[string]string{"go.mod": goMod,
			"app/use.go": "package app\n\nfunc Use(b []byte) int {\n\treturn count(b)\n}\n", "vault/secret.go": "package vault\n\nconst token = \"x\"\n"},
			path: "app/use.go", line: "return count(b)", symbol: "count", withheld: []string{"vault/secret.go"}, reason: record.LeadDefinitionMissing},
		{name: "Go method of a field beside a withheld file of its package", tree: map[string]string{"go.mod": goMod,
			"app/use.go":    "package app\n\ntype Shop struct {\n\tcfg Config\n}\n\nfunc (s *Shop) Use(b []byte) int {\n\treturn s.cfg.count(b)\n}\n",
			"app/secret.go": "package app\n\ntype Config struct{}\n\nfunc (Config) count(b []byte) int {\n\treturn len(b)\n}\n"},
			path: "app/use.go", line: "return s.cfg.count(b)", symbol: "count", withheld: []string{"app/secret.go"}, reason: record.LeadWithheld},
		{name: "Go method of a call's result beside a withheld file of another package", tree: map[string]string{"go.mod": goMod,
			"app/use.go":      "package app\n\ntype Shop struct{}\n\nfunc open() *Shop {\n\treturn &Shop{}\n}\n\nfunc Use(b []byte) int {\n\treturn open().count(b)\n}\n",
			"vault/secret.go": "package vault\n\nconst token = \"x\"\n"},
			path: "app/use.go", line: "return open().count(b)", symbol: "count", withheld: []string{"vault/secret.go"}, reason: record.LeadDefinitionMissing},
		{name: "Go project function named like a builtin", tree: map[string]string{"go.mod": goMod,
			"app/use.go": "package app\n\nfunc Use(b []byte) int {\n\treturn len(b)\n}\n",
			"app/len.go": "package app\n\nfunc len(b []byte) int {\n\treturn 0\n}\n"},
			path: "app/use.go", line: "return len(b)", symbol: "len", resolvedPath: "app/len.go"},

		{name: "Python builtin beside a wildcard import of a withheld module", tree: map[string]string{
			"app/use.py": "from .secret import *\n\n\ndef use(b):\n    return len(b)\n", "app/secret.py": "TOKEN = 'x'\n"},
			path: "app/use.py", line: "return len(b)", symbol: "len", withheld: []string{"app/secret.py"}, reason: record.LeadDefinitionMissing},
		{name: "Python call a wildcard import of a withheld module may define", tree: map[string]string{
			"app/use.py": "from .secret import *\n\n\ndef use(b):\n    return count(b)\n", "app/secret.py": "TOKEN = 'x'\n"},
			path: "app/use.py", line: "return count(b)", symbol: "count", withheld: []string{"app/secret.py"}, reason: record.LeadWithheld},
		{name: "Python call beside a withheld module imported for another name", tree: map[string]string{
			"app/use.py": "from .secret import other\n\n\ndef use(b):\n    other()\n    return count(b)\n", "app/secret.py": "def other():\n    return 1\n"},
			path: "app/use.py", line: "return count(b)", symbol: "count", withheld: []string{"app/secret.py"}, reason: record.LeadDefinitionMissing},
		{name: "Python builtin an explicit import binds", tree: map[string]string{
			"app/use.py": "from .secret import len\n\n\ndef use(b):\n    return len(b)\n", "app/secret.py": "def len(b):\n    return 0\n"},
			path: "app/use.py", line: "return len(b)", symbol: "len", withheld: []string{"app/secret.py"}, reason: record.LeadWithheld},
		{name: "Python project function named like a builtin", tree: map[string]string{
			"app/use.py": "from .util import len\n\n\ndef use(b):\n    return len(b)\n", "app/util.py": "def len(b):\n    return 0\n"},
			path: "app/use.py", line: "return len(b)", symbol: "len", resolvedPath: "app/util.py"},

		{name: "TypeScript builtin beside an import of a withheld module", tree: map[string]string{
			"app/use.ts":    "import { other } from './secret.js';\n\nexport function use(s: string) {\n  return parseInt(s) + other();\n}\n",
			"app/secret.ts": "export function other() {\n  return 1;\n}\n"},
			path: "app/use.ts", line: "return parseInt(s)", symbol: "parseInt", withheld: []string{"app/secret.ts"}, reason: record.LeadDefinitionMissing},
		{name: "TypeScript call an import of a withheld module binds", tree: map[string]string{
			"app/use.ts":    "import { count } from './secret.js';\n\nexport function use(s: string) {\n  return count(s);\n}\n",
			"app/secret.ts": "export function count(s: string) {\n  return s.length;\n}\n"},
			path: "app/use.ts", line: "return count(s)", symbol: "count", withheld: []string{"app/secret.ts"}, reason: record.LeadWithheld},
		{name: "TypeScript call beside a withheld module imported for another name", tree: map[string]string{
			"app/use.ts":    "import { other } from './secret.js';\n\nexport function use(s: string) {\n  other();\n  return count(s);\n}\n",
			"app/secret.ts": "export function other() {\n  return 1;\n}\n"},
			path: "app/use.ts", line: "return count(s)", symbol: "count", withheld: []string{"app/secret.ts"}, reason: record.LeadDefinitionMissing},
		{name: "TypeScript project function named like a builtin", tree: map[string]string{
			"app/use.ts":  "import { parseInt } from './util.js';\n\nexport function use(s: string) {\n  return parseInt(s);\n}\n",
			"app/util.ts": "export function parseInt(s: string) {\n  return 0;\n}\n"},
			path: "app/use.ts", line: "return parseInt(s)", symbol: "parseInt", resolvedPath: "app/util.ts"},

		{name: "Rust builtin beside a glob use of a withheld module", tree: map[string]string{"Cargo.toml": "[package]\nname = 'app'\nversion = '0.1.0'\n",
			"src/lib.rs": "mod secret;\nuse crate::secret::*;\n\npub fn use_it(v: Vec<u8>) {\n    drop(v);\n}\n", "src/secret.rs": "pub const TOKEN: &str = \"x\";\n"},
			path: "src/lib.rs", line: "drop(v);", symbol: "drop", withheld: []string{"src/secret.rs"}, reason: record.LeadDefinitionMissing},
		{name: "Rust call a glob use of a withheld module may define", tree: map[string]string{"Cargo.toml": "[package]\nname = 'app'\nversion = '0.1.0'\n",
			"src/lib.rs": "mod secret;\nuse crate::secret::*;\n\npub fn use_it(v: Vec<u8>) -> usize {\n    count(v)\n}\n", "src/secret.rs": "pub const TOKEN: &str = \"x\";\n"},
			path: "src/lib.rs", line: "count(v)", symbol: "count", withheld: []string{"src/secret.rs"}, reason: record.LeadWithheld},

		{name: "C library call beside an included withheld header", tree: map[string]string{
			"native/use.c":    "#include <stdio.h>\n#include \"secret.h\"\n\nint use(void) {\n  return printf(\"%d\\n\", TOKEN);\n}\n",
			"native/secret.h": "#define TOKEN 7\n"},
			path: "native/use.c", line: "return printf(", symbol: "printf", withheld: []string{"native/secret.h"}, reason: record.LeadDefinitionMissing},
		{name: "C call an included withheld header may declare", tree: map[string]string{
			"native/use.c":    "#include <stdio.h>\n#include \"secret.h\"\n\nint use(void) {\n  return count(3);\n}\n",
			"native/secret.h": "int count(int n);\n"},
			path: "native/use.c", line: "return count(3)", symbol: "count", withheld: []string{"native/secret.h"}, reason: record.LeadWithheld},
		{name: "C call beside a withheld header it does not include", tree: map[string]string{
			"native/use.c": "#include <stdio.h>\n\nint use(void) {\n  return count(3);\n}\n", "native/secret.h": "int count(int n);\n"},
			path: "native/use.c", line: "return count(3)", symbol: "count", withheld: []string{"native/secret.h"}, reason: record.LeadDefinitionMissing},

		{name: "Java standard class beside a withheld file of its package", tree: map[string]string{"pom.xml": "<project/>\n",
			"src/main/java/app/Use.java":    "package app;\n\npublic class Use {\n  static int use(int a, int b) {\n    return Math.max(a, b);\n  }\n}\n",
			"src/main/java/app/Secret.java": "package app;\n\nclass Secret {\n}\n"},
			path: "src/main/java/app/Use.java", line: "return Math.max(a, b)", symbol: "max", withheld: []string{"src/main/java/app/Secret.java"}, reason: record.LeadDefinitionMissing},
		{name: "Java call into a withheld class an import names", tree: map[string]string{"pom.xml": "<project/>\n",
			"src/main/java/app/Use.java":       "package app;\n\nimport pricing.Rules;\n\npublic class Use {\n  static int use(int a) {\n    return Rules.quote(a);\n  }\n}\n",
			"src/main/java/pricing/Rules.java": "package pricing;\n\npublic final class Rules {\n  public static int quote(int a) {\n    return a;\n  }\n}\n"},
			path: "src/main/java/app/Use.java", line: "return Rules.quote(a)", symbol: "quote", withheld: []string{"src/main/java/pricing/Rules.java"}, reason: record.LeadWithheld},
	}
}

// The builtin lists are small, fixed and per language.
func TestLeadBuiltinListsAreSmall(t *testing.T) {
	for lang, names := range leadBuiltins {
		if len(names) == 0 || len(names) > 160 {
			t.Fatalf("%s: %d names", lang, len(names))
		}
		for name := range names {
			if strings.TrimSpace(name) != name || name == "" {
				t.Fatalf("%s: %q", lang, name)
			}
		}
	}
}
