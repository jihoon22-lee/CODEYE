package review

import (
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

var contractQualifiedTerm = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_]*(?:\.[A-Za-z0-9][A-Za-z0-9_]*)+`)

// Qualified names, versions, and explicit code literals can bind a slice's
// cited implementation to the promise without matching translated prose.
func contractStrengthTerms(statement string, docs []record.Excerpt) []string {
	var terms []string
	for _, atom := range contractQualifiedTerm.FindAllString(statement, -1) {
		if len(atom) <= 48 && slices.ContainsFunc(docs, func(e record.Excerpt) bool { return hasContractStrengthTerm(e.Text, atom) }) {
			terms = append(terms, atom)
		}
	}
	for _, x := range witnessCode.FindAllStringSubmatch(statement, -1) {
		if record.ValidContractLiteral(x[1]) && slices.ContainsFunc(docs, func(e record.Excerpt) bool { return hasContractStrengthTerm(e.Text, x[1]) }) {
			terms = append(terms, x[1])
		}
	}
	return terms
}

func hasContractStrengthTerm(text, term string) bool {
	if strings.ContainsRune(term, '.') {
		return slices.Contains(contractQualifiedTerm.FindAllString(text, -1), term)
	}
	return containsContractLiteral(text, term)
}

func contractStrengthCitations(r record.Record, terms []string) map[string][]string {
	if len(terms) == 0 || len(r.Strengths) == 0 {
		return nil
	}
	cited := map[string][]string{}
	for _, strength := range r.Strengths {
		text := strength.Title + "\n" + strength.Benefit
		for _, term := range terms {
			if !hasContractStrengthTerm(text, term) {
				continue
			}
			for _, id := range strength.EvidenceIDs {
				cited[id] = append(cited[id], term)
			}
		}
	}
	return cited
}

// Require the shared term on a newly seen implementation line of the cited
// window. A filename, signature, comment or test cannot match by itself.
func contractStrengthSource(x record.Excerpt, text string, terms []string, overview []record.Excerpt) bool {
	if record.GeneratedHeader([]byte(text)) {
		return false
	}
	lines := strings.Split(text, "\n")
	found := false
	contractExecutableLines(text, x.Path, func(line int, _ bool) bool {
		if line > x.End {
			return true
		}
		found = line >= x.Start && record.NewContractEvidenceLine(x, line, overview) && slices.ContainsFunc(terms, func(term string) bool {
			return hasContractStrengthTerm(lines[line-1], term)
		})
		return found
	})
	return found
}
