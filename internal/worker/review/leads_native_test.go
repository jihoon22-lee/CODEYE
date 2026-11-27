package review

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const ratesHeader = "#ifndef RATES_H\n#define RATES_H\n\nint rate(int n);\n\n#endif\n"
const ratesSource = "#include \"rates.h\"\n\nint rate(int n) {\n  return n * 7;\n} // rate end\n"
const ratesCaller = "#include <stdio.h>\n#include <string.h>\n#include \"rates.h\"\n\nint run(const char *name) {\n  printf(\"%s: %d\\n\", name, 1);\n  return rate((int)strlen(name)) / 2;\n}\n"

// A plain C or C++ call binds by the language's lookup in the anchor and
// the project headers it includes with quotes, beside system includes,
// format strings and division: a definition of the anchor's file (a static
// forward prototype before it names the same function), a static inline
// definition of an included header. A parameter or local of the name, a
// file-scope function pointer of the name, a macro of it, a call in a class
// or a member function, a using directive, and in C++ a name of the C
// library with a system include bind it to nothing; a conditional prototype
// or include, two C++ overloads or two definitions are ambiguous. A read of
// the name (qsort's comparator, a table) refuses nothing, nor does the name
// in a template parameter list (a parameter, a default argument). In C++ the
// innermost namespace declaring the name decides, an anonymous namespace's
// names being visible around it.
func TestNativeDefinitionLeadsFollowLookup(t *testing.T) {
	runBindCases(t, nativeLookupCases())
}

// nativeLookupCases lists the test's cases; the lead reader fuzz tests
// seed with their trees.
func nativeLookupCases() []bindCase {
	scale := "#include <stdio.h>\n#include <string.h>\n\nstatic int scale(int n);\n\nint run(const char *name) {\n  printf(\"%s\\n\", name);\n  return scale((int)strlen(name)) / 2;\n}\n\nstatic int scale(int n) {\n  return n * 3;\n} // scale end\n"
	return []bindCase{
		{name: "same-file definition after a static prototype", tree: map[string]string{"native/app.c": scale},
			path: "native/app.c", line: "return scale(", symbol: "scale", target: "native/app.c", first: "static int scale(int n) {", last: "// scale end"},
		{name: "a default template argument of the name", tree: map[string]string{"native/shape.cpp": "int name(int n) {\n  return n * 3;\n} // name end\n\ntemplate <class T, int N = name> void f();\n\nint run(int n) {\n  return name(n);\n}\n"},
			path: "native/shape.cpp", line: "return name(n);", symbol: "name", target: "native/shape.cpp", first: "int name(int n) {", last: "// name end"},
		{name: "a default template argument calling the name", tree: map[string]string{"native/shape.cpp": "constexpr int name() {\n  return 3;\n} // name end\n\ntemplate <class T, int N = name()> void f();\n\nint run(int n) {\n  return name() * n;\n}\n"},
			path: "native/shape.cpp", line: "return name() * n;", symbol: "name", target: "native/shape.cpp", first: "constexpr int name() {", last: "// name end"},
		{name: "a template parameter of the name", tree: map[string]string{"native/shape.cpp": "int name(int n) {\n  return n * 3;\n} // name end\n\ntemplate <int (*name)(int)> int apply(int n);\n\nint run(int n) {\n  return name(n);\n}\n"},
			path: "native/shape.cpp", line: "return name(n);", symbol: "name", target: "native/shape.cpp", first: "int name(int n) {", last: "// name end"},
		{name: "static inline definition of an included header beside system includes", tree: map[string]string{
			"native/app.c":   "#include <stdlib.h>\n#include \"rates.h\"\n\nint run(int n) {\n  if (n < 0) {\n    exit(1);\n  }\n  return rate(n) / 2;\n}\n",
			"native/rates.h": "#pragma once\n#include <stddef.h>\n\nstatic inline int rate(int n) {\n  return n * 7;\n} // rate end\n"},
			path: "native/app.c", line: "return rate(n) / 2;", symbol: "rate", target: "native/rates.h", first: "static inline int rate", last: "// rate end"},
		{name: "reads of the name refuse nothing", tree: map[string]string{"native/sort.c": "#include <stdlib.h>\n\nstatic int cmp(const void *a, const void *b) {\n  return *(const int *)a - *(const int *)b;\n} // cmp end\n\n" +
			"static int (*const table[])(const void *, const void *) = { cmp };\n\nint run(int *v, size_t n, int x, int y) {\n  qsort(v, n, sizeof v[0], cmp);\n  return cmp(&x, &y);\n}\n"},
			path: "native/sort.c", line: "return cmp(&x, &y);", symbol: "cmp", target: "native/sort.c", first: "static int cmp(", last: "// cmp end"},
		{name: "a C++ prototype and its definition with other parameter names", tree: map[string]string{"native/area.cpp": "#include <string>\n\nint area(int, int);\n\nint run() {\n  return area(2, 3);\n}\n\nint area(int w, int h) {\n  return w * h;\n} // area end\n"},
			path: "native/area.cpp", line: "return area(2, 3);", symbol: "area", target: "native/area.cpp", first: "int area(int w, int h) {", last: "// area end"},
		{name: "the innermost namespace declaring the name", tree: map[string]string{"native/shop.cpp": "int price(int n) {\n  return n;\n}\n\nnamespace shop {\nint price(int n) {\n  return n * 7;\n} // shop price end\n\nint total(int n) {\n  return price(n) + 1;\n}\n}  // namespace shop\n"},
			path: "native/shop.cpp", line: "return price(n) + 1;", symbol: "price", target: "native/shop.cpp", first: "int price(int n) {\n  return n * 7;", last: "// shop price end"},
		{name: "an anonymous namespace's function from the file scope", tree: map[string]string{"native/run.cpp": "namespace {\nint helper(int n) {\n  return n + 1;\n} // helper end\n}  // namespace\n\nint run(int n) {\n  return helper(n);\n}\n"},
			path: "native/run.cpp", line: "return helper(n);", symbol: "helper", target: "native/run.cpp", first: "int helper(int n) {", last: "// helper end"},
		{name: "an inline function of a header anchor", tree: map[string]string{"native/util.h": "#pragma once\n\ninline int twice(int n) {\n  return n * 2;\n} // twice end\n\ninline int quad(int n) {\n  return twice(twice(n));\n}\n"},
			path: "native/util.h", line: "return twice(twice(n));", symbol: "twice", target: "native/util.h", first: "inline int twice(", last: "// twice end"},
		{name: "a project function named like a C library function in C", tree: map[string]string{"native/abs.c": "#include <stdio.h>\n\nstatic int abs(int n) {\n  return n < 0 ? -n : n;\n} // abs end\n\nint run(int n) {\n  return abs(n);\n}\n"},
			path: "native/abs.c", line: "return abs(n);", symbol: "abs", target: "native/abs.c", first: "static int abs(", last: "// abs end"},

		{name: "parameter of the name", tree: map[string]string{"native/app.c": "#include \"rates.h\"\n\nint run(int (*rate)(int), int n) {\n  return rate(n);\n}\n", "native/rates.h": ratesHeader, "native/rates.c": ratesSource},
			path: "native/app.c", line: "return rate(n);", symbol: "rate", reason: record.LeadDefinitionMissing},
		{name: "local function pointer of the name", tree: map[string]string{"native/app.c": "#include \"rates.h\"\n\nint run(int n) {\n  int (*rate)(int) = 0;\n  return rate(n);\n}\n", "native/rates.h": ratesHeader, "native/rates.c": ratesSource},
			path: "native/app.c", line: "return rate(n);", symbol: "rate", reason: record.LeadDefinitionMissing},
		{name: "file-scope function pointer of the name", tree: map[string]string{"native/app.c": "static int seven(int n) {\n  return n * 7;\n}\n\nstatic int (*rate)(int) = seven;\n\nint run(int n) {\n  return rate(n);\n}\n"},
			path: "native/app.c", line: "return rate(n);", symbol: "rate", reason: record.LeadDefinitionMissing},
		{name: "function-like macro of the name after its prototype in the header", tree: map[string]string{"native/app.c": ratesCaller,
			"native/rates.h": "#pragma once\n\nint rate(int n);\n#define rate(n) checked_rate(n)\n", "native/rates.c": ratesSource},
			path: "native/app.c", line: "return rate(", symbol: "rate", reason: record.LeadDefinitionMissing},
		{name: "a function pointer of the name in the call's namespace", tree: map[string]string{"native/shop.cpp": "int rate(int n) {\n  return n;\n}\n\nnamespace shop {\nint (*rate)(int) = nullptr;\n\nint total(int n) {\n  return rate(n);\n}\n}  // namespace shop\n"},
			path: "native/shop.cpp", line: "return rate(n);", symbol: "rate", reason: record.LeadDefinitionMissing},
		{name: "object-like macro of the name in the anchor", tree: map[string]string{"native/app.c": "#define rate fast_rate\n\nint fast_rate(int n);\n\nint rate(int n) {\n  return n;\n}\n\nint run(int n) {\n  return rate(n);\n}\n"},
			path: "native/app.c", line: "return rate(n);", symbol: "rate", reason: record.LeadDefinitionMissing},
		{name: "a call in an in-class member function", tree: map[string]string{"native/shape.cpp": "int area(int w, int h) {\n  return w * h;\n}\n\nclass Shape {\n public:\n  int run() { return area(1, 2); }\n};\n"},
			path: "native/shape.cpp", line: "int run() { return area(1, 2); }", symbol: "area", reason: record.LeadDefinitionMissing},
		{name: "a call in an out-of-line member function", tree: map[string]string{"native/shape.cpp": "int area(int w, int h) {\n  return w * h;\n}\n\nint Shape::run() {\n  return area(1, 2);\n}\n"},
			path: "native/shape.cpp", line: "return area(1, 2);", symbol: "area", reason: record.LeadDefinitionMissing},
		{name: "a using directive", tree: map[string]string{"native/run.cpp": "#include <algorithm>\nusing namespace std;\n\nint count(int n) {\n  return n;\n}\n\nint run(int n) {\n  return count(n);\n}\n"},
			path: "native/run.cpp", line: "return count(n);", symbol: "count", reason: record.LeadDefinitionMissing},
		{name: "a C library name in C++ with a system include", tree: map[string]string{"native/abs.cpp": "#include <cstdlib>\n\nstatic int abs(int n) {\n  return n < 0 ? -n : n;\n}\n\nint run(int n) {\n  return abs(n);\n}\n"},
			path: "native/abs.cpp", line: "return abs(n);", symbol: "abs", reason: record.LeadDefinitionMissing},

		{name: "a C++ overload declared beside the definition of another", tree: map[string]string{"native/area.cpp": "#include \"area.h\"\n\ndouble area(double r) {\n  return 3.14 * r * r;\n}\n\nint run() {\n  return area(2, 3);\n}\n",
			"native/area.h": "#pragma once\n\nint area(int w, int h);\n", "native/rect.cpp": "int area(int w, int h) {\n  return w * h;\n}\n"},
			path: "native/area.cpp", line: "return area(2, 3);", symbol: "area", reason: record.LeadAmbiguous},
		{name: "a template overload with a default template argument", tree: map[string]string{"native/clamp.cpp": "template <typename T = int>\nT clamp(T v) {\n  return v;\n}\n\nint clamp(int v, int lo) {\n  return v < lo ? lo : v;\n}\n\nint run() {\n  return clamp(3);\n}\n"},
			path: "native/clamp.cpp", line: "return clamp(3);", symbol: "clamp", reason: record.LeadAmbiguous},
		{name: "C++ overloads", tree: map[string]string{"native/area.cpp": "int area(int w, int h) {\n  return w * h;\n}\n\ndouble area(double r) {\n  return 3.14 * r * r;\n}\n\nint run() {\n  return area(2, 3);\n}\n"},
			path: "native/area.cpp", line: "return area(2, 3);", symbol: "area", reason: record.LeadAmbiguous},
		{name: "a conditional prototype", tree: map[string]string{"native/app.c": ratesCaller, "native/rates.h": "#pragma once\n\n#ifdef FAST\nint rate(int n);\n#endif\n", "native/rates.c": ratesSource},
			path: "native/app.c", line: "return rate(", symbol: "rate", reason: record.LeadAmbiguous},
		{name: "a header behind a conditional include", tree: map[string]string{"native/app.c": "#include <stdio.h>\n#ifdef FAST\n#include \"rates.h\"\n#endif\n\nint run(int n) {\n  return rate(n);\n}\n",
			"native/rates.h": ratesHeader, "native/rates.c": ratesSource},
			path: "native/app.c", line: "return rate(n);", symbol: "rate", reason: record.LeadAmbiguous},
		{name: "definitions under both branches of a condition", tree: map[string]string{"native/app.c": "#ifdef FAST\nstatic int rate(int n) {\n  return n * 8;\n}\n#else\nstatic int rate(int n) {\n  return n * 7;\n}\n#endif\n\nint run(int n) {\n  return rate(n);\n}\n"},
			path: "native/app.c", line: "return rate(n);", symbol: "rate", reason: record.LeadAmbiguous},
	}
}

// A header prototype the anchor includes, beside system includes, binds the
// call to the one non-static, unconditional definition among the project's
// sources (a static function of the name in another file is not it): the
// definition is sent first and the prototype after it, marked partial.
// Without exactly one such definition the prototype alone is sent.
func TestNativeHeaderPrototypeReachesItsDefinition(t *testing.T) {
	for _, c := range prototypeCases() {
		t.Run(c.name, func(t *testing.T) {
			s := leadStage(t, treeJob(t, c.tree()))
			window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
			l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor)
			proto := record.LeadTarget{Path: "native/rates.h", Start: 4, End: 4, Partial: true}
			want := []record.LeadTarget{proto}
			if c.def {
				want = []record.LeadTarget{{Path: "native/rates.c", Start: 3, End: 5}, proto}
			}
			if !l.Resolved() || len(l.Targets) != len(want) {
				t.Fatalf("lead %+v", l)
			}
			for k, w := range want {
				if g := l.Targets[k]; g.Path != w.Path || g.Start != w.Start || g.End != w.End || g.Partial != w.Partial {
					t.Fatalf("target %d %+v, want %+v", k, g, w)
				}
			}
		})
	}
}

// prototypeCase is a project whose anchor native/app.c calls rate through
// the prototype of native/rates.h: the files beside them and whether the one
// definition is found.
type prototypeCase struct {
	name  string
	extra map[string]string
	def   bool
}

func (c prototypeCase) tree() map[string]string {
	tree := map[string]string{"native/app.c": ratesCaller, "native/rates.h": ratesHeader}
	for k, v := range c.extra {
		tree[k] = v
	}
	return tree
}

// prototypeCases lists the cases of the test above; the lead reader fuzz
// tests seed with their trees.
func prototypeCases() []prototypeCase {
	legacy := "static int rate(int n) {\n  return n * 9;\n}\n"
	return []prototypeCase{
		{"one definition", map[string]string{"native/rates.c": ratesSource}, true},
		{"one definition beside a static function of the name", map[string]string{"native/rates.c": ratesSource, "native/legacy.c": legacy}, true},
		{"two definitions", map[string]string{"native/rates.c": ratesSource, "native/stub.c": "int rate(int n) {\n  return 0;\n}\n"}, false},
		{"only a static function of the name", map[string]string{"native/legacy.c": legacy}, false},
	}
}
