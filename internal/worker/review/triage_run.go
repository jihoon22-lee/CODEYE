package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Running a diagnostic triage slot (docs/REVIEW.md#도구-진단-분류). The slots
// end the plan, after the module slices and the contract follow-up, which
// leave them the held share of the call ceiling (triageHold). A slot is
// admitted, in plan order, when a call for the triage and one for the
// verification of its problems remain, as a module slice is. The decision
// (or why the slot had nothing to send) is written next to the call
// counter and the slot's verify follows it.

const triageContract = `For this role use exactly {"triage":[{"group_id":"<id of input.groups>","classification":"problem|harmless|unclear","title":"what goes wrong (problem)","condition":"the code condition under which it bites (problem)","mechanism":"how the cited lines produce the failure (optional)","impact":"what breaks (problem)","improvement":"fix direction (problem)","reason":"problem: what would make it harmless; harmless: the guard, caller, type or test-only path that makes it harmless here; unclear: what the input lacks","evidence_ids":["ev_...:12-30"]}]}, ignoring any wider output examples. Return one entry per input group, at most %d. Cite only lines inside the entry's own group's context_evidence_ids: a problem cites at least one code or test line there; a harmless group cites the lines that make it harmless; an unclear group may cite. At most 6 evidence IDs per entry; fields a classification does not use stay empty. Keep each tool, rule, line, raw grade, count and default severity as given: do not restate the tool's run status, counts or severity, assign no severity, and never describe absent diagnostics as clean code.`

const triageRepairRules = " Answer again only the groups listed in input.repair.groups."

type triageInput struct {
	Groups []triageGroupInput `json:"groups"`
	Scope  struct {
		Snapshot    string      `json:"snapshot_manifest_digest"`
		Excerpts    excerptList `json:"excerpts"`
		Limitations []string    `json:"limitations"`
	} `json:"scope"`
	Repair *triageRepair `json:"repair,omitempty"`
}

type triageGroupInput struct {
	ID              string            `json:"id"`
	Tools           []string          `json:"tools"`
	Rules           []string          `json:"rule_ids"`
	DefaultSeverity string            `json:"default_severity"`
	Count           int               `json:"diagnostics"`
	CrossTool       bool              `json:"cross_tool,omitempty"`
	Path            string            `json:"path"`
	Lines           []int             `json:"lines"`
	Examples        []triageDiagInput `json:"examples"`
	Context         []string          `json:"context_evidence_ids"`
}

type triageDiagInput struct {
	ObservationID string  `json:"observation_id"`
	Tool          string  `json:"tool"`
	RuleID        string  `json:"rule_id"`
	RawSeverity   *string `json:"raw_severity,omitempty"`
	Message       string  `json:"message"`
	StartLine     int     `json:"start_line"`
	EndLine       int     `json:"end_line"`
	// Related are the diagnostic's related locations (a note, a taint flow
	// step) inside the group's own context excerpts, in the tool's order.
	Related []obsRelatedInput `json:"related,omitempty"`
}

// triageRelatedShown bounds the related locations of one example: the
// first ones and the last (a flow's sink) are kept.
const triageRelatedShown = 8

// triageRelated is the related locations of o that lie in the group's
// context (its unit or the file header sent with it), each naming that
// excerpt; outside says some were left out because they lie elsewhere or
// past triageRelatedShown.
func triageRelated(o toolObservation, u triageUnit, unitID, headerID string) (out []obsRelatedInput, outside bool) {
	for _, r := range o.Related {
		l := r.Location
		id := ""
		switch {
		case l == nil || l.Path != u.entry.Path || l.BlobSHA256 != u.entry.SHA256:
		case u.span.start <= l.StartLine && l.EndLine <= u.span.end:
			id = unitID
		case headerID != "" && 1 <= l.StartLine && l.EndLine <= u.headerEnd():
			id = headerID
		}
		if id == "" {
			outside = true
			continue
		}
		out = append(out, obsRelatedInput{Kind: r.Kind, EvidenceID: id, StartLine: l.StartLine, EndLine: max(l.EndLine, l.StartLine)})
	}
	if len(out) > triageRelatedShown {
		out = append(out[:triageRelatedShown-1], out[len(out)-1])
		outside = true
	}
	return out, outside
}

type triageRepair struct {
	OutputErrors []string    `json:"output_errors"`
	Groups       []string    `json:"groups"`
	Items        []itemIssue `json:"items,omitempty"`
}

type triageAnswer struct {
	GroupID        string   `json:"group_id"`
	Classification string   `json:"classification"`
	Title          string   `json:"title"`
	Condition      string   `json:"condition"`
	Mechanism      string   `json:"mechanism"`
	Impact         string   `json:"impact"`
	Improvement    string   `json:"improvement"`
	Reason         string   `json:"reason"`
	Evidence       []string `json:"evidence_ids"`
}

// Output codes of the triage role (output_codes.go).
const (
	errTriageGroupUnknown   = "triage_group_unknown"
	errTriageGroupDuplicate = "triage_group_duplicate"
	errTriageClassInvalid   = "triage_classification_invalid"
	errTriageNotAtGroup     = "triage_evidence_not_at_group"
	errTriageAnswerMissing  = "triage_answer_missing"
)

// triageSlots is the planned slot count of a job (Plan and every slot use
// it).
func (h Handler) triageSlots(j analysis.Job) int {
	g := codeGroupsFor(j.Snapshot.Manifest, j.Scope, h.secondary(), changedPaths(j), true)
	return triageSlotCount(g.eligible)
}

// skipFile records why a triage slot did not run, for its verify task:
// "admitted", "budget_exhausted" or "<status>:<reason>".
func (s *roleStage) triageDecision(key, decision string) error {
	return os.WriteFile(admissionPath(s.j, key), []byte(decision), 0o600)
}

func (s *roleStage) triageSkip(key string, status domain.TaskStatus, reason string) analysis.TaskResult {
	if err := s.triageDecision(key, string(status)+":"+reason); err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	return result(status, reason)
}

// triageTask runs review.triage.tNNN or review.verify.tNNN.
func (s *roleStage) triageTask(key string) analysis.TaskResult {
	s.triage = key
	if s.role == record.StageVerify {
		b, err := readBounded(admissionPath(s.j, key), 128)
		switch {
		case err != nil:
			return result(domain.TaskSkippedPolicy, reasonDependency)
		case string(b) == decisionExhausted:
			return result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
		case string(b) == decisionAdmitted:
			return s.verify()
		}
		status, reason, _ := strings.Cut(string(b), ":")
		if !record.ValidCode(reason) {
			return result(domain.TaskSkippedPolicy, reasonDependency)
		}
		return result(domain.TaskStatus(status), reason)
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(key, "t"))
	docs, _, reason := s.toolFacts()
	if reason != "" {
		return s.triageSkip(key, domain.TaskSkippedPolicy, reason)
	}
	interpreted, uInt := s.interpretedObservations()
	plan := s.planTriage(docs, interpreted, s.h.triageSlots(s.j))
	if n > len(plan.bins) {
		return s.triageSkip(key, domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
	}
	if s.h.Caller == nil {
		return s.triageSkip(key, domain.TaskUnsupported, "model_not_configured")
	}
	// Admitted while max - used >= the pair's two calls.
	admitted, err := s.callsBelow(s.h.maxCalls(s.j.Scope)-record.SliceMinCalls+1, false)
	if errors.Is(err, errCallsCancelled) {
		return result(domain.TaskCancelled, "cancelled")
	}
	if err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	decision := decisionAdmitted
	if !admitted {
		decision = decisionExhausted
	}
	if err := s.triageDecision(key, decision); err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	if decision == decisionExhausted {
		return result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
	}
	upstream := []record.Upstream{}
	for _, d := range docs {
		upstream = append(upstream, d.upstream)
	}
	upstream = append(upstream, uInt)
	return s.runTriage(plan.bins[n-1], plan, upstream)
}

// interpretedObservations are the observations an accepted interpret
// record's candidates cover, which the triage does not send again.
func (s *roleStage) interpretedObservations() (map[string]bool, record.Upstream) {
	interp, up := s.prior(record.StageInterpret)
	interpreted := map[string]bool{}
	if up.State == record.UpstreamAccepted {
		for _, c := range interp.Candidates {
			for _, id := range c.ObservationIDs {
				interpreted[id] = true
			}
		}
	}
	return interpreted, up
}

// triageShareCalls is the call count per held triage pair: the triage
// share is one pair (record.SliceMinCalls) for every triageShareCalls calls
// the whole-run roles left, at least one pair.
const triageShareCalls = 16

func triageHoldPath(j analysis.Job) string { return filepath.Join(j.Scratch, "review.triage.hold") }

// triageHold is the part of the call ceiling the module slices and the
// contract follow-up leave for the diagnostic triage slots, which run after
// them (docs/REVIEW.md#호출-비용). The slices come first: when every slice
// with lines left fits in the calls the whole-run roles left (its
// admission reserve each), nothing is held and the triage gets the calls
// the slices did not use. Only when they cannot all fit is a bounded share
// held: one pair per triageShareCalls calls left, at least one (the first
// slot holds the highest-severity groups), never more than the slots that
// have groups. It is decided once per attempt, at the first slice or
// contract admission, and kept next to the call counter; without an
// accepted scope artifact, a model or diagnostics nothing is held.
func (s *roleStage) triageHold() int {
	if l := ledgerOf(s.ctx); l != nil {
		// Concurrent slices decide it once.
		l.holdMu.Lock()
		defer l.holdMu.Unlock()
	}
	if b, err := readBounded(triageHoldPath(s.j), 32); err == nil {
		if n, err := strconv.Atoi(string(b)); err == nil && n >= 0 {
			return n
		}
	}
	n := s.decideTriageHold()
	if err := os.WriteFile(triageHoldPath(s.j), []byte(strconv.Itoa(n)), 0o600); err != nil {
		return 0
	}
	return n
}

func (s *roleStage) decideTriageHold() int {
	slots := s.h.triageSlots(s.j)
	if s.h.Caller == nil || slots == 0 {
		return 0
	}
	doc, up := s.readScope()
	if up.State != record.UpstreamAccepted {
		return 0
	}
	left := s.h.maxCalls(s.j.Scope) - doc.Budget.CallsBeforeSlices
	need := 0
	for _, sl := range doc.Slices {
		if slices.ContainsFunc(sl.Files, func(f record.ScopeFile) bool { return f.State == record.FilePlanned }) {
			need += doc.Budget.SliceReserveCalls
		}
	}
	if need <= left {
		return 0
	}
	docs, _, reason := s.toolFacts()
	if reason != "" {
		return 0
	}
	interpreted, _ := s.interpretedObservations()
	plan := s.planTriage(docs, interpreted, slots)
	return min(record.SliceMinCalls*max(1, left/triageShareCalls), record.SliceMinCalls*len(plan.bins))
}

// triageSent is one group as a request carries it.
type triageSent struct {
	id      string
	group   *triageGroup
	context []string
	input   triageGroupInput
}

// triageOutcome is the state of one sent group after a parse.
type triageOutcome struct {
	answer triageAnswer
	text   record.CandidateText // a problem's candidate text
	ids    []string             // a harmless or unclear group's evidence
	lims   []string
	// omitted is the code of a group the platform left without a
	// classification after the repair.
	omitted string
	// form: the answer's faults were all of form, so a group the repair
	// answer then left out is omitted as form (triage_item_omitted).
	form bool
}

func (s *roleStage) runTriage(bin []*triageGroup, plan triagePlan, upstream []record.Upstream) analysis.TaskResult {
	if r := s.begin(upstream); r != nil {
		return *r
	}
	s.noteUpstream(upstream[len(upstream)-1])
	s.rec.Limitations = addLim(s.rec.Limitations, plan.lims...)
	sent, excerpts := s.triageRequest(bin)
	instructions := s.instructions(fmt.Sprintf(triageContract, triageGroupLimit(s.h.Budget)))
	build := func(list []triageSent, r *triageRepair) ([]byte, error) {
		var in triageInput
		in.Scope.Snapshot, in.Scope.Limitations, in.Repair = s.j.Snapshot.Digest, s.rec.Limitations, r
		in.Groups = make([]triageGroupInput, len(list))
		used := map[string]bool{}
		for i, g := range list {
			in.Groups[i] = g.input
			for _, id := range g.context {
				used[id] = true
			}
		}
		for _, x := range excerpts {
			if used[x.ID] {
				in.Scope.Excerpts = append(in.Scope.Excerpts, x)
			}
		}
		return marshal(in)
	}
	// The groups travel as a prefix that fits with room for a repair: the
	// last ones in priority order are left out first.
	for len(sent) > 0 {
		body, err := build(sent, nil)
		if err != nil {
			return *s.fail(result(domain.TaskFailed, "review_input_unavailable"))
		}
		if _, ok := s.fits(instructions+triageRepairRules, body, true); ok {
			break
		}
		sent = sent[:len(sent)-1]
		s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed)
	}
	if len(sent) == 0 {
		return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
	}
	var kept []record.Excerpt
	used := map[string]bool{}
	for _, g := range sent {
		for _, id := range g.context {
			used[id] = true
		}
		for _, d := range g.group.diags {
			o := d.obs
			s.rec.Observations = append(s.rec.Observations, record.ObservationRef{ID: o.ID, TaskKey: d.task, Tool: o.Tool, RuleID: o.RuleID, Path: o.Location.Path, StartLine: o.Location.StartLine})
		}
	}
	for _, x := range excerpts {
		if used[x.ID] {
			kept = append(kept, x)
		}
	}
	s.setEvidence(kept)
	byID := map[string]triageSent{}
	for _, g := range sent {
		byID[g.id] = g
	}
	done := map[string]triageOutcome{}
	faults := map[string]triageOutcome{}
	cited := map[string]record.Excerpt{}
	var citedOrder []string
	keepCited := func(reply *citer, lists ...[]string) {
		for _, x := range reply.used(lists...) {
			if _, ok := cited[x.ID]; !ok {
				cited[x.ID] = x
				citedOrder = append(citedOrder, x.ID)
			}
		}
	}
	// dropped are groups a retry after an answer that was not JSON no
	// longer asks for (left out as form).
	dropped := map[string]bool{}
	pending := func() []string {
		var out []string
		for _, g := range sent {
			if _, ok := done[g.id]; !ok && !dropped[g.id] {
				out = append(out, g.id)
			}
		}
		return out
	}
	// parse reads one output. final resolves faults of form (or leaves the
	// group out) and names a group whose integrity fault remains; before
	// that every fault goes to the repair.
	parse := func(raw json.RawMessage, final bool) (accepted int, codes []string, issues []itemIssue, schemaErr bool) {
		var out struct {
			Triage []triageAnswer `json:"triage"`
		}
		if errs := decodeOutput(raw, &out, "triage"); errs != nil {
			return 0, errs, nil, true
		}
		reply := newCiter(s.j.Snapshot.Digest, kept)
		seen := map[string]bool{}
		for i, a := range out.Triage {
			g, ok := byID[a.GroupID]
			if !ok {
				codes = addLim(codes, errTriageGroupUnknown)
				issues = append(issues, itemIssue{List: "triage", Index: i + 1, Field: "group_id", Error: errTriageGroupUnknown})
				continue
			}
			// A second answer for a group in one output is a fault even when
			// the first was accepted; an answer for a group an earlier
			// output settled is not read again.
			if seen[g.id] {
				codes = addLim(codes, errTriageGroupDuplicate)
				issues = append(issues, itemIssue{List: "triage", Index: i + 1, Field: "group_id", Error: errTriageGroupDuplicate})
				continue
			}
			seen[g.id] = true
			if _, ok := done[g.id]; ok {
				continue
			}
			o, itemCodes, itemIssues := s.checkTriageAnswer(a, g, i+1, reply, final)
			codes = addLim(codes, itemCodes...)
			issues = append(issues, itemIssues...)
			switch {
			case o.omitted == "" && len(itemCodes) == 0, o.omitted == "" && final:
				done[g.id] = o
				delete(faults, g.id)
				accepted++
				keepCited(reply, o.text.Evidence, o.ids)
			default:
				o.form = formOnly(itemCodes)
				faults[g.id] = o
			}
		}
		for _, id := range pending() {
			if !seen[id] {
				codes = addLim(codes, errTriageAnswerMissing)
				issues = append(issues, itemIssue{List: "groups", Index: slices.IndexFunc(sent, func(g triageSent) bool { return g.id == id }) + 1, Field: "group_id", Error: errTriageAnswerMissing})
			}
		}
		return accepted, codes, issues, false
	}
	var repair *triageRepair
	var lastOutput json.RawMessage
	lastCall, lastFinal := -1, false
	repaired, retried, proofRetried := false, false, false
	var failed *analysis.TaskResult
	resolvedLate := false
	// A cancellation ends the loop like a failure: the record still holds
	// the calls made and the groups sent, all left out.
	cancelled := result(domain.TaskCancelled, "cancelled")
	for attempt := 1; attempt <= record.RoleMaxCalls; attempt++ {
		if s.ctx.Err() != nil {
			failed = &cancelled
			break
		}
		// A repair carries only the groups still without an accepted
		// answer, with their excerpts.
		list := sent
		if repair != nil {
			list = slices.DeleteFunc(slices.Clone(sent), func(g triageSent) bool { return !slices.Contains(repair.Groups, g.id) })
		}
		body, err := build(list, repair)
		if err != nil {
			r := result(domain.TaskFailed, "review_input_unavailable")
			failed = &r
			break
		}
		text := instructions
		if repair != nil {
			text += repairRules + triageRepairRules
		}
		q := opencode.Request{Role: s.role, Scratch: s.j.Scratch, SnapshotDigest: s.j.Snapshot.Digest, Instructions: text, Input: body, ProtectedRoots: []string{s.j.Snapshot.SnapshotDir}}
		size := len(opencode.Prompt(q))
		if size > s.limit().request {
			r := result(domain.TaskFailed, reasonInputTooLarge)
			failed = &r
			break
		}
		if r := s.reserveCall(); r != nil {
			failed = r
			break
		}
		reply := s.h.Caller.Review(s.ctx, s.runner, q)
		call := s.recordCall(reply, size, attempt)
		if reply.Reason != "" && reply.Reason != "model_output_invalid" {
			call.Outcome = reply.Reason
			if !record.ValidCode(call.Outcome) {
				call.Outcome = "model_unavailable"
			}
			s.rec.Calls = append(s.rec.Calls, call)
			if reply.WorkspaceProofMismatch() && s.ctx.Err() == nil && !proofRetried && attempt < record.RoleMaxCalls {
				proofRetried = true
				continue
			}
			if s.ctx.Err() != nil {
				failed = &cancelled
				break
			}
			r := failure(reply)
			failed = &r
			break
		}
		if reply.Reason != "" {
			call.Outcome, call.OutputErrors = callRejected, []string{"output_not_json"}
			s.rec.Calls = append(s.rec.Calls, call)
			// An output that is not JSON is most often an answer the output
			// token limit cut: each retry, within the role's calls, asks for
			// the first half of the remaining groups and leaves the rest out
			// as a fault of form. One group that still gets no JSON ends it.
			if retried && (repair == nil || len(repair.Groups) <= 1) {
				break
			}
			retried = true
			if repair == nil {
				repair = &triageRepair{Groups: pending()}
			}
			if len(repair.Groups) > 1 {
				keep := (len(repair.Groups) + 1) / 2
				for _, id := range repair.Groups[keep:] {
					dropped[id] = true
					if _, held := faults[id]; !held {
						faults[id] = triageOutcome{form: true}
					}
				}
				repair.Groups = slices.Clone(repair.Groups[:keep])
			}
			repair.OutputErrors = addLim(append([]string{}, repair.OutputErrors...), "output_not_json")
			continue
		}
		final := repaired || attempt == record.RoleMaxCalls
		n, codes, issues, schemaErr := parse(reply.Output, final)
		call.OutputErrors = nonNil(addLim(nil, codes...))
		call.Outcome = callRejected
		if n > 0 {
			call.Outcome = callAccepted
		}
		s.rec.Calls = append(s.rec.Calls, call)
		s.noteExecutionClaim(call)
		if !schemaErr {
			lastOutput, lastCall, lastFinal = reply.Output, len(s.rec.Calls)-1, final
			if final && len(codes) > 0 && n > 0 {
				resolvedLate = true
			}
		}
		if len(pending()) == 0 || (final && !schemaErr) || repaired {
			break
		}
		repaired = true
		repair = &triageRepair{OutputErrors: codes, Groups: pending(), Items: issues}
	}
	// A repair that could not be sent or answer: the last parsed output is
	// read once more with the faults of form resolved.
	if lastOutput != nil && !lastFinal && len(pending()) > 0 && (failed == nil || failed.Status != domain.TaskCancelled) {
		if n, _, _, _ := parse(lastOutput, true); n > 0 {
			resolvedLate = true
			s.rec.Calls[lastCall].Outcome = callAccepted
			s.rec.Limitations = addLim(s.rec.Limitations, limRepairNotSent)
		}
	}
	if resolvedLate {
		s.rec.Limitations = addLim(s.rec.Limitations, record.LimOutputDegraded)
	}
	// A cancelled slot keeps no classification: its record fails.
	if failed != nil && failed.Status == domain.TaskCancelled {
		clear(done)
	}
	// The groups the slot sent, with what was accepted for each.
	var groups []record.TriageGroup
	var candidates []record.Candidate
	omitted := false
	for _, g := range sent {
		tg := record.TriageGroup{ID: g.id, ContextIDs: append([]string{}, g.context...), CrossTool: g.group.crossTool}
		for _, d := range g.group.diags {
			tg.ObservationIDs = append(tg.ObservationIDs, d.obs.ID)
		}
		o, ok := done[g.id]
		switch {
		case !ok:
			tg.Omitted = record.TriageAnswerMissing
			if f, faulted := faults[g.id]; faulted && f.omitted != "" {
				tg.Omitted = f.omitted
			} else if faulted && f.form {
				tg.Omitted = record.TriageItemOmitted
			} else if faulted {
				tg.Omitted = record.TriageItemRejected
			}
			omitted = true
		case o.answer.Classification == record.TriageProblem:
			tg.Classification = record.TriageProblem
			c := record.Candidate{CandidateText: o.text, Origin: record.StageTriage, ObservationIDs: append([]string{}, tg.ObservationIDs...)}
			c.ID = fmt.Sprintf("triage_%d", len(candidates)+1)
			tg.CandidateID = c.ID
			candidates = append(candidates, c)
		default:
			tg.Classification, tg.Reason, tg.EvidenceIDs = o.answer.Classification, o.answer.Reason, o.ids
		}
		if ok {
			s.rec.Limitations = addLim(s.rec.Limitations, o.lims...)
		}
		groups = append(groups, tg)
	}
	if omitted {
		s.rec.Limitations = addLim(s.rec.Limitations, record.LimTriageGroupsNotTriaged)
	}
	s.rec.TriageGroups = groups
	var extra []record.Excerpt
	for _, id := range citedOrder {
		extra = append(extra, cited[id])
	}
	s.rec.Evidence = withCited(s.rec.Evidence, textless(extra))
	if len(done) == 0 {
		for i := range s.rec.TriageGroups {
			if s.rec.TriageGroups[i].Omitted == "" {
				s.rec.TriageGroups[i].Omitted = record.TriageAnswerMissing
			}
		}
		if failed != nil {
			return *s.fail(*failed)
		}
		return *s.fail(result(domain.TaskFailed, reasonOutputInvalid))
	}
	s.rec.Candidates = candidates
	return s.finish()
}

// triageRequest builds the request groups and the excerpts they name: each
// group's unit and, for a unit that does not start its file, the file's
// header (once per file, and never when the file's first unit is sent).
func (s *roleStage) triageRequest(bin []*triageGroup) ([]triageSent, []record.Excerpt) {
	var excerpts []record.Excerpt
	have := map[string]bool{}
	first := map[string]bool{}
	for _, g := range bin {
		if g.unit.span.start == 1 {
			first[g.unit.entry.Path] = true
		}
	}
	add := func(u triageUnit, start, end int, origin string) string {
		e := u.entry
		id := record.ExcerptID(s.j.Snapshot.Digest, e.Path, e.SHA256, start, end)
		if !have[id] {
			have[id] = true
			excerpts = append(excerpts, record.Excerpt{ID: id, Path: e.Path, SHA256: e.SHA256, Start: start, End: end, Kind: u.kind, Origin: origin,
				Text: strings.Join(u.text[start-1:end], "\n")})
		}
		return id
	}
	var out []triageSent
	for i, g := range bin {
		t := triageSent{id: fmt.Sprintf("g%d", i+1), group: g}
		headerID := ""
		if h := g.unit.headerEnd(); h > 0 && !first[g.unit.entry.Path] {
			headerID = add(g.unit, 1, h, record.OriginHeader)
			t.context = append(t.context, headerID)
		}
		unitID := add(g.unit, g.unit.span.start, g.unit.span.end, "context")
		t.context = append([]string{unitID}, t.context...)
		in := triageGroupInput{ID: t.id, Tools: g.analyzers(), Rules: g.rules(), DefaultSeverity: g.severity, Count: len(g.diags), CrossTool: g.crossTool,
			Path: g.unit.entry.Path, Context: t.context, Examples: []triageDiagInput{}}
		if in.DefaultSeverity == "" {
			in.DefaultSeverity = "low"
		}
		for j, d := range g.diags {
			in.Lines = append(in.Lines, d.obs.Location.StartLine)
			if j < triageShownDiagnostics {
				// The interpretation's rule for tool messages (shownMessage).
				msg, lim := s.shownMessage(d.obs)
				if lim != "" {
					s.rec.Limitations = addLim(s.rec.Limitations, lim)
				}
				related, outside := triageRelated(d.obs, g.unit, unitID, headerID)
				if outside {
					s.rec.Limitations = addLim(s.rec.Limitations, "observation_context_outside_input")
				}
				in.Examples = append(in.Examples, triageDiagInput{ObservationID: d.obs.ID, Tool: d.obs.Tool, RuleID: d.obs.RuleID, RawSeverity: d.obs.Severity,
					Message: msg, StartLine: d.obs.Location.StartLine, EndLine: max(d.obs.Location.EndLine, d.obs.Location.StartLine), Related: related})
			}
		}
		t.input = in
		out = append(out, t)
	}
	// Files in path order, a file's header before its units.
	slices.SortStableFunc(excerpts, func(a, b record.Excerpt) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		return a.Start - b.Start
	})
	return out, excerpts
}

// checkTriageAnswer checks one entry for group g. final: the repair is
// spent, so a fault of form is resolved in the entry or the group is left
// out (triage_item_omitted) and an integrity fault rejects it
// (triage_item_rejected).
func (s *roleStage) checkTriageAnswer(a triageAnswer, g triageSent, index int, reply *citer, final bool) (triageOutcome, []string, []itemIssue) {
	o := triageOutcome{answer: a}
	var codes []string
	var issues []itemIssue
	note := func(field, code, id string) {
		codes = addLim(codes, code)
		issues = append(issues, itemIssue{List: "triage", Index: index, Field: field, ID: id, Error: code})
	}
	settle := func() (triageOutcome, []string, []itemIssue) {
		if len(codes) > 0 && final && o.omitted == "" {
			o.omitted = record.TriageItemRejected
			if formOnly(codes) {
				o.omitted = record.TriageItemOmitted
			}
		}
		return o, codes, issues
	}
	// atGroup keeps the citations of raw that name the group's own excerpts
	// (or an ID the request did not hold, which the ID check reports);
	// outside says another group's excerpt was cited. That is a fault of
	// form: the repair asks again, and after it only the group's own
	// citations are kept.
	atGroupOnly := func(raw []string) (in []string, outside bool) {
		if raw == nil {
			return nil, false
		}
		_, base, _ := reply.resolveIssues(raw, citeNarrow)
		in = []string{}
		for i, b := range base {
			if _, sent := reply.sent[b]; sent && !slices.Contains(g.context, b) {
				outside = true
				continue
			}
			in = append(in, raw[i])
		}
		return in, outside
	}
	raw := a.Evidence
	if in, outside := atGroupOnly(raw); outside {
		note("evidence_ids", errTriageNotAtGroup, "")
		if final {
			raw = in
		}
	}
	switch a.Classification {
	case record.TriageProblem:
		c := record.CandidateText{Title: a.Title, Condition: a.Condition, Impact: a.Impact, Improvement: a.Improvement, Counterpoint: a.Reason,
			Mechanism: a.Mechanism, Evidence: raw}
		atGroup := func(base []string) []record.FieldProblem {
			for _, id := range base {
				if slices.Contains(g.context, id) {
					return nil
				}
			}
			return []record.FieldProblem{{Field: "evidence_ids", Code: errTriageNotAtGroup}}
		}
		chk := s.checkCandidate(c, "triage", index, reply, a.Mechanism != "", final, record.TriageItemOmitted, atGroup)
		codes, issues = addLim(codes, chk.codes...), append(issues, chk.issues...)
		if chk.keep {
			o.text, o.lims = chk.text, chk.lims
			if len(codes) == 0 || final {
				return o, codes, issues
			}
		}
		return settle()
	case record.TriageHarmless, record.TriageUnclear:
		textOK := len(s.textErrors(a.Reason)) == 0
		for _, code := range s.textErrors(a.Reason) {
			note("reason", code, "")
		}
		kinds := []string{"code", "test"}
		if a.Classification == record.TriageUnclear {
			if len(raw) == 0 {
				// Its citations are optional: after the repair an answer
				// whose only fault was citing other groups keeps none.
				if final && len(codes) > 0 && formOnly(codes) && textOK {
					return o, codes, issues
				}
				return settle()
			}
			kinds = append(kinds, "document")
		}
		ids, _, citeIssues := reply.resolveIssues(raw, citeNarrow)
		for _, is := range citeIssues {
			note("evidence_ids", is.code, is.id)
		}
		for _, code := range idErrors(ids, reply.catalog(), 6, kinds...) {
			note("evidence_ids", code, "")
		}
		if len(codes) == 0 {
			o.ids = ids
			return o, nil, nil
		}
		if final && formOnly(codes) && textOK {
			ids, lims, errs := reply.degrade(raw, citeNarrow, 6, nil)
			if len(errs) == 0 && !slices.Contains(lims, limCitationUnspecified) && len(idErrors(ids, reply.catalog(), 6, kinds...)) == 0 {
				o.ids, o.lims = ids, lims
				return o, codes, issues
			}
		}
		return settle()
	}
	note("classification", errTriageClassInvalid, "")
	return settle()
}
