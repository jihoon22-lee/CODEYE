package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Review units (docs/REVIEW.md#검토-단위). A file is sent whole when it
// fits one unit; a larger file is cut at top-level declaration boundaries
// into consecutive units that each fit, and a unit that does not start the
// file travels with the file's header (package clause, imports, includes).
// Go files are cut with go/parser; the other languages with a small lexer
// of their own syntax (declarationCuts). One declaration larger than a unit
// is cut at its member boundaries, and only then at a line.

// lineSpan is a 1-based inclusive line range.
type lineSpan struct{ start, end int }

// fileUnits are the units of one file.
type fileUnits struct {
	header int // last header line (0: none)
	units  []lineSpan
	// split: a unit had to be cut inside one declaration.
	split bool
}

// numberedLineCost is what line n (text s) adds to a numbered excerpt's
// JSON size: the escaped text, the escaped newline and the line prefix.
func numberedLineCost(s string, n int) int {
	return record.JSONLen(s) - 2 + 2 + len(strconv.Itoa(n)) + 2
}

// numberedSize is the JSON size of lines[start-1:end] numbered from start.
func numberedSize(lines []string, start, end int) int {
	if start > end {
		return 0
	}
	return record.NumberedLen(strings.Join(lines[start-1:end], "\n"), start)
}

// unitsOf cuts the lines from..to of a file into units of at most maxBytes
// numbered JSON bytes. A line that alone exceeds maxBytes is skipped and
// reported in long. Units are greedy: each takes as many whole
// declarations as fit.
func unitsOf(p string, lines []string, from, to, maxBytes int) (fu fileUnits, long []int) {
	cuts := declarationCuts(p, lines, maxBytes)
	fu.header = headerEnd(p, lines)
	for pos := from; pos <= to; {
		size, best, fit := 0, -1, -1
		for i := pos; i <= to; i++ {
			size += numberedLineCost(lines[i-1], i)
			if size > maxBytes {
				break
			}
			fit = i
			if i == to || cuts[i+1] {
				best = i
			}
		}
		switch {
		case fit < 0:
			long = append(long, pos)
			pos++
			continue
		case best < 0:
			best = fit
			fu.split = true
		}
		fu.units = append(fu.units, lineSpan{pos, best})
		pos = best + 1
	}
	return fu, long
}

// declarationCuts marks the lines where a top-level declaration (with its
// leading comments and attributes) starts. Inside a declaration larger than
// maxBytes the starts of its members are marked too.
func declarationCuts(p string, lines []string, maxBytes int) map[int]bool {
	cuts := map[int]bool{1: true}
	switch ext := strings.ToLower(path.Ext(p)); {
	case ext == ".py" || ext == ".pyi":
		pythonCuts(lines, cuts)
	case ext == ".go":
		if !goCuts(lines, cuts) {
			braceCuts(lines, cuts, 0, goQuotes)
		}
		splitLargeSpans(lines, cuts, maxBytes, goQuotes)
	case braceLanguage(ext):
		q := cQuotes
		switch {
		case jsLike(ext):
			q = jsQuotes
		case cFamily(ext):
			q = cppQuotes
		}
		braceCuts(lines, cuts, 0, q)
		splitLargeSpans(lines, cuts, maxBytes, q)
	}
	return cuts
}

func braceLanguage(ext string) bool {
	switch ext {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx", ".java", ".kt", ".kts", ".scala", ".cs", ".rs", ".swift":
		return true
	}
	return jsLike(ext)
}

// cFamily are the C and C++ files, whose preprocessor lines the brace
// scan reads.
func cFamily(ext string) bool {
	switch ext {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return true
	}
	return false
}

func jsLike(ext string) bool {
	switch ext {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

// goCuts marks Go declaration starts (doc comments included); false when
// the file does not parse.
func goCuts(lines []string, cuts map[int]bool) bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", strings.Join(lines, "\n"), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	for _, d := range f.Decls {
		start := fset.Position(d.Pos()).Line
		if doc := declDoc(d); doc != nil {
			start = fset.Position(doc.Pos()).Line
		}
		cuts[start] = true
		if end := fset.Position(d.End()).Line; end < len(lines) {
			cuts[end+1] = true
		}
	}
	return true
}

// quoteRule lists the string delimiters and lexical forms of a language
// family that the brace scan must step over.
type quoteRule struct {
	backtick, single bool // ` strings (raw in Go, templates in JS); '…' strings
	// regex: a "/" where an operand is expected opens a regular expression
	// literal (JS), which may hold "/*" or braces.
	regex bool
	// preprocessor: "#" lines are directives (C, C++). Their braces do not
	// count, and the alternative branches of #if/#else count as one: the
	// depth after the first branch.
	preprocessor bool
}

var (
	goQuotes  = quoteRule{backtick: true}
	cQuotes   = quoteRule{}
	cppQuotes = quoteRule{preprocessor: true}
	jsQuotes  = quoteRule{backtick: true, single: true, regex: true}
)

// braceDepths returns the brace depth at the start and end of every line,
// skipping comments, strings and character literals.
func braceDepths(lines []string, q quoteRule) (startDepth, endDepth []int, closes []bool) {
	startDepth, endDepth, closes = make([]int, len(lines)), make([]int, len(lines)), make([]bool, len(lines))
	depth, block, quote := 0, false, byte(0)
	// branches holds, per open #if, the depth at the #if and after its
	// first branch (-1 until an #else or #elif ends it).
	type branch struct{ at, first int }
	var branches []branch
	directive := false
	for i, line := range lines {
		startDepth[i] = depth
		if q.preprocessor && !block && (directive || strings.HasPrefix(strings.TrimSpace(line), "#")) {
			if !directive {
				d := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#"))
				switch {
				case len(d) == 0:
				case strings.HasPrefix(d[0], "if"):
					branches = append(branches, branch{depth, -1})
				case (d[0] == "else" || strings.HasPrefix(d[0], "elif")) && len(branches) > 0:
					b := &branches[len(branches)-1]
					if b.first < 0 {
						b.first = depth
					}
					depth = b.at
				case d[0] == "endif" && len(branches) > 0:
					b := branches[len(branches)-1]
					branches = branches[:len(branches)-1]
					if b.first >= 0 {
						depth = b.first
					}
				}
			}
			directive = strings.HasSuffix(strings.TrimRight(line, " \t\r"), `\`)
			endDepth[i] = depth
			continue
		}
		last := byte(0)
		for j := 0; j < len(line); j++ {
			c := line[j]
			switch {
			case block:
				if c == '*' && j+1 < len(line) && line[j+1] == '/' {
					block = false
					j++
				}
				continue
			case quote != 0:
				if c == '\\' && quote != '`' {
					j++
				} else if c == quote {
					quote = 0
				}
				continue
			case c == '/' && j+1 < len(line) && line[j+1] == '/':
				j = len(line)
				continue
			case c == '/' && j+1 < len(line) && line[j+1] == '*':
				block = true
				j++
				continue
			case c == '"' || (c == '`' && q.backtick):
				quote = c
			case c == '/' && q.regex && regexOperand(line[:j], last):
				j = regexEnd(line, j)
			case c == '\'':
				if q.single {
					quote = c
				} else if k := charLiteralEnd(line, j); k > j {
					j = k
				}
			case c == '{':
				depth++
			case c == '}':
				depth = max(0, depth-1)
			}
			if c != ' ' && c != '\t' {
				last = c
			}
		}
		// A double-quoted string does not continue past a line end.
		if quote == '"' || quote == '\'' {
			quote = 0
		}
		endDepth[i] = depth
		closes[i] = last == '}' || last == ';'
	}
	return startDepth, endDepth, closes
}

// regexOperand reports whether a "/" after before (last its last
// non-blank byte on the line) is where a JS operand is expected: the line
// start, an operator or opening punctuation, or a keyword such as return.
func regexOperand(before string, last byte) bool {
	if last == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", last) >= 0 {
		return true
	}
	word := strings.TrimRightFunc(before, unicode.IsSpace)
	for _, k := range []string{"return", "typeof", "case", "in", "of", "delete", "void", "throw", "yield", "await"} {
		if strings.HasSuffix(word, k) && (len(word) == len(k) || !identByte(word[len(word)-len(k)-1])) {
			return true
		}
	}
	return false
}

func identByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// regexEnd is the index of the "/" closing a regular expression literal
// opened at j (escapes and [...] classes skipped), or j when the line has
// none (then the "/" was a division).
func regexEnd(line string, j int) int {
	class := false
	for k := j + 1; k < len(line); k++ {
		switch c := line[k]; {
		case c == '\\':
			k++
		case c == '[':
			class = true
		case c == ']':
			class = false
		case c == '/' && !class:
			return k
		}
	}
	return j
}

// charLiteralEnd is the index of the quote closing a character literal at
// j ('a', '\n', '\”), or j when the quote opens none (a Rust lifetime).
func charLiteralEnd(line string, j int) int {
	if j+2 < len(line) && line[j+1] != '\\' && line[j+2] == '\'' {
		return j + 2
	}
	if j+3 < len(line) && line[j+1] == '\\' && line[j+3] == '\'' {
		return j + 3
	}
	return j
}

// braceCuts marks, between lines from and to (1-based, 0 for the whole
// file), the line after every line that ends at depth level with a closing
// brace or semicolon: the next declaration at that level starts there.
func braceCuts(lines []string, cuts map[int]bool, level int, q quoteRule) {
	braceCutsIn(lines, cuts, level, q, 1, len(lines))
}

func braceCutsIn(lines []string, cuts map[int]bool, level int, q quoteRule, from, to int) {
	_, end, closes := braceDepths(lines, q)
	for i := from - 1; i < to-1; i++ {
		if end[i] == level && closes[i] {
			cuts[i+2] = true
		}
	}
}

// splitLargeSpans marks member starts inside every span between cuts that
// is larger than maxBytes (a class, an impl block, a namespace).
func splitLargeSpans(lines []string, cuts map[int]bool, maxBytes int, q quoteRule) {
	start, end, _ := braceDepths(lines, q)
	var starts []int
	for n := 1; n <= len(lines); n++ {
		if cuts[n] {
			starts = append(starts, n)
		}
	}
	for k, a := range starts {
		b := len(lines)
		if k+1 < len(starts) {
			b = starts[k+1] - 1
		}
		if numberedSize(lines, a, b) <= maxBytes {
			continue
		}
		// The member level is the depth inside the span's outermost block.
		level := -1
		for i := a - 1; i < b; i++ {
			if end[i] > start[a-1] {
				level = start[a-1] + 1
				break
			}
		}
		if level > 0 {
			braceCutsIn(lines, cuts, level, q, a, b)
		}
	}
}

// pythonCuts marks every top-level statement start outside strings and
// brackets; decorators and the comments directly above a statement stay
// with it.
func pythonCuts(lines []string, cuts map[int]bool) {
	depth, triple := 0, ""
	pending := 0 // first line of comments or decorators above the next statement
	for i, line := range lines {
		n := i + 1
		// A CRLF file's blank line is "\r" here; it is not a statement.
		topLevel := triple == "" && depth == 0 && strings.TrimSpace(line) != "" && line[0] != ' ' && line[0] != '\t'
		if topLevel {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@"):
				if pending == 0 {
					pending = n
				}
			case strings.HasPrefix(trimmed, "else") || strings.HasPrefix(trimmed, "elif") || strings.HasPrefix(trimmed, "except") ||
				strings.HasPrefix(trimmed, "finally") || strings.HasPrefix(trimmed, ")") || strings.HasPrefix(trimmed, "]") || strings.HasPrefix(trimmed, "}"):
				pending = 0
			default:
				if pending == 0 {
					pending = n
				}
				cuts[pending] = true
				pending = 0
			}
		} else if strings.TrimSpace(line) != "" {
			pending = 0
		}
		depth, triple = pythonScan(line, depth, triple)
	}
}

// pythonScan updates the bracket depth and open triple-quoted string over
// one line.
func pythonScan(line string, depth int, triple string) (int, string) {
	for j := 0; j < len(line); j++ {
		if triple != "" {
			if strings.HasPrefix(line[j:], triple) {
				j += 2
				triple = ""
			} else if line[j] == '\\' {
				j++
			}
			continue
		}
		switch c := line[j]; c {
		case '#':
			return depth, triple
		case '"', '\'':
			if strings.HasPrefix(line[j:], strings.Repeat(string(c), 3)) {
				triple = strings.Repeat(string(c), 3)
				j += 2
				continue
			}
			for j++; j < len(line) && line[j] != c; j++ {
				if line[j] == '\\' {
					j++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth = max(0, depth-1)
		}
	}
	return depth, triple
}

// headerLine matches the package, import and include lines of a file's
// header in the supported languages.
var headerLine = regexp.MustCompile(`^\s*(package\s|import[\s({]|from\s+\S+\s+import\s|#\s*(include|import|pragma|ifndef|define|if|endif)\b|using\s+[\w.:]+\s*;|use\s|pub\s+use\s|extern\s+crate\s|mod\s+\w+\s*;|#!\[|(const|let|var)\s+.*=\s*require\(|['"]use strict['"])`)

// maxHeaderLines bounds the header sent with a unit.
const maxHeaderLines = 80

// headerEnd is the last line of the file's header: the leading package,
// import and include lines (with comments, blank lines and, for Go, the
// whole import declarations between them); 0 when the file has none.
func headerEnd(p string, lines []string) int {
	if strings.ToLower(path.Ext(p)) == ".go" {
		if n, ok := goHeaderEnd(lines); ok {
			return min(n, maxHeaderLines)
		}
	}
	last, block, triple := 0, false, ""
	for i := 0; i < len(lines) && i < maxHeaderLines; i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		switch {
		case triple != "":
			if strings.Contains(t, triple) {
				triple = ""
			}
		case block:
			block = !strings.Contains(t, "*/")
		case t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#!") && i == 0 || strings.HasPrefix(t, "# ") && !headerLine.MatchString(line) || t == "#":
		case strings.HasPrefix(t, "/*"):
			block = !strings.Contains(t[2:], "*/")
		case last == 0 && (strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, `'''`)):
			// A module docstring.
			if q := t[:3]; !strings.Contains(t[3:], q) {
				triple = q
			}
		case headerLine.MatchString(line):
			// An import list continued until its brackets close, after a
			// trailing comma, or a directive continued with a backslash (a
			// multi-line #define).
			directive, depth := strings.HasPrefix(t, "#"), 0
			if !directive {
				depth = bracketDelta(t)
			}
			for i+1 < len(lines) && i+1 < maxHeaderLines && (depth > 0 || !directive && strings.HasSuffix(t, ",") || strings.HasSuffix(t, `\`)) {
				i++
				t = strings.TrimSpace(lines[i])
				if !directive {
					depth += bracketDelta(t)
				}
			}
			last = i + 1
		default:
			return last
		}
	}
	return last
}

// bracketDelta is the opening minus the closing brackets of a header line
// outside its quoted strings and trailing comment.
func bracketDelta(t string) int {
	n, quote := 0, byte(0)
	for j := 0; j < len(t); j++ {
		switch c := t[j]; {
		case quote != 0:
			if c == '\\' {
				j++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case j > 0 && (c == '#' || c == '/' && j+1 < len(t) && t[j+1] == '/'):
			return n
		case c == '(' || c == '[' || c == '{':
			n++
		case c == ')' || c == ']' || c == '}':
			n--
		}
	}
	return n
}

// goHeaderEnd is the end of the package clause and the import
// declarations.
func goHeaderEnd(lines []string) (int, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", strings.Join(lines, "\n"), parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return 0, false
	}
	end := fset.Position(f.Name.End()).Line
	for _, d := range f.Decls {
		end = max(end, fset.Position(d.End()).Line)
	}
	return end, true
}

func declDoc(d ast.Decl) *ast.CommentGroup {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return d.Doc
	case *ast.GenDecl:
		return d.Doc
	}
	return nil
}
