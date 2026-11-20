package report

import (
	"path"
	"slices"
	"strings"
)

// The opening selects a few records with source evidence. The later sections
// retain the other records and the report's complete structured counts.
func (v secView) projectOpening() []secPart {
	l, r := v.L, v.R
	var actions, pending []Finding
	ids := make(map[string]bool, len(r.CodeReview.FindingIDs))
	for _, id := range r.CodeReview.FindingIDs {
		ids[id] = true
	}
	for _, f := range r.Findings {
		if !ids[f.ID] || hiddenDisposition[f.Disposition] || !hasProductFindingEvidence(f) {
			continue
		}
		switch f.Validation {
		case ValidationReproduced, ValidationSupported:
			actions = append(actions, f)
		case ValidationInconclusive, ValidationUnreviewed:
			if slices.ContainsFunc(f.Verification, nonempty) {
				pending = append(pending, f)
			}
		}
	}
	prioritize := func(a, b Finding) int {
		if diff := sevRank(b.Severity) - sevRank(a.Severity); diff != 0 {
			return diff
		}
		if a.Validation == ValidationReproduced && b.Validation != ValidationReproduced {
			return -1
		}
		if b.Validation == ValidationReproduced && a.Validation != ValidationReproduced {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(actions, prioritize)
	slices.SortStableFunc(pending, prioritize)

	var parts []secPart
	addFindings := func(key string, entries []Finding, limit int) {
		if len(entries) == 0 {
			return
		}
		p := secPart{Heading: l.Label(key)}
		for _, f := range entries[:min(limit, len(entries))] {
			c := v.findingCard(f, false)
			c.Link = f.ID
			p.Cards = append(p.Cards, c)
		}
		parts = append(parts, p)
	}
	addFindings("opening.actions", actions, 3)

	comparisons := secPart{Heading: l.Label("insight.comparison")}
	addComparison := func(c DocComparison) bool {
		if !hasProductSectionEvidence(c.CodeEvidenceIDs, r.Documents.Evidence, "code") ||
			!hasProductSectionEvidence(c.DocumentEvidenceIDs, r.Documents.Evidence, "document") {
			return false
		}
		comparisons.Cards = append(comparisons.Cards, secCard{
			Title: openingText(c.Statement, l.Label("opening.contract")), State: l.Label("insight." + c.Status), Link: SectionDocuments,
			Lines: append(scopeLine(l, c.AppliesTo), v.comparisonDetails(c, r.Documents.Evidence, false)...),
		})
		return true
	}
	for _, status := range []string{"mismatch", "partial", "match"} {
		shown := 0
		for _, c := range r.Documents.Comparisons {
			if len(comparisons.Cards) == 3 || shown == 2 {
				break
			}
			if c.Status == status && addComparison(c) {
				shown++
			}
		}
	}
	unresolvedComparison := false
	if len(comparisons.Cards) == 0 {
		for _, c := range r.Documents.Comparisons {
			if c.Status == "unknown" && addComparison(c) {
				comparisons.Heading = l.Label("opening.document_follow_up")
				unresolvedComparison = true
				break
			}
		}
	}
	if len(comparisons.Cards) > 0 && !unresolvedComparison {
		parts = append(parts, comparisons)
	}

	strengths := secPart{Heading: l.Label("opening.strengths")}
	for _, s := range r.CodeReview.Strengths {
		if len(strengths.Cards) == 3 {
			break
		}
		if !hasProductSectionEvidence(s.EvidenceIDs, r.CodeReview.Evidence, "code") {
			continue
		}
		lines := scopeLine(l, s.Scope)
		if benefit := openingText(s.Benefit, ""); benefit != "" {
			lines = append(lines, secItem{Label: l.Label("opening.benefit"), Text: benefit})
		}
		item := secItem{Label: l.Label("opening.code_evidence")}
		item.addCitations(v.sourceCitations(s.EvidenceIDs, r.CodeReview.Evidence, false), false)
		lines = append(lines, item)
		strengths.Cards = append(strengths.Cards, secCard{
			Title: openingText(s.Title, l.Label("insight.strength")), Link: SectionCodeReview, Lines: lines,
		})
	}
	if len(strengths.Cards) > 0 {
		parts = append(parts, strengths)
	}
	if unresolvedComparison {
		parts = append(parts, comparisons)
	}
	addFindings("opening.follow_up", pending, 2)
	if len(parts) > 0 {
		parts[0].Model = l.Label("insight.interpretation")
	}
	return parts
}

func (v secView) openingLimits() secPart {
	l, r := v.L, v.R
	p := secPart{Heading: l.Label("opening.limits")}
	if r.Coverage.AIReview.Status != AIReviewPerformed {
		p.Paras = append(p.Paras, l.Label("opening.model."+r.Coverage.AIReview.Status))
	}
	if r.Coverage.AIReview.Status == AIReviewPerformed &&
		len(r.Documents.Comparisons) == 0 && len(r.CodeReview.Strengths) == 0 {
		p.Paras = append(p.Paras, l.Label("opening.no_assessments"))
	}
	unknown := 0
	for _, c := range r.Documents.Comparisons {
		if c.Status == "unknown" {
			unknown++
		}
	}
	if unknown > 0 {
		p.List = append(p.List, secItem{Text: tf(l, "opening.unknown", unknown), Link: SectionDocuments})
	}
	if len(r.Gaps) > 0 {
		p.List = append(p.List, secItem{Text: l.Label("opening.gaps"), Link: SectionGaps})
	}
	return p
}

func (v secView) comparisonDetails(c DocComparison, evidence []SectionEvidence, excerpts bool) []secItem {
	l := v.L
	var items []secItem
	if reason := openingText(c.Reason, ""); reason != "" {
		label := "opening.comparison_reason"
		if c.Status == "unknown" {
			label = "opening.comparison_unknown_reason"
		}
		items = append(items, secItem{Label: l.Label(label), Text: reason})
	}
	for _, x := range []struct {
		label string
		ids   []string
	}{{"opening.document_evidence", c.DocumentEvidenceIDs}, {"opening.code_evidence", c.CodeEvidenceIDs}} {
		sources := v.sourceCitations(x.ids, evidence, excerpts)
		if len(sources) == 0 {
			continue
		}
		item := secItem{Label: l.Label(x.label)}
		item.addCitations(sources, excerpts)
		items = append(items, item)
	}
	return items
}

func scopeLine(l Locale, scope string) []secItem {
	item := secItem{Label: l.Label("insight.scope"), Codes: []string{scope}}
	if scope == "." {
		item.Text, item.Codes = l.Label("opening.root_scope"), nil
	}
	return []secItem{item}
}

func nonempty(s string) bool { return strings.TrimSpace(s) != "" }

func openingText(s, fallback string) string {
	if !nonempty(s) || s == textOmitted {
		return fallback
	}
	return s
}

func (it *secItem) addCitations(sources []sourceCitation, detail bool) {
	for _, s := range sources {
		if detail || s.URL != "" {
			it.Sources = append(it.Sources, s)
		} else {
			it.Codes = append(it.Codes, s.Location)
		}
	}
}

func hasProductSectionEvidence(ids []string, evidence []SectionEvidence, kind string) bool {
	for _, e := range evidence {
		if e.Kind == kind && slices.Contains(ids, e.ID) && productLocation(e.Path, e.StartLine, e.EndLine) {
			return true
		}
	}
	return false
}

func hasProductFindingEvidence(f Finding) bool {
	for _, e := range f.Evidence {
		if e.Kind != "code" || e.Location == nil || slices.Contains(f.CounterevidenceIDs, e.ID) ||
			!productLocation(e.Location.Path, e.Location.StartLine, e.Location.EndLine) {
			continue
		}
		for _, c := range f.Claims {
			if slices.Contains(c.EvidenceIDs, e.ID) {
				return true
			}
		}
		for _, c := range f.Conditions {
			if slices.Contains(c.EvidenceIDs, e.ID) {
				return true
			}
		}
	}
	return false
}

func productLocation(file string, start, end int) bool {
	if file == "" || start < 1 || end < start || secondaryPath(file) {
		return false
	}
	for _, dir := range strings.Split(strings.ToLower(path.Dir(file)), "/") {
		switch dir {
		case "test", "tests", "__tests__", "spec", "specs":
			return false
		}
	}
	base := strings.ToLower(path.Base(file))
	switch ext := path.Ext(base); ext {
	case ".go", ".rs":
		return !strings.HasSuffix(strings.TrimSuffix(base, ext), "_test")
	case ".py":
		return !strings.HasPrefix(base, "test_") && !strings.HasSuffix(base, "_test.py")
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
		stem := strings.TrimSuffix(base, ext)
		return !strings.HasSuffix(stem, ".test") && !strings.HasSuffix(stem, ".spec")
	}
	return true
}

func (v secView) primaryDiagnostic() secPart {
	l := v.L
	for _, o := range v.R.Observations {
		if o.Suppressed || o.Metric != nil || o.EvidenceState != EvidenceVerified || o.Location == nil ||
			!productLocation(o.Location.Path, o.Location.StartLine, o.Location.EndLine) {
			continue
		}
		if !slices.ContainsFunc(v.R.StaticAnalysis.Languages, func(lang StaticLanguage) bool {
			return slices.ContainsFunc(lang.Tools, func(tool StaticTool) bool {
				return optText(tool.Purpose, PurposeDiagnostics) == PurposeDiagnostics && slices.Contains(tool.ObservationIDs, o.ID)
			})
		}) {
			continue
		}
		p := secPart{Heading: l.Label("opening.tool"), Paras: []string{l.Label("opening.tool_note")}}
		item := secItem{Text: o.Tool + " · " + o.RuleID, Link: SectionStaticAnalysis}
		item.addCitations([]sourceCitation{v.citeSource(o.Location.Path, o.Location.StartLine, o.Location.EndLine, nil)}, false)
		p.List = append(p.List, item)
		if o.RawMessage != nil {
			p.Paras = append(p.Paras, *o.RawMessage)
		}
		return p
	}
	return secPart{}
}
