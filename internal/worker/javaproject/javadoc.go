package javaproject

import (
	"path"
	"slices"
	"strings"
)

// APIDocument is a complete Javadoc comment attached to one public method.
// Physical source lines are inclusive. The signature distinguishes overloads.
type APIDocument struct {
	Start, End         int
	Signature          string
	BodyStart, BodyEnd int
}

const apiMaxSource = 512 << 10

type apiToken struct {
	text                                  string
	start, end, line, last, parent, close int
	name, comment                         bool
}

func apiName(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '$'
}
func apiSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }

// tokenizeAPI distinguishes comments from text in string/character literals and
// tracks the precise containing block. Java preprocesses Unicode escapes before
// lexing, so the bounded reader omits files that need that transformation.
func tokenizeAPI(body string) []apiToken {
	if len(body) > apiMaxSource || strings.Contains(body, `\u`) || strings.Contains(body, `"""`) {
		return nil
	}
	var out []apiToken
	var stack []int
	line := 1
	for i := 0; i < len(body); {
		c := body[i]
		if apiSpace(c) {
			if c == '\n' {
				line++
			}
			i++
			continue
		}
		if len(out) >= 65536 || len(stack) > 96 {
			return nil
		}
		t := apiToken{start: i, line: line, last: line, parent: -1, close: -1}
		if len(stack) > 0 {
			t.parent = stack[len(stack)-1]
		}
		next := i + 1
		switch {
		case strings.HasPrefix(body[i:], "//"):
			rest, _, _ := strings.Cut(body[i:], "\n")
			next, t.comment = i+len(rest), true
		case strings.HasPrefix(body[i:], "/*"):
			end := strings.Index(body[i+2:], "*/")
			if end < 0 {
				return nil
			}
			next, t.comment = i+end+4, true
		case c == '"' || c == '\'':
			for next < len(body) && body[next] != c {
				if body[next] == '\n' || body[next] == '\r' {
					return nil
				}
				if body[next] == '\\' {
					next++
				}
				next++
			}
			if next >= len(body) {
				return nil
			}
			next++
		case apiName(c):
			t.name = true
			for next < len(body) && (apiName(body[next]) || body[next] >= '0' && body[next] <= '9') {
				next++
			}
		case strings.ContainsRune("([{", rune(c)):
			stack = append(stack, len(out))
		case strings.ContainsRune(")]}", rune(c)):
			if len(stack) == 0 {
				return nil
			}
			first := stack[len(stack)-1]
			if out[first].text[0] != "([{"[strings.IndexByte(")]}", c)] {
				return nil
			}
			out[first].close = len(out)
			stack = stack[:len(stack)-1]
		}
		t.text = body[i:next]
		line += strings.Count(t.text, "\n")
		t.end, t.last = next, line
		out = append(out, t)
		i = next
	}
	if len(stack) != 0 {
		return nil
	}
	return out
}

func apiTokenIs(tokens []apiToken, i int, text string) bool {
	return i >= 0 && i < len(tokens) && tokens[i].text == text
}

func conventionalJavaAPI(pathname string) (qualified, name string) {
	source := strings.TrimPrefix(pathname, "src/main/java/")
	if source == pathname {
		_, source, _ = strings.Cut(pathname, "/src/main/java/")
	}
	if source == "" || !strings.HasSuffix(source, ".java") {
		return "", ""
	}
	qualified = strings.ReplaceAll(strings.TrimSuffix(source, ".java"), "/", ".")
	return qualified, strings.TrimSuffix(path.Base(source), ".java")
}

func publicAPIClass(tokens []apiToken, qualified, name string) int {
	found := -1
	packageName := ""
	for i, t := range tokens {
		if t.parent != -1 || t.comment {
			continue
		}
		if t.text == "package" {
			if packageName != "" {
				return -1
			}
			var parts []string
			for n := i + 1; n < len(tokens) && tokens[n].name; n += 2 {
				parts = append(parts, tokens[n].text)
				if apiTokenIs(tokens, n+1, ";") {
					packageName = strings.Join(parts, ".")
					break
				}
				if !apiTokenIs(tokens, n+1, ".") {
					return -1
				}
			}
			if packageName == "" {
				return -1
			}
		}
		if !slices.Contains([]string{"class", "interface", "enum", "record"}, t.text) ||
			!apiTokenIs(tokens, i+1, name) {
			continue
		}
		public := false
		for n := i - 1; n >= 0 && slices.Contains([]string{"public", "abstract", "final", "sealed", "strictfp"}, tokens[n].text); n-- {
			public = public || tokens[n].text == "public"
		}
		if !public || found >= 0 {
			return -1
		}
		for n := i + 2; n < len(tokens) && tokens[n].parent == -1; n++ {
			if tokens[n].text == "{" {
				found = n
				break
			}
			if tokens[n].text == ";" || tokens[n].text == "=" {
				return -1
			}
			if tokens[n].close > n {
				n = tokens[n].close
			}
		}
	}
	actual := name
	if packageName != "" {
		actual = packageName + "." + name
	}
	if actual != qualified {
		return -1
	}
	return found
}

func apiAnnotationEnd(tokens []apiToken, start, limit int) (int, bool) {
	i := start + 1
	for i < limit && tokens[i].name {
		i++
		if !apiTokenIs(tokens, i, ".") {
			if apiTokenIs(tokens, i, "(") {
				i = tokens[i].close + 1
			}
			return i, i > start+1 && i < limit
		}
		i++
	}
	return 0, false
}

func publicAPIMethod(source string, tokens []apiToken, owner, from int, class string) (signature string, bodyStart, bodyEnd int) {
	limit := tokens[owner].close
	i := from
	for i < limit && apiTokenIs(tokens, i, "@") {
		n, ok := apiAnnotationEnd(tokens, i, limit)
		if !ok {
			return "", 0, 0
		}
		i = n
	}
	public := false
	for i < limit && slices.Contains([]string{"public", "static", "final", "synchronized", "strictfp", "native", "abstract", "default"}, tokens[i].text) {
		public = public || tokens[i].text == "public"
		i++
	}
	if !public {
		return "", 0, 0
	}
	start := i
	for i < limit && tokens[i].parent == owner && !apiTokenIs(tokens, i, "(") {
		if !(tokens[i].name || slices.Contains([]string{".", "<", ">", ",", "?", "&", "["}, tokens[i].text)) {
			return "", 0, 0
		}
		if tokens[i].text == "[" {
			i = tokens[i].close
		}
		i++
	}
	if i >= limit || !apiTokenIs(tokens, i, "(") || i-start < 2 || !tokens[i-1].name || tokens[i-1].text == class {
		return "", 0, 0
	}
	end := tokens[i].close
	if end <= i || end >= limit {
		return "", 0, 0
	}
	after := end + 1
	if apiTokenIs(tokens, after, "throws") {
		after++
		for after < limit && (tokens[after].name || tokens[after].text == "." || tokens[after].text == ",") {
			after++
		}
	}
	if after >= limit || (!apiTokenIs(tokens, after, "{") && !apiTokenIs(tokens, after, ";")) {
		return "", 0, 0
	}
	for n := i + 1; n < end; n++ {
		if tokens[n].comment || strings.ContainsAny(tokens[n].text, "\r\n=\"'") {
			return "", 0, 0
		}
	}
	args := strings.Join(strings.Fields(source[tokens[i].end:tokens[end].start]), " ")
	sig := tokens[i-1].text + "(" + args + ")"
	if len(sig) > 256 {
		return "", 0, 0
	}
	if apiTokenIs(tokens, after, "{") {
		first, last := after+1, tokens[after].close-1
		for first <= last && tokens[first].comment {
			first++
		}
		for last >= first && tokens[last].comment {
			last--
		}
		if first <= last {
			return sig, tokens[first].line, tokens[last].last
		}
	}
	return sig, 0, 0
}

// APIDocuments accepts only comments wholly on their own source lines and
// adjacent to a public method of the file's conventional public type.
func APIDocuments(pathname, body string) []APIDocument {
	qualified, name := conventionalJavaAPI(pathname)
	if qualified == "" {
		return nil
	}
	tokens := tokenizeAPI(body)
	owner := publicAPIClass(tokens, qualified, name)
	if owner < 0 {
		return nil
	}
	var docs []APIDocument
	for i := owner + 1; i < tokens[owner].close; i++ {
		t := tokens[i]
		if t.parent != owner || !t.comment || !strings.HasPrefix(t.text, "/**") || strings.HasPrefix(t.text, "/***") {
			continue
		}
		lineStart := strings.LastIndexByte(body[:t.start], '\n') + 1
		after, _, _ := strings.Cut(body[t.end:], "\n")
		if strings.TrimSpace(body[lineStart:t.start]) != "" || strings.TrimSpace(after) != "" ||
			strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t.text, "/**"), "*/")) == "" {
			continue
		}
		sig, first, last := publicAPIMethod(body, tokens, owner, i+1, name)
		if sig != "" {
			docs = append(docs, APIDocument{Start: t.line, End: t.last, Signature: qualified + "#" + sig, BodyStart: first, BodyEnd: last})
		}
	}
	return docs
}
