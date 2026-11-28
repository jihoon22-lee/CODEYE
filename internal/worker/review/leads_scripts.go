package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Definition leads in Python, JavaScript and TypeScript files
// (docs/REVIEW.md#단서-해석). A call on the anchor lines of the lead's name,
// or of a local name an import binds to it, binds where the file's scoping
// puts that name. A binding in a scope enclosing the call (a parameter, a
// local, a nested function, a comprehension or catch variable) is no
// project function and resolves nothing. At module level one declaration of
// the name (a def, a function declaration, or a const, let or var holding a
// function or arrow function; exported or not) is the definition, and one
// import binding is followed into the module its specifier names, through
// re-exports. Two module-level bindings of the name, or an assignment to it
// anywhere, are ambiguous: the language decides by run-time order (Python)
// or rejects the module (JavaScript). A read of the name, such as a callback
// argument, binds nothing and refuses nothing. Calls through this, self or
// a class's name bind by the rules of leads_methods.go (scriptMethods).

// Kinds of an occurrence of a name in a script's tokens.
const (
	occRead    = iota // an expression operand: an argument, a callback, an export
	occCall           // a direct call
	occSkip           // a member, a property key, a method name, a module path part or a type
	occBinding        // a declaration, a parameter, an import or another binding
	occAssign         // an assignment to the name
)

// scriptOccurrence is one occurrence of a name: a binding is visible in the
// tokens from..to, or in the whole module.
type scriptOccurrence struct {
	at, kind int
	decl     string
	from, to int
	module   bool
}

// scriptScopes is the scoping of one name in one file.
type scriptScopes struct {
	tokens      []callToken
	python      bool
	typeScript  bool
	occurrences []scriptOccurrence
	blocks      []pyBlock
}

// shadowed reports a binding of the name in a scope enclosing token x
// below module level.
func (s scriptScopes) shadowed(x int) bool {
	for _, o := range s.occurrences {
		if o.kind == occBinding && !o.module && o.decl != "class-attribute" && o.from <= x && x <= o.to {
			return true
		}
	}
	return false
}

// moduleBindings are the bindings of the name at module level, and whether
// the module assigns to the name where no enclosing scope binds it.
func (s scriptScopes) moduleBindings() (bindings []scriptOccurrence, assigned bool) {
	for _, o := range s.occurrences {
		switch {
		case o.kind == occAssign:
			assigned = assigned || !s.shadowed(o.at)
		case o.kind == occBinding && o.module:
			bindings = append(bindings, o)
		}
	}
	return bindings, assigned
}

// scriptScopesOf reads the scoping of name in tokens.
func scriptScopesOf(tokens []callToken, name string, python, typeScript bool, imports []scriptBinding) scriptScopes {
	s := scriptScopes{tokens: tokens, python: python, typeScript: typeScript}
	if python {
		s.blocks = pythonBlocks(tokens)
		s.occurrences = pythonOccurrences(tokens, name, s.blocks, imports)
	} else {
		s.occurrences = javascriptOccurrences(tokens, name, imports, typeScript)
	}
	return s
}

// pyBlock is a def, class or lambda of Python tokens: first its first token
// (a decorator, async, def, class or lambda), name its name token (-1 for a
// lambda), open the first token of its own scope (its parameters onward),
// colon the ":" before its body, last its last token.
type pyBlock struct {
	kind                           string
	first, name, open, colon, last int
}

// pythonBlocks lists the def, class and lambda blocks of Python tokens; a
// def or class body ends before the next statement at its column or left
// of it.
func pythonBlocks(tokens []callToken) []pyBlock {
	var out []pyBlock
	for i, t := range tokens {
		if !t.name {
			continue
		}
		switch t.text {
		case "def", "class":
			if tokenIs(tokens, i-1, ".") || !tokenName(tokens, i+1) {
				continue
			}
			start := i
			if tokenIs(tokens, i-1, "async") {
				start = i - 1
			}
			if !statementStart(tokens, start) {
				continue
			}
			column := tokens[start].column
			first := start
			for {
				j := first - 1
				for j >= 0 && !statementStart(tokens, j) {
					j--
				}
				if j < 0 || !tokenIs(tokens, j, "@") || tokens[j].column != column {
					break
				}
				first = j
			}
			colon := -1
			for k := i + 2; k < len(tokens); k++ {
				if tokens[k].depth == t.depth && tokenIs(tokens, k, ":") {
					colon = k
					break
				}
				if statementStart(tokens, k) {
					break
				}
			}
			if colon < 0 {
				continue
			}
			last := len(tokens) - 1
			for k := colon + 1; k < len(tokens); k++ {
				if statementStart(tokens, k) && tokens[k].column <= column {
					last = k - 1
					break
				}
			}
			out = append(out, pyBlock{kind: t.text, first: first, name: i + 1, open: i + 2, colon: colon, last: last})
		case "lambda":
			colon := -1
			for k := i + 1; k < len(tokens); k++ {
				if tokens[k].depth < t.depth || statementStart(tokens, k) {
					break
				}
				if tokens[k].depth == t.depth && tokenIs(tokens, k, ":") {
					colon = k
					break
				}
			}
			if colon < 0 {
				continue
			}
			last := colon
			for k := colon + 1; k < len(tokens); k++ {
				if tokens[k].depth < t.depth || statementStart(tokens, k) || (tokens[k].depth == t.depth && tokenIs(tokens, k, ",")) {
					break
				}
				last = k
			}
			out = append(out, pyBlock{kind: "lambda", first: i, name: -1, open: i + 1, colon: colon, last: last})
		}
	}
	return out
}

// innermostBlock is the innermost block whose own scope holds token x, -1
// when none (module level); classes are skipped when skipClasses.
func innermostBlock(blocks []pyBlock, x int, skipClasses bool) int {
	best := -1
	for k, b := range blocks {
		if b.open <= x && x <= b.last && (!skipClasses || b.kind != "class") && (best < 0 || b.open > blocks[best].open) {
			best = k
		}
	}
	return best
}

// logicalStart is the first token of the logical line holding token i.
func logicalStart(tokens []callToken, i int) int {
	for i > 0 && !statementStart(tokens, i) {
		i--
	}
	return i
}

// pythonOccurrences classifies the occurrences of name in Python tokens.
func pythonOccurrences(tokens []callToken, name string, blocks []pyBlock, imports []scriptBinding) []scriptOccurrence {
	bound := map[int]bool{}
	for _, b := range imports {
		bound[b.token] = true
	}
	var out []scriptOccurrence
	for i, t := range tokens {
		if !t.name || t.text != name {
			continue
		}
		o := scriptOccurrence{at: i, kind: occRead}
		start := logicalStart(tokens, i)
		switch {
		case tokenIs(tokens, i-1, "."):
			o.kind = occSkip
		case bound[i]:
			o.kind, o.decl = occBinding, "import"
		case tokenIs(tokens, start, "import") || tokenIs(tokens, start, "from"):
			o.kind = occSkip
		case tokenIs(tokens, i-1, "def") || tokenIs(tokens, i-1, "class"):
			o.kind, o.decl = occBinding, tokens[i-1].text
		case tokenIs(tokens, i+1, "("):
			o.kind = occCall
		case pythonBinds(tokens, i, start, blocks):
			o.kind, o.decl = occBinding, "other"
		}
		if o.kind == occBinding {
			owner := innermostBlock(blocks, i, false)
			if owner < 0 {
				o.module = true
				if o.decl != "def" && o.decl != "import" || tokens[start].column != 0 {
					o.decl = "other"
				}
			} else {
				o.from, o.to = blocks[owner].open, blocks[owner].last
				if blocks[owner].kind == "class" {
					// A class body's binding is an attribute: it reaches calls
					// in that body, not in its methods.
					o.decl = "class-attribute"
				}
			}
		}
		out = append(out, o)
	}
	return out
}

// pythonBinds reports a Python occurrence that may bind the name: after as,
// global, nonlocal, del or for; a for or comprehension target; a parameter
// of a def or lambda; an assignment, annotated or augmented assignment or
// walrus target; a capture in a case pattern. A target inside a tuple or
// list display ((a, name), [a, name], nested or starred) is a target where
// one of the displays holding it stands.
func pythonBinds(tokens []callToken, i, start int, blocks []pyBlock) bool {
	for _, k := range []string{"global", "nonlocal"} {
		if tokenIs(tokens, i-1, k) {
			return true
		}
	}
	if tokenIs(tokens, start, "case") {
		return true
	}
	for _, b := range blocks {
		if b.kind == "lambda" && b.first < i && i < b.colon {
			return true
		}
	}
	if p := tokens[i].parent; p >= 1 && tokenIs(tokens, p, "(") && tokenName(tokens, p-1) && tokenIs(tokens, p-2, "def") {
		return !tokenIs(tokens, i-1, "=") && !tokenIs(tokens, i-1, ":")
	}
	if tokenIs(tokens, i+1, ":") && tokenIs(tokens, i+2, "=") {
		return true
	}
	if tokenIs(tokens, i+1, "=") && !tokenIs(tokens, i+2, "=") {
		p := tokens[i].parent
		return p < 0 || !tokenIs(tokens, p, "(") // a keyword argument names no binding
	}
	for g := i; ; {
		if pythonTarget(tokens, g) {
			return true
		}
		// The display holding g as an element: "(" or "[" opening a logical
		// line or after no operand (not a call or a subscript), g between
		// "(", "[", "," or "*" and "," or the display's end.
		p, end := tokens[g].parent, g
		if tokens[g].close > g {
			end = tokens[g].close
		}
		if p < 0 || !tokenIs(tokens, p, "(") && !tokenIs(tokens, p, "[") || pythonOperand(tokens, p-1) && !statementStart(tokens, p) ||
			!tokenIs(tokens, g-1, "(") && !tokenIs(tokens, g-1, "[") && !tokenIs(tokens, g-1, ",") && !tokenIs(tokens, g-1, "*") ||
			!tokenIs(tokens, end+1, ",") && end+1 != tokens[p].close {
			return false
		}
		g = p
	}
}

// pythonTarget reports token g (a name, or the "(" or "[" of a display) in
// a target list: after as, del or for, among the names, starred names and
// displays after a for before its in, or among those of a logical line's
// top level before an "=" (no ==, <=, >= or !=).
func pythonTarget(tokens []callToken, g int) bool {
	for _, k := range []string{"as", "del", "for"} {
		if tokenIs(tokens, g-1, k) {
			return true
		}
	}
walk:
	for j := g - 1; j >= 0; j-- {
		switch {
		case tokenIs(tokens, j, ")") || tokenIs(tokens, j, "]"):
			// A display before g; its closer stands one level below it.
			if j = tokens[j].parent; tokens[j].depth != tokens[g].depth {
				break walk
			}
		case tokens[j].depth != tokens[g].depth:
			break walk
		case tokenIs(tokens, j, "for"):
			return true
		case !tokens[j].name && !tokenIs(tokens, j, ",") && !tokenIs(tokens, j, "*") || tokenIs(tokens, j, "in"):
			break walk
		}
	}
	if tokens[g].depth != 0 {
		return false
	}
	end := g
	if tokens[g].close > g {
		end = tokens[g].close
	}
	for k := end + 1; k < len(tokens) && !statementStart(tokens, k); k++ {
		if tokens[k].depth == 0 && tokenIs(tokens, k, "=") && !tokenIs(tokens, k+1, "=") && !tokenIs(tokens, k-1, "=") &&
			!tokenIs(tokens, k-1, "!") && !(tokenIs(tokens, k-1, "<") && !tokenIs(tokens, k-2, "<")) && !(tokenIs(tokens, k-1, ">") && !tokenIs(tokens, k-2, ">")) {
			return true
		}
	}
	return false
}

// pythonOperand reports token k as the end of an operand a "(" or "[" after
// it calls or subscripts: a name that is no keyword, a literal or a closing
// bracket.
func pythonOperand(tokens []callToken, k int) bool {
	if k < 0 || k >= len(tokens) {
		return false
	}
	t := tokens[k]
	switch {
	case t.quoted || t.stringLiteral:
		return true
	case t.name:
		return !pythonKeywords[t.text]
	}
	return t.text == ")" || t.text == "]" || t.text == "}"
}

// pythonKeywords are the Python keywords an expression may follow.
var pythonKeywords = leadNames(`and as assert async await del elif else except for from if import in is lambda nonlocal global not or
	raise return while with yield case match`)

// javascriptOccurrences classifies the occurrences of name in JavaScript or
// TypeScript tokens.
func javascriptOccurrences(tokens []callToken, name string, imports []scriptBinding, typeScript bool) []scriptOccurrence {
	bound := map[int]bool{}
	types := map[int]bool{}
	for _, b := range imports {
		if b.typeOnly {
			types[b.token] = true
		} else {
			bound[b.token] = true
		}
	}
	var out []scriptOccurrence
	for i, t := range tokens {
		if !t.name || t.text != name || t.quoted {
			continue
		}
		o := scriptOccurrence{at: i, kind: occRead}
		spread := tokenIs(tokens, i-1, ".") && tokenIs(tokens, i-2, ".") && tokenIs(tokens, i-3, ".")
		switch {
		case tokenIs(tokens, i-1, ".") && !spread, tokenIs(tokens, i-1, "#"), classField(tokens, i):
			o.kind = occSkip
		case bound[i]:
			o.kind, o.decl, o.module = occBinding, "import", true
		case types[i] || importStatement(tokens, i):
			o.kind = occSkip
		case tokenIs(tokens, i-1, "function") || tokenIs(tokens, i-1, "*") && tokenIs(tokens, i-2, "function"):
			o.kind, o.decl = occBinding, "function"
		case tokenIs(tokens, i-1, "class"):
			o.kind, o.decl = occBinding, "class"
		case tokenIs(tokens, i-1, "const") || tokenIs(tokens, i-1, "let") || tokenIs(tokens, i-1, "var"):
			o.kind, o.decl = occBinding, tokens[i-1].text
		case tokenIs(tokens, i-1, "interface") || tokenIs(tokens, i-1, "type") && tokenIs(tokens, i+1, "="):
			o.kind = occSkip
		case tokenIs(tokens, i-1, "enum") || tokenIs(tokens, i-1, "namespace") || tokenIs(tokens, i-1, "module"):
			o.kind, o.decl = occBinding, "other"
		case methodName(tokens, i):
			o.kind = occSkip
		case tokenIs(tokens, i+1, "("):
			o.kind = occCall
		default:
			o.kind, o.decl = javascriptPosition(tokens, i)
		}
		if o.kind == occBinding && !o.module {
			o.from, o.to, o.module = javascriptScope(tokens, i, o.decl, typeScript)
		}
		out = append(out, o)
	}
	return out
}

// importStatement reports a token inside an import or export statement at
// module level (its module path, an exported or imported name).
func importStatement(tokens []callToken, i int) bool {
	at := i
	for tokens[at].depth > 0 && tokens[at].parent >= 0 {
		at = tokens[at].parent
	}
	k := logicalStart(tokens, at)
	return tokenIs(tokens, k, "import") && !tokenIs(tokens, k+1, "(") && !tokenIs(tokens, k+1, ".") ||
		tokenIs(tokens, k, "export") && (tokenIs(tokens, k+1, "{") || tokenIs(tokens, k+1, "*") || tokenIs(tokens, k+1, "type") && tokenIs(tokens, k+2, "{"))
}

// methodName reports a class or object member head name(...) {,
// name<T>(...) { or name(...): T {: not a call and no binding of the name.
// A call has no body after it, so any such head in braces is a member's,
// whatever stands before it (a decorator, a modifier); one whose return
// annotation the reader cannot follow is a member's in a class body, or
// where a member of an object literal begins.
func methodName(tokens []callToken, i int) bool {
	open := skipGenerics(tokens, i+1)
	if !tokenName(tokens, i) || !tokenIs(tokens, open, "(") {
		return false
	}
	p := tokens[i].parent
	if p < 0 || !tokenIs(tokens, p, "{") || controlKeyword(tokens, i) && !classBody(tokens, p) {
		return false
	}
	close := tokens[open].close
	switch {
	case functionBody(tokens, close) >= 0:
		return true
	case !tokenIs(tokens, close+1, ":"):
		return false
	case classBody(tokens, p):
		return true
	}
	prev := i - 1
	return prev == p || slices.ContainsFunc(classModifiers, func(m string) bool { return tokenIs(tokens, prev, m) }) ||
		tokenIs(tokens, prev, "}") || tokenIs(tokens, prev, ";") || tokenIs(tokens, prev, ",")
}

// classModifiers are the keywords that may open a class member.
var classModifiers = []string{"static", "async", "get", "set", "*", "public", "private", "protected", "readonly", "override", "abstract", "declare", "accessor"}

// classBody reports a "{" that opens a class body: class, an optional name,
// type parameters, extends and implements clauses, then "{".
func classBody(tokens []callToken, open int) bool {
	depth := tokens[open].depth
	for k := open - 1; k >= 0 && open-k < 200; k-- {
		t := tokens[k]
		switch {
		case t.depth > depth:
		case tokenIs(tokens, k, "class"):
			return true
		case t.name && !t.quoted, len(t.text) == 1 && strings.Contains(".,<>(", t.text) && !t.quoted:
		default:
			return false
		}
	}
	return false
}

// memberPosition reports a token of a class body opening at open that
// begins a member: right after the body's "{", a ";", a "}" or a modifier,
// or on a new line after a token that ends no expression part.
func memberPosition(tokens []callToken, i, open int) bool {
	prev := i - 1
	switch {
	case prev == open, tokenIs(tokens, prev, ";"), tokenIs(tokens, prev, "}"),
		slices.ContainsFunc(classModifiers, func(m string) bool { return tokenIs(tokens, prev, m) }):
		return true
	}
	return tokens[prev].endLine < tokens[i].line && tokens[prev].text != "" && !strings.Contains("=(,:?[|&!+-*/%<>.", tokens[prev].text)
}

// classField reports the name of a class field (name = ..., name: T,
// name;, name?: T, name!: T): a property of each instance, no binding of
// the name.
func classField(tokens []callToken, i int) bool {
	p := tokens[i].parent
	if p < 0 || !tokenIs(tokens, p, "{") || !classBody(tokens, p) || !memberPosition(tokens, i, p) {
		return false
	}
	next := i + 1
	return next >= len(tokens) || tokens[next].line > tokens[i].line || tokenIs(tokens, next, "=") || tokenIs(tokens, next, ":") ||
		tokenIs(tokens, next, ";") || tokenIs(tokens, next, "?") || tokenIs(tokens, next, "!") || tokenIs(tokens, next, "}")
}

// javascriptPosition classifies an occurrence that is no declaration, call
// or member: a parameter, a destructuring binding, an assignment, a
// property key or a read.
func javascriptPosition(tokens []callToken, i int) (kind int, decl string) {
	p := tokens[i].parent
	if tokenIs(tokens, i+1, "=") && tokenIs(tokens, i+2, ">") {
		return occBinding, "param"
	}
	if p >= 0 && tokenIs(tokens, p, "(") && paramList(tokens, p) {
		if tokenIs(tokens, i-1, "=") || tokenIs(tokens, i-1, ":") || tokenIs(tokens, i-1, "?") {
			return occRead, ""
		}
		return occBinding, "param"
	}
	if p >= 0 && (tokenIs(tokens, p, "{") || tokenIs(tokens, p, "[")) {
		if owner, ok := patternOwner(tokens, p); ok {
			if tokenIs(tokens, p, "{") && tokenIs(tokens, i+1, ":") {
				return occSkip, ""
			}
			if tokenIs(tokens, i-1, "=") {
				return occRead, ""
			}
			if owner == "" {
				return occAssign, ""
			}
			return occBinding, owner
		}
		if tokenIs(tokens, p, "{") && tokenIs(tokens, i+1, ":") && (tokenIs(tokens, i-1, "{") || tokenIs(tokens, i-1, ",")) {
			return occSkip, ""
		}
	}
	if assignsAfter(tokens, i) || increments(tokens, i) {
		return occAssign, ""
	}
	if p >= 0 && tokenIs(tokens, p, "(") && tokenIs(tokens, p-1, "for") && (tokenIs(tokens, i+1, "of") || tokenIs(tokens, i+1, "in")) {
		return occAssign, ""
	}
	return occRead, ""
}

// assignsAfter reports an assignment operator after token i: =, or a
// compound one (+=, ??=, >>>=, ...), never a comparison or an arrow.
func assignsAfter(tokens []callToken, i int) bool {
	k := i + 1
	ops := 0
	for ops < 3 && k < len(tokens) && len(tokens[k].text) == 1 && strings.Contains("+-*/%&|^<>!?~", tokens[k].text) && !tokens[k].quoted {
		k++
		ops++
	}
	if !tokenIs(tokens, k, "=") || tokenIs(tokens, k+1, "=") || tokenIs(tokens, k+1, ">") {
		return false
	}
	if ops == 1 && (tokenIs(tokens, i+1, "<") || tokenIs(tokens, i+1, ">") || tokenIs(tokens, i+1, "!")) {
		return false
	}
	return true
}

// increments reports ++ or -- right before or after token i.
func increments(tokens []callToken, i int) bool {
	twin := func(a, b int) bool {
		return a >= 0 && b < len(tokens) && (tokenIs(tokens, a, "+") && tokenIs(tokens, b, "+") || tokenIs(tokens, a, "-") && tokenIs(tokens, b, "-")) &&
			tokens[a].line == tokens[b].line && tokens[b].column == tokens[a].column+1
	}
	return twin(i+1, i+2) || twin(i-2, i-1)
}

// paramList reports a "(" group that holds parameters: of a function,
// method, arrow function or catch clause. A group whose ")" a body follows
// ("{", or a return annotation and "{" or "=>") or "=>" holds parameters
// whatever stands before it (type parameters, a computed or private name, a
// decorator), unless it is the condition of if, while, for, for await, with
// or switch; so does a member head methodName reads.
func paramList(tokens []callToken, open int) bool {
	switch {
	case tokenIs(tokens, open-1, "function"), tokenIs(tokens, open-1, "catch"),
		tokenName(tokens, open-1) && tokenIs(tokens, open-2, "function"),
		tokenIs(tokens, open-1, "*") && tokenIs(tokens, open-2, "function"):
		return true
	case controlKeyword(tokens, open-1) && !(tokens[open].parent >= 0 && classBody(tokens, tokens[open].parent)),
		tokenIs(tokens, open-1, "await") && tokenIs(tokens, open-2, "for"):
		return false
	}
	close := tokens[open].close
	switch {
	case tokenIs(tokens, close+1, "{"), tokenIs(tokens, close+1, "=") && tokenIs(tokens, close+2, ">"):
		return true
	case tokenIs(tokens, close+1, ":"):
		if k, _ := returnType(tokens, close); k >= 0 {
			return true
		}
	}
	head := open - 1
	if tokenIs(tokens, head, ">") {
		head = genericsStart(tokens, head) - 1
	}
	return tokenName(tokens, head) && methodName(tokens, head)
}

// controlKeyword reports a keyword token k whose "(" holds a condition or a
// loop head (if, while, for, with, switch), not a method's name after ".".
func controlKeyword(tokens []callToken, k int) bool {
	if tokenIs(tokens, k-1, ".") {
		return false
	}
	for _, w := range []string{"if", "while", "for", "with", "switch"} {
		if tokenIs(tokens, k, w) {
			return true
		}
	}
	return false
}

// genericsStart is the "<" opening the type parameter list whose ">" is
// token close, -1 when none is found.
func genericsStart(tokens []callToken, close int) int {
	depth := 0
	for k := close; k >= 0; k-- {
		switch {
		case tokenIs(tokens, k, ">") && !tokenIs(tokens, k-1, "="):
			depth++
		case tokenIs(tokens, k, "<"):
			if depth--; depth == 0 {
				return k
			}
		case tokenIs(tokens, k, ";") || tokenIs(tokens, k, "{"):
			return -1
		case tokenIs(tokens, k, ")") || tokenIs(tokens, k, "]") || tokenIs(tokens, k, "}"):
			k = tokens[k].parent // a group inside the list
		}
	}
	return -1
}

// patternOwner reports a "{" or "[" group that is a destructuring pattern
// and what binds it: const, let or var, param, or "" for an assignment.
func patternOwner(tokens []callToken, open int) (string, bool) {
	for _, k := range []string{"const", "let", "var"} {
		if tokenIs(tokens, open-1, k) {
			return k, true
		}
	}
	close := tokens[open].close
	parent := tokens[open].parent
	switch {
	case parent >= 0 && tokenIs(tokens, parent, "(") && paramList(tokens, parent):
		return "param", true
	case parent >= 0 && (tokenIs(tokens, parent, "{") || tokenIs(tokens, parent, "[")):
		if owner, ok := patternOwner(tokens, parent); ok {
			return owner, true
		}
	}
	if tokenIs(tokens, close+1, "=") && !tokenIs(tokens, close+2, "=") && !tokenIs(tokens, close+2, ">") {
		return "", true
	}
	if parent >= 0 && tokenIs(tokens, parent, "(") && tokenIs(tokens, parent-1, "for") && (tokenIs(tokens, close+1, "of") || tokenIs(tokens, close+1, "in")) {
		return "", true
	}
	return "", false
}

// returnType skips a TypeScript return annotation after the parameter list
// closing at close: the index of the token after it ("{" of a body, or "="
// of an arrow's "=>"), and whether "=>" follows. -1 when the annotation
// cannot be read to a body or arrow: unions, intersections, generics,
// arrays, tuples, object and function types, qualified names, literals,
// readonly, keyof, typeof and type predicates (x is T) are read.
func returnType(tokens []callToken, close int) (int, bool) {
	k := close + 1
	if !tokenIs(tokens, k, ":") {
		return -1, false
	}
	k++
	angle := 0
	start := k
	for k < len(tokens) {
		t := tokens[k]
		switch {
		case t.quoted || t.stringLiteral:
		case t.name:
		case tokenIs(tokens, k, "{"):
			if angle == 0 && k > start && typeEnds(tokens, k-1) {
				return k, false
			}
			k = t.close + 1
			continue
		case tokenIs(tokens, k, "(") || tokenIs(tokens, k, "["):
			k = t.close + 1
			continue
		case tokenIs(tokens, k, "<"):
			angle++
		case tokenIs(tokens, k, ">"):
			if tokenIs(tokens, k-1, "=") {
				break
			}
			angle--
			if angle < 0 {
				return -1, false
			}
		case tokenIs(tokens, k, "="):
			if tokenIs(tokens, k+1, ">") {
				if angle == 0 && k > start && typeEnds(tokens, k-1) {
					return k, true
				}
				break
			}
			return -1, false
		case tokenIs(tokens, k, ","):
			if angle == 0 {
				return -1, false
			}
		case len(t.text) == 1 && strings.Contains("|&.?!-", t.text), len(t.text) == 1 && t.text[0] >= '0' && t.text[0] <= '9':
		default:
			return -1, false
		}
		k++
	}
	return -1, false
}

// typeEnds reports a token that can end a type: a name that is no type
// operator, a literal, or a closing bracket.
func typeEnds(tokens []callToken, k int) bool {
	t := tokens[k]
	switch {
	case t.quoted || t.stringLiteral:
		return true
	case t.name:
		return !slices.Contains([]string{"keyof", "typeof", "is", "extends", "readonly", "infer", "asserts", "unique", "new"}, t.text)
	case len(t.text) == 1 && t.text[0] >= '0' && t.text[0] <= '9':
		return true
	}
	return t.text == ")" || t.text == "]" || t.text == "}" || t.text == ">" && !tokenIs(tokens, k-1, "=")
}

// functionBody is the "{" of the body after a parameter list closing at
// close, through a TypeScript return annotation; -1 when none.
func functionBody(tokens []callToken, close int) int {
	if tokenIs(tokens, close+1, "{") {
		return close + 1
	}
	if k, arrow := returnType(tokens, close); k >= 0 && !arrow {
		return k
	}
	return -1
}

// arrowEnd is the last token of an arrow function whose "=>" ends at
// token arrow (the ">"): its block body, or its expression up to the
// enclosing group's end, a "," or ";" at its level, or at the top level the
// line break where the expression ends (scriptLineGoesOn). In TypeScript a
// "," inside type arguments (f<A, B>(...)) ends nothing: a "<...>" list
// typeArguments reads is skipped, and where a "(" does not follow it (a
// template or comparisons may) the end is one the reader is not sure of;
// past a "<" it cannot read to its end, no "," ends the expression and the
// end is not sure either. Where a later line may go on with the expression
// or not, open reports an end the reader is not sure of; wide reads such a
// line as going on (a scope that may be too wide, never too narrow).
func arrowEnd(tokens []callToken, arrow int, wide, typeScript bool) (last int, open bool) {
	k := arrow + 1
	if tokenIs(tokens, k, "{") {
		return tokens[k].close, false
	}
	if k >= len(tokens) {
		return arrow, false
	}
	depth := tokens[k].depth
	last = arrow
	unsure, commas := false, true
	for m := k; m < len(tokens); m++ {
		t := tokens[m]
		if t.depth < depth || t.depth == depth && (commas && tokenIs(tokens, m, ",") || tokenIs(tokens, m, ";")) {
			break
		}
		if depth == 0 && m > k && t.line > tokens[last].endLine {
			switch scriptLineGoesOn(tokens, last, m) {
			case lineEnds:
				return last, unsure
			case lineUnsure:
				if !wide {
					return last, true
				}
			}
		}
		last = m
		switch {
		case t.close > m:
			m = t.close
			last = m
		case typeScript && commas && tokenIs(tokens, m, "<"):
			after := typeArguments(tokens, m)
			if after == typeArgumentsUnread {
				unsure, commas = true, false
			}
			if after < 0 {
				break
			}
			for c := m + 1; c < after; c++ {
				if tokenIs(tokens, c, ",") && tokens[c].depth == depth && !tokenIs(tokens, after, "(") {
					unsure = true
				}
			}
			m = after - 1
			last = m
		}
	}
	return last, unsure
}

// What a line break does to an expression (scriptLineGoesOn).
const (
	lineEnds = iota
	lineGoesOn
	lineUnsure
)

// operandKeywords are the keywords an operand follows inside an expression.
var operandKeywords = []string{"typeof", "void", "delete", "await", "new", "in", "instanceof", "yield", "as", "satisfies", "keyof", "extends"}

// scriptLineGoesOn reports what the line break between token last and
// token next (the first of a later line) does to an expression, by the
// rules that insert a semicolon: the expression goes on after an operator
// or a keyword an operand follows, and, after an operand, before ".", "?",
// ":", "(", "[", a binary operator, in or instanceof; it ends before a name,
// a number, a string, "{", "!", "~", "++", "--", "@" or "#". A template may
// go on with it as a tagged template, and tokens do not tell a template
// from a string with escapes; nor do they tell a ">" ending type arguments
// from a comparison's. Those, and anything else, are unsure.
func scriptLineGoesOn(tokens []callToken, last, next int) int {
	p, n := tokens[last], tokens[next]
	switch {
	case p.quoted || p.stringLiteral, p.name && !slices.Contains(operandKeywords, p.text),
		p.text == ")" || p.text == "]" || p.text == "}", len(p.text) == 1 && p.text[0] >= '0' && p.text[0] <= '9',
		(p.text == "+" || p.text == "-") && postfixAt(tokens, last), p.text == "!" && last > 0 && tokens[last-1].endLine == p.line && operandEnds(tokens, last-1):
		// An operand ends the line.
	case p.text == ">" && !tokenIs(tokens, last-1, "="):
		return lineUnsure
	default:
		return lineGoesOn
	}
	switch {
	case n.name && (n.text == "in" || n.text == "instanceof"):
		return lineGoesOn
	case n.quoted || n.stringLiteral && n.character || n.name || len(n.text) == 1 && n.text[0] >= '0' && n.text[0] <= '9':
		return lineEnds
	case n.stringLiteral:
		return lineUnsure // a template, or a string with escapes
	}
	switch n.text {
	case ".", "?", ":", "(", "[", "*", "/", "%", "&", "|", "^", "<", ">", "=":
		return lineGoesOn
	case "+", "-":
		if tokenIs(tokens, next+1, n.text) && tokens[next+1].line == n.line && tokens[next+1].column == n.column+1 {
			return lineEnds // a prefix ++ or -- of the next statement
		}
		return lineGoesOn
	case "!":
		if tokenIs(tokens, next+1, "=") {
			return lineGoesOn
		}
		return lineEnds
	case "~", "{", "@", "#":
		return lineEnds
	}
	return lineUnsure
}

// postfixAt reports a "+" or "-" token k ending a postfix ++ or -- after an
// operand on its line.
func postfixAt(tokens []callToken, k int) bool {
	t := tokens[k]
	return k > 1 && tokenIs(tokens, k-1, t.text) && tokens[k-1].line == t.line && tokens[k-1].column+1 == t.column &&
		tokens[k-2].endLine == t.line && operandEnds(tokens, k-2)
}

// operandEnds reports token k as the end of an operand: a literal, a name
// that is no keyword an operand follows, a number or a closing bracket.
func operandEnds(tokens []callToken, k int) bool {
	t := tokens[k]
	return t.quoted || t.stringLiteral || t.name && !slices.Contains(operandKeywords, t.text) ||
		t.text == ")" || t.text == "]" || t.text == "}" || len(t.text) == 1 && t.text[0] >= '0' && t.text[0] <= '9'
}

// javascriptScope is the token range a non-import binding at token i is
// visible in, or module when it binds at module level: a let, const or
// class declaration's innermost block (module when none); a var's function
// (the body of the innermost function holding it, module when none), as a
// function declaration's in a block (sloppy code hoists it there); a
// parameter's function; a named function or class expression's own body
// (never the module).
func javascriptScope(tokens []callToken, i int, decl string, typeScript bool) (from, to int, module bool) {
	block := func(k int) (int, int, bool) {
		for p := tokens[k].parent; p >= 0; p = tokens[p].parent {
			if tokenIs(tokens, p, "{") {
				return p, tokens[p].close, false
			}
		}
		return 0, len(tokens) - 1, true
	}
	function := func(k int) (int, int, bool) {
		for p := tokens[k].parent; p >= 0; p = tokens[p].parent {
			if tokenIs(tokens, p, "{") && functionBrace(tokens, p) {
				return p, tokens[p].close, false
			}
		}
		return 0, len(tokens) - 1, true
	}
	statement := func(k int) (int, int, bool) {
		top := k
		for tokens[top].depth > 0 {
			top = tokens[top].parent
		}
		if top == k {
			return 0, len(tokens) - 1, true
		}
		return top, max(tokens[top].close, top), false
	}
	arrow := func(from, arrow int) (int, int, bool) {
		last, _ := arrowEnd(tokens, arrow, true, typeScript)
		return from, last, false
	}
	switch decl {
	case "param":
		p := tokens[i].parent
		for p >= 0 && !(tokenIs(tokens, p, "(") && paramList(tokens, p)) {
			p = tokens[p].parent
		}
		if p < 0 {
			// A single parameter before "=>".
			return arrow(i, i+2)
		}
		close := tokens[p].close
		if tokenIs(tokens, p-1, "catch") && tokenIs(tokens, close+1, "{") {
			return p, tokens[close+1].close, false
		}
		if body := functionBody(tokens, close); body >= 0 {
			return p, tokens[body].close, false
		}
		if k, isArrow := returnType(tokens, close); isArrow {
			return arrow(p, k+1)
		}
		if tokenIs(tokens, close+1, "=") && tokenIs(tokens, close+2, ">") {
			return arrow(p, close+2)
		}
		return statement(i)
	case "function", "class":
		key := i - 1
		if tokenIs(tokens, key, "*") {
			key--
		}
		if !declarationPosition(tokens, key) {
			// A named function or class expression binds its name in its
			// own body; one the reader cannot follow, in the rest of the
			// file.
			if end := ownBodyEnd(tokens, i, decl); end >= 0 {
				return i, end, false
			}
			return i, len(tokens) - 1, false
		}
		if tokens[key].depth > 0 && decl == "function" {
			return function(key)
		}
		return block(key)
	case "var", "const", "let":
		k := declarationKeyword(tokens, i)
		switch {
		case k < 0:
			return statement(i)
		case decl == "var":
			return function(k)
		}
		return block(k)
	}
	return statement(i)
}

// functionBrace reports a "{" token b that opens a function's body: after
// "=>", or after the ")" of a parameter list (not a catch clause's),
// directly or past a return annotation.
func functionBrace(tokens []callToken, b int) bool {
	if tokenIs(tokens, b-1, ">") && tokenIs(tokens, b-2, "=") {
		return true
	}
	for k := b - 1; k >= 0 && b-k < 200; k-- {
		// A closer stands one level below its opener.
		closer := tokenIs(tokens, k, ")") || tokenIs(tokens, k, "]") || tokenIs(tokens, k, "}")
		if closer && tokens[tokens[k].parent].depth != tokens[b].depth || !closer && tokens[k].depth != tokens[b].depth {
			return false
		}
		switch {
		case tokenIs(tokens, k, ")"):
			open := tokens[k].parent
			if paramList(tokens, open) && !tokenIs(tokens, open-1, "catch") && functionBody(tokens, k) == b {
				return true
			}
			k = open
		case closer:
			k = tokens[k].parent
		case tokenIs(tokens, k, ";") || tokenIs(tokens, k, "{") || tokenIs(tokens, k, "=") && !tokenIs(tokens, k+1, ">"):
			return false
		}
	}
	return false
}

// ownBodyEnd is the last token of the body of the function or class whose
// name is token i: a function's parameters and body, a class's first "{"
// at its level; -1 when the reader cannot follow it.
func ownBodyEnd(tokens []callToken, i int, decl string) int {
	if decl == "function" {
		open := skipGenerics(tokens, i+1)
		if !tokenIs(tokens, open, "(") {
			return -1
		}
		if body := functionBody(tokens, tokens[open].close); body >= 0 {
			return tokens[body].close
		}
		return -1
	}
	for k := i + 1; k < len(tokens) && tokens[k].depth >= tokens[i].depth; k++ {
		switch {
		case tokenIs(tokens, k, "{"):
			return tokens[k].close
		case tokenIs(tokens, k, ";"):
			return -1
		case tokens[k].close > k:
			k = tokens[k].close
		}
	}
	return -1
}

// declarationKeyword is the const, let or var token declaring the binding
// at token i: right before it, or before the outermost pattern holding it;
// -1 when none.
func declarationKeyword(tokens []callToken, i int) int {
	for _, k := range []string{"const", "let", "var"} {
		if tokenIs(tokens, i-1, k) {
			return i - 1
		}
	}
	for p := tokens[i].parent; p >= 0; p = tokens[p].parent {
		if !tokenIs(tokens, p, "{") && !tokenIs(tokens, p, "[") {
			return -1
		}
		for _, k := range []string{"const", "let", "var"} {
			if tokenIs(tokens, p-1, k) {
				return p - 1
			}
		}
	}
	return -1
}

// declarationPosition reports a function or class keyword that begins a
// statement (after export, default, async or declare, or at a statement's
// start), not an expression.
func declarationPosition(tokens []callToken, key int) bool {
	k := key - 1
	for tokenIs(tokens, k, "async") || tokenIs(tokens, k, "default") || tokenIs(tokens, k, "export") || tokenIs(tokens, k, "declare") || tokenIs(tokens, k, "abstract") {
		k--
	}
	if k < 0 {
		return true
	}
	// A literal (a string, a template, markup) on the line before ends a
	// statement there as an operand does.
	return tokenIs(tokens, k, "{") || tokenIs(tokens, k, "}") || tokenIs(tokens, k, ";") || tokens[k].endLine < tokens[k+1].line && tokens[k+1].depth == tokens[key].depth &&
		(tokens[k].stringLiteral || !strings.Contains("=(,:?[|&!+-*/%<>", tokens[k].text))
}

// scriptDeclaration is a module-level function declaration: its first and
// last lines, and the bounded window a long one keeps (to its first return);
// open reports an end the reader is not sure of (an arrow function's
// expression a later line may go on with): its window is partial.
type scriptDeclaration struct {
	start, end        int
	boundStart, bound int
	open              bool
}

// jsFunctionDeclaration reads the function a module-level binding at token
// at declares: function name(...) {...} (export, default, async, generator,
// generics and return annotations read), or const, let or var name =
// function, arrow or async arrow; ok false for anything else (a class, a
// value, a call result) or a bodiless signature.
func jsFunctionDeclaration(tokens []callToken, at int, typeScript bool) (scriptDeclaration, bool) {
	first := at - 1
	var body, last int
	open := false
	switch {
	case tokenIs(tokens, first, "function") || tokenIs(tokens, first, "*") && tokenIs(tokens, first-1, "function"):
		if tokenIs(tokens, first, "*") {
			first--
		}
		open := skipGenerics(tokens, at+1)
		if !tokenIs(tokens, open, "(") {
			return scriptDeclaration{}, false
		}
		body = functionBody(tokens, tokens[open].close)
		if body < 0 {
			return scriptDeclaration{}, false
		}
		last = tokens[body].close
	case tokenIs(tokens, first, "const") || tokenIs(tokens, first, "let") || tokenIs(tokens, first, "var"):
		k := at + 1
		if tokenIs(tokens, k, ":") {
			k = skipTypeTo(tokens, k+1, "=")
		}
		if !tokenIs(tokens, k, "=") || tokenIs(tokens, k+1, "=") || tokenIs(tokens, k+1, ">") {
			return scriptDeclaration{}, false
		}
		k++
		if tokenIs(tokens, k, "async") {
			k++
		}
		switch {
		case tokenIs(tokens, k, "function"):
			k++
			if tokenIs(tokens, k, "*") {
				k++
			}
			if tokenName(tokens, k) {
				k++
			}
			k = skipGenerics(tokens, k)
			if !tokenIs(tokens, k, "(") {
				return scriptDeclaration{}, false
			}
			body = functionBody(tokens, tokens[k].close)
			if body < 0 {
				return scriptDeclaration{}, false
			}
			last = tokens[body].close
		default:
			k = skipGenerics(tokens, k)
			var arrow int
			switch {
			case tokenName(tokens, k) && tokenIs(tokens, k+1, "=") && tokenIs(tokens, k+2, ">"):
				arrow = k + 2
			case tokenIs(tokens, k, "("):
				close := tokens[k].close
				if tokenIs(tokens, close+1, "=") && tokenIs(tokens, close+2, ">") {
					arrow = close + 2
				} else if r, ok := returnType(tokens, close); ok {
					arrow = r + 1
				} else {
					return scriptDeclaration{}, false
				}
			default:
				return scriptDeclaration{}, false
			}
			body = arrow + 1
			last, open = arrowEnd(tokens, arrow, false, typeScript)
		}
	default:
		return scriptDeclaration{}, false
	}
	for tokenIs(tokens, first-1, "async") || tokenIs(tokens, first-1, "default") || tokenIs(tokens, first-1, "export") {
		first--
	}
	d := scriptDeclaration{start: tokens[first].line, end: tokens[last].endLine, open: open}
	d.boundStart, d.bound = d.start, scriptFirstReturn(tokens, body, d.start, false)
	return d, true
}

// skipGenerics skips a TypeScript type parameter list "<...>" at k.
func skipGenerics(tokens []callToken, k int) int {
	if !tokenIs(tokens, k, "<") {
		return k
	}
	depth := 0
	for ; k < len(tokens); k++ {
		switch {
		case tokenIs(tokens, k, "<"):
			depth++
		case tokenIs(tokens, k, ">") && !tokenIs(tokens, k-1, "="):
			depth--
			if depth == 0 {
				return k + 1
			}
		case tokenIs(tokens, k, ";") || tokenIs(tokens, k, "{") && depth == 0:
			return -1
		}
		if tokens[k].close > k && !tokenIs(tokens, k, "<") {
			k = tokens[k].close
		}
	}
	return -1
}

// skipTypeTo skips a TypeScript type from k to the token end at its level.
func skipTypeTo(tokens []callToken, k int, end string) int {
	angle := 0
	for ; k < len(tokens); k++ {
		switch {
		case tokenIs(tokens, k, end) && angle == 0 && !(end == "=" && tokenIs(tokens, k+1, ">")):
			return k
		case tokenIs(tokens, k, "<"):
			angle++
		case tokenIs(tokens, k, ">") && !tokenIs(tokens, k-1, "="):
			angle--
		case tokenIs(tokens, k, ";"):
			return -1
		}
		if tokens[k].close > k {
			k = tokens[k].close
		}
	}
	return -1
}

// pythonFunctionDeclaration reads the def at module level whose name is
// token at: from its first decorator to the end of its body.
func pythonFunctionDeclaration(tokens []callToken, blocks []pyBlock, at int) (scriptDeclaration, bool) {
	for _, b := range blocks {
		if b.kind == "def" && b.name == at {
			d := scriptDeclaration{start: tokens[b.first].line, end: tokens[b.last].endLine}
			d.boundStart, d.bound = tokens[b.name-1].line, scriptFirstReturn(tokens, b.colon, tokens[b.name-1].line, true)
			if tokenIs(tokens, b.name-2, "async") {
				d.boundStart = tokens[b.name-2].line
			}
			return d, true
		}
	}
	return scriptDeclaration{}, false
}

// scriptFirstReturn is the last line of the first return, raise, throw or
// assert of a function body opening at token body (its "{" or Python ":"),
// before the body's end (its "}", or the next statement at the Python def's
// column or left of it), within witnessMaxLines of start and before any
// nested function; 0 when none (directDefinition's window).
func scriptFirstReturn(tokens []callToken, body, start int, python bool) int {
	if body < 0 || body >= len(tokens) || !python && !tokenIs(tokens, body, "{") {
		return 0
	}
	end := len(tokens)
	if !python {
		end = tokens[body].close
	} else {
		column := tokens[logicalStart(tokens, body)].column
		for k := body + 1; k < len(tokens); k++ {
			if statementStart(tokens, k) && tokens[k].column <= column {
				end = k
				break
			}
		}
	}
	for i := body + 1; i < end; i++ {
		t := tokens[i]
		if t.line >= start+witnessMaxLines {
			break
		}
		if tokenIs(tokens, i, "def") || tokenIs(tokens, i, "class") || tokenIs(tokens, i, "function") || tokenIs(tokens, i, "=") && tokenIs(tokens, i+1, ">") {
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
				return last
			}
		}
	}
	return 0
}

// scriptCall reports a call token of a lead's name: followed by "(" (in
// TypeScript also by type arguments and "("), not a member (after "." or
// "#"), not a declaration, a construction or a method head.
func scriptCall(tokens []callToken, i int, python, typeScript bool) bool {
	open := i + 1
	if typeScript && tokenIs(tokens, open, "<") {
		open = typeArguments(tokens, open)
	}
	if !tokenIs(tokens, open, "(") || tokenIs(tokens, i-1, "#") || tokenIs(tokens, i-1, ".") && !(tokenIs(tokens, i-2, ".") && tokenIs(tokens, i-3, ".")) {
		return false
	}
	for _, k := range []string{"def", "class", "function", "new"} {
		if tokenIs(tokens, i-1, k) {
			return false
		}
	}
	return python || !methodName(tokens, i)
}

// maxTypeArguments bounds the tokens typeArguments reads, a bracket group
// counting as one: callers read a "<" at every occurrence of a name or in
// every arrow function's expression.
const maxTypeArguments = 200

// typeArgumentsUnread is what typeArguments gives for a list that goes on
// past maxTypeArguments.
const typeArgumentsUnread = -2

// typeArguments is the token after a TypeScript type argument list opening
// with "<" at k, -1 when the tokens there read as no type arguments (a
// comparison, a shift, a logical operator), typeArgumentsUnread when the
// list goes on past maxTypeArguments.
func typeArguments(tokens []callToken, k int) int {
	depth := 0
	for steps := 0; k < len(tokens); k++ {
		if steps++; steps > maxTypeArguments {
			return typeArgumentsUnread
		}
		t := tokens[k]
		switch {
		case tokenIs(tokens, k, "<"):
			depth++
		case tokenIs(tokens, k, ">"):
			depth--
			if depth == 0 {
				return k + 1
			}
		case tokenIs(tokens, k, "=") && tokenIs(tokens, k+1, ">"):
			k++
		case tokenIs(tokens, k, "(") || tokenIs(tokens, k, "[") || tokenIs(tokens, k, "{"):
			k = t.close
		case tokenIs(tokens, k, "&") && tokenIs(tokens, k+1, "&"), tokenIs(tokens, k, "|") && tokenIs(tokens, k+1, "|"):
			return -1
		case t.name, t.quoted, t.stringLiteral, len(t.text) == 1 && strings.Contains(".,|&?:-", t.text), len(t.text) == 1 && t.text[0] >= '0' && t.text[0] <= '9':
		default:
			return -1
		}
	}
	return -1
}

// scriptDefinitions resolves a definition lead anchored in a Python,
// JavaScript or TypeScript file: the declarations the calls of the lead's
// name on the anchor lines bind to (a call of a local name an import binds
// to it counts). called reports such a call; local that every such call
// binds in the anchor's file to no project function (a parameter, a local,
// a value), so no other file can be why nothing was found.
func (r *leadResolver) scriptDefinitions(scan *leadScan, anchor record.Excerpt, symbol string) (hits []leadHit, called, local bool, reason string, err error) {
	lang := leadDefinitionLanguage(anchor.Path)
	python := lang == "python"
	typeScript := typeScriptPath(anchor.Path)
	text, ok := r.anchorText(scan, anchor)
	if !ok {
		return nil, false, false, "", nil
	}
	tokens := leadTokens(anchor.Path, text)
	if tokens == nil || python && pythonDynamicBindings(tokens) {
		return nil, false, false, "", nil
	}
	var imports []scriptBinding
	if python {
		imports = pythonImports(tokens)
	} else {
		imports = javascriptImports(tokens)
	}
	names := []string{symbol}
	for _, b := range imports {
		if !b.typeOnly && b.imported == symbol && b.local != symbol && !slices.Contains(names, b.local) {
			names = append(names, b.local)
		}
	}
	add := func(h leadHit) {
		if !slices.ContainsFunc(hits, func(o leadHit) bool { return o.declaration() == h.declaration() }) {
			hits = append(hits, h)
		}
	}
	ambiguous := false
	calls, locals := 0, 0
	scopes := map[string]scriptScopes{}
	// followed holds what each import binding (by its index) led to: the
	// calls of one lead follow it once.
	type follow struct {
		h     leadHit
		found bool
	}
	followed := map[int]follow{}
	for i, t := range tokens {
		if !t.name || !slices.Contains(names, t.text) || t.line < anchor.Start || anchor.End < t.line || !scriptCall(tokens, i, python, typeScript) {
			continue
		}
		calls++
		s, seen := scopes[t.text]
		if !seen {
			s = scriptScopesOf(tokens, t.text, python, typeScript, imports)
			scopes[t.text] = s
		}
		if s.shadowed(i) || python && pythonClassScope(s, i) {
			locals++
			continue
		}
		bindings, assigned := s.moduleBindings()
		bindings = slices.DeleteFunc(bindings, func(o scriptOccurrence) bool { return typeScript && o.decl == "function" && bodiless(tokens, o.at) })
		switch {
		case len(bindings) == 0:
			continue
		case assigned || len(bindings) > 1:
			ambiguous = true
			continue
		}
		b := bindings[0]
		if b.decl == "import" {
			at := slices.IndexFunc(imports, func(x scriptBinding) bool { return x.token == b.at })
			if at < 0 {
				continue
			}
			f, seen := followed[at]
			if !seen {
				h, found, err := r.scriptImported(scan, anchor.Path, imports[at], python)
				if err != nil {
					return nil, true, false, "", err
				}
				f = follow{h, found}
				followed[at] = f
			}
			if f.found {
				add(f.h)
			}
			continue
		}
		var d scriptDeclaration
		found := false
		switch {
		case python && b.decl == "def":
			d, found = pythonFunctionDeclaration(tokens, s.blocks, b.at)
		case !python:
			d, found = jsFunctionDeclaration(tokens, b.at, typeScript)
		}
		if !found {
			locals++
			continue
		}
		add(declarationHit(anchor.Path, d))
	}
	// Calls through this, self or a class's name.
	methods, through, why, err := r.scriptMethods(scan, anchor, tokens, symbol, python, typeScript, imports)
	if err != nil {
		return nil, true, false, "", err
	}
	for _, h := range methods {
		add(h)
	}
	switch {
	case ambiguous || why == record.LeadAmbiguous || len(hits) > 1:
		return nil, calls > 0 || through, false, record.LeadAmbiguous, nil
	case len(hits) == 0 && why != "":
		return nil, true, false, why, nil
	}
	return hits, calls > 0 || through, calls > 0 && locals == calls, "", nil
}

// pythonClassScope reports a call directly in a class body whose class
// binds the name: a class attribute reaches the calls of its body.
func pythonClassScope(s scriptScopes, x int) bool {
	inner := innermostBlock(s.blocks, x, false)
	if inner < 0 || s.blocks[inner].kind != "class" {
		return false
	}
	for _, o := range s.occurrences {
		if o.kind == occBinding && o.decl == "class-attribute" && innermostBlock(s.blocks, o.at, false) == inner {
			return true
		}
	}
	return false
}

// bodiless reports a TypeScript function signature without a body (an
// overload or ambient declaration): it binds nothing at run time.
func bodiless(tokens []callToken, at int) bool {
	open := skipGenerics(tokens, at+1)
	return tokenIs(tokens, at-1, "function") && tokenIs(tokens, open, "(") && functionBody(tokens, tokens[open].close) < 0
}

// declarationHit is the hit of a declaration found in p: its whole extent,
// and as its bound location the window to its first return when it has one
// (else the extent, whose head a window keeps).
func declarationHit(p string, d scriptDeclaration) leadHit {
	extent := sourceCallLocation{p, d.start, d.end}
	loc := extent
	if d.bound >= d.boundStart && d.bound > 0 {
		loc = sourceCallLocation{p, d.start, d.bound}
	}
	return leadHit{loc: loc, origin: "context", extent: extent, partial: d.open}
}

// leadPythonModule is the one Python file a module name of caller names
// (pythonModuleFiles), "" when none or more than one does or when it is no
// code or test file.
func leadPythonModule(files map[string]source.Entry, caller, module string) string {
	found := pythonModuleFiles(files, caller, module)
	if len(found) != 1 {
		return ""
	}
	if k := windowKind(found[0]); k != "code" && k != "test" {
		return ""
	}
	return found[0]
}

// scriptImported follows an import binding of caller into the declaration
// it names: the one module its specifier names (leadPythonModule,
// leadScriptModule), its module-level function exported under the imported
// name, else the binding that module passes the name on with (an import it
// exports, a re-export), through at most four modules.
func (r *leadResolver) scriptImported(scan *leadScan, caller string, b scriptBinding, python bool) (leadHit, bool, error) {
	seen := map[string]bool{caller: true}
	module, name := b.module, b.imported
	for range 4 {
		if name == "" || name == "*" {
			return leadHit{}, false, nil
		}
		var p string
		if python {
			p = leadPythonModule(r.catalog.files, caller, module)
		} else {
			p = leadScriptModule(r.catalog.files, caller, module)
		}
		if p == "" || seen[p] {
			return leadHit{}, false, nil
		}
		seen[p] = true
		text, ok, err := scan.index.text(p)
		if err != nil || !ok {
			return leadHit{}, false, err
		}
		tokens := leadTokens(p, text)
		if tokens == nil || python && pythonDynamicBindings(tokens) {
			return leadHit{}, false, nil
		}
		e := scriptExported(tokens, name, python, typeScriptPath(p))
		switch {
		case e.refused:
			return leadHit{}, false, nil
		case e.found:
			return declarationHit(p, e.decl), true, nil
		case e.passes:
			caller, module, name = p, e.follow.module, e.follow.imported
			continue
		}
		binding, ok := scriptReexport(tokens, name, python)
		if !ok {
			return leadHit{}, false, nil
		}
		caller, module, name = p, binding.module, binding.symbol
	}
	return leadHit{}, false, nil
}

// scriptExport is what a module exports under a name: decl, the
// module-level function declaring it (found), or follow, the one import
// binding the module passes it on with (passes). refused reports a module
// that binds the name more than once or assigns to it: nothing is chosen.
type scriptExport struct {
	decl                   scriptDeclaration
	follow                 scriptBinding
	found, passes, refused bool
}

// importExport is the export of the import binding whose bound name is
// token at: passed on when it names one imported name, else refused.
func importExport(imports []scriptBinding, at int) scriptExport {
	k := slices.IndexFunc(imports, func(x scriptBinding) bool { return x.token == at })
	if k < 0 || imports[k].typeOnly || imports[k].imported == "" || imports[k].imported == "*" {
		return scriptExport{refused: true}
	}
	return scriptExport{follow: imports[k], passes: true}
}

// scriptExported reads what a module exports under name: for Python its one
// module-level def or import of the name, for JavaScript an exported
// declaration (export function, export const, export default function for
// "default", a declaration an export { local as name } clause names) or an
// import such a clause (or export default) passes on.
func scriptExported(tokens []callToken, name string, python, typeScript bool) scriptExport {
	if python {
		imports := pythonImports(tokens)
		s := scriptScopesOf(tokens, name, true, false, imports)
		bindings, _ := s.moduleBindings()
		switch {
		case len(bindings) == 0:
			return scriptExport{}
		case len(bindings) > 1:
			return scriptExport{refused: true}
		}
		switch b := bindings[0]; b.decl {
		case "def":
			d, found := pythonFunctionDeclaration(tokens, s.blocks, b.at)
			return scriptExport{decl: d, found: found, refused: !found}
		case "import":
			return importExport(imports, b.at)
		}
		return scriptExport{refused: true}
	}
	local, exported := name, false
	if name == "default" {
		for i := range tokens {
			if tokens[i].depth != 0 || !tokenIs(tokens, i, "export") || !tokenIs(tokens, i+1, "default") {
				continue
			}
			k := i + 2
			if tokenIs(tokens, k, "async") {
				k++
			}
			if tokenIs(tokens, k, "function") {
				at := k + 1
				if tokenIs(tokens, at, "*") {
					at++
				}
				if !tokenName(tokens, at) {
					// An anonymous default export: the function itself.
					open := skipGenerics(tokens, at)
					if !tokenIs(tokens, open, "(") {
						return scriptExport{}
					}
					body := functionBody(tokens, tokens[open].close)
					if body < 0 {
						return scriptExport{}
					}
					d := scriptDeclaration{start: tokens[i].line, end: tokens[tokens[body].close].endLine}
					d.boundStart, d.bound = d.start, scriptFirstReturn(tokens, body, d.start, false)
					return scriptExport{decl: d, found: true}
				}
				local, exported = tokens[at].text, true
			} else if tokenName(tokens, k) && (tokenIs(tokens, k+1, ";") || k+1 == len(tokens) || statementStart(tokens, k+1)) {
				local, exported = tokens[k].text, true
			}
			break
		}
		if !exported {
			return scriptExport{}
		}
	} else if l, ok := exportClause(tokens, name); ok {
		local, exported = l, true
	}
	imports := javascriptImports(tokens)
	s := scriptScopesOf(tokens, local, false, typeScript, imports)
	bindings, assigned := s.moduleBindings()
	bindings = slices.DeleteFunc(bindings, func(o scriptOccurrence) bool { return typeScript && o.decl == "function" && bodiless(tokens, o.at) })
	switch {
	case len(bindings) == 0:
		return scriptExport{}
	case assigned || len(bindings) > 1:
		return scriptExport{refused: true}
	}
	b := bindings[0]
	if b.decl == "import" {
		if !exported {
			return scriptExport{}
		}
		return importExport(imports, b.at)
	}
	if !exported && !exportedDeclaration(tokens, b.at) {
		return scriptExport{}
	}
	d, found := jsFunctionDeclaration(tokens, b.at, typeScript)
	return scriptExport{decl: d, found: found, refused: !found}
}

// exportedDeclaration reports a declaration whose statement opens with
// export.
func exportedDeclaration(tokens []callToken, at int) bool {
	k := at - 1
	for k >= 0 && (tokenIs(tokens, k, "function") || tokenIs(tokens, k, "*") || tokenIs(tokens, k, "async") || tokenIs(tokens, k, "const") ||
		tokenIs(tokens, k, "let") || tokenIs(tokens, k, "var") || tokenIs(tokens, k, "default")) {
		k--
	}
	return tokenIs(tokens, k, "export")
}

// exportClause is the local name an export { local as name } or export {
// name } clause (without from) exports as name.
func exportClause(tokens []callToken, name string) (string, bool) {
	for i := range tokens {
		if tokens[i].depth != 0 || !tokenIs(tokens, i, "export") || !tokenIs(tokens, i+1, "{") {
			continue
		}
		end := tokens[i+1].close
		if tokenIs(tokens, end+1, "from") {
			continue
		}
		for k := i + 2; k < end; k++ {
			if !tokenName(tokens, k) {
				continue
			}
			local, exported := tokens[k].text, tokens[k].text
			if tokenIs(tokens, k+1, "as") && tokenName(tokens, k+2) {
				exported = tokens[k+2].text
				k += 2
			}
			if exported == name {
				return local, true
			}
		}
	}
	return "", false
}
