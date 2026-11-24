package report

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed reportui/rule_descriptions_ko.json
var ruleDescriptionsJSON []byte

var koreanRuleDescriptions = func() map[string]string {
	var descriptions map[string]string
	if err := json.Unmarshal(ruleDescriptionsJSON, &descriptions); err != nil {
		panic(err)
	}
	return descriptions
}()

func (l Locale) ruleDescription(analyzer, rule, fallback string) string {
	if text := l.RuleDescriptions[analyzer+":"+rule]; text != "" {
		return text
	}
	if fallback == "" || fallback == l.Label("message_omitted") || genericDiagnosticMessage(analyzer, rule, fallback) {
		if text := l.RuleDescriptions[analyzer+":*"]; text != "" {
			return text
		}
	}
	return fallback
}

// firstSentence is the description up to its first sentence end: a title
// carries the statement of the problem, the full paragraph (why it matters
// and how to fix it) stays in the overview.
func firstSentence(description string) string {
	if i := strings.Index(description, ". "); i >= 0 {
		return description[:i+1]
	}
	return description
}

func genericDiagnosticMessage(analyzer, rule, message string) bool {
	switch analyzer {
	case "java.pmd":
		return message == "pmd reported "+rule
	case "java.spotbugs":
		return message == "spotbugs reported "+rule
	case "javascript.dependency_cruiser", "python.import_linter":
		return message == "Dependency rule violated: "+rule
	}
	return false
}

func diagnosticFindingTitle(l Locale, f Finding, o Observation) string {
	if f.Validation == ValidationUnreviewed && o.Metric == nil && o.Tool != "" && o.RuleID != "" {
		prefix := o.Tool + " " + o.RuleID
		if strings.HasPrefix(f.Title, prefix+": ") || strings.HasPrefix(f.Title, prefix+" 진단") {
			raw := optText(o.RawMessage, "")
			description := l.ruleDescription(o.Tool, o.RuleID, raw)
			if description != "" && description != raw {
				return prefix + " · " + firstSentence(description)
			}
			if genericDiagnosticMessage(o.Tool, o.RuleID, raw) {
				return prefix
			}
		}
	}
	return f.Title
}
