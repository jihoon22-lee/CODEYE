package review

import (
	"errors"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The review verifier applies the verification semantics
// (per-candidate acceptance, one bounded repair of the rejected candidates,
// quarantine as unreviewed, reproduction claims downgraded, success without
// a call when there is no candidate) over the accepted candidates of
// investigate and interpret. A non-JSON reply or workspace proof mismatch can
// be retried once each within record.RoleMaxCalls. An entry citing observation
// IDs among its evidence is accepted without them when the remaining evidence
// meets the verdict's rule (evidence_id_dropped on the call). A later call
// that fails keeps the verdicts already accepted (verification_call_failed).
const verifyContract = `For this role use exactly {"verifications":[{"candidate_id":"<id of input.candidates>","verdict":"supported|refuted|inconclusive","category":"correctness|error_handling|concurrency|resource_lifecycle|data_integrity|api_contract|security|maintainability|performance|test_gap","severity":"high|medium|low|info","severity_reason":"...","impact":"...","valid_conditions":["..."],"invalid_conditions":["..."],"supporting_evidence_ids":["ev_..."],"counterevidence_ids":["ev_..."],"unresolved_assumptions":["..."],"next_verification":["..."]}]}, ignoring any wider output examples. Return exactly one entry per candidate in input. Before deciding, read the caller and test excerpts (origin caller/test) for guards, deferred cleanup or ownership transfer that would invalidate the candidate, and keep each documents contract to its applies_to path. supported needs a code or test evidence ID and at least one valid condition; refuted needs counterevidence IDs and invalid conditions; otherwise use inconclusive with unresolved assumptions. Nothing was executed: never claim reproduction, test results or runtime output. Cite only evidence IDs given in scope; do not add paths, line numbers or other fields. Give 1-3 next_verification items that are reading or review steps, not commands.`

const verifyRepairRules = "\nFormat repair: your previous output for the candidates listed in input.repair was rejected with the listed platform error codes. Return a verifications entry only for those candidates, citing only IDs from scope.excerpts; accepted_evidence_ids lists every valid ID."

type verifyInput struct {
	Scope struct {
		Snapshot    string           `json:"snapshot_manifest_digest"`
		Excerpts    []record.Excerpt `json:"excerpts"`
		Limitations []string         `json:"limitations"`
	} `json:"scope"`
	Contracts    []record.Contract       `json:"contracts"`
	Observations []record.ObservationRef `json:"observations"`
	Candidates   []record.Candidate      `json:"candidates"`
	Repair       *repairInput            `json:"repair,omitempty"`
}

// limVerifyBudget marks candidates left unreviewed because the verifier
// request could not carry them within PromptBudget.
const limVerifyBudget = "verification_budget_exceeded"

func verificationClaimsExecution(v record.Verification) bool {
	for _, t := range append(append(append([]string{v.SeverityReason, v.Impact}, v.ValidConditions...), v.InvalidConditions...), append(v.UnresolvedAssumptions, v.NextVerification...)...) {
		if record.ClaimsExecution(t) {
			return true
		}
	}
	return false
}

// verify checks the accepted investigate and interpret candidates of the
// whole run or, for a slice task, its own slice's investigate candidates
// only (with the review.scope artifact as upstream).
func (s *roleStage) verify() analysis.TaskResult {
	docRec, uDoc := s.prior(record.StageDocuments)
	var invRec, intRec record.Record
	var uInv, uInt record.Upstream
	var upstream []record.Upstream
	if s.slice == nil {
		invRec, uInv = s.prior(record.StageInvestigate)
		intRec, uInt = s.prior(record.StageInterpret)
		upstream = []record.Upstream{uDoc, uInv, uInt}
	} else {
		invRec, uInv = s.prior(record.StageInvestigate + "." + s.slice.Key)
		upstream = []record.Upstream{uDoc, uInv, s.scopeUp}
	}
	if uInv.State != record.UpstreamAccepted && uInt.State != record.UpstreamAccepted {
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	if r := s.begin(upstream); r != nil {
		return *r
	}
	s.noteUpstream(upstream...)
	var candidates []record.Candidate
	var context []record.Excerpt
	seen := map[string]bool{}
	addEvidence := func(list []record.Excerpt) {
		for _, e := range list {
			if !seen[e.ID] {
				seen[e.ID] = true
				context = append(context, e)
			}
		}
	}
	if uInv.State == record.UpstreamAccepted {
		candidates = append(candidates, invRec.Candidates...)
		addEvidence(invRec.Evidence)
	}
	if uInt.State == record.UpstreamAccepted {
		candidates = append(candidates, intRec.Candidates...)
		addEvidence(intRec.Evidence)
		used := map[string]bool{}
		for _, c := range intRec.Candidates {
			for _, id := range c.ObservationIDs {
				used[id] = true
			}
		}
		for _, o := range intRec.Observations {
			if used[o.ID] {
				s.rec.Observations = append(s.rec.Observations, o)
			}
		}
	}
	contracts := []record.Contract{}
	if uDoc.State == record.UpstreamAccepted {
		contracts = nonNilList(docRec.Contracts)
		addEvidence(citedDocuments(docRec))
	}
	s.rec.Candidates = nonNilList(candidates)
	if len(candidates) == 0 {
		s.setEvidence(context)
		s.rec.Verifications = []record.Verification{}
		s.rec.Limitations = addLim(s.rec.Limitations, limNoCandidates)
		return s.finish()
	}
	withText, err := s.withText(context)
	if err != nil {
		s.setEvidence(context)
		return *s.fail(result(domain.TaskFailed, "review_input_unavailable"))
	}
	plain := make([]CandidateText, len(candidates))
	cited := map[string]bool{}
	for i, c := range candidates {
		plain[i] = c.CandidateText
		for _, id := range c.Evidence {
			cited[id] = true
		}
	}
	extra, lims, err := expand(s.ctx, s.j.Snapshot, normalizedScope(s.j.Scope), withText, plain)
	if err != nil {
		s.setEvidence(context)
		return *s.fail(result(domain.TaskFailed, "review_input_unavailable"))
	}
	// Callers and tests in a file a secret analyzer reported stay out.
	extra = slices.DeleteFunc(extra, func(x Excerpt) bool {
		if s.secretFiles()[x.Path] {
			s.rec.Limitations = addLim(s.rec.Limitations, limSecretWithheld)
			return true
		}
		return false
	})
	s.rec.Limitations = addLim(s.rec.Limitations, lims...)
	// The request must fit PromptBudget. Expansion, uncited context and
	// contracts go first; then candidates are verified as a prefix (investigate
	// before interpret) and the rest stay unreviewed with a limitation. Every
	// sent candidate keeps all excerpts it cites.
	in := verifyInput{Observations: s.rec.Observations}
	in.Scope.Snapshot = s.j.Snapshot.Digest
	instructions := s.instructions(verifyContract)
	var sent []record.Excerpt
	verified := 0
	fitted := false
	for n := len(candidates); n >= 1 && !fitted; n-- {
		citedBy := map[string]bool{}
		for _, c := range candidates[:n] {
			for _, id := range c.Evidence {
				citedBy[id] = true
			}
		}
		var ordered []record.Excerpt
		for _, e := range withText {
			if citedBy[e.ID] {
				ordered = append(ordered, e)
			}
		}
		keep := len(ordered)
		ordered = append(ordered, extra...)
		for _, e := range withText {
			if !citedBy[e.ID] {
				ordered = append(ordered, e)
			}
		}
		if len(ordered) > 40 {
			ordered = ordered[:max(keep, 40)]
		}
		for _, withContracts := range []bool{true, false} {
			x := in
			x.Candidates, x.Contracts = candidates[:n], []record.Contract{}
			if withContracts {
				x.Contracts = contracts
			}
			build := func(list []record.Excerpt) ([]byte, error) {
				y := x
				y.Scope.Excerpts, y.Scope.Limitations = list, s.rec.Limitations
				return marshal(y)
			}
			list, trimmed, err := s.fitExcerpts(instructions, keep, ordered, build)
			if errors.Is(err, errTooLarge) {
				continue
			}
			if err != nil {
				s.setEvidence(context)
				return s.inputFailure(err)
			}
			if trimmed {
				s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed, limCallersOutside)
			}
			if !withContracts && len(contracts) > 0 {
				s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed, "contracts_outside_verify_input")
			}
			in, sent, verified, fitted = x, list, n, true
			break
		}
	}
	if !fitted {
		s.setEvidence(context)
		return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
	}
	in.Scope.Excerpts, in.Scope.Limitations = sent, s.rec.Limitations
	// The record keeps every candidate (its lineage) with the excerpts they
	// cite, plus what was sent.
	recorded := append([]record.Excerpt{}, sent...)
	have := catalogOf(sent)
	for _, e := range context {
		if _, ok := have[e.ID]; !ok && cited[e.ID] {
			recorded = append(recorded, e)
		}
	}
	s.setEvidence(recorded)
	catalog := catalogOf(sent)
	for id, e := range catalog {
		e.Text = ""
		catalog[id] = e
	}
	ids := make([]string, 0, len(sent))
	for _, e := range sent {
		ids = append(ids, e.ID)
	}
	budgetDropped := candidates[verified:]
	candidatesSent := candidates[:verified]
	if len(budgetDropped) > 0 {
		s.rec.Limitations = addLim(s.rec.Limitations, limVerifyBudget)
	}

	accepted := map[string]record.Verification{}
	pending := candidatesSent
	var repair *repairInput
	repaired, retried, proofRetried := false, false, false
	// Observation IDs are in the request but are not evidence; an entry that
	// cites one beside valid evidence may be trimmed (parseVerificationItems).
	observationIDs := map[string]bool{}
	for _, o := range in.Observations {
		observationIDs[o.ID] = true
	}
	for attempt := 1; attempt <= record.RoleMaxCalls && len(pending) > 0; attempt++ {
		if s.ctx.Err() != nil {
			return *s.fail(result(domain.TaskCancelled, "cancelled"))
		}
		x := in
		x.Candidates, x.Repair = pending, repair
		query, err := marshal(x)
		if err != nil {
			return *s.fail(result(domain.TaskFailed, "review_input_unavailable"))
		}
		text := instructions
		if repair != nil {
			text += verifyRepairRules
		}
		q := opencode.Request{Role: s.role, Scratch: s.j.Scratch, SnapshotDigest: s.j.Snapshot.Digest, Instructions: text, Input: query, ProtectedRoots: []string{s.j.Snapshot.SnapshotDir}}
		size := len(opencode.Prompt(q))
		if size > PromptBudget {
			return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
		}
		if r := s.reserveCall(); r != nil {
			return *s.fail(*r)
		}
		reply := s.h.Caller.Review(s.ctx, s.runner, q)
		s.rec.Limitations = noteVersion(s.rec.Limitations, reply)
		call := record.Call{Role: s.role, Model: reply.Model, Version: reply.Version, PackID: s.pack.ID, PackVersion: s.pack.Version, PackDigest: s.pack.Digest,
			PackFiles: s.pack.Files, RequestDigest: reply.RequestDigest, RequestBytes: size, Usage: usage(reply.Usage), Attempt: attempt, OutputErrors: []string{}}
		if reply.Reason != "" && reply.Reason != "model_output_invalid" {
			call.Outcome = reply.Reason
			if !record.ValidCode(call.Outcome) {
				call.Outcome = "model_unavailable"
			}
			s.rec.Calls = append(s.rec.Calls, call)
			if reply.WorkspaceProofMismatch() {
				if s.ctx.Err() != nil {
					return *s.fail(result(domain.TaskCancelled, "cancelled"))
				}
				if !proofRetried && attempt < record.RoleMaxCalls {
					proofRetried = true
					continue
				}
			}
			// A later call that fails (timeout, refusal) keeps the verdicts
			// already accepted; the rest are quarantined and the task ends
			// incomplete. Cancellation still fails the whole stage.
			if len(accepted) == 0 || s.ctx.Err() != nil || call.Outcome == "cancelled" {
				return *s.fail(failure(reply))
			}
			s.rec.Limitations = addLim(s.rec.Limitations, "verification_call_failed")
			break
		}
		want := map[string]bool{}
		for _, p := range pending {
			want[p.ID] = true
		}
		notJSON := reply.Reason != ""
		got, itemErrs, outputErrs, dropped := map[string]record.Verification{}, map[string][]string{}, []string{"output_not_json"}, []string(nil)
		if !notJSON {
			got, itemErrs, outputErrs, dropped = parseVerificationItems(reply.Output, want, catalog, observationIDs)
		}
		for id, v := range got {
			if verificationClaimsExecution(v) {
				delete(got, id)
				itemErrs[id] = addLim(itemErrs[id], "execution_claim")
			}
		}
		all := addLim(nil, outputErrs...)
		for _, errs := range itemErrs {
			all = addLim(all, errs...)
		}
		for _, id := range dropped {
			if _, ok := got[id]; ok {
				all = addLim(all, "evidence_id_dropped")
			}
		}
		call.Outcome, call.OutputErrors = callRejected, nonNil(all)
		if len(got) > 0 {
			call.Outcome = callAccepted
		}
		s.rec.Calls = append(s.rec.Calls, call)
		s.noteExecutionClaim(call)
		for id, v := range got {
			accepted[id] = v
		}
		// A reply that is not JSON says nothing about the items: it gets one
		// retry of the same request (with the earlier item errors, if any)
		// and does not use up the repair of the rejected items.
		if notJSON {
			if retried {
				break
			}
			retried = true
		} else {
			if repaired {
				var next []record.Candidate
				for _, p := range pending {
					if _, ok := accepted[p.ID]; !ok {
						next = append(next, p)
					}
				}
				pending = next
				break
			}
			repaired = true
		}
		prior := map[string][]string{}
		if notJSON && repair != nil {
			for _, item := range repair.Candidates {
				prior[item.ID] = item.Errors
			}
		}
		var next []record.Candidate
		repair = &repairInput{OutputErrors: nonNil(outputErrs), Candidates: []repairItem{}, AcceptedIDs: ids}
		for _, p := range pending {
			if _, ok := accepted[p.ID]; ok {
				continue
			}
			next = append(next, p)
			errs := itemErrs[p.ID]
			if len(errs) == 0 {
				errs = prior[p.ID]
			}
			if len(errs) == 0 {
				errs = []string{"verification_missing"}
			}
			repair.Candidates = append(repair.Candidates, repairItem{ID: p.ID, Errors: errs})
		}
		pending = next
	}
	if len(accepted) == 0 {
		return *s.fail(result(domain.TaskFailed, reasonOutputInvalid))
	}
	s.rec.Verifications = make([]record.Verification, len(candidates))
	for i, c := range candidates {
		v, ok := accepted[c.ID]
		switch {
		case ok:
		case i >= verified:
			v = unreviewed(c.ID, limVerifyBudget)
		default:
			v = unreviewed(c.ID, record.LimQuarantined)
			s.rec.Limitations = addLim(s.rec.Limitations, record.LimQuarantined)
		}
		if slices.Contains(v.Limitations, record.LimReproductionRejected) {
			s.rec.Limitations = addLim(s.rec.Limitations, record.LimReproductionRejected)
		}
		s.rec.Verifications[i] = v
	}
	res := s.finish()
	switch {
	case res.Status != domain.TaskSucceeded:
	case len(pending) > 0:
		res.Status, res.Reason = domain.TaskFailed, "review_output_incomplete"
	case len(budgetDropped) > 0:
		res.Status, res.Reason = domain.TaskFailed, "review_input_incomplete"
	}
	return res
}
