package review

import (
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type formulaOperations uint8

const (
	formulaSum formulaOperations = 1 << iota
	formulaMultiply
	formulaDivide
)

type pythonFormulaQuery struct {
	ops    formulaOperations
	labels map[string]int
}

func (q pythonFormulaQuery) anchored() bool {
	return q.ops&formulaSum != 0 && q.ops&(formulaMultiply|formulaDivide) != 0
}

// Only use arithmetic cues present in both the accepted statement and its
// cited passage. The text indicates which operation to inspect.
func (q *pythonFormulaQuery) fromPassage(statement, passage string) {
	ops := documentedFormulaOperations(statement) & documentedFormulaOperations(passage)
	if ops&formulaSum == 0 || ops&(formulaMultiply|formulaDivide) == 0 {
		return
	}
	if q.labels == nil {
		q.labels = map[string]int{}
	}
	words := map[string]int{}
	addWitnessWords(words, statement, 4)
	for _, term := range witnessTerms(passage) {
		if words[term] == 0 || utf8.RuneCountInString(term) < 2 || documentedFormulaOperations(term) != 0 {
			continue
		}
		switch term {
		case "calculation", "current", "each", "formula", "group", "per", "result", "value", "계산", "각각", "결과", "현재":
			continue
		}
		q.labels[term] = words[term]
	}
	q.ops |= ops
}

func documentedFormulaOperations(text string) formulaOperations {
	var ops formulaOperations
	for _, word := range witnessTerms(text) {
		switch {
		case word == "sum" || word == "sums" || word == "summed" || word == "summing" ||
			strings.HasPrefix(word, "aggregat") || word == "합" || strings.HasPrefix(word, "총합") || strings.HasPrefix(word, "합산"):
			ops |= formulaSum
		case strings.HasPrefix(word, "multipl") || strings.HasPrefix(word, "곱"):
			ops |= formulaMultiply
		case strings.HasPrefix(word, "divid") || strings.HasPrefix(word, "나누") ||
			strings.HasPrefix(word, "나눈") || strings.HasPrefix(word, "나눠"):
			ops |= formulaDivide
		}
	}
	if strings.ContainsAny(text, "∑Σ") {
		ops |= formulaSum
	}
	if strings.Contains(text, "×") {
		ops |= formulaMultiply
	}
	if strings.Contains(text, "÷") {
		ops |= formulaDivide
	}
	for _, code := range witnessCode.FindAllString(text, -1) {
		if strings.Contains(code, " * ") || strings.Contains(code, "sum(") && strings.Count(code, "*") == 1 {
			ops |= formulaMultiply
		}
		if strings.Contains(code, " / ") || strings.Contains(code, "sum(") && strings.Count(code, "/") == 1 {
			ops |= formulaDivide
		}
	}
	return ops
}

// Match a subject in a declaration's docstring or an exact documented name,
// then require a bounded executable result with the requested operations.
// If equally supported declarations match, leave ordinary evidence ordering.
func pythonFormulaWitness(e source.Entry, content string, query witnessQuery) (contractWitness, bool) {
	if !query.formula.anchored() || record.SourceKind(e.Path) != "code" || callLanguage(e.Path) != "python" ||
		!record.ScopeUnder(e.Path, query.scope) || !strings.Contains(content, "sum") {
		return contractWitness{}, false
	}
	tokens := callTokens(content, true)
	if pythonDynamicBindings(tokens) {
		return contractWitness{}, false
	}
	for i, tok := range tokens {
		if tok.name && tok.text == "sum" && !tokenIs(tokens, i-1, ".") && !isDirectCall(tokens, i) {
			return contractWitness{}, false
		}
	}
	lines := strings.Split(content, "\n")
	var best contractWitness
	tied := false
	for _, d := range pythonDocDeclarations(tokens) {
		if d.class || d.indent != 0 {
			continue
		}
		doc := pythonDocstring(tokens, d.body)
		score := 0
		if doc.start > 0 {
			subject := strings.Join(lines[doc.start-1:doc.end], " ")
			if documentedFormulaOperations(subject)&formulaSum != 0 {
				score = witnessMatch(witnessTerms(subject), query.formula.labels)
			}
		}
		for _, name := range query.navigationNames {
			if name == d.name {
				score += witnessMinScore
				break
			}
		}
		if score == 0 || score < best.score {
			continue
		}
		first, end := pythonFormulaResult(tokens, lines, d, doc, query.formula.ops)
		if first > 0 {
			if score == best.score {
				tied = true
				continue
			}
			best, tied = contractWitness{entry: e, line: first, end: end, score: score, direct: true}, false
		}
	}
	return best, tied
}

func pythonFormulaResult(tokens []callToken, lines []string, d pythonDocDeclaration, doc pythonDocRange, ops formulaOperations) (int, int) {
	var starts []int
	indent := tokens[d.body].column
	first, end := 0, 0
	for i := d.body; i < len(tokens); i++ {
		t := tokens[i]
		if t.depth != 0 {
			continue
		}
		if t.column <= d.indent {
			break
		}
		if t.line <= doc.end || t.column != indent || i > d.body && tokens[i-1].endLine == t.line {
			continue
		}
		if !tokenIs(tokens, i, "return") {
			starts = append(starts, t.line)
			continue
		}
		if !tokenIs(tokens, i+1, "sum") || !tokenIs(tokens, i+2, "(") || tokens[i+2].close <= i+2 {
			starts = starts[:0]
			continue
		}
		last := tokens[i+2].close
		if (last+1 < len(tokens) && tokens[last+1].line == tokens[last].endLine) ||
			!pythonFormulaOperands(tokens[i+3:last], ops) || !fitsWitness(lines, t.line, tokens[last].endLine) || first != 0 {
			return 0, 0
		}
		first, end = t.line, tokens[last].endLine
		for _, start := range starts {
			if end-start < witnessMaxLines && fitsWitness(lines, start, end) {
				first = start
				break
			}
		}
		starts = starts[:0]
	}
	return first, end
}

func pythonFormulaOperands(tokens []callToken, ops formulaOperations) bool {
	seen := formulaSum
	for i := 1; i+1 < len(tokens); i++ {
		left, right := tokens[i-1], tokens[i+1]
		isOperand := func(t callToken, delimiters string) bool {
			return t.name || len(t.text) == 1 && strings.Contains(delimiters+"0123456789", t.text)
		}
		if !isOperand(left, ")]") || !isOperand(right, "([") {
			continue
		}
		switch {
		case tokenIs(tokens, i, "*"):
			seen |= formulaMultiply
		case tokenIs(tokens, i, "/"):
			seen |= formulaDivide
		}
	}
	return seen&ops == ops
}
