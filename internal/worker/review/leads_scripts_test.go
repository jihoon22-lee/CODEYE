package review

import (
	"slices"
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
// function, as does a target inside a tuple or list display (of a for, a
// with, an assignment or a comprehension, nested or starred); a def and an
// import of the name, or a module-level rebinding (a tuple target too),
// leave the run-time order to decide; a read of the name (in a tuple
// argument or a subscript key too) binds nothing; a method through self is
// left to its receiver.
func TestPythonDefinitionLeadsFollowScoping(t *testing.T) {
	runBindCases(t, pythonScopingCases())
}

// pythonScopingCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func pythonScopingCases() []bindCase {
	return []bindCase{
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
		{name: "a tuple argument and a subscript key holding the name refuse nothing", tree: map[string]string{"app/jobs.py": "def helper(x):\n    return x * 2\n\n\ndef run(x, table):\n    print((x, helper))\n    table[(x, helper)] = 1\n    return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", target: "app/jobs.py", first: "def helper(x):", last: "return x * 2"},

		{name: "a for target in parentheses", tree: map[string]string{"app/jobs.py": "def run(pairs, x):\n    for i, (name, helper) in enumerate(pairs):\n        return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a nested starred for target", tree: map[string]string{"app/jobs.py": "def run(pairs, x):\n    for (a, (b, *helper)), c in pairs:\n        return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a for target after a display", tree: map[string]string{"app/jobs.py": "def run(pairs, x):\n    for (a, b), helper in pairs:\n        return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a tuple assignment target in parentheses", tree: map[string]string{"app/jobs.py": "def run(make, x):\n    (a, helper) = make()\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a tuple target opening a line after a name", tree: map[string]string{"app/jobs.py": "def run(make, x):\n    y = make\n    (a, helper) = y()\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a list assignment target", tree: map[string]string{"app/jobs.py": "def run(make, x):\n    [a, helper] = make()\n    return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a comprehension target in parentheses", tree: map[string]string{"app/jobs.py": "def run(pairs):\n    return [helper(1) for (k, helper) in pairs]\n" + pyHelper},
			path: "app/jobs.py", line: "return [helper(1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a with target in parentheses", tree: map[string]string{"app/jobs.py": "def run(ctx, x):\n    with ctx() as (a, helper):\n        return helper(x)\n" + pyHelper},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
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
		{name: "a module-level tuple target beside the def", tree: map[string]string{"app/jobs.py": "(other, helper) = make()\n" + pyHelper + "\n\ndef run(x):\n    return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "defs on both branches of a condition", tree: map[string]string{"app/jobs.py": "import sys\n\nif sys.platform == 'win32':\n    def helper(x):\n        return x\nelse:\n    def helper(x):\n        return x * 2\n\n\ndef run(x):\n    return helper(x)\n"},
			path: "app/jobs.py", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
	}
}

const tsHelper = "\nfunction helper(x: number): number {\n  return x * 2;\n} // helper end\n"

// A JavaScript or TypeScript call binds where the module's scoping puts its
// name: a module-level function declaration, or a const, let or var holding
// a function or arrow function (exported or not), or the one import of the
// name. A parameter (destructured too, of a generic, annotated, private,
// computed or decorated function or method), a local, a nested function, a
// catch variable or the name of a named function expression in its body
// binds the call to no project function, and such an expression binds no
// module name; a method head of the name is no call of it. An import and a
// declaration of one name (a syntax error), an assignment to the module's
// binding, or a var or a function declaration of the name in a module-level
// block are ambiguous; a read, a condition, a class member of the name, an
// assignment to a parameter of the name and a var in another function's
// block refuse nothing; this.m() and this.#m() are left to their receiver.
// An arrow function's expression ends where its line no longer goes on (a
// line opening with ".", "?" or ":", one after an operator); where a later
// line may go on with it (a template), the window is partial.
func TestScriptDefinitionLeadsFollowScoping(t *testing.T) {
	runBindCases(t, scriptScopingCases())
}

// scriptScopingCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func scriptScopingCases() []bindCase {
	run := "export function run(x: number) {\n  return helper(x);\n}\n"
	return []bindCase{
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
		{name: "conditions on the name refuse nothing", tree: map[string]string{"app/run.ts": "export async function run(x: number, ks: AsyncIterable<number>) {\n  for await (const k of helper(ks)) {\n  }\n  while (helper) {\n  }\n  switch (helper) {\n  }\n  if (helper) {\n    return helper(x);\n  }\n  return 0;\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(", last: "// helper end"},
		{name: "module function from a generic method", tree: map[string]string{"app/run.ts": "export class Job {\n  run<T>(x: T): number {\n    return helper(1);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(1)", symbol: "helper", target: "app/run.ts", first: "function helper(", last: "// helper end"},
		{name: "an arrow expression continued by ? and : lines", tree: map[string]string{"app/run.ts": "export function run(x: number) {\n  return pick(x);\n}\n\nconst pick = (x: number) =>\n  x > 0\n    ? \"pos\"\n    : \"neg\"; // pick end\n"},
			path: "app/run.ts", line: "return pick(x)", symbol: "pick", target: "app/run.ts", first: "const pick =", last: "// pick end"},
		{name: "an arrow expression continued by . lines", tree: map[string]string{"app/run.js": "export function run(url) {\n  return load(url);\n}\n\nconst load = (url) => fetch(url)\n  .then((r) => r.json())\n  .catch(() => null); // load end\n\nconst other = 1;\n"},
			path: "app/run.js", line: "return load(url)", symbol: "load", target: "app/run.js", first: "const load =", last: "// load end"},
		{name: "an arrow expression continued after an operator", tree: map[string]string{"app/run.js": "export function run(x) {\n  return scale(x);\n}\n\nconst scale = (x) => x *\n  2 // scale end\nexport const other = 1;\n"},
			path: "app/run.js", line: "return scale(x)", symbol: "scale", target: "app/run.js", first: "const scale =", last: "// scale end"},
		{name: "an arrow expression a template may continue is partial", tree: map[string]string{"app/run.js": "export function run(x) {\n  return tag(x);\n}\n\nconst tag = (x) => x\n`tail`;\n"},
			path: "app/run.js", line: "return tag(x)", symbol: "tag", target: "app/run.js", first: "const tag =", last: "const tag =", partial: true},
		{name: "a var in another function's block refuses nothing", tree: map[string]string{"app/run.cjs": "function other(y) {\n  if (y) {\n    var helper = y;\n  }\n}\n\nfunction run(x) {\n  return helper(x);\n}\n\nfunction helper(x) {\n  return x * 2;\n} // helper end\n\nmodule.exports = { run, other };\n"},
			path: "app/run.cjs", line: "return helper(x)", symbol: "helper", target: "app/run.cjs", first: "function helper(", last: "// helper end"},

		{name: "a parameter of a generic function", tree: map[string]string{"app/run.ts": "export function run<T>(helper: (x: T) => T, x: T) {\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a parameter of a generic function with a return annotation", tree: map[string]string{"app/run.ts": "export function run<T>(helper: (x: T) => T, x: T): T {\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a parameter of a generic method", tree: map[string]string{"app/run.ts": "export class Job {\n  run<T>(helper: () => T): T {\n    return helper();\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper()", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a destructured parameter of a generic function", tree: map[string]string{"app/run.ts": "export function run<T>({ helper }: Deps<T>, x: T) {\n  return helper(x);\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a parameter of a private method", tree: map[string]string{"app/run.ts": "export class Job {\n  #run(helper: (x: number) => number) {\n    return helper(1);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a parameter of a computed method", tree: map[string]string{"app/run.ts": "const key = 'run';\n\nexport class Job {\n  [key](helper: (x: number) => number) {\n    return helper(1);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a parameter of a decorated method", tree: map[string]string{"app/run.ts": "export class Job {\n  @Trace() run(helper: (x: number) => number) {\n    return helper(1);\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "return helper(1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a generic method head of the name", tree: map[string]string{"app/run.ts": "export class Job {\n  helper<T>(x: T): T {\n    return x;\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "helper<T>(x: T): T {", symbol: "helper", reason: record.LeadSymbolNotAtAnchor},
		{name: "a decorated method head of the name", tree: map[string]string{"app/run.ts": "export class Job {\n  @Trace() helper(x: number) {\n    return x;\n  }\n}\n" + tsHelper},
			path: "app/run.ts", line: "@Trace() helper(x: number) {", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a recursive call in a named function expression of the name", tree: map[string]string{"app/run.ts": "export const fact = function helper(n: number): number {\n  return n <= 1 ? 1 : n * helper(n - 1);\n};\n" + tsHelper},
			path: "app/run.ts", line: "n * helper(n - 1)", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a named function expression is no module binding", tree: map[string]string{"app/run.ts": run + "\nexport const api = function helper(x: number) {\n  return x * 2;\n};\n"},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadDefinitionMissing},
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
		{name: "a var in a block at module level beside the declaration", tree: map[string]string{"app/run.cjs": "function run(x) {\n  return helper(x);\n}\n\nfunction helper(x) {\n  return x;\n}\n\nif (process.env.FAST) {\n  var helper = function (x) {\n    return x * 2;\n  };\n}\n\nmodule.exports = { run };\n"},
			path: "app/run.cjs", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a var in a for head at module level beside the declaration", tree: map[string]string{"app/run.cjs": "function run(x) {\n  return helper(x);\n}\n\nfunction helper(x) {\n  return x;\n}\n\nfor (var helper of [Math.abs]) {\n}\n\nmodule.exports = { run };\n"},
			path: "app/run.cjs", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
		{name: "a function declaration in a block at module level beside the declaration", tree: map[string]string{"app/run.cjs": "function run(x) {\n  return helper(x);\n}\n\nfunction helper(x) {\n  return x;\n}\n\nif (process.env.FAST) {\n  function helper(x) {\n    return x * 2;\n  }\n}\n\nmodule.exports = { run };\n"},
			path: "app/run.cjs", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous},
	}
}

// A TypeScript return annotation is read to the body it ends at: unions,
// generics, arrays, tuples, qualified names, readonly, object types, type
// predicates and assertions, after a parameter list over several lines. An
// annotation the reader cannot follow (a conditional type, a function type)
// refuses the declaration: nothing is sent rather than a wrong window.
func TestTypeScriptReturnAnnotationsReachTheBody(t *testing.T) {
	runBindCases(t, returnAnnotationCases())
}

// returnAnnotationCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func returnAnnotationCases() []bindCase {
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
	return cases
}

// A relative specifier names the file it spells, else (that file absent)
// TypeScript's source for its output extension: .js to .ts then .tsx, .jsx
// to .tsx, .mjs to .mts, .cjs to .cts, a directory's index.js the same way.
// A .js file beside its .ts source is refused, as is a package import.
// Anchors of every script extension resolve.
func TestScriptSpecifiersReachTypeScriptSources(t *testing.T) {
	runBindCases(t, specifierCases())
}

// specifierCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func specifierCases() []bindCase {
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
	return cases
}

// slashCase is a JavaScript or TypeScript text and how the lead scan reads
// its slashes: the regular expression literals in order, the number of
// division operators, names the scan must see and names a literal must hide;
// or nothing, the scan giving up.
type slashCase struct {
	name      string
	text      string
	regexes   []string
	divisions int
	names     []string
	hidden    []string
	abandon   bool
}

// The lead scan reads a "/" after an operand as division and a "/" where an
// operand starts as a regular expression literal, one opaque token; a
// template's ${...} expressions are read as code to their "}". Where the two
// cannot be told apart (after "}" or "<"), at an unterminated literal and at
// a "\" outside a literal it gives up.
func TestScriptLeadTokensTellRegularExpressionsFromDivision(t *testing.T) {
	for _, c := range slashCases() {
		t.Run(c.name, func(t *testing.T) {
			tokens := leadTokens("app/slashes.ts", c.text)
			if c.abandon {
				if tokens != nil {
					t.Fatalf("read %d tokens, want none", len(tokens))
				}
				return
			}
			if tokens == nil {
				t.Fatal("the scan gave up")
			}
			var regexes, names []string
			divisions := 0
			for _, tok := range tokens {
				switch {
				case tok.stringLiteral && !tok.quoted && strings.HasPrefix(tok.text, "/"):
					regexes = append(regexes, tok.text)
				case !tok.quoted && !tok.stringLiteral && tok.text == "/":
					divisions++
				case tok.name:
					names = append(names, tok.text)
				}
			}
			if strings.Join(regexes, " ") != strings.Join(c.regexes, " ") || divisions != c.divisions {
				t.Fatalf("regular expressions %q and %d divisions, want %q and %d", regexes, divisions, c.regexes, c.divisions)
			}
			for _, n := range c.names {
				if !slices.Contains(names, n) {
					t.Fatalf("name %s missing from %q", n, names)
				}
			}
			for _, n := range c.hidden {
				if slices.Contains(names, n) {
					t.Fatalf("name %s of a literal read as code: %q", n, names)
				}
			}
		})
	}
}

// slashCases lists the test's cases; the scan's fuzz test seeds with their
// texts.
func slashCases() []slashCase {
	return []slashCase{
		{name: "a call argument", text: `s = s.replace(/[\u0100-\uffff]/g, '');`, regexes: []string{`/[\u0100-\uffff]/g`}},
		{name: "a class of characters beyond ASCII", text: "s = s.replace(/[Ā-ɏ]/g, '');", regexes: []string{"/[Ā-ɏ]/g"}},
		{name: "after = and :", text: "const re = /a+b/;\nconst o = { re: /x/i };", regexes: []string{"/a+b/", "/x/i"}},
		{name: "after , and [", text: "f(a, /y/);\nconst list = [/a/, /b/g];", regexes: []string{"/y/", "/a/", "/b/g"}},
		{name: "after !, &&, || and ?", text: `ok = !/^\d+$/.test(s) && /x/.test(s) || b ? /y/ : /z/;`, regexes: []string{`/^\d+$/`, "/x/", "/y/", "/z/"}},
		{name: "after ;, return and typeof", text: "x = 1;/re/.test(s);\nfunction f(s) {\n  return /x/.test(s) && typeof /y/;\n}\n", regexes: []string{"/re/", "/x/", "/y/"}},
		{name: "after an if condition, else and an arrow", text: "if (s) /x/.exec(s);\nelse /w/.exec(s);\nconst f = (s) => /y/.test(s);", regexes: []string{"/x/", "/w/", "/y/"}},
		{name: "a class holding a slash", text: "parts = path.split(/[/]/);", regexes: []string{"/[/]/"}},
		{name: "an escaped slash and an escaped bracket", text: `parts = path.split(/a\/b/);` + "\n" + `m = /[\]/]+/u;`, regexes: []string{`/a\/b/`, `/[\]/]+/u`}},
		{name: "quotes, a comment opener and flags", text: `q = s.split(/['"]/);` + "\n" + `c = /\/*/dgimsuy.test(s);`, regexes: []string{`/['"]/`, `/\/*/dgimsuy`}},
		{name: "a declaration spelled in a literal", text: "const re = /function helper\\(x\\)/;\nrun(re);", regexes: []string{`/function helper\(x\)/`},
			names: []string{"run"}, hidden: []string{"helper"}},
		{name: "template expressions", text: "msg = `a ${s.replace(/[}`/]/g, '')} b ${`c ${d / 2}`} e ${x /* } */ + y}`;\ndone(msg);",
			names: []string{"msg", "done"}, hidden: []string{"replace", "d", "x", "y"}},

		{name: "division after operands", text: "a = b / c;\nd = f(x) / 2;\ne = arr[0] / n;\ng = (a + b) / 2;\nh = 10 / 3;\ni = 's'.length / 2;", divisions: 6},
		{name: "division after a template, a non-null assertion and a postfix increment", text: "a = `t` / 2;\nb = x! / 2;\nc = this.count! / total;\nd = i++ / 2;\ne = j-- / 2;",
			divisions: 5},
		{name: "division after a property named like a keyword", text: "a = mod.default / 2;\nb = obj.return / 3;", divisions: 2},
		{name: "division after a method named like a keyword", text: "y = arr.with(0, a) / (helper = other) / 2;\nz = Symbol.for(k) / 2;", divisions: 3,
			names: []string{"helper", "other"}},
		{name: "a hashbang line", text: "#!/usr/bin/env node\nconst re = /x/;", regexes: []string{"/x/"}, names: []string{"re"}, hidden: []string{"usr", "env", "node"}},
		{name: "a compound division and a division on the next line", text: "total /= n;\nx = a\n  / b;", divisions: 2},
		{name: "code between two divisions", text: "x = a / (helper = other) / 2;\ny = a! / (helper = other) / 2;", divisions: 4, names: []string{"helper", "other"}},

		{name: "a slash after a closing brace", text: "function f() {}\n/x/.test(s);", abandon: true},
		{name: "JSX closing tags", text: "const v = <p>a</p><b>c</b>;", abandon: true},
		{name: "an unterminated literal", text: "s.split(/abc\n);", abandon: true},
		{name: "a backslash outside a literal", text: `const \u0061 = 1;`, abandon: true},
	}
}

// A regular expression literal no longer hides a JavaScript or TypeScript
// file from the lead reader: a same-file function and an imported function
// beside literals resolve, a declaration spelled inside a literal is none,
// and an assignment between two divisions (after an operand, a non-null
// assertion, a postfix increment, or a property or method named like a
// keyword) is still seen.
func TestScriptLeadsReadPastRegularExpressions(t *testing.T) {
	runBindCases(t, regexLiteralCases())
}

// regexLiteralCases lists the test's cases; the lead reader fuzz tests seed
// with their trees.
func regexLiteralCases() []bindCase {
	run := "import { share } from './share.js';\n\nconst prefix = `id-${name.replace(/[^a-z/]/g, '')}`;\n\n" +
		"function clean(s: string) {\n  return s.replace(/[\\u0100-\\uffff]/g, '');\n} // clean end\n\n" +
		"export function run(total: number, people: number) {\n  return clean(String(share(total, people)));\n}\n"
	share := "const digits = /^\\d+$/;\n\nexport function share(total: number, people: number) {\n  return digits.test(String(people)) ? total / people : 0;\n} // share end\n"
	tree := map[string]string{"app/run.ts": run, "app/share.ts": share}
	caller := "export function run(x: number) {\n  return helper(x);\n}\n"
	assigned := func(division string) map[string]string {
		return map[string]string{"app/run.ts": caller + "\nexport function swap(a: number, other: (x: number) => number) {\n  const y = " + division + ";\n  return y;\n}\n" + tsHelper}
	}
	cases := []bindCase{
		{name: "a same-file function beside literals", tree: tree, path: "app/run.ts", line: "return clean(String(share(", symbol: "clean",
			target: "app/run.ts", first: "function clean(", last: "// clean end"},
		{name: "an imported function from a file with a literal", tree: tree, path: "app/run.ts", line: "return clean(String(share(", symbol: "share",
			target: "app/share.ts", first: "export function share(", last: "// share end"},
		{name: "a declaration spelled in a literal", tree: map[string]string{"app/run.ts": "const re = /function helper\\(x: number\\)/;\n\n" + caller + tsHelper},
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", target: "app/run.ts", first: "function helper(x: number)", last: "// helper end"},
	}
	for _, division := range []string{"a / (helper = other) / 2", "a! / (helper = other) / 2", "a++ / (helper = other) / 2", "Math.default / (helper = other) / 2",
		"[a].with(0, a) / (helper = other) / 2"} {
		cases = append(cases, bindCase{name: "an assignment between divisions: " + division, tree: assigned(division),
			path: "app/run.ts", line: "return helper(x)", symbol: "helper", reason: record.LeadAmbiguous})
	}
	return cases
}

// A JavaScript or TSX file's JSX markup is read as opaque text and its
// {...} expressions as code: the calls beside the markup and inside its
// expressions bind as anywhere else, and an assignment inside an expression
// refuses as anywhere else. A generic arrow function and a function type
// stay TypeScript. Markup the scan cannot read leaves the file unread.
func TestScriptLeadsReadPastJSXMarkup(t *testing.T) {
	runBindCases(t, jsxCases())
}

// jsxTokenCase is a TSX text and how the lead scan reads it: names it must
// see as code and names the markup must hide, or nothing, the scan giving
// up.
type jsxTokenCase struct {
	name, text    string
	names, hidden []string
	abandon       bool
}

// A "<" where an operand starts opens JSX markup: tags, attribute names and
// strings, comments in tags and text are opaque; {...} expressions (spread
// attributes, values, children) are code, markup nesting again inside them.
// A "<" after an operand is a comparison, and TypeScript's type parameters
// of a generic arrow function, a function type or a call signature stay
// code. Markup JSX rejects or the reader cannot follow stops the scan.
func TestScriptLeadTokensReadJSXMarkup(t *testing.T) {
	for _, c := range jsxTokenCases() {
		t.Run(c.name, func(t *testing.T) {
			tokens := leadTokens("app/view.tsx", c.text)
			if c.abandon {
				if tokens != nil {
					t.Fatalf("read %d tokens, want none", len(tokens))
				}
				return
			}
			if tokens == nil {
				t.Fatal("the scan gave up")
			}
			checkLeadTokens(t, c.text, tokens)
			names := leadTokenNames(tokens, len(tokens))
			for _, n := range c.names {
				if !slices.Contains(names, n) {
					t.Fatalf("name %q not read in %v", n, names)
				}
			}
			for _, n := range c.hidden {
				if slices.Contains(names, n) {
					t.Fatalf("name %q of markup read as code in %v", n, names)
				}
			}
		})
	}
}

// jsxTokenCases lists the test's cases; the script scan fuzz test seeds
// with their texts.
func jsxTokenCases() []jsxTokenCase {
	return []jsxTokenCase{
		{name: "attributes, text and expressions", text: "const a = <div className=\"x y\" data-id={id / 2}>Hello, {name}! a/b c//d 'q</div>;",
			names: []string{"a", "id", "name"}, hidden: []string{"div", "className", "data", "Hello", "q"}},
		{name: "a fragment, a self-closing element and markup in an expression", text: "return <><img src=\"/a.png\" />{list.map((i) => <li key={i}>{i}</li>)}</>;",
			names: []string{"list", "map", "i"}, hidden: []string{"img", "src", "li", "key"}},
		{name: "member, namespaced and dashed names", text: "x = <Foo.Bar xlink:href=\"#a\"><my-el aria-label='b'>t</my-el></Foo.Bar>;",
			names: []string{"x"}, hidden: []string{"Foo", "Bar", "xlink", "href", "my", "el", "aria", "label"}},
		{name: "comments in a tag and an expression of a comment", text: "y = <a /* c */ href=\"h\" // d\n>{/* only a comment */}</a>;",
			names: []string{"y"}, hidden: []string{"href", "c", "d", "only", "comment"}},
		{name: "a spread attribute", text: "z = <A {...props} b={c} disabled />;", names: []string{"z", "props", "c"}, hidden: []string{"A", "b", "disabled"}},
		{name: "a template and a regular expression in an expression", text: "w = <p title={`t${x}`}>{/</.source + `${y}`}</p>;", names: []string{"w", "source"}, hidden: []string{"p", "title"}},
		{name: "a ternary of elements after markup", text: "v = ok ? <A /> : <B>b</B>;\nu = 1 / 2;", names: []string{"v", "ok", "u"}, hidden: []string{"A", "B", "b"}},
		{name: "a comparison", text: "if (a < b) {\n  c = d > e;\n}", names: []string{"a", "b", "c", "d", "e"}},
		{name: "a generic arrow function", text: "const id = <T,>(x: T) => x;", names: []string{"id", "T", "x"}},
		{name: "a constrained generic arrow function", text: "const f = <T extends object>(x: T) => x;", names: []string{"f", "T", "extends", "object", "x"}},
		{name: "a generic function type", text: "let g: <T>(x: T) => T = id;", names: []string{"g", "T", "x", "id"}},
		{name: "a generic call signature", text: "interface I {\n  <T>(x: T): T;\n}", names: []string{"I", "T", "x"}},

		{name: "a > in text", text: "a = <p>x > y</p>;", abandon: true},
		{name: "a } in text", text: "a = <p>x } y</p>;", abandon: true},
		{name: "an unclosed element", text: "a = <p>x", abandon: true},
		{name: "a closing tag of another element", text: "a = <p>x</q>;", abandon: true},
		{name: "type arguments on an element", text: "a = <Foo<T> />;", abandon: true},
		{name: "an element as an attribute value", text: "a = <A b=<c /> />;", abandon: true},
		{name: "a < before no element", text: "a = < 3;", abandon: true},
		{name: "an unterminated attribute string", text: "a = <A b=\"x />;", abandon: true},
	}
}

// jsxCases lists the test's cases; the lead reader fuzz tests seed with
// their trees.
func jsxCases() []bindCase {
	card := "import { share } from './share.js';\n\nfunction clean(s: string): string {\n  return s.trim();\n} // clean end\n\n" +
		"export function Card({ title, total }: { title: string; total: number }) {\n  const label = clean(title);\n  return (\n" +
		"    <div className=\"card\" data-total={total / 2}>\n      <h1>{label}</h1>\n      <p>Don't panic: a/b, c//d</p>\n" +
		"      <img src=\"/logo.png\" alt=\"\" />\n      <>{share(total, 2)}</>\n      {total > 0 && <Badge.Item n={total} />}\n    </div>\n  );\n}\n\n" +
		"export function footer(s: string) {\n  return clean(s);\n}\n"
	share := "export function share(total: number, people: number) {\n  return total / people;\n} // share end\n"
	tree := map[string]string{"app/card.tsx": card, "app/share.ts": share}
	untyped := strings.NewReplacer(": string", "", ": { title: string; total: number }", "", ": number", "")
	plain, plainShare := untyped.Replace(card), untyped.Replace(share)
	generic := "const id = <T,>(x: T) => x;\nconst pick = <T extends object>(x: T) => x;\nconst ident: <T>(x: T) => T = (x) => x;\n\n" +
		"export function Row() {\n  return <li>{id(1)}</li>;\n}\n\nexport function run(x: number) {\n  return helper(x);\n}\n" + tsHelper
	cases := []bindCase{
		{name: "a same-file function beside markup", tree: tree, path: "app/card.tsx", line: "const label = clean(title)", symbol: "clean",
			target: "app/card.tsx", first: "function clean(", last: "// clean end"},
		{name: "a same-file function after markup", tree: tree, path: "app/card.tsx", line: "return clean(s)", symbol: "clean",
			target: "app/card.tsx", first: "function clean(", last: "// clean end"},
		{name: "an imported function called in markup", tree: tree, path: "app/card.tsx", line: "<>{share(total, 2)}</>", symbol: "share",
			target: "app/share.ts", first: "export function share(", last: "// share end"},
		{name: "a same-file function beside markup in a .jsx file", tree: map[string]string{"app/card.jsx": plain, "app/share.js": plainShare},
			path: "app/card.jsx", line: "return clean(s)", symbol: "clean", target: "app/card.jsx", first: "function clean(", last: "// clean end"},
		{name: "a same-file function beside markup in a .js file", tree: map[string]string{"app/card.js": plain, "app/share.js": plainShare},
			path: "app/card.js", line: "return clean(s)", symbol: "clean", target: "app/card.js", first: "function clean(", last: "// clean end"},
		{name: "generic arrow functions and a function type beside markup", tree: map[string]string{"app/run.tsx": generic},
			path: "app/run.tsx", line: "return helper(x)", symbol: "helper", target: "app/run.tsx", first: "function helper(", last: "// helper end"},

		{name: "an assignment to the name inside markup", tree: map[string]string{"app/card.tsx": strings.Replace(card, "<h1>{label}</h1>", "<h1 onClick={() => { clean = String; }}>{label}</h1>", 1),
			"app/share.ts": share}, path: "app/card.tsx", line: "return clean(s)", symbol: "clean", reason: record.LeadAmbiguous},
		{name: "a > in markup text", tree: map[string]string{"app/card.tsx": strings.Replace(card, "Don't panic", "a -> b", 1), "app/share.ts": share},
			path: "app/card.tsx", line: "return clean(s)", symbol: "clean", reason: record.LeadDefinitionMissing},
		{name: "a closing tag of another element", tree: map[string]string{"app/card.tsx": strings.Replace(card, "<h1>{label}</h1>", "<h1>{label}</h2>", 1), "app/share.ts": share},
			path: "app/card.tsx", line: "return clean(s)", symbol: "clean", reason: record.LeadDefinitionMissing},
		{name: "type arguments of an element", tree: map[string]string{"app/card.tsx": strings.Replace(card, "<Badge.Item n=", "<Badge.Item<number> n=", 1), "app/share.ts": share},
			path: "app/card.tsx", line: "return clean(s)", symbol: "clean", reason: record.LeadDefinitionMissing},
	}
	return cases
}
