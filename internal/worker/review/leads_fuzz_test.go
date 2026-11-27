package review

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// The lead readers read the project's own source, which may hold anything:
// no input may panic them, and a location they find lies in its file. The
// fuzz targets run them on mutated fixture trees (go test runs the seeds;
// go test -fuzz explores further); TestLeadReadersReadEveryPrefix runs them
// on every prefix of every fixture file.

// leadFile is one file of a lead reader's input; a refused file is withheld
// from the lead's scan.
type leadFile struct {
	path, text string
	refused    bool
}

// leadFixture is a tree of the lead tests, its anchor file and the symbol
// its lead names.
type leadFixture struct {
	tree     map[string]string
	anchor   string
	symbol   string
	withheld []string
}

// files lists the fixture's files, the anchor first.
func (x leadFixture) files() []leadFile {
	out := []leadFile{{path: x.anchor, text: x.tree[x.anchor], refused: slices.Contains(x.withheld, x.anchor)}}
	for _, p := range slices.Sorted(maps.Keys(x.tree)) {
		if p != x.anchor {
			out = append(out, leadFile{path: p, text: x.tree[p], refused: slices.Contains(x.withheld, p)})
		}
	}
	return out
}

// leadFixtures are the trees of the lead binding, reason and dry-run tests.
func leadFixtures() []leadFixture {
	var out []leadFixture
	for _, cases := range [][]bindCase{pythonScopingCases(), scriptScopingCases(), returnAnnotationCases(), specifierCases(),
		regexLiteralCases(), rustScopingCases(), rustMethodCases(), nativeLookupCases()} {
		for _, c := range cases {
			out = append(out, leadFixture{c.tree, c.path, c.symbol, c.withheld})
		}
	}
	for _, c := range reasonCases() {
		out = append(out, leadFixture{c.tree, c.path, c.symbol, c.withheld})
	}
	for _, c := range prototypeCases() {
		out = append(out, leadFixture{tree: c.tree(), anchor: "native/app.c", symbol: "rate"})
	}
	for _, c := range dryRunLeadCases(3) {
		out = append(out, leadFixture{tree: c.tree, anchor: c.anchor, symbol: "share"})
	}
	return append(out, leadFixture{tree: rustTree(rustWholeFile), anchor: "src/lib.rs", symbol: "price"})
}

// leadSeedFixtures are leadFixtures and the per-language call projects.
func leadSeedFixtures() []leadFixture {
	out := leadFixtures()
	projects := testCallProjects()
	for _, lang := range slices.Sorted(maps.Keys(projects)) {
		p := projects[lang]
		m := leadCallName.FindAllStringSubmatch(p.call, -1)
		out = append(out, leadFixture{tree: p.tree, anchor: p.caller, symbol: m[len(m)-1][1]})
	}
	return out
}

// leadSeedTexts are the fixture files of the languages langs, each once.
func leadSeedTexts(langs ...string) []string {
	var out []string
	for _, x := range leadSeedFixtures() {
		for _, f := range x.files() {
			if slices.Contains(langs, leadDefinitionLanguage(f.path)) && !slices.Contains(out, f.text) {
				out = append(out, f.text)
			}
		}
	}
	return out
}

// A fuzz input holds a tree as its files joined by "\x00", each one
// "path\x01text" with "!" before a withheld file's path; the first file is
// the anchor.
func encodeLeadFiles(files []leadFile) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		p := f.path
		if f.refused {
			p = "!" + p
		}
		parts = append(parts, p+"\x01"+f.text)
	}
	return strings.Join(parts, "\x00")
}

// decodeLeadFiles reads encodeLeadFiles' form: at most eight files of clean
// relative paths, 64 KiB in all; false for anything else.
func decodeLeadFiles(s string) ([]leadFile, bool) {
	if len(s) > 1<<16 {
		return nil, false
	}
	var out []leadFile
	for part := range strings.SplitSeq(s, "\x00") {
		p, text, ok := strings.Cut(part, "\x01")
		refused := strings.HasPrefix(p, "!")
		p = strings.TrimPrefix(p, "!")
		if !ok || len(out) == 8 || p == "" || p == "." || len(p) > 128 || !utf8.ValidString(p) || path.Clean(p) != p || path.IsAbs(p) ||
			p == ".." || strings.HasPrefix(p, "../") || slices.ContainsFunc(out, func(f leadFile) bool { return f.path == p }) {
			return nil, false
		}
		out = append(out, leadFile{path: p, text: text, refused: refused})
	}
	return out, len(out) > 0
}

// leadCallName finds a name a call may use: an identifier before "(" or
// before type arguments and "(".
var leadCallName = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:(?:::)?<[^<>(){};]*>\s*)?\(`)

// leadReaderDepth is how much runLeadReaders reads: thorough reads up to
// nine names of the anchor (symbol and the names it calls), a whole-file and
// a one-line anchor, a scan below and at its limit; else symbol alone, the
// whole file, below the limit.
type leadReaderDepth bool

const (
	leadReadersThorough leadReaderDepth = true
	leadReadersLight    leadReaderDepth = false
)

// runLeadReaders runs the lead readers over files (the anchor first): each
// file's scan and declaration readers, then the definition and reason
// finders of the anchor, the scan served from memory. It fails on a token
// tree that breaks the scan's invariants or a location outside its file.
func runLeadReaders(t testing.TB, files []leadFile, symbol string, depth leadReaderDepth) {
	t.Helper()
	for _, f := range files {
		checkLeadFileReaders(t, f)
	}
	runLeadFinders(t, files, symbol, depth)
}

// runLeadFinders runs the definition and reason finders of files' anchor.
func runLeadFinders(t testing.TB, files []leadFile, symbol string, depth leadReaderDepth) {
	t.Helper()
	anchor := files[0]
	lang := leadDefinitionLanguage(anchor.path)
	if lang == "" || anchor.refused {
		return
	}
	entries := map[string]source.Entry{}
	lines := map[string]int{}
	for _, f := range files {
		size := int64(len(f.text))
		entries[f.path] = source.Entry{Path: f.path, Kind: "file", Size: &size, SHA256: "fuzz"}
		lines[f.path] = strings.Count(f.text, "\n") + 1
	}
	r := &leadResolver{s: &roleStage{ctx: t.Context()}, catalog: newSourceCallCatalog(entries), sentPaths: map[string]bool{}}
	scan := func(limited bool) *leadScan {
		l := r.scan()
		for _, f := range files {
			if f.refused {
				l.readable[f.path], l.refused[f.path] = false, record.LeadWithheld
				continue
			}
			l.texts[f.path], l.readable[f.path] = f.text, true
		}
		l.limited = limited
		return l
	}
	names := []string{symbol}
	excerpts := func(string) []record.Excerpt {
		return []record.Excerpt{{Path: anchor.path, Start: 1, End: lines[anchor.path], Text: anchor.text}}
	}
	limits := []bool{false}
	if depth == leadReadersThorough {
		for _, m := range leadCallName.FindAllStringSubmatch(anchor.text, -1) {
			if len(names) < 9 && !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
		whole := excerpts("")
		excerpts = func(name string) []record.Excerpt {
			for n, line := range strings.Split(anchor.text, "\n") {
				if strings.Contains(line, name+"(") {
					return append(whole, record.Excerpt{Path: anchor.path, Start: n + 1, End: n + 1, Text: line})
				}
			}
			return whole
		}
		limits = []bool{false, true}
	}
	for _, name := range names {
		// The record checks take only such symbols (checkLeads).
		if !record.ValidEvidenceSymbol(name) {
			continue
		}
		for _, x := range excerpts(name) {
			for _, limited := range limits {
				// Go and Java definitions are the shared readers'; their
				// reasons are this layer's.
				if lang != "go" && lang != "java" {
					hits, _, err := r.definitions(scan(limited), name, x)
					if err != nil {
						t.Fatalf("definitions of %q: %v", name, err)
					}
					for _, h := range hits {
						checkLeadLocation(t, lines, h.loc)
						if h.extent.start != 0 {
							checkLeadLocation(t, lines, h.extent)
						}
					}
				}
				r.definitionMissing(scan(limited), x, name)
			}
		}
	}
}

// checkLeadLocation fails unless loc names lines of a file of the input.
func checkLeadLocation(t testing.TB, lines map[string]int, loc sourceCallLocation) {
	t.Helper()
	n, ok := lines[loc.path]
	if !ok || loc.start < 1 || loc.end < loc.start || loc.end > n {
		t.Fatalf("location %+v outside the input (%d lines)", loc, n)
	}
}

// checkLeadTokens fails unless tokens keep the invariants the readers index
// by: lines in order and inside the text, every bracket's close and parent
// pointing at its partner, depths following the parents. nil (the scan gave
// up) passes.
func checkLeadTokens(t testing.TB, text string, tokens []callToken) {
	t.Helper()
	lines := strings.Count(text, "\n") + 1
	opener := func(k int) bool {
		return !tokens[k].quoted && (tokens[k].text == "(" || tokens[k].text == "[" || tokens[k].text == "{")
	}
	closer := func(k int) bool {
		return !tokens[k].quoted && (tokens[k].text == ")" || tokens[k].text == "]" || tokens[k].text == "}")
	}
	for i, tok := range tokens {
		bad := ""
		switch {
		case tok.line < 1 || tok.endLine < tok.line || tok.endLine > lines || i > 0 && tok.line < tokens[i-1].endLine:
			bad = "lines"
		case tok.parent < -1 || tok.parent >= i || tok.parent == -1 && tok.depth != 0:
			bad = "parent"
		case tok.parent >= 0 && (!opener(tok.parent) || tokens[tok.parent].close < i || tok.depth != tokens[tok.parent].depth+1):
			bad = "parent's bracket"
		case opener(i) && (tok.close <= i || tok.close >= len(tokens) || !closer(tok.close) || tokens[tok.close].parent != i):
			bad = "close"
		case !opener(i) && tok.close != 0:
			bad = "close of no bracket"
		case closer(i) && (tok.parent < 0 || tokens[tok.parent].close != i):
			bad = "closer"
		}
		if bad != "" {
			t.Fatalf("token %d %+v: %s (%d lines)", i, tok, bad, lines)
		}
	}
}

// leadTokenNames are the distinct names of tokens, the first limit of them.
func leadTokenNames(tokens []callToken, limit int) []string {
	var out []string
	for _, tok := range tokens {
		if tok.name && !tok.quoted && len(out) < limit && !slices.Contains(out, tok.text) {
			out = append(out, tok.text)
		}
	}
	return out
}

// checkLeadFileReaders runs the scan and the declaration readers of f's
// language over every name of f (the first sixteen), and fails on an index
// outside the tokens or a declaration outside the file.
func checkLeadFileReaders(t testing.TB, f leadFile) {
	t.Helper()
	lines := strings.Count(f.text, "\n") + 1
	all := record.Excerpt{Path: f.path, Start: 1, End: lines}
	declaration := func(d scriptDeclaration) {
		if d.start < 1 || d.end < d.start || d.end > lines || d.boundStart < d.start || d.boundStart > d.end || d.bound != 0 && (d.bound < d.boundStart || d.bound > d.end) {
			t.Fatalf("declaration %+v outside %s (%d lines)", d, f.path, lines)
		}
	}
	span := func(what string, first, last, n int) {
		if first < 0 || last < first || last >= n {
			t.Fatalf("%s %d-%d outside %d tokens of %s", what, first, last, n, f.path)
		}
	}
	switch lang := leadDefinitionLanguage(f.path); lang {
	case "python", "javascript":
		tokens := leadTokens(lang, f.text)
		checkLeadTokens(t, f.text, tokens)
		python, typeScript := lang == "python", typeScriptPath(f.path)
		var imports []scriptBinding
		if python {
			imports = pythonImports(tokens)
		} else {
			imports = javascriptImports(tokens)
		}
		for _, b := range imports {
			span("import binding", b.token, b.token, len(tokens))
		}
		for _, name := range leadTokenNames(tokens, 16) {
			s := scriptScopesOf(tokens, name, python, typeScript, imports)
			s.moduleBindings()
			for _, o := range s.occurrences {
				s.shadowed(o.at)
				if python {
					pythonClassScope(s, o.at)
					if d, ok := pythonFunctionDeclaration(tokens, s.blocks, o.at); ok {
						declaration(d)
					}
					continue
				}
				bodiless(tokens, o.at)
				exportedDeclaration(tokens, o.at)
				if d, ok := jsFunctionDeclaration(tokens, o.at); ok {
					declaration(d)
				}
			}
			if e := scriptExported(tokens, name, python, typeScript); e.found {
				declaration(e.decl)
			}
			exportClause(tokens, name)
		}
		for i := range tokens {
			scriptCall(tokens, i, python, typeScript)
		}
	case "rust":
		tokens := leadTokens(lang, f.text)
		checkLeadTokens(t, f.text, tokens)
		rf := newRustFile(tokens)
		for _, fn := range rf.fns {
			span("fn", fn.first, fn.last, len(tokens))
		}
		for _, name := range leadTokenNames(tokens, 16) {
			for _, c := range rf.calls(all, name) {
				if out := rf.bind(c, name); out.hit != nil {
					span("bound fn", out.hit.first, out.hit.last, len(tokens))
				}
			}
			rf.valueItems(-1, name)
		}
	case "c", "cpp":
		masked, ok := nativeText(f.text)
		if ok && (len(masked) != len(f.text) || strings.Count(masked, "\n") != lines-1) {
			t.Fatalf("masked text of %s moved bytes or lines", f.path)
		}
		directives, cond := nativeDirectives(f.text)
		if len(cond) != lines+1 || slices.ContainsFunc(directives, func(d nativeDirective) bool { return d.line < 1 || d.line > lines }) {
			t.Fatalf("directives of %s outside its %d lines: %d marks, %+v", f.path, lines, len(cond), directives)
		}
		nativeIncludes(directives)
		u := newNativeUnit(f.path, f.text)
		if u == nil {
			return
		}
		checkLeadTokens(t, masked, u.tokens)
		for _, name := range leadTokenNames(u.tokens, 16) {
			for _, d := range u.decls(name) {
				span("declaration", d.first, d.last, len(u.tokens))
			}
			for _, i := range u.calls(all, name) {
				if _, fn, body, ok := u.callScope(i); ok && body >= 0 {
					u.shadowed(fn, body, name)
				}
			}
			u.macro(name)
			u.using(name)
		}
		for i := range u.tokens {
			switch {
			case tokenIs(u.tokens, i, "("):
				nativeSignature(u.tokens, i)
			case tokenIs(u.tokens, i, "{"):
				u.block(i)
				u.declScope(i)
			}
		}
	}
}

// leadFuzzTree decodes a fuzz tree whose anchor is in one of langs.
func leadFuzzTree(tree string, langs ...string) ([]leadFile, bool) {
	files, ok := decodeLeadFiles(tree)
	if !ok || !slices.Contains(langs, leadDefinitionLanguage(files[0].path)) {
		return nil, false
	}
	return files, true
}

// addLeadSeeds seeds f with the fixture trees whose anchor is in langs.
func addLeadSeeds(f *testing.F, langs ...string) {
	for _, x := range leadSeedFixtures() {
		if slices.Contains(langs, leadDefinitionLanguage(x.anchor)) {
			f.Add(encodeLeadFiles(x.files()), x.symbol)
		}
	}
}

// The JavaScript and TypeScript scan (a "/" read as division or as a
// regular expression literal, template expressions read as code) and its
// import reader.
func FuzzLeadScriptTokens(f *testing.F) {
	for _, text := range leadSeedTexts("javascript") {
		f.Add(text)
	}
	for _, c := range slashCases() {
		f.Add(c.text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		tokens := leadTokens("javascript", text)
		checkLeadTokens(t, text, tokens)
		for _, b := range javascriptImports(tokens) {
			if b.token < 0 || b.token >= len(tokens) {
				t.Fatalf("import binding %+v outside %d tokens", b, len(tokens))
			}
		}
	})
}

// The C and C++ scan (comments, strings, characters and directives
// blanked) and its directive reader.
func FuzzLeadNativeTokens(f *testing.F) {
	for _, text := range leadSeedTexts("c", "cpp") {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		checkLeadFileReaders(t, leadFile{path: "native/fuzz.c", text: text})
	})
}

// The Python scoping and declaration readers and the Python definition and
// reason finders.
func FuzzLeadPythonDefinitions(f *testing.F) {
	addLeadSeeds(f, "python")
	// An indented def whose body sits at its column: the first return
	// after it is no longer its own.
	f.Add(encodeLeadFiles([]leadFile{{path: "app/jobs.py", text: " def A:\n return 0"}}), "A")
	f.Fuzz(func(t *testing.T, tree, symbol string) {
		if files, ok := leadFuzzTree(tree, "python"); ok {
			runLeadReaders(t, files, symbol, leadReadersThorough)
		}
	})
}

// The JavaScript and TypeScript scoping, declaration and export readers and
// their definition and reason finders.
func FuzzLeadScriptDefinitions(f *testing.F) {
	addLeadSeeds(f, "javascript")
	f.Fuzz(func(t *testing.T, tree, symbol string) {
		if files, ok := leadFuzzTree(tree, "javascript"); ok {
			runLeadReaders(t, files, symbol, leadReadersThorough)
		}
	})
}

// The Rust item, use and pattern readers and the Rust definition and reason
// finders.
func FuzzLeadRustDefinitions(f *testing.F) {
	addLeadSeeds(f, "rust")
	f.Fuzz(func(t *testing.T, tree, symbol string) {
		if files, ok := leadFuzzTree(tree, "rust"); ok {
			runLeadReaders(t, files, symbol, leadReadersThorough)
		}
	})
}

// The C and C++ declaration, scope and include readers and their definition
// and reason finders (translation unit, external definitions).
func FuzzLeadNativeDefinitions(f *testing.F) {
	addLeadSeeds(f, "c", "cpp")
	f.Fuzz(func(t *testing.T, tree, symbol string) {
		if files, ok := leadFuzzTree(tree, "c", "cpp"); ok {
			runLeadReaders(t, files, symbol, leadReadersThorough)
		}
	})
}

// The Go and Java reason finders (the visibility of a refused file).
func FuzzLeadGoJavaReasons(f *testing.F) {
	addLeadSeeds(f, "go", "java")
	f.Fuzz(func(t *testing.T, tree, symbol string) {
		if files, ok := leadFuzzTree(tree, "go", "java"); ok {
			runLeadReaders(t, files, symbol, leadReadersThorough)
		}
	})
}

// Every prefix of every fixture file, the rest of its tree whole, passes
// through the file's readers and the anchor's finders.
func TestLeadReadersReadEveryPrefix(t *testing.T) {
	checked := map[string]bool{}
	for _, x := range leadFixtures() {
		files := x.files()
		for k, f := range files {
			if leadDefinitionLanguage(f.path) == "" {
				continue
			}
			// The file readers depend on the file alone: each text once.
			key := path.Ext(f.path) + "\x00" + f.text
			read := !checked[key]
			checked[key] = true
			cut := slices.Clone(files)
			for n := range len(f.text) + 1 {
				cut[k].text = f.text[:n]
				if read {
					checkLeadFileReaders(t, cut[k])
				}
				runLeadFinders(t, cut, x.symbol, leadReadersLight)
			}
		}
	}
}
