package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Definition leads in C and C++ files (docs/REVIEW.md#단서-해석). A plain call
// of the lead's name on the anchor lines binds by the language's lookup over
// the anchor and the project headers its quoted includes reach (four
// levels, sixteen headers; a system <...> include is not read and does not
// stop the reading). A declaration of the name in the enclosing function (a
// parameter, a local, a structured binding) is no project function; else
// the innermost namespace scope declaring the name decides (the call's
// namespaces outward, the file last). There a variable of the name binds
// the call to no function; a conditional declaration, two signatures (C++
// overloads) or two definitions are ambiguous; one definition is the
// binding (a forward or static prototype beside it in the same file, or a
// header prototype, names the same function). With prototypes only, the
// definition is the one non-static, unconditional definition of the scope
// (and, in C++, of the signature) among the readable C and C++ sources,
// sent first with the header prototype marked partial after it; without
// exactly one (or once the scan reached its limit, or with a quoted include
// left unread) the header prototype alone. In C++ that binding is ambiguous
// when a function of the name may be found otherwise: one in an inline
// namespace, or, for a call with arguments, one in a namespace the lookup
// does not reach or a class's friend (argument-dependent lookup decides by
// the arguments' types). A macro of the name, a using directive or
// declaration, a call in a class, a member function, an initializer's
// braces or a lambda at namespace scope bind nothing; so does, in C++ with a
// system include, a name the C library also declares. A name in a template
// parameter list declares nothing. A header anchor reads with C++ rules (no
// translation unit says which language includes it); an included header
// follows its includer.

// nativeUnit is one file of a C or C++ translation unit: its tokens
// (comments, strings and preprocessor lines blanked), its directives and,
// per line, whether a conditional block holds it. conditional reports a
// header the anchor reaches through a conditional include.
type nativeUnit struct {
	path        string
	tokens      []callToken
	directives  []nativeDirective
	cond        []bool
	conditional bool
}

// newNativeUnit reads the unit of file p; nil when its text cannot be
// tokenized. A file of preprocessor lines alone (its macros, its includes)
// is a unit without tokens.
func newNativeUnit(p, text string) *nativeUnit {
	tokens, ok := leadTokenScan("c", false, text)
	if !ok {
		return nil
	}
	u := &nativeUnit{path: p, tokens: tokens}
	u.directives, u.cond = nativeDirectives(text)
	return u
}

// nativeExpressionWords are the C and C++ keywords an expression operand may
// follow: a name after one is no declarator.
var nativeExpressionWords = map[string]bool{
	"return": true, "sizeof": true, "case": true, "goto": true, "else": true, "do": true, "throw": true, "new": true, "delete": true,
	"co_return": true, "co_yield": true, "co_await": true, "typeid": true, "alignof": true, "_Alignof": true, "decltype": true,
	"typeof": true, "__typeof__": true, "not": true, "and": true, "or": true, "xor": true, "bitand": true, "bitor": true, "compl": true,
	"not_eq": true, "and_eq": true, "or_eq": true, "xor_eq": true, "if": true, "while": true, "for": true, "switch": true,
}

// nativeSkipNames are the names a "(" follows in a declaration's head that
// are no function name.
var nativeSkipNames = []string{"__attribute__", "__declspec", "alignas", "_Alignas", "decltype", "__typeof__", "typeof", "noexcept", "throw", "sizeof", "__asm__", "asm"}

// nativeTypeToken reports token k as one a declarator may follow: a name
// that is no expression keyword, the ">" of a template type, or the ")" of
// a type specifier or attribute (decltype(...), __typeof__(...),
// __attribute__((...))).
func nativeTypeToken(t []callToken, k int) bool {
	return tokenName(t, k) && !nativeExpressionWords[t[k].text] || tokenIs(t, k, ">") && !tokenIs(t, k-1, "-") ||
		tokenIs(t, k, ")") && t[k].parent >= 1 && slices.Contains(nativeSpecifiers, t[t[k].parent-1].text) && !t[t[k].parent-1].quoted
}

// nativeSpecifiers are the names whose parenthesized operand makes a type
// or an attribute of a declaration.
var nativeSpecifiers = []string{"decltype", "typeof", "__typeof__", "__typeof", "__attribute__", "__declspec", "alignas", "_Alignas"}

// start is the first token of the statement holding token i at its depth.
func (u *nativeUnit) start(i int) int {
	t := u.tokens
	d := t[i].depth
	at := i
	for k := i - 1; k >= 0; k-- {
		if t[k].depth > d {
			continue
		}
		if t[k].depth < d || tokenIs(t, k, ";") || tokenIs(t, k, "{") {
			break
		}
		at = k
	}
	return at
}

// head lists the tokens at the depth of "{" token b from its statement's
// start up to b.
func (u *nativeUnit) head(b int) []int {
	t := u.tokens
	var out []int
	for k := u.start(b); k < b; k++ {
		if t[k].depth == t[b].depth {
			out = append(out, k)
		}
	}
	return out
}

// nativeBlock is what a "{" opens: extern (a linkage block), namespace
// (names its path, "" for an anonymous one), inline (an inline namespace),
// function (name its name token, params its "(", member a name after "::",
// "~" or operator), class, init (an initializer, a lambda at namespace
// scope) or "".
type nativeBlock struct {
	kind         string
	names        []string
	name, params int
	member       bool
}

func (u *nativeUnit) block(b int) nativeBlock {
	t := u.tokens
	h := u.head(b)
	if len(h) == 1 && tokenIs(t, h[0], "extern") {
		return nativeBlock{kind: "extern"}
	}
	for x, k := range h {
		switch {
		case tokenIs(t, k, "namespace"):
			if x > 0 && tokenIs(t, h[x-1], "inline") {
				return nativeBlock{kind: "inline"}
			}
			var names []string
			for _, n := range h[x+1:] {
				switch {
				case tokenName(t, n):
					names = append(names, t[n].text)
				case !tokenIs(t, n, ":"):
					return nativeBlock{}
				}
			}
			if len(names) == 0 {
				names = []string{""}
			}
			return nativeBlock{kind: "namespace", names: names}
		case tokenIs(t, k, "=") && !tokenIs(t, k+1, "="):
			return nativeBlock{kind: "init"}
		}
	}
	for x, k := range h {
		if tokenIs(t, k, "(") && x > 0 && h[x-1] == k-1 && tokenName(t, k-1) && !slices.Contains(nativeSkipNames, t[k-1].text) {
			name := k - 1
			member := tokenIs(t, name-1, ":") && tokenIs(t, name-2, ":") || tokenIs(t, name-1, "~") || tokenIs(t, name-1, "operator")
			return nativeBlock{kind: "function", name: name, params: k, member: member}
		}
	}
	for _, k := range h {
		switch t[k].text {
		case "class", "struct", "union", "enum":
			return nativeBlock{kind: "class"}
		}
	}
	return nativeBlock{}
}

// declScope is the namespace path of token x when it sits at namespace
// scope (in no function, class, initializer or parentheses); false
// otherwise.
func (u *nativeUnit) declScope(x int) ([]string, bool) {
	t := u.tokens
	var chain []int
	for p := t[x].parent; p >= 0; p = t[p].parent {
		chain = append(chain, p)
	}
	var scope []string
	for k := len(chain) - 1; k >= 0; k-- {
		p := chain[k]
		if !tokenIs(t, p, "{") {
			return nil, false
		}
		switch b := u.block(p); b.kind {
		case "extern":
		case "namespace":
			scope = append(scope, b.names...)
		default:
			return nil, false
		}
	}
	return scope, true
}

// callScope is the namespace path of the call at token x, the function
// whose body holds it and that body's "{" (-1 for a call at namespace scope:
// an initializer, a default argument); false for a call in a class, a
// member function, an initializer's braces or a block it cannot read.
func (u *nativeUnit) callScope(x int) (scope []string, fn nativeBlock, body int, ok bool) {
	t := u.tokens
	var chain []int
	for p := t[x].parent; p >= 0; p = t[p].parent {
		chain = append(chain, p)
	}
	body = -1
	for k := len(chain) - 1; k >= 0 && body < 0; k-- {
		p := chain[k]
		if !tokenIs(t, p, "{") {
			continue
		}
		switch b := u.block(p); b.kind {
		case "extern":
		case "namespace":
			scope = append(scope, b.names...)
		case "function":
			if b.member {
				return nil, nativeBlock{}, -1, false
			}
			fn, body = b, p
		default:
			return nil, nativeBlock{}, -1, false
		}
	}
	return scope, fn, body, true
}

// nativeDeclarator reports token i (a name no "(" follows) as the name a
// declaration declares: after a type (int f, T *f, T &f, std::vector<T> f,
// int (*f)(int)), after "," in a declaration list, in a structured binding
// or as a lambda's init-capture.
func nativeDeclarator(t []callToken, i int) bool {
	if p := t[i].parent; p >= 0 && tokenIs(t, p, "[") {
		q := p - 1
		for tokenIs(t, q, "&") {
			q--
		}
		if tokenIs(t, q, "auto") || tokenIs(t, i+1, "=") && !tokenIs(t, i+2, "=") {
			return true
		}
	}
	k := i - 1
	for tokenIs(t, k, "*") || tokenIs(t, k, "&") || tokenIs(t, k, "const") || tokenIs(t, k, "volatile") || tokenIs(t, k, "restrict") {
		k--
	}
	if k < i-1 && tokenIs(t, k, "(") {
		close := t[k].close
		if close != i+1 || !tokenIs(t, close+1, "(") && !tokenIs(t, close+1, "[") {
			return false
		}
		k--
	}
	switch {
	case k < 0:
		return false
	case nativeTypeToken(t, k):
		return true
	case tokenIs(t, k, ","):
		p := t[k].parent
		return p < 0 || tokenIs(t, p, "{")
	}
	return false
}

// shadowed reports a declaration of name in the function fn whose body
// opens at "{" token body: a parameter, a local, a local function
// declaration, a structured binding or an init-capture. using reports a
// using directive or declaration in it.
func (u *nativeUnit) shadowed(fn nativeBlock, body int, name string) (shadow, using bool) {
	t := u.tokens
	for i := fn.params + 1; i < t[body].close; i++ {
		if tokenIs(t, i, "using") {
			using = true
		}
		if !tokenIs(t, i, name) || tokenIs(t, i-1, ".") || tokenIs(t, i-1, ">") && tokenIs(t, i-2, "-") || tokenIs(t, i-1, ":") && tokenIs(t, i-2, ":") {
			continue
		}
		if tokenIs(t, i+1, "(") {
			if nativeTypeToken(t, i-1) && !tokenIs(t, i-1, ">") {
				return true, using
			}
			continue
		}
		if nativeDeclarator(t, i) {
			return true, using
		}
	}
	return false, using
}

// nativeDecl is one declaration of a name at namespace scope: its name
// token, its first and last tokens (the ";" or the body's "}"), its
// namespace path, whether it is a definition (body), static, a variable
// (value: an object or a function pointer) or conditional, and in C++ its
// parameter types (signature, readable when read).
type nativeDecl struct {
	unit                             *nativeUnit
	name, first, last                int
	scope                            []string
	body, static, value, conditional bool
	signature                        string
	readable                         bool
}

// decls lists the declarations of name at namespace scope in the unit. A
// definition qualified by a namespace (int n::f(...) {...}) counts for that
// namespace.
func (u *nativeUnit) decls(name string) []nativeDecl {
	t := u.tokens
	var out []nativeDecl
	for i := range t {
		if !tokenIs(t, i, name) || tokenIs(t, i-1, ".") || tokenIs(t, i-1, "~") || tokenIs(t, i-1, ">") && tokenIs(t, i-2, "-") {
			continue
		}
		if slices.ContainsFunc([]string{"struct", "class", "union", "enum", "goto", "namespace", "using"}, func(k string) bool { return tokenIs(t, i-1, k) }) {
			continue
		}
		// A parenthesized declarator, int (*f)(int): its group stands where
		// the name would.
		if p := t[i].parent; p >= 0 && tokenIs(t, p, "(") && t[p].close == i+1 && p+1 < i &&
			!slices.ContainsFunc(t[p+1:i], func(x callToken) bool { return !slices.Contains([]string{"*", "&", "const", "volatile"}, x.text) }) {
			scope, ok := u.declScope(p)
			start := u.start(p)
			if !ok || nativeTemplateEnd(t, start) > p || slices.ContainsFunc(t[start:p], func(x callToken) bool { return x.depth == t[p].depth && x.text == "=" }) {
				continue // a template parameter, or an initializer's operand
			}
			d := nativeDecl{unit: u, name: i, first: start, scope: scope, value: true, last: u.headEnd(t[p].close+1, t[p].depth)}
			if d.last < 0 {
				continue
			}
			d.conditional = u.conditional || slices.Contains(u.cond[t[d.first].line:min(t[d.last].line, len(u.cond)-1)+1], true)
			out = append(out, d)
			continue
		}
		scope, ok := u.declScope(i)
		if !ok {
			continue
		}
		head := i
		var qualifier []string
		for tokenIs(t, head-1, ":") && tokenIs(t, head-2, ":") {
			if !tokenName(t, head-3) {
				head = -1
				break
			}
			head -= 3
			qualifier = append([]string{t[head].text}, qualifier...)
		}
		if head < 0 {
			continue
		}
		start := u.start(i)
		params := nativeTemplateEnd(t, start)
		if params > i {
			continue // in a template parameter list (template <int N = name>): no declaration
		}
		if slices.ContainsFunc(t[params:head], func(x callToken) bool { return x.depth == t[i].depth && x.text == "=" }) {
			continue // an initializer's operand
		}
		d := nativeDecl{unit: u, name: i, first: start, scope: append(scope, qualifier...)}
		d.static = slices.ContainsFunc(t[start:head], func(x callToken) bool { return x.depth == t[i].depth && x.text == "static" })
		if tokenIs(t, i+1, "(") {
			if head > start && !nativeTypeToken(t, head-1) && !tokenIs(t, head-1, "*") && !tokenIs(t, head-1, "&") {
				continue // a call in an expression
			}
			close := t[i+1].close
			// T f(3): a variable; a number in a default argument (int
			// level = 0) is no sign of one.
			def := false
			for _, x := range t[i+2 : close] {
				switch {
				case x.depth != t[i].depth+1:
				case x.text == ",":
					def = false
				case x.text == "=":
					def = true
				case !def && len(x.text) == 1 && x.text[0] >= '0' && x.text[0] <= '9':
					d.value = true
				}
			}
			end := u.headEnd(close+1, t[i].depth)
			switch {
			case end < 0:
				continue
			case tokenIs(t, end, "{"):
				d.body, d.last = true, t[end].close
			default:
				d.last = end
			}
			d.signature, d.readable = nativeSignature(t, i+1)
		} else {
			if !nativeDeclarator(t, i) {
				continue
			}
			d.value = true
			d.last = u.headEnd(i+1, t[i].depth)
			if d.last < 0 {
				continue
			}
		}
		if len(qualifier) > 0 && !d.body {
			continue
		}
		d.conditional = u.conditional || slices.Contains(u.cond[t[d.first].line:min(t[d.last].line, len(u.cond)-1)+1], true)
		out = append(out, d)
	}
	return out
}

// nativeTemplateEnd is the token after a template parameter list opening a
// declaration at token k (template <class T = int>), else k.
func nativeTemplateEnd(t []callToken, k int) int {
	if !tokenIs(t, k, "template") || !tokenIs(t, k+1, "<") {
		return k
	}
	depth := 0
	for j := k + 1; j < len(t); j++ {
		switch {
		case tokenIs(t, j, "<"):
			depth++
		case tokenIs(t, j, ">") && !tokenIs(t, j-1, "-"):
			depth--
			if depth == 0 {
				return j + 1
			}
		case tokenIs(t, j, ";") || tokenIs(t, j, "{"):
			return k
		}
		if t[j].close > j {
			j = t[j].close
		}
	}
	return k
}

// headEnd is the "{", ";", "," or "=" that ends a declaration's head from
// token k at depth d; -1 when the depth ends first.
func (u *nativeUnit) headEnd(k, d int) int {
	t := u.tokens
	for ; k < len(t); k++ {
		if t[k].depth < d {
			return -1
		}
		if t[k].depth == d && (tokenIs(t, k, "{") || tokenIs(t, k, ";") || tokenIs(t, k, ",") || tokenIs(t, k, "=") && !tokenIs(t, k+1, "=")) {
			return k
		}
		if t[k].close > k {
			k = t[k].close
		}
	}
	return -1
}

// nativeTypeWords are the C and C++ words that build a type and name no
// parameter.
var nativeTypeWords = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true, "float": true, "double": true, "signed": true, "unsigned": true,
	"bool": true, "_Bool": true, "wchar_t": true, "char8_t": true, "char16_t": true, "char32_t": true, "auto": true, "const": true,
	"volatile": true, "struct": true, "union": true, "enum": true, "class": true, "typename": true, "restrict": true, "register": true,
}

// nativeSignature is the parameter types of the list opening at "(" token
// open: each parameter's tokens without its declarator name or default
// argument; false when a parameter is empty.
func nativeSignature(t []callToken, open int) (string, bool) {
	close := t[open].close
	var params [][]int
	var cur []int
	for k := open + 1; k < close; k++ {
		if t[k].depth == t[open].depth+1 && tokenIs(t, k, ",") {
			params = append(params, cur)
			cur = nil
			continue
		}
		cur = append(cur, k)
	}
	if len(cur) > 0 || len(params) > 0 {
		params = append(params, cur)
	}
	var out []string
	for _, p := range params {
		if at := slices.IndexFunc(p, func(k int) bool { return t[k].depth == t[open].depth+1 && tokenIs(t, k, "=") }); at >= 0 {
			p = p[:at]
		}
		if len(p) == 0 {
			return "", false
		}
		var words []string
		for x, k := range p {
			declared := tokenName(t, k) && !nativeTypeWords[t[k].text] && x > 0 && !tokenIs(t, p[x-1], ":") &&
				(x == len(p)-1 || tokenIs(t, p[x+1], "[") || x >= 2 && tokenIs(t, p[x+1], ")") && tokenIs(t, p[x-1], "*") && tokenIs(t, p[x-2], "(")) &&
				slices.ContainsFunc(p[:x], func(q int) bool {
					return !slices.Contains([]string{"const", "volatile", "struct", "union", "enum", "class", "typename", "restrict", "register", "("}, t[q].text)
				})
			if !declared {
				words = append(words, t[k].text)
			}
		}
		out = append(out, strings.Join(words, " "))
	}
	if len(out) == 1 && out[0] == "void" {
		out = nil
	}
	return strings.Join(out, ","), true
}

// macro reports a #define or #undef of name in the unit.
func (u *nativeUnit) macro(name string) bool {
	return slices.ContainsFunc(u.directives, func(d nativeDirective) bool {
		return (d.command == "define" || d.command == "undef") && strings.HasPrefix(d.rest, name) && (len(d.rest) == len(name) || !isIdentByte(d.rest[len(name)]))
	})
}

// using reports a using directive, or a using declaration naming name.
func (u *nativeUnit) using(name string) bool {
	t := u.tokens
	for i := range t {
		if !tokenIs(t, i, "using") {
			continue
		}
		if tokenIs(t, i+1, "namespace") {
			return true
		}
		for k := i + 1; k < len(t) && !tokenIs(t, k, ";"); k++ {
			if tokenIs(t, k, name) {
				return true
			}
		}
	}
	return false
}

// hides reports a declaration of name the namespace lookup does not place:
// one in an inline namespace (its names are visible around it), or a friend
// function a class declares (argument-dependent lookup finds it).
func (u *nativeUnit) hides(name string) (inline, friend bool) {
	t := u.tokens
	for i := range t {
		if !tokenIs(t, i, name) || !tokenIs(t, i+1, "(") || !nativeTypeToken(t, i-1) && !tokenIs(t, i-1, "*") && !tokenIs(t, i-1, "&") {
			continue
		}
		for p := t[i].parent; p >= 0; p = t[p].parent {
			if tokenIs(t, p, "{") && u.block(p).kind == "inline" {
				inline = true
			}
		}
		start := u.start(i)
		if slices.ContainsFunc(t[start:i], func(x callToken) bool { return x.depth == t[i].depth && x.text == "friend" }) {
			friend = true
		}
	}
	return inline, friend
}

// nativeReached reports a namespace path the lookup of a call in namespace
// call reaches: call or a namespace enclosing it, or an anonymous namespace
// directly in one of them.
func nativeReached(d, call []string) bool {
	for k := 0; k <= len(call); k++ {
		if slices.Equal(d, call[:k]) || len(d) == k+1 && d[k] == "" && slices.Equal(d[:k], call[:k]) {
			return true
		}
	}
	return false
}

// systemInclude reports a <...> include in the unit.
func (u *nativeUnit) systemInclude() bool {
	return slices.ContainsFunc(nativeIncludes(u.directives), func(inc nativeInclude) bool { return !inc.quoted })
}

// calls lists the calls of symbol on the anchor lines: name( not after
// ".", "->", "::" or "~", not a declaration's name.
func (u *nativeUnit) calls(anchor record.Excerpt, symbol string) []int {
	t := u.tokens
	var out []int
	for i, tok := range t {
		if !tokenIs(t, i, symbol) || tok.line < anchor.Start || tok.line > anchor.End || !tokenIs(t, i+1, "(") ||
			tokenIs(t, i-1, ".") || tokenIs(t, i-1, "~") || tokenIs(t, i-1, ">") && tokenIs(t, i-2, "-") || tokenIs(t, i-1, ":") && tokenIs(t, i-2, ":") {
			continue
		}
		if nativeTypeToken(t, i-1) && !tokenIs(t, i-1, ">") {
			continue // a declaration: int f(...), T f(3)
		}
		out = append(out, i)
	}
	return out
}

// nativeTU is the anchor's translation unit as a lead reads it: the anchor
// and the project headers its quoted includes reach; complete reports every
// quoted include read.
type nativeTU struct {
	units    []*nativeUnit
	complete bool
}

func (r *leadResolver) nativeTranslationUnit(scan *leadScan, anchor *nativeUnit) (nativeTU, error) {
	tu := nativeTU{units: []*nativeUnit{anchor}, complete: true}
	seen := map[string]bool{anchor.path: true}
	type item struct {
		u     *nativeUnit
		level int
	}
	queue := []item{{anchor, 0}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for _, inc := range nativeIncludes(it.u.directives) {
			if !inc.quoted {
				continue
			}
			h := nativeQuotedFile(r.catalog, it.u.path, inc.name)
			switch {
			case h == "":
				tu.complete = false
				continue
			case seen[h]:
				continue
			}
			seen[h] = true
			if it.level >= 4 || len(tu.units) > 16 {
				tu.complete = false
				continue
			}
			text, ok, err := scan.index.text(h)
			if err != nil {
				return nativeTU{}, err
			}
			u := (*nativeUnit)(nil)
			if ok {
				u = newNativeUnit(h, text)
			}
			if u == nil {
				tu.complete = false
				continue
			}
			u.conditional = it.u.conditional || inc.conditional
			tu.units = append(tu.units, u)
			queue = append(queue, item{u, it.level + 1})
		}
	}
	return tu, nil
}

// nativeQuotedFile is the project file a quoted include of p names: beside
// p, where a compiler looks first; "" when none. A file elsewhere whose path
// ends in the name is not it: only the build's include paths could say so,
// and the include stays unread.
func nativeQuotedFile(c *sourceCallCatalog, p, name string) string {
	return nativeHeader(c.files, p, nativeInclude{name: name, quoted: true})
}

// nativeOutcome is what one call binds to: hits, else local (a declaration
// that is no project function), ambiguous, refused (nothing bound safely)
// or open (no declaration seen, so nothing bound).
type nativeOutcome struct {
	hits                            []leadHit
	local, ambiguous, refused, open bool
}

// nativeHit is the hit of a declaration: its lines; a prototype's target is
// partial.
func nativeHit(d nativeDecl, prototype bool) leadHit {
	t := d.unit.tokens
	loc := sourceCallLocation{d.unit.path, t[d.first].line, t[d.last].endLine}
	return leadHit{loc: loc, origin: "context", extent: loc, partial: prototype}
}

// nativeLead is one lead's reading of the anchor's translation unit, which
// the calls of its name share: the unit, the name's declarations in it,
// whether a macro or a using of the name (in C++ with a system include, the
// C library too) binds the name otherwise, whether a declaration the C++
// lookup does not place exists (in an inline namespace, a class's friend),
// and the definitions in the other sources, read once.
type nativeLead struct {
	r              *leadResolver
	scan           *leadScan
	tu             nativeTU
	symbol, lang   string
	all            []nativeDecl
	otherwise      bool
	inline, friend bool
	external       *nativeExternals
}

func (r *leadResolver) newNativeLead(scan *leadScan, tu nativeTU, symbol, lang string) *nativeLead {
	l := &nativeLead{r: r, scan: scan, tu: tu, symbol: symbol, lang: lang}
	for _, x := range tu.units {
		l.all = append(l.all, x.decls(symbol)...)
		l.otherwise = l.otherwise || x.macro(symbol) || lang == "cpp" && x.using(symbol)
		inline, friend := x.hides(symbol)
		l.inline, l.friend = l.inline || inline, l.friend || friend
	}
	if lang == "cpp" && leadBuiltin("cpp", symbol) && slices.ContainsFunc(tu.units, (*nativeUnit).systemInclude) {
		l.otherwise = true
	}
	return l
}

// bind resolves the call at token i of the anchor unit.
func (l *nativeLead) bind(i int) (nativeOutcome, error) {
	u := l.tu.units[0]
	scope, fn, body, ok := u.callScope(i)
	if !ok {
		return nativeOutcome{refused: true}, nil
	}
	if body >= 0 {
		shadow, using := u.shadowed(fn, body, l.symbol)
		switch {
		case shadow:
			return nativeOutcome{local: true}, nil
		case using:
			return nativeOutcome{refused: true}, nil
		}
	}
	if l.otherwise {
		return nativeOutcome{refused: true}, nil
	}
	for s := scope; ; s = s[:len(s)-1] {
		// An anonymous namespace's names are visible in the scope holding it.
		decls := slices.DeleteFunc(slices.Clone(l.all), func(d nativeDecl) bool {
			return !slices.Equal(d.scope, s) && !(len(d.scope) == len(s)+1 && d.scope[len(s)] == "" && slices.Equal(d.scope[:len(s)], s))
		})
		if len(decls) > 0 {
			out, err := l.decide(decls, s)
			if err == nil && len(out.hits) > 0 && l.lang == "cpp" && l.otherwiseFound(i, scope) {
				return nativeOutcome{ambiguous: true}, nil
			}
			return out, err
		}
		if len(s) == 0 {
			return nativeOutcome{open: true}, nil
		}
	}
}

// otherwiseFound reports a C++ function of the name the call at token i of
// the anchor may bind to outside its namespace lookup, whose binding
// depends on types the reader does not know: a declaration in an inline
// namespace (visible around it) or, for a call with arguments, one that
// argument-dependent lookup may add (a namespace the lookup does not reach,
// a class's friend).
func (l *nativeLead) otherwiseFound(i int, scope []string) bool {
	t := l.tu.units[0].tokens
	args := t[i+1].close > i+2
	return l.inline || args && (l.friend || slices.ContainsFunc(l.all, func(d nativeDecl) bool { return !nativeReached(d.scope, scope) }))
}

// decide binds a call to the declarations of the scope its lookup stops
// at.
func (l *nativeLead) decide(decls []nativeDecl, scope []string) (nativeOutcome, error) {
	switch {
	case slices.ContainsFunc(decls, func(d nativeDecl) bool { return d.value }):
		return nativeOutcome{local: true}, nil
	case slices.ContainsFunc(decls, func(d nativeDecl) bool { return d.conditional }):
		return nativeOutcome{ambiguous: true}, nil
	}
	if l.lang == "cpp" {
		signatures := map[string]bool{}
		for _, d := range decls {
			if !d.readable && len(decls) > 1 {
				return nativeOutcome{ambiguous: true}, nil
			}
			signatures[d.signature] = true
		}
		if len(signatures) > 1 {
			return nativeOutcome{ambiguous: true}, nil
		}
	}
	defs := slices.DeleteFunc(slices.Clone(decls), func(d nativeDecl) bool { return !d.body })
	switch {
	case len(defs) > 1:
		return nativeOutcome{ambiguous: true}, nil
	case len(defs) == 1:
		return nativeOutcome{hits: []leadHit{nativeHit(defs[0], false)}}, nil
	}
	if slices.ContainsFunc(decls, func(d nativeDecl) bool { return d.static || slices.Contains(d.scope, "") }) {
		// Internal linkage: the definition belongs to this unit, and it holds none.
		return nativeOutcome{refused: true}, nil
	}
	anchor := l.tu.units[0]
	at := slices.IndexFunc(decls, func(d nativeDecl) bool { return d.unit != anchor })
	var def leadHit
	found := false
	if l.tu.complete && (l.lang == "c" || decls[0].readable) {
		var err error
		if def, found, err = l.externalDefinition(scope, decls[0].signature); err != nil {
			return nativeOutcome{}, err
		}
	}
	var hits []leadHit
	if found {
		hits = append(hits, def)
	}
	if at >= 0 {
		h := nativeHit(decls[at], true)
		h.secondary = found
		hits = append(hits, h)
	}
	if len(hits) == 0 {
		return nativeOutcome{refused: true}, nil
	}
	return nativeOutcome{hits: hits}, nil
}

// nativeSource reports a C or C++ source a definition is looked for in: C
// files for a C anchor, C and C++ files for a C++ one.
func nativeSource(p, lang string) bool {
	switch path.Ext(p) {
	case ".c":
		return true
	case ".cc", ".cpp", ".cxx", ".c++":
		return lang == "cpp"
	}
	return false
}

// externalDefinition finds the one definition of the name at scope among
// the other sources' (nativeExternals), in C++ of the prototype's
// signature. False when there is none or more than one, or when the
// search could not read every source.
func (l *nativeLead) externalDefinition(scope []string, signature string) (leadHit, bool, error) {
	if l.external == nil {
		x, err := l.r.nativeExternals(l.scan, l.tu, l.lang, l.symbol)
		if err != nil {
			return leadHit{}, false, err
		}
		l.external = &x
	}
	if !l.external.complete {
		return leadHit{}, false, nil
	}
	var found []nativeDecl
	for _, d := range l.external.defs {
		if slices.Equal(d.scope, scope) && (l.lang == "c" || d.readable && d.signature == signature) {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		return leadHit{}, false, nil
	}
	return nativeHit(found[0], false), true, nil
}

// nativeExternals are the definitions of a name at namespace scope (not
// static, not conditional, no variable) in the project's C and C++ sources
// outside a translation unit, those on a secondary path included; complete
// reports a search that read every such source.
type nativeExternals struct {
	defs     []nativeDecl
	complete bool
}

// nativeExternals reads the external definitions of name. The search cannot
// be complete when the scan reached its limit, a source holding the name
// could not be tokenized, or a source could not be read at all (withheld,
// outside the scope or the change, generated, unreadable): that source may
// hold the definition the readable ones only stand in for. A source on a
// secondary path is read for the search alone (checkText): its definition
// counts, so beside another one neither is chosen, and is never sent
// (resolve refuses its window, lead_secondary_path).
func (r *leadResolver) nativeExternals(scan *leadScan, tu nativeTU, lang, name string) (nativeExternals, error) {
	in := map[string]bool{}
	for _, u := range tu.units {
		in[u.path] = true
	}
	var x nativeExternals
	for _, p := range r.catalog.ordered {
		if in[p] || !nativeSource(p, lang) {
			continue
		}
		text, ok, err := scan.checkText(p)
		if err != nil {
			return nativeExternals{}, err
		}
		if scan.limited || !ok {
			return nativeExternals{}, nil
		}
		if !strings.Contains(text, name) {
			continue
		}
		u := newNativeUnit(p, text)
		if u == nil {
			return nativeExternals{}, nil
		}
		for _, d := range u.decls(name) {
			if d.body && !d.static && !d.value && !d.conditional {
				x.defs = append(x.defs, d)
			}
		}
	}
	x.complete = true
	return x, nil
}

// nativeDefinitions resolves a definition lead anchored in a C or C++ file:
// the declarations the calls of the lead's name on the anchor lines bind
// to. called reports such a call, local that every one binds to no project
// function, open that the anchor could not be read (left to the call
// catalog).
func (r *leadResolver) nativeDefinitions(scan *leadScan, anchor record.Excerpt, symbol string) (hits []leadHit, called, local, open bool, reason string, err error) {
	text, ok := r.anchorText(scan, anchor)
	if !ok {
		return nil, false, false, true, "", nil
	}
	u := newNativeUnit(anchor.Path, text)
	if u == nil {
		return nil, false, false, true, "", nil
	}
	calls := u.calls(anchor, symbol)
	if len(calls) == 0 {
		return nil, false, false, false, "", nil
	}
	tu, err := r.nativeTranslationUnit(scan, u)
	if err != nil {
		return nil, true, false, false, "", err
	}
	lead := r.newNativeLead(scan, tu, symbol, leadDefinitionLanguage(anchor.Path))
	locals := 0
	ambiguous := false
	for _, i := range calls {
		out, err := lead.bind(i)
		if err != nil {
			return nil, true, false, false, "", err
		}
		for _, h := range out.hits {
			if !slices.ContainsFunc(hits, func(o leadHit) bool { return o.declaration() == h.declaration() }) {
				hits = append(hits, h)
			}
		}
		switch {
		case out.ambiguous:
			ambiguous = true
		case out.local:
			locals++
		}
	}
	primary := slices.DeleteFunc(slices.Clone(hits), func(h leadHit) bool { return h.secondary })
	if ambiguous || len(primary) > 1 {
		return nil, true, false, false, record.LeadAmbiguous, nil
	}
	return hits, true, locals == len(calls), false, "", nil
}
