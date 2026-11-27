package review

import (
	"go/ast"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The reason of a definition lead nothing was found for
// (docs/REVIEW.md#단서-해석). A file the run may not read (withheld,
// generated, outside the change or the scope, secondary) names it only when
// the anchor language's visibility rules let that file define the name the
// call on the anchor lines uses: for Go the package the call names, for
// Java the anchor's package and the classes its imports name, for Python,
// JavaScript/TypeScript and Rust the files an import naming the call's
// binding (or a wildcard import) points at, for C and C++ the files the
// anchor includes. A name the anchor's language defines itself
// (leadBuiltin) never takes such a reason unless an import or a declaration
// of the anchor's file binds it.

// definitionMissing is the reason of a definition lead the resolution found
// nothing for: a visible file's refusal, in the order of leadScan.missing,
// then the scan limit, else lead_definition_not_found.
func (r *leadResolver) definitionMissing(scan *leadScan, anchor record.Excerpt, symbol string) string {
	files, builtin := r.visibleFiles(scan, anchor, symbol)
	if !builtin {
		for _, reason := range []string{record.LeadWithheld, record.LeadOutsideChange, record.LeadOutsideScope, record.LeadGenerated, record.LeadSecondary} {
			for _, p := range files {
				if r.fileReason(scan, p) == reason {
					return reason
				}
			}
		}
	}
	if scan.limited {
		return record.LeadScanLimit
	}
	return record.LeadDefinitionMissing
}

// fileReason is why the lead may not read p: what its scan refused, or, for
// a file the scan did not try, what blocked says; "" when p may be read.
func (r *leadResolver) fileReason(scan *leadScan, p string) string {
	if why, ok := scan.refused[p]; ok {
		return why
	}
	if _, tried := scan.readable[p]; tried || callPathMetadata(p) || r.sentPaths[p] {
		return ""
	}
	if _, ok := r.catalog.files[p]; !ok {
		return ""
	}
	return r.blocked(p)
}

// leadCall is one call of a lead's symbol on its anchor lines, found in the
// tokens of the anchor's file: at is the name's token; path the names of a
// path call (a::b::symbol, Rust and C++; [""] for ::symbol); receiver the
// name before "." or "->" (when that is a name); member reports "." or "->".
type leadCall struct {
	at       int
	path     []string
	receiver string
	member   bool
}

// leadCalls lists the calls of symbol on the anchor lines: the name followed
// by "(", not declared there (after function, def, fn or class) and not
// constructed (after new).
func leadCalls(tokens []callToken, anchor record.Excerpt, symbol string) []leadCall {
	var out []leadCall
	for i, t := range tokens {
		if !t.name || t.text != symbol || t.line < anchor.Start || t.line > anchor.End || !tokenIs(tokens, i+1, "(") {
			continue
		}
		if slices.ContainsFunc([]string{"function", "def", "fn", "class", "new"}, func(k string) bool { return tokenIs(tokens, i-1, k) }) {
			continue
		}
		c := leadCall{at: i}
		switch {
		case tokenIs(tokens, i-1, "."):
			c.member = true
			if tokenName(tokens, i-2) {
				c.receiver = tokens[i-2].text
			}
		case tokenIs(tokens, i-1, ">") && tokenIs(tokens, i-2, "-"):
			c.member = true
			if tokenName(tokens, i-3) {
				c.receiver = tokens[i-3].text
			}
		case tokenIs(tokens, i-1, ":") && tokenIs(tokens, i-2, ":"):
			for k := i - 3; ; k -= 3 {
				if !tokenName(tokens, k) {
					if len(c.path) == 0 {
						c.path = []string{""}
					}
					break
				}
				c.path = append([]string{tokens[k].text}, c.path...)
				if !(tokenIs(tokens, k-1, ":") && tokenIs(tokens, k-2, ":")) {
					break
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// anchorText is the readable text of the anchor's file through the lead's
// scan.
func (r *leadResolver) anchorText(scan *leadScan, anchor record.Excerpt) (string, bool) {
	e, ok := r.catalog.files[anchor.Path]
	if !ok {
		return "", false
	}
	text, readable, err := scan.read(e)
	return text, err == nil && readable
}

// visibleFiles lists the files that may define the name a call of symbol on
// the anchor lines uses, and reports a builtin name of the anchor's
// language no import or declaration of the anchor's file binds.
func (r *leadResolver) visibleFiles(scan *leadScan, anchor record.Excerpt, symbol string) ([]string, bool) {
	lang := leadDefinitionLanguage(anchor.Path)
	if lang == "go" {
		return r.goVisible(scan, anchor, symbol)
	}
	text, ok := r.anchorText(scan, anchor)
	if !ok {
		return nil, false
	}
	var tokens []callToken
	if lang == "java" {
		tokens = javaTokens(text)
	} else {
		tokens = leadTokens(lang, text)
	}
	calls := leadCalls(tokens, anchor, symbol)
	switch lang {
	case "java":
		return r.javaVisible(tokens, calls, anchor, symbol)
	case "python", "javascript":
		return r.scriptVisible(lang, tokens, calls, anchor, symbol)
	case "rust":
		return r.rustVisible(tokens, calls, anchor, symbol)
	case "c", "cpp":
		return r.nativeVisible(lang, text, tokens, calls, anchor, symbol)
	}
	return nil, false
}

// goVisible: a package-qualified call names the files of the imported
// project package; a plain call or a method the anchor's package (its test
// files too for a test anchor). A builtin is a plain call of a Go builtin.
func (r *leadResolver) goVisible(scan *leadScan, anchor record.Excerpt, symbol string) ([]string, bool) {
	dir := path.Dir(anchor.Path)
	pkg := func(d string) []string {
		files := slices.Clone(r.catalog.dirs[d])
		if d == dir && strings.HasSuffix(anchor.Path, "_test.go") {
			for _, q := range r.catalog.ordered {
				if path.Dir(q) == dir && strings.HasSuffix(q, "_test.go") {
					files = append(files, q)
				}
			}
		}
		return files
	}
	own, err := scan.index.goFile(anchor.Path)
	if err != nil || own.parsed == nil {
		return pkg(dir), false
	}
	imports := map[string]string{}
	for _, spec := range own.parsed.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		name := path.Base(imported)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = imported
	}
	var files []string
	plain, other := false, false
	for call := range goCallsNamed(own.parsed, own.line, symbol, anchor) {
		selector, ok := ast.Unparen(goCallee(call)).(*ast.SelectorExpr)
		if !ok {
			plain = true
			files = append(files, pkg(dir)...)
			continue
		}
		other = true
		// A method of a value (a local, a field, a call's result) or a call
		// qualified by no import: the anchor's package.
		x, ok := selector.X.(*ast.Ident)
		if !ok || x.Obj != nil {
			files = append(files, pkg(dir)...)
			continue
		}
		imported, isImport := imports[x.Name]
		if !isImport {
			files = append(files, pkg(dir)...)
			continue
		}
		root := scan.index.goModule(dir)
		mod, err := scan.index.goModuleID(root)
		if root == "" || mod == "" || err != nil {
			continue
		}
		if relative, local := strings.CutPrefix(imported, mod+"/"); local && relative != "" {
			files = append(files, pkg(path.Join(root, relative))...)
		}
	}
	_, dotImport := imports["."]
	return files, plain && !other && !dotImport && leadBuiltin("go", symbol)
}

// javaVisible: the anchor's package (its directory), the class file a
// single-type import of a call's qualifier names, and the class file of a
// static import of the symbol or of all members. A builtin is a call
// qualified by a java.lang class no import names and the package does not
// declare.
func (r *leadResolver) javaVisible(tokens []callToken, calls []leadCall, anchor record.Excerpt, symbol string) ([]string, bool) {
	dir := path.Dir(anchor.Path)
	var files []string
	for _, q := range r.catalog.ordered {
		if path.Dir(q) == dir && strings.HasSuffix(q, ".java") && q != anchor.Path {
			files = append(files, q)
		}
	}
	classFiles := func(class string) {
		suffix := strings.ReplaceAll(class, ".", "/") + ".java"
		for _, q := range r.catalog.ordered {
			if q == suffix || strings.HasSuffix(q, "/"+suffix) {
				files = append(files, q)
			}
		}
	}
	imported := map[string]bool{}
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "import") {
			continue
		}
		start := i + 1
		static := tokenIs(tokens, start, "static")
		if static {
			start++
		}
		var names []string
		at := start
		for tokenName(tokens, at) || tokenIs(tokens, at, "*") {
			names = append(names, tokens[at].text)
			if !tokenIs(tokens, at+1, ".") {
				break
			}
			at += 2
		}
		if len(names) < 2 {
			continue
		}
		last := names[len(names)-1]
		switch {
		case static && (last == symbol || last == "*"):
			classFiles(strings.Join(names[:len(names)-1], "."))
		case !static && last != "*":
			imported[last] = true
			for _, c := range calls {
				if c.receiver == last {
					classFiles(strings.Join(names, "."))
				}
			}
		case !static:
			for _, c := range calls {
				if c.receiver != "" {
					classFiles(strings.Join(names[:len(names)-1], ".") + "." + c.receiver)
				}
			}
		}
	}
	builtin := len(calls) > 0
	for _, c := range calls {
		_, local := r.catalog.files[path.Join(dir, c.receiver+".java")]
		builtin = builtin && c.receiver != "" && leadBuiltin("java", c.receiver) && !imported[c.receiver] && !local
	}
	return files, builtin
}

// scriptVisible: the files an import binding the call's name points at (a
// plain call's name, a qualified call's receiver), and for Python the files
// of its wildcard imports. A builtin is a name of the language's list that no
// import (but a wildcard) and no declaration of the anchor's file binds.
func (r *leadResolver) scriptVisible(lang string, tokens []callToken, calls []leadCall, anchor record.Excerpt, symbol string) ([]string, bool) {
	python := lang == "python"
	var bindings []scriptBinding
	if python {
		bindings = pythonImports(tokens)
	} else {
		bindings = javascriptImports(tokens)
	}
	modules := func(b scriptBinding) []string {
		if python {
			out := pythonModuleFiles(r.catalog.files, anchor.Path, b.module)
			if b.imported != "" && b.imported != "*" {
				out = append(out, pythonModuleFiles(r.catalog.files, anchor.Path, strings.TrimSuffix(b.module, ".")+"."+b.imported)...)
			}
			return out
		}
		return leadScriptModuleFiles(r.catalog.files, anchor.Path, b.module)
	}
	var files []string
	bound := map[string]bool{}
	for _, b := range bindings {
		if b.typeOnly {
			continue
		}
		if b.local == "*" {
			files = append(files, modules(b)...)
			continue
		}
		bound[b.local] = true
		for _, c := range calls {
			if (!c.member && b.local == symbol) || (c.member && c.receiver == b.local) {
				files = append(files, modules(b)...)
				break
			}
		}
	}
	builtin := len(calls) > 0
	for _, c := range calls {
		name := symbol
		if c.member {
			name = c.receiver
		}
		builtin = builtin && name != "" && leadBuiltin(lang, name) && !bound[name] && !scriptDeclares(tokens, name, python)
	}
	return files, builtin
}

// scriptDeclares reports a declaration of name in a Python or JavaScript
// file: def, class, function, const, let, var or an assignment at the start
// of a statement.
func scriptDeclares(tokens []callToken, name string, python bool) bool {
	for i, t := range tokens {
		if !t.name || t.text != name {
			continue
		}
		for _, k := range []string{"def", "class", "function", "const", "let", "var"} {
			if tokenIs(tokens, i-1, k) {
				return true
			}
		}
		if statementStart(tokens, i) && tokenIs(tokens, i+1, "=") && !tokenIs(tokens, i+2, "=") {
			return true
		}
	}
	return false
}

// rustVisible: the module files a use binding the call's name (or its
// path's first name) points at, the files of its glob uses, and the module a
// path call names from the anchor's module (crate::, self::, super::, a child
// module). A builtin is a prelude name (a plain call, or the first name of a
// path call such as Box::new) no use and no item of the anchor's file binds.
func (r *leadResolver) rustVisible(tokens []callToken, calls []leadCall, anchor record.Excerpt, symbol string) ([]string, bool) {
	src, module, ok := rustCrate(r.catalog.files, anchor.Path)
	if !ok {
		return nil, false
	}
	modules := func(parts []string) []string {
		var out []string
		for _, m := range rustPathModules(module, parts) {
			out = append(out, rustModuleFiles(r.catalog.files, src, m)...)
		}
		return out
	}
	var files []string
	bound := map[string]bool{}
	for _, u := range rustUses(tokens) {
		if u.local == "" {
			files = append(files, modules(u.path[:len(u.path)-1])...)
			continue
		}
		bound[u.local] = true
		for _, c := range calls {
			switch {
			case len(c.path) == 0 && !c.member && u.local == symbol:
				files = append(files, modules(u.path[:len(u.path)-1])...)
			case len(c.path) > 0 && c.path[0] == u.local:
				files = append(files, modules(append(slices.Clone(u.path), c.path[1:]...))...)
			}
		}
	}
	for _, c := range calls {
		if len(c.path) > 0 && c.path[0] != "" && !bound[c.path[0]] {
			files = append(files, modules(c.path)...)
		}
	}
	builtin := len(calls) > 0
	for _, c := range calls {
		name := symbol
		if len(c.path) > 0 {
			name = c.path[0]
		}
		builtin = builtin && !c.member && leadBuiltin("rust", name) && !bound[name] && !rustDeclares(tokens, name)
	}
	return files, builtin
}

// rustDeclares reports an item of name in a Rust file (fn, struct, enum,
// union, trait, type, const, static or mod).
func rustDeclares(tokens []callToken, name string) bool {
	for i, t := range tokens {
		if t.name && t.text == name {
			for _, k := range []string{"fn", "struct", "enum", "union", "trait", "type", "const", "static", "mod"} {
				if tokenIs(tokens, i-1, k) {
					return true
				}
			}
		}
	}
	return false
}

// nativeVisible: the files the anchor includes (a quoted header beside it,
// else any C or C++ file whose path ends in the included name). A builtin
// is a plain call of a C library name the anchor neither declares nor
// defines as a macro, or a C++ call qualified by std.
func (r *leadResolver) nativeVisible(lang, text string, tokens []callToken, calls []leadCall, anchor record.Excerpt, symbol string) ([]string, bool) {
	directives, _ := nativeDirectives(text)
	var files []string
	for _, inc := range nativeIncludes(directives) {
		files = append(files, nativeIncludeFiles(r.catalog, anchor.Path, inc)...)
	}
	builtin := len(calls) > 0
	for _, c := range calls {
		switch {
		case c.member:
			builtin = false
		case len(c.path) > 0:
			builtin = builtin && lang == "cpp" && c.path[0] == "std"
		default:
			builtin = builtin && leadBuiltin(lang, symbol) && !nativeDeclares(tokens, directives, symbol)
		}
	}
	return files, builtin
}

// nativeDeclares reports a file-scope declaration or a macro of name in a C
// or C++ file: name( after a type at the outer level, or #define name.
func nativeDeclares(tokens []callToken, directives []nativeDirective, name string) bool {
	for _, d := range directives {
		if d.command == "define" && strings.HasPrefix(d.rest, name) &&
			(len(d.rest) == len(name) || !isIdentByte(d.rest[len(name)])) {
			return true
		}
	}
	for i, t := range tokens {
		if t.name && t.text == name && t.depth == 0 && tokenIs(tokens, i+1, "(") &&
			(tokenName(tokens, i-1) || tokenIs(tokens, i-1, "*") || tokenIs(tokens, i-1, "&")) {
			return true
		}
	}
	return false
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
