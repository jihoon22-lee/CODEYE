package review

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Definition leads in Rust files (docs/REVIEW.md#단서-해석). A plain call of
// the lead's name on the anchor lines binds where Rust's name resolution puts
// it. A binding of the name in the enclosing function (a parameter, a let,
// for, closure or match pattern, a nested item or use) is no project
// function. Else the function item of the call's module (the file, or the
// inline module holding the call) is the definition when it is the module's
// only value item of the name: a second fn, a const, static or tuple struct
// of the name, or an explicit use of it, make the call ambiguous, and a
// glob use yields to the item. An item under #[cfg], #[cfg_attr] or a test
// attribute is left to the build: ambiguous. An inline module whose only
// glob use is super::* reaches the items of the module holding it. A path
// call Self::m or Type::m, and self.m in a method, bind to the one fn m of
// the inherent impl Type blocks of the module, Type a plain name and the
// impl without generics; self.m only when m takes self as the calling method
// does (both self, &self or &mut self), so that no trait method is found
// before it. Other calls are left to the call catalog's use bindings. A call
// inside a macro_rules body binds nothing.

// rustItem is an item of a Rust file: its keyword token, its name token, its
// first token (outer attributes and qualifiers included), its last token
// (the body's "}" or the ";"); conditional reports a #[cfg], #[cfg_attr] or
// test attribute; receiver how a method takes self (self, &self, &mut self,
// "" for none, "other" for a typed self).
type rustItem struct {
	keyword, name, first, last int
	conditional                bool
	receiver                   string
}

// rustImpl is an impl block: its body "{", its self type when that is a
// plain name and the impl has no generics, whether it implements a trait.
type rustImpl struct {
	keyword, body int
	self          string
	trait         bool
	conditional   bool
}

// rustFile holds a Rust file's tokens and the items its lead resolution
// reads.
type rustFile struct {
	tokens  []callToken
	openers map[int]int
	fns     []rustItem
	bodies  map[int]int // a fn body's "{" to its index in fns
	impls   []rustImpl
	uses    []rustUse
}

func newRustFile(tokens []callToken) *rustFile {
	f := &rustFile{tokens: tokens, openers: map[int]int{}, bodies: map[int]int{}}
	for i, t := range tokens {
		if t.close > i {
			f.openers[t.close] = i
		}
	}
	for k := range tokens {
		if f.keyword(k, "fn") && tokenName(tokens, k+1) {
			if item, ok := f.fn(k); ok {
				f.fns = append(f.fns, item)
				if tokenIs(tokens, item.last, "}") {
					f.bodies[f.openers[item.last]] = len(f.fns) - 1
				}
			}
		}
		if f.keyword(k, "impl") && f.itemStart(k) {
			if impl, ok := f.impl(k); ok {
				f.impls = append(f.impls, impl)
			}
		}
	}
	f.uses = rustUses(tokens)
	return f
}

// keyword reports token k as the keyword word, not a lifetime ('static) or
// a path segment.
func (f *rustFile) keyword(k int, word string) bool {
	return tokenIs(f.tokens, k, word) && !tokenIs(f.tokens, k-1, "'") && !tokenIs(f.tokens, k-1, ".") &&
		!(tokenIs(f.tokens, k-1, ":") && tokenIs(f.tokens, k-2, ":"))
}

// itemStart reports a keyword at token k that may open an item: first in
// the file, or after "}", ";", "{", an attribute's "]", unsafe or default
// (not impl Trait in a type).
func (f *rustFile) itemStart(k int) bool {
	return k == 0 || slices.ContainsFunc([]string{"}", ";", "{", "]", "unsafe", "default"}, func(p string) bool { return tokenIs(f.tokens, k-1, p) })
}

// first is the first token of the item whose keyword is token k: its
// qualifiers (pub, pub(...), const, async, unsafe, extern "C", default) and
// outer attributes; conditional reports #[cfg(...)], #[cfg_attr(...)] or an
// attribute whose path ends in test.
func (f *rustFile) first(k int) (first int, conditional bool) {
	t := f.tokens
	first = k
	for {
		j := first - 1
		switch {
		case j < 0:
			return first, conditional
		case tokenIs(t, j, "pub"), tokenIs(t, j, "const"), tokenIs(t, j, "async"), tokenIs(t, j, "unsafe"), tokenIs(t, j, "extern"), tokenIs(t, j, "default"):
			first = j
		case t[j].stringLiteral && tokenIs(t, j-1, "extern"):
			first = j
		case tokenIs(t, j, ")"):
			o, ok := f.openers[j]
			if !ok || !tokenIs(t, o-1, "pub") {
				return first, conditional
			}
			first = o - 1
		case tokenIs(t, j, "]"):
			o, ok := f.openers[j]
			if !ok || !tokenIs(t, o-1, "#") {
				return first, conditional
			}
			conditional = conditional || rustConditionalAttribute(t, o)
			first = o - 1
		default:
			return first, conditional
		}
	}
}

// rustConditionalAttribute reports an attribute opening at "[" token o that
// makes an item depend on the build: cfg, cfg_attr, or a path ending in test.
func rustConditionalAttribute(t []callToken, o int) bool {
	if tokenIs(t, o+1, "cfg") || tokenIs(t, o+1, "cfg_attr") {
		return true
	}
	k := o + 1
	for tokenName(t, k) && tokenIs(t, k+1, ":") && tokenIs(t, k+2, ":") {
		k += 3
	}
	return tokenIs(t, k, "test")
}

// skipAngles skips a generic list "<...>" at k ("->" inside is no closer).
func (f *rustFile) skipAngles(k int) int {
	t := f.tokens
	if !tokenIs(t, k, "<") {
		return k
	}
	depth := 0
	for ; k < len(t); k++ {
		switch {
		case tokenIs(t, k, "<"):
			depth++
		case tokenIs(t, k, ">") && !tokenIs(t, k-1, "-"):
			depth--
			if depth == 0 {
				return k + 1
			}
		case tokenIs(t, k, ";") || tokenIs(t, k, "{"):
			return -1
		}
		if t[k].close > k {
			k = t[k].close
		}
	}
	return -1
}

// bodyAfter is the "{" or ";" that ends the head of an item whose head
// runs from k at depth (a return type, a where clause, a trait path), -1
// when none.
func (f *rustFile) bodyAfter(k, depth int) int {
	t := f.tokens
	for ; k < len(t); k++ {
		if t[k].depth < depth {
			return -1
		}
		if t[k].depth == depth && (tokenIs(t, k, "{") || tokenIs(t, k, ";")) {
			return k
		}
		if t[k].close > k && !tokenIs(t, k, "{") {
			k = t[k].close
		}
	}
	return -1
}

// fn reads the fn item whose keyword is token k.
func (f *rustFile) fn(k int) (rustItem, bool) {
	t := f.tokens
	open := f.skipAngles(k + 2)
	if open < 0 || !tokenIs(t, open, "(") {
		return rustItem{}, false
	}
	end := f.bodyAfter(t[open].close+1, t[k].depth)
	if end < 0 {
		return rustItem{}, false
	}
	last := end
	if tokenIs(t, end, "{") {
		last = t[end].close
	}
	first, conditional := f.first(k)
	return rustItem{keyword: k, name: k + 1, first: first, last: last, conditional: conditional, receiver: f.receiver(open)}, true
}

// receiver is how the parameter list opening at "(" token open takes self.
func (f *rustFile) receiver(open int) string {
	t := f.tokens
	k := open + 1
	kind := ""
	switch {
	case tokenIs(t, k, "self"):
		kind = "self"
	case tokenIs(t, k, "mut") && tokenIs(t, k+1, "self"):
		kind, k = "self", k+1
	case tokenIs(t, k, "&"):
		k++
		if tokenIs(t, k, "'") && tokenName(t, k+1) {
			k += 2
		}
		kind = "&self"
		if tokenIs(t, k, "mut") {
			kind, k = "&mut self", k+1
		}
		if !tokenIs(t, k, "self") {
			return ""
		}
	default:
		return ""
	}
	if !tokenIs(t, k+1, ",") && !tokenIs(t, k+1, ")") {
		return "other"
	}
	return kind
}

// impl reads the impl block whose keyword is token k.
func (f *rustFile) impl(k int) (rustImpl, bool) {
	t := f.tokens
	body := f.bodyAfter(k+1, t[k].depth)
	if body < 0 || !tokenIs(t, body, "{") {
		return rustImpl{}, false
	}
	impl := rustImpl{keyword: k, body: body}
	_, impl.conditional = f.first(k)
	switch {
	case tokenIs(t, k+1, "<"):
	case body == k+2 && tokenName(t, k+1):
		impl.self = t[k+1].text
	case body > k+3 && tokenName(t, body-1) && tokenIs(t, body-2, "for"):
		impl.self, impl.trait = t[body-1].text, true
	}
	return impl, true
}

// scope is the "{" of the innermost inline module holding token x, -1 for
// the file.
func (f *rustFile) scope(x int) int {
	t := f.tokens
	for p := t[x].parent; p >= 0; p = t[p].parent {
		if tokenIs(t, p, "{") && tokenName(t, p-1) && f.keyword(p-2, "mod") {
			return p
		}
	}
	return -1
}

// parentScope is the scope holding the inline module whose body is "{"
// token m.
func (f *rustFile) parentScope(m int) int {
	return f.scope(m - 2)
}

// enclosingFn is the outermost fn of the scope whose body holds token x;
// false when none.
func (f *rustFile) enclosingFn(x, scope int) (rustItem, bool) {
	t := f.tokens
	out := -1
	for p := t[x].parent; p >= 0 && p != scope; p = t[p].parent {
		if k, ok := f.bodies[p]; ok {
			out = k
		}
	}
	if out < 0 {
		return rustItem{}, false
	}
	return f.fns[out], true
}

// inMacroRules reports token x inside the body of a macro_rules! definition.
func (f *rustFile) inMacroRules(x int) bool {
	t := f.tokens
	for p := t[x].parent; p >= 0; p = t[p].parent {
		if tokenName(t, p-1) && tokenIs(t, p-2, "!") && tokenIs(t, p-3, "macro_rules") {
			return true
		}
	}
	return false
}

// valueItems lists the value-namespace items of name directly in scope s:
// fn, const, static and tuple or unit struct items, and the explicit uses
// binding name; globs reports the scope's glob uses.
func (f *rustFile) valueItems(s int, name string) (fns []rustItem, others int, globs []rustUse) {
	t := f.tokens
	for _, fn := range f.fns {
		if t[fn.keyword].parent == s && t[fn.name].text == name {
			fns = append(fns, fn)
		}
	}
	for k := range t {
		if t[k].parent != s || !tokenIs(t, k+1, name) {
			continue
		}
		switch {
		case f.keyword(k, "const") && tokenIs(t, k+2, ":"),
			f.keyword(k, "static") && tokenIs(t, k+2, ":"),
			f.keyword(k, "struct") && (tokenIs(t, k+2, "(") || tokenIs(t, k+2, ";")):
			others++
		case tokenIs(t, k, "mut") && f.keyword(k-1, "static") && tokenIs(t, k+2, ":"):
			others++
		}
	}
	for _, u := range f.uses {
		if t[u.keyword].parent != s {
			continue
		}
		switch {
		case u.local == "" && len(u.path) > 0 && u.path[len(u.path)-1] == "*":
			globs = append(globs, u)
		case u.local == name:
			others++
		}
	}
	return fns, others, globs
}

// shadowed reports a binding of name in fn: in its parameters, a let, for,
// closure or match pattern, or a nested item or use of its body.
func (f *rustFile) shadowed(fn rustItem, name string) bool {
	t := f.tokens
	open := f.skipAngles(fn.keyword + 2)
	if open < 0 {
		return true
	}
	for i := open + 1; i < t[open].close; i++ {
		if tokenIs(t, i, name) {
			return true
		}
	}
	if !tokenIs(t, fn.last, "}") {
		return false
	}
	body := f.openers[fn.last]
	patterns := f.patterns(body, fn.last)
	for i := body + 1; i < fn.last; i++ {
		if !tokenIs(t, i, name) || t[i].quoted {
			continue
		}
		switch {
		case tokenIs(t, i-1, ".") || tokenIs(t, i-1, ":") && tokenIs(t, i-2, ":") || tokenIs(t, i+1, ":") && tokenIs(t, i+2, ":"):
			continue
		case tokenIs(t, i+1, "!") && !tokenIs(t, i+2, "="):
			continue
		}
		for _, kw := range []string{"fn", "const", "static", "struct", "enum", "union", "trait", "type", "mod", "use"} {
			if f.keyword(i-1, kw) {
				return true
			}
		}
		if tokenIs(t, i+1, "(") {
			continue // a call, or a tuple struct pattern of a type of that name
		}
		if slices.ContainsFunc(patterns, func(r [2]int) bool { return r[0] < i && i < r[1] }) {
			return true
		}
	}
	for _, u := range f.uses {
		if u.keyword > body && u.keyword < fn.last && u.local == name {
			return true
		}
	}
	return false
}

// patterns lists the token ranges of the patterns between body and end: let
// to its "=" (if let and while let too), for to in, a closure's parameters
// and a match arm's pattern and guard up to "=>".
func (f *rustFile) patterns(body, end int) [][2]int {
	t := f.tokens
	var out [][2]int
	until := func(k int, stop func(int) bool) int {
		for j := k + 1; j < end; j++ {
			if t[j].depth < t[k].depth {
				return j
			}
			if t[j].depth == t[k].depth && stop(j) {
				return j
			}
			if t[j].close > j {
				j = t[j].close
			}
		}
		return end
	}
	assign := func(j int) bool {
		return tokenIs(t, j, "=") && !tokenIs(t, j+1, "=") && !tokenIs(t, j+1, ">") || tokenIs(t, j, ";")
	}
	for k := body + 1; k < end; k++ {
		switch {
		case f.keyword(k, "let"):
			out = append(out, [2]int{k, until(k, assign)})
		case f.keyword(k, "for") && !tokenIs(t, k+1, "<"):
			out = append(out, [2]int{k, until(k, func(j int) bool { return tokenIs(t, j, "in") || tokenIs(t, j, "{") })})
		case tokenIs(t, k, "|") && rustClosureStart(t, k):
			// The parameters end at the next "|"; the scan goes on after it.
			close := until(k, func(j int) bool { return tokenIs(t, j, "|") })
			out = append(out, [2]int{k, close})
			k = close
		case f.keyword(k, "match"):
			b := f.bodyAfter(k+1, t[k].depth)
			if b < 0 || !tokenIs(t, b, "{") {
				continue
			}
			out = append(out, f.arms(b)...)
		}
	}
	return out
}

// rustClosureStart reports a "|" at k that may open a closure's parameters:
// it follows no operand. A second "|" of a || operator counts too (the
// reading stays on the safe side: more patterns, fewer bindings).
func rustClosureStart(t []callToken, k int) bool {
	return k == 0 || slices.ContainsFunc([]string{"(", ",", "=", "{", ";", "move", "return", ">", "|", "[", ":"}, func(p string) bool { return tokenIs(t, k-1, p) })
}

// arms lists the pattern ranges (to "=>") of the match arms of the body
// opening at "{" token b.
func (f *rustFile) arms(b int) [][2]int {
	t := f.tokens
	var out [][2]int
	end := t[b].close
	at := b + 1
	for at < end {
		arrow := -1
		for j := at; j < end; j++ {
			if t[j].depth == t[b].depth+1 && tokenIs(t, j, "=") && tokenIs(t, j+1, ">") {
				arrow = j
				break
			}
			if t[j].close > j {
				j = t[j].close
			}
		}
		if arrow < 0 {
			break
		}
		out = append(out, [2]int{at - 1, arrow})
		next := arrow + 2
		if tokenIs(t, next, "{") {
			next = t[next].close + 1
			if tokenIs(t, next, ",") {
				next++
			}
		} else {
			for next < end && !(t[next].depth == t[b].depth+1 && tokenIs(t, next, ",")) {
				if t[next].close > next {
					next = t[next].close
				}
				next++
			}
			next++
		}
		at = next
	}
	return out
}

// methods lists the fn items named name in the plain inherent impl blocks
// of type self directly in scope s; conditional reports one of them or its
// impl under a conditional attribute.
func (f *rustFile) methods(s int, self, name string) (out []rustItem, conditional bool) {
	t := f.tokens
	for _, impl := range f.impls {
		if impl.trait || impl.self != self || t[impl.keyword].parent != s {
			continue
		}
		for _, fn := range f.fns {
			if t[fn.keyword].parent == impl.body && t[fn.name].text == name {
				out = append(out, fn)
				conditional = conditional || fn.conditional || impl.conditional
			}
		}
	}
	return out, conditional
}

// implOf is the impl whose body holds fn, false when fn is no method.
func (f *rustFile) implOf(fn rustItem) (rustImpl, bool) {
	p := f.tokens[fn.keyword].parent
	at := slices.IndexFunc(f.impls, func(x rustImpl) bool { return x.body == p })
	if at < 0 {
		return rustImpl{}, false
	}
	return f.impls[at], true
}

// typeShadowed reports a binding of the type name in fn: a generic
// parameter or a nested type item or use.
func (f *rustFile) typeShadowed(fn rustItem, name string) bool {
	t := f.tokens
	open := f.skipAngles(fn.keyword + 2)
	for i := fn.keyword + 2; i >= 0 && i < open; i++ {
		if tokenIs(t, i, name) {
			return true
		}
	}
	if !tokenIs(t, fn.last, "}") {
		return false
	}
	body := f.openers[fn.last]
	for i := body + 1; i < fn.last; i++ {
		if !tokenIs(t, i, name) {
			continue
		}
		for _, kw := range []string{"struct", "enum", "union", "trait", "type", "mod"} {
			if f.keyword(i-1, kw) {
				return true
			}
		}
	}
	for _, u := range f.uses {
		if u.keyword > body && u.keyword < fn.last && u.local == name {
			return true
		}
	}
	return false
}

// rustLeadCall is one call of the lead's name: at its name token, path the
// names before it (Self::m, Type::m, a::b::m), receiver the name before "."
// of a method call.
type rustLeadCall struct {
	at       int
	path     []string
	receiver string
	member   bool
}

// calls lists the calls of symbol on the anchor lines: name( and name::<T>(,
// not a declaration and not a macro.
func (f *rustFile) calls(anchor record.Excerpt, symbol string) []rustLeadCall {
	t := f.tokens
	var out []rustLeadCall
	for i, tok := range t {
		if !tokenIs(t, i, symbol) || tok.line < anchor.Start || tok.line > anchor.End || f.keyword(i-1, "fn") {
			continue
		}
		open := i + 1
		if tokenIs(t, open, ":") && tokenIs(t, open+1, ":") && tokenIs(t, open+2, "<") {
			open = f.skipAngles(open + 2)
		}
		if open < 0 || !tokenIs(t, open, "(") {
			continue
		}
		c := rustLeadCall{at: i}
		switch {
		case tokenIs(t, i-1, "."):
			c.member = true
			if tokenName(t, i-2) {
				c.receiver = t[i-2].text
			}
		case tokenIs(t, i-1, ":") && tokenIs(t, i-2, ":"):
			for k := i - 3; ; k -= 3 {
				if !tokenName(t, k) {
					c.path = append([]string{""}, c.path...)
					break
				}
				c.path = append([]string{t[k].text}, c.path...)
				if !(tokenIs(t, k-1, ":") && tokenIs(t, k-2, ":")) {
					break
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// rustOutcome is what one call binds to: hit, else local (a binding of the
// enclosing function, or an item of the file that is no function), ambiguous,
// refused (bound by the file but not safely), or open (left to the call
// catalog).
type rustOutcome struct {
	hit                             *rustItem
	local, ambiguous, refused, open bool
}

// bind resolves one call.
func (f *rustFile) bind(c rustLeadCall, symbol string) rustOutcome {
	t := f.tokens
	if f.inMacroRules(c.at) {
		return rustOutcome{refused: true}
	}
	scope := f.scope(c.at)
	fn, inFn := f.enclosingFn(c.at, scope)
	pick := func(found []rustItem, conditional bool) rustOutcome {
		switch {
		case len(found) == 0:
			return rustOutcome{open: true}
		case len(found) > 1 || conditional:
			return rustOutcome{ambiguous: true}
		}
		return rustOutcome{hit: &found[0]}
	}
	switch {
	case c.member && c.receiver == "self":
		if !inFn {
			return rustOutcome{open: true}
		}
		impl, ok := f.implOf(fn)
		if !ok || impl.self == "" || fn.receiver == "" || fn.receiver == "other" {
			return rustOutcome{open: true}
		}
		out := pick(f.methods(f.scope(impl.keyword), impl.self, symbol))
		if out.hit != nil && out.hit.receiver != fn.receiver {
			return rustOutcome{refused: true}
		}
		return out
	case c.member:
		return rustOutcome{open: true}
	case len(c.path) == 1 && c.path[0] == "Self":
		for p := t[c.at].parent; p >= 0; p = t[p].parent {
			at := slices.IndexFunc(f.impls, func(x rustImpl) bool { return x.body == p })
			if at < 0 {
				continue
			}
			impl := f.impls[at]
			if impl.self == "" {
				return rustOutcome{open: true}
			}
			return pick(f.methods(f.scope(impl.keyword), impl.self, symbol))
		}
		return rustOutcome{open: true}
	case len(c.path) == 1 && c.path[0] != "":
		if inFn && f.typeShadowed(fn, c.path[0]) {
			return rustOutcome{open: true}
		}
		return pick(f.methods(scope, c.path[0], symbol))
	case len(c.path) > 0:
		return rustOutcome{open: true}
	}
	if inFn && f.shadowed(fn, symbol) {
		return rustOutcome{local: true}
	}
	for s := scope; ; {
		fns, others, globs := f.valueItems(s, symbol)
		conditional := slices.ContainsFunc(fns, func(x rustItem) bool { return x.conditional })
		switch {
		case len(fns) > 0 && (len(fns)+others > 1 || conditional):
			return rustOutcome{ambiguous: true}
		case len(fns) == 1:
			return rustOutcome{hit: &fns[0]}
		case others > 0 || s < 0 || len(globs) != 1 || !slices.Equal(globs[0].path, []string{"super", "*"}):
			return rustOutcome{open: true}
		}
		s = f.parentScope(s)
	}
}

// rustDefinitions resolves a definition lead anchored in a Rust file: the
// items the calls of the lead's name on the anchor lines bind to. called
// reports such a call, local that every one binds in the anchor's file to no
// project function, open that one is left to the call catalog (as is a file
// the token reader gives up on).
func (r *leadResolver) rustDefinitions(scan *leadScan, anchor record.Excerpt, symbol string) (hits []leadHit, called, local, open bool, reason string) {
	text, ok := r.anchorText(scan, anchor)
	if !ok {
		return nil, false, false, true, ""
	}
	tokens := leadTokens("rust", text)
	if tokens == nil {
		return nil, false, false, true, ""
	}
	f := newRustFile(tokens)
	calls := f.calls(anchor, symbol)
	locals := 0
	ambiguous := false
	for _, c := range calls {
		out := f.bind(c, symbol)
		switch {
		case out.hit != nil:
			h := leadHit{loc: sourceCallLocation{anchor.Path, tokens[out.hit.first].line, tokens[out.hit.last].endLine}, origin: "context"}
			h.extent = h.loc
			if !slices.ContainsFunc(hits, func(o leadHit) bool { return o.declaration() == h.declaration() }) {
				hits = append(hits, h)
			}
		case out.ambiguous:
			ambiguous = true
		case out.local:
			locals++
		case out.open:
			open = true
		}
	}
	if ambiguous || len(hits) > 1 {
		return nil, len(calls) > 0, false, false, record.LeadAmbiguous
	}
	return hits, len(calls) > 0, len(calls) > 0 && locals == len(calls), open, ""
}
