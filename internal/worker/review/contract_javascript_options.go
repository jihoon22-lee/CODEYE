package review

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var jsOptionPhrase = regexp.MustCompile("(?i)(?:options?|settings?|parameters?|옵션|매개변수)[ \t]+`([A-Za-z_$][A-Za-z0-9_$]*)`|`([A-Za-z_$][A-Za-z0-9_$]*)`[ \t]*(?:options?|settings?|parameters?|옵션|매개변수)")

func jsDocumentedOptions(statement string, cited []string) []string {
	var names []string
	for _, match := range jsOptionPhrase.FindAllStringSubmatch(statement, 12) {
		name := match[1] + match[2]
		if slices.Contains(cited, name) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

type jsOption struct {
	argument  int
	property  string
	variable  string
	value     string
	omittable bool
}

type jsOptionImplementation struct {
	name, export string
	async        bool
	option       jsOption
	witness      contractWitness
	branch       contractWitness
}

type jsOptionCheck struct {
	module, symbol              string
	arguments                   [][]callToken
	result, projection, context string
	awaited                     bool
	witness                     contractWitness
}

type jsOptionTrace struct {
	index                  int
	symbol                 string
	implementation, branch contractWitness
	defaultCheck, override contractWitness
}

func jsOptionTraces(queries []witnessQuery, contents map[string]string, entries map[string]source.Entry, maintenance map[string]bool) []jsOptionTrace {
	if !slices.ContainsFunc(queries, func(q witnessQuery) bool { return len(q.navigationNames) > 0 }) {
		return nil
	}
	var implementations []jsOptionImplementation
	var checks []jsOptionCheck
	for _, p := range slices.Sorted(maps.Keys(contents)) {
		if callLanguage(p) != "javascript" || maintenance[p] || benchmarkPath(p) || typeDeclarationPath(p) ||
			!slices.ContainsFunc(queries, func(q witnessQuery) bool { return len(q.navigationNames) > 0 && record.ScopeUnder(p, q.scope) }) {
			continue
		}
		text := contents[p]
		tokens, lines := scanCallTokens(text, false, true), strings.Split(text, "\n")
		if windowKind(p) == "test" {
			checks = append(checks, jsOptionChecks(entries[p], lines, tokens)...)
		} else if windowKind(p) == "code" {
			implementations = append(implementations, jsOptionImplementations(entries[p], lines, tokens)...)
		}
	}
	var traces []jsOptionTrace
	for _, q := range queries {
		var definitions []jsOptionImplementation
		for _, impl := range implementations {
			if !record.ScopeUnder(impl.witness.entry.Path, q.scope) || len(q.jsAPINames) > 0 && !slices.Contains(q.jsAPINames, impl.name) {
				continue
			}
			if len(q.jsOptionNames) > 0 && !slices.Contains(q.jsOptionNames, impl.option.publicName()) {
				continue
			}
			if slices.Contains(q.navigationNames, impl.option.publicName()) ||
				slices.Contains(q.navigationNames, impl.name) {
				definitions = append(definitions, impl)
			}
		}
		// A stated option narrows a named API with several parameters.
		if slices.ContainsFunc(definitions, func(i jsOptionImplementation) bool {
			return slices.Contains(q.navigationNames, i.option.publicName())
		}) {
			definitions = slices.DeleteFunc(definitions, func(i jsOptionImplementation) bool {
				return !slices.Contains(q.navigationNames, i.option.publicName())
			})
		}
		if slices.ContainsFunc(definitions, func(i jsOptionImplementation) bool { return q.names(i.witness.entry.Path) }) {
			definitions = slices.DeleteFunc(definitions, func(i jsOptionImplementation) bool { return !q.names(i.witness.entry.Path) })
		}
		if len(definitions) != 1 {
			continue
		}
		impl := definitions[0]
		trace := jsOptionTrace{index: q.contractIndex, symbol: impl.name, implementation: impl.witness, branch: impl.branch}
		var defaults, overrides []jsOptionCheck
		for _, check := range checks {
			p := check.witness.entry.Path
			if check.symbol != impl.export || impl.async && !check.awaited || !record.ScopeUnder(p, q.scope) ||
				directModule(entries, p, check.module, false) != impl.witness.entry.Path {
				continue
			}
			mode, context := impl.option.compare(check.arguments)
			check.context = context
			switch mode {
			case "default":
				defaults = append(defaults, check)
			case "override":
				overrides = append(overrides, check)
			}
		}
		for _, d := range defaults {
			if i := slices.IndexFunc(overrides, func(c jsOptionCheck) bool {
				return c.result != d.result && c.projection == d.projection && c.context == d.context
			}); i >= 0 {
				trace.defaultCheck, trace.override = d.witness, overrides[i].witness
				break
			}
		}
		traces = append(traces, trace)
	}
	return traces
}

func jsOptionImplementations(entry source.Entry, lines []string, tokens []callToken) []jsOptionImplementation {
	var out []jsOptionImplementation
	for at, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, at, "export") {
			continue
		}
		i := at + 1
		defaultExport := tokenIs(tokens, i, "default")
		if defaultExport {
			i++
		}
		async := tokenIs(tokens, i, "async")
		if async {
			i++
		}
		arrow := tokenIs(tokens, i, "const") && !async && !defaultExport
		if !tokenIs(tokens, i, "function") && !arrow || !tokenName(tokens, i+1) {
			continue
		}
		declaration := i + 1
		name, params := tokens[declaration].text, declaration+1
		if arrow {
			if !tokenIs(tokens, params, "=") {
				continue
			}
			params++
			async = tokenIs(tokens, params, "async")
			if async {
				params++
			}
		}
		if !tokenIs(tokens, params, "(") {
			continue
		}
		export := name
		if defaultExport {
			export = "default"
		}
		body := tokens[params].close + 1
		if tokenIs(tokens, body, ":") && tokenName(tokens, body+1) {
			body += 2
			if tokenIs(tokens, body, "<") && tokenName(tokens, body+1) && tokenIs(tokens, body+2, ">") {
				body += 3
			}
		}
		if arrow {
			if !tokenIs(tokens, body, "=") || !tokenIs(tokens, body+1, ">") {
				continue
			}
			body += 2
		}
		if !tokenIs(tokens, body, "{") || !jsPublicFunction(tokens, declaration) {
			continue
		}
		for argument, arg := range jsArguments(tokens, params) {
			for _, option := range jsOptionDefaults(arg, argument) {
				from, end := jsOptionDecision(tokens, body, option.variable)
				if from == 0 {
					continue
				}
				w := contractWitness{entry: entry, line: t.line, end: end, direct: true, documented: true}
				impl := jsOptionImplementation{name: name, export: export, async: async, option: option, witness: w}
				if !fitsWitness(lines, w.line, w.end) {
					declEnd := tokens[body].line
					if from <= declEnd || !fitsWitness(lines, w.line, declEnd) || !fitsWitness(lines, from, end) {
						continue
					}
					impl.witness.end = declEnd
					impl.branch = contractWitness{entry: entry, line: from, end: end, direct: true, documented: true}
				}
				out = append(out, impl)
			}
		}
	}
	return out
}

func jsPublicFunction(tokens []callToken, declaration int) bool {
	name := tokens[declaration].text
	for i, t := range tokens {
		if i == declaration || !t.name || t.text != name || isDirectCall(tokens, i) {
			continue
		}
		return false
	}
	return true
}

func jsOptionDefaults(arg []callToken, argument int) []jsOption {
	if len(arg) < 3 {
		return nil
	}
	if arg[0].name {
		if i := jsParameterDefault(arg, 1); i >= 0 {
			if v := jsScalar(arg[i:]); v != "" {
				return []jsOption{{argument: argument, variable: arg[0].text, value: v, omittable: true}}
			}
		}
		return nil
	}
	if !tokenIs(arg, 0, "{") {
		return nil
	}
	end := 1
	for end < len(arg) && arg[end].depth > arg[0].depth {
		// The closing brace keeps the opening brace as its parent.
		if tokenIs(arg, end, "}") && arg[end].depth == arg[0].depth+1 {
			break
		}
		end++
	}
	if !tokenIs(arg, end, "}") {
		return nil
	}
	omit := false
	if end+1 < len(arg) {
		i := jsParameterDefault(arg, end+1)
		omit = i >= 0 && len(arg) == i+2 && tokenIs(arg, i, "{") && tokenIs(arg, i+1, "}")
		if i >= 0 && !omit {
			return nil
		}
	}
	var out []jsOption
	for _, field := range jsComponents(arg[1:end], arg[0].depth+1) {
		if len(field) < 3 || !field[0].name || !tokenIs(field, 1, "=") {
			continue
		}
		if v := jsScalar(field[2:]); v != "" {
			out = append(out, jsOption{argument: argument, property: field[0].text, variable: field[0].text, value: v, omittable: omit})
		}
	}
	return out
}

func jsParameterDefault(arg []callToken, i int) int {
	if tokenIs(arg, i, ":") && tokenName(arg, i+1) {
		i += 2
	}
	if !tokenIs(arg, i, "=") {
		return -1
	}
	return i + 1
}

func jsArguments(tokens []callToken, open int) [][]callToken {
	return jsComponents(tokens[open+1:tokens[open].close], tokens[open].depth+1)
}

func jsComponents(tokens []callToken, depth int) [][]callToken {
	var parts [][]callToken
	start := 0
	for i, t := range tokens {
		if t.depth == depth && tokenIs(tokens, i, ",") {
			parts = append(parts, tokens[start:i])
			start = i + 1
		}
	}
	if start < len(tokens) {
		parts = append(parts, tokens[start:])
	}
	return parts
}

func jsScalar(tokens []callToken) string {
	if len(tokens) == 0 || len(tokens) > 24 {
		return ""
	}
	if len(tokens) == 1 {
		if tokens[0].quoted {
			return "string:" + tokens[0].text
		}
		switch tokens[0].text {
		case "true", "false", "null":
			return tokens[0].text
		}
	}
	var b strings.Builder
	for i, t := range tokens {
		if t.name || t.stringLiteral || len(t.text) != 1 || !strings.Contains("0123456789.-+", t.text) ||
			(i > 0 && (t.line != tokens[i-1].line || t.column != tokens[i-1].column+1)) {
			return ""
		}
		b.WriteString(t.text)
	}
	if v, err := strconv.ParseFloat(b.String(), 64); err == nil {
		return "number:" + strconv.FormatFloat(v, 'g', -1, 64)
	}
	return ""
}

// Require a stable option in one direct function-level guard and the
// terminal statements of both paths. Nested callbacks cannot establish it.
func jsOptionDecision(tokens []callToken, body int, variable string) (int, int) {
	end := tokens[body].close
	guard, from := -1, 0
	for i := body + 1; i < end; i++ {
		t := tokens[i]
		if t.name && t.text == variable {
			if tokenIs(tokens, i-1, ".") || tokenIs(tokens, i+1, ".") {
				continue
			}
			j := i - 1
			if tokenIs(tokens, j, "!") {
				j--
			}
			if !tokenIs(tokens, j-1, "if") || !tokenIs(tokens, j, "(") || tokens[j].close != i+1 ||
				tokens[j-1].parent != body || guard >= 0 {
				return 0, 0
			}
			guard, from = i+2, tokens[j-1].line
		}
	}
	if guard < 0 {
		return 0, 0
	}
	first := jsTerminal(tokens, guard, end)
	if first < 0 {
		return 0, 0
	}
	if tokenIs(tokens, first, "else") {
		first++
	}
	last := jsTerminal(tokens, first, end)
	if last < 0 {
		return 0, 0
	}
	return from, tokens[last-1].line
}

func jsTerminal(tokens []callToken, at, limit int) int {
	if at >= limit {
		return -1
	}
	close := -1
	if tokenIs(tokens, at, "{") {
		close = tokens[at].close
		at++
		limit = close
	}
	if !tokenIs(tokens, at, "return") && !tokenIs(tokens, at, "throw") || at+1 >= limit || tokens[at+1].line != tokens[at].line {
		return -1
	}
	depth := tokens[at].depth
	i := at + 1
	for i < limit && tokens[i].line <= tokens[i-1].endLine && !tokenIs(tokens, i, ";") && !tokenIs(tokens, i, "else") {
		if tokens[i].depth < depth {
			break
		}
		i = max(i+1, tokens[i].close+1)
	}
	if i == at+1 {
		return -1
	}
	if tokenIs(tokens, i, ";") {
		i++
	}
	if close >= 0 {
		if i != close {
			return -1
		}
		return close + 1
	}
	return i
}

func (o jsOption) compare(args [][]callToken) (mode, context string) {
	var keys []string
	for i, arg := range args {
		if i == o.argument {
			continue
		}
		if scalar := jsScalar(arg); scalar != "" {
			keys = append(keys, strconv.Itoa(i)+":"+strconv.Quote(scalar))
		} else {
			return "", ""
		}
	}
	switch {
	case len(args) <= o.argument:
		if !o.omittable {
			return "", ""
		}
		mode = "default"
	case o.property == "":
		if v := jsScalar(args[o.argument]); v != "" && jsTruthy(v) != jsTruthy(o.value) {
			mode = "override"
		}
	default:
		a := args[o.argument]
		if len(a) < 2 || !tokenIs(a, 0, "{") || !tokenIs(a, len(a)-1, "}") {
			return "", ""
		}
		seen := map[string]bool{}
		mode = "default"
		for _, field := range jsComponents(a[1:len(a)-1], a[0].depth+1) {
			if len(field) < 3 || !field[0].name && !field[0].quoted || !tokenIs(field, 1, ":") {
				return "", ""
			}
			name, value := field[0].text, jsScalar(field[2:])
			if value == "" || seen[name] {
				return "", ""
			}
			seen[name] = true
			if name != o.property {
				keys = append(keys, "property:"+strconv.Quote(name)+strconv.Quote(value))
			} else if jsTruthy(value) != jsTruthy(o.value) {
				mode = "override"
			} else {
				mode = ""
			}
		}
	}
	slices.Sort(keys)
	return mode, strings.Join(keys, "\n")
}

func (o jsOption) publicName() string {
	if o.property != "" {
		return o.property
	}
	return o.variable
}

func jsTruthy(value string) bool {
	switch value {
	case "false", "null", "number:0", "number:-0", "string:":
		return false
	default:
		return true
	}
}

func (t jsOptionTrace) windows(s *roleStage, sources map[string]string) []record.Excerpt {
	witnesses := []contractWitness{t.implementation}
	if t.branch.line > 0 {
		witnesses = append(witnesses, t.branch)
	}
	if t.defaultCheck.line > 0 && t.override.line > 0 {
		d, o := t.defaultCheck, t.override
		if d.entry.Path == o.entry.Path {
			lines := strings.Split(sources[d.entry.Path], "\n")
			first, last := min(d.line, o.line), max(d.end, o.end)
			if fitsWitness(lines, first, last) {
				d.line, d.end = first, last
				witnesses = append(witnesses, d)
			} else {
				witnesses = append(witnesses, d, o)
			}
		} else {
			witnesses = append(witnesses, d, o)
		}
	}
	var windows []record.Excerpt
	for _, w := range witnesses {
		lines := strings.Split(sources[w.entry.Path], "\n")
		x, ok := s.boundedExcerpt(w.entry, windowKind(w.entry.Path), "context", w.line, lines[w.line-1:w.end], witnessMaxExcerpt)
		if !ok || x.End != w.end {
			return nil
		}
		windows = append(windows, x)
	}
	return windows
}
