package report

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// projectDecision is a reading of verified links within the stored report.
// Rendering does not turn evidence coverage or a candidate into a verdict.
type projectDecision struct {
	Tone, State, Title, Reason, Boundary, Next   string
	Limitation                                   string
	DocumentClaim, ReasonLabel, Condition, Scope string
	ComparisonAnchor, ComparisonExample          string
	ComparisonHeading                            string
	ComparisonCounts                             []countRow
	ComparisonRows                               []secItem
	Sources                                      []sourceCitation
	Action                                       *projectAction
	Followup                                     bool
	AdditionalObservation                        bool
	Glossary                                     []reportTerm
	Review                                       bool
}

type projectAction struct {
	Title, State, Limitation string
	Source                   sourceCitation
}

func (v secView) projectDecision() projectDecision {
	assessment, representative, counts := v.documentOverview()
	d := v.selectDecision(assessment, representative)
	d.ComparisonCounts = counts
	d.ComparisonHeading = v.L.Label("decision.document_assessment")
	if n := len(v.R.Documents.Comparisons); n > 0 {
		d.ComparisonHeading = tf(v.L, "decision.assessment_total", n)
	}
	if !v.R.Export.IncludeCode && d.Review {
		d.Title, d.Reason, d.Boundary, d.Next = v.L.Label("decision.omitted"), "", "", ""
		d.DocumentClaim, d.Condition, d.Limitation = "", "", ""
		if d.Action != nil {
			d.Action.Title, d.Action.Limitation = "", ""
		}
	}
	d.Glossary = decisionGlossary(d)
	return d
}

// Count the stored comparisons. The example needs a product document and
// any resolved conclusion needs product code.
func (v secView) documentOverview() (assessment, representative int, counts []countRow) {
	doc := v.R.Documents
	assessment, representative = -1, -1
	for _, status := range []string{"mismatch", "partial", "match", "unknown"} {
		var tied []int
		n, bestRank := 0, -1
		for i, c := range doc.Comparisons {
			if c.Status != status {
				continue
			}
			n++
			if !v.hasReviewText(c.Statement) || !productLocation(path.Join(c.AppliesTo, "scope"), 1, 1) ||
				!hasProductSectionEvidence(c.DocumentEvidenceIDs, doc.Evidence, "document") {
				continue
			}
			additional := additionalPrimaryCode(c, doc.Evidence) != nil
			code := additional || hasProductSectionEvidence(c.CodeEvidenceIDs, doc.Evidence, "code")
			if status != "unknown" && !code {
				continue
			}
			rank := 0
			if code {
				rank = 1
			}
			if c.Followup || additional {
				rank = 2
			}
			if rank > bestRank {
				bestRank = rank
				tied = tied[:0]
			}
			if rank == bestRank {
				tied = append(tied, i)
			}
		}
		if n > 0 {
			counts = append(counts, countRow{Name: status, Label: v.L.Label("decision.count." + status), Value: tf(v.L, "decision.count_items", n)})
			if assessment < 0 && len(tied) > 0 {
				assessment, representative = tied[0], tied[0]
				if status == "unknown" && len(tied) > 1 && v.R.Export.IncludeCode {
					assessment = questionComparison(doc, tied)
				}
			}
		}
	}
	return assessment, representative, counts
}

func (v secView) selectDecision(assessment, representative int) projectDecision {
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
	if assessment >= 0 && doc.Comparisons[assessment].Status != "unknown" {
		d := v.comparisonDecision(doc.Comparisons[assessment], true)
		d.ComparisonAnchor = comparisonAnchor(assessment)
		if action != nil && sevRank(action.Severity) >= sevRank("high") {
			risk := v.findingDecision(*action, false)
			d.Action = &projectAction{Title: risk.Title, State: risk.State, Limitation: risk.Limitation, Source: risk.Sources[0]}
		}
		return d
	}
	withExample := func(d projectDecision) projectDecision {
		if representative >= 0 {
			d.ComparisonAnchor = comparisonAnchor(representative)
			if r.Export.IncludeCode {
				d.ComparisonExample = doc.Comparisons[representative].Statement
			}
		}
		return d
	}
	if action != nil {
		return withExample(v.findingDecision(*action, false))
	}
	if assessment >= 0 {
		if strength := v.strengthDecision(); strength.Title != "" {
			return withExample(strength)
		}
		c := doc.Comparisons[assessment]
		d := v.comparisonDecision(c, hasProductSectionEvidence(c.CodeEvidenceIDs, doc.Evidence, "code"))
		d.ComparisonAnchor = comparisonAnchor(assessment)
		return d
	}

	if pending != nil {
		d := v.findingDecision(*pending, true)
		return v.withRelatedStrength(d, *pending)
	}
	if strength := v.strengthDecision(); strength.Title != "" {
		return strength
	}
	if tool := v.primaryDiagnostic(); tool.Title != "" {
		return tool
	}
	return projectDecision{Tone: "neutral", State: l.Label("decision.scope_only"), Title: l.Label("opening.no_highlights"), Reason: noDecisionReason(l, r.Coverage.AIReview)}
}

// A question can break a tie only when its scope, cited document and subject
// identify one of the tied comparisons. Retain the published order otherwise.
func questionComparison(docs *DocumentsSection, tied []int) int {
	for _, question := range docs.Questions {
		matched := -1
		for _, i := range tied {
			row := docs.Comparisons[i]
			if row.AppliesTo != question.AppliesTo || !sharedSubject(row.Statement, question.Question) ||
				!slices.ContainsFunc(docs.Evidence, func(e SectionEvidence) bool {
					return e.Kind == "document" && productLocation(e.Path, e.StartLine, e.EndLine) &&
						slices.Contains(row.DocumentEvidenceIDs, e.ID) && slices.Contains(question.EvidenceIDs, e.ID)
				}) {
				continue
			}
			if matched >= 0 {
				matched = -1
				break
			}
			matched = i
		}
		if matched >= 0 {
			return matched
		}
	}
	return tied[0]
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
				d.Title, d.Boundary, d.State = strength.Title, f.Title, l.Label("decision.observed_and_pending")
				d.Reason = distinctStrengthDetails([]string{strength.Title}, strength.Benefit)
				d.Scope = displayDocumentScope(strength.Scope)
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
	case "코드", "구현", "동작", "보류", "기능", "확인", "확인한다", "오류", "실패", "처리", "검사", "결과", "때문", "조건", "일부", "검증", "관리", "분기", "경로", "예외", "반환", "발생", "가능", "필요", "있음", "없음",
		"문서", "범위", "지원", "버전", "환경", "여부", "사용", "실제", "설정",
		"code", "error", "errors", "failed", "failure", "failures", "check", "checks", "checking", "test", "during", "with", "the", "and", "when", "from", "not", "may", "can", "does", "is", "in", "on", "to", "of", "or",
		"for", "after", "before", "handle", "handling", "result", "state", "case", "path", "paths",
		"document", "documentation", "support", "supported", "scope", "version", "environment", "actual", "configuration":
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

func findingLimitation(f Finding) string {
	if f.Validation == ValidationSupported || f.Validation == ValidationInconclusive {
		for _, note := range f.Limitations {
			if text := openingText(note, ""); text != "" && !record.ValidCode(text) {
				return text
			}
		}
	}
	return ""
}

func (v secView) findingDecision(f Finding, pending bool) projectDecision {
	l := v.L
	d := projectDecision{Tone: "action", State: lab(l, "validation", f.Validation), Title: f.Title,
		Reason: openingText(f.Impact, ""), Next: openingText(f.Recommendation.Summary, ""), Limitation: findingLimitation(f), Review: true}
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
			if d.Tone == "action" {
				switch f.Validation {
				case ValidationSupported:
					d.State = l.Label("decision.supported")
				case ValidationReproduced:
					d.State = l.Label("decision.reproduced")
				}
			}
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
		Reason: openingText(c.Reason, ""), Scope: displayDocumentScope(c.AppliesTo), Review: true, Followup: c.Followup}
	document := bestProductEvidence(c.DocumentEvidenceIDs, evidence, "document")
	additional := additionalPrimaryCode(c, evidence)
	if c.Status == "unknown" && v.R.Export.IncludeCode && openingText(c.Statement, "") != "" {
		codeRefs := c.CodeEvidenceIDs
		if additional != nil && openingText(c.AdditionalObservation.Reason, "") != "" {
			d.AdditionalObservation = true
			d.Reason = c.AdditionalObservation.Reason
			codeRefs = c.AdditionalObservation.CodeEvidenceIDs
		}
		if d.Reason != "" {
			d.Title = l.Label("decision.document_comparison")
			if !d.AdditionalObservation {
				d.State = l.Label("decision.code_evidence_missing")
			}
			docRow := secItem{Label: l.Label("decision.row_document"), Text: c.Statement}
			if document != nil {
				docRow.Sources = []sourceCitation{v.citeDecisionDocument(*document, c.Statement)}
			}
			d.ComparisonRows = []secItem{docRow, {Label: l.Label("decision.row_code"), Text: d.Reason, Sources: v.sourceCitations(codeRefs, evidence)}}
			d.Reason = ""
			return d
		}
	}
	if c.Status == "unknown" {
		d.Title = l.Label("decision.document_claim")
		d.DocumentClaim = c.Statement
		d.ReasonLabel = l.Label("decision.unconfirmed_reason")
	}
	if document != nil {
		d.Sources = append(d.Sources, v.citeDecisionDocument(*document, c.Statement))
	}
	if additional != nil {
		d.AdditionalObservation = true
		d.Reason = openingText(c.AdditionalObservation.Reason, "")
		d.ReasonLabel = l.Label("decision.additional_observation")
		d.Sources = append(d.Sources, v.sourceCitations(c.AdditionalObservation.CodeEvidenceIDs, evidence)...)
		return d
	}
	if code {
		if implementation := bestProductEvidence(c.CodeEvidenceIDs, evidence, "code"); implementation != nil {
			d.Sources = append(d.Sources, v.citeSectionEvidence(*implementation))
			if c.Status == "match" && v.R.Export.IncludeCode {
				if condition := v.matchingCondition(c, *implementation); condition != nil {
					d.Condition = condition.Statement
					if doc := bestProductEvidence(condition.DocumentEvidenceIDs, evidence, "document"); doc != nil {
						source := v.citeSectionEvidence(*doc)
						if !slices.ContainsFunc(d.Sources, func(e sourceCitation) bool { return e.Example != nil }) {
							source = v.citeDecisionDocument(*doc, condition.Statement)
						}
						index := slices.IndexFunc(d.Sources, func(e sourceCitation) bool { return e.Location == source.Location })
						if index < 0 {
							d.Sources = append(d.Sources, source)
						} else if source.Example != nil && source.Text == d.Sources[index].Text {
							d.Sources[index] = source
						}
					}
				}
			}
		}
	}
	return d
}

func (v secView) citeDecisionDocument(e SectionEvidence, statement string) sourceCitation {
	source := v.citeSectionEvidence(e)
	if v.R.Export.IncludeCode {
		source.Example = citedDocumentExample(statement, &e)
		source.ExampleLabel = v.L.Label("decision.document_example")
	}
	return source
}

func additionalPrimaryCode(c DocComparison, evidence []SectionEvidence) *SectionEvidence {
	if c.AdditionalObservation != nil {
		return bestProductEvidence(c.AdditionalObservation.CodeEvidenceIDs, evidence, "code")
	}
	return nil
}

var backtickRuns = regexp.MustCompile("`+")

// inlineCodeParts preserves unmatched delimiters as text. Only two single
// backticks delimit a code span and all parts remain ordinary text.
func inlineCodeParts(s string) []secCell {
	var parts []secCell
	start, open := 0, -1
	for _, run := range backtickRuns.FindAllStringIndex(s, -1) {
		if run[1]-run[0] != 1 {
			continue
		}
		if open < 0 {
			open = run[0]
			continue
		}
		parts = append(parts, secCell{Text: s[start:open]}, secCell{Text: s[open+1 : run[0]], Code: true})
		start, open = run[1], -1
	}
	return append(parts, secCell{Text: s[start:]})
}

var namedDocumentOperation = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]{2,})\s*\(`)
var explicitDocumentOption = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]{2,})\x60?(?:가|이|를|을|은|는)?\s*(?:={1,3}\s*(?:true|false)\b|(?:is\s+)?(?:enabled|disabled)\b|활성화|비활성화|참이면|거짓이면)`)

// Both documented rules must identify the same callable and cite the exact
// implementation range. A second rule adds a named configuration condition.
func (v secView) matchingCondition(first DocComparison, source SectionEvidence) *DocComparison {
	functions := namedDocumentOperation.FindAllStringSubmatch(first.Statement, -1)
	if len(functions) == 0 {
		return nil
	}
	doc := v.R.Documents
	for i := range doc.Comparisons {
		other := &doc.Comparisons[i]
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
				return other
			}
		}
	}
	return nil
}

func (v secView) strengthDecision() projectDecision {
	review := v.R.CodeReview
	type locatedStrength struct {
		title, benefit, scope string
		evidence              *SectionEvidence
	}
	var items []locatedStrength
	for _, s := range review.Strengths {
		e := bestProductEvidence(s.EvidenceIDs, review.Evidence, "code")
		if e != nil && v.hasReviewText(s.Title) && v.hasReviewText(s.Benefit) {
			items = append(items, locatedStrength{s.Title, s.Benefit, s.Scope, e})
		}
	}
	// A shared, overlapping code location can substantiate one decision
	// about related controls. Shared module scope alone is not a connection.
	for i, first := range items {
		for _, second := range items[i+1:] {
			a, b := first.evidence, second.evidence
			if a.Path != b.Path || strengthTextKey(first.title) == strengthTextKey(second.title) ||
				strengthTextKey(first.benefit) == strengthTextKey(second.benefit) || !sharedSubject(first.title, second.title) {
				continue
			}
			if b.StartLine <= a.StartLine && b.EndLine >= a.EndLine {
				a, b = b, a
			}
			if a.StartLine <= b.StartLine && a.EndLine >= b.EndLine {
				scope := ""
				if first.scope == second.scope {
					scope = displayDocumentScope(first.scope)
				}
				return projectDecision{Tone: "strength", State: v.L.Label("decision.preserve"),
					Title:   tf(v.L, "decision.related", first.title, second.title),
					Reason:  distinctStrengthDetails([]string{first.title, second.title}, first.benefit, second.benefit),
					Sources: []sourceCitation{v.citeSectionEvidence(*a)}, Scope: scope, Review: true}
			}
		}
	}
	if len(items) == 0 {
		return projectDecision{}
	}
	first := items[0]
	return projectDecision{Tone: "strength", State: v.L.Label("decision.preserve"), Title: first.title,
		Reason:  distinctStrengthDetails([]string{first.title}, first.benefit),
		Sources: []sourceCitation{v.citeSectionEvidence(*first.evidence)}, Scope: displayDocumentScope(first.scope), Review: true}
}

func strengthTextKey(text string) string {
	return strings.TrimSpace(strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(text), " ")), ".。!?！？"))
}

// Keep the stored wording and suppress exact repeats with superficial edits.
func distinctStrengthDetails(titles []string, benefits ...string) string {
	seen := map[string]bool{"": true}
	for _, title := range titles {
		seen[strengthTextKey(title)] = true
	}
	var details []string
	for _, benefit := range benefits {
		key := strengthTextKey(benefit)
		if !seen[key] {
			details = append(details, benefit)
			seen[key] = true
		}
	}
	return strings.Join(details, " ")
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
	if scope := displayDocumentScope(c.AppliesTo); scope != "" {
		items = append(items, secItem{Label: l.Label("col.applies_to"), Text: scope})
	}
	if c.Followup {
		items = append(items, secItem{Text: l.Label("decision.followup")})
	}
	if reason := openingText(c.Reason, ""); reason != "" {
		label := "opening.comparison_reason"
		if c.Status == "unknown" {
			label = "opening.comparison_unknown_reason"
		}
		items = append(items, secItem{Label: l.Label(label), Text: reason})
	}
	priorRefs := slices.Clone(c.DocumentEvidenceIDs)
	for _, id := range c.CodeEvidenceIDs {
		if c.AdditionalObservation == nil || !slices.Contains(c.AdditionalObservation.CodeEvidenceIDs, id) {
			priorRefs = append(priorRefs, id)
		}
	}
	if sources := v.sourceCitations(priorRefs, evidence); len(sources) > 0 {
		items = append(items, secItem{Sources: sources})
	}
	if extra := c.AdditionalObservation; extra != nil {
		items = append(items, secItem{Label: l.Label("decision.additional_observation"), Text: openingText(extra.Reason, ""),
			Sources: v.sourceCitations(extra.CodeEvidenceIDs, evidence)})
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
		case "test", "tests", "__tests__", "spec", "specs", "e2e", "integration-tests", "unit-tests", "fuzz", "fuzzer", "fuzzers", "fuzzing":
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
	case ".sh", ".bash":
		return !strings.HasPrefix(base, "test_") && !strings.HasPrefix(base, "test-") && !strings.HasPrefix(base, "qa_") && !strings.HasPrefix(base, "qa-")
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
