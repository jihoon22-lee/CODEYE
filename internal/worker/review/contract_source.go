package review

import (
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

var (
	rustTestAttribute = regexp.MustCompile(`#\s*\[\s*(?:cfg\s*\(\s*test\s*\)|test)\s*\]`)
	contractDecision  = regexp.MustCompile(`(?:^|[\s{;])(return|raise|throw|if|match|switch|for|while|yield)[\s(;]`)
	contractAction    = regexp.MustCompile(`(?:^|[\s{;])(await|defer|go)[\s(;]|^(?:(?:const|let|var)\s+)?[A-Za-z_][A-Za-z0-9_.]*\s*:?=\s*[^=;\s]|^[A-Za-z_][A-Za-z0-9_.]*\s*\([^)]*\)\s*;?$`)
)

func contractRuntimePath(path, kind string) bool {
	return kind == "code" && !typeDeclarationPath(path) && !strings.HasSuffix(strings.ToLower(path), ".pyi")
}

// A location can guide review when it contains implementation activity.
// This lexical floor does not establish a condition, result or caller.
func contractExecutableLines(text, path string, visit func(line int, decision bool) bool) {
	text = contractExecutableSource(text, record.SourceEcosystem(path))
	navigationSourceLines(strings.Split(text, "\n"), func(line int, raw string) bool {
		action, decision := contractLineActivity(raw)
		return action && visit(line, decision)
	})
}

func contractLineActivity(raw string) (action, decision bool) {
	raw = strings.TrimSpace(raw)
	decision = contractDecision.MatchString(raw)
	return decision || contractAction.MatchString(raw), decision
}

// contractExecutableSource masks syntax that looks executable to a line
// matcher while preserving byte and line positions in the snapshot.
func contractExecutableSource(text, ecosystem string) string {
	javascript, rust := ecosystem == "javascript", ecosystem == "rust"
	c := ecosystem == "c"
	if !javascript && !rust && !c {
		return text
	}
	out := []byte(text)
	for i := 0; i < len(text); {
		tail := text[i:]
		n := 1
		switch {
		case strings.HasPrefix(tail, "//"):
			n = strings.IndexByte(tail, '\n')
			if n < 0 {
				n = len(tail)
			}
		case strings.HasPrefix(tail, "/*"):
			n = contractBlockCommentSize(tail, rust)
		case javascript && tail[0] == '`':
			n = memberTemplateSize(tail)
			if n == 0 {
				// Unknown nested template syntax is opaque.
				n = len(tail)
			}
		case rust && rustRawStringSize(tail) > 0:
			n = rustRawStringSize(tail)
		case tail[0] == '"' || ((javascript || c) && tail[0] == '\''):
			n = contractQuotedSize(tail)
		case rust && tail[0] == '\'':
			if n = contractQuotedSize(tail); n <= 1 || n > 8 {
				// Rust lifetimes share the quote prefix with character literals.
				i++
				continue
			}
		default:
			i++
			continue
		}
		maskContractRange(out[i : i+n])
		i += n
	}
	if rust {
		for _, at := range rustTestAttribute.FindAllIndex(out, -1) {
			if out[at[0]] == ' ' {
				// A previous attribute already covered this nested item.
				continue
			}
			maskContractRange(out[at[0]:rustTestItemEnd(out, at[1])])
		}
	}
	return string(out)
}

func maskContractRange(bytes []byte) {
	for i, c := range bytes {
		if c != '\n' && c != '\r' {
			bytes[i] = ' '
		}
	}
}

func contractBlockCommentSize(text string, nested bool) int {
	depth := 1
	for i := 2; i+1 < len(text); i++ {
		switch text[i : i+2] {
		case "/*":
			if nested {
				depth++
				i++
			}
		case "*/":
			depth--
			if depth == 0 {
				return i + 2
			}
			i++
		}
	}
	return len(text)
}

func contractQuotedSize(text string) int {
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case text[0]:
			return i + 1
		case '\n':
			if text[0] == '\'' {
				return 1
			}
		}
	}
	return len(text)
}

func rustRawStringSize(text string) int {
	if text[0] != 'r' {
		return 0
	}
	i := 1
	for i < len(text) && text[i] == '#' {
		i++
	}
	if i == len(text) || text[i] != '"' {
		return 0
	}
	close := "\"" + text[1:i]
	end := strings.Index(text[i+1:], close)
	if end < 0 {
		return len(text)
	}
	return i + 1 + end + len(close)
}

// rustTestItemEnd follows the attributed item, including nested braces and
// any additional attributes. Literal and comment delimiters were masked.
func rustTestItemEnd(source []byte, start int) int {
	var parens, brackets, braces int
	for i := start; i < len(source); i++ {
		switch source[i] {
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			brackets++
		case ']':
			brackets--
		case '{':
			braces++
		case '}':
			if braces == 0 {
				return i
			}
			braces--
			if braces == 0 && parens == 0 && brackets == 0 {
				return i + 1
			}
		case ';':
			if braces == 0 && parens == 0 && brackets == 0 {
				return i + 1
			}
		}
	}
	return len(source)
}
