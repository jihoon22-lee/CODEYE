package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func cutLines(p, text string, maxBytes int) []int {
	cuts := declarationCuts(p, strings.Split(text, "\n"), maxBytes)
	var out []int
	for n := range cuts {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func TestDeclarationCutsGo(t *testing.T) {
	src := "package x\n\nimport \"fmt\"\n\n// A is documented.\nfunc A() {\n\tfmt.Println(\"}\")\n}\n\nvar b = `{`\n\ntype C struct{}\n"
	got := cutLines("x.go", src, 1<<20)
	// Declarations start at the doc comment (5), the var (10) and the type
	// (12); a cut follows each declaration's last line.
	for _, n := range []int{1, 3, 5, 9, 10, 11, 12} {
		if !slices.Contains(got, n) {
			t.Fatalf("cut %d missing: %v", n, got)
		}
	}
	if slices.Contains(got, 7) || slices.Contains(got, 6) {
		t.Fatalf("cut inside a function or between doc and function: %v", got)
	}
	if h := headerEnd("x.go", strings.Split(src, "\n")); h != 3 {
		t.Fatalf("header %d", h)
	}
}

func TestDeclarationCutsJavaMembers(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p;\n\nimport java.util.List;\n\npublic class A {\n")
	for i := range 6 {
		fmt.Fprintf(&b, "    /** m%d */\n    int m%d() {\n        String s = \"}\";\n        return %d;\n    }\n\n", i, i, i)
	}
	b.WriteString("}\n")
	lines := strings.Split(b.String(), "\n")
	small := cutLines("A.java", b.String(), 200)
	// Member starts inside the class: the line after each closing brace.
	if !slices.Contains(small, 11) || !slices.Contains(small, 17) {
		t.Fatalf("member cuts %v", small)
	}
	if large := cutLines("A.java", b.String(), 1<<20); slices.Contains(large, 11) {
		t.Fatalf("a class that fits is not cut at members: %v", large)
	}
	if h := headerEnd("A.java", lines); h != 3 {
		t.Fatalf("header %d", h)
	}
	fu, long := unitsOf("A.java", lines, 1, len(lines), 400)
	if len(long) != 0 || len(fu.units) < 2 || fu.split || fu.units[0].start != 1 || fu.units[len(fu.units)-1].end != len(lines) {
		t.Fatalf("units %+v long %v", fu, long)
	}
	cuts := declarationCuts("A.java", lines, 400)
	for i, u := range fu.units {
		if !cuts[u.start] {
			t.Fatalf("unit %d starts inside a member: %+v", i, fu.units)
		}
		if numberedSize(lines, u.start, u.end) > 400 {
			t.Fatalf("unit %d too large", i)
		}
		if i > 0 && u.start != fu.units[i-1].end+1 {
			t.Fatalf("units not consecutive: %+v", fu.units)
		}
	}
}

func TestDeclarationCutsPython(t *testing.T) {
	src := "\"\"\"Module.\n\ndef not_code():\n\"\"\"\nimport os\n\n# helper\n@decorator\ndef f(\n    a,\n):\n    return a\n\nclass C:\n    def g(self):\n        pass\nX = [\n1,\n]\n"
	got := cutLines("m.py", src, 1<<20)
	want := []int{1, 5, 7, 14, 17}
	if !slices.Equal(got, want) {
		t.Fatalf("cuts %v want %v", got, want)
	}
	if h := headerEnd("m.py", strings.Split(src, "\n")); h != 5 {
		t.Fatalf("header %d", h)
	}
}

func TestDeclarationCutsRustAndC(t *testing.T) {
	rs := "use std::io;\n\n#[derive(Debug)]\nstruct S<'a> { x: &'a str }\n\nfn f() -> char {\n    '}'\n}\n"
	if got := cutLines("lib.rs", rs, 1<<20); !slices.Contains(got, 2) || !slices.Contains(got, 5) || slices.Contains(got, 4) {
		t.Fatalf("rust cuts %v", got)
	}
	if h := headerEnd("lib.rs", strings.Split(rs, "\n")); h != 1 {
		t.Fatalf("rust header %d", h)
	}
	c := "#include <stdio.h>\n#include \"x.h\"\n\n/* block { */\nint a(void) {\n  return '{';\n}\nint b;\n"
	if got := cutLines("x.c", c, 1<<20); !slices.Contains(got, 8) || !slices.Contains(got, 9) || slices.Contains(got, 6) {
		t.Fatalf("c cuts %v", got)
	}
	if h := headerEnd("x.c", strings.Split(c, "\n")); h != 2 {
		t.Fatalf("c header %d", h)
	}
	ts := "import { a } from \"./a\";\nimport {\n  b,\n} from \"./b\";\n\nexport function f() {\n  return `${a}}`;\n}\n"
	if h := headerEnd("x.ts", strings.Split(ts, "\n")); h != 4 {
		t.Fatalf("ts header %d", h)
	}
	if got := cutLines("x.ts", ts, 1<<20); !slices.Contains(got, 9) || slices.Contains(got, 7) {
		t.Fatalf("ts cuts %v", got)
	}
}

func TestUnitsWholeFileAndLineFallback(t *testing.T) {
	lines := strings.Split("package x\n\nfunc A() {}\n", "\n")
	fu, _ := unitsOf("x.go", lines, 1, len(lines), 1<<20)
	if len(fu.units) != 1 || fu.units[0] != (lineSpan{1, len(lines)}) || fu.split {
		t.Fatalf("whole file %+v", fu)
	}
	// One declaration larger than a unit is cut at lines and says so.
	var b strings.Builder
	b.WriteString("package x\n\nfunc Big() {\n")
	for i := range 200 {
		fmt.Fprintf(&b, "\tprintln(%d)\n", i)
	}
	b.WriteString("}\n")
	lines = strings.Split(b.String(), "\n")
	fu, long := unitsOf("x.go", lines, 1, len(lines), 600)
	if !fu.split || len(long) != 0 || fu.units[len(fu.units)-1].end != len(lines) {
		t.Fatalf("fallback %+v", fu)
	}
	// A line longer than a unit is reported, not sent.
	lines = []string{"package x", strings.Repeat("a", 900), "var y = 1"}
	if _, long := unitsOf("x.go", lines, 1, len(lines), 300); !slices.Equal(long, []int{2}) {
		t.Fatalf("long line %v", long)
	}
}
