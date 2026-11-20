package report

import (
	_ "embed"
	"encoding/json"
	"regexp"
)

//go:embed reportui/overview_glossary.json
var overviewGlossaryJSON []byte

type reportTerm struct {
	Name, Definition, Pattern string
	Source                    bool
	match                     *regexp.Regexp
}

var overviewGlossary = func() []reportTerm {
	var terms []reportTerm
	if err := json.Unmarshal(overviewGlossaryJSON, &terms); err != nil {
		panic(err)
	}
	for i := range terms {
		if terms[i].Pattern != "" {
			terms[i].match = regexp.MustCompile("(?i)" + terms[i].Pattern)
		}
	}
	return terms
}()

func decisionGlossary(d projectDecision) []reportTerm {
	texts := []string{d.Title, d.DocumentClaim, d.Reason, d.Condition, d.Boundary, d.Next}
	hasSources := len(d.Sources) > 0
	for _, row := range d.ComparisonRows {
		texts = append(texts, row.Text)
		hasSources = hasSources || len(row.Sources) > 0
	}
	var terms []reportTerm
	for _, term := range overviewGlossary {
		if term.Source && hasSources {
			terms = append(terms, term)
		} else if term.match != nil {
			for _, text := range texts {
				if term.match.MatchString(text) {
					terms = append(terms, term)
					break
				}
			}
		}
	}
	return terms
}
