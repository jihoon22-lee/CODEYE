package report

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

type DocComparison struct {
	Statement           string   `json:"statement"`
	AppliesTo           string   `json:"applies_to"`
	Status              string   `json:"status"`
	Reason              string   `json:"reason"`
	DocumentEvidenceIDs []string `json:"document_evidence_ids"`
	CodeEvidenceIDs     []string `json:"code_evidence_ids"`
}

func (c sectionContext) addComparisons(s *DocumentsSection, doc *record.Record) {
	if len(s.Contracts) == 0 {
		return
	}
	st := c.states["review.investigate"]
	byContract := map[int]record.ContractComparison{}
	catalog := map[string]bool{}
	if reviewAccepted(st) {
		rec := st.reviewResult.Record
		var codeIDs [][]string
		for _, x := range rec.ContractComparisons {
			byContract[x.ContractIndex] = x
			codeIDs = append(codeIDs, x.CodeEvidenceIDs)
		}
		for _, e := range s.Evidence {
			catalog[e.ID] = true
		}
		for _, e := range evidenceOf(rec, codeIDs...) {
			if !catalog[e.ID] {
				catalog[e.ID] = true
				s.Evidence = append(s.Evidence, e)
			}
		}
	}
	for i, x := range doc.Contracts {
		comparison := DocComparison{Statement: x.Statement, AppliesTo: x.AppliesTo, Status: "unknown",
			Reason:              "이 문서 계약에 대한 코드 대조 결과가 기록되지 않았습니다.",
			DocumentEvidenceIDs: slices.Clone(x.EvidenceIDs), CodeEvidenceIDs: []string{}}
		if x, ok := byContract[i+1]; ok {
			comparison.AppliesTo, comparison.Status, comparison.Reason = x.AppliesTo, x.Status, x.Reason
			comparison.CodeEvidenceIDs = slices.Clone(x.CodeEvidenceIDs)
		}
		s.Comparisons = append(s.Comparisons, comparison)
	}
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
			if seen[strength.Title] {
				continue
			}
			seen[strength.Title] = true
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
