package review

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// repairInput is verify's repair block: the rejected candidates with their
// codes and the fields at fault, the IDs citable alone and the excerpts
// citable only with a line range.
type repairInput struct {
	OutputErrors  []string        `json:"output_errors"`
	Candidates    []repairItem    `json:"candidates"`
	AcceptedIDs   []string        `json:"accepted_evidence_ids"`
	RangeRequired []rangeRequired `json:"range_required_evidence,omitempty"`
}

type repairItem struct {
	ID     string        `json:"candidate_id"`
	Errors []string      `json:"errors"`
	Issues []verifyIssue `json:"issues,omitempty"`
}

// verifyIssue is one fault of a verification: the field it concerns, the
// sent excerpt a citation fault names (an ID the request did not hold is
// never echoed) and the code. No model text.
type verifyIssue struct {
	Field string `json:"field"`
	ID    string `json:"evidence_id,omitempty"`
	Error string `json:"error"`
}

// verdictFields are the fields a verdict-rule code concerns when it names
// one field.
var verdictFields = map[string]string{
	"next_verification_invalid": "next_verification", "verdict_invalid": "verdict", "category_invalid": "category",
	"severity_invalid": "severity", "severity_not_assignable": "severity", "supported_without_code_evidence": "supporting_evidence_ids",
	"valid_conditions_missing": "valid_conditions", "refuted_without_counterevidence": "counterevidence_ids",
	"invalid_conditions_missing": "invalid_conditions", "unresolved_assumptions_missing": "unresolved_assumptions",
}

// faultFields names the fields of m (its citations resolved) that code
// concerns.
func faultFields(m modelVerification, code string, catalog map[string]Excerpt, listText int) []string {
	lists := []struct {
		name  string
		items []string
	}{{"valid_conditions", m.ValidConditions}, {"invalid_conditions", m.InvalidConditions}, {"unresolved_assumptions", m.UnresolvedAssumptions}, {"next_verification", m.NextVerification}}
	evidence := []struct {
		name string
		ids  []string
	}{{"supporting_evidence_ids", m.SupportingEvidence}, {"counterevidence_ids", m.Counterevidence}}
	var out []string
	switch code {
	case "list_too_long":
		for _, l := range lists {
			if len(l.items) > 4 {
				out = append(out, l.name)
			}
		}
	case "text_invalid":
		if !record.SafeText(m.SeverityReason) {
			out = append(out, "severity_reason")
		}
		if !record.SafeText(m.Impact) {
			out = append(out, "impact")
		}
		for _, l := range lists {
			if slices.ContainsFunc(l.items, func(s string) bool { return !record.SafeText(s) || len(s) > listText }) {
				out = append(out, l.name)
			}
		}
	case "evidence_list_too_long", "evidence_id_duplicate", "evidence_id_unknown":
		for _, l := range evidence {
			unknown := slices.ContainsFunc(l.ids, func(id string) bool { _, ok := catalog[id]; return !ok })
			if (code == "evidence_list_too_long" && len(l.ids) > 8) || (code == "evidence_id_duplicate" && len(tidyIDs(l.ids, len(l.ids))) != len(l.ids)) ||
				(code == "evidence_id_unknown" && unknown) {
				out = append(out, l.name)
			}
		}
	default:
		if f, ok := verdictFields[code]; ok {
			out = append(out, f)
		}
	}
	return out
}

// rangeRequired is a sent code or test excerpt of more than
// record.WholeCitationLines lines: it is citable only as
// "<evidence id>:<start>-<end>" inside these lines.
type rangeRequired struct {
	ID    string `json:"evidence_id"`
	Start int    `json:"start_line"`
	End   int    `json:"end_line"`
}

// citableIDs splits the sent excerpts into those citable by ID alone and
// those that need a range.
func citableIDs(sent []record.Excerpt) (alone []string, ranged []rangeRequired) {
	alone = []string{}
	for _, e := range sent {
		if rangeNeeded(e) {
			ranged = append(ranged, rangeRequired{e.ID, e.Start, e.End})
		} else {
			alone = append(alone, e.ID)
		}
	}
	return alone, ranged
}

// rangeNeeded reports an excerpt cited without a range only at fault
// (citer.resolveIssues, errCitationMissing).
func rangeNeeded(e record.Excerpt) bool {
	return (e.Kind == "code" || e.Kind == "test") && e.Origin != record.OriginCited && e.End-e.Start+1 > record.WholeCitationLines
}

// verifyItem is one verification entry after its check: accepted (ok, with
// v) or rejected with its codes and the citations at fault. resolvable marks
// a rejected entry whose faults are all of form.
type verifyItem struct {
	v          Verification
	ok         bool
	resolvable bool
	errs       []string
	issues     []verifyIssue
	dropped    bool // observation IDs left out of its evidence lists
}

// verdictResolutions are the limitations a verdict carries when the
// platform resolved a fault of its form (verifyEntry).
var verdictResolutions = []string{limCitationUnspecified, limCitationFromCandidate, limCitationListTrimmed, limVerificationTrimmed}

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
// text is echoed into a repair request. raws holds each candidate's entry as
// given, for a later final reading (verifyEntry).
func parseVerificationItems(raw json.RawMessage, want map[string]bool, cite *citer, droppable map[string]bool, listText int, final bool,
	hints map[string][]string) (items map[string]verifyItem, raws map[string]json.RawMessage, outputErrs []string) {
	items, raws = map[string]verifyItem{}, map[string]json.RawMessage{}
	var res struct {
		Verifications []json.RawMessage `json:"verifications"`
	}
	if domain.DecodeStrict(raw, &res) != nil || res.Verifications == nil {
		return items, raws, []string{"output_schema_invalid"}
	}
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
			items[id] = verifyItem{errs: []string{"duplicate_candidate_verification"}}
			continue
		}
		raws[id] = item
		items[id] = verifyEntry(item, cite, droppable, listText, final, hints[id])
	}
	return items, raws, outputErrs
}

// verifyEntry checks one verification entry. An entry whose only defect is
// citing, in its evidence lists, IDs the platform itself gave in the request
// as something other than evidence (droppable: the observation IDs) is
// accepted without them when what remains passes every check of its verdict
// on its own (dropped); any other unknown ID keeps it rejected.
//
// With final (the repair is spent or cannot be sent) an entry whose faults
// are all of form (docs/REVIEW.md#출력-오류-분류) is resolved and checked
// again: a citation of a long excerpt without a usable range stands for the
// candidate's own cited ranges inside that excerpt (hints) or else the
// whole sent excerpt, repeated or surplus IDs go, a condition, assumption or
// next step that fails its own rule is left out and each list keeps its
// first allowed items. The limitations of what was resolved are added once
// every verdict rule passed.
func verifyEntry(item json.RawMessage, cite *citer, droppable map[string]bool, listText int, final bool, hints []string) verifyItem {
	var m modelVerification
	if domain.DecodeStrict(item, &m) != nil {
		return verifyItem{errs: []string{"item_schema_invalid"}}
	}
	support, counter := m.SupportingEvidence, m.Counterevidence
	var r verifyItem
	var supportIssues, counterIssues []citeIssue
	m.SupportingEvidence, _, supportIssues = cite.resolveIssues(support, citeNarrow)
	m.Counterevidence, _, counterIssues = cite.resolveIssues(counter, citeNarrow)
	for _, x := range []struct {
		field  string
		issues []citeIssue
	}{{"supporting_evidence_ids", supportIssues}, {"counterevidence_ids", counterIssues}} {
		for _, is := range x.issues {
			r.errs = addLim(r.errs, is.code)
			r.issues = append(r.issues, verifyIssue{Field: x.field, ID: is.id, Error: is.code})
		}
	}
	catalog := cite.catalog()
	check := func(m modelVerification) (Verification, []string, bool) {
		v, errs := checkModel(m, catalog, listText)
		if len(droppable) > 0 && slices.Equal(errs, []string{"evidence_id_unknown"}) && onlyDroppable(m.SupportingEvidence, catalog, droppable) && onlyDroppable(m.Counterevidence, catalog, droppable) {
			known := m
			known.SupportingEvidence, known.Counterevidence = knownIDs(m.SupportingEvidence, catalog), knownIDs(m.Counterevidence, catalog)
			if kv, kerrs := checkModel(known, catalog, listText); len(kerrs) == 0 {
				return kv, nil, true
			}
		}
		return v, errs, false
	}
	v, errs, dropped := check(m)
	if len(r.errs) == 0 && len(errs) == 0 {
		r.v, r.ok, r.dropped = v, true, dropped
		return r
	}
	r.errs = addLim(r.errs, errs...)
	for _, code := range errs {
		for _, field := range faultFields(m, code, catalog, listText) {
			r.issues = append(r.issues, verifyIssue{Field: field, Error: code})
		}
	}
	// An unknown ID the droppable rule may clear does not stop the
	// resolution; the rule runs again on the resolved entry.
	faults := r.errs
	if onlyDroppable(m.SupportingEvidence, catalog, droppable) && onlyDroppable(m.Counterevidence, catalog, droppable) {
		faults = slices.DeleteFunc(slices.Clone(faults), func(c string) bool { return c == "evidence_id_unknown" })
	}
	r.resolvable = formOnly(faults)
	if !final || !r.resolvable {
		return r
	}
	var lims, supportLims, counterLims, supportErrs, counterErrs []string
	m.SupportingEvidence, supportLims, supportErrs = cite.degrade(support, citeNarrow, 8, hints)
	m.Counterevidence, counterLims, counterErrs = cite.degrade(counter, citeNarrow, 8, hints)
	if len(supportErrs)+len(counterErrs) > 0 {
		return r
	}
	lims = addLim(lims, supportLims...)
	lims = addLim(lims, counterLims...)
	for _, x := range []struct {
		list  *[]string
		limit int
	}{{&m.ValidConditions, 4}, {&m.InvalidConditions, 4}, {&m.UnresolvedAssumptions, 4}, {&m.NextVerification, 3}} {
		if *x.list == nil {
			continue
		}
		kept := slices.DeleteFunc(slices.Clone(*x.list), func(s string) bool { return !record.SafeText(s) || len(s) > listText })
		if len(kept) > x.limit {
			kept = kept[:x.limit]
		}
		if len(kept) != len(*x.list) {
			*x.list, lims = kept, addLim(lims, limVerificationTrimmed)
		}
	}
	catalog = cite.catalog()
	v, errs, dropped = check(m)
	if len(errs) != 0 {
		return r
	}
	v.Limitations = addLim(v.Limitations, lims...)
	r.v, r.ok, r.dropped = v, true, dropped
	return r
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

func checkModel(m modelVerification, catalog map[string]Excerpt, listText int) (Verification, []string) {
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
			if !record.SafeText(s) || len(s) > listText {
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
	if len(errs) == 0 && record.CheckVerificationWithin(v, catalog, listText) != nil {
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
