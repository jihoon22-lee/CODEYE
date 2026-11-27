package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// bindCase is one definition lead whose call the anchor file's scoping
// binds: the anchor line in path and the symbol. A resolved lead names
// target and the lines holding first and last (the first line holding
// each); an unresolved one names reason.
type bindCase struct {
	name        string
	tree        map[string]string
	path, line  string
	symbol      string
	withheld    []string
	target      string
	first, last string
	partial     bool
	reason      string
}

func runBindCases(t *testing.T, cases []bindCase) {
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
			if c.reason != "" {
				if l.Resolved() || l.Reason != c.reason || l.Targets != nil {
					t.Fatalf("want %s, lead %+v", c.reason, l)
				}
				return
			}
			text := c.tree[c.target]
			start, end := evidenceLine(t, text, c.first), evidenceLine(t, text, c.last)
			if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != c.target || l.Targets[0].Start != start || l.Targets[0].End != end ||
				l.Targets[0].Partial != c.partial {
				t.Fatalf("want %s %d-%d, lead %+v", c.target, start, end, l)
			}
		})
	}
}

const pyHelper = "\n\ndef helper(x):\n    return x * 2\n"

// A Python call binds where Python's scoping puts its name: a module-level
// def of the anchor's file (exported or not, decorated or not) or the one
// import of the name. A parameter, a local, a nested def, a comprehension
// or lambda variable and a class attribute bind the call to no project
// function; a def and an import of the name, or a module-level rebinding,
// leave the run-time order to decide; a read of the name binds nothing; a
// method through self is left to its receiver.
func TestPythonDefinitionLeadsFollowScoping(t *testing.T) {
	runBindCases(t, []bindCase{
		{name: "same-file def after the caller", tree: map[string]string{"app/jobs.py": "def run(x):\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", target: "app/jobs.py", first: "def helper(x):", last: "return x * 2"},
		{name: "decorated same-file def", tree: map[string]string{"app/jobs.py": "import functools\n\n\ndef run(x):\n    return helper(x)\n\n\n@functools.lru_cache(maxsize=None)\ndef helper(x):\n    return x * 2\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", target: "app/jobs.py", first: "@functools.lru_cache", last: "return x * 2"},
		{name: "module function from a method beside a method of the name", tree: map[string]string{"app/jobs.py": "def helper(x):\n    return x * 2\n\n\nclass Job:\n    def helper(self, x):\n        return x\n\n    def run(self, x):\n        return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", target: "app/jobs.py", first: "def helper(x):", last: "return x * 2"},
		{name: "reads of the name refuse nothing", tree: map[string]string{"app/jobs.py": "def helper(x):\n    return x * 2\n\n\nTABLE = {'double': helper}\nORDER = sorted([3, 1], key=helper)\n\n\ndef run(x):\n    pick = TABLE.get('double', helper)\n    return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", target: "app/jobs.py", first: "def helper(x):", last: "return x * 2"},
		{name: "a package re-export in parentheses", tree: map[string]string{"main.py": "from app import helper\n\n\ndef run(x):\n    return helper(x)\n",
			"app/__init__.py": "from .impl import (\n    helper,\n)\n", "app/impl.py": "def helper(x):\n    return x * 2\n"},
			path: "main.py", line: "return helper(x)", symbol: "helper", target: "app/impl.py", first: "def helper(x):", last: "return x * 2"},

		{name: "nested def of the name", tree: map[string]string{"app/jobs.py": "def run(x):\n    def helper(y):\n        return y\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "parameter of the name", tree: map[string]string{"app/jobs.py": "def run(helper, x):\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "local of the name", tree: map[string]string{"app/jobs.py": "def run(make, x):\n    helper = make(x)\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "comprehension variable of the name", tree: map[string]string{"app/jobs.py": "def run(fs):\n    return [helper(1) for helper in fs]\n" + pyHelper},
			path: "app/jobs.py", line: "return [helper(1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "lambda parameter of the name", tree: map[string]string{"app/jobs.py": "apply = lambda helper: helper(1)\n" + pyHelper},
			path: "app/jobs.py", line: "apply = lambda", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "class attribute of the name in its class body", tree: map[string]string{"app/jobs.py": "def helper(x):\n    return x * 2\n\n\nclass Job:\n    helper = staticmethod(abs)\n    value = helper(1)\n"},
			path: "app/jobs.py", line: "value = helper(1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a parameter beside an import of the name from a withheld module", tree: map[string]string{"app/jobs.py": "from .secret import helper\n\n\ndef run(helper, x):\n    return helper(x)\n",
			"app/secret.py": "def helper(x):\n    return x\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", withheld: []string{"app/secret.py"}, reason: record.LeadDefinitionMissing},
		{name: "self call beside a module function of the name", tree: map[string]string{"app/jobs.py": "def helper(x):\n    return x * 2\n\n\nclass Job:\n    def helper(self, x):\n        return x\n\n    def run(self, x):\n        return self.helper(x)\n"},
			path: "app/jobs.py", line: "return self.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},

		{name: "def and import of the name", tree: map[string]string{"app/jobs.py": "from .other import helper\n\n\ndef run(x):\n    return helper(x)\n" + pyHelper,
			"app/other.py": "def helper(x):\n    return x\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "module-level rebinding of the def", tree: map[string]string{"app/jobs.py": "def helper(x):\n    return x * 2\n\n\nhelper = wrap(helper)\n\n\ndef run(x):\n    return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "defs on both branches of a condition", tree: map[string]string{"app/jobs.py": "import sys\n\nif sys.platform == 'win32':\n    def helper(x):\n        return x\nelse:\n    def helper(x):\n        return x * 2\n\n\ndef run(x):\n    return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
	})
}

const tsHelper = "\nfunction helper(x: number): number {\n  return x * 2;\n} // helper end\n"

// A JavaScript or TypeScript call binds where the module's scoping puts its
// name: a module-level function declaration, or a const, let or var holding
// a function or arrow function (exported or not), or the one import of the
// name. A parameter (destructured too), a local, a nested function or a
// catch variable binds the call to no project function; an import and a
// declaration of one name (a syntax error), or an assignment to the
// module's binding, are ambiguous; a read, a class member of the name and
// an assignment to a parameter of the name refuse nothing; this.m() and
// this.#m() are left to their receiver.
func TestScriptDefinitionLeadsFollowScoping(t *testing.T) {
	run := "export function run(x: number) {\n  return helper(x);\n}\n"
	runBindCases(t, []bindCase{
		{name: "same-file function declaration", tree: map[string]string{"app/run.ts": run + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(", last: "// helper end"},
		{name: "same-file arrow function", tree: map[string]string{"app/run.ts": run + "\nconst helper = (x: number): number => x * 2;\n"},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "const helper =", last: "const helper ="},
		{name: "same-file async function expression", tree: map[string]string{"app/run.js": "export async function run(x) {\n  return helper(x);\n}\n\nlet helper = async function (x) {\n  return x * 2;\n}; // helper end\n"},
			path: "app/run.js", line: "return helper(x)", symbol: "helper", target: "app/run.js", first: "let helper =", last: "// helper end"},
		{name: "generic call", tree: map[string]string{"app/run.ts": "export function run(x: number) {\n  return helper<number>(x);\n}\n" + strings.ReplaceAll(tsHelper, "helper(x: number)", "helper<T>(x: T)")},
			path: "app/run.ts", line: "return helper<number>(x)", symbol: "helper", target: "app/run.ts", first: "function helper<T>(", last: "// helper end"},
		{name: "overload signatures and the implementation", tree: map[string]string{"app/run.ts": run + "\nfunction helper(x: string): string;\nfunction helper(x: number): number;\nfunction helper(x: any): any {\n  return x;\n} // helper end\n"},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(x: any)", last: "// helper end"},
		{name: "reads of the name refuse nothing", tree: map[string]string{"app/run.ts": "export function run(x: number, flag: boolean) {\n  const table = { double: helper };\n  [1, 2].map(helper);\n  (flag ? helper : Math.abs)(x);\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(", last: "// helper end"},
		{name: "assignment to a parameter of the name", tree: map[string]string{"app/run.ts": run + "\nexport function wrap(helper: (x: number) => number) {\n  helper = helper.bind(null);\n  return helper;\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(x: number)", last: "// helper end"},
		{name: "module function from a method beside a method and a field of the name", tree: map[string]string{"app/run.ts": "export class Job {\n  helper(x: number) {\n    return x;\n  }\n  other = (x: number) => x\n  helper2 = 1;\n  run(x: number) {\n    return helper(x);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(", last: "// helper end"},
		{name: "a class field of the name", tree: map[string]string{"app/run.ts": "export class Job {\n  helper = (x: number) => x;\n  run(x: number) {\n    return helper(x);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(", last: "// helper end"},
		{name: "an import and an export clause passing it on", tree: map[string]string{"app/run.ts": "import { estimate } from './api.js';\n\nexport function run(x: number) {\n  return estimate(x);\n}\n",
			"app/api.ts": "import { quote } from './rules.js';\n\nexport { quote as estimate };\n", "app/rules.ts": "export function quote(x: number) {\n  return x * 7;\n} // quote end\n"},
			path: "app/run.ts", line: "return estimate(x)", symbol: "estimate", target: "app/rules.ts", first: "export function quote(", last: "// quote end"},
		{name: "a default import of an anonymous default export", tree: map[string]string{"app/run.ts": "import calc from './calc.js';\n\nexport function run(x: number) {\n  return calc(x);\n}\n",
			"app/calc.ts": "export default function (x: number) {\n  return x * 7;\n} // calc end\n"},
			path: "app/run.ts", line: "return calc(x)", symbol: "calc", target: "app/calc.ts", first: "export default function", last: "// calc end"},

		{name: "nested function of the name", tree: map[string]string{"app/run.ts": "export function run(x: number) {\n  function helper(y: number) {\n    return y;\n  }\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "parameter of the name", tree: map[string]string{"app/run.ts": "export function run(helper: (x: number) => number, x: number) {\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "destructured parameter of the name", tree: map[string]string{"app/run.ts": "export function run({ helper }: Deps, x: number) {\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "arrow parameter of the name", tree: map[string]string{"app/run.ts": "export const run = (helper: (x: number) => number) => helper(1);\n" + tsHelper},
			path: "app/run.ts", line: "export const run", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "local of the name", tree: map[string]string{"app/run.ts": "export function run(deps: Deps, x: number) {\n  const helper = deps.pick();\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "catch variable of the name", tree: map[string]string{"app/run.js": "export function run(x) {\n  try {\n    return x();\n  } catch (helper) {\n    return helper(x);\n  }\n}\n\nfunction helper(x) {\n  return x;\n}\n"},
			path: "app/run.js", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "this call beside a module function of the name", tree: map[string]string{"app/run.ts": "export class Job {\n  helper(x: number) {\n    return x;\n  }\n  run(x: number) {\n    return this.helper(x);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return this.helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "private method call beside a module function of the name", tree: map[string]string{"app/run.ts": "export class Job {\n  #helper(x: number) {\n    return x;\n  }\n  run(x: number) {\n    return this.#helper(x);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return this.#helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},

		{name: "import and declaration of the name", tree: map[string]string{"app/run.ts": "import { helper } from './other.js';\n\n" + run + tsHelper,
			"app/other.ts": "export function helper(x: number) {\n  return x;\n}\n"},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "assignment to the module binding", tree: map[string]string{"app/run.js": "export function run(x) {\n  return helper(x);\n}\n\nlet helper = function (x) {\n  return x * 2;\n};\n\nexport function swap(other) {\n  helper = other;\n}\n"},
			path: "app/run.js", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
	})
}

// A TypeScript return annotation is read to the body it ends at: unions,
// generics, arrays, tuples, qualified names, readonly, object types, type
// predicates and assertions, after a parameter list over several lines. An
// annotation the reader cannot follow (a conditional type, a function type)
// refuses the declaration: nothing is sent rather than a wrong window.
func TestTypeScriptReturnAnnotationsReachTheBody(t *testing.T) {
	var cases []bindCase
	for _, c := range []struct {
		name, annotation string
		read             bool
	}{
		{"union", "string | null", true},
		{"generics", "Promise<Map<string, number[]>>", true},
		{"array", "number[]", true},
		{"tuple", "[number, string]", true},
		{"qualified name", "ns.Result<T>", true},
		{"readonly array", "readonly string[]", true},
		{"object type", "{ a: number; b?: string }", true},
		{"array of object types", "Array<{ id: string }>", true},
		{"type predicate", "x is Foo", true},
		{"assertion", "asserts x is Foo", true},
		{"conditional type", "T extends string ? A : B", false},
		{"function type", "() => void", false},
	} {
		helper := "\nfunction helper<T>(\n  x: unknown,\n  y?: T,\n): " + c.annotation + " {\n  return x as never;\n} // helper end\n"
		bc := bindCase{name: c.name, tree: map[string]string{"app/run.ts": "export function run(x: unknown) {\n  return helper(x);\n}\n" + helper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper"}
		if c.read {
			bc.target, bc.first, bc.last = "app/run.ts", "function helper<T>(", "// helper end"
		} else {
			bc.reason = record.LeadDefinitionMissing
		}
		cases = append(cases, bc)
	}
	runBindCases(t, cases)
}

// A relative specifier names the file it spells, else (that file absent)
// TypeScript's source for its output extension: .js to .ts then .tsx, .jsx
// to .tsx, .mjs to .mts, .cjs to .cts, a directory's index.js the same way.
// A .js file beside its .ts source is refused, as is a package import.
// Anchors of every script extension resolve.
func TestScriptSpecifiersReachTypeScriptSources(t *testing.T) {
	callee := "export function helper(x: number) {\n  return x * 2;\n} // helper end\n"
	caller := func(specifier string) string {
		return "import { helper } from '" + specifier + "';\n\nexport function run(x: number) {\n  return helper(x);\n}\n"
	}
	var cases []bindCase
	for _, c := range []struct {
		name, anchor, specifier string
		files                   []string
		target                  string
	}{
		{".js to .tsx", "app/run.ts", "./view.js", []string{"app/view.tsx"}, "app/view.tsx"},
		{".js to .ts before .tsx", "app/run.ts", "./view.js", []string{"app/view.tsx", "app/view.ts"}, "app/view.ts"},
		{".jsx to .tsx", "app/run.tsx", "./view.jsx", []string{"app/view.tsx"}, "app/view.tsx"},
		{".mjs to .mts", "app/run.mts", "./util.mjs", []string{"app/util.mts"}, "app/util.mts"},
		{".cjs to .cts", "app/run.cts", "./util.cjs", []string{"app/util.cts"}, "app/util.cts"},
		{"index.js to index.ts", "app/run.ts", "./lib/index.js", []string{"app/lib/index.ts"}, "app/lib/index.ts"},
		{"the named .js file alone", "app/run.mjs", "./util.js", []string{"app/util.js"}, "app/util.js"},
		{".js beside its .ts source", "app/run.ts", "./util.js", []string{"app/util.js", "app/util.ts"}, ""},
		{"a package import", "app/run.ts", "helpers", []string{"node_modules/helpers/index.ts"}, ""},
	} {
		tree := map[string]string{c.anchor: caller(c.specifier)}
		for _, f := range c.files {
			tree[f] = callee
		}
		bc := bindCase{name: c.name, tree: tree, path: c.anchor, line: "return helper(x)", symbol: "helper"}
		if c.target != "" {
			bc.target, bc.first, bc.last = c.target, "export function helper(", "// helper end"
		} else {
			bc.reason = record.LeadDefinitionMissing
		}
		cases = append(cases, bc)
	}
	for _, anchor := range []string{"app/run.tsx", "app/run.jsx", "app/run.cjs", "app/run.cts", "app/run.mts"} {
		body := "export function run(x) {\n  return helper(x);\n}\n\nfunction helper(x) {\n  return x * 2;\n} // helper end\n"
		cases = append(cases, bindCase{name: "same-file function in " + anchor, tree: map[string]string{anchor: body},
			path: anchor, line: "return helper(x)", symbol: "helper", target: anchor, first: "function helper(", last: "// helper end"})
	}
	runBindCases(t, cases)
}
