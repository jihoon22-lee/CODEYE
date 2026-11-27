package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// What a lead's anchor file imports or includes, and the snapshot files a
// specifier names (docs/REVIEW.md#단서-해석). These readings serve lead
// resolution only; the shared call catalog keeps its own (directModule,
// importedBindings, rustLocalImports, cCalls).

// scriptBinding is one name an import binds in a Python or JavaScript file:
// local is the bound name ("*" for a Python wildcard), imported the name in
// the module ("" for the module or namespace itself, "default" for a
// default import, "*" for a wildcard), module the specifier and token the
// index of the bound name's token.
type scriptBinding struct {
	local, imported, module string
	token                   int
	typeOnly                bool
}

// statementStart reports a token that begins a logical line: the first
// token of its line outside any bracket, or the token after ";".
func statementStart(tokens []callToken, i int) bool {
	if tokens[i].depth != 0 {
		return false
	}
	return i == 0 || tokens[i-1].endLine < tokens[i].line || tokenIs(tokens, i-1, ";")
}

// pythonImports lists the bindings of Python import statements: from M
// import a, b as c (in parentheses too), from M import *, import a.b and
// import a.b as c (import a.b binds a, the top package).
func pythonImports(tokens []callToken) []scriptBinding {
	var out []scriptBinding
	for i, t := range tokens {
		if !t.name || !statementStart(tokens, i) {
			continue
		}
		switch t.text {
		case "from":
			j := i + 1
			var module strings.Builder
			for tokenIs(tokens, j, ".") {
				module.WriteString(".")
				j++
			}
			for tokenName(tokens, j) && !tokenIs(tokens, j, "import") {
				module.WriteString(tokens[j].text)
				j++
				if !tokenIs(tokens, j, ".") {
					break
				}
				module.WriteString(".")
				j++
			}
			if !tokenIs(tokens, j, "import") || module.Len() == 0 {
				continue
			}
			j++
			end := len(tokens)
			if tokenIs(tokens, j, "(") {
				end = tokens[j].close
				j++
			}
			if tokenIs(tokens, j, "*") {
				out = append(out, scriptBinding{local: "*", imported: "*", module: module.String(), token: j})
				continue
			}
			for j < end && tokenName(tokens, j) {
				name, at := tokens[j].text, j
				if tokenIs(tokens, j+1, "as") && tokenName(tokens, j+2) {
					at = j + 2
					j += 2
				}
				out = append(out, scriptBinding{local: tokens[at].text, imported: name, module: module.String(), token: at})
				j++
				if !tokenIs(tokens, j, ",") {
					break
				}
				j++
			}
		case "import":
			for j := i + 1; tokenName(tokens, j); j++ {
				first := j
				module := tokens[j].text
				for tokenIs(tokens, j+1, ".") && tokenName(tokens, j+2) {
					module += "." + tokens[j+2].text
					j += 2
				}
				if tokenIs(tokens, j+1, "as") && tokenName(tokens, j+2) {
					out = append(out, scriptBinding{local: tokens[j+2].text, module: module, token: j + 2})
					j += 2
				} else {
					out = append(out, scriptBinding{local: tokens[first].text, module: tokens[first].text, token: first})
				}
				if !tokenIs(tokens, j+1, ",") {
					break
				}
				j++
			}
		}
	}
	return out
}

// javascriptImports lists the bindings of ES imports (default, named with
// aliases, namespace; TypeScript type-only imports marked) and of
// const|let|var X = require('m') and const|let|var { a, b: c } =
// require('m').
func javascriptImports(tokens []callToken) []scriptBinding {
	var out []scriptBinding
	for i, t := range tokens {
		if !t.name || t.depth != 0 || tokenIs(tokens, i-1, ".") {
			continue
		}
		switch t.text {
		case "import":
			if tokenIs(tokens, i+1, "(") || tokenIs(tokens, i+1, ".") {
				continue
			}
			j := i + 1
			typeOnly := false
			if tokenIs(tokens, j, "type") && !tokenIs(tokens, j+1, "from") && !tokenIs(tokens, j+1, ",") {
				typeOnly = true
				j++
			}
			var found []scriptBinding
			if tokenName(tokens, j) && !tokenIs(tokens, j, "from") {
				found = append(found, scriptBinding{local: tokens[j].text, imported: "default", token: j, typeOnly: typeOnly})
				j++
				if tokenIs(tokens, j, ",") {
					j++
				}
			}
			switch {
			case tokenIs(tokens, j, "*") && tokenIs(tokens, j+1, "as") && tokenName(tokens, j+2):
				found = append(found, scriptBinding{local: tokens[j+2].text, token: j + 2, typeOnly: typeOnly})
				j += 3
			case tokenIs(tokens, j, "{"):
				end := tokens[j].close
				for k := j + 1; k < end; k++ {
					typed := typeOnly
					if tokenIs(tokens, k, "type") && tokenName(tokens, k+1) && !tokenIs(tokens, k+1, "as") {
						typed = true
						k++
					}
					if !tokenName(tokens, k) {
						break
					}
					name, at := tokens[k].text, k
					if tokenIs(tokens, k+1, "as") && tokenName(tokens, k+2) {
						at = k + 2
						k += 2
					}
					found = append(found, scriptBinding{local: tokens[at].text, imported: name, token: at, typeOnly: typed})
					k++
					if !tokenIs(tokens, k, ",") {
						break
					}
				}
				j = end + 1
			}
			if !tokenIs(tokens, j, "from") || j+1 >= len(tokens) || !tokens[j+1].quoted {
				continue
			}
			for _, b := range found {
				b.module = tokens[j+1].text
				out = append(out, b)
			}
		case "const", "let", "var":
			j := i + 1
			var found []scriptBinding
			switch {
			case tokenName(tokens, j):
				found = append(found, scriptBinding{local: tokens[j].text, token: j})
				j++
			case tokenIs(tokens, j, "{"):
				end := tokens[j].close
				for k := j + 1; k < end; k++ {
					if !tokenName(tokens, k) {
						break
					}
					name, at := tokens[k].text, k
					if tokenIs(tokens, k+1, ":") && tokenName(tokens, k+2) {
						at = k + 2
						k += 2
					}
					found = append(found, scriptBinding{local: tokens[at].text, imported: name, token: at})
					k++
					if !tokenIs(tokens, k, ",") {
						break
					}
				}
				j = end + 1
			default:
				continue
			}
			if !tokenIs(tokens, j, "=") || !tokenIs(tokens, j+1, "require") || !tokenIs(tokens, j+2, "(") || j+3 >= len(tokens) ||
				!tokens[j+3].quoted || tokens[j+2].close != j+4 {
				continue
			}
			for _, b := range found {
				b.module = tokens[j+3].text
				out = append(out, b)
			}
		}
	}
	return out
}

// relativeSpecifier reports a JavaScript specifier naming a file of the
// project by path: "./" or "../" and no query, fragment or backslash.
func relativeSpecifier(module string) bool {
	return (strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../")) && !strings.ContainsAny(module, "?#\\")
}

// scriptFile reports a JavaScript or TypeScript file a lead may bind into.
func scriptFile(p string) bool {
	k := windowKind(p)
	return leadDefinitionLanguage(p) == "javascript" && (k == "code" || k == "test")
}

// leadScriptModule is the one file a relative specifier of caller names, ""
// when none or more than one does. An extensionless specifier tries each
// script extension and its directory index, as directModule does. A
// specifier with an output extension names that file when the snapshot holds
// it and no TypeScript source beside it (both are refused, as directModule
// refuses them); without it, TypeScript's mapping to the source picks the
// first that exists: .js to .ts then .tsx, .jsx to .tsx, .mjs to .mts, .cjs
// to .cts (a directory's index.js the same way).
func leadScriptModule(files map[string]source.Entry, caller, module string) string {
	if !relativeSpecifier(module) {
		return ""
	}
	base := path.Join(path.Dir(caller), module)
	exists := func(p string) bool { _, ok := files[p]; return ok && scriptFile(p) && p != caller }
	ext := path.Ext(base)
	sources := map[string][]string{".js": {".ts", ".tsx"}, ".jsx": {".tsx"}, ".mjs": {".mts"}, ".cjs": {".cts"}}
	if mapped, output := sources[ext]; output {
		stem := strings.TrimSuffix(base, ext)
		if exists(base) {
			for _, e := range mapped {
				if exists(stem + e) {
					return ""
				}
			}
			return base
		}
		for _, e := range mapped {
			if exists(stem + e) {
				return stem + e
			}
		}
		return ""
	}
	var candidates []string
	if ext == "" {
		if _, packageConfig := files[path.Join(base, "package.json")]; packageConfig {
			return ""
		}
		for _, e := range []string{".js", ".ts", ".mjs", ".mts", ".jsx", ".tsx", ".cjs", ".cts"} {
			candidates = append(candidates, base+e, path.Join(base, "index"+e))
		}
	} else {
		candidates = []string{base}
	}
	found := ""
	for _, p := range candidates {
		if exists(p) {
			if found != "" {
				return ""
			}
			found = p
		}
	}
	return found
}

// leadScriptModuleFiles lists every snapshot file a relative JavaScript or
// TypeScript specifier of caller may name: leadScriptModule's candidates,
// all of them even when more than one exists.
func leadScriptModuleFiles(files map[string]source.Entry, caller, module string) []string {
	if !relativeSpecifier(module) {
		return nil
	}
	base := path.Join(path.Dir(caller), module)
	stem, ext := strings.TrimSuffix(base, path.Ext(base)), path.Ext(base)
	var candidates []string
	switch ext {
	case "":
		for _, e := range []string{".js", ".ts", ".mjs", ".mts", ".jsx", ".tsx", ".cjs", ".cts"} {
			candidates = append(candidates, base+e, path.Join(base, "index"+e))
		}
	case ".js":
		candidates = []string{base, stem + ".ts", stem + ".tsx"}
	case ".jsx":
		candidates = []string{base, stem + ".tsx"}
	case ".mjs":
		candidates = []string{base, stem + ".mts"}
	case ".cjs":
		candidates = []string{base, stem + ".cts"}
	default:
		candidates = []string{base}
	}
	var out []string
	for _, p := range candidates {
		if _, ok := files[p]; ok && p != caller && scriptFile(p) {
			out = append(out, p)
		}
	}
	return out
}

// pythonModuleFiles lists the snapshot files a Python module name of caller
// may be, in directModule's order: relative to the caller's package, then
// (absolute names) from the project root and its src directory. A name may
// also be the package that holds it.
func pythonModuleFiles(files map[string]source.Entry, caller, module string) []string {
	dir := path.Dir(caller)
	relative := strings.HasPrefix(module, ".")
	for strings.HasPrefix(module, ".") {
		module = module[1:]
		if !strings.HasPrefix(module, ".") {
			break
		}
		if dir == "." {
			return nil
		}
		dir = path.Dir(dir)
	}
	var paths []string
	add := func(root string) {
		if module == "" {
			paths = append(paths, path.Join(root, "__init__.py"))
			return
		}
		base := path.Join(root, strings.ReplaceAll(module, ".", "/"))
		paths = append(paths, base+".py", path.Join(base, "__init__.py"))
	}
	add(dir)
	if !relative {
		for root := dir; ; root = path.Dir(root) {
			_, pyproject := files[path.Join(root, "pyproject.toml")]
			_, setup := files[path.Join(root, "setup.cfg")]
			if root == "." || pyproject || setup {
				if root != dir {
					add(root)
				}
				if src := path.Join(root, "src"); src != dir {
					add(src)
				}
				break
			}
		}
	}
	var out []string
	for _, p := range paths {
		if _, ok := files[p]; ok && p != caller {
			out = append(out, p)
		}
	}
	return out
}

// rustUse is one name a use declaration binds: path is the full path (a
// glob ends in "*"), local the bound name ("" for a glob), token the bound
// name's token (or the "*").
type rustUse struct {
	path  []string
	local string
	token int
}

// rustUses lists the bindings of the use declarations in tokens, nested
// trees, aliases, self and globs included.
func rustUses(tokens []callToken) []rustUse {
	var out []rustUse
	var tree func(prefix []string, at, end int)
	tree = func(prefix []string, at, end int) {
		parts := append([]string(nil), prefix...)
		for at < end {
			switch {
			case tokenIs(tokens, at, "{"):
				close := tokens[at].close
				for k := at + 1; k < close; {
					next := k
					for next < close && !(tokens[next].depth == tokens[at].depth+1 && tokenIs(tokens, next, ",")) {
						next++
					}
					tree(parts, k, next)
					k = next + 1
				}
				return
			case tokenIs(tokens, at, "*"):
				out = append(out, rustUse{path: append(parts, "*"), token: at})
				return
			case tokenName(tokens, at):
				parts = append(parts, tokens[at].text)
				if tokenIs(tokens, at+1, ":") && tokenIs(tokens, at+2, ":") {
					at += 3
					continue
				}
				local, token := tokens[at].text, at
				if tokenIs(tokens, at+1, "as") && tokenName(tokens, at+2) {
					local, token = tokens[at+2].text, at+2
				}
				if local == "self" && len(parts) > 1 {
					parts, local = parts[:len(parts)-1], parts[len(parts)-2]
				}
				out = append(out, rustUse{path: parts, local: local, token: token})
				return
			case tokenIs(tokens, at, ":") && tokenIs(tokens, at+1, ":"):
				at += 2
			default:
				return
			}
		}
	}
	for i := range tokens {
		if !tokenIs(tokens, i, "use") || tokenIs(tokens, i-1, ":") || tokenIs(tokens, i-1, ".") {
			continue
		}
		end := i + 1
		for end < len(tokens) && !(tokens[end].depth == tokens[i].depth && tokenIs(tokens, end, ";")) {
			end++
		}
		tree(nil, i+1, end)
	}
	return out
}

// rustCrate is where a Rust source sits in its crate: src the crate's src
// directory and module the file's module path ([] for lib.rs or main.rs).
func rustCrate(files map[string]source.Entry, p string) (src string, module []string, ok bool) {
	for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
		if path.Base(dir) != "src" {
			continue
		}
		if _, cargo := files[path.Join(path.Dir(dir), "Cargo.toml")]; !cargo {
			return "", nil, false
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, dir+"/"), ".rs")
		switch {
		case rel == "lib" || rel == "main":
			return dir, nil, true
		case strings.HasPrefix(rel, "bin/"):
			return "", nil, false
		}
		module = strings.Split(strings.TrimSuffix(rel, "/mod"), "/")
		return dir, module, true
	}
	return "", nil, false
}

// rustModuleFiles lists the files of a module path of the crate with src
// directory src: its name.rs and name/mod.rs, or lib.rs and main.rs for the
// crate root.
func rustModuleFiles(files map[string]source.Entry, src string, module []string) []string {
	var paths []string
	if len(module) == 0 {
		paths = []string{path.Join(src, "lib.rs"), path.Join(src, "main.rs")}
	} else {
		base := path.Join(append([]string{src}, module...)...)
		paths = []string{base + ".rs", path.Join(base, "mod.rs")}
	}
	var out []string
	for _, p := range paths {
		if _, ok := files[p]; ok {
			out = append(out, p)
		}
	}
	return out
}

// rustPathModules lists the module paths a use or call path's module part
// (path without its last name) may name from the module of the anchor:
// crate:: from the root, self:: and super:: from the anchor's module, and a
// plain first name as a child of the anchor's module or of the root.
func rustPathModules(anchor []string, parts []string) [][]string {
	if len(parts) == 0 {
		return [][]string{anchor}
	}
	switch parts[0] {
	case "crate":
		return [][]string{parts[1:]}
	case "self":
		return [][]string{append(append([]string(nil), anchor...), parts[1:]...)}
	case "super":
		base := append([]string(nil), anchor...)
		for len(parts) > 0 && parts[0] == "super" {
			if len(base) == 0 {
				return nil
			}
			base, parts = base[:len(base)-1], parts[1:]
		}
		return [][]string{append(base, parts...)}
	}
	return [][]string{append(append([]string(nil), anchor...), parts...), parts}
}

// nativeInclude is one #include of a C or C++ file: name is the header as
// written, quoted for "name" (else <name>), conditional when the line sits
// inside a #if, #ifdef or #ifndef block that is not the file's include guard.
type nativeInclude struct {
	name        string
	quoted      bool
	line        int
	conditional bool
}

// nativeDirective is one preprocessor line: its command (include, define,
// if, ...), the rest of the raw line, its line and whether it sits inside a
// conditional block other than the include guard.
type nativeDirective struct {
	command, rest string
	line          int
	conditional   bool
}

// nativeDirectives reads the preprocessor lines of C or C++ text (a "#"
// first on a line outside comments and strings, continuation lines joined
// to it) and marks the lines inside a conditional block (#if, #ifdef,
// #ifndef up to its #endif, the directive lines included). An include
// guard does not count as one: the first conditional directive is #ifndef X
// or #if !defined(X), the next directive defines X, its #endif is the last
// directive, and no code lies outside it.
func nativeDirectives(text string) ([]nativeDirective, []bool) {
	masked := strings.Split(contractExecutableSource(text, "c"), "\n")
	raw := strings.Split(text, "\n")
	var out []nativeDirective
	code := make([]bool, len(raw)+1)
	continued := false
	for i, line := range masked {
		trimmed := strings.TrimSpace(line)
		start := !continued && strings.HasPrefix(trimmed, "#")
		inside := continued || start
		continued = inside && strings.HasSuffix(strings.TrimSpace(raw[i]), "\\")
		if !inside {
			code[i+1] = trimmed != ""
			continue
		}
		if !start {
			continue
		}
		body := strings.TrimLeft(strings.TrimSpace(raw[i])[1:], " \t")
		command := body
		if end := strings.IndexFunc(command, func(r rune) bool { return r < 'a' || r > 'z' }); end >= 0 {
			command = command[:end]
		}
		out = append(out, nativeDirective{command: command, rest: strings.TrimSpace(body[len(command):]), line: i + 1})
	}
	guardOpen, guardClose := -1, -1
	for k, d := range out {
		if d.command != "if" && d.command != "ifdef" && d.command != "ifndef" {
			continue
		}
		name := guardName(d)
		if name != "" && k+1 < len(out) && out[k+1].command == "define" && firstWord(out[k+1].rest) == name {
			depth := 0
			for m := k; m < len(out); m++ {
				switch out[m].command {
				case "if", "ifdef", "ifndef":
					depth++
				case "endif":
					depth--
				}
				if depth == 0 {
					if m == len(out)-1 && !slices.Contains(code[:d.line], true) && !slices.Contains(code[out[m].line:], true) {
						guardOpen, guardClose = k, m
					}
					break
				}
			}
		}
		break
	}
	conditional := make([]bool, len(raw)+1)
	depth, at := 0, 0
	for n := 1; n <= len(raw); n++ {
		mark := depth > 0
		for at < len(out) && out[at].line == n {
			switch out[at].command {
			case "if", "ifdef", "ifndef":
				if at != guardOpen {
					depth++
					mark = true
				}
			case "endif":
				if at != guardClose {
					mark = mark || depth > 0
					depth--
				}
			}
			out[at].conditional = mark || depth > 0
			at++
		}
		conditional[n] = mark || depth > 0
	}
	return out, conditional
}

// guardName is X of #ifndef X or #if !defined(X) / #if !defined X.
func guardName(d nativeDirective) string {
	switch d.command {
	case "ifndef":
		return firstWord(d.rest)
	case "if":
		rest := strings.TrimSpace(strings.TrimPrefix(d.rest, "!"))
		if rest == d.rest {
			return ""
		}
		rest, ok := strings.CutPrefix(rest, "defined")
		if !ok {
			return ""
		}
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
			rest = strings.TrimSpace(rest[1 : len(rest)-1])
		}
		if strings.ContainsAny(rest, " \t()&|!") {
			return ""
		}
		return rest
	}
	return ""
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// nativeIncludes lists the #include lines of C or C++ text.
func nativeIncludes(directives []nativeDirective) []nativeInclude {
	var out []nativeInclude
	for _, d := range directives {
		if d.command != "include" || len(d.rest) < 2 {
			continue
		}
		open, close := d.rest[0], byte('"')
		if open == '<' {
			close = '>'
		} else if open != '"' {
			continue
		}
		end := strings.IndexByte(d.rest[1:], close)
		if end <= 0 {
			continue
		}
		out = append(out, nativeInclude{name: d.rest[1 : 1+end], quoted: open == '"', line: d.line, conditional: d.conditional})
	}
	return out
}

// nativeHeader is the snapshot file a quoted include of p names relative to
// p's directory (the first place a compiler looks), "" when none.
func nativeHeader(files map[string]source.Entry, p string, inc nativeInclude) string {
	if !inc.quoted || path.IsAbs(inc.name) {
		return ""
	}
	h := path.Join(path.Dir(p), inc.name)
	if strings.HasPrefix(h, "../") || h == ".." {
		return ""
	}
	if _, ok := files[h]; !ok || leadDefinitionLanguage(h) == "" {
		return ""
	}
	return h
}

// nativeIncludeFiles lists the snapshot files an include of p may name: the
// quoted header beside p, else (and for <name>) any C or C++ file whose path
// ends in the name (an include directory of the build).
func nativeIncludeFiles(c *sourceCallCatalog, p string, inc nativeInclude) []string {
	if h := nativeHeader(c.files, p, inc); h != "" {
		return []string{h}
	}
	var out []string
	for _, q := range c.ordered {
		if (q == inc.name || strings.HasSuffix(q, "/"+inc.name)) && leadDefinitionLanguage(q) != "" {
			out = append(out, q)
		}
	}
	return out
}
