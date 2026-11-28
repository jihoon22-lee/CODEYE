package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const ratesHeader = "#ifndef RATES_H\n#define RATES_H\n\nint rate(int n);\n\n#endif\n"
const ratesSource = "#include \"rates.h\"\n\nint rate(int n) {\n  return n * 7;\n} // rate end\n"
const ratesCaller = "#include <stdio.h>\n#include <string.h>\n#include \"rates.h\"\n\nint run(const char *name) {\n  printf(\"%s: %d\\n\", name, 1);\n  return rate((int)strlen(name)) / 2;\n}\n"

// A plain C or C++ call binds by the language's lookup in the anchor and
// the project headers it includes with quotes, beside system includes,
// format strings and division: a definition of the anchor's file (a static
// forward prototype before it names the same function), a static inline
// definition of an included header. A parameter or local of the name, a
// file-scope function pointer of the name, a local whose type decltype or
// __typeof__ writes, a macro of it (in a header of preprocessor lines only
// too), a call in a class or a member function, a using directive, in C++ a
// name of the C library with a system include, and a quoted include found
// only in another directory than the including file's bind it to nothing; a
// conditional prototype
// or include, two C++ overloads or two definitions are ambiguous, as is a
// C++ call another lookup may bind (a function of the name in an inline
// namespace, or for a call with arguments in a namespace the lookup does not
// reach or as a class's friend). A read of the name (qsort's comparator, a
// table) refuses nothing, nor does the name in a template parameter list (a
// parameter, a default argument) or a prototype's numeric default argument.
// In C++ the innermost namespace declaring
// the name decides, an anonymous namespace's names being visible around it.
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
		{name: "a call without arguments beside a function of the name in another namespace", tree: map[string]string{"native/geo.cpp": "int origin() {\n  return 1;\n} // origin end\n\nnamespace geo {\nint origin() {\n  return 0;\n}\n}  // namespace geo\n\nint run() {\n  return origin();\n}\n"},
			path: "native/geo.cpp", line: "return origin();", symbol: "origin", target: "native/geo.cpp", first: "int origin() {", last: "// origin end"},
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
		{name: "a C++ prototype with a numeric default argument", tree: map[string]string{"native/notify.cpp": "void notify(int level = 0);\n\nint run() {\n  notify();\n  return 0;\n}\n\nvoid notify(int level) {\n  (void)level;\n} // notify end\n"},
			path: "native/notify.cpp", line: "  notify();", symbol: "notify", target: "native/notify.cpp", first: "void notify(int level) {", last: "// notify end"},

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
		{name: "a local declared with decltype", tree: map[string]string{"native/run.cpp": "int pick(int n) {\n  return n;\n}\n\nint helper(int n) {\n  return n * 2;\n}\n\nint run(int n) {\n  decltype(&pick) helper = &pick;\n  return helper(n);\n}\n"},
			path: "native/run.cpp", line: "return helper(n);", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a local declared with __typeof__", tree: map[string]string{"native/run.c": "static int pick(int n) {\n  return n;\n}\n\nstatic int helper(int n) {\n  return n * 2;\n}\n\nint run(int n) {\n  __typeof__(pick) *helper = pick;\n  return helper(n);\n}\n"},
			path: "native/run.c", line: "return helper(n);", symbol: "helper", reason: record.LeadDefinitionMissing},
		{name: "a function-like macro in a header of preprocessor lines only", tree: map[string]string{"native/app.c": "#include \"alloc.h\"\n#include \"memdebug.h\"\n\nvoid *run(void) {\n  return xmalloc(16);\n}\n",
			"native/alloc.h": "#pragma once\n#include <stddef.h>\n\nvoid *xmalloc(size_t n);\n", "native/memdebug.h": "#pragma once\n#ifdef MEMDEBUG\n#define xmalloc(n) xmalloc_debug(n, __FILE__, __LINE__)\n#endif\n",
			"native/alloc.c": "#include \"alloc.h\"\n\nvoid *xmalloc(size_t n) {\n  return 0;\n}\n"},
			path: "native/app.c", line: "return xmalloc(16);", symbol: "xmalloc", reason: record.LeadDefinitionMissing},
		{name: "a quoted include found only in another directory", tree: map[string]string{"native/app.c": "#include \"compat.h\"\n\nint run(int n) {\n  return shim(n);\n}\n",
			"plugins/legacy/compat.h": "#pragma once\n\nstatic inline int shim(int n) {\n  return n;\n}\n"},
			path: "native/app.c", line: "return shim(n);", symbol: "shim", reason: record.LeadDefinitionMissing},

		{name: "a C++ overload declared beside the definition of another", tree: map[string]string{"native/area.cpp": "#include \"area.h\"\n\ndouble area(double r) {\n  return 3.14 * r * r;\n}\n\nint run() {\n  return area(2, 3);\n}\n",
			"native/area.h": "#pragma once\n\nint area(int w, int h);\n", "native/rect.cpp": "int area(int w, int h) {\n  return w * h;\n}\n"},
			path: "native/area.cpp", line: "return area(2, 3);", symbol: "area", reason: record.LeadAmbiguous},
		{name: "a template overload with a default template argument", tree: map[string]string{"native/clamp.cpp": "template <typename T = int>\nT clamp(T v) {\n  return v;\n}\n\nint clamp(int v, int lo) {\n  return v < lo ? lo : v;\n}\n\nint run() {\n  return clamp(3);\n}\n"},
			path: "native/clamp.cpp", line: "return clamp(3);", symbol: "clamp", reason: record.LeadAmbiguous},
		{name: "C++ overloads", tree: map[string]string{"native/area.cpp": "int area(int w, int h) {\n  return w * h;\n}\n\ndouble area(double r) {\n  return 3.14 * r * r;\n}\n\nint run() {\n  return area(2, 3);\n}\n"},
			path: "native/area.cpp", line: "return area(2, 3);", symbol: "area", reason: record.LeadAmbiguous},
		{name: "a function of the name in the namespace of an argument's type", tree: map[string]string{"native/geo.cpp": "namespace geo {\nstruct Point {\n  int x, y;\n};\n\nint norm(Point p) {\n  return p.x * p.x + p.y * p.y;\n}\n}  // namespace geo\n\nint norm(int n) {\n  return n < 0 ? -n : n;\n}\n\nint run(geo::Point p) {\n  return norm(p);\n}\n"},
			path: "native/geo.cpp", line: "return norm(p);", symbol: "norm", reason: record.LeadAmbiguous},
		{name: "a friend function of the name in an argument's class", tree: map[string]string{"native/shop.cpp": "namespace shop {\nstruct Order {\n  int qty;\n  friend int price(const Order &o) {\n    return o.qty * 7;\n  }\n};\n}  // namespace shop\n\nint price(int n) {\n  return n;\n}\n\nint run(shop::Order o) {\n  return price(o);\n}\n"},
			path: "native/shop.cpp", line: "return price(o);", symbol: "price", reason: record.LeadAmbiguous},
		{name: "a declaration of the name in an inline namespace", tree: map[string]string{"native/rates.cpp": "inline namespace v2 {\nint rate(double n);\n}  // namespace v2\n\nint rate(int n) {\n  return n * 7;\n}\n\nint run() {\n  return rate(3);\n}\n"},
			path: "native/rates.cpp", line: "return rate(3);", symbol: "rate", reason: record.LeadAmbiguous},
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
// sources (a static function of the name in another file is not it, nor
// does a header of preprocessor lines alone stop the search, nor do sources
// on a secondary path or in a directory the review does not read that do not
// define the name, nor does a withheld source without the name): the
// definition is sent first and the prototype after it, marked partial.
// Without exactly one such definition (one on a secondary path or in a
// directory the review does not read counts, and is never sent), or beside
// a withheld source holding the name (by the screen or for its content),
// the prototype alone is sent.
func TestNativeHeaderPrototypeReachesItsDefinition(t *testing.T) {
	for _, c := range prototypeCases() {
		t.Run(c.name, func(t *testing.T) {
			s := leadStage(t, treeJob(t, c.tree()))
			if len(c.withheld) > 0 {
				s.screen = secretScreen{state: screenApplied, withheld: map[string]bool{}}
				for _, p := range c.withheld {
					s.screen.withheld[p] = true
				}
			}
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

// prototypeCase is a project whose anchor native/app.c (ratesCaller unless
// caller says) calls rate through the prototype of native/rates.h: the
// files beside them, those withheld, and whether the one definition is
// found.
type prototypeCase struct {
	name     string
	extra    map[string]string
	def      bool
	caller   string
	withheld []string
}

func (c prototypeCase) tree() map[string]string {
	tree := map[string]string{"native/app.c": ratesCaller, "native/rates.h": ratesHeader}
	if c.caller != "" {
		tree["native/app.c"] = c.caller
	}
	for k, v := range c.extra {
		tree[k] = v
	}
	return tree
}

// fakeRates is a second definition of rate a test build links instead.
const fakeRates = "int rate(int n) {\n  return 0;\n}\n"

// prototypeCases lists the cases of the test above; the lead reader fuzz
// tests seed with their trees.
func prototypeCases() []prototypeCase {
	legacy := "static int rate(int n) {\n  return n * 9;\n}\n"
	return []prototypeCase{
		{name: "one definition", extra: map[string]string{"native/rates.c": ratesSource}, def: true},
		{name: "one definition beside a static function of the name", extra: map[string]string{"native/rates.c": ratesSource, "native/legacy.c": legacy}, def: true},
		{name: "one definition beside a header of definitions only", extra: map[string]string{"native/rates.c": ratesSource, "native/config.h": "#pragma once\n#define RATE_SCALE 7\n"},
			def: true, caller: "#include \"config.h\"\n" + ratesCaller},
		{name: "two definitions", extra: map[string]string{"native/rates.c": ratesSource, "native/stub.c": fakeRates}},
		{name: "only a static function of the name", extra: map[string]string{"native/legacy.c": legacy}},
		{name: "one readable definition beside a withheld source", extra: map[string]string{"native/rates.c": ratesSource, "test/fake_rates.c": fakeRates},
			withheld: []string{"native/rates.c"}},
		{name: "one definition beside sources on a secondary path that do not define it", extra: map[string]string{"native/rates.c": ratesSource,
			"examples/demo.c": "#include \"../native/rates.h\"\n\nint main(void) {\n  return rate(3);\n}\n", "examples/hello.c": helloSource}, def: true},
		{name: "a second definition on a secondary path", extra: map[string]string{"native/rates.c": ratesSource, "examples/rates.c": fakeRates}},
		{name: "the one definition on a secondary path", extra: map[string]string{"examples/rates.c": ratesSource}},
		{name: "the one definition in a directory the review does not read", extra: map[string]string{"vendor/rates.c": ratesSource}},
		{name: "one definition beside a source in a directory the review does not read that does not define it", extra: map[string]string{"native/rates.c": ratesSource,
			"vendor/hello.c": helloSource}, def: true},
		{name: "a second definition in a directory the review does not read", extra: map[string]string{"native/rates.c": ratesSource, "vendor/rates.c": fakeRates}},
		{name: "one definition beside a withheld source without the name", extra: map[string]string{"native/rates.c": ratesSource, "examples/hello.c": helloSource},
			withheld: []string{"examples/hello.c"}, def: true},
		{name: "one definition beside a withheld source holding the name", extra: map[string]string{"native/rates.c": ratesSource,
			"examples/demo.c": "#include \"../native/rates.h\"\n\nint main(void) {\n  return rate(3);\n}\n"}, withheld: []string{"examples/demo.c"}},
		{name: "one definition beside a source holding a credential without the name", extra: map[string]string{"native/rates.c": ratesSource,
			"examples/keys.c": "static const char *password = \"" + "codeye" + "fixture" + strings.Repeat("0", 8) + "\";\n"}, def: true},
		{name: "one definition beside a source holding a credential and the name", extra: map[string]string{"native/rates.c": ratesSource,
			"examples/keys.c": "static const char *password = \"" + "codeye" + "fixture" + strings.Repeat("0", 8) + "\";\nint burst(void) {\n  return rate(2);\n}\n"}},
	}
}

// helloSource is a C source no lead of rate reaches.
const helloSource = "int hello(void) {\n  return 0;\n}\n"

// A header prototype reaches its one definition in a file the run may send
// in a pull request and a path-scoped run as in a full one: the definition
// is sent before the prototype, and the sources the run may not send (an
// unchanged file, one outside the scope) are read for the check and never
// sent.
func TestNativeHeaderPrototypeReachesItsDefinitionInBoundedRuns(t *testing.T) {
	tree := map[string]string{
		"app/main.c":  "#include \"net.h\"\n\nint main(void) {\n  return net_send(1);\n}\n",
		"app/net.h":   "int net_send(int n);\n",
		"app/net.c":   "#include \"net.h\"\n\nint net_send(int n) {\n  return n;\n}\n",
		"app/other.c": "#include \"net.h\"\n\nint other(void) {\n  return net_send(2);\n}\n",
		"lib/util.c":  "int util(void) {\n  return net_send(3);\n}\n",
	}
	for _, c := range []struct {
		name      string
		configure func(j *analysis.Job)
		unsent    []string
	}{
		{"a full run", func(*analysis.Job) {}, nil},
		{"a pull request", func(j *analysis.Job) {
			j.PullRequest = &domain.PullRequestJob{Changed: []string{"app/main.c", "app/net.h", "app/net.c"}}
		}, []string{"app/other.c", "lib/util.c"}},
		{"a path scope", func(j *analysis.Job) { j.Scope = domain.Scope{Kind: "paths", Paths: []string{"app"}} }, []string{"lib/util.c"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, tree)
			c.configure(&j)
			s := leadStage(t, j)
			window, anchor := leadWindow(t, s, "app/main.c", "return net_send(1);", 0)
			r := s.newLeadResolver([]record.Excerpt{window})
			read := map[string]bool{}
			r.worldRead = func(e source.Entry) (string, bool, error) {
				read[e.Path] = true
				return s.worldBytes(e)
			}
			l, err := r.resolve(record.Lead{ID: "lead_1", Need: record.LeadNeedDefinition, Symbol: "net_send", EvidenceID: anchor.ID,
				Suspicion: "호출한 함수가 오류를 돌려주는지 확인해야 한다."}, anchor)
			if err != nil {
				t.Fatal(err)
			}
			want := []record.LeadTarget{{Path: "app/net.c", Start: 3, End: 5}, {Path: "app/net.h", Start: 1, End: 1, Partial: true}}
			if !l.Resolved() || len(l.Targets) != len(want) {
				t.Fatalf("lead %+v", l)
			}
			for k, w := range want {
				if g := l.Targets[k]; g.Path != w.Path || g.Start != w.Start || g.End != w.End || g.Partial != w.Partial {
					t.Fatalf("target %d %+v, want %+v", k, g, w)
				}
			}
			for _, p := range c.unsent {
				if r.blocked(p) == "" || !read[p] {
					t.Fatalf("%s: blocked %q, read %v; want a source the run may not send, read for the check", p, r.blocked(p), read[p])
				}
			}
		})
	}
}

// The one-definition check reads the sources the lead may not send too
// (outside the scope, outside a pull request's changed files): a second
// definition there still counts, and the header prototype alone is sent;
// the one definition there is never sent, and with the prototype already in
// the request the lead names that file's reason.
func TestNativeExternalDefinitionReadsSourcesItMayNotSend(t *testing.T) {
	for name, configure := range map[string]func(j *analysis.Job){
		"outside the scope": func(j *analysis.Job) {
			j.Scope = domain.Scope{Kind: "paths", Paths: []string{"native/app.c", "native/rates.h", "test"}}
		},
		"outside the change": func(j *analysis.Job) {
			j.PullRequest = &domain.PullRequestJob{Changed: []string{"native/app.c", "native/rates.h", "test/fake_rates.c"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, prototypeCase{extra: map[string]string{"native/rates.c": ratesSource, "test/fake_rates.c": fakeRates}}.tree())
			configure(&j)
			s := leadStage(t, j)
			window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
			l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor)
			if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "native/rates.h" || !l.Targets[0].Partial {
				t.Fatalf("lead %+v", l)
			}
		})
	}
	for name, c := range map[string]struct {
		configure func(j *analysis.Job)
		reason    string
	}{
		"outside the scope": {func(j *analysis.Job) {
			j.Scope = domain.Scope{Kind: "paths", Paths: []string{"native/app.c", "native/rates.h"}}
		}, record.LeadOutsideScope},
		"outside the change": {func(j *analysis.Job) {
			j.PullRequest = &domain.PullRequestJob{Changed: []string{"native/app.c", "native/rates.h"}}
		}, record.LeadOutsideChange},
	} {
		t.Run("the one definition "+name, func(t *testing.T) {
			j := treeJob(t, prototypeCase{extra: map[string]string{"native/rates.c": ratesSource}}.tree())
			c.configure(&j)
			s := leadStage(t, j)
			window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
			l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor, sentLines(t, s, "native/rates.h", 4, 4))
			if l.Resolved() || l.Reason != c.reason || l.Targets != nil {
				t.Fatalf("lead %+v, want %s", l, c.reason)
			}
		})
	}
}

// A definition on a secondary path or in a directory the review does not
// read is never sent, and the lead names that file's reason when nothing
// else is sent: the header prototype already in the request, or a prototype
// in the anchor itself.
func TestNativeDefinitionOnASecondaryPathIsNeverSent(t *testing.T) {
	inAnchor := "int rate(int n);\n\nint run(int n) {\n  return rate(n) / 2;\n}\n"
	for _, c := range []struct {
		name   string
		tree   map[string]string
		sent   bool
		reason string
	}{
		{"the header prototype in the request", prototypeCase{extra: map[string]string{"examples/rates.c": ratesSource}}.tree(), true, record.LeadSecondary},
		{"a prototype in the anchor", map[string]string{"native/app.c": inAnchor, "examples/rates.c": ratesSource}, false, record.LeadSecondary},
		{"a definition in a directory the review does not read", prototypeCase{extra: map[string]string{"vendor/rates.c": ratesSource}}.tree(), true, record.LeadOutsideScope},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := leadStage(t, treeJob(t, c.tree))
			window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
			var sent []record.Excerpt
			if c.sent {
				sent = append(sent, sentLines(t, s, "native/rates.h", 4, 4))
			}
			l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor, sent...)
			if l.Resolved() || l.Reason != c.reason || l.Targets != nil {
				t.Fatalf("lead %+v, want %s", l, c.reason)
			}
		})
	}
}

// The one-definition check reads the closed world, not the lead's scan:
// sources past the scan's file limit that do not hold the name cost no
// tokenizing, but past maxWorldFiles sources holding it not every one was
// tokenized, and the header prototype alone is sent beside the one
// definition elsewhere.
func TestNativeExternalDefinitionStopsAtTheWorldBound(t *testing.T) {
	tree := prototypeCase{extra: map[string]string{"native/rates.c": ratesSource}}.tree()
	for k := range maxScanFiles + 10 {
		tree[fmt.Sprintf("examples/demo%03d.c", k)] = fmt.Sprintf("int demo%d(void) {\n  return %d;\n}\n", k, k)
	}
	s := leadStage(t, treeJob(t, tree))
	window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
	if l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor); !l.Resolved() || len(l.Targets) != 2 || l.Targets[0].Path != "native/rates.c" {
		t.Fatalf("lead %+v, want the definition past the scan's file limit", l)
	}
	tree = prototypeCase{extra: map[string]string{"native/rates.c": ratesSource}}.tree()
	for k := range maxWorldFiles {
		tree[fmt.Sprintf("examples/demo%03d.c", k)] = fmt.Sprintf("#include \"../native/rates.h\"\n\nint demo%d(void) {\n  return rate(%d);\n}\n", k, k)
	}
	s = leadStage(t, treeJob(t, tree))
	window, anchor = leadWindow(t, s, "native/app.c", "return rate(", 0)
	l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor)
	if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "native/rates.h" || !l.Targets[0].Partial {
		t.Fatalf("lead %+v", l)
	}
}

// A definition the investigate request already sent is in the request,
// whether one excerpt holds it or several do (or one holds all of it but its
// closing brace): its header prototype is not sent alone in its place.
func TestNativeDefinitionSentInPartsIsInTheRequest(t *testing.T) {
	source := "#include \"rates.h\"\n\nint rate(int n) {\n  return n * 7;\n}\n"
	tree := map[string]string{"native/app.c": ratesCaller, "native/rates.h": ratesHeader, "native/rates.c": source}
	for name, ranges := range map[string][][2]int{
		"one excerpt":               {{3, 5}},
		"all but the closing brace": {{3, 4}},
		"two excerpts":              {{3, 4}, {5, 5}},
	} {
		t.Run(name, func(t *testing.T) {
			s := leadStage(t, treeJob(t, tree))
			window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
			var sent []record.Excerpt
			var ids []string
			for _, r := range ranges {
				x := sentLines(t, s, "native/rates.c", r[0], r[1])
				sent = append(sent, x)
				ids = append(ids, x.ID)
			}
			slices.Sort(ids)
			l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor, sent...)
			if l.Resolved() || l.Reason != record.LeadInRequest || l.Targets != nil || !slices.Equal(l.InRequest, ids) {
				t.Fatalf("lead %+v, want in the request as %v", l, ids)
			}
		})
	}
}

// The calls of one lead share its reading of the project: an anchor calling
// the name two hundred times takes about as long as one calling it once,
// well under two seconds with twenty sources of 50 KB holding the name. The
// race detector slows every reading alike, so only the ratio holds there.
func TestNativeLeadReadsTheSourcesOnce(t *testing.T) {
	tree := map[string]string{"native/net.h": "#pragma once\n\nint net_send(int n);\n",
		"native/net.c": "#include \"net.h\"\n\nint net_send(int n) {\n  return n;\n}\n"}
	for f := range 20 {
		var b strings.Builder
		b.WriteString("#include \"net.h\"\n\n")
		for k := 0; b.Len() < 50<<10; k++ {
			fmt.Fprintf(&b, "/* %s */\nint relay_%d_%d(int n) {\n  return net_send(n + %d) * 2;\n}\n\n", strings.Repeat("relays the message. ", 20), f, k, k)
		}
		tree[fmt.Sprintf("native/relay%02d.c", f)] = b.String()
	}
	var lines []string
	for k := 0; k < 200; k += 3 {
		calls := []string{}
		for c := k; c < min(k+3, 200); c++ {
			calls = append(calls, fmt.Sprintf("net_send(%d)", c))
		}
		lines = append(lines, "  s += "+strings.Join(calls, " + ")+";")
	}
	tree["native/app.c"] = "#include \"net.h\"\n\nint one(void) {\n  return net_send(1);\n}\n\nint run(void) {\n  int s = 0;\n" + strings.Join(lines, "\n") + "\n  return s;\n}\n"
	s := leadStage(t, treeJob(t, tree))
	resolve := func(first, last string) time.Duration {
		window, _ := leadWindow(t, s, "native/app.c", first, 0)
		whole := sentLines(t, s, "native/app.c", 1, strings.Count(tree["native/app.c"], "\n"))
		anchor := record.Narrow(whole, s.j.Snapshot.Digest, window.Start, evidenceLine(t, tree["native/app.c"], last))
		began := time.Now()
		l := resolveOne(t, s, record.LeadNeedDefinition, "net_send", whole, anchor)
		took := time.Since(began)
		if !l.Resolved() || len(l.Targets) != 2 || l.Targets[0].Path != "native/net.c" || l.Targets[1].Path != "native/net.h" {
			t.Fatalf("lead %+v", l)
		}
		return took
	}
	once := resolve("return net_send(1);", "return net_send(1);")
	many := resolve("s += net_send(0)", "net_send(199)")
	if many > 4*once+250*time.Millisecond || !raceDetector && many > 2*time.Second {
		t.Fatalf("200 calls took %v, one call %v", many, once)
	}
}
