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
	var terms []reportTerm
	for _, term := range overviewGlossary {
		if term.Source && len(d.Sources) > 0 {
			terms = append(terms, term)
		} else if term.match != nil {
			for _, text := range []string{d.Title, d.DocumentClaim, d.Reason, d.Condition, d.Boundary, d.Next} {
				if term.match.MatchString(text) {
					terms = append(terms, term)
					break
				}
			}
		}
	}
	return terms
}
