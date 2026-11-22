package review

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type rustAPIUse struct {
	target string
	local  string
	first  int
	last   int
}

func rustPath(tokens []callToken, at int) ([]string, int) {
	if !tokenName(tokens, at) {
		return nil, at
	}
	names := []string{tokens[at].text}
	at++
	for len(names) < 8 && tokenIs(tokens, at, ":") && tokenIs(tokens, at+1, ":") && tokenName(tokens, at+2) {
		names = append(names, tokens[at+2].text)
		at += 3
	}
	return names, at
}

func rustUsePaths(tokens []callToken, at int) []rustAPIUse {
	prefix, next := rustPath(tokens, at+1)
	if len(prefix) == 0 {
		return nil
	}
	alias := func(parts []string, cursor int) (rustAPIUse, int) {
		local := parts[len(parts)-1]
		if tokenIs(tokens, cursor, "as") && tokenName(tokens, cursor+1) {
			local = tokens[cursor+1].text
			cursor += 2
		}
		return rustAPIUse{target: strings.Join(parts, "::"), local: local, first: at, last: cursor - 1}, cursor
	}
	switch {
	case tokenIs(tokens, next, ":") && tokenIs(tokens, next+1, ":") && tokenIs(tokens, next+2, "{"):
		end := tokens[next+2].close
		if !tokenIs(tokens, end+1, ";") {
			return nil
		}
		var bindings []rustAPIUse
		for cursor := next + 3; cursor < end; {
			names, tail := rustPath(tokens, cursor)
			if len(names) == 0 || names[0] == "self" {
				return nil
			}
			b, after := alias(slices.Concat(prefix, names), tail)
			if after != end && !tokenIs(tokens, after, ",") {
				return nil
			}
			b.last = end
			bindings = append(bindings, b)
			cursor = after + 1
		}
		return bindings
	default:
		b, end := alias(prefix, next)
		if len(prefix) >= 2 && tokenIs(tokens, end, ";") {
			return []rustAPIUse{b}
		}
	}
	return nil
}

func rustBoundCalls(tokens []callToken, b rustAPIUse) []int {
	var calls []int
	for i, t := range tokens {
		if !t.name || t.text != b.local || i >= b.first && i <= b.last {
			continue
		}
		if tokenIs(tokens, i-1, ".") || tokenIs(tokens, i-1, ":") {
			continue // qualified paths don't use the imported name
		}
		if !tokenIs(tokens, i+1, "(") || tokenIs(tokens, i-1, "fn") || tokenIs(tokens, i-1, "let") {
			return nil
		}
		calls = append(calls, i)
	}
	return calls
}

func rustExampleTargets(text string) []rustAPIUse {
	tokens := documentationTokens(text, false)
	var imports []rustAPIUse
	count := map[string]int{}
	for i := range tokens {
		if tokenIs(tokens, i, "use") && tokens[i].depth == 0 {
			for _, b := range rustUsePaths(tokens, i) {
				imports = append(imports, b)
				count[b.local]++
			}
		}
	}
	var used []rustAPIUse
	for _, b := range imports {
		if count[b.local] == 1 && len(rustBoundCalls(tokens, b)) > 0 {
			used = append(used, b)
		}
	}
	for i := range tokens {
		if tokenIs(tokens, i-1, ":") || tokenIs(tokens, i-1, ".") {
			continue
		}
		names, after := rustPath(tokens, i)
		if len(names) >= 2 && tokenIs(tokens, after, "(") {
			b := rustAPIUse{target: strings.Join(names, "::"), local: names[len(names)-1]}
			used = append(used, b)
		}
	}
	return used
}

func documentedRustTargets(text string) []rustAPIUse {
	var out []rustAPIUse
	for _, block := range apiFences(text) {
		if block.lang != "rust" && block.lang != "rs" {
			continue
		}
		for _, use := range rustExampleTargets(block.text) {
			if !slices.ContainsFunc(out, func(old rustAPIUse) bool { return old.target == use.target }) {
				out = append(out, use)
				if len(out) == 12 {
					return out
				}
			}
		}
	}
	return out
}

var cargoAPILabel = regexp.MustCompile(`^name[ \t]*=[ \t]*(?:"([A-Za-z][A-Za-z0-9_-]*)"|'([A-Za-z][A-Za-z0-9_-]*)')[ \t]*(?:#.*)?$`)

func rustDocumentationCrate(text string) string {
	table, pkg, lib := "", "", ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			table, _, _ = strings.Cut(line, "#")
			table = strings.TrimSpace(table)
			continue
		}
		key, _, _ := strings.Cut(line, "=")
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if table == "[package]" && slices.Contains([]string{"autolib", "build"}, key) || table == "[lib]" && key == "path" {
			return ""
		}
		if key != "name" || table != "[package]" && table != "[lib]" {
			continue
		}
		m := cargoAPILabel.FindStringSubmatch(line)
		if m == nil || table == "[package]" && pkg != "" || table == "[lib]" && lib != "" {
			return ""
		}
		name := strings.ReplaceAll(m[1]+m[2], "-", "_")
		if table == "[package]" {
			pkg = name
		} else {
			lib = name
		}
	}
	if pkg != "" && lib != "" {
		return lib
	}
	return pkg
}

func rustAttributes(tokens []callToken, first int, safe ...string) (int, bool) {
	for tokenIs(tokens, first-1, "]") {
		open := tokens[first-1].parent
		if !tokenIs(tokens, open-1, "#") {
			break
		}
		if !slices.ContainsFunc(safe, func(name string) bool { return tokenIs(tokens, open+1, name) }) {
			return first, false
		}
		first = open - 1
	}
	return first, true
}

func rustAPIModule(tokens []callToken, name string, public bool) bool {
	found := false
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "mod") || !tokenIs(tokens, i+1, name) {
			continue
		}
		first := i
		if tokenIs(tokens, first-1, "pub") {
			first--
		}
		_, safe := rustAttributes(tokens, first)
		if found || !safe || !tokenIs(tokens, i+2, ";") || public && first == i {
			return false
		}
		found = true
	}
	return found
}

func documentedRustObject(c *sourceCalls, scope string, use rustAPIUse) (source.Entry, string, string, error) {
	cargo := path.Join(scope, "Cargo.toml")
	manifest, ok, err := c.text(cargo)
	if !ok || err != nil {
		return source.Entry{}, "", "", err
	}
	if _, custom := c.files[path.Join(scope, "build.rs")]; custom {
		return source.Entry{}, "", "", nil
	}
	crate := rustDocumentationCrate(manifest)
	names := strings.Split(use.target, "::")
	if crate == "" || len(names) < 2 || names[0] != crate {
		return source.Entry{}, "", "", nil
	}
	dir := path.Join(scope, "src")
	root := path.Join(dir, "lib.rs")
	return rustAPIResolve(c, scope, root, root, dir, dir, names[1:], true, 0)
}

// Plain public re-exports are the only path through a private module. The
// manifest is checked before source filters so competing module files cannot
// disappear from the ambiguity check.
func rustAPIResolve(c *sourceCalls, scope, root, file, dir, rootDir string, names []string, public bool, depth int) (source.Entry, string, string, error) {
	var none source.Entry
	if depth >= 8 || len(names) == 0 || !productionAPISource(c.files[file], scope, ".rs") {
		return none, "", "", nil
	}
	text, ok, err := c.text(file)
	if !ok || err != nil {
		return none, "", "", err
	}
	tokens := documentationTokens(text, false)
	if len(tokens) == 0 {
		return none, "", "", nil
	}
	name := names[0]
	if len(names) > 1 && rustAPIModule(tokens, name, public) {
		dir = path.Join(dir, name)
		flat, nested := dir+".rs", path.Join(dir, "mod.rs")
		_, hasFlat := c.files[flat]
		_, hasNested := c.files[nested]
		if hasFlat == hasNested {
			return none, "", "", nil
		}
		if hasFlat {
			nested = flat
		}
		return rustAPIResolve(c, scope, root, nested, dir, rootDir, names[1:], public, depth+1)
	}
	if len(names) != 1 {
		return none, "", "", nil
	}
	var exports []rustAPIUse
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "use") || !tokenIs(tokens, i-1, "pub") {
			continue
		}
		for _, b := range rustUsePaths(tokens, i) {
			if b.local == name {
				if _, safe := rustAttributes(tokens, i-1); !safe {
					return none, "", "", nil
				}
				exports = append(exports, b)
			}
		}
	}
	decl := rustPublicFunction(tokens, name)
	if decl.first > 0 {
		if len(exports) == 0 {
			return c.files[file], name, text, nil
		}
		return none, "", "", nil
	}
	if len(exports) != 1 {
		return none, "", "", nil
	}
	bound := strings.Split(exports[0].target, "::")
	switch bound[0] {
	case "crate":
		file, dir, bound = root, rootDir, bound[1:]
	case "self":
		bound = bound[1:]
	case "super":
		return none, "", "", nil
	}
	return rustAPIResolve(c, scope, root, file, dir, rootDir, bound, false, depth+1)
}

type rustFunction struct {
	first, last int
	open        int
	docThrough  int
}

func rustPublicFunction(tokens []callToken, name string) rustFunction {
	var found rustFunction
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "fn") || !tokenIs(tokens, i+1, name) {
			continue
		}
		if found.first > 0 {
			return rustFunction{}
		}
		first := i - 1
		for tokenIs(tokens, first, "const") || tokenIs(tokens, first, "async") || tokenIs(tokens, first, "unsafe") {
			first--
		}
		if !tokenIs(tokens, first, "pub") {
			return rustFunction{}
		}
		attr, safe := rustAttributes(tokens, first, "inline", "must_use", "deprecated")
		if !safe {
			return rustFunction{}
		}
		for body := i + 2; body < len(tokens); body++ {
			if tokenIs(tokens, body, "{") && tokens[body].depth == 0 {
				found = rustFunction{first: tokens[first].line, last: tokens[tokens[body].close].line, open: body, docThrough: tokens[attr].line - 1}
				break
			}
			if tokenIs(tokens, body, ";") || tokenIs(tokens, body, "fn") && tokens[body].depth == 0 {
				return rustFunction{}
			}
		}
	}
	return found
}

func rustDocLines(lines []string, end int) (int, int) {
	if end < 1 || end > len(lines) {
		return 0, 0
	}
	isDoc := func(raw string) bool {
		trim := strings.TrimSpace(raw)
		return strings.HasPrefix(trim, "///") && !strings.HasPrefix(trim, "////")
	}
	if isDoc(lines[end-1]) {
		start := end
		for start > 1 && isDoc(lines[start-2]) {
			start--
		}
		return start, end
	}
	if strings.HasSuffix(strings.TrimSpace(lines[end-1]), "*/") {
		for start := end; start >= 1; start-- {
			line := strings.TrimSpace(lines[start-1])
			if strings.HasPrefix(line, "/**") && !strings.HasPrefix(line, "/***") {
				return start, end
			}
			if !strings.HasPrefix(line, "*") {
				break
			}
		}
	}
	return 0, 0
}

func (s *roleStage) rustDocExcerpt(entry source.Entry, page, text, name string, budget int) (record.Excerpt, bool) {
	tokens := documentationTokens(text, false)
	decl := rustPublicFunction(tokens, name)
	lines := strings.Split(text, "\n")
	start, end := rustDocLines(lines, decl.docThrough)
	if start == 0 {
		return record.Excerpt{}, false
	}
	if end-start+1 > roleMaxDocLines {
		start = end - roleMaxDocLines + 1
	}
	x, ok := s.boundedExcerpt(entry, "document", "", start, lines[start-1:end], budget)
	x.DirectivePath = page
	return x, ok
}
