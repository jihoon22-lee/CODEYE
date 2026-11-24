package record

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Diagnostic triage (docs/REVIEW.md#도구-진단-분류). Every non-suppressed,
// non-metric, non-secret tool diagnostic is grouped (analyzer and rule
// within one review unit; tools reporting the same construct on the same
// line join one group) and the groups are packed into planned triage slots.
// A slot's triage record (review.triage.tNNN) holds the groups it sent and
// the classification the model gave each one; a group classified as a real
// problem is also a candidate (origin triage) the slot's verify record
// (review.verify.tNNN) checks. Both records are schema 0.8; every other
// record keeps its version.
const (
	SchemaVersionTriage = "0.8"
	StageTriage         = "triage"

	// Classifications of a triage group.
	TriageProblem  = "problem"
	TriageHarmless = "harmless"
	TriageUnclear  = "unclear"

	// MaxTriageGroups bounds the groups of one triage request and record;
	// MaxTriageObservations the observations a 0.8 record names and
	// MaxGroupObservations those of one group (a candidate links them all).
	MaxTriageGroups       = 64
	MaxTriageObservations = 1024
	MaxGroupObservations  = 256
	// MaxTriageSlots bounds the planned triage slots of a run.
	MaxTriageSlots = 24

	// Why a sent group has no classification: no acceptable answer after
	// the repair (an integrity fault: rejected, a fault of form: omitted),
	// no answer at all, or the request budget could not carry it.
	TriageItemRejected  = "triage_item_rejected"
	TriageItemOmitted   = "triage_item_omitted"
	TriageAnswerMissing = "triage_answer_missing"
	TriageInputTrimmed  = "triage_input_trimmed"

	// LimTriageGroupsNotTriaged marks a triage record with a sent group
	// left without a classification.
	LimTriageGroupsNotTriaged = "diagnostic_groups_not_triaged"
)

var (
	triageKey      = regexp.MustCompile(`^t(0[0-9][1-9]|0[1-9][0-9]|[1-9][0-9]{2})$`)
	triageGroupID  = regexp.MustCompile(`^g([1-9]|[1-5][0-9]|6[0-4])$`)
	triageCandidID = regexp.MustCompile(`^triage_([1-9]|[1-5][0-9]|6[0-4])$`)
)

// ValidTriageKey accepts t001 through t999.
func ValidTriageKey(k string) bool { return triageKey.MatchString(k) }

// TriageTask splits a triage slot task key (review.triage.t001,
// review.verify.t001) into its stage and slot key.
func TriageTask(key string) (stage, slot string, ok bool) {
	rest, found := strings.CutPrefix(key, "review.")
	if !found {
		return "", "", false
	}
	stage, slot, found = strings.Cut(rest, ".")
	if !found || (stage != StageTriage && stage != StageVerify) || !ValidTriageKey(slot) {
		return "", "", false
	}
	return stage, slot, true
}

// TriageGroup is one group of tool diagnostics a triage slot sent, with
// the model's classification when one was accepted. Statistics of the
// report come from these structured fields only.
type TriageGroup struct {
	ID             string   `json:"id"`
	ObservationIDs []string `json:"observation_ids"`
	// ContextIDs are the code units sent as the group's context.
	ContextIDs []string `json:"context_evidence_ids"`
	// CrossTool marks a group joining diagnostics of two or more analyzers
	// on one line whose rules are of one family (the tool findings stay
	// separate).
	CrossTool      bool   `json:"cross_tool,omitempty"`
	Classification string `json:"classification,omitempty"`
	// Omitted is the code of a group without a classification.
	Omitted string `json:"omitted,omitempty"`
	// Reason is why the group is harmless (the guard, caller, type or
	// test-only path) or why the input cannot tell; for a problem the
	// candidate's counterpoint holds it.
	Reason      string   `json:"reason,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	CandidateID string   `json:"candidate_id,omitempty"`
}

// TriageSlotRef is the slice reference of a 0.8 record: the slot key and
// no modules.
func TriageSlotRef(key string) *SliceRef { return &SliceRef{Key: key, Modules: []string{}} }

// checkTriageSlot validates the slot reference of a 0.8 record.
func checkTriageSlot(r Record, want Expect) error {
	s := r.Slice
	switch {
	case s == nil || s.Modules == nil || len(s.Modules) != 0:
		return invalid("triage record without its slot")
	case r.Stage != StageTriage && r.Stage != StageVerify:
		return invalid("triage slot stage")
	case !ValidTriageKey(s.Key) || s.Key != want.Slice:
		return invalid("triage slot key")
	}
	return nil
}

// checkTriage validates the groups and candidates of a 0.8 triage record:
// sequential group IDs, each observation in one group, every problem group
// bound to exactly one candidate with the same observations, and the
// classification rules on evidence and prose.
func checkTriage(r Record, catalog map[string]Excerpt, observations map[string]ObservationRef, b bounds) error {
	if r.Stage != StageTriage {
		if len(r.TriageGroups) != 0 {
			return invalid("triage groups outside a triage record")
		}
		return nil
	}
	if len(r.TriageGroups) > MaxTriageGroups || len(r.Limits) != 0 || len(r.Verifications) != 0 {
		return invalid("triage content")
	}
	if r.Outcome == OutcomeFailed && len(r.Candidates) != 0 {
		return invalid("failed triage with candidates")
	}
	used := map[string]bool{}
	candidates := map[string]Candidate{}
	for _, c := range r.Candidates {
		candidates[c.ID] = c
	}
	bound := map[string]bool{}
	for i, g := range r.TriageGroups {
		if g.ID != fmt.Sprintf("g%d", i+1) || !triageGroupID.MatchString(g.ID) {
			return invalid("triage group id")
		}
		if g.ObservationIDs == nil || len(g.ObservationIDs) == 0 || len(g.ObservationIDs) > MaxGroupObservations || g.ContextIDs == nil || len(g.ContextIDs) == 0 {
			return invalid("triage group %s lists", g.ID)
		}
		for _, id := range g.ObservationIDs {
			if _, ok := observations[id]; !ok || used[id] {
				return invalid("triage group %s observation", g.ID)
			}
			used[id] = true
		}
		if !idsOK(g.ContextIDs, catalog, 64) || !hasKind(g.ContextIDs, catalog, "code") {
			return invalid("triage group %s context", g.ID)
		}
		failed := r.Outcome == OutcomeFailed
		switch g.Classification {
		case "":
			if !ValidCode(g.Omitted) || g.Reason != "" || g.EvidenceIDs != nil || g.CandidateID != "" {
				return invalid("triage group %s without classification", g.ID)
			}
			continue
		case TriageProblem, TriageHarmless, TriageUnclear:
			if failed || g.Omitted != "" {
				return invalid("triage group %s classification", g.ID)
			}
		default:
			return invalid("triage group %s classification", g.ID)
		}
		if g.Classification == TriageProblem {
			c, ok := candidates[g.CandidateID]
			if !ok || bound[c.ID] || g.Reason != "" || g.EvidenceIDs != nil || !slices.Equal(c.ObservationIDs, g.ObservationIDs) {
				return invalid("triage group %s candidate", g.ID)
			}
			bound[c.ID] = true
			continue
		}
		if g.CandidateID != "" || !ProseText(g.Reason, b.text) {
			return invalid("triage group %s reason", g.ID)
		}
		switch {
		case g.Classification == TriageHarmless && !refs(g.EvidenceIDs, catalog, 6, "code", "test"):
			return invalid("triage group %s harmless evidence", g.ID)
		case g.Classification == TriageUnclear && g.EvidenceIDs != nil && !refs(g.EvidenceIDs, catalog, 6, "code", "test", "document"):
			return invalid("triage group %s evidence", g.ID)
		}
	}
	if len(bound) != len(r.Candidates) {
		return invalid("triage candidate without its group")
	}
	if len(used) != len(r.Observations) {
		return invalid("triage observation outside the groups")
	}
	return nil
}
