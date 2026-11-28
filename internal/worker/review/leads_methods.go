package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Method calls of definition leads in Python, JavaScript and TypeScript
// files (docs/REVIEW.md#단서-해석): a call of the lead's name through this,
// self or a class's name binds to a method a class body declares only when
// nothing else can answer it at run time.
//
// A method no other class can override binds to its class's declaration: a
// JavaScript private method (this.#m), a TypeScript private one, a Python
// name-mangled one (self.__m), and a static method called through the name
// of a class the anchor's file declares or imports by name (Class.m; in
// Python a staticmethod or classmethod). An ordinary method (this.m,
// self.m) binds in a closed world only (leadWorld.methodAnswer): its class
// body declares the name once, as a method; no source of the language
// writes a member of the name or may patch classes otherwise; and no other
// class declares a member of the name. The receiver must be the instance:
// in JavaScript this in a method of the class, through arrow functions only;
// in Python self as the first parameter of a plain method directly in the
// class body, bound nowhere else. A method the class does not declare (an
// inherited one) stays unresolved, as does one the reader cannot take
// whole: a field, an accessor, a decorated method.

// methodCall is a call of a lead's name through a receiver on the anchor
// lines: at is the name's token, receiver the receiver's (this, self or a
// class's name), private reports this.#name.
type methodCall struct {
	at, receiver int
	private      bool
}

// methodCalls lists the calls of symbol through a receiver name on the
// anchor lines: receiver "." name "(" (in JavaScript "." "#" name for a
// private name, in TypeScript type arguments before "(" too). A receiver
// that is itself a member (a.b.m()) or an optional chain is no such call.
func methodCalls(tokens []callToken, anchor record.Excerpt, symbol string, python, typeScript bool) []methodCall {
	var out []methodCall
	for i, t := range tokens {
		if !t.name || t.quoted || t.text != symbol || t.line < anchor.Start || anchor.End < t.line {
			continue
		}
		open := i + 1
		if typeScript && tokenIs(tokens, open, "<") {
			open = typeArguments(tokens, open)
		}
		if !tokenIs(tokens, open, "(") {
			continue
		}
		dot, private := i-1, false
		if !python && tokenIs(tokens, dot, "#") {
			dot, private = dot-1, true
		}
		if !tokenIs(tokens, dot, ".") || !tokenName(tokens, dot-1) || tokens[dot-1].quoted || tokenIs(tokens, dot-2, ".") || tokenIs(tokens, dot-2, "?") {
			continue
		}
		out = append(out, methodCall{at: i, receiver: dot - 1, private: private})
	}
	return out
}

// scriptMethods resolves the calls of symbol through a receiver on the
// anchor lines (methodCalls): their hits, whether there was such a call,
// and why one bound nothing when that says more than that no definition was
// found (lead_ambiguous before any other reason).
func (r *leadResolver) scriptMethods(scan *leadScan, anchor record.Excerpt, tokens []callToken, symbol string, python, typeScript bool, imports []scriptBinding) (hits []leadHit, called bool, reason string, err error) {
	calls := methodCalls(tokens, anchor, symbol, python, typeScript)
	var blocks []pyBlock
	if python && len(calls) > 0 {
		blocks = pythonBlocks(tokens)
	}
	for _, c := range calls {
		var h leadHit
		var found bool
		var why string
		switch {
		case python && tokenIs(tokens, c.receiver, "self"):
			h, found, why, err = r.pythonSelfCall(anchor.Path, tokens, blocks, c, symbol)
		case python:
			h, found, why, err = r.classCall(scan, anchor.Path, tokens, blocks, c, symbol, true, false, imports)
		case tokenIs(tokens, c.receiver, "this"):
			h, found, why, err = r.thisCall(anchor.Path, tokens, c, symbol, typeScript)
		case !c.private:
			h, found, why, err = r.classCall(scan, anchor.Path, tokens, nil, c, symbol, false, typeScript, imports)
		}
		if err != nil {
			return nil, true, "", err
		}
		if found && !slices.ContainsFunc(hits, func(o leadHit) bool { return o.declaration() == h.declaration() }) {
			hits = append(hits, h)
		}
		if why == record.LeadAmbiguous || reason == "" {
			reason = why
		}
	}
	return hits, len(calls) > 0, reason, nil
}

// thisCall binds a JavaScript or TypeScript call through this: to the class
// body's private method (#name) or TypeScript private method when nothing
// in the file writes it, else to its ordinary method in a closed world.
func (r *leadResolver) thisCall(p string, tokens []callToken, c methodCall, symbol string, typeScript bool) (leadHit, bool, string, error) {
	body, ok := jsThisClass(tokens, c.receiver)
	if !ok {
		return leadHit{}, false, "", nil
	}
	m, why := jsClassMethod(tokens, body, symbol, c.private, false, typeScript)
	if m.name < 0 {
		return leadHit{}, false, why, nil
	}
	declarations := everySource
	switch {
	case c.private:
		// No code outside the class body reaches a private name.
		return declarationHit(p, jsMethodDeclaration(tokens, m)), true, "", nil
	case typeScript && m.tsPrivate:
		// TypeScript rejects another declaration of the name in a class it
		// checks (a JavaScript source it does not), but code may write it.
		declarations = func(q string) bool { return !typeScriptPath(q) }
	}
	why, err := r.closedWorld().methodAnswer("javascript", p, symbol, body, declarations)
	if err != nil || why != "" {
		return leadHit{}, false, why, err
	}
	return declarationHit(p, jsMethodDeclaration(tokens, m)), true, "", nil
}

// everySource counts the declarations of every source (methodAnswer).
func everySource(string) bool { return true }

// pythonSelfCall binds a Python call through self: to the class's
// name-mangled method (__name) when nothing in the file writes it, else to
// its ordinary method in a closed world.
func (r *leadResolver) pythonSelfCall(p string, tokens []callToken, blocks []pyBlock, c methodCall, symbol string) (leadHit, bool, string, error) {
	cls := pySelfClass(tokens, blocks, c.receiver)
	if cls < 0 {
		return leadHit{}, false, "", nil
	}
	def, why := pyClassMethod(tokens, blocks, cls, symbol, false)
	if def < 0 {
		return leadHit{}, false, why, nil
	}
	name, owner := symbol, blocks[cls].name
	if class := strings.TrimLeft(tokens[owner].text, "_"); class != "" && strings.HasPrefix(symbol, "__") && !strings.HasSuffix(symbol, "__") {
		// Name mangling makes it the class's own (_Class__name): the class's
		// code reaches it, and code anywhere spelling the mangled name.
		if _, writes := pyMemberSites(tokens, blocks, symbol); writes {
			return leadHit{}, false, record.LeadAmbiguous, nil
		}
		name, owner = "_"+class+symbol, -1
	}
	why, err := r.closedWorld().methodAnswer("python", p, name, owner, everySource)
	if err != nil || why != "" {
		return leadHit{}, false, why, err
	}
	d, ok := pythonFunctionDeclaration(tokens, blocks, blocks[def].name)
	if !ok {
		return leadHit{}, false, "", nil
	}
	return declarationHit(p, d), true, "", nil
}

// classCall binds a call through a class's name (Class.m) to the class's
// static method (JavaScript static, Python staticmethod or classmethod):
// the class declared at module level in the anchor's file or in the module
// an import of the name binds (its declaration there), when neither file
// writes a member of the name or may patch classes.
func (r *leadResolver) classCall(scan *leadScan, p string, tokens []callToken, blocks []pyBlock, c methodCall, symbol string, python, typeScript bool, imports []scriptBinding) (leadHit, bool, string, error) {
	name := tokens[c.receiver].text
	if name == "super" || name == "cls" || name == "this" {
		return leadHit{}, false, "", nil
	}
	s := scriptScopesOf(tokens, name, python, typeScript, imports)
	bindings, assigned := s.moduleBindings()
	if s.shadowed(c.receiver) || assigned || len(bindings) != 1 {
		return leadHit{}, false, "", nil
	}
	file, ftokens, fblocks, at := p, tokens, blocks, bindings[0].at
	if bindings[0].decl == "import" {
		k := slices.IndexFunc(imports, func(x scriptBinding) bool { return x.token == at })
		if k < 0 || imports[k].typeOnly || imports[k].imported == "" || imports[k].imported == "*" {
			return leadHit{}, false, "", nil
		}
		if python {
			file = leadPythonModule(r.catalog.files, p, imports[k].module)
		} else {
			file = leadScriptModule(r.catalog.files, p, imports[k].module)
		}
		if file == "" {
			return leadHit{}, false, "", nil
		}
		text, ok, err := scan.index.text(file)
		if err != nil || !ok {
			return leadHit{}, false, scan.refused[file], err
		}
		if ftokens = leadTokens(file, text); ftokens == nil || python && pythonDynamicBindings(ftokens) {
			return leadHit{}, false, "", nil
		}
		if python {
			fblocks = pythonBlocks(ftokens)
		}
		if at = scriptClassDeclaration(ftokens, imports[k].imported, python, typeScriptPath(file)); at < 0 {
			return leadHit{}, false, "", nil
		}
	} else if !scriptModuleClass(tokens, at, python) {
		return leadHit{}, false, "", nil
	}
	// Neither file may copy members into an object, and no source of the
	// language may write a member of the name or patch a class.
	lang := "python"
	if !python {
		lang = "javascript"
		if jsAssigns(tokens) || jsAssigns(ftokens) {
			return leadHit{}, false, record.LeadAmbiguous, nil
		}
	}
	if why, err := r.closedWorld().methodAnswer(lang, p, symbol, -1, nil); err != nil || why != "" {
		return leadHit{}, false, why, err
	}
	if python {
		cls := slices.IndexFunc(fblocks, func(b pyBlock) bool { return b.kind == "class" && b.name == at })
		if cls < 0 {
			return leadHit{}, false, "", nil
		}
		def, why := pyClassMethod(ftokens, fblocks, cls, symbol, true)
		if def < 0 {
			return leadHit{}, false, why, nil
		}
		d, ok := pythonFunctionDeclaration(ftokens, fblocks, fblocks[def].name)
		if !ok {
			return leadHit{}, false, "", nil
		}
		return declarationHit(file, d), true, "", nil
	}
	body := jsClassBodyOf(ftokens, at)
	if body < 0 {
		return leadHit{}, false, "", nil
	}
	m, why := jsClassMethod(ftokens, body, symbol, false, true, typeScriptPath(file))
	if m.name < 0 {
		return leadHit{}, false, why, nil
	}
	return declarationHit(file, jsMethodDeclaration(ftokens, m)), true, "", nil
}

// scriptModuleClass reports a module-level class declaration whose name is
// token at.
func scriptModuleClass(tokens []callToken, at int, python bool) bool {
	if !tokenIs(tokens, at-1, "class") {
		return false
	}
	if python {
		return tokens[at-1].column == 0
	}
	return tokens[at].depth == 0
}

// scriptClassDeclaration is the name token of the module-level class a
// module exports as name (an exported class declaration, a declaration an
// export clause names, export default class for "default"; in Python its one
// module-level class of the name), -1 when it exports none or binds the
// name more than once.
func scriptClassDeclaration(tokens []callToken, name string, python, typeScript bool) int {
	local, exported := name, python
	if !python {
		if name == "default" {
			for i := range tokens {
				if tokens[i].depth == 0 && tokenIs(tokens, i, "export") && tokenIs(tokens, i+1, "default") && tokenIs(tokens, i+2, "class") && tokenName(tokens, i+3) {
					local, exported = tokens[i+3].text, true
					break
				}
			}
		} else if l, ok := exportClause(tokens, name); ok {
			local, exported = l, true
		}
	}
	var imports []scriptBinding
	if python {
		imports = pythonImports(tokens)
	} else {
		imports = javascriptImports(tokens)
	}
	s := scriptScopesOf(tokens, local, python, typeScript, imports)
	bindings, assigned := s.moduleBindings()
	if assigned || len(bindings) != 1 || !scriptModuleClass(tokens, bindings[0].at, python) {
		return -1
	}
	at := bindings[0].at
	if !exported {
		k := at - 2
		for tokenIs(tokens, k, "abstract") || tokenIs(tokens, k, "declare") {
			k--
		}
		if !tokenIs(tokens, k, "export") {
			return -1
		}
	}
	return at
}

// jsClassBodyOf is the "{" of the body of the class whose name is token at,
// -1 when the reader does not find it.
func jsClassBodyOf(tokens []callToken, at int) int {
	for k := at + 1; k < len(tokens) && tokens[k].depth >= tokens[at].depth; k++ {
		switch {
		case tokenIs(tokens, k, "{") && tokens[k].depth == tokens[at].depth:
			if classBody(tokens, k) {
				return k
			}
			return -1
		case tokenIs(tokens, k, ";"):
			return -1
		case tokens[k].close > k:
			k = tokens[k].close
		}
	}
	return -1
}

// jsThisClass is the class body whose instance this (token at) is: the
// innermost function holding it, past arrow functions, must be a method,
// an accessor or the constructor of that class body, and not static. A
// function declaration or expression, an object's method, a static member,
// code directly in a class body (a field's initializer, a computed key, a
// static block) and code outside any function give this another value.
func jsThisClass(tokens []callToken, at int) (int, bool) {
	for b := tokens[at].parent; b >= 0; b = tokens[b].parent {
		if !tokenIs(tokens, b, "{") {
			continue
		}
		if classLike(tokens, b) {
			return -1, false
		}
		if !functionBrace(tokens, b) {
			continue
		}
		if tokenIs(tokens, b-1, ">") && tokenIs(tokens, b-2, "=") {
			continue // an arrow function: this is the one around it
		}
		open := jsParamsOf(tokens, b)
		if open < 0 {
			return -1, false
		}
		name := open - 1
		if tokenIs(tokens, name, ">") {
			name = genericsStart(tokens, name) - 1
		}
		owner := -1
		if name >= 0 {
			owner = tokens[name].parent
		}
		if !tokenName(tokens, name) || tokens[name].quoted || tokenIs(tokens, name, "function") || tokenIs(tokens, name-1, "function") ||
			tokenIs(tokens, name-1, "*") && tokenIs(tokens, name-2, "function") || owner < 0 || !tokenIs(tokens, owner, "{") || !classBody(tokens, owner) ||
			!methodName(tokens, name) || jsHead(tokens, name, owner).static {
			return -1, false
		}
		return owner, true
	}
	return -1, false
}

// jsParamsOf is the "(" of the parameter list whose function body opens at
// brace b, directly after its ")" or past a return annotation; -1 when none.
func jsParamsOf(tokens []callToken, b int) int {
	for k := b - 1; k >= 0 && b-k < 200; k-- {
		closer := tokenIs(tokens, k, ")") || tokenIs(tokens, k, "]") || tokenIs(tokens, k, "}")
		if closer && tokens[tokens[k].parent].depth != tokens[b].depth || !closer && tokens[k].depth != tokens[b].depth {
			return -1
		}
		switch {
		case tokenIs(tokens, k, ")"):
			open := tokens[k].parent
			if paramList(tokens, open) && !tokenIs(tokens, open-1, "catch") && functionBody(tokens, k) == b {
				return open
			}
			k = open
		case closer:
			k = tokens[k].parent
		case tokenIs(tokens, k, ";") || tokenIs(tokens, k, "{") || tokenIs(tokens, k, "=") && !tokenIs(tokens, k+1, ">"):
			return -1
		}
	}
	return -1
}

// classLike reports a "{" that may open a class body: a class keyword
// before it, past what a class's name, type parameters and extends or
// implements clauses may hold (names, strings, operators, groups). It errs
// towards a class.
func classLike(tokens []callToken, open int) bool {
	depth := tokens[open].depth
	for k := open - 1; k >= 0 && open-k < 400; k-- {
		t := tokens[k]
		switch {
		case !t.quoted && (t.text == ")" || t.text == "]") && t.parent >= 0:
			k = t.parent
		case t.depth != depth, tokenIs(tokens, k, ";"), tokenIs(tokens, k, "{"), tokenIs(tokens, k, "}"):
			return false
		case tokenIs(tokens, k, "class"):
			return true
		}
	}
	return false
}

// jsMemberHead is what stands before a class member's name: its first
// token (a decorator or modifier, else the name), and whether it is static,
// an accessor (get, set), TypeScript private or decorated.
type jsMemberHead struct {
	first                                  int
	static, accessor, tsPrivate, decorated bool
}

// jsHead reads the head of the member of the class body opening at body
// whose name is token k (after "#" for a private name).
func jsHead(tokens []callToken, k, body int) jsMemberHead {
	h := jsMemberHead{first: k}
	i := k - 1
	if tokenIs(tokens, i, "#") {
		h.first, i = i, i-1
	}
	for ; i > body && tokens[i].parent == body && !tokens[i].quoted && slices.Contains(classModifiers, tokens[i].text); i-- {
		switch tokens[i].text {
		case "static":
			h.static = true
		case "get", "set":
			h.accessor = h.accessor || i == h.first-1
		case "private":
			h.tsPrivate = true
		}
		h.first = i
	}
	for i > body {
		e := i
		if tokenIs(tokens, e, ")") && tokens[e].parent > body {
			e = tokens[e].parent - 1
		}
		n := e
		for n > body && tokens[n].parent == body && (tokenName(tokens, n) || tokenIs(tokens, n, ".")) {
			n--
		}
		if n == e || !tokenIs(tokens, n, "@") || tokens[n].parent != body {
			break
		}
		h.decorated, h.first, i = true, n, n-1
	}
	return h
}

// jsMethod is a class body's method: its name token (-1 for none), its
// first token, its body's "{" and whether it is TypeScript private.
type jsMethod struct {
	name, first, body int
	tsPrivate         bool
}

// jsClassMethod is the one method named name (#name when private) the class
// body opening at body declares on the instance side, or with static on
// the static side: at a member's place, with a body, not an accessor and
// not decorated. TypeScript signatures (overloads, abstract or ambient
// members) declare nothing at run time. Its name is -1 when the class body
// declares no such method (an inherited one stays unresolved) or declares
// the name otherwise (a field, an accessor, a decorated method, a string or
// computed key, a TypeScript constructor parameter property): the reason
// is lead_ambiguous for such a declaration beside a method.
func jsClassMethod(tokens []callToken, body int, name string, private, static, typeScript bool) (jsMethod, string) {
	none := jsMethod{name: -1}
	var methods []jsMethod
	other := false
	for k := body + 1; k < tokens[body].close; k++ {
		t := tokens[k]
		if t.parent != body {
			continue
		}
		switch {
		case t.quoted && t.text == name && !private && memberPosition(tokens, k, body):
			other = true
		case tokenIs(tokens, k, "[") && memberPosition(tokens, k, body) && !jsOtherKey(tokens, k, name):
			other = true
		case t.name && !t.quoted && t.text == name && !tokenIs(tokens, k-1, ".") && tokenIs(tokens, k-1, "#") == private:
			h := jsHead(tokens, k, body)
			if h.static != static || !memberPosition(tokens, h.first, body) && h.first != body+1 {
				continue
			}
			open := skipGenerics(tokens, k+1)
			var b int = -1
			if methodName(tokens, k) && tokenIs(tokens, open, "(") {
				b = functionBody(tokens, tokens[open].close)
			}
			switch {
			case !methodName(tokens, k), h.accessor, h.decorated:
				other = true
			case b < 0:
				// A signature: nothing at run time in TypeScript.
				other = other || !typeScript
			default:
				methods = append(methods, jsMethod{name: k, first: h.first, body: b, tsPrivate: h.tsPrivate})
			}
		}
	}
	if !static && !private && jsParameterPropertyIn(tokens, body, name) {
		other = true
	}
	switch {
	case len(methods) > 1, len(methods) == 1 && other:
		return none, record.LeadAmbiguous
	case len(methods) == 0, other:
		return none, ""
	}
	return methods[0], ""
}

// jsOtherKey reports a computed key "[...]" opening at open that names
// another string than name, or a Symbol property.
func jsOtherKey(tokens []callToken, open int, name string) bool {
	close := tokens[open].close
	switch {
	case close == open+2 && tokens[open+1].quoted:
		return tokens[open+1].text != name
	case close == open+4 && tokenIs(tokens, open+1, "Symbol") && tokenIs(tokens, open+2, ".") && tokenName(tokens, open+3):
		return true
	}
	return false
}

// parameterModifiers make a TypeScript constructor parameter a property of
// each instance.
var parameterModifiers = []string{"public", "private", "protected", "readonly", "override"}

// jsParameterProperty is the class body whose instances a TypeScript
// constructor parameter property named by token i belongs to
// (constructor(private name: T)); -1 when token i names none.
func jsParameterProperty(tokens []callToken, i int) int {
	p := tokens[i].parent
	if p < 1 || !tokenIs(tokens, p, "(") || !tokenIs(tokens, p-1, "constructor") || !slices.Contains(parameterModifiers, tokens[i-1].text) || tokens[i-1].quoted {
		return -1
	}
	return tokens[p-1].parent
}

// jsParameterPropertyIn reports a constructor parameter property named name
// of the class body opening at body.
func jsParameterPropertyIn(tokens []callToken, body int, name string) bool {
	for k := body + 1; k < tokens[body].close; k++ {
		if tokens[k].name && !tokens[k].quoted && tokens[k].text == name && jsParameterProperty(tokens, k) == body {
			return true
		}
	}
	return false
}

// jsMethodDeclaration is the declaration of a class body's method m: from
// its first modifier to its body's "}", bounded to its first return.
func jsMethodDeclaration(tokens []callToken, m jsMethod) scriptDeclaration {
	d := scriptDeclaration{start: tokens[m.first].line, end: tokens[tokens[m.body].close].endLine}
	d.boundStart, d.bound = d.start, scriptFirstReturn(tokens, m.body, d.start, false)
	return d
}

// jsMemberSites lists what in a JavaScript or TypeScript source may give a
// class's instances a member of name, by the bracket each stands in: a name
// token of the name directly in a class body (a member's name, static or
// not, or a word of an initializer), a string of the name or a computed key
// that may be the name at a member's place, and a TypeScript constructor
// parameter property of the name. writes reports an assignment or update
// of a member of the name (.m =, .m += , ++x.m, x["m"] =, a target of a
// destructuring assignment or a for head, delete x.m) or a key of the name
// in what a call of Object or Reflect copies or defines. It errs towards a site: the
// check refuses on one.
func jsMemberSites(tokens []callToken, name string) (owners []int, writes bool) {
	classes := map[int]bool{}
	inClass := func(b int) bool {
		if b < 0 || !tokenIs(tokens, b, "{") {
			return false
		}
		v, ok := classes[b]
		if !ok {
			v = classLike(tokens, b)
			classes[b] = v
		}
		return v
	}
	for i, t := range tokens {
		p := t.parent
		switch {
		case t.name && !t.quoted && t.text == name:
			switch {
			case tokenIs(tokens, i-1, "."):
				writes = writes || assignsAfter(tokens, i) || increments(tokens, i) || jsTarget(tokens, i) || jsDeleted(tokens, i)
			case tokenIs(tokens, i-1, "#"):
				// A private name: no other class's member.
			case inClass(p):
				owners = append(owners, p)
			case jsParameterProperty(tokens, i) >= 0:
				owners = append(owners, jsParameterProperty(tokens, i))
			default:
				writes = writes || jsReflected(tokens, i)
			}
		case t.quoted && t.text == name:
			switch {
			case tokenIs(tokens, i-1, "[") && tokenIs(tokens, i+1, "]") && (assignsAfter(tokens, i+1) || increments(tokens, i+1) || jsTarget(tokens, i+1) || jsDeleted(tokens, i+1)):
				writes = true
			case inClass(p) && memberPosition(tokens, i, p):
				owners = append(owners, p)
			case tokenIs(tokens, i-1, "[") && p >= 0 && inClass(tokens[p].parent) && memberPosition(tokens, p, tokens[p].parent):
				owners = append(owners, tokens[p].parent)
			default:
				writes = writes || jsReflected(tokens, i)
			}
		case tokenIs(tokens, i, "[") && inClass(p) && memberPosition(tokens, i, p) && !jsOtherKey(tokens, i, name):
			owners = append(owners, p)
		}
	}
	return owners, writes
}

// jsTarget reports a member expression ending at token k that is the target
// of a destructuring assignment or a for head.
func jsTarget(tokens []callToken, k int) bool {
	p := tokens[k].parent
	if p < 0 {
		return false
	}
	if tokenIs(tokens, p, "(") && tokenIs(tokens, p-1, "for") && (tokenIs(tokens, k+1, "of") || tokenIs(tokens, k+1, "in")) {
		return true
	}
	if !tokenIs(tokens, p, "[") && !tokenIs(tokens, p, "{") || !tokenIs(tokens, k+1, ",") && k+1 != tokens[p].close && !(tokenIs(tokens, k+1, "=") && !tokenIs(tokens, k+2, "=")) {
		return false
	}
	owner, ok := patternOwner(tokens, p)
	return ok && owner == ""
}

// jsDeleted reports a member expression ending at token k (a name after
// "." or the "]" of a key) that delete removes: delete stands before the
// chain of names, this, calls and keys joined by "." that ends there.
func jsDeleted(tokens []callToken, k int) bool {
	at := k
	for {
		if tokenIs(tokens, at, "]") || tokenIs(tokens, at, ")") {
			open := tokens[at].parent
			if open < 0 {
				return false
			}
			at = open
			if tokenIs(tokens, open-1, "]") || tokenIs(tokens, open-1, ")") || tokenName(tokens, open-1) && !tokenIs(tokens, open-1, "delete") {
				// A call's callee or a key's object.
				at = open - 1
				continue
			}
			break
		}
		if !tokenIs(tokens, at-1, ".") {
			break
		}
		at -= 2
	}
	return tokenIs(tokens, at-1, "delete")
}

// jsReflected reports a token inside the arguments of a call of Object's or
// Reflect's assign, create, defineProperty, defineProperties, set or
// setPrototypeOf: a key of the name there may become a member.
func jsReflected(tokens []callToken, i int) bool {
	for p := tokens[i].parent; p >= 0; p = tokens[p].parent {
		if tokenIs(tokens, p, "(") && tokenIs(tokens, p-2, ".") && (tokenIs(tokens, p-3, "Object") || tokenIs(tokens, p-3, "Reflect")) &&
			slices.Contains([]string{"assign", "create", "defineProperty", "defineProperties", "set", "setPrototypeOf"}, tokens[p-1].text) {
			return true
		}
	}
	return false
}

// jsPatchWords are the words of a JavaScript or TypeScript source that may
// patch classes (jsPatches): a source holding one is read for it.
var jsPatchWords = []string{"prototype", "__proto__", "defineProperty", "defineProperties", "setPrototypeOf", "Object.assign", "Reflect.", "this["}

// jsPatches reports a JavaScript or TypeScript source that may give
// classes or instances members no member of a name shows: Object.assign,
// Object.defineProperty or defineProperties, Reflect.defineProperty or
// Reflect.set on this or a prototype; setPrototypeOf; an assignment to a
// prototype, or to a computed member of one or of this whose key is no
// string; __proto__.
func jsPatches(tokens []callToken) bool {
	for i, t := range tokens {
		switch {
		case t.text == "__proto__":
			return true
		case t.quoted || !t.name:
			continue
		case t.text == "setPrototypeOf":
			return true
		case t.text == "prototype" && tokenIs(tokens, i-1, "."):
			if assignsAfter(tokens, i) || tokenIs(tokens, i+1, "[") && !jsStringKey(tokens, i+1) && assignsAfter(tokens, tokens[i+1].close) {
				return true
			}
		case t.text == "this" && tokenIs(tokens, i+1, "[") && !jsStringKey(tokens, i+1) && (assignsAfter(tokens, tokens[i+1].close) || jsTarget(tokens, tokens[i+1].close)):
			return true
		case tokenIs(tokens, i+1, "(") && tokenIs(tokens, i-1, ".") && jsPatchTarget(tokens, i+1) &&
			(t.text == "assign" && tokenIs(tokens, i-2, "Object") || t.text == "defineProperty" || t.text == "defineProperties" || t.text == "set" && tokenIs(tokens, i-2, "Reflect")):
			return true
		}
	}
	return false
}

// jsAssigns reports a call of Object.assign, Object.defineProperty or
// defineProperties, or Reflect's defineProperty or set, on anything: a
// class's own members may change through one.
func jsAssigns(tokens []callToken) bool {
	for i, t := range tokens {
		if t.name && !t.quoted && tokenIs(tokens, i+1, "(") && tokenIs(tokens, i-1, ".") && (tokenIs(tokens, i-2, "Object") || tokenIs(tokens, i-2, "Reflect")) &&
			slices.Contains([]string{"assign", "defineProperty", "defineProperties", "set", "setPrototypeOf"}, t.text) {
			return true
		}
	}
	return false
}

// jsStringKey reports a computed member "[...]" opening at open whose key
// is one string.
func jsStringKey(tokens []callToken, open int) bool {
	return tokens[open].close == open+2 && tokens[open+1].quoted
}

// jsPatchTarget reports a call opening at open whose first argument is
// this or ends in a prototype (X.prototype, this.constructor.prototype).
func jsPatchTarget(tokens []callToken, open int) bool {
	end := open + 1
	for end < tokens[open].close && !(tokenIs(tokens, end, ",") && tokens[end].parent == open) {
		if tokens[end].close > end {
			end = tokens[end].close
		}
		end++
	}
	last := end - 1
	return last == open+1 && tokenIs(tokens, last, "this") || last > open+1 && tokenIs(tokens, last, "prototype") && tokenIs(tokens, last-1, ".")
}

// pySelfClass is the class block whose instance self (token at) is: the one
// binding of self around it must be the first parameter of a def directly
// in that class's body, the def neither a staticmethod nor a classmethod;
// -1 when self is something else.
func pySelfClass(tokens []callToken, blocks []pyBlock, at int) int {
	s := scriptScopesOf(tokens, "self", true, false, nil)
	var around []scriptOccurrence
	for _, o := range s.occurrences {
		if o.kind == occBinding && (o.module || o.from <= at && at <= o.to) {
			around = append(around, o)
		}
	}
	if len(around) != 1 || around[0].module {
		return -1
	}
	p := around[0].at
	open := tokens[p].parent
	if open < 2 || !tokenIs(tokens, open, "(") || open+1 != p || !tokenIs(tokens, open-2, "def") {
		return -1
	}
	def := slices.IndexFunc(blocks, func(b pyBlock) bool { return b.kind == "def" && b.name == open-1 })
	if def < 0 {
		return -1
	}
	cls := innermostBlock(blocks, blocks[def].first, false)
	if cls < 0 || blocks[cls].kind != "class" {
		return -1
	}
	for _, d := range pyDecorators(tokens, blocks[def]) {
		if d == "staticmethod" || d == "classmethod" {
			return -1
		}
	}
	return cls
}

// pyDecorators are the names of the decorators of block b (the last part
// of a dotted name).
func pyDecorators(tokens []callToken, b pyBlock) []string {
	var out []string
	for k := b.first; k < b.name; k++ {
		if !tokenIs(tokens, k, "@") || !statementStart(tokens, k) {
			continue
		}
		n := k + 1
		for tokenName(tokens, n) && tokenIs(tokens, n+1, ".") {
			n += 2
		}
		if tokenName(tokens, n) {
			out = append(out, tokens[n].text)
		} else {
			out = append(out, "")
		}
	}
	return out
}

// pyClassMethod is the def block of the one binding of name directly in
// the class block cls: a def without decorators or, with static, one whose
// only decorator is staticmethod or classmethod; -1 when the class binds the
// name otherwise (an assignment, an annotation alone, another decorator) or
// not at all, the reason lead_ambiguous for more than one binding of it.
func pyClassMethod(tokens []callToken, blocks []pyBlock, cls int, name string, static bool) (int, string) {
	owners, _ := pyMemberSites(tokens, blocks, name)
	n := 0
	for _, o := range owners {
		if o == blocks[cls].name {
			n++
		}
	}
	def := slices.IndexFunc(blocks, func(b pyBlock) bool {
		return b.kind == "def" && tokens[b.name].text == name && innermostBlock(blocks, b.first, false) == cls
	})
	switch {
	case n > 1:
		return -1, record.LeadAmbiguous
	case n == 0 || def < 0:
		return -1, ""
	}
	decorators := pyDecorators(tokens, blocks[def])
	if static && (len(decorators) != 1 || decorators[0] != "staticmethod" && decorators[0] != "classmethod") || !static && len(decorators) > 0 {
		return -1, ""
	}
	return def, ""
}

// pyMemberSites lists what in a Python source may give a class's instances
// an attribute of name, by the class block it stands in (its name token): a
// binding of the name in a class body (a def, an assignment, an annotation
// alone, an import, a nested class, a loop target). writes reports an
// assignment, deletion, setattr or delattr of an attribute of the name
// (x.m = ..., x.m += ..., a, x.m = ..., for x.m in, del x.m, setattr(x,
// "m", v), object.__setattr__(x, "m", v)) or a definition the reader cannot
// follow (exec, eval, a three-argument type()).
func pyMemberSites(tokens []callToken, blocks []pyBlock, name string) (owners []int, writes bool) {
	class := func(i int) int {
		if b := innermostBlock(blocks, i, false); b >= 0 && blocks[b].kind == "class" {
			return blocks[b].name
		}
		return -1
	}
	s := scriptScopesOf(tokens, name, true, false, pythonImports(tokens))
	for _, o := range s.occurrences {
		if o.kind == occBinding {
			if c := class(o.at); c >= 0 {
				owners = append(owners, c)
			}
		}
	}
	for i, t := range tokens {
		switch {
		case t.quoted:
			if t.text == name && pyCallArgument(tokens, i, "setattr", "delattr", "__setattr__", "__delattr__", "type") {
				writes = true
			}
		case !t.name:
		case t.text == name && tokenIs(tokens, i-1, "."):
			writes = writes || assignsAfter(tokens, i) || pyTargetAttribute(tokens, i)
		case t.text == name && statementStart(tokens, i) && tokenIs(tokens, i+1, ":") && !slices.ContainsFunc(s.occurrences, func(o scriptOccurrence) bool { return o.at == i && o.kind == occBinding }):
			// An annotation alone (name: T) declares a dataclass's field.
			if c := class(i); c >= 0 {
				owners = append(owners, c)
			}
		case (t.text == "exec" || t.text == "eval") && tokenIs(tokens, i+1, "(") && !tokenIs(tokens, i-1, "."):
			writes = true
		case t.text == "type" && tokenIs(tokens, i+1, "(") && !tokenIs(tokens, i-1, ".") && pyArguments(tokens, i+1) >= 3:
			writes = true
		}
	}
	return owners, writes
}

// pyTargetAttribute reports an attribute ending at token i that a
// statement binds: after del, as or a for before its in, or in the targets
// before a statement's "=" (a, x.m = ...).
func pyTargetAttribute(tokens []callToken, i int) bool {
	start := logicalStart(tokens, i)
	receiver := i - 2
	for tokenIs(tokens, receiver-1, ".") && tokenName(tokens, receiver-2) {
		receiver -= 2
	}
	switch {
	case tokenIs(tokens, start, "del"), tokenIs(tokens, receiver-1, "as"):
		return true
	case tokenIs(tokens, start, "for"):
		for k := i + 1; k < len(tokens) && !statementStart(tokens, k); k++ {
			if tokenIs(tokens, k, "in") && tokens[k].depth == tokens[start].depth {
				return true
			}
		}
		return false
	}
	// Climb the displays holding the attribute ((a, x.m) = ...).
	at := i
	for p := tokens[at].parent; p >= 0 && (tokenIs(tokens, p, "(") || tokenIs(tokens, p, "[")) && !(pythonOperand(tokens, p-1) && !statementStart(tokens, p)); p = tokens[p].parent {
		at = p
	}
	if tokens[at].depth != 0 {
		return false
	}
	for k := i + 1; k < len(tokens) && !statementStart(tokens, k); k++ {
		if tokens[k].depth == 0 && tokenIs(tokens, k, "=") && !tokenIs(tokens, k+1, "=") && !strings.Contains("=<>!:", tokens[k-1].text) {
			return true
		}
	}
	return false
}

// pyCallArgument reports a token that is an argument of a call of one of
// names.
func pyCallArgument(tokens []callToken, i int, names ...string) bool {
	p := tokens[i].parent
	return p >= 1 && tokenIs(tokens, p, "(") && tokenName(tokens, p-1) && slices.Contains(names, tokens[p-1].text)
}

// pyArguments counts the arguments of the call whose "(" is token open.
func pyArguments(tokens []callToken, open int) int {
	close := tokens[open].close
	if close <= open+1 {
		return 0
	}
	n := 1
	for k := open + 1; k < close; k++ {
		if tokenIs(tokens, k, ",") && tokens[k].parent == open && k+1 < close {
			n++
		}
	}
	return n
}

// pyPatchWords are the words of a Python source that may patch classes
// (pyPatches): a source holding one is read for it.
var pyPatchWords = []string{"setattr", "delattr", "__setattr__", "__delattr__", "__dict__", "__getattribute__", "__class__", "metaclass"}

// pyPatches reports a Python source that may give classes or instances
// attributes no attribute of a name shows: setattr or delattr (or a call of
// __setattr__ or __delattr__) with a name that is no string (or not
// called), a write through __dict__ (update, setdefault, an item or the
// attribute assigned), a class's __getattribute__, an assignment to
// __class__, a metaclass other than abc's ABCMeta.
func pyPatches(tokens []callToken) bool {
	for i, t := range tokens {
		if !t.name || t.quoted {
			continue
		}
		switch t.text {
		case "setattr", "delattr":
			if !tokenIs(tokens, i+1, "(") || pyArguments(tokens, i+1) < 2 || !pyStringArgument(tokens, i+1, 1) {
				return true
			}
		case "__dict__":
			switch {
			case assignsAfter(tokens, i),
				tokenIs(tokens, i+1, ".") && (tokenIs(tokens, i+2, "update") || tokenIs(tokens, i+2, "setdefault") || tokenIs(tokens, i+2, "__setitem__")),
				tokenIs(tokens, i+1, "[") && (assignsAfter(tokens, tokens[i+1].close) || pyTargetAttribute(tokens, tokens[i+1].close)):
				return true
			}
		case "__setattr__", "__delattr__":
			// object.__setattr__(x, name, v) names it second, x.__setattr__(name, v)
			// first; its def patches nothing by itself.
			at := 0
			if tokenIs(tokens, i-1, ".") && (tokenIs(tokens, i-2, "object") || tokenIs(tokens, i-2, "type")) {
				at = 1
			}
			if !tokenIs(tokens, i-1, "def") && (!tokenIs(tokens, i+1, "(") || pyArguments(tokens, i+1) <= at || !pyStringArgument(tokens, i+1, at)) {
				return true
			}
		case "__getattribute__":
			if tokenIs(tokens, i-1, "def") {
				return true
			}
		case "__class__":
			if tokenIs(tokens, i-1, ".") && (assignsAfter(tokens, i) || pyTargetAttribute(tokens, i)) {
				return true
			}
		case "metaclass":
			if tokenIs(tokens, i+1, "=") && !tokenIs(tokens, i+2, "=") &&
				!(tokenIs(tokens, i+2, "ABCMeta") && !tokenIs(tokens, i+3, ".") || tokenIs(tokens, i+2, "abc") && tokenIs(tokens, i+3, ".") && tokenIs(tokens, i+4, "ABCMeta")) {
				return true
			}
		}
	}
	return false
}

// pyStringArgument reports a call opening at open whose argument n (from 0)
// is one string.
func pyStringArgument(tokens []callToken, open, n int) bool {
	k := open + 1
	for ; n > 0 && k < tokens[open].close; k++ {
		if tokenIs(tokens, k, ",") && tokens[k].parent == open {
			n--
		}
		if tokens[k].close > k {
			k = tokens[k].close
		}
	}
	return n == 0 && k < tokens[open].close && tokens[k].quoted && (tokenIs(tokens, k+1, ",") || k+1 == tokens[open].close)
}
