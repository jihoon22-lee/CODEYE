package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func (c *sourceCalls) cCalls(p, scope string) ([]sourceCall, error) {
	text, ok, err := c.text(p)
	if !ok || err != nil {
		return nil, err
	}
	tokens := callTokens(text, false)
	header := ""
	for i, t := range tokens {
		if !tokenIs(tokens, i, "#") {
			continue
		}
		if header != "" || t.depth != 0 || !tokenIs(tokens, i+1, "include") || i+2 >= len(tokens) ||
			!tokens[i+2].quoted || tokens[i+2].character || tokens[i+2].line != t.line {
			return nil, nil // other headers and preprocessing can change the binding
		}
		name := tokens[i+2].text
		switch path.Ext(name) {
		case ".h", ".hh", ".hpp", ".hxx":
		default:
			return nil, nil
		}
		if path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") {
			return nil, nil
		}
		header = path.Join(path.Dir(p), name)
	}
	if header == "" || !record.ScopeUnder(header, scope) {
		return nil, nil
	}
	headerText, ok, err := c.text(header)
	if !ok || err != nil {
		return nil, err
	}
	headerTokens := callTokens(headerText, false)
	for i, t := range headerTokens {
		if tokenIs(headerTokens, i, "#") && !(tokenIs(headerTokens, i+1, "pragma") && tokenIs(headerTokens, i+2, "once") &&
			i+2 < len(headerTokens) && headerTokens[i+2].line == t.line) {
			return nil, nil
		}
	}
	sites := map[string][]int{}
	bad := map[string]bool{}
	for i, t := range tokens {
		if !t.name {
			continue
		}
		// A return statement makes the call and its use explicit. Any
		// other occurrence may be a local declaration or macro binding.
		if t.depth > 0 && tokenIs(tokens, i-1, "return") && tokenIs(tokens, i+1, "(") && tokenIs(tokens, tokens[i+1].close+1, ";") {
			sites[t.text] = append(sites[t.text], i)
		} else {
			bad[t.text] = true
		}
	}
	wanted := map[string]bool{}
	for name := range sites {
		wanted[name] = !bad[name]
	}
	definitions := namedCDefinitions(headerText, wanted, true)
	lines := strings.Split(headerText, "\n")
	var out []sourceCall
	for _, d := range definitions {
		if d.conditional || !cStatic(d.returnType) || !slices.Contains(strings.Fields(d.returnType), "inline") || !fitsWitness(lines, d.start, d.end) {
			continue
		}
		count := 0
		for _, t := range headerTokens {
			if t.name && t.text == d.name {
				count++
			}
		}
		if count != 1 {
			continue
		}
		for _, at := range sites[d.name] {
			out = append(out, sourceCall{caller: sourceCallLocation{p, tokens[at].line, tokens[tokens[at+1].close].line},
				callee: sourceCallLocation{header, d.start, d.end}, symbol: d.name})
		}
	}
	return out, nil
}

func rustTokens(content string) []callToken {
	tokens := callTokens(content, false)
	for i := range tokens {
		if tokenIs(tokens, i, "#") || (tokenIs(tokens, i, "!") && tokenName(tokens, i-1) && !tokenIs(tokens, i+1, "=")) {
			return nil // attributes, macros and alternate module paths need build context
		}
	}
	return tokens
}

type rustImport struct {
	parts []string
	at    int
}

func rustLocalImports(tokens []callToken) []rustImport {
	var out []rustImport
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "use") {
			continue
		}
		if tokenIs(tokens, i-1, "pub") {
			return nil
		}
		if !tokenIs(tokens, i+1, "crate") {
			return nil // another import can add or shadow the local name
		}
		var parts []string
		at := i + 1
		for tokenIs(tokens, at+1, ":") && tokenIs(tokens, at+2, ":") && tokenName(tokens, at+3) {
			at += 3
			parts = append(parts, tokens[at].text)
		}
		if tokenIs(tokens, at+1, "as") && tokenName(tokens, at+2) {
			at += 2
		}
		if !tokenIs(tokens, at+1, ";") || len(parts) < 2 {
			return nil
		}
		out = append(out, rustImport{parts, at})
	}
	return out
}

func (c *sourceCalls) rustRoot(p string) (string, string) {
	for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
		if path.Base(dir) != "src" {
			continue
		}
		if _, hasCargo := c.files[path.Join(path.Dir(dir), "Cargo.toml")]; !hasCargo {
			return "", ""
		}
		lib, main := path.Join(dir, "lib.rs"), path.Join(dir, "main.rs")
		if p == lib || p == main {
			return dir, p
		}
		_, hasLib := c.files[lib]
		_, hasMain := c.files[main]
		if hasLib == hasMain {
			return "", ""
		}
		if hasLib {
			return dir, lib
		}
		return dir, main
	}
	return "", ""
}

func rustModule(tokens []callToken, name string) bool {
	matches := 0
	for i, t := range tokens {
		if t.depth == 0 && tokenIs(tokens, i, "mod") && tokenIs(tokens, i+1, name) {
			if !tokenIs(tokens, i+2, ";") {
				return false
			}
			matches++
		}
	}
	return matches == 1
}

func (c *sourceCalls) rustModules(dir, root, scope string, parts []string) (string, error) {
	p := root
	for _, module := range parts {
		text, ok, err := c.text(p)
		if !ok || err != nil || !rustModule(rustTokens(text), module) {
			return "", err
		}
		dir = path.Join(dir, module)
		file, nested := dir+".rs", path.Join(dir, "mod.rs")
		_, hasFile := c.files[file]
		_, hasNested := c.files[nested]
		if hasFile == hasNested {
			return "", nil
		}
		p = nested
		if hasFile {
			p = file
		}
		if !record.ScopeUnder(p, scope) {
			return "", nil
		}
	}
	return p, nil
}

func (c *sourceCalls) rustCalls(p, scope string) ([]sourceCall, error) {
	text, ok, err := c.text(p)
	if !ok || err != nil {
		return nil, err
	}
	tokens := rustTokens(text)
	imports := rustLocalImports(tokens)
	if len(imports) == 0 {
		return nil, nil
	}
	dir, root := c.rustRoot(p)
	if root == "" || !record.ScopeUnder(root, scope) {
		return nil, nil
	}
	manifest, ok, err := c.text(path.Join(path.Dir(dir), "Cargo.toml"))
	if !ok || err != nil {
		return nil, err
	}
	if _, hasBuild := c.files[path.Join(path.Dir(dir), "build.rs")]; hasBuild {
		return nil, nil
	}
	for line := range strings.SplitSeq(manifest, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[lib]") || strings.HasPrefix(line, "[[bin]]") {
			return nil, nil // Cargo can replace the conventional source root
		}
		key, _, _ := strings.Cut(line, "=")
		if slices.Contains([]string{"build", "autobins", "autolib"}, strings.Trim(strings.TrimSpace(key), `"'`)) {
			return nil, nil
		}
	}
	if p != root {
		rel := strings.TrimPrefix(p, dir+"/")
		rel = strings.TrimSuffix(strings.TrimSuffix(rel, "/mod.rs"), ".rs")
		bound, err := c.rustModules(dir, root, scope, strings.Split(rel, "/"))
		if err != nil || bound != p {
			return nil, err
		}
	}
	var out []sourceCall
	for _, imp := range imports {
		local := tokens[imp.at].text
		var sites []int
		for i, t := range tokens {
			if !t.name || t.text != local || i == imp.at || i == imp.at-2 && tokenIs(tokens, i+1, "as") {
				continue
			}
			if t.depth == 0 || !tokenIs(tokens, i+1, "(") || tokenIs(tokens, i-1, ":") || tokenIs(tokens, i-1, ".") || tokenIs(tokens, i-1, "fn") {
				sites = nil
				break
			}
			sites = append(sites, i)
		}
		if len(sites) == 0 {
			continue
		}
		target, err := c.rustModules(dir, root, scope, imp.parts[:len(imp.parts)-1])
		if err != nil {
			return nil, err
		}
		if target == p || target == "" {
			continue
		}
		definition, ok, err := c.text(target)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		symbol := imp.parts[len(imp.parts)-1]
		start, end := rustDefinition(rustTokens(definition), symbol)
		if start == 0 || !fitsWitness(strings.Split(definition, "\n"), start, end) {
			continue
		}
		for _, i := range sites {
			out = append(out, sourceCall{caller: sourceCallLocation{p, tokens[i].line, tokens[tokens[i+1].close].line}, callee: sourceCallLocation{target, start, end}, symbol: symbol})
		}
	}
	return out, nil
}

func rustDefinition(tokens []callToken, symbol string) (start, end int) {
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, symbol) {
			continue
		}
		if start != 0 || !tokenIs(tokens, i-1, "fn") || !tokenIs(tokens, i+1, "(") {
			return 0, 0
		}
		first := i - 2
		if tokenIs(tokens, first, ")") && tokenIs(tokens, first-1, "crate") && tokenIs(tokens, first-2, "(") {
			first -= 3
		}
		if !tokenIs(tokens, first, "pub") {
			return 0, 0
		}
		for at := tokens[i+1].close + 1; at < len(tokens); at++ {
			if tokenIs(tokens, at, "{") {
				start, end = tokens[first].line, tokens[tokens[at].close].line
				break
			}
			if tokenIs(tokens, at, ";") || tokenIs(tokens, at, "fn") {
				return 0, 0
			}
		}
	}
	return start, end
}
