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
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Leads of an investigation and their follow-up
// (docs/REVIEW.md#단서-후속-조사).
//
// An investigation whose request leaves files or lines of its scope out is
// offered up to record.MaxLeads leads besides its candidates (the record's
// lead_limit). A lead follows the open premise of one of its candidates;
// leads are optional items, like strengths: one that fails its check or
// names no candidate is left out (review_lead_omitted) and is never named in
// a format repair. The platform resolves the kept leads from the snapshot
// (leads_resolve.go) and records them on the investigate record (schema
// 0.9).
//
// The follow-up task of that investigation (review.leads for the overview,
// review.leads.sNNN for a slot) is always planned between it and its
// verify. It makes no call and ends skipped_policy with a reason that never
// makes the stage partial (domain.LeadsNotRun) when the request offered no
// leads (leads_not_offered), the investigation named none (no_leads), none
// resolved (no_leads_resolved), the ceiling refused its slot
// (leads_slot_not_run) or the ceiling has no room for it
// (leads_not_admitted). It is admitted only while the ceiling leaves, after
// both of its calls, the worst case of all the existing work after it
// (leadsCallLimit): its verify, the contract follow-up when the overview has
// a contract probe, the investigate and verify of every later slot with
// planned lines and the triage and verify of every diagnostic triage bin,
// each at its call cap, and for the overview the whole-run bound with its
// verify. Follow-ups are dropped first, so no existing task loses a call.
// One request (record.LeadsMaxCalls: one format repair or one retry of a
// reply that is not JSON) carries the resolved leads, the candidates they
// concern, the cited lines of both and the resolved excerpts. Its result
// refines or contests candidates and may add one per lead; a follow-up never
// removes a candidate. A contested candidate goes to verify unchanged with
// the follow-up's reason and cited lines as counter-evidence. The record
// holds the final candidates and one disposition for each, and the verify of
// the same investigation checks those. A follow-up that fails leaves verify
// with the investigation's candidates.

const limLeadOmitted = "review_lead_omitted"

// limLeadResultOmitted: a result of the follow-up still invalid after the
// repair was left out; its candidate stays as the investigation gave it.
const limLeadResultOmitted = "lead_result_omitted"

// Output errors of the follow-up's results.
const (
	errLeadResultInvalid   = "lead_result_invalid"
	errLeadResultDuplicate = "lead_result_duplicate"
	// A contest cites no line of a whole resolved declaration of a lead of
	// its candidate that the investigation did not send (a partial window
	// cannot show the condition never happens).
	errLeadContestLines = "lead_contest_evidence_not_resolved"
	// An added candidate cites no line of its lead's resolved code that the
	// investigation did not send.
	errLeadAdditionLines = "lead_addition_evidence_not_new"
	// A refinement drops a citation of the candidate it refines: it may
	// rewrite the text from new code, not leave the code it started from.
	errLeadRefinementCitations = "lead_refinement_citations_dropped"
	// A refinement keeps the candidate's citations but not first and in
	// order, which would move the finding's location.
	errLeadRefinementOrder = "lead_refinement_citations_reordered"
	// A refinement adds no citation, or adds one outside the whole resolved
	// declarations of one lead of its candidate, or none of its new
	// citations holds a line the investigation did not send.
	errLeadRefinementLines = "lead_refinement_evidence_not_resolved"
	// A refinement adds more than record.MaxRefinementNewEvidence citations.
	errLeadRefinementOverCap = "lead_refinement_citations_over_cap"
)

// investigateLeadsContract offers leads; it travels only in a request that
// offers them (scope.lead_limit), so a request that offers none carries the
// same instructions as before leads existed.
const investigateLeadsContract = ` Optionally return up to %d "leads":[{"need":"definition|callers","evidence_id":"ev_...:40-42","symbol":"Name","suspicion":"the open premise and what that code decides","candidate":1}]. A lead is no finding: write the suspicion as a candidate first with its unconfirmed premise, and give a lead only when that premise depends on code this request lacks; candidate is the 1-based position of that candidate (required). If the sent code decides the point, write the candidate without a lead. evidence_id cites the sent code lines holding symbol, one identifier: for definition the callee of a call on those lines, for callers the declaration there. The platform finds only project code: a project function, a method of a concrete project type the code fixes as the receiver's type, and a package-qualified call into a project package. It finds no standard library or external module code and no call through an interface or a value of unknown type: do not name those. Never name code this request carries.`

const leadsContract = `For this role use exactly {"results":[{"basis":"investigate_1 or lead_1","disposition":"refined|contested|added","title":"what goes wrong","condition":"the concrete trigger input or state","mechanism":"how the cited lines produce the failure, step by step","impact":"...","improvement":"fix direction","counterpoint":"what would refute it","evidence_ids":["ev_...:12-30"],"reason":"contested only: why the cited lines show the condition cannot happen"}]}. At most one result per basis. For an input candidate (basis its id) give refined with the complete corrected candidate, keeping every evidence ID the input candidate cites first and in the same order and adding one or two after them, each inside a resolved excerpt of one lead of that candidate that is not in the lead's partial_evidence_ids (so a refined candidate cites at most 8), or contested with only reason and 1-6 code evidence_ids, one of them inside a resolved excerpt of a lead of that candidate that is not in the lead's partial_evidence_ids, that show the condition cannot happen: the candidate stays and verify weighs the contest; omit a candidate that stays as it is. For a lead (basis the lead id) give at most one added candidate for another problem the resolved code shows, citing a resolved excerpt of that lead. An added candidate cites 1-6 evidence IDs of scope.excerpts; every candidate cites at least one code or test. Do not claim reproduction or provide attack instructions.`

// leadOutput is one lead as the investigate output gives it.
type leadOutput struct {
	Need       string `json:"need"`
	EvidenceID string `json:"evidence_id"`
	Symbol     string `json:"symbol"`
	Suspicion  string `json:"suspicion"`
	Candidate  int    `json:"candidate"`
}

// leadsOffered: the investigation's code leaves files or lines of its scope
// out, so a lead can name code the request does not carry.
func leadsOffered(lims []string) bool {
	return slices.Contains(lims, "files_outside_review_context") || slices.Contains(lims, "excerpt_only")
}

// checkLeads keeps the valid leads of an output in order, at most
// record.MaxLeads; omitted reports one left out (or given unoffered).
// keptAs maps an output candidate's position to its kept ID.
func (s *roleStage) checkLeads(out []leadOutput, offered bool, reply *citer, keptAs map[int]string) ([]record.Lead, bool) {
	if !offered {
		return nil, len(out) > 0
	}
	var leads []record.Lead
	omitted := false
	for _, x := range out {
		if len(leads) == record.MaxLeads {
			omitted = true
			break
		}
		l, ok := s.checkLead(x, reply, keptAs)
		if !ok || slices.ContainsFunc(leads, func(y record.Lead) bool {
			return y.Need == l.Need && y.Symbol == l.Symbol && y.EvidenceID == l.EvidenceID
		}) {
			omitted = true
			continue
		}
		l.ID = fmt.Sprintf("lead_%d", len(leads)+1)
		leads = append(leads, l)
	}
	return leads, omitted
}

func (s *roleStage) checkLead(x leadOutput, reply *citer, keptAs map[int]string) (record.Lead, bool) {
	symbol := strings.TrimSuffix(strings.TrimSpace(x.Symbol), "()")
	if i := strings.LastIndexByte(symbol, '.'); i >= 0 {
		symbol = symbol[i+1:]
	}
	need := strings.TrimSpace(x.Need)
	if (need != record.LeadNeedDefinition && need != record.LeadNeedCallers) || !record.ValidEvidenceSymbol(symbol) ||
		record.Sensitive([]byte(symbol)) || len(s.textErrors(x.Suspicion)) > 0 {
		return record.Lead{}, false
	}
	ids, _, errs := reply.resolve([]string{x.EvidenceID}, citeNarrow)
	if len(errs) > 0 || len(ids) != 1 {
		return record.Lead{}, false
	}
	e, ok := reply.catalog()[ids[0]]
	if !ok || (e.Kind != "code" && e.Kind != "test") {
		return record.Lead{}, false
	}
	// A lead follows a candidate's premise: one without a candidate would
	// lose its suspicion when it is not resolved or not followed.
	id, ok := keptAs[x.Candidate]
	if !ok {
		return record.Lead{}, false
	}
	return record.Lead{Need: need, Symbol: symbol, EvidenceID: ids[0], Suspicion: x.Suspicion, CandidateID: id}, true
}

// ---- the follow-up task -----------------------------------------------------

// leadsTask runs review.leads (slot "") or review.leads.sNNN.
func (s *roleStage) leadsTask(slot string) analysis.TaskResult {
	name := record.StageInvestigate
	var doc record.ScopeDoc
	if slot != "" {
		var up record.Upstream
		doc, up = s.readScope()
		if up.State != record.UpstreamAccepted {
			return result(domain.TaskSkippedPolicy, reasonDependency)
		}
		i := slices.IndexFunc(doc.Slices, func(sl record.ScopeSlice) bool { return sl.Key == slot })
		if i < 0 || !slices.ContainsFunc(doc.Slices[i].Files, func(f record.ScopeFile) bool { return f.State == record.FilePlanned }) {
			return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
		}
		// The ceiling refused the slot: there is no investigation to follow.
		if b, err := readBounded(admissionPath(s.j, slot), 64); err == nil && string(b) == decisionExhausted {
			return result(domain.TaskSkippedPolicy, record.ReasonLeadsSlotNotRun)
		}
		s.slice, s.sliceLimits, s.scopeUp, s.sliceReserve = &doc.Slices[i], doc.Limits, up, doc.Budget.SliceReserveCalls
		name += "." + slot
	}
	inv, uInv := s.prior(name)
	switch {
	case uInv.State != record.UpstreamAccepted:
		return result(domain.TaskSkippedPolicy, reasonDependency)
	case len(inv.Leads) == 0 && inv.LeadLimit == 0:
		return result(domain.TaskSkippedPolicy, record.ReasonLeadsNotOffered)
	case len(inv.Leads) == 0:
		return result(domain.TaskSkippedPolicy, record.ReasonNoLeads)
	case !slices.ContainsFunc(inv.Leads, record.Lead.Resolved):
		return result(domain.TaskSkippedPolicy, record.ReasonNoLeadsResolved)
	case s.h.Caller == nil:
		return result(domain.TaskUnsupported, "model_not_configured")
	}
	// Admitted when both calls fit below the limit; the admission check
	// counts nothing and each call is counted against the same limit
	// (reserveCall).
	s.leadsLimit = s.leadsCallLimit(doc, slot, inv)
	admitted, err := s.callsBelow(s.leadsLimit-(record.LeadsMaxCalls-1), false)
	switch {
	case errors.Is(err, errCallsCancelled):
		return result(domain.TaskCancelled, "cancelled")
	case err != nil:
		return result(domain.TaskFailed, reasonCallCountFailed)
	case !admitted:
		return result(domain.TaskSkippedPolicy, record.ReasonLeadsNotAdmitted)
	}
	upstream := []record.Upstream{uInv}
	if slot != "" {
		upstream = append(upstream, s.scopeUp)
	}
	return s.leadsFollowup(inv, upstream)
}

// leadsCallLimit is the count of calls below which a follow-up may call:
// the ceiling less the worst case of the existing work after it (see the
// package comment above), every task at its call cap, so a follow-up never
// takes a call a later task could use however many repairs and retries it
// needs. The overview follow-up runs before review.scope and counts the
// slots the scope will give lines from the same computation (scopeDoc);
// when that fails every planned slot keeps its worst case. It also keeps the
// whole-run bound for itself and its verify, so calls_before_slices and the
// ceiling's minimum do not change.
func (s *roleStage) leadsCallLimit(doc record.ScopeDoc, slot string, inv record.Record) int {
	reserve := record.TaskMaxCalls(sliceTaskKey(record.StageVerify, slot))
	overview := inv
	if slot != "" {
		overview, _ = s.prior(record.StageInvestigate)
	}
	if overview.ContractProbe != nil {
		reserve += record.TaskMaxCalls(record.TaskContract)
	}
	unit := func(key string) int {
		return record.TaskMaxCalls(sliceTaskKey(record.StageInvestigate, key)) + record.TaskMaxCalls(sliceTaskKey(record.StageVerify, key))
	}
	overviewTask := slot == ""
	if overviewTask {
		planner := *s
		planner.rec = record.Record{}
		scope, r := planner.scopeDoc()
		if r != nil {
			// Without the computation every planned slot keeps its worst case.
			for i := range s.h.plannedSlots(s.ctx, s.j) {
				reserve += unit(fmt.Sprintf("s%03d", i+1))
			}
		}
		doc = scope
	}
	// Every later slot with planned lines (every one for the overview).
	after := overviewTask
	for _, sl := range doc.Slices {
		if after && slices.ContainsFunc(sl.Files, func(f record.ScopeFile) bool { return f.State == record.FilePlanned }) {
			reserve += unit(sl.Key)
		}
		after = after || sl.Key == slot
	}
	for i := range s.triageBins() {
		key := fmt.Sprintf("t%03d", i+1)
		reserve += record.TaskMaxCalls(record.StageTriage+"."+key) + record.TaskMaxCalls(record.StageVerify+"."+key)
	}
	limit := s.h.maxCalls(s.j.Scope) - reserve
	if overviewTask {
		limit = min(limit, record.WholeRunMaxCalls-record.TaskMaxCalls(record.StageVerify))
	}
	return limit
}

// sliceTaskKey is the task of a role for a slot ("" for the whole run).
func sliceTaskKey(role, slot string) string {
	if slot == "" {
		return "review." + role
	}
	return "review." + role + "." + slot
}

func triageBinsPath(j analysis.Job) string { return filepath.Join(j.Scratch, "review.leads.triage") }

// triageBins is the number of diagnostic triage bins the attempt's triage
// slots will have, decided once and kept next to the call counter: none
// without a model, triage slots or readable analyzer results.
func (s *roleStage) triageBins() int {
	if l := ledgerOf(s.ctx); l != nil {
		l.holdMu.Lock()
		defer l.holdMu.Unlock()
	}
	if b, err := readBounded(triageBinsPath(s.j), 32); err == nil {
		if n, err := strconv.Atoi(string(b)); err == nil && n >= 0 {
			return n
		}
	}
	n := 0
	if slots := s.h.triageSlots(s.j); slots > 0 && s.h.Caller != nil {
		if docs, _, reason := s.toolFacts(); reason == "" {
			interpreted, _ := s.interpretedObservations()
			n = len(s.planTriage(docs, interpreted, slots).bins)
		}
	}
	_ = os.WriteFile(triageBinsPath(s.j), []byte(strconv.Itoa(n)), 0o600)
	return n
}

// leadInput is one lead as the follow-up request carries it.
type leadInput struct {
	ID          string   `json:"id"`
	Need        string   `json:"need"`
	EvidenceID  string   `json:"evidence_id"`
	Symbol      string   `json:"symbol"`
	Suspicion   string   `json:"suspicion"`
	CandidateID string   `json:"candidate_id"`
	Resolved    []string `json:"resolved_evidence_ids"`
	// Partial are the resolved definition windows that are not the whole
	// declaration.
	Partial []string `json:"partial_evidence_ids,omitempty"`
}

type leadsInput struct {
	Scope struct {
		Snapshot    string      `json:"snapshot_manifest_digest"`
		Subject     string      `json:"subject,omitempty"`
		Excerpts    excerptList `json:"excerpts"`
		Limitations []string    `json:"limitations"`
	} `json:"scope"`
	Leads      []leadInput            `json:"leads"`
	Candidates []record.CandidateText `json:"candidates"`
	Repair     *outputRepair          `json:"repair,omitempty"`
}

// leadResult is one result of the follow-up output.
type leadResult struct {
	Basis        string   `json:"basis"`
	Disposition  string   `json:"disposition"`
	Title        string   `json:"title"`
	Condition    string   `json:"condition"`
	Mechanism    string   `json:"mechanism"`
	Impact       string   `json:"impact"`
	Improvement  string   `json:"improvement"`
	Counterpoint string   `json:"counterpoint"`
	EvidenceIDs  []string `json:"evidence_ids"`
	Reason       string   `json:"reason"`
}

func (x leadResult) candidate() record.CandidateText {
	return record.CandidateText{Title: x.Title, Condition: x.Condition, Mechanism: x.Mechanism, Impact: x.Impact,
		Improvement: x.Improvement, Counterpoint: x.Counterpoint, Evidence: x.EvidenceIDs}
}

// leadsRequest is what one follow-up request carries for its first n
// resolved leads: the leads, the candidates they concern, the excerpts in
// order (each lead's cited lines, the candidates' citations, then the
// resolved excerpts) and the IDs of the resolved ones.
type leadsRequest struct {
	leads      []record.Lead
	candidates []record.Candidate
	excerpts   []record.Excerpt
	added      []string
	// partial are the added excerpts that are partial definition windows.
	partial []string
}

func leadsRequestFor(inv record.Record, leads []record.Lead) leadsRequest {
	catalog := catalogOf(inv.Evidence)
	var req leadsRequest
	seen := map[string]bool{}
	add := func(e record.Excerpt) {
		if e.ID != "" && !seen[e.ID] {
			seen[e.ID] = true
			req.excerpts = append(req.excerpts, e)
		}
	}
	linked := map[string]bool{}
	for _, l := range leads {
		req.leads = append(req.leads, l)
		add(catalog[l.EvidenceID])
		if l.CandidateID != "" {
			linked[l.CandidateID] = true
		}
	}
	for _, c := range inv.Candidates {
		if linked[c.ID] {
			req.candidates = append(req.candidates, c)
			for _, id := range c.Evidence {
				add(catalog[id])
			}
		}
	}
	for _, l := range leads {
		for _, t := range l.Targets {
			if !seen[t.ID] {
				req.added = append(req.added, t.ID)
				if t.Partial {
					req.partial = append(req.partial, t.ID)
				}
			}
			add(t.Excerpt())
		}
	}
	return req
}

// leadsFollowup sends the resolved leads of inv and assembles the final
// candidates.
func (s *roleStage) leadsFollowup(inv record.Record, upstream []record.Upstream) analysis.TaskResult {
	if r := s.begin(upstream); r != nil {
		return *r
	}
	s.rec.Schema = record.SchemaVersionLeads
	s.noteUpstream(upstream...)
	var resolved []record.Lead
	for _, l := range inv.Leads {
		if l.Resolved() {
			resolved = append(resolved, l)
		}
	}
	instructions := s.instructions(leadsContract + s.subjectContract())
	// The request fits the budget with its first n resolved leads; a lead
	// that does not fit is not sent (review_input_trimmed).
	var req leadsRequest
	var in leadsInput
	var withText []record.Excerpt
	fitted := false
	for n := len(resolved); n >= 1 && !fitted; n-- {
		req = leadsRequestFor(inv, resolved[:n])
		var err error
		withText, err = s.withText(req.excerpts)
		if err != nil {
			s.setEvidence(req.excerpts)
			return *s.inputUnavailable()
		}
		in = leadsInput{Candidates: []record.CandidateText{}}
		in.Scope.Snapshot, in.Scope.Subject, in.Scope.Excerpts = s.j.Snapshot.Digest, s.subject(), withText
		in.Scope.Limitations = nonNilList(addLim(nil, s.rec.Limitations...))
		if n < len(resolved) {
			in.Scope.Limitations = addLim(in.Scope.Limitations, limInputTrimmed)
		}
		for _, l := range req.leads {
			x := leadInput{ID: l.ID, Need: l.Need, EvidenceID: l.EvidenceID, Symbol: l.Symbol, Suspicion: l.Suspicion, CandidateID: l.CandidateID, Resolved: []string{}}
			for _, t := range l.Targets {
				x.Resolved = append(x.Resolved, t.ID)
				if t.Partial {
					x.Partial = append(x.Partial, t.ID)
				}
			}
			in.Leads = append(in.Leads, x)
		}
		for _, c := range req.candidates {
			in.Candidates = append(in.Candidates, c.CandidateText)
		}
		body, err := marshal(in)
		if err != nil {
			return *s.inputUnavailable()
		}
		if _, ok := s.fits(instructions, body, true); ok {
			fitted = true
			if n < len(resolved) {
				s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed)
			}
		}
	}
	// The record holds what the request sent; an accepted one also the
	// citations of the candidates it keeps (assembleLeads).
	catalog := catalogOf(inv.Evidence)
	s.setEvidence(req.excerpts)
	if !fitted {
		return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
	}
	sentLeads := map[string]record.Lead{}
	for _, l := range req.leads {
		sentLeads[l.ID] = l
	}
	sentCandidates := map[string]bool{}
	citedBy := map[string][]string{}
	for _, c := range req.candidates {
		sentCandidates[c.ID] = true
		citedBy[c.ID] = c.Evidence
	}
	// within reports a citation inside one of the listed resolved excerpts.
	within := func(cited map[string]record.Excerpt, id string, list []string) bool {
		e := cited[id]
		return slices.ContainsFunc(list, func(n string) bool {
			t := cited[n]
			return t.Path == e.Path && t.SHA256 == e.SHA256 && t.Start <= e.Start && e.End <= t.End
		})
	}
	// A result rests on the code of one lead: a refinement or contest on a
	// lead that names its candidate, an addition on its own lead (the
	// disposition's lead), and on a line of that code the investigation did
	// not send (a caller window may overlap its excerpts).
	leadsOf := map[string][]record.Lead{}
	for _, l := range req.leads {
		if l.CandidateID != "" {
			leadsOf[l.CandidateID] = append(leadsOf[l.CandidateID], l)
		}
	}
	targetsOf := func(l record.Lead, partial bool) []string {
		var out []string
		for _, t := range l.Targets {
			if partial || !t.Partial {
				out = append(out, t.ID)
			}
		}
		return out
	}
	newCode := func(cited map[string]record.Excerpt, id string) bool {
		return record.LineCount(record.LeadNewLineSet([]string{id}, cited, inv.Evidence)) > 0
	}
	var refined, added map[string]record.CandidateText
	var refinedLeads map[string]string
	var contested map[string]record.LeadDisposition
	var cite *citer
	var issues []itemIssue
	var omitted bool
	parse := func(raw json.RawMessage) []string {
		issues = nil
		var out struct {
			Results []leadResult `json:"results"`
		}
		if errs := decodeOutput(raw, &out, "results"); errs != nil {
			return errs
		}
		reply := newCiter(s.j.Snapshot.Digest, withText)
		reply.known = catalog
		var f outputFaults
		ref, add, con := map[string]record.CandidateText{}, map[string]record.CandidateText{}, map[string]record.LeadDisposition{}
		refLead := map[string]string{}
		seen := map[string]bool{}
		left := false
		none := func([]string) []record.FieldProblem { return nil }
		for i, x := range out.Results {
			basis, disposition := strings.TrimSpace(x.Basis), strings.TrimSpace(x.Disposition)
			lead, isLead := sentLeads[basis]
			fault := func(field, code string) {
				f.optional([]string{code}, []itemIssue{{List: "results", Index: i + 1, Field: field, Error: code}}, nil)
				left = true
			}
			switch {
			case seen[basis]:
				fault("basis", errLeadResultDuplicate)
				continue
			case disposition == record.DispositionKept && sentCandidates[basis]:
				seen[basis] = true
				continue
			case sentCandidates[basis] && disposition == record.DispositionRefined, isLead && lead.ID != "" && disposition == record.DispositionAdded:
				// A refinement cites up to record.MaxRefinedEvidence: its
				// original's citations and one or two more. More new ones
				// leave it out at once, before the count would ask a repair.
				cite := record.MaxCandidateEvidence
				if !isLead {
					cite = record.MaxRefinedEvidence
					ids, _, _ := reply.resolveIssues(x.EvidenceIDs, citeNarrow)
					more := map[string]bool{}
					for _, id := range ids {
						if !slices.Contains(citedBy[basis], id) {
							more[id] = true
						}
					}
					if len(more) > record.MaxRefinementNewEvidence {
						f.reject(errLeadRefinementOverCap)
						left = true
						continue
					}
				}
				r := s.checkCandidateCiting(x.candidate(), "results", i+1, reply, true, s.lastChance, limLeadResultOmitted, cite, none)
				f.optional(r.codes, r.issues, r.lims)
				if !r.keep {
					left = true
					continue
				}
				cited := reply.catalog()
				if isLead && !slices.ContainsFunc(r.text.Evidence, func(id string) bool { return within(cited, id, targetsOf(lead, true)) && newCode(cited, id) }) {
					fault("evidence_ids", errLeadAdditionLines)
					continue
				}
				if !isLead {
					// A refinement rewrites the text from the resolved code: it
					// keeps the citations it started from first and in order
					// (the finding keeps its location) and adds one or two, all
					// lines of the whole resolved declarations of one lead of
					// its candidate and one of them new to the investigation
					// (record.RefinementGrounded); the lineage records that
					// lead. One outside those bounds is left out at once,
					// asking no repair, and the candidate goes to verify as the
					// investigation gave it.
					original, ev := citedBy[basis], r.text.Evidence
					fresh := slices.DeleteFunc(slices.Clone(ev), func(id string) bool { return slices.Contains(original, id) })
					code, from := "", ""
					switch {
					case slices.ContainsFunc(original, func(id string) bool { return !slices.Contains(ev, id) }):
						code = errLeadRefinementCitations
					case !slices.Equal(ev[:len(original)], original):
						code = errLeadRefinementOrder
					case len(fresh) > record.MaxRefinementNewEvidence:
						code = errLeadRefinementOverCap
					default:
						for _, l := range leadsOf[basis] {
							whole := targetsOf(l, false)
							if len(fresh) > 0 && !slices.ContainsFunc(fresh, func(id string) bool { return !within(cited, id, whole) }) &&
								slices.ContainsFunc(fresh, func(id string) bool { return newCode(cited, id) }) {
								from = l.ID
								break
							}
						}
						if from == "" {
							code = errLeadRefinementLines
						}
					}
					if code != "" {
						f.reject(code)
						left = true
						continue
					}
					refLead[basis] = from
				}
				seen[basis] = true
				if isLead {
					add[basis] = r.text
				} else {
					ref[basis] = r.text
				}
			case sentCandidates[basis] && disposition == record.DispositionContested:
				ids, _, citeIssues := reply.resolveIssues(x.EvidenceIDs, citeNarrow)
				var codes []string
				for _, is := range citeIssues {
					codes = addLim(codes, is.code)
				}
				cited := reply.catalog()
				codes = addLim(codes, idErrors(ids, cited, record.MaxCandidateEvidence, "code")...)
				codes = addLim(codes, s.textErrors(x.Reason)...)
				// The contest stands on new lines of a whole resolved
				// declaration of a lead of its candidate; the lineage records
				// that lead.
				from := ""
				for _, l := range leadsOf[basis] {
					whole := targetsOf(l, false)
					if slices.ContainsFunc(ids, func(id string) bool { return within(cited, id, whole) && newCode(cited, id) }) {
						from = l.ID
						break
					}
				}
				if len(codes) == 0 && from == "" {
					codes = addLim(codes, errLeadContestLines)
				}
				if len(codes) > 0 {
					for _, code := range codes {
						fault("evidence_ids", code)
					}
					continue
				}
				seen[basis] = true
				con[basis] = record.LeadDisposition{CandidateID: basis, Disposition: record.DispositionContested, Lead: from, Reason: x.Reason, EvidenceIDs: ids}
			default:
				fault("disposition", errLeadResultInvalid)
			}
		}
		issues = f.issues
		errs := s.outcome(&f)
		if len(errs) == 0 {
			refined, refinedLeads, added, contested, cite, omitted = ref, refLead, add, con, reply, left
		}
		return errs
	}
	input := func(r *outputRepair) ([]byte, error) {
		return s.fitItems(r, issues, instructions, func(r *outputRepair) ([]byte, error) { x := in; x.Repair = r; return marshal(x) })
	}
	if r := s.exchange(leadsContract+s.subjectContract(), input, parse); r != nil {
		return *r
	}
	if omitted {
		s.rec.Limitations = addLim(s.rec.Limitations, limLeadResultOmitted)
	}
	s.assembleLeads(inv, req, refined, refinedLeads, added, contested, cite)
	if !record.LeadFollowupMatches(s.rec, inv) {
		return *s.fail(result(domain.TaskFailed, reasonLineageMismatch))
	}
	return s.finish()
}

// assembleLeads writes the final candidates and their dispositions: every
// candidate of the investigation in order (contested, refined or kept, then
// renumbered), then the added ones in lead order within the record's
// candidate bound. No candidate is removed. A contest or refinement names
// the lead whose code it rests on (refinedLeads for a refinement).
func (s *roleStage) assembleLeads(inv record.Record, req leadsRequest, refined map[string]record.CandidateText, refinedLeads map[string]string,
	added map[string]record.CandidateText, contested map[string]record.LeadDisposition, cite *citer) {
	final := []record.Candidate{}
	dispositions := []record.LeadDisposition{}
	var cited [][]string
	for _, c := range inv.Candidates {
		id := fmt.Sprintf("%s_%d", record.StageInvestigate, len(final)+1)
		next := c
		next.ID = id
		if d, ok := contested[c.ID]; ok {
			// Unchanged; verify reads the contest from the disposition.
			d.FinalID = id
			final = append(final, next)
			dispositions = append(dispositions, d)
			cited = append(cited, next.Evidence, d.EvidenceIDs)
			continue
		}
		d := record.LeadDisposition{CandidateID: c.ID, FinalID: id, Disposition: record.DispositionKept}
		if t, ok := refined[c.ID]; ok {
			t.ID = c.ID
			if !sameText(t, c.CandidateText) {
				t.ID = id
				original := c.CandidateText
				original.ID = ""
				next.CandidateText = t
				d.Disposition, d.Lead, d.Original = record.DispositionRefined, refinedLeads[c.ID], &original
			}
		}
		final = append(final, next)
		dispositions = append(dispositions, d)
		cited = append(cited, next.Evidence)
	}
	for _, l := range req.leads {
		t, ok := added[l.ID]
		if !ok || len(final) == record.MaxCandidates {
			continue
		}
		id := fmt.Sprintf("%s_%d", record.StageInvestigate, len(final)+1)
		t.ID = id
		final = append(final, record.Candidate{CandidateText: t, Origin: record.StageInvestigate})
		dispositions = append(dispositions, record.LeadDisposition{FinalID: id, Disposition: record.DispositionAdded, Lead: l.ID})
		cited = append(cited, t.Evidence)
	}
	s.rec.Candidates = final
	// The citations of an unchanged candidate are the investigation's
	// entries; new citations are ranges narrowed from what the request sent.
	investigated := catalogOf(inv.Evidence)
	var carried []record.Excerpt
	for _, list := range cited {
		for _, id := range list {
			if e, ok := investigated[id]; ok {
				carried = append(carried, e)
			}
		}
	}
	s.rec.Evidence = withCited(s.rec.Evidence, carried)
	if cite != nil {
		s.rec.Evidence = withCited(s.rec.Evidence, textless(cite.used(cited...)))
	}
	f := &record.LeadFollowup{Leads: []string{}, Sent: []string{}, New: []string{}, Dispositions: dispositions}
	for _, l := range req.leads {
		f.Leads = append(f.Leads, l.ID)
	}
	for _, e := range req.excerpts {
		f.Sent = append(f.Sent, e.ID)
	}
	f.New = append(f.New, req.added...)
	f.Partial = slices.Clone(req.partial)
	f.NewLines = record.LineCount(record.LeadNewLineSet(f.New, catalogOf(s.rec.Evidence), inv.Evidence))
	s.rec.LeadFollowup = f
}

func sameText(a, b record.CandidateText) bool {
	return a.Title == b.Title && a.Condition == b.Condition && a.Mechanism == b.Mechanism && a.Impact == b.Impact &&
		a.Improvement == b.Improvement && a.Counterpoint == b.Counterpoint && slices.Equal(a.Evidence, b.Evidence)
}

// leadsRecord reads the follow-up record of an investigation for its
// verify (name "leads" or "leads.sNNN"): ok is false when the follow-up
// wrote none (it was skipped or refused before any call), and then verify
// names no follow-up upstream.
func (s *roleStage) leadsRecord(name string) (record.Record, record.Upstream, bool) {
	if _, err := os.Stat(recordPath(s.j, name)); err != nil {
		return record.Record{}, record.Upstream{}, false
	}
	r, u := s.prior(name)
	return r, u, true
}
