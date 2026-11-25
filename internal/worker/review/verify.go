package review

import (
	"encoding/json"
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
// meets the verdict's rule (evidence_id_dropped on the call). Once the repair
// is spent, cannot be sent or fails, an entry whose faults are all of form is
// resolved and accepted (verifyEntry, docs/REVIEW.md#출력-오류-분류); an
// integrity fault keeps its candidate quarantined. A later call that fails
// keeps the verdicts already accepted (verification_call_failed when a
// candidate stays unreviewed).
const verifyContract = `For this role use exactly {"verifications":[{"candidate_id":"<id of input.candidates>","verdict":"supported|refuted|inconclusive","category":"correctness|error_handling|concurrency|resource_lifecycle|data_integrity|api_contract|security|maintainability|performance|test_gap","severity":"high|medium|low|info","severity_reason":"...","impact":"...","valid_conditions":["..."],"invalid_conditions":["..."],"supporting_evidence_ids":["ev_...:120-134"],"counterevidence_ids":["ev_...:88-96"],"unresolved_assumptions":["..."],"next_verification":["..."]}]}. Return one verdict per input candidate; each evidence list holds at most 8 distinct IDs and each condition or assumption list at most 4 items. Check callers, ownership, error paths, tests and each contract's scope. Cite only scope.excerpts IDs; for a long excerpt cite the candidate's own range or the lines that show the point, at most 80 lines, never its ID alone. Supported needs supporting code/test; refuted needs counterevidence; otherwise use inconclusive. Use observed_test_cases only for the cited assertion. Never claim reproduction. Give 1-3 next_verification items that are reading or review steps, not commands.`

const verifyRepairRules = "\nFormat repair: your previous output for the candidates in input.repair.candidates was rejected with the listed platform error codes; " +
	"issues names each fault by field and, for a citation, the excerpt it cited. Return a verifications entry only for those candidates. " +
	"Cite an ID of input.repair.accepted_evidence_ids alone or with a range; cite an excerpt of input.repair.range_required_evidence only as " +
	"\"<evidence id>:<start>-<end>\" inside its start_line..end_line and at most 80 lines, for example the candidate's own cited range."

type observedTestCase struct {
	EvidenceID string `json:"evidence_id"`
	Line       int    `json:"line"`
	Status     string `json:"status"`
}

// An executed case is relevant only when its verified source line is inside
// a test excerpt presented to this verifier. No runner-controlled name or
// stream content is sent to the model.
func observedTests(excerpts []record.Excerpt, facts []analysis.ObservedTest) []observedTestCase {
	if len(facts) == 0 {
		return nil
	}
	var out []observedTestCase
	for _, e := range excerpts {
		if e.Kind != "test" {
			continue
		}
		for _, fact := range facts {
			if fact.Path != e.Path || fact.Line < e.Start || fact.Line > e.End {
				continue
			}
			m := observedTestCase{e.ID, fact.Line, fact.Status}
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
			if len(out) == 16 {
				return out
			}
		}
	}
	return out
}

type verifyInput struct {
	ObservedTests []observedTestCase `json:"observed_test_cases,omitempty"`
	Scope         struct {
		Snapshot    string      `json:"snapshot_manifest_digest"`
		Excerpts    excerptList `json:"excerpts"`
		Limitations []string    `json:"limitations"`
	} `json:"scope"`
	Contracts    []record.Contract       `json:"contracts"`
	Observations []record.ObservationRef `json:"observations"`
	Candidates   []record.Candidate      `json:"candidates"`
	Repair       *repairInput            `json:"repair,omitempty"`
}

// limVerifyBudget marks candidates left unreviewed because the verifier
// request could not carry them within the request budget.
const limVerifyBudget = "verification_budget_exceeded"

// reasonLineageMismatch fails a verify record that would carry an excerpt
// of an upstream record changed; the report builder names the same check.
const reasonLineageMismatch = "review_lineage_mismatch"
const limVerifyHashesOmitted = "verification_excerpt_hashes_omitted"

// limVerifyObservationsTrimmed: a candidate standing on more diagnostics
// than verifyObservationsShown named only its first ones in the request;
// the record keeps them all.
const limVerifyObservationsTrimmed = "verification_observations_trimmed"

// verifyObservationsShown is how many of one candidate's observations a
// verify request names (a triage problem group holds up to 64, about 170
// bytes each as a reference).
const verifyObservationsShown = 8

// requestObservations are the candidates as a request carries them, each
// with at most verifyObservationsShown observation IDs, and the references
// of the observations they still name, in the record's order.
func requestObservations(candidates []record.Candidate, all []record.ObservationRef) ([]record.Candidate, []record.ObservationRef, bool) {
	out := make([]record.Candidate, len(candidates))
	named := map[string]bool{}
	trimmed := false
	for i, c := range candidates {
		if len(c.ObservationIDs) > verifyObservationsShown {
			c.ObservationIDs, trimmed = c.ObservationIDs[:verifyObservationsShown], true
		}
		for _, id := range c.ObservationIDs {
			named[id] = true
		}
		out[i] = c
	}
	refs := []record.ObservationRef{}
	for _, o := range all {
		if named[o.ID] {
			refs = append(refs, o)
		}
	}
	return out, refs, trimmed
}

func marshalVerifyInput(in verifyInput, omitHashes bool) ([]byte, error) {
	if omitHashes {
		// The worker already verifies the blobs. Keep citable IDs, paths,
		// lines, text, and the original artifact evidence at every size.
		in.Scope.Excerpts = slices.Clone(in.Scope.Excerpts)
		for i := range in.Scope.Excerpts {
			in.Scope.Excerpts[i].SHA256 = ""
		}
	}
	return marshal(in)
}

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
// inputUnavailable ends the stage whose input could not be built: cancelled
// when the attempt was cancelled meanwhile (the snapshot reads stop with
// the context), failed otherwise.
func (s *roleStage) inputUnavailable() *analysis.TaskResult {
	if s.ctx.Err() != nil {
		return s.fail(result(domain.TaskCancelled, "cancelled"))
	}
	return s.fail(result(domain.TaskFailed, "review_input_unavailable"))
}

func (s *roleStage) verify() analysis.TaskResult {
	docRec, uDoc := s.prior(record.StageDocuments)
	var invRec, intRec record.Record
	var uInv, uInt record.Upstream
	var upstream []record.Upstream
	switch {
	case s.triage != "":
		// A triage slot's verify checks its problems (origin triage).
		invRec, uInv = s.prior(record.StageTriage + "." + s.triage)
		upstream = []record.Upstream{uDoc, uInv}
	case s.slice == nil:
		invRec, uInv = s.prior(record.StageInvestigate)
		intRec, uInt = s.prior(record.StageInterpret)
		upstream = []record.Upstream{uDoc, uInv, uInt}
	default:
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
		if s.triage != "" {
			used := map[string]bool{}
			for _, c := range invRec.Candidates {
				for _, id := range c.ObservationIDs {
					used[id] = true
				}
			}
			for _, o := range invRec.Observations {
				if used[o.ID] {
					s.rec.Observations = append(s.rec.Observations, o)
				}
			}
		}
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
		return *s.inputUnavailable()
	}
	plain := make([]CandidateText, len(candidates))
	cited := map[string]bool{}
	for i, c := range candidates {
		plain[i] = c.CandidateText
		for _, id := range c.Evidence {
			cited[id] = true
		}
	}
	var changed map[string]bool
	if s.j.PullRequest != nil {
		changed = make(map[string]bool, len(s.j.PullRequest.Changed))
		for _, p := range s.j.PullRequest.Changed {
			changed[p] = true
		}
	}
	// The expansion is measured against the cited excerpts only: a caller
	// inside an upstream unit that this request does not resend (a long
	// unit of another file) still travels, and one inside a unit it does
	// resend travels as a reference to that unit (requestExcerpts).
	anchors := slices.DeleteFunc(slices.Clone(withText), func(x record.Excerpt) bool { return !cited[x.ID] })
	extra, lims, err := expandWithin(s.ctx, s.j.Snapshot, normalizedScope(s.j.Scope), anchors, plain, s.secretFiles(), changed,
		expansionCaps{s.limit().expansion, s.limit().expansionBytes})
	if err != nil {
		s.setEvidence(context)
		return *s.inputUnavailable()
	}
	declarations, err := s.enclosingDeclarations(anchors)
	if err != nil {
		s.setEvidence(context)
		return *s.inputUnavailable()
	}
	budget := s.limit().expansionBytes
	for _, x := range extra {
		budget -= record.JSONLen(x.Text)
	}
	linked, err := s.sourceCallContext(".", anchors, append(slices.Clone(anchors), extra...), s.limit().expansion-len(extra), budget)
	if err != nil {
		s.setEvidence(context)
		return *s.inputUnavailable()
	}
	if len(linked) != 0 {
		extra = append(extra, linked...)
		lims = slices.DeleteFunc(lims, func(lim string) bool {
			return lim == limExpansionGoOnly || lim == limDirectCalls || lim == limNoSymbol || lim == limNoCallers
		})
	}
	s.rec.Limitations = addLim(s.rec.Limitations, lims...)
	// A derived range with the ID of an excerpt an upstream record holds
	// (a declaration that is exactly a sent unit, a caller window that is
	// an upstream window) travels and is recorded as that upstream entry
	// (record.CarriesUpstreamEvidence).
	upstreamEvidence := catalogOf(withText)
	for _, list := range [][]record.Excerpt{extra, declarations} {
		for i := range list {
			if original, ok := upstreamEvidence[list[i].ID]; ok {
				list[i] = original
			}
		}
	}
	// The request must fit the request budget. The cited ranges come first,
	// then the declarations enclosing them, the expansion (callers, tests,
	// direct calls), the whole units of the cited files and the other small
	// upstream windows; contracts go first among the optional parts. Then
	// candidates are verified as a prefix (investigate before interpret) and
	// the rest stay unreviewed with a limitation. Every sent candidate keeps
	// all excerpts it cites.
	in := verifyInput{Observations: s.rec.Observations}
	in.Scope.Snapshot = s.j.Snapshot.Digest
	contract := verifyContract
	if len(s.j.TestEvidence) > 0 {
		contract += " Use an observed test to assess its cited assertion. State the code inference only; CODEYE reports the recorded outcome separately."
	}
	instructions := s.instructions(contract)
	var sent []record.Excerpt
	verified := 0
	fitted := false
	omitHashes := false
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
		citedFiles := map[string]bool{}
		for _, e := range ordered {
			citedFiles[e.Path] = true
		}
		add := func(e record.Excerpt) {
			if !citedBy[e.ID] {
				ordered = append(ordered, e)
				citedBy[e.ID] = true
			}
		}
		for _, d := range declarations {
			if slices.ContainsFunc(ordered[:keep], func(x record.Excerpt) bool { return x.Path == d.Path && d.Start <= x.Start && x.End <= d.End }) {
				add(d)
			}
		}
		for _, e := range extra {
			add(e)
		}
		for _, e := range withText {
			if citedFiles[e.Path] && e.End-e.Start+1 > record.MaxCitedLines {
				add(e)
			}
		}
		for _, e := range withText {
			if e.End-e.Start+1 <= record.MaxCitedLines {
				add(e)
			}
		}
		ordered = containedRanges(ordered, cited)
		if len(ordered) > s.limit().verifyExcerpts {
			ordered = ordered[:max(keep, s.limit().verifyExcerpts)]
		}
		for _, variant := range []struct{ withContracts, omitHashes bool }{
			{true, false}, {false, false}, {true, true}, {false, true},
		} {
			x := in
			x.Contracts = []record.Contract{}
			x.Candidates, x.Observations, _ = requestObservations(candidates[:n], s.rec.Observations)
			if variant.withContracts {
				x.Contracts = contracts
			}
			build := func(list []record.Excerpt) ([]byte, error) {
				y := x
				y.Scope.Excerpts, y.Scope.Limitations = list, s.rec.Limitations
				y.ObservedTests = observedTests(list, s.j.TestEvidence)
				if variant.omitHashes {
					y.Scope.Limitations = addLim(y.Scope.Limitations, limVerifyHashesOmitted)
				}
				return marshalVerifyInput(y, variant.omitHashes)
			}
			list, trimmed, err := s.fitExcerpts(instructions, keep, ordered, build)
			if errors.Is(err, errTooLarge) {
				continue
			}
			if err != nil {
				s.setEvidence(context)
				return s.inputFailure(err)
			}
			// The budget may have cut the unit a cited range lies in.
			list = containedRanges(list, cited)
			if trimmed {
				s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed, limCallersOutside)
			}
			if !variant.withContracts && len(contracts) > 0 {
				s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed, "contracts_outside_verify_input")
			}
			if variant.omitHashes {
				s.rec.Limitations = addLim(s.rec.Limitations, limVerifyHashesOmitted)
			}
			in, sent, verified, fitted = x, list, n, true
			omitHashes = variant.omitHashes
			break
		}
	}
	if !fitted {
		s.setEvidence(context)
		return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
	}
	if _, _, trimmed := requestObservations(candidates[:verified], s.rec.Observations); trimmed {
		s.rec.Limitations = addLim(s.rec.Limitations, limVerifyObservationsTrimmed)
	}
	in.Scope.Excerpts, in.Scope.Limitations = sent, s.rec.Limitations
	in.ObservedTests = observedTests(sent, s.j.TestEvidence)
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
	cite := newCiter(s.j.Snapshot.Digest, sent)
	cite.known = upstreamEvidence
	ids, rangeIDs := citableIDs(sent)
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
	// cites one beside valid evidence may be trimmed (verifyEntry).
	observationIDs := map[string]bool{}
	for _, o := range s.rec.Observations {
		observationIDs[o.ID] = true
	}
	// hints are each candidate's own citations: a verdict citing a long
	// excerpt without a usable range stands for them after the repair.
	hints := map[string][]string{}
	for _, c := range candidatesSent {
		hints[c.ID] = c.Evidence
	}
	// held is the latest entry of each pending candidate whose faults are all
	// of form, with the index of its call: when the repair cannot be sent or
	// does not answer for it, the platform resolves that entry.
	type heldEntry struct {
		raw  []byte
		call int
	}
	held := map[string]heldEntry{}
	callFailed := false
	for attempt := 1; attempt <= record.RoleMaxCalls && len(pending) > 0; attempt++ {
		if s.ctx.Err() != nil {
			return *s.fail(result(domain.TaskCancelled, "cancelled"))
		}
		x := in
		x.Candidates, x.Observations, _ = requestObservations(pending, s.rec.Observations)
		x.Repair = repair
		query, err := marshalVerifyInput(x, omitHashes)
		if err != nil {
			return *s.fail(result(domain.TaskFailed, "review_input_unavailable"))
		}
		text := instructions
		if repair != nil {
			text += verifyRepairRules
		}
		q := opencode.Request{Role: s.role, Scratch: s.j.Scratch, SnapshotDigest: s.j.Snapshot.Digest, Instructions: text, Input: query, ProtectedRoots: []string{s.j.Snapshot.SnapshotDir}}
		size := len(opencode.Prompt(q))
		// A repair that does not fit the request budget or the call budget
		// is not sent; the verdicts accepted so far and the held entries
		// stand (output_repair_not_sent).
		if size > s.limit().request {
			if len(accepted)+len(held) == 0 {
				return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
			}
			s.rec.Limitations = addLim(s.rec.Limitations, limRepairNotSent)
			break
		}
		if r := s.reserveCall(); r != nil {
			if len(accepted)+len(held) == 0 || r.Status == domain.TaskCancelled {
				return *s.fail(*r)
			}
			s.rec.Limitations = addLim(s.rec.Limitations, limRepairNotSent)
			break
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
			// already accepted and the held entries; the rest are quarantined
			// and the task ends incomplete. Cancellation still fails the
			// whole stage.
			if len(accepted)+len(held) == 0 || s.ctx.Err() != nil || call.Outcome == "cancelled" {
				return *s.fail(failure(reply))
			}
			callFailed = true
			break
		}
		want := map[string]bool{}
		for _, p := range pending {
			want[p.ID] = true
		}
		notJSON := reply.Reason != ""
		items, raws, outputErrs := map[string]verifyItem{}, map[string]json.RawMessage{}, []string{"output_not_json"}
		if !notJSON {
			// The repair's answer is the last chance of its candidates.
			items, raws, outputErrs = parseVerificationItems(reply.Output, want, cite, observationIDs, s.limit().listText, repaired, hints)
		}
		got, itemErrs := map[string]record.Verification{}, map[string][]string{}
		issues := map[string][]verifyIssue{}
		all := addLim(nil, outputErrs...)
		for id, it := range items {
			if it.ok && verificationClaimsExecution(it.v) {
				it = verifyItem{errs: []string{"execution_claim"}}
			}
			all = addLim(all, it.errs...)
			if it.ok {
				got[id] = it.v
				if it.dropped {
					all = addLim(all, "evidence_id_dropped")
				}
				continue
			}
			itemErrs[id], issues[id] = it.errs, it.issues
			if it.resolvable {
				held[id] = heldEntry{raws[id], len(s.rec.Calls)}
			} else {
				delete(held, id)
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
			delete(held, id)
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
		prior := map[string]repairItem{}
		if notJSON && repair != nil {
			for _, item := range repair.Candidates {
				prior[item.ID] = item
			}
		}
		var next []record.Candidate
		repair = &repairInput{OutputErrors: nonNil(outputErrs), Candidates: []repairItem{}, AcceptedIDs: ids, RangeRequired: rangeIDs}
		for _, p := range pending {
			if _, ok := accepted[p.ID]; ok {
				continue
			}
			next = append(next, p)
			item := repairItem{ID: p.ID, Errors: itemErrs[p.ID], Issues: issues[p.ID]}
			if len(item.Errors) == 0 {
				item = prior[p.ID]
				item.ID = p.ID
			}
			if len(item.Errors) == 0 {
				item.Errors = []string{"verification_missing"}
			}
			repair.Candidates = append(repair.Candidates, item)
		}
		pending = next
	}
	// The held entries of candidates still pending are resolved now that no
	// repair answers for them (verifyEntry with final); the call each came
	// from is then accepted.
	degraded := false
	for _, p := range pending {
		h, ok := held[p.ID]
		if !ok {
			continue
		}
		it := verifyEntry(h.raw, cite, observationIDs, s.limit().listText, true, hints[p.ID])
		if !it.ok || verificationClaimsExecution(it.v) {
			continue
		}
		accepted[p.ID], degraded = it.v, true
		c := &s.rec.Calls[h.call]
		c.Outcome = callAccepted
		if it.dropped {
			c.OutputErrors = addLim(c.OutputErrors, "evidence_id_dropped")
		}
	}
	if degraded {
		pending = slices.DeleteFunc(pending, func(p record.Candidate) bool { _, ok := accepted[p.ID]; return ok })
	}
	if callFailed && len(pending) > 0 {
		s.rec.Limitations = addLim(s.rec.Limitations, "verification_call_failed")
	}
	if len(accepted) == 0 {
		return *s.fail(result(domain.TaskFailed, reasonOutputInvalid))
	}
	var citedIDs [][]string
	for _, v := range accepted {
		citedIDs = append(citedIDs, v.SupportingEvidence, v.Counterevidence)
	}
	s.rec.Evidence = withCited(s.rec.Evidence, textless(cite.used(citedIDs...)))
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
		for _, lim := range v.Limitations {
			if slices.Contains(verdictResolutions, lim) {
				s.rec.Limitations = addLim(s.rec.Limitations, lim, record.LimOutputDegraded)
			}
		}
		s.rec.Verifications[i] = v
	}
	var priors []record.Record
	if uInv.State == record.UpstreamAccepted {
		priors = append(priors, invRec)
	}
	if uInt.State == record.UpstreamAccepted {
		priors = append(priors, intRec)
	}
	if !record.CarriesUpstreamEvidence(s.rec, priors...) {
		// The report would set this record aside; fail it here instead.
		return *s.fail(result(domain.TaskFailed, reasonLineageMismatch))
	}
	res := s.finish()
	switch {
	case res.Status != domain.TaskSucceeded:
	case len(pending) > 0:
		res.Status, res.Reason = domain.TaskFailed, record.ReasonOutputIncomplete
	case len(budgetDropped) > 0:
		res.Status, res.Reason = domain.TaskFailed, record.ReasonInputIncomplete
	}
	return res
}

// containedRanges drops from list each cited range no candidate cites
// (exempt) whose enclosing excerpt list does not carry: an upstream
// strength's range inside a unit of a file no candidate cites, or one the
// budget cut the unit of. The record keeps a cited range only beside an
// excerpt it lies in (record checkCitedRanges).
func containedRanges(list []record.Excerpt, exempt map[string]bool) []record.Excerpt {
	return slices.DeleteFunc(slices.Clone(list), func(e record.Excerpt) bool {
		return e.Origin == record.OriginCited && !exempt[e.ID] && !slices.ContainsFunc(list, func(x record.Excerpt) bool {
			return (x.Origin != record.OriginCited || exempt[x.ID]) && x.ID != e.ID && x.Path == e.Path && x.SHA256 == e.SHA256 && x.Start <= e.Start && e.End <= x.End
		})
	})
}
