package review

import (
	"context"
	"encoding/json"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review-v1"
)

// verifyBudget keeps instructions+input under the OpenCode request limit with
// room for a repair list.
const verifyBudget = 60 << 10

const verifyContract = `For this role use exactly {"verifications":[{"candidate_id":"candidate_N","verdict":"supported|refuted|inconclusive","category":"correctness|error_handling|concurrency|resource_lifecycle|data_integrity|api_contract|security|maintainability|performance|test_gap","severity":"high|medium|low|info","severity_reason":"...","impact":"...","valid_conditions":["..."],"invalid_conditions":["..."],"supporting_evidence_ids":["ev_..."],"counterevidence_ids":["ev_..."],"unresolved_assumptions":["..."],"next_verification":["..."]}]}, ignoring any wider output examples. Return exactly one entry per candidate in input. Before deciding, read the caller and test excerpts (origin caller/test) for guards, deferred cleanup or ownership transfer that would invalidate the candidate. supported needs a code or test evidence ID and at least one valid condition; refuted needs counterevidence IDs and invalid conditions; otherwise use inconclusive with unresolved assumptions. Nothing was executed: never claim reproduction, test results or runtime output. Cite only evidence IDs given in scope; do not add paths, line numbers or other fields. Give 1-3 next_verification items that are reading or review steps, not commands.`

const verifyRules = "\nUse only the provided excerpts. Review ordinary software behavior and maintainability. No code execution, test generation or external access. Cite only evidence IDs given in scope. Write concise Korean prose."

const repairRules = "\nFormat repair: your previous output for the candidates listed in input.repair was rejected with the listed platform error codes. Return a verifications entry only for those candidates, citing only IDs from scope.excerpts; accepted_evidence_ids lists every valid ID."

type verifyInput struct {
	Scope      Corpus       `json:"scope"`
	Facts      []Fact       `json:"facts"`
	Candidates []Candidate  `json:"candidates"`
	Repair     *repairInput `json:"repair,omitempty"`
}

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

// sameEvidence requires the prior record's catalog to be exactly the corpus
// rebuilt now from the immutable snapshot (ID, path, blob, range, kind).
func sameEvidence(prior, now []Excerpt) bool {
	if len(prior) != len(now) {
		return false
	}
	for i, p := range prior {
		n := now[i]
		if p.ID != n.ID || p.Path != n.Path || p.SHA256 != n.SHA256 || p.Start != n.Start || p.End != n.End || p.Kind != n.Kind || (p.Origin != "" && p.Origin != "context") {
			return false
		}
	}
	return true
}

func (h Handler) verify(ctx context.Context, j analysis.Job, runner executor.Runner) analysis.TaskResult {
	prior, err := readRecord(j, "investigate")
	if err != nil {
		return result(domain.TaskSkippedPolicy, "dependency_failed")
	}
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		return result(domain.TaskFailed, "review_input_unavailable")
	}
	if !sameEvidence(prior.Evidence, c.Excerpts) {
		return result(domain.TaskFailed, "review_evidence_invalid")
	}
	for i := range c.Excerpts {
		c.Excerpts[i].Origin = "context"
	}
	rec := Record{Schema: record.SchemaVersion, Snapshot: c.Snapshot, RunID: j.RunID, AttemptID: j.AttemptID, Stage: "verify", Outcome: record.OutcomeAccepted,
		Facts: prior.Facts, Candidates: prior.Candidates, Limitations: addLim(nil, c.Limitations...), Calls: append([]Provenance{}, prior.Calls...)}
	setEvidence := func(list []Excerpt) {
		rec.Evidence = make([]Excerpt, len(list))
		for i, e := range list {
			e.Text = ""
			rec.Evidence[i] = e
		}
	}
	setEvidence(c.Excerpts)
	if len(prior.Candidates) == 0 {
		rec.Limitations = addLim(rec.Limitations, limNoCandidates)
		res := writeRecord(j, rec)
		res.Limitations = rec.Limitations
		return res
	}
	extra, lims, err := expand(ctx, j.Snapshot, j.Scope, c.Excerpts, prior.Candidates)
	if err != nil {
		return failedRecord(j, rec, result(domain.TaskFailed, "review_input_unavailable"))
	}
	pack, err := reviewpack.For("verify")
	if err != nil {
		return failedRecord(j, rec, result(domain.TaskFailed, "review_pack_invalid"))
	}
	instructions := pack.Text + "\n" + verifyContract + verifyRules
	scope := c
	scope.Limitations = addLim(append([]string{}, c.Limitations...), lims...)
	for {
		scope.Excerpts = append(append([]Excerpt{}, c.Excerpts...), extra...)
		b, err := json.Marshal(verifyInput{Scope: scope, Facts: prior.Facts, Candidates: prior.Candidates})
		if err == nil && len(b)+len(instructions)+len(repairRules)+4096 <= verifyBudget {
			break
		}
		if len(extra) == 0 {
			return failedRecord(j, rec, result(domain.TaskFailed, reasonInputTooLarge))
		}
		extra = extra[:len(extra)-1]
		scope.Limitations = addLim(scope.Limitations, limCallersOutside)
	}
	rec.Limitations = addLim(rec.Limitations, scope.Limitations...)
	setEvidence(scope.Excerpts)
	catalog := map[string]Excerpt{}
	ids := make([]string, 0, len(scope.Excerpts))
	for _, e := range rec.Evidence {
		catalog[e.ID] = e
		ids = append(ids, e.ID)
	}

	accepted := map[string]Verification{}
	pending := prior.Candidates
	var repair *repairInput
	for attempt := 1; attempt <= 2 && len(pending) > 0; attempt++ {
		if ctx.Err() != nil {
			return failedRecord(j, rec, result(domain.TaskCancelled, "cancelled"))
		}
		in := verifyInput{Scope: scope, Facts: prior.Facts, Candidates: pending, Repair: repair}
		query, err := json.Marshal(in)
		if err != nil {
			return failedRecord(j, rec, result(domain.TaskFailed, "review_input_unavailable"))
		}
		text := instructions
		if repair != nil {
			text += repairRules
		}
		reply := h.Caller.Review(ctx, runner, opencode.Request{Role: "verify", Scratch: j.Scratch, SnapshotDigest: c.Snapshot,
			Instructions: text, Input: query, ProtectedRoots: []string{j.Snapshot.SnapshotDir}})
		call := Provenance{Role: "verify", Model: reply.Model, Version: reply.Version, PackDigest: pack.Digest, PackFiles: pack.Files,
			RequestDigest: reply.RequestDigest, Usage: usage(reply.Usage), Attempt: attempt}
		if reply.Reason != "" && reply.Reason != "model_output_invalid" {
			call.Outcome = reply.Reason
			if !record.ValidCode(call.Outcome) {
				call.Outcome = "model_unavailable"
			}
			rec.Calls = append(rec.Calls, call)
			// A failed process cannot back a succeeded task (the worker checks
			// every recorded execution), so a failed repair call fails the task.
			return failedRecord(j, rec, failure(reply))
		}
		want := map[string]bool{}
		for _, p := range pending {
			want[p.ID] = true
		}
		got, itemErrs, outputErrs := map[string]Verification{}, map[string][]string{}, []string{"output_not_json"}
		if reply.Reason == "" {
			got, itemErrs, outputErrs = parseVerifications(reply.Output, want, catalog)
		}
		call.Outcome = callRejected
		if len(got) > 0 {
			call.Outcome = callAccepted
		}
		rec.Calls = append(rec.Calls, call)
		for id, v := range got {
			accepted[id] = v
		}
		var next []Candidate
		repair = &repairInput{OutputErrors: nonNil(outputErrs), Candidates: []repairItem{}, AcceptedIDs: ids}
		for _, p := range pending {
			if _, ok := accepted[p.ID]; ok {
				continue
			}
			next = append(next, p)
			errs := itemErrs[p.ID]
			if len(errs) == 0 {
				errs = []string{"verification_missing"}
			}
			repair.Candidates = append(repair.Candidates, repairItem{ID: p.ID, Errors: errs})
		}
		pending = next
	}
	if len(accepted) == 0 {
		return failedRecord(j, rec, result(domain.TaskFailed, reasonOutputInvalid))
	}
	rec.Verifications = make([]Verification, len(rec.Candidates))
	for i, cand := range rec.Candidates {
		v, ok := accepted[cand.ID]
		if !ok {
			v = unreviewed(cand.ID, record.LimQuarantined)
			rec.Limitations = addLim(rec.Limitations, record.LimQuarantined)
		}
		if slices.Contains(v.Limitations, record.LimReproductionRejected) {
			rec.Limitations = addLim(rec.Limitations, record.LimReproductionRejected)
		}
		rec.Verifications[i] = v
	}
	res := writeRecord(j, rec)
	if res.Status == domain.TaskSucceeded && len(pending) > 0 {
		res.Status, res.Reason = domain.TaskFailed, "review_output_incomplete"
	}
	res.Limitations = rec.Limitations
	return res
}

// fail keeps the attempted calls and candidates (all unreviewed) in a failed
// record so a report can show what was tried; no model result is retained.
func failedRecord(j analysis.Job, rec Record, res analysis.TaskResult) analysis.TaskResult {
	rec.Outcome, rec.FailureReason = record.OutcomeFailed, res.Reason
	if !record.ValidCode(rec.FailureReason) {
		rec.FailureReason = "review_failed"
	}
	if rec.Stage == "verify" {
		rec.Verifications = make([]Verification, len(rec.Candidates))
		for i, cand := range rec.Candidates {
			rec.Verifications[i] = unreviewed(cand.ID, record.LimNotCompleted)
		}
	}
	for i := range rec.Calls {
		if rec.Calls[i].Role == rec.Stage && rec.Calls[i].Outcome == callAccepted {
			rec.Calls[i].Outcome = callDiscarded
		}
	}
	if art, err := persist(j, rec); err == nil {
		res.Artifacts = append(res.Artifacts, art)
	} else {
		res.Limitations = addLim(res.Limitations, "review_record_failed")
	}
	return res
}

// parseVerifications accepts each entry independently. Output-level errors
// and per-candidate error codes are fixed platform strings; no model text is
// echoed into a repair request.
func parseVerifications(raw json.RawMessage, want map[string]bool, catalog map[string]Excerpt) (map[string]Verification, map[string][]string, []string) {
	got, itemErrs := map[string]Verification{}, map[string][]string{}
	var res struct {
		Verifications []json.RawMessage `json:"verifications"`
	}
	if domain.DecodeStrict(raw, &res) != nil || res.Verifications == nil {
		return got, itemErrs, []string{"output_schema_invalid"}
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
		if v, errs := checkModel(m, catalog); len(errs) > 0 {
			itemErrs[id] = errs
		} else {
			got[id] = v
		}
	}
	return got, itemErrs, outputErrs
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
		// Only a platform execution artifact can reproduce; W12 has none.
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
