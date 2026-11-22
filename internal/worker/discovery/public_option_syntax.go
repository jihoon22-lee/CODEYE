package discovery

import (
	"bytes"
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

type optionToken struct {
	text                     string
	line, end, close, parent int
	quoted, unindented       bool
}

// optionTokens keeps literals and source lines while treating comments and
// unrelated strings as opaque. The extractors accept explicit API forms
// only; unbalanced or ambiguous syntax is left unclassified.
func optionTokens(body []byte, ext string) []optionToken {
	python := ext == ".py"
	var out []optionToken
	var stack []int
	line, column := 1, 1
	for len(body) > 0 {
		if len(out) >= 16000 || len(stack) >= 128 {
			return nil
		}
		c := body[0]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
			if c == '\n' {
				line++
				column = 1
			} else {
				column++
			}
			body = body[1:]
			continue
		}
		if python && c == '#' || !python && bytes.HasPrefix(body, []byte("//")) {
			i := bytes.IndexByte(body, '\n')
			if i < 0 {
				break
			}
			body = body[i:]
			continue
		}
		if !python && bytes.HasPrefix(body, []byte("/*")) {
			i := bytes.Index(body[2:], []byte("*/"))
			if i < 0 {
				return nil
			}
			line += bytes.Count(body[:i+4], []byte{'\n'})
			column += i + 4
			body = body[i+4:]
			continue
		}
		t := optionToken{line: line, parent: -1, unindented: column == 1}
		if len(stack) > 0 {
			t.parent = stack[len(stack)-1]
		}
		n := 1
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote := body[:1]
			if python && len(body) >= 3 && body[1] == c && body[2] == c {
				quote = body[:3]
			}
			i := len(quote)
			for i < len(body) && !bytes.HasPrefix(body[i:], quote) {
				if body[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(body) {
				return nil
			}
			raw := body[len(quote):i]
			if c != '`' && len(quote) == 1 && !bytes.ContainsAny(raw, "\\\r\n") {
				t.text = string(raw)
			}
			t.quoted, n = true, i+len(quote)
		case optionIdentByte(c):
			for n < len(body) && optionIdentByte(body[n]) {
				n++
			}
			t.text = string(body[:n])
			if ext == ".rs" && n < len(body) && (body[n] == '"' || body[n] == '#') &&
				slices.Contains([]string{"r", "br", "cr"}, t.text) {
				return nil // raw literal needs its own delimiter parser
			}
		default:
			t.text = string(body[:1])
			switch c {
			case '(', '[', '{':
				stack = append(stack, len(out))
			case ')', ']', '}':
				if len(stack) == 0 {
					return nil
				}
				at := stack[len(stack)-1]
				if out[at].text[0] != "([{"[strings.IndexByte(")]}", c)] {
					return nil
				}
				out[at].close = len(out)
				stack = stack[:len(stack)-1]
			}
		}
		line += bytes.Count(body[:n], []byte{'\n'})
		if lastLine := bytes.LastIndexByte(body[:n], '\n'); lastLine >= 0 {
			column = n - lastLine
		} else {
			column += n
		}
		t.end = line
		out = append(out, t)
		body = body[n:]
	}
	if len(stack) != 0 {
		return nil
	}
	return out
}

func optionIdentByte(b byte) bool {
	return b == '_' || b == '$' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

func hasOptionTokens(tokens []optionToken, at int, want ...string) bool {
	if at < 0 || at+len(want) > len(tokens) {
		return false
	}
	for i, value := range want {
		if tokens[at+i].quoted || tokens[at+i].text != value {
			return false
		}
	}
	return true
}

func optionTokenArgs(tokens []optionToken, open int) [][]optionToken {
	if open < 0 || open >= len(tokens) || tokens[open].close <= open {
		return nil
	}
	end := tokens[open].close
	var out [][]optionToken
	start := open + 1
	for i := start; i < end; i++ {
		if tokens[i].parent == open && hasOptionTokens(tokens, i, ",") {
			out = append(out, tokens[start:i])
			start = i + 1
		}
	}
	if start < end {
		out = append(out, tokens[start:end])
	}
	return out
}

func optionArgument(args [][]optionToken, name string) []optionToken {
	var value []optionToken
	for _, a := range args {
		if hasOptionTokens(a, 0, name, "=") {
			if value != nil {
				return nil
			}
			value = a[2:]
		}
	}
	return value
}

func optionScalar(tokens []optionToken) string {
	if len(tokens) == 1 {
		if tokens[0].quoted || tokens[0].text == "true" || tokens[0].text == "false" || tokens[0].text == "True" || tokens[0].text == "False" ||
			(tokens[0].text != "" && strings.IndexByte("0123456789", tokens[0].text[0]) >= 0) {
			return tokens[0].text
		}
	}
	return ""
}

func tokenPublicOptions(p string, b []byte) []publicOption {
	ext := path.Ext(p)
	// The limited tokenization does not interpret JS regular-expression
	// literals, Java Unicode escapes, C line splices or Rust raw strings.
	if ext == ".java" && (bytes.Contains(b, []byte(`\u`)) || bytes.Contains(b, []byte(`"""`))) ||
		(ext == ".c" || ext == ".cc" || ext == ".cpp" || ext == ".cxx" || ext == ".c++") && bytes.Contains(b, []byte("\\\n")) {
		return nil
	}
	tokens := optionTokens(b, ext)
	switch ext {
	case ".py":
		return scriptOptions(tokens, true)
	case ".js", ".ts", ".mjs", ".mts":
		if slices.ContainsFunc(tokens, func(t optionToken) bool { return t.text == "/" && !t.quoted }) {
			return nil
		}
		return scriptOptions(tokens, false)
	case ".java":
		return javaOptions(tokens)
	case ".c", ".cc", ".cpp", ".cxx", ".c++":
		return cOptions(tokens)
	case ".rs":
		return rustOptions(tokens)
	}
	return nil
}

const maxPublicParserBindings = 16

func simpleOptionNames(tokens []optionToken) []string {
	if len(tokens) == 1 && tokens[0].quoted {
		return []string{tokens[0].text}
	}
	if !hasOptionTokens(tokens, 0, "{") || !hasOptionTokens(tokens, len(tokens)-1, "}") {
		return nil
	}
	var out []string
	for i, token := range tokens[1 : len(tokens)-1] {
		if i%2 == 0 && token.quoted {
			out = append(out, token.text)
		} else if i%2 == 0 || token.quoted || token.text != "," {
			return nil
		}
	}
	return out
}

func scriptOptions(tokens []optionToken, python bool) []publicOption {
	ctor := []string{"=", "new", "Command", "("}
	method, parse := "option", "parse"
	imported := false
	if python {
		ctor = []string{"=", "argparse", ".", "ArgumentParser", "("}
		method, parse = "add_argument", "parse_args"
	}
	for i, t := range tokens {
		if python && hasOptionTokens(tokens, i, "import", "argparse") && t.parent < 0 &&
			t.unindented && !hasOptionTokens(tokens, i+2, "as") {
			imported = true
		}
		if !python && hasOptionTokens(tokens, i, "import", "{", "Command", "}", "from") && i+5 < len(tokens) &&
			tokens[i+5].quoted && tokens[i+5].text == "commander" && t.parent < 0 {
			imported = true
		}
	}
	if !imported {
		return nil
	}
	// A shadow of the imported constructor prevents precise lexical binding.
	for i, t := range tokens {
		if t.quoted {
			continue
		}
		if python && t.text == "argparse" && !hasOptionTokens(tokens, i-1, "import", "argparse") &&
			!hasOptionTokens(tokens, i, "argparse", ".", "ArgumentParser", "(") && !hasOptionTokens(tokens, i, "argparse", ".", "SUPPRESS") {
			return nil
		}
		if !python && t.text == "Command" && !hasOptionTokens(tokens, i-2, "import", "{", "Command") && !hasOptionTokens(tokens, i-1, "new", "Command", "(") {
			return nil
		}
	}
	var out []publicOption
	bindings := 0
	for i, t := range tokens {
		if t.quoted || t.parent >= 0 || !hasOptionTokens(tokens, i+1, ctor...) {
			continue
		}
		if python && !t.unindented || !python && (!hasOptionTokens(tokens, i-1, "const") || !scriptStatementStart(tokens, i-1, false)) {
			continue
		}
		bindings++
		if bindings > maxPublicParserBindings {
			return nil
		}
		var options []publicOption
		parsedAt := -1
		valid := true
		for j, use := range tokens {
			if use.quoted || use.text != t.text || i == j {
				continue
			}
			if j < i || !hasOptionTokens(tokens, j+1, ".") || !hasOptionTokens(tokens, j+3, "(") {
				valid = false
				break
			}
			statement := scriptStatementStart(tokens, j, python)
			if !statement && python && j >= 2 && hasOptionTokens(tokens, j-1, "=") && tokens[j-2].line == use.line &&
				scriptStatementStart(tokens, j-2, true) && tokens[j+2].text == parse {
				statement = true
			}
			if !statement {
				valid = false
				break
			}
			switch tokens[j+2].text {
			case parse:
				parsedAt = j
			case method:
				if parsedAt >= 0 {
					continue
				}
				a := optionTokenArgs(tokens, j+3)
				if len(a) == 0 || len(a[0]) != 1 || !a[0][0].quoted {
					continue
				}
				name := a[0][0].text
				fallback := ""
				if python {
					help := optionArgument(a, "help")
					if len(help) > 0 && !(len(help) == 1 && help[0].quoted) {
						continue
					}
					fallback = optionScalar(optionArgument(a, "default"))
				} else {
					name, _, _ = strings.Cut(name, " ")
					if len(a) == 3 {
						fallback = optionScalar(a[2])
					}
				}
				options = append(options, publicOption{envdoc.DocGapOption, name, fallback, use.line, tokens[tokens[j+3].close].end})
			default:
				valid = false // set_defaults, hidden registrations or rebinding
			}
		}
		if valid && parsedAt > i {
			out = append(out, options...)
		}
	}
	return out
}

func scriptStatementStart(tokens []optionToken, i int, python bool) bool {
	if i < 0 || tokens[i].parent >= 0 {
		return false
	}
	if python {
		return tokens[i].unindented && !hasOptionTokens(tokens, i-1, "\\")
	}
	return i == 0 || hasOptionTokens(tokens, i-1, ";")
}

func optionHidden(value []optionToken) bool {
	return len(value) > 0 && !(len(value) == 1 && hasOptionTokens(value, 0, "false"))
}

func javaOptions(tokens []optionToken) []publicOption {
	short, qualified := false, false
	for i := range tokens {
		if hasOptionTokens(tokens, i, "import", "picocli", ".", "CommandLine", ".", "Option", ";") {
			short = true
		}
		if hasOptionTokens(tokens, i, "import", "picocli", ".", "CommandLine", ";") {
			qualified = true
		}
		if hasOptionTokens(tokens, i, "interface", "Option") || hasOptionTokens(tokens, i, "class", "CommandLine") {
			return nil
		}
	}
	var out []publicOption
	for i, t := range tokens {
		open := 0
		switch {
		case hasOptionTokens(tokens, i, "@", "picocli", ".", "CommandLine", ".", "Option", "("):
			open = i + 6
		case qualified && hasOptionTokens(tokens, i, "@", "CommandLine", ".", "Option", "("):
			open = i + 4
		case short && hasOptionTokens(tokens, i, "@", "Option", "("):
			open = i + 2
		}
		if open == 0 {
			continue
		}
		a := optionTokenArgs(tokens, open)
		hidden := optionArgument(a, "hidden")
		if optionHidden(hidden) {
			continue
		}
		names := optionArgument(a, "names")
		for _, n := range simpleOptionNames(names) {
			if envdoc.ValidDocumentationSymbol(envdoc.DocGapOption, n) {
				out = append(out, publicOption{envdoc.DocGapOption, n, optionScalar(optionArgument(a, "defaultValue")), t.line, tokens[tokens[open].close].end})
				break
			}
		}
	}
	return out
}

func cOptions(tokens []optionToken) []publicOption {
	include := false
	for i := range tokens {
		if !hasOptionTokens(tokens, i, "#") {
			continue
		}
		if !hasOptionTokens(tokens, i+1, "include") {
			return nil
		}
		if hasOptionTokens(tokens, i+2, "<", "getopt", ".", "h", ">") {
			include = true
		}
	}
	if !include {
		return nil
	}
	mainStart, mainEnd := 0, 0
	for i, t := range tokens {
		if t.parent < 0 && hasOptionTokens(tokens, i, "main", "(") {
			body := tokens[i+1].close + 1
			if hasOptionTokens(tokens, body, "{") {
				mainStart, mainEnd = body, tokens[body].close
			}
		}
	}
	if mainEnd == 0 {
		return nil
	}
	var out []publicOption
	bindings := 0
	for i := range tokens {
		if !hasOptionTokens(tokens, i, "struct", "option") || i+5 >= len(tokens) || !hasOptionTokens(tokens, i+3, "[", "]", "=", "{") {
			continue
		}
		bindings++
		if bindings > maxPublicParserBindings {
			return nil
		}
		name, open := tokens[i+2].text, i+6
		used := false
		for j := mainStart + 1; j < mainEnd; j++ {
			if !hasOptionTokens(tokens, j, "getopt_long", "(") {
				continue
			}
			a := optionTokenArgs(tokens, j+1)
			if len(a) == 5 && len(a[3]) == 1 && hasOptionTokens(a[3], 0, name) {
				used = true
				break
			}
		}
		// A second declaration or an assignment to this binding makes the
		// spelling alone insufficient to identify the table passed to the call.
		uses := 0
		for j := range tokens {
			if hasOptionTokens(tokens, j, name) {
				uses++
			}
		}
		if !used || uses != 2 {
			continue
		}
		for _, a := range optionTokenArgs(tokens, open) {
			if len(a) < 3 || !hasOptionTokens(a, 0, "{") || !a[1].quoted || !hasOptionTokens(a, 2, ",") {
				continue
			}
			out = append(out, publicOption{envdoc.DocGapOption, "--" + a[1].text, "", a[0].line, a[len(a)-1].end})
		}
	}
	return out
}

func rustOptions(tokens []optionToken) []publicOption {
	imported := false
	mainBody := -1
	for i, token := range tokens {
		if token.parent < 0 && hasOptionTokens(tokens, i, "fn", "main", "(", ")", "{") {
			mainBody = i + 4
		}
		if hasOptionTokens(tokens, i, "use", "clap", ":", ":", "Parser", ";") {
			imported = true
		}
		if hasOptionTokens(tokens, i, "cfg", "(") || hasOptionTokens(tokens, i, "cfg_attr", "(") {
			return nil
		}
	}
	if mainBody < 0 {
		return nil
	}
	var out []publicOption
	for i := range tokens {
		if tokens[i].parent >= 0 {
			continue
		}
		before := 0
		if hasOptionTokens(tokens, i, "#", "[", "derive", "(", "clap", ":", ":", "Parser", ")", "]", "struct") {
			before = 11
		} else if imported && hasOptionTokens(tokens, i, "#", "[", "derive", "(", "Parser", ")", "]", "struct") {
			before = 8
		}
		if before == 0 || i+before+1 >= len(tokens) || !hasOptionTokens(tokens, i+before+1, "{") {
			continue
		}
		name, body := tokens[i+before].text, i+before+1
		parsed := false
		for j := mainBody + 1; j < tokens[mainBody].close; j++ {
			if tokens[j].parent == mainBody && hasOptionTokens(tokens, j, name, ":", ":", "parse", "(", ")") {
				parsed = true
				break
			}
		}
		if !parsed {
			continue
		}
		for j := body + 1; j < tokens[body].close; j++ {
			if tokens[j].parent != body || !hasOptionTokens(tokens, j, "#", "[", "arg", "(") {
				continue
			}
			open := j + 3
			a := optionTokenArgs(tokens, open)
			hidden := optionArgument(a, "hide")
			if optionHidden(hidden) {
				continue
			}
			from, to := tokens[j].line, tokens[tokens[open].close].end
			fallback := optionScalar(optionArgument(a, "default_value"))
			if fallback == "" {
				fallback = optionScalar(optionArgument(a, "default_value_t"))
			}
			if long := optionArgument(a, "long"); len(long) == 1 && long[0].quoted {
				out = append(out, publicOption{envdoc.DocGapOption, "--" + long[0].text, fallback, from, to})
			}
			if env := optionArgument(a, "env"); len(env) == 1 && env[0].quoted && !optionHidden(optionArgument(a, "hide_env")) {
				out = append(out, publicOption{envdoc.DocGapEnvironment, env[0].text, fallback, from, to})
			}
		}
	}
	return out
}
