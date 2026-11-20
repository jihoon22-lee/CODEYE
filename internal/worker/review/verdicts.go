package review

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type repairInput struct {
	OutputErrors []string     `json:"output_errors"`
	Candidates   []repairItem `json:"candidates"`
	AcceptedIDs  []string     `json:"accepted_evidence_ids"`
}

type repairItem struct {
	ID     string   `json:"candidate_id"`
	Errors []string `json:"errors"`
}

// modelVerification is the only shape a model may return. Unknown fields
// (paths, line numbers, execution IDs) make the item invalid.
type modelVerification struct {
	CandidateID           string   `json:"candidate_id"`
	Verdict               string   `json:"verdict"`
	Category              string   `json:"category"`
	Severity              string   `json:"severity"`
	SeverityReason        string   `json:"severity_reason"`
	Impact                string   `json:"impact"`
	ValidConditions       []string `json:"valid_conditions"`
	InvalidConditions     []string `json:"invalid_conditions"`
	SupportingEvidence    []string `json:"supporting_evidence_ids"`
	Counterevidence       []string `json:"counterevidence_ids"`
	UnresolvedAssumptions []string `json:"unresolved_assumptions"`
	NextVerification      []string `json:"next_verification"`
}

// Limitation and reason codes of the verify stage.
const (
	limNoCandidates     = "no_candidates_to_verify"
	reasonOutputInvalid = "review_output_invalid"
	reasonInputTooLarge = "review_input_too_large"
	callAccepted        = record.OutcomeAccepted
	callRejected        = "output_rejected"
	callDiscarded       = "result_discarded"
)

func addLim(list []string, codes ...string) []string {
	list = append(list, codes...)
	sort.Strings(list)
	return slices.Compact(list)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func unreviewed(id, lim string) Verification {
	return Verification{CandidateID: id, Verdict: record.VerdictUnreviewed, ValidConditions: []string{}, InvalidConditions: []string{},
		SupportingEvidence: []string{}, Counterevidence: []string{}, UnresolvedAssumptions: []string{}, NextVerification: []string{}, Limitations: []string{lim}}
}

// parseVerificationItems accepts each entry independently. Output-level
// errors and per-candidate error codes are fixed platform strings; no model
// text is echoed into a repair request. An entry whose only defect is
// citing, in its evidence lists, IDs the platform itself gave in the request
// as something other than evidence (droppable: the observation IDs) is
// accepted without them when what remains passes every check of its verdict
// on its own. Any other unknown ID keeps the entry rejected. Those
// candidates are returned in dropped.
func parseVerificationItems(raw json.RawMessage, want map[string]bool, catalog map[string]Excerpt, droppable map[string]bool) (map[string]Verification, map[string][]string, []string, []string) {
	got, itemErrs := map[string]Verification{}, map[string][]string{}
	var dropped []string
	var res struct {
		Verifications []json.RawMessage `json:"verifications"`
	}
	if domain.DecodeStrict(raw, &res) != nil || res.Verifications == nil {
		return got, itemErrs, []string{"output_schema_invalid"}, dropped
	}
	var outputErrs []string
	count := map[string]int{}
	heads := make([]string, len(res.Verifications))
	for i, item := range res.Verifications {
		var head struct {
			CandidateID string `json:"candidate_id"`
		}
		if json.Unmarshal(item, &head) != nil || !want[head.CandidateID] {
			outputErrs = addLim(outputErrs, "unknown_candidate_id")
			continue
		}
		heads[i] = head.CandidateID
		count[head.CandidateID]++
	}
	for i, item := range res.Verifications {
		id := heads[i]
		if id == "" {
			continue
		}
		if count[id] > 1 {
			itemErrs[id] = []string{"duplicate_candidate_verification"}
			continue
		}
		var m modelVerification
		if domain.DecodeStrict(item, &m) != nil {
			itemErrs[id] = []string{"item_schema_invalid"}
			continue
		}
		v, errs := checkModel(m, catalog)
		if len(droppable) > 0 && slices.Equal(errs, []string{"evidence_id_unknown"}) && onlyDroppable(m.SupportingEvidence, catalog, droppable) && onlyDroppable(m.Counterevidence, catalog, droppable) {
			known := m
			known.SupportingEvidence, known.Counterevidence = knownIDs(m.SupportingEvidence, catalog), knownIDs(m.Counterevidence, catalog)
			if kv, kerrs := checkModel(known, catalog); len(kerrs) == 0 {
				v, errs = kv, nil
				dropped = append(dropped, id)
			}
		}
		if len(errs) > 0 {
			itemErrs[id] = errs
		} else {
			got[id] = v
		}
	}
	return got, itemErrs, outputErrs, dropped
}

// onlyDroppable reports whether every ID of list outside catalog is droppable.
func onlyDroppable(list []string, catalog map[string]Excerpt, droppable map[string]bool) bool {
	for _, id := range list {
		if _, ok := catalog[id]; !ok && !droppable[id] {
			return false
		}
	}
	return true
}

// knownIDs keeps the IDs of list that are in catalog, in order.
func knownIDs(list []string, catalog map[string]Excerpt) []string {
	if list == nil {
		return nil
	}
	out := []string{}
	for _, id := range list {
		if _, ok := catalog[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

func checkModel(m modelVerification, catalog map[string]Excerpt) (Verification, []string) {
	v := Verification{CandidateID: m.CandidateID, Verdict: m.Verdict, Category: m.Category, Severity: m.Severity, SeverityReason: m.SeverityReason, Impact: m.Impact,
		ValidConditions: nonNil(m.ValidConditions), InvalidConditions: nonNil(m.InvalidConditions), SupportingEvidence: nonNil(m.SupportingEvidence),
		Counterevidence: nonNil(m.Counterevidence), UnresolvedAssumptions: nonNil(m.UnresolvedAssumptions), NextVerification: nonNil(m.NextVerification), Limitations: []string{}}
	var errs []string
	bad := func(code string) { errs = addLim(errs, code) }
	if m.ValidConditions == nil || m.InvalidConditions == nil || m.SupportingEvidence == nil || m.Counterevidence == nil || m.UnresolvedAssumptions == nil || m.NextVerification == nil {
		bad("item_schema_invalid")
	}
	if v.Verdict == "reproduced" {
		// Only a platform execution artifact can reproduce; verification has none.
		v.Verdict = record.VerdictInconclusive
		v.Limitations = []string{record.LimReproductionRejected}
	}
	switch v.Verdict {
	case record.VerdictSupported, record.VerdictRefuted, record.VerdictInconclusive:
	default:
		bad("verdict_invalid")
	}
	if !slices.Contains(record.Categories, v.Category) {
		bad("category_invalid")
	}
	if v.Severity == "critical" {
		bad("severity_not_assignable")
	} else if !slices.Contains(record.Severities, v.Severity) {
		bad("severity_invalid")
	}
	if !record.SafeText(v.SeverityReason) || !record.SafeText(v.Impact) {
		bad("text_invalid")
	}
	for _, list := range [][]string{v.ValidConditions, v.InvalidConditions, v.UnresolvedAssumptions, v.NextVerification} {
		if len(list) > 4 {
			bad("list_too_long")
		}
		for _, s := range list {
			if !record.SafeText(s) || len(s) > 600 {
				bad("text_invalid")
			}
		}
	}
	if len(v.NextVerification) == 0 || len(v.NextVerification) > 3 {
		bad("next_verification_invalid")
	}
	for _, list := range [][]string{v.SupportingEvidence, v.Counterevidence} {
		if len(list) > 8 {
			bad("evidence_list_too_long")
		}
		seen := map[string]bool{}
		for _, id := range list {
			if _, ok := catalog[id]; !ok {
				bad("evidence_id_unknown")
			}
			if seen[id] {
				bad("evidence_id_duplicate")
			}
			seen[id] = true
		}
	}
	switch v.Verdict {
	case record.VerdictSupported:
		code := false
		for _, id := range v.SupportingEvidence {
			k := catalog[id].Kind
			code = code || k == "code" || k == "test"
		}
		if !code {
			bad("supported_without_code_evidence")
		}
		if len(v.ValidConditions) == 0 {
			bad("valid_conditions_missing")
		}
	case record.VerdictRefuted:
		if !record.RefsOK(v.Counterevidence, catalog, "code", 8) {
			bad("refuted_without_counterevidence")
		}
		if len(v.InvalidConditions) == 0 {
			bad("invalid_conditions_missing")
		}
	case record.VerdictInconclusive:
		if len(v.UnresolvedAssumptions) == 0 && len(v.Limitations) == 0 {
			bad("unresolved_assumptions_missing")
		}
	}
	if len(errs) == 0 && record.CheckVerification(v, catalog) != nil {
		bad("verification_invalid")
	}
	if len(errs) == 0 && slices.Contains(v.Limitations, record.LimReproductionRejected) {
		// A model claiming execution supplies no trusted account of what happened.
		v.Severity = "info"
		v.SeverityReason = "실행 근거가 없어 영향을 확정하지 못했다."
		v.Impact = "실행 결과를 확인할 수 없다."
		v.ValidConditions, v.InvalidConditions, v.UnresolvedAssumptions = []string{}, []string{}, []string{}
		v.NextVerification = []string{"인용한 코드와 실제 호출 조건을 대조한다."}
	}
	return v, errs
}
