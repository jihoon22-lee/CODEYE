package report

import (
	"sort"
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

var hiddenDisposition = map[string]bool{"false_positive": true, "suppressed": true, "accepted_risk": true, "fixed_by_user": true}

var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

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
			if !hiddenDisposition[f.Disposition] {
				s.ActionableFindings++
				cands = append(cands, cand{f, 0})
			}
		case "unreviewed", "inconclusive":
			s.PendingCandidates++
			if !hiddenDisposition[f.Disposition] {
				cands = append(cands, cand{f, 1})
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
