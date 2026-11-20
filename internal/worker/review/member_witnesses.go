package review

import (
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var (
	qualifiedMember = regexp.MustCompile(`(?:[A-Za-z_$][A-Za-z0-9_$]*\.)+[A-Za-z_$][A-Za-z0-9_$]*`)
	documentedCall  = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)[ \t]*\(`)
)

const memberSearchTokens = 768

func documentedMembers(text string, includeCalls bool) []string {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if !seen[name] && len(names) < 12 {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, ref := range qualifiedMember.FindAllString(text, 12) {
		if windowKind(ref) != "" {
			continue
		}
		add(ref[strings.LastIndexByte(ref, '.')+1:])
	}
	if includeCalls {
		for _, match := range documentedCall.FindAllStringSubmatch(text, 12) {
			add(match[1])
		}
	}
	return names
}

type memberPair struct {
	source, test contractWitness
}

type memberTest struct {
	module  string
	witness contractWitness
}

type memberKey struct{ path, name string }

type memberWitnesses struct {
	sources map[memberKey][]contractWitness
	tests   map[string][]memberTest
}

func (m *memberWitnesses) observe(e source.Entry, text string, queries []witnessQuery, test bool) {
	if callLanguage(e.Path) != "javascript" {
		return
	}
	wanted := map[string]bool{}
	for _, q := range queries {
		if record.ScopeUnder(e.Path, q.scope) {
			for _, member := range q.members {
				if strings.Contains(text, member) {
					wanted[member] = true
				}
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	tokens := scanCallTokens(text, false, true)
	if len(tokens) == 0 {
		return
	}
	lines := strings.Split(text, "\n")
	if !test {
		for i, t := range tokens {
			if (t.name || t.quoted) && wanted[t.text] {
				start, end := memberBody(tokens, i)
				if start > 0 && fitsWitness(lines, start, end) {
					key := memberKey{e.Path, t.text}
					m.sources[key] = append(m.sources[key], contractWitness{entry: e, line: start, end: end})
				}
			}
		}
		return
	}
	factories := memberFactories(tokens)
	sites := 0
	for i, t := range tokens {
		if !t.name || !wanted[t.text] || !tokenIs(tokens, i-1, ".") || !tokenName(tokens, i-2) ||
			len(lines[t.line-1]) > witnessMaxExcerpt {
			continue
		}
		sites++
		if sites > callMaxSites {
			break
		}
		module, start, end, score := memberAssertion(tokens, i, factories)
		if module != "" && fitsWitness(lines, start, end) {
			m.tests[t.text] = append(m.tests[t.text], memberTest{module, contractWitness{entry: e, line: start, end: end, score: score}})
		}
	}
}

// memberBody recognizes method blocks and descriptor objects installed by
// Object.defineProperties. Whole bodies are required inside the excerpt cap.
func memberBody(tokens []callToken, i int) (start, end int) {
	t := tokens[i]
	body := -1
	if tokenIs(tokens, i+1, ":") && tokenIs(tokens, i+2, "{") {
		object := t.parent
		if !tokenIs(tokens, object, "{") {
			return 0, 0
		}
		args := tokens[object].parent
		if tokenIs(tokens, args, "(") && tokenIs(tokens, args-3, "Object") && tokenIs(tokens, args-2, ".") &&
			tokenIs(tokens, args-1, "defineProperties") && tokenIs(tokens, object-1, ",") {
			body = i + 2
		}
	} else if tokenIs(tokens, i+1, "(") && tokenIs(tokens, t.parent, "{") {
		next := tokens[i+1].close + 1
		if tokenIs(tokens, next, "{") {
			if tokenIs(tokens, i-1, ".") || tokenIs(tokens, i-1, "function") || tokenIs(tokens, i-1, "new") {
				return 0, 0
			}
			body = next
		}
	}
	if body < 0 || tokens[body].close == body+1 {
		return 0, 0
	}
	return t.line, tokens[tokens[body].close].line
}

// memberFactories accepts explicit relative ESM imports used to create the
// same receiver checked in a test.
func memberFactories(tokens []callToken) map[string]string {
	out := map[string]string{}
	binding := map[string]int{}
	for name, bindings := range importedBindings(tokens, false) {
		if len(bindings) == 1 {
			out[name] = bindings[0].module
			binding[name] = bindings[0].token
		}
	}
	for i, t := range tokens {
		if !t.name || t.text != "import" || t.depth != 0 || !tokenName(tokens, i+1) || tokenIs(tokens, i+1, "type") {
			continue
		}
		j := i + 2
		if tokenIs(tokens, j, ",") && tokenIs(tokens, j+1, "{") {
			j = tokens[j+1].close + 1
		}
		if tokenIs(tokens, j, "from") && j+1 < len(tokens) && tokens[j+1].quoted {
			module := tokens[j+1].text
			if strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../") {
				out[tokens[i+1].text] = module
				binding[tokens[i+1].text] = i + 1
			}
		}
	}
	for i, t := range tokens {
		introduced, found := binding[t.text]
		if t.name && found && i != introduced &&
			(!tokenIs(tokens, i+1, "(") || tokenIs(tokens, i-1, ".") || tokenIs(tokens, i-1, "function") || tokenIs(tokens, i-1, "class")) {
			delete(out, t.text)
		}
	}
	return out
}

func memberAssertion(tokens []callToken, i int, factories map[string]string) (module string, start, end, score int) {
	receiver := tokens[i-2].text
	block := tokens[i].parent
	for block >= 0 && !tokenIs(tokens, block, "{") {
		block = tokens[block].parent
	}
	if block < 0 {
		return "", 0, 0, 0
	}
	found := -1
	for j := i - 3; j > max(block, i-memberSearchTokens) && tokens[i].line-tokens[j].line < witnessMaxLines; j-- {
		if !tokenIs(tokens, j, receiver) || !tokenIs(tokens, j+1, "=") {
			continue
		}
		if !tokenIs(tokens, j-1, "const") && !tokenIs(tokens, j-1, "let") && !tokenIs(tokens, j-1, "var") {
			return "", 0, 0, 0
		}
		factory := j + 2
		if tokenIs(tokens, factory, "new") || tokenIs(tokens, factory, "await") {
			factory++
		}
		if tokenName(tokens, factory) && tokenIs(tokens, factory+1, "(") {
			module = factories[tokens[factory].text]
			found = j - 1
		}
		break
	}
	if module == "" {
		return "", 0, 0, 0
	}
	// A member invocation may be inside an assertion, or followed by an
	// assertion on that same receiver in the same test block.
	invoked := tokenIs(tokens, i+1, "(")
	standalone := invoked && tokens[i].parent == block
	stop := min(tokens[block].close, found+memberSearchTokens)
	assertions := 0
	for j := found + 1; j < stop && tokens[j].line-tokens[found].line < witnessMaxLines; j++ {
		if j > i && tokenIs(tokens, j, receiver) && tokenIs(tokens, j+1, "=") && !tokenIs(tokens, j+2, "=") {
			break
		}
		close := assertionEnd(tokens, j)
		if close < i || close >= stop || tokens[close].line-tokens[found].line >= witnessMaxLines {
			continue
		}
		for k := j + 2; k < close; k++ {
			if !tokenIs(tokens, k, receiver) {
				continue
			}
			switch {
			case j > i && standalone:
				score = max(score, 30-min(8, tokens[j].line-tokens[i].line))
			case j < i && invoked:
				score = max(score, 12)
			case j < i:
				score = max(score, 1)
			}
			break
		}
		if score > 0 {
			end = max(end, tokens[close].line)
			if j > i {
				assertions++
			}
		}
	}
	if score == 0 {
		return "", 0, 0, 0
	}
	// Additional assertions break ties without outranking a stronger
	// relationship between the invocation and the check.
	return module, tokens[found].line, end, 10*score + min(9, assertions)
}

func assertionEnd(tokens []callToken, i int) int {
	if !tokens[i].name || !tokenIs(tokens, i+1, "(") {
		return -1
	}
	close := tokens[i+1].close
	switch tokens[i].text {
	case "expect":
		if tokenIs(tokens, close+1, ".") && tokenIs(tokens, close+2, "not") {
			close += 2
		}
		if tokenIs(tokens, close+1, ".") && tokenName(tokens, close+2) &&
			strings.HasPrefix(tokens[close+2].text, "to") && tokenIs(tokens, close+3, "(") {
			return tokens[close+3].close
		}
	case "assert", "assertEqual", "equal", "equals", "strictEqual", "deepEqual", "deepStrictEqual",
		"is", "isNot", "throws", "throwsAsync", "rejects", "ok":
		return close
	}
	return -1
}

// choose pairs one implemented member with an assertion from a test that
// resolves to the implementation's module. Ambiguous definitions are left
// to ordinary contract witnesses.
func (m memberWitnesses) choose(queries []witnessQuery, files map[string]source.Entry) (memberPair, bool) {
	best := memberPair{}
	for i, q := range queries {
		for _, name := range q.members {
			count := 0
			for key := range m.sources {
				if key.name == name && record.ScopeUnder(key.path, q.scope) {
					count++
				}
			}
			for _, test := range m.tests[name] {
				p := test.witness.entry.Path
				target := directModule(files, p, test.module, false)
				definitions := m.sources[memberKey{target, name}]
				if len(definitions) != 1 || (count > 1 && !q.names(target)) || test.witness.score <= best.test.score ||
					!record.ScopeUnder(p, q.scope) || !record.ScopeUnder(target, q.scope) {
					continue
				}
				source := definitions[0]
				source.query, test.witness.query = i+1, i+1
				best = memberPair{source, test.witness}
			}
		}
	}
	return best, best.test.score > 0
}
