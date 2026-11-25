package report

import (
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// decideStatus maps structured facts to the report status. final is the run's
// terminal status for a report-only publication (failed/cancelled).
func decideStatus(final string, readable, incomplete bool, ai string) string {
	switch {
	case final == StatusCancelled:
		return StatusCancelled
	case final == StatusFailed, !readable:
		return StatusFailed
	case incomplete || ai != AIReviewPerformed:
		return StatusPartial
	}
	return StatusCompleted
}

// DecideVerdict: the first matching rule wins. A clean verdict needs a
// completed report, performed AI review and no pending candidate.
func DecideVerdict(status string, s Summary, ai string) string {
	switch {
	case s.ActionableFindings > 0:
		return VerdictAttention
	case status != StatusCompleted, ai != AIReviewPerformed, s.PendingCandidates > 0, s.UnresolvedObservations > 0:
		return VerdictInconclusive
	}
	return VerdictNoActionable
}

// contractDifferences counts scoped comparisons with evidence of divergence.
func contractDifferences(d *DocumentsSection) int {
	count := 0
	if d != nil {
		for _, c := range d.Comparisons {
			if c.Status == "mismatch" || c.Status == "partial" {
				count++
			}
		}
	}
	return count
}

func reportVerdict(r Report) string {
	if contractDifferences(r.Documents) > 0 {
		return VerdictAttention
	}
	verdict := DecideVerdict(r.Status, r.Summary, r.Coverage.AIReview.Status)
	// A model candidate or hypothesis left out after the format repair
	// (its location could not be fixed) is not an absence of findings.
	if verdict == VerdictNoActionable && slices.ContainsFunc(r.Limitations, func(l string) bool {
		return l == record.LimCandidateOmitted || l == record.LimInterpretItemsOmitted
	}) {
		return VerdictInconclusive
	}
	if verdict == VerdictNoActionable && r.Documents != nil {
		for _, c := range r.Documents.Comparisons {
			if c.Status == "unknown" {
				return VerdictInconclusive
			}
		}
	}
	return verdict
}

var hiddenDisposition = map[string]bool{"false_positive": true, "suppressed": true, "accepted_risk": true, "fixed_by_user": true}

var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

// ValidationClass orders findings for every list that ranks or cuts them
// (docs/REPORT.md#검증-상태): reproduced 0, supported 1, a diagnostic triage
// problem not yet confirmed 2, any other unconfirmed finding (a tool finding,
// a model candidate) 3, refuted 4. Severity orders within a class only, so an
// unconfirmed high never pushes a confirmed medium out of a short list.
func ValidationClass(f Finding) int {
	switch f.Validation {
	case ValidationReproduced:
		return 0
	case ValidationSupported:
		return 1
	case ValidationUnreviewed, ValidationInconclusive:
		if triageFindingID.MatchString(f.ID) {
			return 2
		}
		return 3
	}
	return 4
}

// summaryClass is ValidationClass with reproduced and supported as one
// class: ValidateReport recomputes the stored summary, and the summaries of
// earlier builders ranked the two together by severity. The other classes
// keep the order those summaries had (a triage problem exists only from
// builder 22).
func summaryClass(f Finding) int {
	return max(1, ValidationClass(f))
}

// ComputeSummary is the single source of every count a format shows.
func ComputeSummary(findings []Finding, obs []Observation) Summary {
	s := Summary{TotalFindings: len(findings), RawObservations: len(obs), Priorities: []string{}}
	for _, o := range obs {
		if o.EvidenceState != EvidenceVerified {
			s.UnresolvedObservations++
		}
		if o.Metric != nil {
			s.MetricObservations++
		}
	}
	type cand struct {
		f    Finding
		rank int
	}
	var cands []cand
	for _, f := range findings {
		if hiddenDisposition[f.Disposition] {
			s.HiddenFindings++
		}
		switch f.Validation {
		case "supported", "reproduced":
			if FindingHypothesisNotShipped(f) {
				// Listed with its verified state, not an action item.
				continue
			}
			if !hiddenDisposition[f.Disposition] {
				s.ActionableFindings++
				cands = append(cands, cand{f, summaryClass(f)})
			}
		case "unreviewed", "inconclusive":
			if findingHeldBack(f) {
				// Listed, not pending: it was not verified by design, or it
				// is about diagnostics outside shipped code.
				continue
			}
			s.PendingCandidates++
			if !hiddenDisposition[f.Disposition] {
				cands = append(cands, cand{f, summaryClass(f)})
			}
		case "refuted":
			s.RefutedFindings++
		}
	}
	sort.SliceStable(cands, func(a, b int) bool {
		x, y := cands[a], cands[b]
		if x.rank != y.rank {
			return x.rank < y.rank
		}
		if severityRank[x.f.Severity] != severityRank[y.f.Severity] {
			return severityRank[x.f.Severity] < severityRank[y.f.Severity]
		}
		return findingKey(x.f) < findingKey(y.f)
	})
	for i := 0; i < len(cands) && i < 5; i++ {
		s.Priorities = append(s.Priorities, cands[i].f.ID)
	}
	return s
}
