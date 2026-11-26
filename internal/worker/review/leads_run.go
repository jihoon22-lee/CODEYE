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
// An investigation whose request leaves files or lines of its scope out may
// name up to record.MaxLeads leads besides its candidates. They are
// optional items, like strengths: one that fails its check is left out
// (review_lead_omitted) and is never named in a format repair. The platform
// resolves the kept leads from the snapshot (leads_resolve.go) and records
// them on the investigate record (schema 0.9).
//
// The follow-up task of that investigation (review.leads for the overview,
// review.leads.sNNN for a slot) is always planned between it and its
// verify. It makes no call and ends skipped_policy when the investigation
// named no lead (no_leads) or none resolved (no_leads_resolved). Otherwise
// it is admitted only while the call ceiling leaves, after its own call, the
// calls of the work after it (leadsCallLimit): its verify's worst case, the
// contract follow-up's two calls when the overview has a contract probe,
// one pair for every later slot with planned lines and every diagnostic
// triage bin, and for the overview the whole-run bound with its verify. A
// refused follow-up ends budget_exhausted (review_budget_exhausted) and
// makes no call: the coverage of planned lines comes before deeper reading.
// One request (record.LeadsMaxCalls: one format repair or one retry of a
// reply that is not JSON) carries the resolved leads, the candidates they
// concern, the cited lines of both and the resolved excerpts. Its result
// refines, withdraws or adds candidates; every other candidate stays as it
// was. The record holds the final candidates and one disposition for each,
// and the verify of the same investigation checks those. A follow-up that
// fails leaves verify with the investigation's candidates.

const limLeadOmitted = "review_lead_omitted"

// limLeadResultOmitted: a result of the follow-up still invalid after the
// repair was left out; its candidate stays as the investigation gave it.
const limLeadResultOmitted = "lead_result_omitted"

// Output errors of the follow-up's results.
const (
	errLeadResultInvalid   = "lead_result_invalid"
	errLeadResultDuplicate = "lead_result_duplicate"
	errLeadWithdrawalLines = "lead_withdrawal_evidence_not_new"
)

const investigateLeadsContract = ` Optionally return up to %d "leads":[{"need":"definition|callers","evidence_id":"ev_...:40-42","symbol":"Name","suspicion":"the suspected problem and what that code decides","candidate":1}] for a suspicion whose cause or trigger depends on code this request lacks. evidence_id cites the sent code lines holding symbol, one identifier: for definition a call of it in those lines, for callers its declaration there. candidate is the 1-based position of the candidate the lead concerns, or 0. A lead is no finding and never names code this request carries.`

const leadsContract = `For this role use exactly {"results":[{"basis":"investigate_1 or lead_1","disposition":"refined|withdrawn|added","title":"what goes wrong","condition":"the concrete trigger input or state","mechanism":"how the cited lines produce the failure, step by step","impact":"...","improvement":"fix direction","counterpoint":"what would refute it","evidence_ids":["ev_...:12-30"],"reason":"withdrawn only: why the cited lines show it cannot happen"}]}. At most one result per basis. For an input candidate (basis its id) give refined with the complete corrected candidate, or withdrawn with only reason and 1-6 code evidence_ids, one of them inside a lead's resolved_evidence_ids, that show the condition cannot happen; omit a candidate that stays as it is. For a lead without candidate_id (basis the lead id) give at most one added candidate. A candidate cites 1-6 evidence IDs of scope.excerpts, at least one code or test. Do not claim reproduction or provide attack instructions.`

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
	l := record.Lead{Need: need, Symbol: symbol, EvidenceID: ids[0], Suspicion: x.Suspicion}
	if x.Candidate != 0 {
		id, ok := keptAs[x.Candidate]
		if !ok {
			return record.Lead{}, false
		}
		l.CandidateID = id
	}
	return l, true
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
		if i < 0 {
			return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
		}
		s.slice, s.sliceLimits, s.scopeUp, s.sliceReserve = &doc.Slices[i], doc.Limits, up, doc.Budget.SliceReserveCalls
		name += "." + slot
	}
	inv, uInv := s.prior(name)
	switch {
	case uInv.State != record.UpstreamAccepted:
		return result(domain.TaskSkippedPolicy, reasonDependency)
	case len(inv.Leads) == 0:
		return result(domain.TaskSkippedPolicy, record.ReasonNoLeads)
	case !slices.ContainsFunc(inv.Leads, record.Lead.Resolved):
		return result(domain.TaskSkippedPolicy, record.ReasonNoLeadsResolved)
	case s.h.Caller == nil:
		return result(domain.TaskUnsupported, "model_not_configured")
	}
	// The admission check counts nothing; the call itself is counted
	// against the same limit (reserveCall).
	s.leadsLimit = s.leadsCallLimit(doc, slot, inv)
	admitted, err := s.callsBelow(s.leadsLimit, false)
	switch {
	case errors.Is(err, errCallsCancelled):
		return result(domain.TaskCancelled, "cancelled")
	case err != nil:
		return result(domain.TaskFailed, reasonCallCountFailed)
	case !admitted:
		return result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
	}
	upstream := []record.Upstream{uInv}
	if slot != "" {
		upstream = append(upstream, s.scopeUp)
	}
	return s.leadsFollowup(inv, upstream)
}

// leadsCallLimit is the count of calls below which a follow-up may call:
// the ceiling less the calls the later work keeps (see the package comment
// above). The overview follow-up also keeps the whole-run bound for itself
// and its verify, so calls_before_slices and the ceiling's minimum do not
// change.
func (s *roleStage) leadsCallLimit(doc record.ScopeDoc, slot string, inv record.Record) int {
	reserve := record.RoleMaxCalls
	overview := inv
	if slot != "" {
		overview, _ = s.prior(record.StageInvestigate)
	}
	if overview.ContractProbe != nil {
		reserve += record.ContractMaxCalls
	}
	if slot == "" {
		reserve += record.SliceMinCalls * s.h.plannedSlots(s.ctx, s.j)
	} else {
		after := false
		for _, sl := range doc.Slices {
			if after && slices.ContainsFunc(sl.Files, func(f record.ScopeFile) bool { return f.State == record.FilePlanned }) {
				reserve += doc.Budget.SliceReserveCalls
			}
			after = after || sl.Key == slot
		}
	}
	reserve += record.SliceMinCalls * s.triageBins()
	limit := s.h.maxCalls(s.j.Scope) - reserve
	if slot == "" {
		limit = min(limit, record.WholeRunMaxCalls-record.RoleMaxCalls)
	}
	return limit
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
	CandidateID string   `json:"candidate_id,omitempty"`
	Resolved    []string `json:"resolved_evidence_ids"`
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
	// The record holds what the request sent and the citations of every
	// candidate it keeps.
	evidence := slices.Clone(req.excerpts)
	catalog := catalogOf(inv.Evidence)
	for _, c := range inv.Candidates {
		for _, id := range c.Evidence {
			if e, ok := catalog[id]; ok && !slices.ContainsFunc(evidence, func(x record.Excerpt) bool { return x.ID == id }) {
				evidence = append(evidence, e)
			}
		}
	}
	s.setEvidence(evidence)
	if !fitted {
		return *s.fail(result(domain.TaskFailed, reasonInputTooLarge))
	}
	sentLeads := map[string]record.Lead{}
	for _, l := range req.leads {
		sentLeads[l.ID] = l
	}
	sentCandidates := map[string]bool{}
	for _, c := range req.candidates {
		sentCandidates[c.ID] = true
	}
	newIDs := map[string]bool{}
	for _, id := range req.added {
		newIDs[id] = true
	}
	var refined, added map[string]record.CandidateText
	var withdrawn map[string]record.LeadDisposition
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
		ref, add, wd := map[string]record.CandidateText{}, map[string]record.CandidateText{}, map[string]record.LeadDisposition{}
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
			case sentCandidates[basis] && disposition == record.DispositionRefined, isLead && lead.CandidateID == "" && disposition == record.DispositionAdded:
				r := s.checkCandidate(x.candidate(), "results", i+1, reply, true, s.lastChance, limLeadResultOmitted, none)
				f.optional(r.codes, r.issues, r.lims)
				if !r.keep {
					left = true
					continue
				}
				seen[basis] = true
				if isLead {
					add[basis] = r.text
				} else {
					ref[basis] = r.text
				}
			case sentCandidates[basis] && disposition == record.DispositionWithdrawn:
				ids, _, citeIssues := reply.resolveIssues(x.EvidenceIDs, citeNarrow)
				var codes []string
				for _, is := range citeIssues {
					codes = addLim(codes, is.code)
				}
				cited := reply.catalog()
				codes = addLim(codes, idErrors(ids, cited, 6, "code")...)
				codes = addLim(codes, s.textErrors(x.Reason)...)
				if len(codes) == 0 && !slices.ContainsFunc(ids, func(id string) bool {
					e := cited[id]
					return slices.ContainsFunc(req.added, func(n string) bool {
						t := cited[n]
						return t.Path == e.Path && t.SHA256 == e.SHA256 && t.Start <= e.Start && e.End <= t.End
					})
				}) {
					codes = addLim(codes, errLeadWithdrawalLines)
				}
				if len(codes) > 0 {
					for _, code := range codes {
						fault("evidence_ids", code)
					}
					continue
				}
				seen[basis] = true
				wd[basis] = record.LeadDisposition{CandidateID: basis, Disposition: record.DispositionWithdrawn, Reason: x.Reason, EvidenceIDs: ids}
			default:
				fault("disposition", errLeadResultInvalid)
			}
		}
		issues = f.issues
		errs := s.outcome(&f)
		if len(errs) == 0 {
			refined, added, withdrawn, cite, omitted = ref, add, wd, reply, left
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
	s.assembleLeads(inv, req, refined, added, withdrawn, cite)
	if !record.LeadFollowupMatches(s.rec, inv) {
		return *s.fail(result(domain.TaskFailed, reasonLineageMismatch))
	}
	return s.finish()
}

// assembleLeads writes the final candidates and their dispositions: every
// candidate of the investigation in order (withdrawn, refined or kept, then
// renumbered), then the added ones in lead order within the record's
// candidate bound.
func (s *roleStage) assembleLeads(inv record.Record, req leadsRequest, refined, added map[string]record.CandidateText,
	withdrawn map[string]record.LeadDisposition, cite *citer) {
	leadOf := map[string]string{}
	for _, l := range req.leads {
		if l.CandidateID != "" && leadOf[l.CandidateID] == "" {
			leadOf[l.CandidateID] = l.ID
		}
	}
	final := []record.Candidate{}
	dispositions := []record.LeadDisposition{}
	var cited [][]string
	for _, c := range inv.Candidates {
		if d, ok := withdrawn[c.ID]; ok {
			d.Lead = leadOf[c.ID]
			dispositions = append(dispositions, d)
			cited = append(cited, d.EvidenceIDs)
			continue
		}
		id := fmt.Sprintf("%s_%d", record.StageInvestigate, len(final)+1)
		next := c
		next.ID = id
		d := record.LeadDisposition{CandidateID: c.ID, FinalID: id, Disposition: record.DispositionKept}
		if t, ok := refined[c.ID]; ok {
			t.ID = c.ID
			if !sameText(t, c.CandidateText) {
				t.ID = id
				next.CandidateText = t
				d.Disposition, d.Lead = record.DispositionRefined, leadOf[c.ID]
			}
		}
		final = append(final, next)
		dispositions = append(dispositions, d)
		cited = append(cited, next.Evidence)
	}
	for _, l := range req.leads {
		t, ok := added[l.ID]
		if !ok || l.CandidateID != "" || len(final) == record.MaxCandidates {
			continue
		}
		id := fmt.Sprintf("%s_%d", record.StageInvestigate, len(final)+1)
		t.ID = id
		final = append(final, record.Candidate{CandidateText: t, Origin: record.StageInvestigate})
		dispositions = append(dispositions, record.LeadDisposition{FinalID: id, Disposition: record.DispositionAdded, Lead: l.ID})
		cited = append(cited, t.Evidence)
	}
	s.rec.Candidates = final
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
	f.NewLines = record.LeadLines(f.New, catalogOf(s.rec.Evidence))
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
