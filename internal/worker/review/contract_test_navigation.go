package review

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type testAssertion struct {
	start int
	end   int
	body  int
}

// javascriptTestAssertions recognizes complete assertions within a named
// test/it callback. A filename, a setup function, or the test title alone
// cannot provide an assertion window.
func javascriptTestAssertions(content string) ([]callToken, []testAssertion) {
	tokens := scanCallTokens(content, false, true)
	bodies := map[int]bool{}
	for i, tok := range tokens {
		if !tok.name || (tok.text != "test" && tok.text != "it") || tokenIs(tokens, i-1, ".") ||
			!tokenIs(tokens, i+1, "(") || i+2 >= len(tokens) || !tokens[i+2].quoted {
			continue
		}
		call := i + 1
		for k := call + 2; k < tokens[call].close; k++ {
			if tokens[k].parent != call {
				continue
			}
			if tokenIs(tokens, k, "{") && testCallbackBody(tokens, k) {
				bodies[k] = true
				break
			}
			if tokens[k].close > k {
				k = tokens[k].close
			}
		}
	}
	var assertions []testAssertion
	for i, tok := range tokens {
		end := assertionEnd(tokens, i)
		if end <= i {
			continue
		}
		for p := tok.parent; p >= 0; p = tokens[p].parent {
			if bodies[p] {
				if end < tokens[p].close {
					assertions = append(assertions, testAssertion{start: tok.line, end: tokens[end].line, body: p})
				}
				break
			}
			// A helper declared inside a test may never be called. Its own
			// assertions are not locations in the named callback's body.
			if tokenIs(tokens, p, "{") && testCallbackBody(tokens, p) {
				break
			}
		}
		if len(assertions) == callMaxSites {
			break
		}
	}
	return tokens, assertions
}

func testCallbackBody(tokens []callToken, body int) bool {
	if tokenIs(tokens, body-1, ">") && tokenIs(tokens, body-2, "=") {
		return true
	}
	if !tokenIs(tokens, body-1, ")") {
		return false
	}
	beforeParameters := tokens[body-1].parent - 1
	return tokenIs(tokens, beforeParameters, "function") ||
		(tokenName(tokens, beforeParameters) && tokenIs(tokens, beforeParameters-1, "function"))
}

func (s *roleStage) testNavigationLocation(e source.Entry, lines []string) (record.ContractLocation, bool) {
	if record.SourceKind(e.Path) != "test" || record.SourceEcosystem(e.Path) != "javascript" {
		return record.ContractLocation{}, false
	}
	tokens, assertions := javascriptTestAssertions(strings.Join(lines, "\n"))
	var best record.ContractLocation
	bestCount := 0
	for i, first := range assertions {
		body := tokens[first.body]
		start := max(body.line, first.start-3)
		last := tokens[body.close].line
		location, ok := s.navigationLocation(e, lines[:last], start+1, "")
		if !ok || location.End < first.end {
			continue
		}
		count := 0
		for _, assertion := range assertions[i:] {
			if assertion.start > location.End {
				break
			}
			if assertion.body == first.body && assertion.end <= location.End {
				count++
			}
		}
		if count > bestCount {
			best, bestCount = location, count
		}
	}
	return best, bestCount > 0
}
