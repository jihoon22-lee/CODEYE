package review

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A CRLF file's blank line ("\r" after splitting on "\n") is not a
// top-level statement.
func TestPythonCutsIgnoreCRLFBlankLines(t *testing.T) {
	src := "import os\r\n\r\ndef f():\r\n    x = 1\r\n\r\n    return x\r\n\r\ndef g():\r\n    pass\r\n"
	got := cutLines("m.py", src, 1<<20)
	if want := []int{1, 3, 8}; !slices.Equal(got, want) {
		t.Fatalf("cuts %v want %v", got, want)
	}
}

// An import continued over several lines ends where its brackets close,
// and the header goes on to the next import; a multi-line #define is not
// cut in the middle.
func TestHeaderEndContinuedImports(t *testing.T) {
	for _, c := range []struct {
		path, src string
		want      int
	}{
		{"m.py", "from x import (a,\n    b)\nimport os\n\ndef f():\n    pass\n", 3},
		{"m.py", "from x import (\n    a,\n    b,\n)\n\nclass C:\n    pass\n", 4},
		{"x.ts", "import { a,\n  b } from \"x\";\nimport c from \"c\";\n\nexport function f() {}\n", 3},
		{"x.ts", "import {\n  a,\n} from \"./a\";\nimport {\n  b,\n} from \"./b\";\n\nexport const x = 1;\n", 6},
		{"x.c", "#include <a.h>\n#define TWICE(x) \\\n  ((x) + \\\n   (x))\n\nint f(void) { return 1; }\n", 4},
	} {
		if h := headerEnd(c.path, strings.Split(c.src, "\n")); h != c.want {
			t.Errorf("%s %q: header %d want %d", c.path, c.src, h, c.want)
		}
	}
}

// A JS regular expression literal containing "/*" is not a comment, so the
// declarations after it are still cut.
func TestBraceCutsJSRegexLiteral(t *testing.T) {
	src := "export function trim(s) {\n  return s.replace(/\\/*$/, \"\");\n}\n\nexport function g() {\n  return 1;\n}\n"
	if got := cutLines("x.js", src, 1<<20); !slices.Contains(got, 4) {
		t.Fatalf("cuts %v", got)
	}
	div := "export function half(a, b) {\n  return a / b / 2;\n}\n\nexport function g() {}\n"
	if got := cutLines("x.js", div, 1<<20); !slices.Contains(got, 4) {
		t.Fatalf("division cuts %v", got)
	}
	// JSX closing and self-closing tags are not regular expressions.
	jsx := "export function A({ flag }) {\n  return <span>{flag && <b>yes</b>}</span>;\n}\n\nexport function B({ x }) {\n  return <C v={x} />;\n}\n\nexport function D() {}\n"
	if got := cutLines("x.tsx", jsx, 1<<20); !slices.Contains(got, 4) || !slices.Contains(got, 8) {
		t.Fatalf("jsx cuts %v", got)
	}
}

// A CRLF file is cut like the same file with LF line ends.
func TestBraceCutsCRLF(t *testing.T) {
	for path, src := range map[string]string{
		"x.c":    "#include <stdio.h>\n\nint a(void) {\n  return 1;\n}\nint b;\n",
		"A.java": "package p;\n\nclass A {\n  int f() { return 1; }\n}\n\nclass B {}\n",
		"x.ts":   "import { a } from \"./a\";\n\nexport function f() {\n  return a;\n}\n\nexport const x = 1;\n",
	} {
		lf := cutLines(path, src, 1<<20)
		if crlf := cutLines(path, strings.ReplaceAll(src, "\n", "\r\n"), 1<<20); !slices.Equal(lf, crlf) {
			t.Errorf("%s: CRLF cuts %v, LF cuts %v", path, crlf, lf)
		}
	}
}

// Alternative preprocessor branches that each open a function do not leave
// the rest of a C file one level deep.
func TestBraceCutsCPreprocessorBranches(t *testing.T) {
	src := "#ifdef WIDE\nint f(long a) {\n#else\nint f(int a) {\n#endif\n  return 1;\n}\n\nint g(void) {\n  return 2;\n}\n"
	if got := cutLines("x.c", src, 1<<20); !slices.Contains(got, 8) {
		t.Fatalf("cuts %v", got)
	}
	macro := "#define OPEN {\nint f(void) {\n  return 1;\n}\n\nint g(void) {\n  return 2;\n}\n"
	if got := cutLines("x.c", macro, 1<<20); !slices.Contains(got, 5) {
		t.Fatalf("macro cuts %v", got)
	}
}

// fitExcerpts with nothing to fit but required items is too large, not a
// panic.
func TestFitExcerptsKeepBeyondList(t *testing.T) {
	s := &roleStage{h: Handler{Budget: smallBudget}}
	_, _, err := s.fitExcerpts("", 1, nil, func(list []record.Excerpt) ([]byte, error) {
		return []byte(strings.Repeat("x", 1<<20)), nil
	})
	if !errors.Is(err, errTooLarge) {
		t.Fatalf("err %v", err)
	}
}
