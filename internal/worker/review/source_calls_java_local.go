package review

import (
	"path"
	"slices"
	"strings"
)

type localJavaMethod struct {
	name                 string
	start, body, end     int
	parameters           []string
	parameterTypes       []string
	exact, static, known bool
}

// javaLocalCalls binds direct calls within a top-level class. An instance
// helper has to be private, final, or in a final class. In an inherited class
// we also require an exact argument type match, since a parent overload may
// otherwise be a better match than the same-name declaration in this file.
func javaLocalCalls(filename string, lines []string, tokens []callToken) []sourceCall {
	name := strings.TrimSuffix(path.Base(filename), ".java")
	owner, inherited, final := javaLocalClass(tokens, name)
	if owner < 0 {
		return nil
	}
	methods := javaLocalMethods(tokens, owner, name, final)
	byName := map[string][]localJavaMethod{}
	for _, m := range methods {
		byName[m.name] = append(byName[m.name], m)
	}
	classNameUnshadowed := javaLocalQualifier(tokens, name)
	var out []sourceCall
	for _, caller := range methods {
		if caller.body < 0 || !caller.known {
			continue
		}
		for i := caller.body + 1; i < caller.end; i++ {
			if !tokenName(tokens, i) || !tokenIs(tokens, i+1, "(") || !javaLocalCallFrame(tokens, i, caller.body) {
				continue
			}
			choices := byName[tokens[i].text]
			if len(choices) == 0 || tokenIs(tokens, i-1, "new") || tokenIs(tokens, i-1, "@") {
				continue
			}
			from, classQualified := i, false
			if tokenIs(tokens, i-1, ".") {
				classQualified = classNameUnshadowed && tokenIs(tokens, i-2, name)
				if i < 2 || tokenIs(tokens, i-3, ".") || !classQualified && !tokenIs(tokens, i-2, "this") {
					continue
				}
				from = i - 2
			}
			// A class qualifier cannot reach an instance method. The enclosing
			// method's parameter types supply the static types of bare arguments.
			args := javaLocalArgumentTypes(tokens, i+1, caller)
			target := javaLocalTarget(choices, args, inherited, classQualified || caller.static)
			if target.body <= 0 || target.body == caller.body {
				continue
			}
			call := sourceCallLocation{filename, tokens[from].line, tokens[tokens[i+1].close].line}
			first, last := tokens[target.start].line, tokens[target.end].line
			definition := sourceCallLocation{filename, first, last}
			if !fitsWitness(lines, call.start, call.end) {
				continue
			}
			for _, window := range javaLocalWindows(lines, tokens, target) {
				window.path = filename
				out = append(out, sourceCall{caller: call, callee: window, symbol: target.name, calleeExtent: definition})
				if len(out) >= callMaxSites {
					return nil
				}
			}
		}
	}
	// The public hint identifies a physical span and a symbol. If two
	// overloads have that same span, it cannot identify which call we mean.
	type site struct {
		location sourceCallLocation
		symbol   string
	}
	definitions := map[site]sourceCallLocation{}
	ambiguous := map[site]bool{}
	for _, call := range out {
		key := site{call.caller, call.symbol}
		if old, found := definitions[key]; found && old != call.calleeExtent {
			ambiguous[key] = true
		}
		definitions[key] = call.calleeExtent
	}
	return slices.DeleteFunc(out, func(call sourceCall) bool { return ambiguous[site{call.caller, call.symbol}] })
}

func javaLocalClass(tokens []callToken, name string) (owner int, inherited, final bool) {
	owner = -1
	for i, token := range tokens {
		if token.depth != 0 || !tokenIs(tokens, i, "class") || !tokenIs(tokens, i+1, name) {
			continue
		}
		for j := i - 1; tokenName(tokens, j); j-- {
			final = final || tokenIs(tokens, j, "final")
		}
		for j := i + 2; j < len(tokens) && tokens[j].parent < 0; j++ {
			if tokenIs(tokens, j, "{") {
				return j, inherited, final
			}
			if !tokenName(tokens, j) && !slices.Contains([]string{".", ",", "<", ">", "?", "&"}, tokens[j].text) {
				break
			}
			inherited = inherited || tokenIs(tokens, j, "extends") || tokenIs(tokens, j, "implements")
		}
	}
	return owner, false, false
}

func javaLocalMethods(tokens []callToken, owner int, className string, finalClass bool) []localJavaMethod {
	var out []localJavaMethod
	for i := owner + 1; i < tokens[owner].close; i++ {
		if tokens[i].parent != owner || !tokenName(tokens, i) || tokens[i].text == className || !tokenIs(tokens, i+1, "(") ||
			!(tokenName(tokens, i-1) || tokenIs(tokens, i-1, "]") || tokenIs(tokens, i-1, ">")) {
			continue
		}
		first := i - 1
		static, exact := false, finalClass
		// Everything since the previous class member belongs to this
		// declaration. A top-level assignment is a field initializer.
		for first > owner+1 && !tokenIs(tokens, first-1, ";") && !tokenIs(tokens, first-1, "}") {
			first--
		}
		isDeclaration, generic := true, false
		for j := first; j < i; j++ {
			if tokens[j].parent == owner {
				if tokenIs(tokens, j, "=") || tokenIs(tokens, j, "new") {
					isDeclaration = false
					break
				}
				static = static || tokenIs(tokens, j, "static")
				exact = exact || tokenIs(tokens, j, "private") || tokenIs(tokens, j, "final")
				if tokenIs(tokens, j, "<") {
					generic = generic || j == first || slices.Contains([]string{"public", "protected", "private", "static", "final", "synchronized"}, tokens[j-1].text)
				}
			}
		}
		if !isDeclaration {
			continue
		}
		m := localJavaMethod{name: tokens[i].text, start: first, body: -1, end: -1, exact: exact || static, static: static}
		m.parameters, m.parameterTypes, m.known = javaLocalParameters(tokens, i+1)
		m.known = m.known && !generic
		body := tokens[i+1].close + 1
		if tokenIs(tokens, body, "throws") {
			body++
			for tokenName(tokens, body) || tokenIs(tokens, body, ".") || tokenIs(tokens, body, ",") {
				body++
			}
		}
		if tokenIs(tokens, body, "{") {
			m.body, m.end = body, tokens[body].close
		} else if !tokenIs(tokens, body, ";") {
			continue
		}
		out = append(out, m)
	}
	return out
}

func javaLocalParameters(tokens []callToken, open int) (names, types []string, known bool) {
	for _, part := range javaArguments(tokens, open) {
		if tokenIs(part, 0, "final") {
			part = part[1:]
		}
		last := len(part) - 1
		if last < 1 || !tokenName(part, last) || !tokenName(part, 0) {
			return nil, nil, false
		}
		var typ strings.Builder
		typ.WriteString(part[0].text)
		i := 1
		for i+1 < last && tokenIs(part, i, ".") && tokenName(part, i+1) {
			typ.WriteByte('.')
			typ.WriteString(part[i+1].text)
			i += 2
		}
		for i+1 < last && tokenIs(part, i, "[") && tokenIs(part, i+1, "]") {
			typ.WriteString("[]")
			i += 2
		}
		if i != last {
			return nil, nil, false // generic, varargs, annotation or alternate array syntax
		}
		names, types = append(names, part[last].text), append(types, typ.String())
	}
	return names, types, true
}

func javaLocalArgumentTypes(tokens []callToken, open int, caller localJavaMethod) []string {
	types := javaArgumentTypes(tokens, open)
	for i, part := range javaArguments(tokens, open) {
		if types[i] == "" && len(part) == 1 && part[0].name {
			if slot := slices.Index(caller.parameters, part[0].text); slot >= 0 {
				types[i] = caller.parameterTypes[slot]
			}
		}
	}
	return types
}

func javaLocalTarget(choices []localJavaMethod, args []string, inherited, static bool) localJavaMethod {
	var sameArity []localJavaMethod
	for _, m := range choices {
		if !m.known {
			return localJavaMethod{}
		}
		if len(m.parameterTypes) == len(args) && (!static || m.static) {
			sameArity = append(sameArity, m)
		}
	}
	if inherited || len(sameArity) > 1 {
		if slices.Contains(args, "") {
			return localJavaMethod{}
		}
		sameArity = slices.DeleteFunc(sameArity, func(m localJavaMethod) bool { return !slices.Equal(m.parameterTypes, args) })
	}
	if len(sameArity) != 1 || !sameArity[0].exact {
		return localJavaMethod{}
	}
	for i, actual := range args {
		if actual != "" && actual != sameArity[0].parameterTypes[i] {
			return localJavaMethod{}
		}
	}
	return sameArity[0]
}

func javaLocalQualifier(tokens []callToken, name string) bool {
	for i, t := range tokens {
		if !t.name || t.text != name {
			continue
		}
		if tokenIs(tokens, i-1, "class") || tokenIs(tokens, i-1, "new") || tokenIs(tokens, i+1, ".") {
			continue
		}
		return false
	}
	return true
}

func javaLocalCallFrame(tokens []callToken, i, body int) bool {
	for p := tokens[i].parent; p > body; p = tokens[p].parent {
		if !tokenIs(tokens, p, "{") {
			continue
		}
		// A nested class, lambda, or anonymous class has its own receiver.
		if tokenIs(tokens, p-1, "else") || tokenIs(tokens, p-1, "try") || tokenIs(tokens, p-1, "finally") {
			continue
		}
		if !tokenIs(tokens, p-1, ")") {
			return false
		}
		control := tokens[p-1].parent - 1
		if control < body || !slices.Contains([]string{"if", "for", "while", "catch", "switch", "synchronized"}, tokens[control].text) {
			return false
		}
	}
	return true
}

func javaLocalWindows(lines []string, tokens []callToken, m localJavaMethod) []sourceCallLocation {
	first, last := tokens[m.start].line, tokens[m.end].line
	if fitsWitness(lines, first, last) {
		return []sourceCallLocation{{start: first, end: last}}
	}
	// Retain executable entry and exit windows from a body that exceeds
	// the witness budget. Neither segment claims to cover the whole method.
	first = tokens[m.body+1].line
	if first >= last {
		return nil
	}
	half := witnessMaxLines / 2
	head := min(first+half-1, last-1)
	for head > first && !fitsWitness(lines, first, head) {
		head--
	}
	foot := max(head+1, last-half+1)
	for foot < last && !fitsWitness(lines, foot, last) {
		foot++
	}
	var windows []sourceCallLocation
	if head > first {
		windows = append(windows, sourceCallLocation{start: first, end: head})
	}
	if foot < last {
		windows = append(windows, sourceCallLocation{start: foot, end: last})
	}
	return windows
}
