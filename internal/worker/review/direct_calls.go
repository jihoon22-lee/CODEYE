package review

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	callMaxTokens = 16384
	callMaxDepth  = 128
	callMaxSites  = 256
)

// directCall is a request-only navigation hint. The two excerpts are the
// evidence for the model to read and cite.
type directCall struct {
	CallerEvidenceID string `json:"caller_evidence_id"`
	CalleeEvidenceID string `json:"callee_evidence_id"`
	Symbol           string `json:"symbol"`
}

type callBinding struct {
	module, symbol string
	token          int
}

type callSite struct {
	module, symbol string
	line, end      int
}

type callToken struct {
	text                string
	line, column, depth int
	endLine             int
	close, parent       int
	name, quoted        bool
	stringLiteral       bool
	character           bool
}

func callLanguage(p string) string {
	switch path.Ext(p) {
	case ".py":
		return "python"
	case ".js", ".mjs", ".ts", ".mts":
		return "javascript"
	}
	return ""
}

// callTokens marks names and shallow nesting in a limited source subset. It
// leaves comments and strings opaque and abandons syntax it cannot separate
// safely, such as JavaScript regular expressions or interpolated templates.
func callTokens(content string, python bool) []callToken {
	return scanCallTokens(content, python, false)
}

func scanCallTokens(content string, python, member bool) []callToken {
	return lexicalCallTokens(content, python, member, false, callMaxTokens)
}

func documentationTokens(content string, python bool) []callToken {
	return lexicalCallTokens(content, python, false, !python, 4*callMaxTokens)
}

func lexicalCallTokens(content string, python, member, rust bool, limit int) []callToken {
	var tokens []callToken
	var stack []int
	line, column := 1, 0
	advance := func(chunk string) {
		line += strings.Count(chunk, "\n")
		if i := strings.LastIndexByte(chunk, '\n'); i >= 0 {
			column = len(chunk) - i - 1
		} else {
			column += len(chunk)
		}
	}
	for len(content) > 0 {
		if len(tokens) >= limit || len(stack) > callMaxDepth {
			return nil
		}
		t := callToken{line: line, column: column, depth: len(stack), parent: -1}
		if len(stack) > 0 {
			t.parent = stack[len(stack)-1]
		}
		c, size := content[0], 1
		switch {
		case c == ' ' || c == '\r' || c == '\n' || c == '\t' || c == '\f':
			advance(content[:1])
			content = content[1:]
			continue
		case (python && c == '#') || (!python && strings.HasPrefix(content, "//")):
			size = strings.IndexByte(content, '\n')
			if size < 0 {
				size = len(content)
			}
			advance(content[:size])
			content = content[size:]
			continue
		case !python && strings.HasPrefix(content, "/*"):
			if rust {
				size = rustCommentSize(content)
				if size == 0 {
					return nil
				}
				advance(content[:size])
				content = content[size:]
				continue
			}
			end := strings.Index(content[2:], "*/")
			if end < 0 {
				return nil
			}
			size = end + 4
			advance(content[:size])
			content = content[size:]
			continue
		case !python && member && c == '`':
			size = memberTemplateSize(content)
			if size == 0 {
				return nil
			}
		case rust && rustRawStringPrefix(content) != "":
			prefix := rustRawStringPrefix(content)
			end := `"` + prefix[1:len(prefix)-1]
			at := strings.Index(content[len(prefix):], end)
			if at < 0 {
				return nil
			}
			size = len(prefix) + at + len(end)
			t.stringLiteral = true
		case rust && c == '\'' && rustLifetime(content):
			t.text = "'"
		case c == '\'' || c == '"' || (!python && !rust && c == '`'):
			t.stringLiteral = true
			t.character = c == '\''
			quote := content[:1]
			if python && strings.HasPrefix(content, strings.Repeat(quote, 3)) {
				quote = strings.Repeat(quote, 3)
			}
			size = len(quote)
			for size < len(content) && !strings.HasPrefix(content[size:], quote) {
				if content[size] == '\\' {
					size += 2
					continue
				}
				size++
			}
			if size >= len(content) {
				return nil
			}
			raw := content[len(quote):size]
			if c == '`' && (strings.Contains(raw, "${") || strings.Contains(raw, "\\")) {
				return nil
			}
			if len(quote) == 1 && c != '`' && strings.Contains(raw, "\n") && !strings.Contains(raw, "\\") {
				return nil
			}
			if len(quote) == 1 && c != '`' && !strings.ContainsAny(raw, "\\\n") {
				t.text, t.quoted = raw, true
			}
			size += len(quote)
		case !python && !rust && (c == '/' || c == '\\'):
			return nil
		default:
			first, n := utf8.DecodeRuneInString(content)
			isName := func(r rune) bool {
				return unicode.IsLetter(r) || r == '_' || (!python && r == '$')
			}
			if isName(first) {
				t.name, size = true, n
				for size < len(content) {
					r, n := utf8.DecodeRuneInString(content[size:])
					if !isName(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
						break
					}
					size += n
				}
			}
			t.text = content[:size]
			switch c {
			case '(', '[', '{':
				stack = append(stack, len(tokens))
			case ')', ']', '}':
				matching := "([{"[strings.IndexByte(")]}", c)]
				if len(stack) == 0 {
					return nil
				}
				open := stack[len(stack)-1]
				if tokens[open].text[0] != matching {
					return nil
				}
				tokens[open].close = len(tokens)
				stack = stack[:len(stack)-1]
			}
		}
		advance(content[:size])
		t.endLine = line
		tokens = append(tokens, t)
		content = content[size:]
	}
	if len(stack) != 0 {
		return nil
	}
	return tokens
}

func rustCommentSize(content string) int {
	depth := 1
	for i := 2; i+1 < len(content); i++ {
		switch content[i : i+2] {
		case "/*":
			depth++
			i++
		case "*/":
			depth--
			if depth == 0 {
				return i + 2
			}
			i++
		}
	}
	return 0
}

func rustRawStringPrefix(text string) string {
	if text[0] != 'r' {
		return ""
	}
	i := 1
	for i < len(text) && text[i] == '#' {
		i++
	}
	if i < len(text) && text[i] == '"' {
		return text[:i+1]
	}
	return ""
}

func rustLifetime(text string) bool {
	r, n := utf8.DecodeRuneInString(text[1:])
	return (unicode.IsLetter(r) || r == '_') && (len(text) <= n+1 || text[n+1] != '\'')
}

// memberTemplateSize skips simple template expressions as opaque text. A
// nested template or comment needs a grammar beyond this token pass.
func memberTemplateSize(content string) int {
	depth := 0
	var quote byte
	for i := 1; i < len(content); i++ {
		c := content[i]
		switch {
		case c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case depth == 0:
			if c == '`' {
				return i + 1
			}
			if c == '$' && i+1 < len(content) && content[i+1] == '{' {
				depth++
				i++
			}
		case c == '`' || c == '/':
			return 0
		case c == '\'' || c == '"':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			depth--
		}
	}
	return 0
}

func tokenIs(tokens []callToken, i int, text string) bool {
	return i >= 0 && i < len(tokens) && !tokens[i].quoted && tokens[i].text == text
}

func tokenName(tokens []callToken, i int) bool {
	return i >= 0 && i < len(tokens) && tokens[i].name
}

func pythonDynamicBindings(tokens []callToken) bool {
	for i, t := range tokens {
		if !t.name {
			continue
		}
		switch t.text {
		case "exec", "eval", "globals", "locals", "nonlocal", "global", "__import__", "__dict__", "__builtins__":
			return true
		case "import":
			if tokenIs(tokens, i+1, "*") {
				return true
			}
		}
	}
	return false
}

// importedBindings accepts Python from-imports and ESM named imports. It
// records both the originating name and the token that introduces its local
// binding. Encountering constructs that can rewrite Python's name bindings
// makes the file unsuitable for this selection pass.
func importedBindings(tokens []callToken, python bool) map[string][]callBinding {
	if python && pythonDynamicBindings(tokens) {
		return nil
	}
	out := map[string][]callBinding{}
	add := func(module, symbol string, i int) {
		name := tokens[i].text
		out[name] = append(out[name], callBinding{module, symbol, i})
	}
	for i, t := range tokens {
		if !t.name {
			continue
		}
		if python {
			if t.text != "from" || t.column != 0 || t.depth != 0 {
				continue
			}
			cursor := i + 1
			for tokenIs(tokens, cursor, ".") {
				cursor++
			}
			for tokenName(tokens, cursor) && !tokenIs(tokens, cursor, "import") {
				cursor++
				if !tokenIs(tokens, cursor, ".") {
					break
				}
				cursor++
			}
			if cursor == i+1 || !tokenIs(tokens, cursor, "import") || tokens[cursor].line != t.line {
				continue
			}
			var module strings.Builder
			for _, part := range tokens[i+1 : cursor] {
				module.WriteString(part.text)
			}
			for j := cursor + 1; tokenName(tokens, j) && tokens[j].line == t.line; j++ {
				symbol := tokens[j].text
				if tokenIs(tokens, j+1, "as") && tokenName(tokens, j+2) {
					j += 2
				}
				add(module.String(), symbol, j)
				if !tokenIs(tokens, j+1, ",") {
					break
				}
				j++
			}
			continue
		}

		if t.text != "import" || t.depth != 0 || !tokenIs(tokens, i+1, "{") {
			continue
		}
		end := tokens[i+1].close
		if !tokenIs(tokens, end+1, "from") || end+2 >= len(tokens) || !tokens[end+2].quoted {
			continue
		}
		module := tokens[end+2].text
		if !strings.HasPrefix(module, "./") && !strings.HasPrefix(module, "../") {
			continue
		}
		for j := i + 2; j < end && tokenName(tokens, j); j++ {
			symbol := tokens[j].text
			if tokenIs(tokens, j+1, "as") && j+2 < end && tokenName(tokens, j+2) {
				j += 2
			}
			if j+1 != end && !tokenIs(tokens, j+1, ",") {
				break
			}
			add(module, symbol, j)
			j++
		}
	}
	return out
}

func isDirectCall(tokens []callToken, i int) bool {
	if !tokenIs(tokens, i+1, "(") {
		return false
	}
	for _, prev := range []string{".", "def", "function", "class", "new"} {
		if tokenIs(tokens, i-1, prev) {
			return false
		}
	}
	return true
}

func importedCalls(tokens []callToken, python bool) []callSite {
	bindings := importedBindings(tokens, python)
	ambiguous := map[string]bool{}
	for i, t := range tokens {
		if names := bindings[t.text]; t.name && len(names) == 1 &&
			i != names[0].token && !isDirectCall(tokens, i) {
			ambiguous[t.text] = true
		}
	}
	var out []callSite
	for i, t := range tokens {
		if names := bindings[t.text]; t.name && len(names) == 1 &&
			i != names[0].token && !ambiguous[t.text] && isDirectCall(tokens, i) {
			close := tokens[i+1].close
			if !tokenIs(tokens, close+1, "{") {
				out = append(out, callSite{names[0].module, names[0].symbol, t.line, tokens[close].line})
				if len(out) > callMaxSites {
					return nil
				}
			}
		}
	}
	return out
}

// directModule resolves only conventional source paths, checking alternative
// paths against the complete manifest before review filters are applied.
func directModule(files map[string]source.Entry, caller, module string, python bool) string {
	dir := path.Dir(caller)
	var paths []string
	if python {
		relative := strings.HasPrefix(module, ".")
		for strings.HasPrefix(module, ".") {
			module = module[1:]
			if !strings.HasPrefix(module, ".") {
				break
			}
			if dir == "." {
				return ""
			}
			dir = path.Dir(dir)
		}
		if module == "" {
			return ""
		}
		appendPaths := func(root string) {
			base := path.Join(root, strings.ReplaceAll(module, ".", "/"))
			paths = append(paths, base+".py", base+".pyi", path.Join(base, "__init__.py"), path.Join(base, "__init__.pyi"))
		}
		appendPaths(dir)
		if !relative {
			for root := dir; ; root = path.Dir(root) {
				_, pyproject := files[path.Join(root, "pyproject.toml")]
				_, setup := files[path.Join(root, "setup.cfg")]
				if root == "." || pyproject || setup {
					if root != dir {
						appendPaths(root)
					}
					if src := path.Join(root, "src"); src != dir {
						appendPaths(src)
					}
					break
				}
			}
		}
	} else {
		if strings.ContainsAny(module, "?#\\") {
			return ""
		}
		base := path.Join(dir, module)
		switch path.Ext(base) {
		case "":
			if _, packageConfig := files[path.Join(base, "package.json")]; packageConfig {
				return ""
			}
			for _, ext := range []string{".js", ".ts", ".mjs", ".mts", ".jsx", ".tsx", ".cjs", ".cts", ".json"} {
				paths = append(paths, base+ext, path.Join(base, "index"+ext))
			}
		case ".js":
			stem := strings.TrimSuffix(base, ".js")
			paths = []string{base, stem + ".ts", stem + ".tsx"}
		case ".mjs":
			stem := strings.TrimSuffix(base, ".mjs")
			paths = []string{base, stem + ".mts"}
		default:
			paths = []string{base}
		}
	}
	var found string
	for _, candidate := range paths {
		if _, exists := files[candidate]; exists {
			if found != "" {
				return ""
			}
			found = candidate
		}
	}
	if found == caller || callLanguage(found) != callLanguage(caller) || windowKind(found) != "code" {
		return ""
	}
	return found
}

// directDefinition selects the visible result of a unique module-level
// definition. It accepts a conventional Python def or ESM export function,
// including async variants, and refuses declarations that have other uses
// which might replace their binding.
func directDefinition(tokens []callToken, symbol string, python bool) (start, end int) {
	if python && pythonDynamicBindings(tokens) {
		return 0, 0
	}
	name := -1
	for i, t := range tokens {
		if !t.name || t.text != symbol {
			continue
		}
		decl := tokenIs(tokens, i-1, "def")
		if !python {
			decl = tokenIs(tokens, i-1, "function")
		}
		if !decl {
			if !isDirectCall(tokens, i) {
				return 0, 0
			}
			continue
		}
		if name >= 0 || t.depth != 0 {
			return 0, 0
		}
		name = i
	}
	if name < 0 || !tokenIs(tokens, name+1, "(") {
		return 0, 0
	}
	decl := name - 1
	if tokenIs(tokens, decl-1, "async") {
		decl--
	}
	if python {
		if tokens[decl].column != 0 {
			return 0, 0
		}
		for i := decl - 1; i >= 0; i-- {
			if tokens[i].depth == 0 && tokens[i].column == 0 {
				if tokenIs(tokens, i, "@") {
					return 0, 0
				}
				break
			}
		}
	} else {
		decl--
		if !tokenIs(tokens, decl, "export") {
			return 0, 0
		}
	}
	close := tokens[name+1].close
	body := close + 1
	if python {
		// A return annotation can follow the parameter list.
		for body < len(tokens) && tokens[body].line == tokens[close].line && !tokenIs(tokens, body, ":") {
			body++
		}
		if !tokenIs(tokens, body, ":") {
			return 0, 0
		}
	} else {
		// Accept a simple return annotation before the opening brace.
		if tokenIs(tokens, body, ":") && tokenName(tokens, body+1) {
			body += 2
		}
		if !tokenIs(tokens, body, "{") {
			return 0, 0
		}
	}
	start = tokens[decl].line
	for i := body + 1; i < len(tokens); i++ {
		t := tokens[i]
		if t.line >= start+witnessMaxLines ||
			(python && t.column == 0 && t.depth == 0) ||
			(!python && t.depth == 1 && tokenIs(tokens, i, "}")) {
			break
		}
		// A return inside a nested function belongs to that function.
		if tokenIs(tokens, i, "def") || tokenIs(tokens, i, "class") || tokenIs(tokens, i, "function") ||
			(tokenIs(tokens, i, "=") && tokenIs(tokens, i+1, ">")) {
			break
		}
		if !t.name || i+1 == len(tokens) {
			continue
		}
		switch t.text {
		case "return", "raise", "throw", "assert":
			if tokens[i+1].line == t.line && !tokenIs(tokens, i-1, ".") && !tokenIs(tokens, i+1, ":") {
				last := t.line
				for j := i + 1; j < len(tokens) && tokens[j].line <= last; j++ {
					if tokenIs(tokens, j, ";") || tokens[j].depth < t.depth {
						break
					}
					if next := tokens[j].close; next > j {
						j = next
						last = tokens[j].line
					}
				}
				return start, last
			}
		}
	}
	return 0, 0
}
