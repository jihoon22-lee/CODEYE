package review

import (
	"slices"
	"strings"
	"unicode"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type javaImport struct {
	class, member string
	token         int
}

func javaQualifiedName(tokens []callToken, start int) ([]string, int) {
	var names []string
	for at := start; tokenName(tokens, at); at += 2 {
		names = append(names, tokens[at].text)
		if !tokenIs(tokens, at+1, ".") {
			return names, at + 1
		}
	}
	return nil, start
}

func javaImports(tokens []callToken) []javaImport {
	var out []javaImport
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "import") {
			continue
		}
		start := i + 1
		static := tokenIs(tokens, start, "static")
		if static {
			start++
		}
		names, end := javaQualifiedName(tokens, start)
		if !tokenIs(tokens, end, ";") || len(names) < 2 {
			return nil // includes on-demand imports, which can shadow these names
		}
		entry := javaImport{token: end - 1}
		if static {
			if len(names) < 3 {
				return nil
			}
			entry.member = names[len(names)-1]
			names = names[:len(names)-1]
		}
		entry.class = strings.Join(names, ".")
		out = append(out, entry)
	}
	return out
}

func javaTokens(text string) []callToken {
	// Java Unicode escapes are processed before tokenization, including in
	// comments. Text blocks also need a different string lexer.
	if strings.Contains(text, `\u`) || strings.Contains(text, `"""`) {
		return nil
	}
	return callTokens(text, false)
}

func (c *sourceCalls) javaCalls(p, scope string) ([]sourceCall, error) {
	text, ok, err := c.text(p)
	if !ok || err != nil {
		return nil, err
	}
	tokens := javaTokens(text)
	imports := javaImports(tokens)
	out := javaLocalCalls(p, strings.Split(text, "\n"), tokens)
	for _, imp := range imports {
		className := imp.class[strings.LastIndexByte(imp.class, '.')+1:]
		qualified := imp.member == ""
		used := className
		if !qualified {
			used = imp.member
		}
		uses := []int{}
		ambiguous := false
		for i, t := range tokens {
			if tokenIs(tokens, i, "extends") || tokenIs(tokens, i, "implements") {
				ambiguous = true // inherited fields or methods can shadow the binding
				break
			}
			if t.depth >= 1 && (tokenIs(tokens, i, "class") || tokenIs(tokens, i, "record") || tokenIs(tokens, i, "enum") || tokenIs(tokens, i, "interface")) {
				ambiguous = true // enclosing type or receiver can change
				break
			}
			if !t.name || t.text != used || i == imp.token {
				continue
			}
			if t.depth < 2 || tokenIs(tokens, i-1, ".") {
				ambiguous = true
				break
			}
			if qualified && tokenIs(tokens, i+1, ".") && tokenName(tokens, i+2) && tokenIs(tokens, i+3, "(") {
				uses = append(uses, i+2)
			} else if !qualified && isDirectCall(tokens, i) && !tokenIs(tokens, tokens[i+1].close+1, "{") {
				uses = append(uses, i)
			} else {
				ambiguous = true // includes every additional declaration or assignment of the spelling
				break
			}
		}
		if ambiguous || len(uses) == 0 {
			continue
		}
		target := ""
		suffix := strings.ReplaceAll(imp.class, ".", "/") + ".java"
		for _, candidate := range c.ordered {
			if candidate == suffix || strings.HasSuffix(candidate, "/"+suffix) {
				if target != "" {
					target = ""
					break
				}
				target = candidate
			}
		}
		if target == "" || target == p || !record.ScopeUnder(target, scope) {
			continue
		}
		definition, ok, err := c.text(target)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		decls := javaTokens(definition)
		body := javaClass(decls, imp.class)
		if body == 0 {
			continue
		}
		lines := strings.Split(definition, "\n")
		for _, i := range uses {
			start, end := javaMethod(decls, body, tokens[i].text, javaArgumentTypes(tokens, i+1))
			if start == 0 || !fitsWitness(lines, start, end) {
				continue
			}
			from := i
			if qualified {
				from -= 2
			}
			out = append(out, sourceCall{caller: sourceCallLocation{p, tokens[from].line, tokens[tokens[i+1].close].line},
				callee: sourceCallLocation{target, start, end}, symbol: tokens[i].text})
			if len(out) >= callMaxSites {
				return nil, nil
			}
		}
	}
	return out, nil
}

func javaClass(tokens []callToken, qualified string) int {
	dot := strings.LastIndexByte(qualified, '.')
	if dot < 0 {
		return 0
	}
	packageName, className := qualified[:dot], qualified[dot+1:]
	pkg, body := "", 0
	for i, t := range tokens {
		if t.depth != 0 {
			continue
		}
		if tokenIs(tokens, i, "package") {
			names, end := javaQualifiedName(tokens, i+1)
			if pkg != "" || !tokenIs(tokens, end, ";") {
				return 0
			}
			pkg = strings.Join(names, ".")
		}
		if tokenIs(tokens, i, "class") && tokenIs(tokens, i+1, className) {
			first := i - 1
			for tokenIs(tokens, first, "final") || tokenIs(tokens, first, "abstract") {
				first--
			}
			if body != 0 || !tokenIs(tokens, first, "public") || !tokenIs(tokens, i+2, "{") {
				return 0
			}
			body = i + 2
		}
	}
	if pkg != packageName {
		return 0
	}
	return body
}

func javaArguments(tokens []callToken, open int) [][]callToken {
	var parts [][]callToken
	start := open + 1
	for i := start; i < tokens[open].close; i++ {
		if tokenIs(tokens, i, ",") && tokens[i].parent == open {
			parts = append(parts, tokens[start:i])
			start = i + 1
		}
	}
	if start < tokens[open].close {
		parts = append(parts, tokens[start:tokens[open].close])
	}
	return parts
}

func javaArgumentTypes(tokens []callToken, open int) []string {
	var types []string
	for _, arg := range javaArguments(tokens, open) {
		typ := ""
		if len(arg) == 1 {
			switch {
			case arg[0].quoted && !arg[0].character:
				typ = "String"
			case arg[0].quoted && arg[0].character:
				typ = "char"
			case tokenIs(arg, 0, "true") || tokenIs(arg, 0, "false"):
				typ = "boolean"
			}
		}
		if typ == "" && len(arg) > 0 {
			last := len(arg)
			numericType := "int"
			if tokenIs(arg, last-1, "L") || tokenIs(arg, last-1, "l") {
				numericType, last = "long", last-1
			}
			if last > 0 && !slices.ContainsFunc(arg[:last], func(t callToken) bool {
				return t.quoted || len(t.text) != 1 || !unicode.IsDigit(rune(t.text[0]))
			}) {
				typ = numericType
			}
		}
		types = append(types, typ)
	}
	return types
}

func javaMethod(tokens []callToken, class int, symbol string, args []string) (start, end int) {
	type method struct {
		start, end int
		types      []string
	}
	var available []method
	for i := class + 1; i < tokens[class].close; i++ {
		t := tokens[i]
		if t.parent != class || !tokenIs(tokens, i, symbol) || !tokenIs(tokens, i+1, "(") || !tokenName(tokens, i-1) {
			continue
		}
		close := tokens[i+1].close
		body := close + 1
		if !tokenIs(tokens, body, "{") {
			return 0, 0 // qualified signatures, throws and abstract variants require more context
		}
		first := i - 1
		public, static := false, false
		for first > class+1 && tokenName(tokens, first-1) && tokens[first-1].parent == class {
			first--
			public = public || tokenIs(tokens, first, "public")
			static = static || tokenIs(tokens, first, "static")
		}
		if tokenIs(tokens, first-1, "@") || !public || !static {
			return 0, 0
		}
		var types []string
		for _, part := range javaArguments(tokens, i+1) {
			if len(part) != 2 || !part[0].name || !part[1].name {
				return 0, 0 // includes varargs, arrays and generic type syntax
			}
			types = append(types, part[0].text)
		}
		if len(types) == len(args) {
			available = append(available, method{tokens[first].line, tokens[tokens[body].close].line, types})
		}
	}
	if len(available) == 1 {
		for i, actual := range args {
			if actual != "" && actual != available[0].types[i] {
				return 0, 0
			}
		}
		return available[0].start, available[0].end
	}
	if slices.Contains(args, "") {
		return 0, 0
	}
	for _, m := range available {
		if slices.Equal(m.types, args) {
			if start != 0 {
				return 0, 0
			}
			start, end = m.start, m.end
		}
	}
	return start, end
}
