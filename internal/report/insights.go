package report

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

type DocComparison struct {
	Statement             string                     `json:"statement"`
	AppliesTo             string                     `json:"applies_to"`
	Status                string                     `json:"status"`
	Reason                string                     `json:"reason"`
	DocumentEvidenceIDs   []string                   `json:"document_evidence_ids"`
	CodeEvidenceIDs       []string                   `json:"code_evidence_ids"`
	Followup              bool                       `json:"followup,omitempty"`
	AdditionalObservation *AdditionalCodeObservation `json:"additional_observation,omitempty"`
	RelatedWorkflow       *RelatedWorkflow           `json:"related_workflow,omitempty"`
}

type AdditionalCodeObservation struct {
	Reason          string   `json:"reason"`
	CodeEvidenceIDs []string `json:"code_evidence_ids"`
}

// RelatedWorkflow is context retained from the investigation request. Its
// citations do not count as a document verdict or a test execution.
type RelatedWorkflow struct {
	ReadEvidenceID   string `json:"read_evidence_id"`
	ChangeEvidenceID string `json:"change_evidence_id"`
	TestEvidenceID   string `json:"test_evidence_id"`
}

func (w RelatedWorkflow) evidenceIDs() [3]string {
	return [3]string{w.ReadEvidenceID, w.ChangeEvidenceID, w.TestEvidenceID}
}

func (c sectionContext) addComparisons(s *DocumentsSection, doc *record.Record) {
	if len(s.Contracts) == 0 {
		return
	}
	st := c.states["review.investigate"]
	byContract := map[int]record.ContractComparison{}
	catalog := map[string]bool{}
	for _, e := range s.Evidence {
		catalog[e.ID] = true
	}
	addEvidence := func(rec *record.Record, ids []string) {
		for _, e := range evidenceOf(rec, ids) {
			if !catalog[e.ID] {
				catalog[e.ID] = true
				s.Evidence = append(s.Evidence, e)
			}
		}
	}
	var followupIndex, observationIndex int
	var observation *AdditionalCodeObservation
	var reading *record.WorkflowReading
	if reviewAccepted(st) {
		rec := st.reviewResult.Record
		reading = rec.WorkflowReading
		if reading != nil {
			addEvidence(rec, reading.EvidenceIDs())
		}
		for _, x := range rec.ContractComparisons {
			byContract[x.ContractIndex] = x
		}
		final := c.states[record.TaskContract]
		if reviewAccepted(final) {
			result := final.reviewResult.Record
			if len(result.ContractComparisons) == 1 {
				x := result.ContractComparisons[0]
				if x.Status != "unknown" {
					followupIndex = x.ContractIndex
					byContract[followupIndex] = x
				} else if extra := additionalCodeObservation(x, result.Evidence, rec.Evidence); extra != nil {
					observationIndex, observation = x.ContractIndex, extra
				}
			}
		}
		for _, original := range rec.ContractComparisons {
			if original.ContractIndex == followupIndex {
				addEvidence(final.reviewResult.Record, byContract[followupIndex].CodeEvidenceIDs)
			} else {
				addEvidence(rec, original.CodeEvidenceIDs)
			}
		}
		if observation != nil {
			addEvidence(final.reviewResult.Record, observation.CodeEvidenceIDs)
		}
	}
	for i, x := range doc.Contracts {
		comparison := DocComparison{Statement: x.Statement, AppliesTo: x.AppliesTo, Status: "unknown",
			Reason:              "이 문서 계약에 대한 코드 대조 결과가 기록되지 않았습니다.",
			DocumentEvidenceIDs: slices.Clone(x.EvidenceIDs), CodeEvidenceIDs: []string{}}
		if x, ok := byContract[i+1]; ok {
			comparison.AppliesTo, comparison.Status, comparison.Reason = x.AppliesTo, x.Status, x.Reason
			comparison.CodeEvidenceIDs = slices.Clone(x.CodeEvidenceIDs)
			comparison.Followup = i+1 == followupIndex
			if i+1 == observationIndex {
				comparison.AdditionalObservation = observation
			}
		}
		if reading != nil && reading.ContractIndex == i+1 {
			comparison.RelatedWorkflow = &RelatedWorkflow{
				ReadEvidenceID: reading.ReadEvidenceID, ChangeEvidenceID: reading.ChangeEvidenceID, TestEvidenceID: reading.TestEvidenceID,
			}
		}
		s.Comparisons = append(s.Comparisons, comparison)
	}
}

// additionalCodeObservation preserves the final comparison's citations in
// reading order. At least one cited implementation must add a line.
func additionalCodeObservation(x record.ContractComparison, final, overview []record.Excerpt) *AdditionalCodeObservation {
	byID := make(map[string]record.Excerpt, len(final))
	for _, e := range final {
		byID[e.ID] = e
	}
	if !validAdditionalObservation(x, byID, overview) {
		return nil
	}
	return &AdditionalCodeObservation{Reason: x.Reason, CodeEvidenceIDs: slices.Clone(x.CodeEvidenceIDs)}
}

func validAdditionalObservation(x record.ContractComparison, catalog map[string]record.Excerpt, overview []record.Excerpt) bool {
	// Use the definitive comparison's scope, citation and implementation
	// checks. The stored decision keeps its unknown status.
	check := x
	check.Status = "partial"
	if !check.Valid(catalog) {
		return false
	}
	hasNewImplementation := false
	for _, id := range x.CodeEvidenceIDs {
		e := catalog[id]
		if e.Start < 1 || e.End < e.Start || e.End-e.Start >= 80 || record.SourceKind(e.Path) != e.Kind {
			return false
		}
		if !hasNewImplementation && e.Kind == "code" && freshObservationRange(e, overview) {
			check.CodeEvidenceIDs = []string{id}
			hasNewImplementation = check.Valid(catalog)
		}
	}
	return hasNewImplementation
}

func freshObservationRange(e record.Excerpt, overview []record.Excerpt) bool {
	if slices.ContainsFunc([]string{".d.ts", ".d.mts", ".d.cts", ".pyi"}, func(ext string) bool {
		return strings.HasSuffix(strings.ToLower(e.Path), ext)
	}) {
		return false
	}
	for offset := 0; offset <= e.End-e.Start; offset++ {
		line := e.Start + offset
		if !slices.ContainsFunc(overview, func(old record.Excerpt) bool {
			return (old.Kind == "code" || old.Kind == "test") && old.Path == e.Path && old.SHA256 == e.SHA256 && old.Start <= line && line <= old.End
		}) {
			return true
		}
	}
	return false
}

func (c sectionContext) addStrengths(s *CodeReviewSection) {
	seen := map[string]bool{}
	cited := map[string]bool{}
	for _, key := range reviewRecordKeys(c.states) {
		st := c.states[key]
		if !reviewAccepted(st) {
			continue
		}
		rec := st.reviewResult.Record
		var ids [][]string
		for _, strength := range rec.Strengths {
			if len(s.Strengths) == MaxReportedStrengths {
				break
			}
			title := strings.ToLower(strings.Join(strings.Fields(strength.Title), " "))
			implementation := make([]string, 0, len(strength.EvidenceIDs))
			for _, e := range rec.Evidence {
				if e.Kind == "code" && slices.Contains(strength.EvidenceIDs, e.ID) {
					implementation = append(implementation, e.ID)
				}
			}
			slices.Sort(implementation)
			evidence := strings.Join(implementation, "\x00")
			if seen["title\x00"+title] || seen["evidence\x00"+evidence] {
				continue
			}
			seen["title\x00"+title] = true
			seen["evidence\x00"+evidence] = true
			s.Strengths = append(s.Strengths, strength)
			ids = append(ids, strength.EvidenceIDs)
		}
		for _, e := range evidenceOf(rec, ids...) {
			if !cited[e.ID] {
				cited[e.ID] = true
				s.Evidence = append(s.Evidence, e)
			}
		}
	}
}

const MaxReportedStrengths = 12

func attachExcerpts(r *Report, snippets map[sourceexcerpt.Key][]sourceexcerpt.Excerpt, legacy bool) {
	budget := 256 << 10
	get := func(path, sha string, start, end int, model bool) *sourceexcerpt.Excerpt {
		windows := snippets[sourceexcerpt.Key{Path: path, SHA256: sha, Line: start}]
		choice := preferredExcerpt(windows, start, end, model, legacy)
		if choice == nil {
			return nil
		}
		cost := record.JSONLen(choice.Text)
		if cost > budget {
			return nil
		}
		budget -= cost
		return choice
	}
	withheld := map[string]bool{}
	for _, d := range r.Secrets.Detections {
		if d.Path != nil {
			withheld[*d.Path] = true
		}
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		model := usesModelExcerpts(r, f.ID)
		for j := range f.Evidence {
			e := &f.Evidence[j]
			if e.Location != nil && !withheld[e.Location.Path] {
				e.Excerpt = get(e.Location.Path, e.BlobSHA256, e.Location.StartLine, e.Location.EndLine, model)
			}
		}
	}
	for _, ev := range [][]SectionEvidence{r.Documents.Evidence, r.Environment.Evidence, r.CodeReview.Evidence} {
		for i := range ev {
			e := &ev[i]
			if !withheld[e.Path] {
				e.Excerpt = get(e.Path, e.BlobSHA256, e.StartLine, e.EndLine, true)
			}
		}
	}
	// Only excerpt windows already present in the validated evidence artifact
	// can appear here. Copies sharing an exact location reuse the first sample.
	used := map[sourceexcerpt.Key]bool{}
	metricWindow := func(l *ObservationLocation) *sourceexcerpt.Excerpt {
		if l == nil || withheld[l.Path] {
			return nil
		}
		key := sourceexcerpt.Key{Path: l.Path, SHA256: l.BlobSHA256, Line: l.StartLine}
		if used[key] {
			return nil
		}
		used[key] = true
		return get(l.Path, l.BlobSHA256, l.StartLine, l.EndLine, false)
	}
	for _, o := range r.Observations {
		if o.Metric == nil || o.EvidenceState != EvidenceVerified {
			continue
		}
		o.Metric.Excerpt = metricWindow(o.Location)
		o.Metric.CounterpartExcerpt = metricWindow(o.Metric.Counterpart)
	}
}

func usesModelExcerpts(r *Report, id string) bool {
	return r.CodeReview != nil && slices.Contains(r.CodeReview.FindingIDs, id)
}

func preferredExcerpt(windows []sourceexcerpt.Excerpt, start, end int, model, legacy bool) *sourceexcerpt.Excerpt {
	if len(windows) == 0 {
		return nil
	}
	if legacy {
		return &windows[0]
	}
	var forward, context *sourceexcerpt.Excerpt
	for _, window := range windows {
		if model {
			if x, ok := window.AtCitation(start, end); ok && (forward == nil || x.EndLine > forward.EndLine) {
				forward = &x
			}
		}
		if window.ValidDiagnostic(start) && window.StartLine >= start-3 && window.EndLine-start <= 5 &&
			(context == nil || window.EndLine-window.StartLine > context.EndLine-context.StartLine ||
				window.EndLine-window.StartLine == context.EndLine-context.StartLine && window.StartLine < context.StartLine) {
			context = &window
		}
	}
	if forward != nil {
		return forward
	}
	return context
}
