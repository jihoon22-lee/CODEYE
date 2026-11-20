package report

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// projectDecision is a reading of verified links within the stored report.
// Rendering does not turn evidence coverage or a candidate into a verdict.
type projectDecision struct {
	Tone, State, Title, Reason, Boundary, Next, Coverage string
	DocumentClaim, ReasonLabel, Condition                string
	Sources                                              []sourceCitation
	Glossary                                             []reportTerm
	Review                                               bool
}

func (v secView) projectDecision() projectDecision {
	d := v.selectDecision()
	if !v.R.Export.IncludeCode && d.Review {
		d.Title, d.Reason, d.Boundary, d.Next = v.L.Label("decision.omitted"), "", "", ""
		d.DocumentClaim, d.Condition = "", ""
	}
	if n := len(v.R.Documents.Comparisons); n > 0 {
		unknown := 0
		for _, comparison := range v.R.Documents.Comparisons {
			if comparison.Status == "unknown" {
				unknown++
			}
		}
		if unknown > 0 {
			if unknown == n {
				d.Coverage = tf(v.L, "decision.comparison_all_unknown", n)
			} else {
				d.Coverage = tf(v.L, "decision.comparison_some_unknown", n, unknown)
			}
		}
	}
	d.Glossary = decisionGlossary(d)
	return d
}

func (v secView) selectDecision() projectDecision {
	l, r := v.L, v.R
	doc, review := r.Documents, r.CodeReview

	var action, pending *Finding
	for i := range r.Findings {
		f := &r.Findings[i]
		if !slices.Contains(review.FindingIDs, f.ID) || hiddenDisposition[f.Disposition] || !hasProductFindingEvidence(*f) ||
			openingText(f.Title, "") == "" {
			continue
		}
		switch f.Validation {
		case ValidationReproduced, ValidationSupported:
			if action == nil || strongerFinding(f, action) {
				action = f
			}
		case ValidationInconclusive, ValidationUnreviewed:
			if (firstOpeningText(f.Verification) != "" || !r.Export.IncludeCode && len(f.Verification) > 0) &&
				(pending == nil || strongerFinding(f, pending)) {
				pending = f
			}
		}
	}
	if action != nil {
		return v.findingDecision(*action, false)
	}

	var match, unresolved, documented *DocComparison
	for _, status := range []string{"mismatch", "partial", "match", "unknown"} {
		for i := range doc.Comparisons {
			c := &doc.Comparisons[i]
			if c.Status != status || !v.hasReviewText(c.Statement) ||
				!hasProductSectionEvidence(c.DocumentEvidenceIDs, doc.Evidence, "document") {
				continue
			}
			code := hasProductSectionEvidence(c.CodeEvidenceIDs, doc.Evidence, "code")
			if (status == "mismatch" || status == "partial") && code {
				return v.comparisonDecision(*c, true)
			}
			if status == "match" && code && match == nil {
				match = c
			}
			if status == "unknown" {
				if code && unresolved == nil {
					unresolved = c
				}
				if documented == nil {
					documented = c
				}
			}
		}
	}

	if pending != nil {
		d := v.findingDecision(*pending, true)
		return v.withRelatedStrength(d, *pending)
	}
	if match != nil {
		return v.comparisonDecision(*match, true)
	}
	if strength := v.strengthDecision(); strength.Title != "" {
		return strength
	}
	if unresolved != nil {
		return v.comparisonDecision(*unresolved, true)
	}
	if documented != nil {
		return v.comparisonDecision(*documented, false)
	}
	if tool := v.primaryDiagnostic(); tool.Title != "" {
		return tool
	}
	return projectDecision{Tone: "neutral", State: l.Label("decision.scope_only"), Title: l.Label("opening.no_highlights"), Reason: noDecisionReason(l, r.Coverage.AIReview)}
}

func noDecisionReason(l Locale, review AIReview) string {
	if review.Status == "not_requested" {
		return l.Label("opening.model.not_requested")
	}
	for _, task := range review.RequiredTasks {
		if task.Reason != nil {
			if reason := reasonText(l, *task.Reason); reason != *task.Reason {
				return reason
			}
		}
	}
	if review.Reason != nil {
		if reason := reasonText(l, *review.Reason); reason != *review.Reason {
			return reason
		}
	}
	return ""
}

// A controlled behavior and an unresolved path can share a subject without
// sharing a line range. Keep each claim on the location that substantiates it.
func (v secView) withRelatedStrength(d projectDecision, f Finding) projectDecision {
	review, l := v.R.CodeReview, v.L
	if !v.R.Export.IncludeCode {
		return d
	}
	for _, strength := range review.Strengths {
		if openingText(strength.Benefit, "") == "" || !sharedSubject(f.Title, strength.Title) {
			continue
		}
		for _, e := range review.Evidence {
			if e.Kind != "code" || !slices.Contains(strength.EvidenceIDs, e.ID) || !productLocation(e.Path, e.StartLine, e.EndLine) {
				continue
			}
			for _, candidate := range f.Evidence {
				if !productFindingEvidence(f, candidate) || candidate.Location.Path != e.Path {
					continue
				}
				d.Title, d.Boundary, d.State = strength.Benefit, f.Title, l.Label("decision.observed_and_pending")
				confirmed := v.citeSectionEvidence(e)
				confirmed.ActionLabel = l.Label("evidence.view_confirmed")
				loc := candidate.Location
				unconfirmed := v.citeEvidence("code", loc.Path, loc.StartLine, loc.EndLine, candidate.Excerpt)
				unconfirmed.ActionLabel = l.Label("evidence.view_pending")
				d.Sources = []sourceCitation{confirmed, unconfirmed}
				return d
			}
		}
	}
	return d
}

func sharedSubject(first, second string) bool {
	split := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }
	words := make(map[string]bool)
	for _, word := range strings.FieldsFunc(strings.ToLower(first), split) {
		if word = subjectWord(word); word != "" {
			words[word] = true
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(second), split) {
		if words[subjectWord(word)] {
			return true
		}
	}
	return false
}

func subjectWord(word string) string {
	if len([]rune(word)) < 2 || strings.IndexFunc(word, unicode.IsLetter) == -1 {
		return ""
	}
	switch word {
	case "코드", "구현", "동작", "보류", "기능", "확인", "오류", "실패", "처리", "검사", "결과", "때문", "조건", "일부", "검증", "관리", "분기", "경로", "예외", "반환", "발생", "가능", "필요", "있음", "없음",
		"code", "error", "errors", "failed", "failure", "failures", "check", "checks", "checking", "test", "during", "with", "the", "and", "when", "from", "not", "may", "can", "does", "is", "in", "on", "to", "of", "or",
		"for", "after", "before", "handle", "handling", "result", "state", "case", "path", "paths":
		return ""
	}
	return word
}

func strongerFinding(a, b *Finding) bool {
	if sevRank(a.Severity) != sevRank(b.Severity) {
		return sevRank(a.Severity) > sevRank(b.Severity)
	}
	return a.Validation == ValidationReproduced && b.Validation != ValidationReproduced
}

func firstOpeningText(values []string) string {
	for _, s := range values {
		if text := openingText(s, ""); text != "" {
			return text
		}
	}
	return ""
}

var verificationOutcome = regexp.MustCompile(`목록|복원|출력|표시|응답|반환값|로그|(?i:\b(list|restore|output|display|response|return value|logs?)\b)`)
var verificationContract = regexp.MustCompile(`호출자|계약|(?i:\b(caller|contract)\b)`)

func readerVerification(values []string) string {
	for _, s := range values {
		if s = openingText(s, ""); verificationOutcome.MatchString(s) && !verificationContract.MatchString(s) {
			return s
		}
	}
	return firstOpeningText(values)
}

func (v secView) findingDecision(f Finding, pending bool) projectDecision {
	l := v.L
	d := projectDecision{Tone: "action", State: lab(l, "validation", f.Validation), Title: f.Title,
		Reason: openingText(f.Impact, ""), Next: openingText(f.Recommendation.Summary, ""), Review: true}
	if d.Reason == "" {
		for _, c := range f.Conditions {
			if d.Reason = openingText(c.Statement, ""); d.Reason != "" {
				break
			}
		}
	}
	if pending {
		d.Tone, d.State = "pending", l.Label("decision.candidate")
		// A candidate's predicted impact is not a fact the renderer can
		// state unqualified. The stored verification still makes it useful.
		d.Reason = ""
		d.Next = readerVerification(f.Verification)
	}
	for _, e := range f.Evidence {
		if productFindingEvidence(f, e) {
			loc := e.Location
			d.Sources = []sourceCitation{v.citeEvidence("code", loc.Path, loc.StartLine, loc.EndLine, e.Excerpt)}
			break
		}
	}
	return d
}

func (v secView) comparisonDecision(c DocComparison, code bool) projectDecision {
	l, evidence := v.L, v.R.Documents.Evidence
	tone := "pending"
	if c.Status == "match" {
		tone = "strength"
	}
	d := projectDecision{Tone: tone, State: l.Label("decision.comparison." + c.Status), Title: c.Statement,
		Reason: openingText(c.Reason, ""), Review: true}
	if c.Status == "unknown" {
		d.Title = l.Label("decision.document_claim")
		d.DocumentClaim = c.Statement
		d.ReasonLabel = l.Label("decision.unconfirmed_reason")
	}
	if !code {
		d.State = l.Label("opening.implementation_unknown")
	}
	if doc := bestProductEvidence(c.DocumentEvidenceIDs, evidence, "document"); doc != nil {
		d.Sources = append(d.Sources, v.citeSectionEvidence(*doc))
	}
	if code {
		if implementation := bestProductEvidence(c.CodeEvidenceIDs, evidence, "code"); implementation != nil {
			d.Sources = append(d.Sources, v.citeSectionEvidence(*implementation))
			if c.Status == "match" && v.R.Export.IncludeCode {
				d.Condition = v.matchingCondition(c, *implementation)
				if d.Condition != "" {
					d.Reason = ""
				}
			}
		}
	}
	return d
}

var namedDocumentOperation = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]{2,})\s*\(`)
var explicitDocumentOption = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]{2,})\x60?(?:가|이|를|을|은|는)?\s*(?:={1,3}\s*(?:true|false)\b|(?:is\s+)?(?:enabled|disabled)\b|활성화|비활성화|참이면|거짓이면)`)

// Both documented rules must identify the same callable and cite the exact
// implementation range. A second rule adds a named configuration condition.
func (v secView) matchingCondition(first DocComparison, source SectionEvidence) string {
	functions := namedDocumentOperation.FindAllStringSubmatch(first.Statement, -1)
	if len(functions) == 0 {
		return ""
	}
	doc := v.R.Documents
	for _, other := range doc.Comparisons {
		if other.Status != "match" || other.AppliesTo != first.AppliesTo || other.Statement == first.Statement ||
			!hasProductSectionEvidence(other.DocumentEvidenceIDs, doc.Evidence, "document") {
			continue
		}
		code := bestProductEvidence(other.CodeEvidenceIDs, doc.Evidence, "code")
		if code == nil || code.Path != source.Path || code.StartLine != source.StartLine || code.EndLine != source.EndLine {
			continue
		}
		calls := namedDocumentOperation.FindAllStringSubmatch(other.Statement, -1)
		if !slices.ContainsFunc(functions, func(a []string) bool {
			return slices.ContainsFunc(calls, func(b []string) bool { return a[1] == b[1] })
		}) {
			continue
		}
		for _, option := range explicitDocumentOption.FindAllStringSubmatch(other.Statement, -1) {
			if !strings.Contains(first.Statement, option[1]) {
				return other.Statement
			}
		}
	}
	return ""
}

func (v secView) strengthDecision() projectDecision {
	review := v.R.CodeReview
	type locatedStrength struct {
		title, benefit string
		evidence       *SectionEvidence
	}
	var items []locatedStrength
	for _, s := range review.Strengths {
		e := bestProductEvidence(s.EvidenceIDs, review.Evidence, "code")
		if e != nil && v.hasReviewText(s.Title) && v.hasReviewText(s.Benefit) {
			items = append(items, locatedStrength{s.Title, s.Benefit, e})
		}
	}
	// A shared, overlapping code location can substantiate one decision
	// about related controls. Shared module scope alone is not a connection.
	for i, first := range items {
		for _, second := range items[i+1:] {
			a, b := first.evidence, second.evidence
			if a.Path != b.Path || first.title == second.title || first.benefit == second.benefit || !sharedSubject(first.title, second.title) {
				continue
			}
			if b.StartLine <= a.StartLine && b.EndLine >= a.EndLine {
				a, b = b, a
			}
			if a.StartLine <= b.StartLine && a.EndLine >= b.EndLine {
				return projectDecision{Tone: "strength", State: v.L.Label("decision.preserve"),
					Title:  tf(v.L, "decision.related", first.title, second.title),
					Reason: first.benefit + " " + second.benefit, Sources: []sourceCitation{v.citeSectionEvidence(*a)}, Review: true}
			}
		}
	}
	if len(items) == 0 {
		return projectDecision{}
	}
	first := items[0]
	return projectDecision{Tone: "strength", State: v.L.Label("decision.preserve"), Title: first.title,
		Reason: first.benefit, Sources: []sourceCitation{v.citeSectionEvidence(*first.evidence)}, Review: true}
}

func (v secView) hasReviewText(s string) bool {
	return openingText(s, "") != "" || !v.R.Export.IncludeCode && s == textOmitted
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

func (v secView) comparisonDetails(c DocComparison, evidence []SectionEvidence) []secItem {
	l := v.L
	var items []secItem
	if reason := openingText(c.Reason, ""); reason != "" {
		label := "opening.comparison_reason"
		if c.Status == "unknown" {
			label = "opening.comparison_unknown_reason"
		}
		items = append(items, secItem{Label: l.Label(label), Text: reason})
	}
	if sources := v.sourceCitations(append(slices.Clone(c.DocumentEvidenceIDs), c.CodeEvidenceIDs...), evidence); len(sources) > 0 {
		items = append(items, secItem{Sources: sources})
	}
	return items
}

func nonempty(s string) bool { return strings.TrimSpace(s) != "" }

func openingText(s, fallback string) string {
	if !nonempty(s) || s == textOmitted {
		return fallback
	}
	return s
}

func hasProductSectionEvidence(ids []string, evidence []SectionEvidence, kind string) bool {
	return bestProductEvidence(ids, evidence, kind) != nil
}

func bestProductEvidence(ids []string, evidence []SectionEvidence, kind string) *SectionEvidence {
	for i := range evidence {
		e := &evidence[i]
		if e.Kind == kind && slices.Contains(ids, e.ID) && productLocation(e.Path, e.StartLine, e.EndLine) {
			return e
		}
	}
	return nil
}

func hasProductFindingEvidence(f Finding) bool {
	return slices.ContainsFunc(f.Evidence, func(e Evidence) bool { return productFindingEvidence(f, e) })
}

func productFindingEvidence(f Finding, e Evidence) bool {
	if e.Kind != "code" || e.Location == nil || slices.Contains(f.CounterevidenceIDs, e.ID) ||
		!productLocation(e.Location.Path, e.Location.StartLine, e.Location.EndLine) {
		return false
	}
	return slices.ContainsFunc(f.Claims, func(c Claim) bool { return slices.Contains(c.EvidenceIDs, e.ID) }) ||
		slices.ContainsFunc(f.Conditions, func(c Condition) bool { return slices.Contains(c.EvidenceIDs, e.ID) })
}

func productLocation(file string, start, end int) bool {
	if file == "" || start < 1 || end < start || secondaryPath(file) {
		return false
	}
	for _, dir := range strings.Split(strings.ToLower(path.Dir(file)), "/") {
		switch dir {
		case "test", "tests", "__tests__", "spec", "specs", "fuzz", "fuzzer", "fuzzers", "fuzzing":
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

func (v secView) primaryDiagnostic() projectDecision {
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
		return projectDecision{Tone: "neutral", State: l.Label("opening.tool"), Title: o.Tool + " · " + o.RuleID,
			Reason:  openingText(optText(o.RawMessage, ""), ""),
			Sources: []sourceCitation{v.citeEvidence("code", o.Location.Path, o.Location.StartLine, o.Location.EndLine, nil)}}
	}
	return projectDecision{}
}
