package record

import (
	"slices"
	"strings"
)

const (
	// TaskContract follows the existing whole-run roles and any review slices.
	TaskContract            = "review.contract"
	ContractMaxCalls        = 2
	MaxContractSliceSources = 4
)

type ContractPartialSupport struct {
	Matching  ContractConditionEvidence `json:"matching"`
	Violating ContractConditionEvidence `json:"violating"`
}

type ContractConditionEvidence struct {
	Condition  string `json:"condition"`
	EvidenceID string `json:"evidence_id"`
}

func validContractPartial(c ContractComparison, p *ContractPartialSupport, catalog map[string]Excerpt) bool {
	if c.Status != "partial" {
		return p == nil
	}
	if p == nil {
		return false
	}
	a, b := p.Matching, p.Violating
	first, second := catalog[a.EvidenceID], catalog[b.EvidenceID]
	if first.Path == second.Path &&
		((first.Start <= second.Start && second.End <= first.End) || (second.Start <= first.Start && first.End <= second.End)) {
		return false
	}
	return a.EvidenceID != b.EvidenceID && !strings.EqualFold(strings.TrimSpace(a.Condition), strings.TrimSpace(b.Condition)) &&
		insightText(a.Condition) && insightText(b.Condition) &&
		slices.Contains(c.CodeEvidenceIDs, a.EvidenceID) && slices.Contains(c.CodeEvidenceIDs, b.EvidenceID) &&
		first.Kind == "code" && SourceKind(first.Path) == "code" &&
		second.Kind == "code" && SourceKind(second.Path) == "code"
}

// ContractFollowupPrior selects the single earlier decision a final review may
// reconsider. An explicitly conflicting document in its scope blocks it.
func ContractFollowupPrior(documents, investigate Record) (ContractComparison, bool) {
	probe := investigate.ContractProbe
	if !documents.Accepted() || documents.Stage != StageDocuments || !investigate.Accepted() || investigate.Stage != StageInvestigate ||
		investigate.Slice != nil || probe == nil || documents.Snapshot != investigate.Snapshot || documents.RunID != investigate.RunID ||
		documents.AttemptID != investigate.AttemptID {
		return ContractComparison{}, false
	}
	for _, x := range investigate.ContractComparisons {
		if !probe.Valid(investigate.Snapshot, x) || !x.MatchesContract(documents.Contracts) {
			continue
		}
		for _, conflict := range documents.Conflicts {
			if !ScopeUnder(x.AppliesTo, conflict.Scope) && !ScopeUnder(conflict.Scope, x.AppliesTo) {
				continue
			}
			for _, id := range x.DocumentEvidenceIDs {
				if slices.Contains(conflict.FirstEvidence, id) || slices.Contains(conflict.SecondEvidence, id) {
					return ContractComparison{}, false
				}
			}
		}
		return x, true
	}
	return ContractComparison{}, false
}

// NewContractEvidenceLine requires a previously unexposed implementation line.
// A wider window with the same already-read lines plus an unrelated test
// cannot establish a new implementation comparison.
func NewContractEvidenceLine(e Excerpt, line int, overview []Excerpt) bool {
	return e.Kind == "code" && SourceKind(e.Path) == "code" && !slices.ContainsFunc(overview, func(old Excerpt) bool {
		return old.Kind == "code" && old.Path == e.Path && old.SHA256 == e.SHA256 && old.Start <= line && line <= old.End
	})
}

// HasNewContractEvidence checks whether a bounded source range includes a
// line outside the code windows read by the overview.
func HasNewContractEvidence(e Excerpt, overview []Excerpt) bool {
	if e.Start < 1 || e.End < e.Start || e.End-e.Start >= 80 {
		return false
	}
	for offset := 0; offset <= e.End-e.Start; offset++ {
		if NewContractEvidenceLine(e, e.Start+offset, overview) {
			return true
		}
	}
	return false
}

// ValidateContractFollowup links the single comparison to the original approved
// scope and document. Callers separately bind upstream digests to artifacts.
func ValidateContractFollowup(final, documents, investigate Record) bool {
	prior, ok := ContractFollowupPrior(documents, investigate)
	if !ok || !final.Accepted() || final.Stage != StageContract || len(final.ContractComparisons) != 1 ||
		final.Snapshot != documents.Snapshot || final.RunID != documents.RunID || final.AttemptID != documents.AttemptID {
		return false
	}
	x := final.ContractComparisons[0]
	if x.ContractIndex != prior.ContractIndex || x.AppliesTo != prior.AppliesTo || !slices.Equal(x.DocumentEvidenceIDs, prior.DocumentEvidenceIDs) {
		return false
	}
	byID := contractEvidence(final.Evidence)
	if !x.Valid(byID) || !validContractPartial(x, final.PartialSupport, byID) {
		return false
	}
	docs, old := contractEvidence(documents.Evidence), contractEvidence(investigate.Evidence)
	for _, id := range x.DocumentEvidenceIDs {
		e := byID[id]
		e.Text = ""
		if docs[id] != e || old[id] != e {
			return false
		}
	}
	if x.Status == "unknown" {
		return true
	}
	if x.Status == "partial" {
		return HasNewContractEvidence(byID[final.PartialSupport.Matching.EvidenceID], investigate.Evidence) ||
			HasNewContractEvidence(byID[final.PartialSupport.Violating.EvidenceID], investigate.Evidence)
	}
	return slices.ContainsFunc(x.CodeEvidenceIDs, func(id string) bool {
		return HasNewContractEvidence(byID[id], investigate.Evidence)
	})
}

func contractEvidence(list []Excerpt) map[string]Excerpt {
	out := make(map[string]Excerpt, len(list))
	for _, e := range list {
		out[e.ID] = e
	}
	return out
}

func checkContractUpstream(r Record, seen map[string]bool) error {
	if len(r.Upstream) > 3+MaxContractSliceSources || !seen["review.documents"] || !seen["review.investigate"] {
		return invalid("contract upstream")
	}
	hasSlice := false
	for _, u := range r.Upstream {
		if u.State != UpstreamAccepted {
			return invalid("contract upstream state")
		}
		if _, key, ok := SliceTask(u.Source); ok {
			hasSlice = true
			if !seen["review.investigate."+key] {
				return invalid("contract upstream slice")
			}
		}
	}
	if hasSlice != seen[TaskScope] {
		return invalid("contract upstream scope")
	}
	return nil
}

// ContractSliceEvidence reports whether a later accepted record contributed an
// actual code or test window. A verifier is also allowed to contribute via its
// own investigate dependency, which is required for the verifier's lineage.
func ContractSliceEvidence(final, later Record) bool {
	if later.Slice == nil || !later.Accepted() || len(later.Calls) == 0 {
		return false
	}
	for _, e := range later.Evidence {
		if (e.Kind == "code" || e.Kind == "test") && slices.Contains(final.Evidence, e) {
			return true
		}
	}
	return false
}
