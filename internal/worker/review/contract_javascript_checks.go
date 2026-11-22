package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Match a direct comparison in an ordinary test callback. In particular,
// passing an API in a callback to a generic assertion does not check its value.
func jsOptionChecks(entry source.Entry, lines []string, tokens []callToken) []jsOptionCheck {
	bindings := importedBindings(tokens, false)
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "import") || !tokenName(tokens, i+1) || tokenIs(tokens, i+1, "type") {
			continue
		}
		from := i + 2
		if tokenIs(tokens, from, ",") && tokenIs(tokens, from+1, "{") {
			from = tokens[from+1].close + 1
		}
		if from+1 < len(tokens) && tokenIs(tokens, from, "from") && tokens[from+1].quoted {
			module := tokens[from+1].text
			if strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../") {
				local := tokens[i+1].text
				bindings[local] = append(bindings[local], callBinding{module, "default", i + 1})
			}
		}
	}
	for i, t := range tokens {
		if b := bindings[t.text]; t.name && len(b) == 1 && i != b[0].token && !isDirectCall(tokens, i) {
			delete(bindings, t.text)
		}
	}
	if len(bindings) == 0 {
		return nil
	}
	test := jsUnshadowedTestName(tokens, "test")
	it := jsUnshadowedTestName(tokens, "it")
	expect := jsUnshadowedTestName(tokens, "expect")
	assert := jsUnshadowedTestName(tokens, "assert")
	describe := jsUnshadowedTestName(tokens, "describe")
	containers := map[int]bool{-1: true}
	var out []jsOptionCheck
	for i, t := range tokens {
		if !t.name || !containers[t.parent] || !isDirectCall(tokens, i) {
			continue
		}
		if (t.text != "test" || !test) && (t.text != "it" || !it) && (t.text != "describe" || !describe) {
			continue
		}
		body := jsTestBody(tokens, i+1)
		if body < 0 {
			continue
		}
		if t.text == "describe" {
			containers[body] = true
			continue
		}
		for j := body + 1; j < tokens[body].close && len(out) < callMaxSites; j++ {
			if tokens[j].parent != body {
				continue
			}
			var check jsOptionCheck
			switch {
			case expect && tokenIs(tokens, j, "expect") && isDirectCall(tokens, j):
				check = jsExpectCheck(tokens, j, bindings)
			case assert && tokenIs(tokens, j, "assert") && tokenIs(tokens, j+1, ".") &&
				(tokenIs(tokens, j+2, "equal") || tokenIs(tokens, j+2, "strictEqual") || tokenIs(tokens, j+2, "deepEqual") || tokenIs(tokens, j+2, "deepStrictEqual")) &&
				tokenIs(tokens, j+3, "("):
				open := j + 3
				args := jsArguments(tokens, open)
				if len(args) == 2 {
					check = jsAssertedCall(tokens, open+1, open+1+len(args[0]), bindings)
					check.result = jsScalar(args[1])
					check.witness.end = tokens[tokens[open].close].line
				}
			}
			if check.symbol == "" || check.result == "" || !fitsWitness(lines, t.line, check.witness.end) {
				continue
			}
			check.witness.entry, check.witness.line = entry, t.line
			out = append(out, check)
		}
	}
	return out
}

func jsUnshadowedTestName(tokens []callToken, name string) bool {
	for i, t := range tokens {
		if !t.name || t.text != name || isDirectCall(tokens, i) ||
			!tokenIs(tokens, i-1, ".") && tokenIs(tokens, i+1, ".") && tokenIs(tokens, i+3, "(") {
			continue
		}
		// Accept imported runner/assertion bindings from the platforms
		// whose direct test and assertion forms are recognized here.
		from := i + 1
		if tokenIs(tokens, t.parent, "{") && tokenIs(tokens, t.parent-1, "import") {
			from = tokens[t.parent].close + 1
		} else if !tokenIs(tokens, i-1, "import") {
			return false
		}
		if !tokenIs(tokens, from, "from") || from+1 >= len(tokens) || !tokens[from+1].quoted {
			return false
		}
		switch tokens[from+1].text {
		case "node:test", "node:assert", "node:assert/strict", "vitest", "@jest/globals", "@playwright/test", "bun:test":
		default:
			return false
		}
	}
	return true
}

func jsTestBody(tokens []callToken, open int) int {
	if open+4 >= len(tokens) || !tokens[open+1].quoted || !tokenIs(tokens, open+2, ",") {
		return -1
	}
	at := open + 3
	if tokenIs(tokens, at, "async") {
		at++
	}
	function := tokenIs(tokens, at, "function")
	if function {
		at++
	}
	if !tokenIs(tokens, at, "(") || !tokenIs(tokens, at+1, ")") {
		return -1
	}
	at += 2
	if !function {
		if !tokenIs(tokens, at, "=") || !tokenIs(tokens, at+1, ">") {
			return -1
		}
		at += 2
	}
	if !tokenIs(tokens, at, "{") || tokens[at].close > tokens[open].close {
		return -1
	}
	return at
}

func jsExpectCheck(tokens []callToken, at int, bindings map[string][]callBinding) jsOptionCheck {
	open := at + 1
	close := tokens[open].close
	check := jsAssertedCall(tokens, open+1, close, bindings)
	if check.symbol == "" {
		return jsOptionCheck{}
	}
	next := close + 1
	kind := "value"
	if tokenIs(tokens, next, ".") && (tokenIs(tokens, next+1, "resolves") || tokenIs(tokens, next+1, "rejects")) {
		if !tokenIs(tokens, at-1, "await") && !tokenIs(tokens, at-1, "return") || check.awaited {
			return jsOptionCheck{}
		}
		kind = tokens[next+1].text
		check.awaited = true
		next += 2
	}
	if !tokenIs(tokens, next, ".") || !tokenName(tokens, next+1) || !tokenIs(tokens, next+2, "(") {
		return jsOptionCheck{}
	}
	switch tokens[next+1].text {
	case "toBe", "toEqual", "toStrictEqual":
	case "toThrow":
		if kind != "rejects" {
			return jsOptionCheck{}
		}
	default:
		return jsOptionCheck{}
	}
	if args := jsArguments(tokens, next+2); len(args) == 1 {
		if scalar := jsScalar(args[0]); scalar != "" {
			check.result = scalar
			if kind == "rejects" {
				check.result = "rejected:" + scalar
			}
			check.witness.end = tokens[tokens[next+2].close].line
		}
	}
	return check
}

func jsAssertedCall(tokens []callToken, at, end int, bindings map[string][]callBinding) jsOptionCheck {
	awaited := tokenIs(tokens, at, "await")
	if awaited {
		at++
	}
	if at >= end || !tokenName(tokens, at) || !isDirectCall(tokens, at) {
		return jsOptionCheck{}
	}
	b := bindings[tokens[at].text]
	last := tokens[at+1].close + 1
	if len(b) != 1 || last > end {
		return jsOptionCheck{}
	}
	projection := ""
	if !awaited && last+2 == end && tokenIs(tokens, last, ".") && tokenName(tokens, last+1) {
		projection = tokens[last+1].text
	} else if last != end {
		return jsOptionCheck{}
	}
	if last-at > 256 {
		return jsOptionCheck{}
	}
	args := jsArguments(tokens, at+1)
	for i, a := range args {
		args[i] = slices.Clone(a)
	}
	return jsOptionCheck{module: b[0].module, symbol: b[0].symbol, awaited: awaited, projection: projection, arguments: args}
}
