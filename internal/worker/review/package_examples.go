package review

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type packageUse struct {
	name, callable, exported string
	require                  bool
}

type packageUsePassage struct {
	packageUse
	start, end int
}

type packageExampleBinding struct {
	name, exported string
	token          int
	namespace      bool
	memberOnly     bool
}

type packageExampleImport struct {
	name     string
	bindings []packageExampleBinding
	end      int
	require  bool
}

func packageExampleImportAt(tokens []callToken, i int) packageExampleImport {
	var out packageExampleImport
	if tokens[i].depth != 0 || (i > 0 && tokens[i-1].endLine == tokens[i].line && !tokenIs(tokens, i-1, ";")) {
		return out
	}
	var first, module, last int
	switch tokens[i].text {
	case "import":
		first, last = i+1, i+1
		if tokenIs(tokens, first, "{") {
			last = tokens[first].close
		} else if tokenIs(tokens, first, "*") && tokenIs(tokens, first+1, "as") {
			last = first + 2
		}
		if !tokenIs(tokens, last+1, "from") {
			return out
		}
		module, out.end = last+2, last+2
	case "const", "let", "var":
		first, last = i+1, i+1
		if tokenIs(tokens, first, "{") {
			last = tokens[first].close
		}
		if !tokenIs(tokens, last+1, "=") || !tokenIs(tokens, last+2, "require") || !tokenIs(tokens, last+3, "(") || !tokenIs(tokens, last+5, ")") {
			return out
		}
		module, out.end, out.require = last+4, last+5, true
	default:
		return out
	}
	if module >= len(tokens) || !tokens[module].quoted || !record.ValidPackageName(tokens[module].text) ||
		(out.end+1 < len(tokens) && tokens[out.end+1].line == tokens[out.end].endLine && !tokenIs(tokens, out.end+1, ";")) {
		return packageExampleImport{}
	}
	out.name = tokens[module].text
	if tokenIs(tokens, first, "{") {
		for j := first + 1; j < last; j++ {
			if !tokenName(tokens, j) {
				return packageExampleImport{}
			}
			exported := tokens[j].text
			if (!out.require && tokenIs(tokens, j+1, "as")) || (out.require && tokenIs(tokens, j+1, ":")) {
				j += 2
				if j >= last || !tokenName(tokens, j) {
					return packageExampleImport{}
				}
			}
			out.bindings = append(out.bindings, packageExampleBinding{name: tokens[j].text, exported: exported, token: j})
			if j+1 < last && !tokenIs(tokens, j+1, ",") {
				return packageExampleImport{}
			}
			j++
		}
	} else if tokenName(tokens, last) {
		out.bindings = []packageExampleBinding{{name: tokens[last].text, exported: "default", token: last,
			namespace: true, memberOnly: first != last}}
	}
	return out
}

func packageExampleImports(tokens []callToken) []packageExampleImport {
	var imports []packageExampleImport
	for i, token := range tokens {
		if !token.name {
			continue
		}
		if imp := packageExampleImportAt(tokens, i); len(imp.bindings) > 0 {
			imports = append(imports, imp)
		}
	}
	return imports
}

func packageExampleCalls(tokens []callToken, imp packageExampleImport) []packageUse {
	if imp.require {
		for i, t := range tokens {
			if t.name && t.text == "require" && i != imp.end-3 && !isDirectCall(tokens, i) {
				return nil
			}
		}
	}
	var out []packageUse
	for _, b := range imp.bindings {
		var uses []packageUse
		for i, t := range tokens {
			if !t.name || t.text != b.name || i == b.token {
				continue
			}
			u, ok := packageExampleCall(tokens, i, imp, b, false)
			if !ok || i <= imp.end || t.line-tokens[imp.end].line > 12 {
				// Another occurrence could replace or shadow the binding.
				uses = nil
				break
			}
			uses = append(uses, u)
		}
		out = append(out, uses...)
	}
	return out
}

func packageExampleCall(tokens []callToken, i int, imp packageExampleImport, b packageExampleBinding, inline bool) (packageUse, bool) {
	u := packageUse{name: imp.name, callable: b.name, exported: b.exported, require: imp.require}
	open := i + 1
	if b.namespace && tokenIs(tokens, i+1, ".") && tokenName(tokens, i+2) {
		u.exported = tokens[i+2].text
		u.callable += "." + u.exported
		open = i + 3
	} else if b.memberOnly {
		return packageUse{}, false
	}
	if !tokenIs(tokens, open, "(") || tokenIs(tokens, tokens[open].close+1, "{") || tokenIs(tokens, i-1, ".") ||
		tokenIs(tokens, i-1, "function") || tokenIs(tokens, i-1, "class") {
		return packageUse{}, false
	}
	if inline && tokens[open].close != len(tokens)-1 {
		return packageUse{}, false
	}
	return u, true
}

func inlinePackageCall(raw string, imp packageExampleImport) []packageUse {
	var out []packageUse
	for _, m := range witnessCode.FindAllStringSubmatch(raw, -1) {
		tokens := callTokens(m[1], false)
		if len(tokens) == 0 {
			continue
		}
		for _, b := range imp.bindings {
			if !tokenIs(tokens, 0, b.name) {
				continue
			}
			if u, ok := packageExampleCall(tokens, 0, imp, b, true); ok {
				out = append(out, u)
			}
		}
	}
	return out
}

// documentedPackageUse considers complete JS/TS examples and explicit
// inline-code imports in the exact excerpt. Reading the preceding lines
// only tracks Markdown fence and comment state at the excerpt boundary.
func documentedPackageUses(lines []string, start, end int) []packageUsePassage {
	var uses []packageUsePassage
	add := func(calls []packageUse, from, through int) {
		for _, u := range calls {
			uses = append(uses, packageUsePassage{packageUse: u, start: from + 1, end: through + 1})
		}
	}
	var fence string
	var open, inlineAt, inlineStart int
	var js, comment bool
	var inline packageExampleImport
	for i, raw := range lines[:end] {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == "" {
				if js && open >= start-1 {
					tokens := callTokens(strings.Join(lines[open+1:i], "\n"), false)
					imports := packageExampleImports(tokens)
					for _, imp := range imports {
						add(packageExampleCalls(tokens, imp), open, i)
					}
					if len(imports) == 1 && imports[0].end >= len(tokens)-2 {
						inline, inlineAt, inlineStart = imports[0], i, open
					}
				}
				fence = ""
			}
			continue
		}
		if comment || strings.Contains(line, "<!--") {
			comment = !strings.Contains(line, "-->")
			inline = packageExampleImport{}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fence = line[:len(line)-len(strings.TrimLeft(line, line[:1]))]
			lang, _, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, fence)), " ")
			switch strings.ToLower(lang) {
			case "js", "javascript", "ts", "typescript", "jsx", "tsx", "mjs", "cjs":
				js = true
			default:
				js = false
			}
			open = i
			inline = packageExampleImport{}
			continue
		}
		if i < start-1 || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") || i-inlineAt > 4 {
			inline = packageExampleImport{}
		}
		if i < start-1 || strings.HasPrefix(line, ">") || strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		if inline.name != "" {
			add(inlinePackageCall(line, inline), inlineStart, i)
		}
		for _, m := range witnessCode.FindAllStringSubmatchIndex(line, -1) {
			tokens := callTokens(line[m[2]:m[3]], false)
			imports := packageExampleImports(tokens)
			if len(imports) == 1 && imports[0].end >= len(tokens)-2 {
				inline, inlineAt, inlineStart = imports[0], i, i
				add(inlinePackageCall(line[m[1]:], inline), i, i)
			}
		}
	}
	return uses
}
