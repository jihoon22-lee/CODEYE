package report

import (
	"strings"
	"unicode"
)

func readingIssueContext(f Finding) string {
	return strings.Join([]string{f.Category, f.Validation, f.Disposition}, "\x00")
}

func readingCodeRefs(f Finding) string {
	if len(f.Evidence) == 0 {
		return ""
	}
	var locations, evidence []string
	for _, location := range f.Locations {
		locations = append(locations, sourceRange(location.Path, location.StartLine, location.EndLine))
	}
	for _, ref := range f.Evidence {
		if ref.Kind != "code" || ref.Location == nil || ref.ID == "" {
			return ""
		}
		location := ref.Location
		evidence = append(evidence, ref.ID+"\x00"+sourceRange(location.Path, location.StartLine, location.EndLine))
	}
	return readingIssueContext(f) + "\x00" + strings.Join(sortedUnique(locations), "\n") + "\x00" + strings.Join(sortedUnique(evidence), "\n")
}

// duplicateReadingFindings identifies records only for the compact reading
// view. Matching locations alone do not identify an issue. An unlinked model
// sibling needs one matching code citation, observed rule and issue title.
func duplicateReadingFindings(findings []Finding, observations []Observation) map[string]bool {
	rules := map[string]string{}
	for _, observation := range observations {
		rules[observation.ID] = strings.ToLower(strings.TrimSpace(observation.RuleID))
	}
	observed := map[string][]Finding{}
	seen := map[string]bool{}
	duplicates := map[string]bool{}
	for _, finding := range findings {
		ids := sortedUnique(finding.ObservationIDs)
		if len(ids) == 0 {
			continue
		}
		key := readingIssueContext(finding) + "\x00" + strings.Join(ids, "\n")
		if seen[key] {
			duplicates[finding.ID] = true
			continue
		}
		seen[key] = true
		if evidence := readingCodeRefs(finding); evidence != "" && len(ids) == 1 && rules[ids[0]] != "" {
			observed[evidence] = append(observed[evidence], finding)
		}
	}
	for _, finding := range findings {
		if len(finding.ObservationIDs) > 0 {
			continue
		}
		sources := observed[readingCodeRefs(finding)]
		if len(sources) == 1 && sameRuleTitle(finding.Title, sources[0].Title, rules[sources[0].ObservationIDs[0]]) {
			duplicates[finding.ID] = true
		}
	}
	return duplicates
}

func sameRuleTitle(left, right, rule string) bool {
	words := func(title string) map[string]bool {
		out := map[string]bool{}
		for _, word := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
		}) {
			out[word] = true
		}
		return out
	}
	a, b := words(left), words(right)
	if !a[rule] || !b[rule] {
		return false
	}
	shared := 0
	for word := range a {
		if b[word] {
			shared++
		}
	}
	return shared >= 3 && shared*2 >= max(len(a), len(b))
}
