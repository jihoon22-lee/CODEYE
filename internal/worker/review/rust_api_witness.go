package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type rustDocumentBinding struct {
	entry source.Entry
	name  string
	decl  rustFunction
	text  string
}

func rustDocumentedBindings(c *sourceCalls, q witnessQuery) ([]rustDocumentBinding, error) {
	var out []rustDocumentBinding
	paths := make([]string, 0, len(q.rustDocs))
	for p := range q.rustDocs {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		e := c.files[p]
		if !productionAPISource(e, q.scope, ".rs") {
			continue
		}
		text, ok, err := c.text(p)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		tokens := documentationTokens(text, false)
		lines := strings.Split(text, "\n")
		for i, t := range tokens {
			if t.depth != 0 || !tokenIs(tokens, i, "fn") || !tokenName(tokens, i+1) {
				continue
			}
			name := tokens[i+1].text
			d := rustPublicFunction(tokens, name)
			start, end := rustDocLines(lines, d.docThrough)
			if start > 0 && slices.ContainsFunc(q.rustDocs[p], func(doc record.Excerpt) bool {
				return doc.SHA256 == e.SHA256 && doc.Start >= start && doc.End <= end
			}) {
				out = append(out, rustDocumentBinding{e, name, d, text})
			}
		}
	}
	return out, nil
}

func rustImplementationWitness(binding rustDocumentBinding, q witnessQuery) contractWitness {
	lines := strings.Split(binding.text, "\n")
	first, last := binding.decl.first, binding.decl.last
	if fitsWitness(lines, first, last) {
		return contractWitness{entry: binding.entry, line: first, end: last, score: 4 * witnessMinScore, direct: true, documented: true}
	}
	var best contractWitness
	for start := first; start <= last; start++ {
		for end := min(last, start+witnessMaxLines-1); end >= start; end-- {
			if !fitsWitness(lines, start, end) {
				continue
			}
			passage := strings.Join(lines[start-1:end], "\n")
			score := witnessMatch(witnessTerms(passage), q.terms)
			if start == first {
				score += witnessMinScore
			}
			if score > best.score && !strings.Contains(passage, "///") {
				best = contractWitness{entry: binding.entry, line: start, end: end, score: score, direct: true, documented: true}
			}
			break
		}
	}
	return best
}

func rustTestFunction(tokens []callToken, at int) bool {
	for block := tokens[at].parent; block >= 0; block = tokens[block].parent {
		if !tokenIs(tokens, block, "{") || !tokenIs(tokens, block-1, ")") {
			continue
		}
		open := tokens[block-1].parent
		if !tokenName(tokens, open-1) || !tokenIs(tokens, open-2, "fn") {
			continue
		}
		first, safe := rustAttributes(tokens, open-2, "test")
		if safe && first < open-2 && tokenIs(tokens, first+2, "test") && tokenIs(tokens, first+3, "]") {
			return true
		}
	}
	return false
}

func rustAssertion(tokens []callToken, at int) (int, int) {
	if !rustTestFunction(tokens, at) {
		return 0, 0
	}
	for p := tokens[at].parent; p >= 2; p = tokens[p].parent {
		if !tokenIs(tokens, p-1, "!") || !tokenIs(tokens, p, "(") {
			continue
		}
		switch tokens[p-2].text {
		case "assert", "assert_eq", "assert_ne", "matches":
			if tokens[p-2].text == "matches" {
				continue // standalone pattern matching is not an assertion
			}
			return tokens[p-2].line, tokens[tokens[p].close].endLine
		}
	}
	return 0, 0
}

func (b rustDocumentBinding) sameFileCalls(entry source.Entry, tokens []callToken) []int {
	if entry.Path != b.entry.Path {
		return nil
	}
	var calls []int
	for i, t := range tokens {
		if t.text != b.name || !tokenIs(tokens, i+1, "(") || tokenIs(tokens, i-1, ".") ||
			!(tokenIs(tokens, i-3, "super") && tokenIs(tokens, i-2, ":") && tokenIs(tokens, i-1, ":")) {
			continue
		}
		// Each checked call is in a test nested inside one top-level module,
		// so super:: resolves to the root of the file holding the declaration.
		block, modules := t.parent, 0
		for ; block >= 0; block = tokens[block].parent {
			if tokenIs(tokens, block, "{") && tokenName(tokens, block-1) && tokenIs(tokens, block-2, "mod") {
				modules++
			}
		}
		if modules == 1 {
			calls = append(calls, i)
		}
	}
	return calls
}

func (b rustDocumentBinding) linkedAssertion(c *sourceCalls, entry source.Entry, text string, q witnessQuery) (contractWitness, error) {
	if path.Ext(entry.Path) != ".rs" || entry.Path != b.entry.Path && !record.IsTestPath(entry.Path) ||
		!record.ScopeUnder(entry.Path, q.scope) {
		return contractWitness{}, nil
	}
	tokens := documentationTokens(text, false)
	calls := b.sameFileCalls(entry, tokens)
	if record.IsTestPath(entry.Path) {
		count := map[string]int{}
		var imports []rustAPIUse
		for i, t := range tokens {
			if t.depth != 0 || !tokenIs(tokens, i, "use") {
				continue
			}
			for _, use := range rustUsePaths(tokens, i) {
				count[use.local]++
				imports = append(imports, use)
			}
		}
		for _, use := range imports {
			if count[use.local] != 1 {
				continue
			}
			sites := rustBoundCalls(tokens, use)
			if len(sites) == 0 {
				continue
			}
			e, symbol, _, err := documentedRustObject(c, q.scope, use)
			if err != nil {
				return contractWitness{}, err
			}
			if e.Path == b.entry.Path && symbol == b.name {
				calls = append(calls, sites...)
			}
		}
	}
	lines := strings.Split(text, "\n")
	var best contractWitness
	for _, at := range calls {
		start, end := rustAssertion(tokens, at)
		if start == 0 || !fitsWitness(lines, start, end) || entry.Path == b.entry.Path && start <= b.decl.last {
			continue
		}
		score := 3*witnessMinScore + min(witnessMinScore, witnessMatch(witnessTerms(strings.Join(lines[start-1:end], "\n")), q.terms))
		if score > best.score {
			best = contractWitness{entry: entry, line: start, end: end, score: score, direct: true, documented: true}
		}
	}
	return best, nil
}
