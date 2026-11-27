package review

import (
	"path"
	"slices"
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
	// local is the name the call uses: the import's binding, which an alias
	// makes differ from symbol.
	local string
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
	tokens, _, ok := lexScan(content, lexMode{python: python, member: member, rust: rust}, limit, 0)
	if !ok {
		return nil
	}
	return tokens
}

// lexMode is how lexScan reads a text: python, member (JavaScript templates
// as one token) and rust (raw strings, lifetimes, nested comments) are the
// shared modes. slashes and jsx are the lead reader's for JavaScript and
// TypeScript (member mode). With slashes a "/" after an operand
// (slashDivides) is the division operator, and a "/" where an operand
// starts (slashRegex) opens a regular expression literal, one opaque token
// like a string; a template literal is one opaque token too, its ${...}
// expressions read as code to the "}" that ends each. A "/" where neither
// can be told (after "}" or "<") and a "\" outside a literal still abandon
// the scan. With jsx a "<" where an operand starts opens JSX markup
// (jsxMarkup), read as opaque tokens, its {...} expressions as code.
type lexMode struct {
	python, member, rust, slashes, jsx bool
	// count is the number of tokens the scan has read, those of its
	// template expressions' own scans included (lexScan sets it).
	count *int
}

// lexScan reads content in mode m. With nesting above 0 it reads the
// expression of a template's ${...} (slashes mode): the "}" that closes no
// bracket ends it, and used counts the bytes read through that "}". The
// limit bounds the tokens of the whole scan, a template expression's
// included, so no nesting of templates lets the scan read past it. ok is
// false when the text cannot be separated safely.
func lexScan(content string, m lexMode, limit, nesting int) (tokens []callToken, used int, ok bool) {
	python, member, rust, slashes := m.python, m.member, m.rust, m.slashes
	if m.count == nil {
		m.count = new(int)
	}
	length := len(content)
	var stack []int
	// jsx holds the JSX element trees being read, the innermost last.
	var jsx []jsxTree
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
		if *m.count >= limit || len(stack) > callMaxDepth || len(jsx) > callMaxDepth {
			return nil, 0, false
		}
		t := callToken{line: line, column: column, depth: len(stack), parent: -1}
		if len(stack) > 0 {
			t.parent = stack[len(stack)-1]
		}
		if n := len(jsx); n > 0 && jsx[n-1].container < 0 && !jsx[n-1].pending {
			// Markup: one opaque token up to an expression container or
			// the tree's end.
			size, stop := jsxMarkup(content, &jsx[n-1])
			switch stop {
			case jsxUnread:
				return nil, 0, false
			case jsxContainer:
				jsx[n-1].pending = true
			case jsxEnd:
				jsx = jsx[:n-1]
			}
			if size > 0 {
				t.stringLiteral = true
				advance(content[:size])
				t.endLine = line
				tokens = append(tokens, t)
				*m.count++
				content = content[size:]
			}
			continue
		}
		c, size := content[0], 1
		switch {
		case c == ' ' || c == '\r' || c == '\n' || c == '\t' || c == '\f':
			advance(content[:1])
			content = content[1:]
			continue
		case (python && c == '#') || (!python && strings.HasPrefix(content, "//")) ||
			slashes && nesting == 0 && len(content) == length && strings.HasPrefix(content, "#!"):
			// A hashbang line opening a script is a comment too.
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
					return nil, 0, false
				}
				advance(content[:size])
				content = content[size:]
				continue
			}
			end := strings.Index(content[2:], "*/")
			if end < 0 {
				return nil, 0, false
			}
			size = end + 4
			advance(content[:size])
			content = content[size:]
			continue
		case !python && member && c == '`':
			if slashes {
				size, t.stringLiteral = codeTemplateSize(content, m, limit, nesting), true
			} else {
				size = memberTemplateSize(content)
			}
			if size == 0 {
				return nil, 0, false
			}
		case rust && rustRawStringPrefix(content) != "":
			prefix := rustRawStringPrefix(content)
			end := `"` + prefix[1:len(prefix)-1]
			at := strings.Index(content[len(prefix):], end)
			if at < 0 {
				return nil, 0, false
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
				return nil, 0, false
			}
			raw := content[len(quote):size]
			if c == '`' && (strings.Contains(raw, "${") || strings.Contains(raw, "\\")) {
				return nil, 0, false
			}
			if len(quote) == 1 && c != '`' && strings.Contains(raw, "\n") && !strings.Contains(raw, "\\") {
				return nil, 0, false
			}
			if len(quote) == 1 && c != '`' && !strings.ContainsAny(raw, "\\\n") {
				t.text, t.quoted = raw, true
			}
			size += len(quote)
		case slashes && c == '/' && slashDivides(tokens):
			t.text = "/"
		case slashes && c == '/' && slashRegex(tokens):
			size = regexSize(content)
			if size == 0 {
				return nil, 0, false
			}
			t.text, t.stringLiteral = content[:size], true
		case m.jsx && c == '<' && !slashDivides(tokens) && slashRegex(tokens) && !tokenIs(tokens, len(tokens)-1, ".") &&
			!tsTypeParameters(content[:min(len(content), tsLookahead)]):
			// Where an operand starts, "<" opens JSX markup, or nothing the
			// scan can read.
			if !jsxOpens(content) {
				return nil, 0, false
			}
			jsx = append(jsx, jsxTree{container: -1})
			continue
		case !python && !rust && (c == '/' || c == '\\'):
			return nil, 0, false
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
				if n := len(jsx); c == '{' && n > 0 && jsx[n-1].pending {
					jsx[n-1].pending, jsx[n-1].container = false, len(tokens)
				}
				stack = append(stack, len(tokens))
			case ')', ']', '}':
				matching := "([{"[strings.IndexByte(")]}", c)]
				if len(stack) == 0 {
					if nesting > 0 && c == '}' && len(jsx) == 0 {
						return tokens, length - len(content) + 1, true
					}
					return nil, 0, false
				}
				open := stack[len(stack)-1]
				if tokens[open].text[0] != matching {
					return nil, 0, false
				}
				tokens[open].close = len(tokens)
				stack = stack[:len(stack)-1]
				if n := len(jsx); n > 0 && jsx[n-1].container == open {
					// The container closes: its markup goes on.
					jsx[n-1].container = -1
				}
			}
		}
		advance(content[:size])
		t.endLine = line
		tokens = append(tokens, t)
		*m.count++
		content = content[size:]
	}
	if len(stack) != 0 || nesting > 0 || len(jsx) != 0 {
		return nil, 0, false
	}
	return tokens, length, true
}

// jsxTree is one JSX element tree lexScan reads: the names of its open
// elements (tags; "" for a fragment), whether the reading is inside the
// opening tag of the element name (its attributes), the "{" token of the
// expression container the reading is in (container, -1 for none) and
// whether a "{" opening one comes next (pending).
type jsxTree struct {
	tags      []string
	tag       bool
	name      string
	container int
	pending   bool
}

// What jsxMarkup stopped at.
const (
	jsxUnread    = iota // markup the reader cannot follow
	jsxContainer        // the "{" of an expression container
	jsxEnd              // the end of the tree's outermost element
)

// jsxMarkup reads the JSX markup of tree x from the start of content (an
// element's "<", or the markup after an expression container's "}") up to
// the next container or the end of the tree: tags, their element and
// attribute names, attribute strings (no escapes, as JSX has none), comments
// inside tags, and text, which holds no ">" or "}" (JSX rejects both). An
// attribute whose value is an element, type arguments on an element, a
// closing tag of another element and an unterminated tree are not read.
func jsxMarkup(content string, x *jsxTree) (int, int) {
	i := 0
	for {
		switch {
		case x.tag:
			if i = jsxSpace(content, i); i < 0 || i >= len(content) {
				return 0, jsxUnread
			}
			switch c := content[i]; {
			case c == '/':
				if !strings.HasPrefix(content[i:], "/>") {
					return 0, jsxUnread
				}
				i += 2
				x.tag = false
				if len(x.tags) == 0 {
					return i, jsxEnd
				}
			case c == '>':
				i++
				x.tag = false
				if x.tags = append(x.tags, x.name); len(x.tags) > callMaxDepth {
					return 0, jsxUnread
				}
			case c == '{':
				return i, jsxContainer // a spread attribute
			default:
				n := jsxName(content[i:], false)
				if n == 0 {
					return 0, jsxUnread
				}
				i += n
				j := jsxSpace(content, i)
				if j < 0 || j >= len(content) || content[j] != '=' {
					continue // a boolean attribute
				}
				if j = jsxSpace(content, j+1); j < 0 || j >= len(content) {
					return 0, jsxUnread
				}
				switch q := content[j]; q {
				case '"', '\'':
					end := strings.IndexByte(content[j+1:], q)
					if end < 0 {
						return 0, jsxUnread
					}
					i = j + end + 2
				case '{':
					return j, jsxContainer
				default:
					return 0, jsxUnread
				}
			}
		case i < len(content) && content[i] == '<':
			closing := strings.HasPrefix(content[i:], "</")
			j := i + 1
			if closing {
				j++
			}
			if j = jsxSpace(content, j); j < 0 || j >= len(content) {
				return 0, jsxUnread
			}
			n := 0
			if content[j] != '>' {
				if n = jsxName(content[j:], true); n == 0 {
					return 0, jsxUnread
				}
			}
			name := content[j : j+n]
			j += n
			switch {
			case !closing && n == 0:
				// A fragment.
				if x.tags = append(x.tags, ""); len(x.tags) > callMaxDepth {
					return 0, jsxUnread
				}
				i = j + 1
			case !closing:
				x.tag, x.name = true, name
				i = j
			default:
				if j = jsxSpace(content, j); j < 0 || j >= len(content) || content[j] != '>' || len(x.tags) == 0 || x.tags[len(x.tags)-1] != name {
					return 0, jsxUnread
				}
				x.tags = x.tags[:len(x.tags)-1]
				i = j + 1
				if len(x.tags) == 0 {
					return i, jsxEnd
				}
			}
		default:
			// Text, up to a tag or an expression container.
			end := strings.IndexAny(content[i:], "<{>}")
			if end < 0 || content[i+end] == '>' || content[i+end] == '}' {
				return 0, jsxUnread
			}
			if i += end; content[i] == '{' {
				return i, jsxContainer
			}
		}
	}
}

// jsxSpace is the index after the whitespace and comments at content[i:],
// -1 at an unterminated comment.
func jsxSpace(content string, i int) int {
	for i < len(content) {
		switch {
		case strings.IndexByte(" \t\r\n\f", content[i]) >= 0:
			i++
		case strings.HasPrefix(content[i:], "//"):
			end := strings.IndexByte(content[i:], '\n')
			if end < 0 {
				return len(content)
			}
			i += end
		case strings.HasPrefix(content[i:], "/*"):
			end := strings.Index(content[i+2:], "*/")
			if end < 0 {
				return -1
			}
			i += end + 4
		default:
			return i
		}
	}
	return i
}

// jsxName is the length of the JSX name content starts with, 0 for none:
// identifiers that may hold "-", joined by ":" (a namespace) or, in an
// element's name, by "." (a member).
func jsxName(content string, element bool) int {
	i := 0
	for {
		n := identifierSize(content[i:], true)
		if n == 0 {
			return 0
		}
		i += n
		if i+1 >= len(content) || !(content[i] == ':' || element && content[i] == '.') || identifierSize(content[i+1:], true) == 0 {
			return i
		}
		i++
	}
}

// identifierSize is the length of the JavaScript identifier content starts
// with (with "-" in it when dashes), 0 for none.
func identifierSize(content string, dashes bool) int {
	size := 0
	for size < len(content) {
		r, n := utf8.DecodeRuneInString(content[size:])
		if !(unicode.IsLetter(r) || r == '_' || r == '$' || size > 0 && (unicode.IsDigit(r) || unicode.IsMark(r) || dashes && r == '-')) {
			break
		}
		size += n
	}
	return size
}

// jsxOpens reports a "<" before an element name or ">" (a fragment).
func jsxOpens(content string) bool {
	return len(content) > 1 && (content[1] == '>' || identifierSize(content[1:], false) > 0)
}

// tsLookahead bounds the text tsTypeParameters reads after a "<", so that
// every "<" where an operand starts costs the scan a bounded read (the scan
// reads untrusted source, and an unbounded search for the ")" of each
// <T>(... would make a file of many such "<" cost its length squared). A
// <T>(...) whose ")" lies farther is read as the start of JSX markup
// instead, and that markup reads no further (JSX text holds no ">" of an
// "=>", and no closing tag ends the element): the file stays unread, not
// mis-read.
const tsLookahead = 2 << 10

// tsTypeParameters reports a "<" (content's first byte) opening the type
// parameters of a TypeScript generic arrow function or function type, not
// JSX: TypeScript's own reading of <T,>, <T = U> and <T extends U> (not an
// attribute named extends), after an optional const, and <T>(...) followed
// by "=>" or ":" (no JSX text holds the ">" of "=>"). It reads content only,
// which lexScan cuts to tsLookahead.
func tsTypeParameters(content string) bool {
	i := blankEnd(content, 1)
	if strings.HasPrefix(content[i:], "const") && blankEnd(content, i+5) > i+5 {
		i = blankEnd(content, i+5)
	}
	n := identifierSize(content[i:], false)
	if n == 0 {
		return false
	}
	if i = blankEnd(content, i+n); i >= len(content) {
		return false
	}
	switch {
	case content[i] == ',' || content[i] == '=' && !strings.HasPrefix(content[i:], "=>"):
		return true
	case identifierSize(content[i:], false) == 7 && strings.HasPrefix(content[i:], "extends"):
		j := blankEnd(content, i+7)
		return j < len(content) && strings.IndexByte("=>/", content[j]) < 0
	case content[i] == '>':
		j := blankEnd(content, i+1)
		if j >= len(content) || content[j] != '(' {
			return false
		}
		end := parenEnd(content, j)
		if end < 0 {
			return false
		}
		k := blankEnd(content, end+1)
		return strings.HasPrefix(content[k:], "=>") || strings.HasPrefix(content[k:], ":")
	}
	return false
}

// blankEnd is the index after the whitespace at content[i:].
func blankEnd(content string, i int) int {
	for i < len(content) && strings.IndexByte(" \t\r\n\f", content[i]) >= 0 {
		i++
	}
	return i
}

// parenEnd is the index of the ")" closing the "(" at content[open], past
// nested brackets, strings, templates and comments; -1 when none is found.
func parenEnd(content string, open int) int {
	depth := 0
	for i := open; i < len(content); i++ {
		switch c := content[i]; {
		case c == '(':
			depth++
		case c == ')':
			if depth--; depth == 0 {
				return i
			}
		case c == '\'' || c == '"' || c == '`':
			for i++; i < len(content) && content[i] != c; i++ {
				if content[i] == '\\' {
					i++
				} else if c != '`' && content[i] == '\n' {
					return -1
				}
			}
			if i >= len(content) {
				return -1
			}
		case strings.HasPrefix(content[i:], "//"):
			end := strings.IndexByte(content[i:], '\n')
			if end < 0 {
				return -1
			}
			i += end
		case strings.HasPrefix(content[i:], "/*"):
			end := strings.Index(content[i+2:], "*/")
			if end < 0 {
				return -1
			}
			i += end + 3
		}
	}
	return -1
}

// slashDivides reports a "/" after an operand: the last token is a string,
// template or regular expression literal, a name that is no keyword before
// an expression (a property name after "." is none), a digit, "]", or a ")"
// whose "(" follows no if, while, for or with (a method of that name after
// "." is none). A TypeScript non-null "!" or a postfix "++" or "--" on the
// operand's line leaves the operand ended.
func slashDivides(tokens []callToken) bool {
	last := len(tokens) - 1
	for last > 0 {
		t := tokens[last]
		if tokenIs(tokens, last, "!") && tokens[last-1].endLine == t.line {
			last--
			continue
		}
		if (tokenIs(tokens, last, "+") || tokenIs(tokens, last, "-")) && last > 1 && tokenIs(tokens, last-1, t.text) &&
			tokens[last-1].line == t.line && tokens[last-1].column+1 == t.column && tokens[last-2].endLine == t.line {
			last -= 2
			continue
		}
		break
	}
	if last < 0 {
		return false
	}
	t := tokens[last]
	switch {
	case t.stringLiteral:
		return true
	case t.name:
		if tokenIs(tokens, last-1, ".") {
			return true
		}
		switch t.text {
		case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else",
			"yield", "await", "extends", "export", "import", "default", "if", "while", "for", "with", "as", "satisfies":
			return false
		}
		return true
	case len(t.text) == 1 && t.text[0] >= '0' && t.text[0] <= '9', t.text == "]":
		return true
	case t.text == ")":
		// A ")" token's parent is its "("; a method named like the
		// keyword (arr.with(...), Symbol.for(...)) opens no condition.
		open := t.parent
		condition := tokenIs(tokens, open-1, "if") || tokenIs(tokens, open-1, "while") || tokenIs(tokens, open-1, "for") || tokenIs(tokens, open-1, "with")
		return open >= 0 && !(condition && !tokenIs(tokens, open-2, "."))
	}
	return false
}

// regexKeywords are the keywords an expression, so a regular expression
// literal, may follow.
var regexKeywords = []string{"return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else",
	"yield", "await", "extends", "default"}

// slashRegex reports a "/" that slashDivides does not read as division and
// that opens a regular expression literal because an operand starts there:
// at the start of the text, after an opening bracket or an operator, after a
// keyword an expression follows, or after the ")" of an if, while, for or
// with condition. After "}" (a block or an object literal may end there),
// after "<" (a JSX closing tag) and after a keyword no expression follows,
// the "/" is not read.
func slashRegex(tokens []callToken) bool {
	if len(tokens) == 0 {
		return true
	}
	t := tokens[len(tokens)-1]
	switch {
	case t.quoted || t.stringLiteral:
		return false
	case t.name:
		return slices.Contains(regexKeywords, t.text)
	case t.text == ")":
		return true
	}
	return len(t.text) == 1 && strings.Contains("([{,;:=!?&|^~+-*%>.", t.text)
}

// regexSize is the length of the regular expression literal content opens:
// its body to the first "/" no "\" escapes and no character class holds,
// then its flags; 0 when a line or the text ends first.
func regexSize(content string) int {
	class := false
	for i := 1; i < len(content); i++ {
		switch c := content[i]; {
		case c == '\\':
			i++
			if i < len(content) && (content[i] == '\n' || content[i] == '\r') {
				return 0
			}
		case c == '\n' || c == '\r' || strings.HasPrefix(content[i:], "\u2028") || strings.HasPrefix(content[i:], "\u2029"):
			return 0
		case c == '[':
			class = true
		case c == ']':
			class = false
		case c == '/' && !class:
			i++
			for i < len(content) {
				r, n := utf8.DecodeRuneInString(content[i:])
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$' {
					break
				}
				i += n
			}
			return i
		}
	}
	return 0
}

// codeTemplateSize is the length of the template literal content opens, its
// ${...} expressions read as code (lexScan, nesting one deeper, their
// tokens counted against the scan's limit) to the "}" that ends each, so
// their strings, comments, templates and regular expressions are their own;
// 0 when the template cannot be read.
func codeTemplateSize(content string, m lexMode, limit, nesting int) int {
	if nesting >= callMaxDepth {
		return 0
	}
	for i := 1; i < len(content); i++ {
		switch content[i] {
		case '\\':
			i++
		case '`':
			return i + 1
		case '$':
			if i+1 < len(content) && content[i+1] == '{' {
				_, used, ok := lexScan(content[i+2:], m, limit, nesting+1)
				if !ok {
					return 0
				}
				i += 1 + used
			}
		}
	}
	return 0
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
				out = append(out, callSite{module: names[0].module, symbol: names[0].symbol, line: t.line, end: tokens[close].line, local: t.text})
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
	decl, body, ok := directDeclaration(tokens, symbol, python)
	if !ok {
		return 0, 0
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

// directDeclaration finds the declaration directDefinition accepts: its
// first token and the token opening its body.
func directDeclaration(tokens []callToken, symbol string, python bool) (decl, body int, ok bool) {
	if python && pythonDynamicBindings(tokens) {
		return 0, 0, false
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
				return 0, 0, false
			}
			continue
		}
		if name >= 0 || t.depth != 0 {
			return 0, 0, false
		}
		name = i
	}
	if name < 0 || !tokenIs(tokens, name+1, "(") {
		return 0, 0, false
	}
	decl = name - 1
	if tokenIs(tokens, decl-1, "async") {
		decl--
	}
	if python {
		if tokens[decl].column != 0 {
			return 0, 0, false
		}
		for i := decl - 1; i >= 0; i-- {
			if tokens[i].depth == 0 && tokens[i].column == 0 {
				if tokenIs(tokens, i, "@") {
					return 0, 0, false
				}
				break
			}
		}
	} else {
		decl--
		if !tokenIs(tokens, decl, "export") {
			return 0, 0, false
		}
	}
	close := tokens[name+1].close
	body = close + 1
	if python {
		// A return annotation can follow the parameter list.
		for body < len(tokens) && tokens[body].line == tokens[close].line && !tokenIs(tokens, body, ":") {
			body++
		}
		if !tokenIs(tokens, body, ":") {
			return 0, 0, false
		}
	} else {
		// Accept a simple return annotation before the opening brace.
		if tokenIs(tokens, body, ":") && tokenName(tokens, body+1) {
			body += 2
		}
		if !tokenIs(tokens, body, "{") {
			return 0, 0, false
		}
	}
	return decl, body, true
}
