package review

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var sphinxAPI = regexp.MustCompile(`^\.\. (currentmodule|automodule|autofunction|automethod)::[ \t]+([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*){0,7})[ \t]*$`)
var pythonModule = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*){0,7}$`)

func documentedPythonTargets(page, text string) []string {
	var targets []string
	add := func(target string) {
		if len(targets) < 20 && len(target) <= 200 && pythonModule.MatchString(target) && !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	switch {
	case record.SphinxDirectivePath(page):
		module := ""
		for _, line := range strings.Split(text, "\n") {
			m := sphinxAPI.FindStringSubmatch(strings.TrimRight(line, "\r"))
			if m == nil {
				continue
			}
			switch m[1] {
			case "automodule", "currentmodule":
				module = m[2]
			case "autofunction":
				if !strings.Contains(m[2], ".") && module != "" {
					add(module + "." + m[2])
				} else if strings.Contains(m[2], ".") {
					add(m[2])
				}
			case "automethod":
				switch strings.Count(m[2], ".") {
				case 1:
					if module != "" {
						add(module + "." + m[2])
					}
				case 0:
				default:
					add(m[2])
				}
			}
		}
	case record.MarkdownDirectivePath(page):
		for _, block := range apiFences(text) {
			if block.lang != "python" && block.lang != "py" {
				continue
			}
			for _, call := range importedCalls(callTokens(block.text, true), true) {
				if !strings.HasPrefix(call.module, ".") {
					add(call.module + "." + call.symbol)
				}
			}
		}
	}
	return targets
}

// Resolve the longest explicit module prefix. A direct literal re-export can
// lead to a function in another file, and conflicting source paths stop the
// lookup even when one of the alternatives is unavailable to the reviewer.
func pythonDocumentedObject(c *sourceCalls, scope, target string) (source.Entry, []string, string, error) {
	e, object := pythonDocSource(c.files, scope, target)
	for range 4 {
		if !productionAPISource(e, scope, ".py") || len(object) == 0 {
			break
		}
		text, ok, err := c.text(e.Path)
		if !ok || err != nil {
			return source.Entry{}, nil, "", err
		}
		tokens := documentationTokens(text, true)
		if pythonDynamicBindings(tokens) {
			break
		}
		if d, ok := pythonObjectDeclaration(tokens, object); ok && pythonStableObject(tokens, object, d) {
			return e, object, text, nil
		}
		if len(object) != 1 {
			break
		}
		b, ok := scriptReexport(tokens, object[0], true)
		if !ok {
			break
		}
		p := directModule(c.files, e.Path, b.module, true)
		e, object = c.files[p], []string{b.symbol}
	}
	return source.Entry{}, nil, "", nil
}

func pythonStableObject(tokens []callToken, object []string, declaration pythonDocDeclaration) bool {
	// A decorator can replace the named object. Limit authored API links to
	// direct function declarations and plain methods of an ordinary class.
	found := false
	for i, t := range tokens {
		if !t.name || t.text != object[len(object)-1] || t.depth != 0 {
			continue
		}
		if tokenIs(tokens, i-1, "def") && t.line == declaration.line {
			found = true
			if declaration.class {
				return false
			}
			first := i - 1
			if tokenIs(tokens, first-1, "async") && tokens[first-1].line == t.line {
				first--
			}
			for at := first - 1; at >= 0; at-- {
				if tokens[at].depth == 0 && tokenIs(tokens, at, "@") && tokens[at].column == declaration.indent {
					return false
				}
				if tokens[at].depth == 0 && tokens[at].column <= declaration.indent &&
					(at == 0 || tokens[at-1].endLine < tokens[at].line) {
					break
				}
			}
			continue
		}
		if (t.column == declaration.indent && (tokenIs(tokens, i+1, "=") || tokenIs(tokens, i+1, ":"))) ||
			(len(object) == 1 && (tokenIs(tokens, i-1, "as") || tokenIs(tokens, i-1, "import"))) {
			return false
		}
	}
	return found
}

type pythonAssertionImport struct {
	local, module, name string
	at, first, last     int
}

func pythonAssertionImports(tokens []callToken) []pythonAssertionImport {
	var out []pythonAssertionImport
	for local, bindings := range importedBindings(tokens, true) {
		if len(bindings) == 1 {
			b := bindings[0]
			out = append(out, pythonAssertionImport{local, b.module, b.symbol, b.token, b.token, b.token})
		}
	}
	for i, t := range tokens {
		if !tokenIs(tokens, i, "import") || t.column != 0 || t.depth != 0 || !tokenName(tokens, i+1) {
			continue
		}
		at := i + 1
		var module strings.Builder
		module.WriteString(tokens[at].text)
		for tokenIs(tokens, at+1, ".") && tokenName(tokens, at+2) {
			at += 2
			module.WriteByte('.')
			module.WriteString(tokens[at].text)
		}
		if tokenIs(tokens, at+1, "as") && tokenName(tokens, at+2) {
			at += 2
		} else if at != i+1 {
			continue // import a.b binds a, not a.b
		}
		if at+1 < len(tokens) && tokens[at+1].line == t.line {
			continue
		}
		out = append(out, pythonAssertionImport{tokens[at].text, module.String(), "", at, i, at})
	}
	slices.SortFunc(out, func(a, b pythonAssertionImport) int { return a.at - b.at })
	return out
}

func linkedPythonAssertion(c *sourceCalls, entry source.Entry, text string, q witnessQuery) (contractWitness, error) {
	if len(q.pythonDocs) == 0 || path.Ext(entry.Path) != ".py" || !record.IsTestPath(entry.Path) || !record.ScopeUnder(entry.Path, q.scope) {
		return contractWitness{}, nil
	}
	tokens := documentationTokens(text, true)
	if pythonDynamicBindings(tokens) {
		return contractWitness{}, nil
	}
	imports := pythonAssertionImports(tokens)
	count := map[string]int{}
	for _, b := range imports {
		count[b.local]++
	}
	lines := strings.Split(text, "\n")
	var best contractWitness
	for _, b := range imports {
		if count[b.local] != 1 {
			continue
		}
		var sites []int
		for i, t := range tokens {
			if !t.name || t.text != b.local || i >= b.first && i <= b.last {
				continue
			}
			leaf := i
			if b.name == "" && tokenIs(tokens, i+1, ".") && tokenName(tokens, i+2) {
				leaf = i + 2
			}
			if tokenIs(tokens, i-1, ".") || !tokenIs(tokens, leaf+1, "(") || tokenIs(tokens, i-1, "def") {
				sites = nil
				break
			}
			sites = append(sites, leaf)
		}
		for _, at := range sites {
			start, end := pythonAssertionRange(tokens, at)
			if start == 0 || !fitsWitness(lines, start, end) {
				continue
			}
			symbol := b.name
			if symbol == "" {
				symbol = tokens[at].text
			}
			sourceFile, object, sourceText, err := pythonDocumentedObject(c, q.scope, b.module+"."+symbol)
			if err != nil {
				return contractWitness{}, err
			}
			if !documentedPythonObject(sourceFile, sourceText, object, q.pythonDocs[sourceFile.Path]) {
				continue
			}
			score := 3*witnessMinScore + min(witnessMinScore, witnessMatch(witnessTerms(strings.Join(lines[start-1:end], "\n")), q.terms))
			if score > best.score {
				best = contractWitness{entry: entry, line: start, end: end, score: score, direct: true}
			}
		}
	}
	return best, nil
}

func documentedPythonObject(e source.Entry, content string, object []string, docs []record.Excerpt) bool {
	if len(docs) == 0 {
		return false
	}
	tokens := documentationTokens(content, true)
	d, ok := pythonObjectDeclaration(tokens, object)
	if !ok {
		return false
	}
	r := pythonDocstring(tokens, d.body)
	return r.start > 0 && slices.ContainsFunc(docs, func(doc record.Excerpt) bool {
		return doc.SHA256 == e.SHA256 && r.start <= doc.End && doc.End <= r.end && d.line <= doc.Start
	})
}

func pythonAssertionRange(tokens []callToken, at int) (int, int) {
	end := tokens[tokens[at+1].close].endLine
	first := at
	for parent := tokens[at].parent; parent >= 0; parent = tokens[parent].parent {
		if parent == 0 || !tokenIs(tokens, parent, "(") {
			continue
		}
		switch tokens[parent-1].text {
		case "assertEqual", "assertNotEqual", "assertTrue", "assertFalse", "assertIs", "assertIsNone", "assertIn":
			if tokenIs(tokens, parent-2, ".") && tokenIs(tokens, parent-3, "self") {
				return tokens[parent-3].line, tokens[tokens[parent].close].endLine
			}
		}
		first = parent
	}
	for i := first; i >= 0 && tokens[i].line == tokens[first].line; i-- {
		if tokens[i].depth == 0 && tokenIs(tokens, i, "assert") && (i == 0 || tokens[i-1].endLine < tokens[i].line) {
			for index := tokens[at+1].close + 1; index < len(tokens) && tokens[index].line <= end; index++ {
				end = max(end, tokens[index].endLine)
			}
			return tokens[i].line, end
		}
	}
	return 0, 0
}
