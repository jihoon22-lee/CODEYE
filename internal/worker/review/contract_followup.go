package review

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const contractOutput = `Return exactly {"contract_comparisons":[{"contract_index":<input.prior.contract_index>,"applies_to":"<input.prior.applies_to>","status":"match|partial|mismatch|unknown","reason":"...","document_evidence_ids":["ev_..."],"code_evidence_ids":["ev_...:40-52"]}]}. Copy the prior index, scope and document IDs exactly. Cite newly read implementation for any status other than unknown. For partial only, add "partial_support":{"matching":{"condition":"...","evidence_id":"ev_...:40-44"},"violating":{"condition":"...","evidence_id":"ev_...:48-52"}} inside the comparison. Give distinct conditions and distinct cited implementation excerpts; each must show the stated matching or violating outcome. One confirmed condition and a second unresolved condition leave the contract unknown; mention the confirmed part and cite it if new. A test excerpt is supporting evidence and does not establish an implementation or executed result. For unknown, an empty code_evidence_ids array is allowed.`

const (
	maxContractExcerpts         = 12
	maxContractSliceReads       = 80
	maxContractStrengthExcerpts = 2
	limContractInconclusive     = "contract_comparison_inconclusive"
)

type contractReviewInput struct {
	Contract record.Contract           `json:"contract"`
	Prior    record.ContractComparison `json:"prior"`
	Scope    struct {
		Snapshot       string                  `json:"snapshot_manifest_digest"`
		Excerpts       excerptList             `json:"excerpts"`
		DirectCalls    []directCall            `json:"direct_calls,omitempty"`
		WorkflowRoutes []workflowEvidenceInput `json:"workflow_routes,omitempty"`
	} `json:"scope"`
	Repair *outputRepair `json:"repair,omitempty"`
}

type contractExcerpt struct {
	excerpt      record.Excerpt
	sources      []record.Upstream
	fromStrength bool
	selectedPath bool
}

type contractModelComparison struct {
	record.ContractComparison
	PartialSupport *record.ContractPartialSupport `json:"partial_support,omitempty"`
}

func (s *roleStage) contractFollowup() analysis.TaskResult {
	doc, uDoc := s.prior(record.StageDocuments)
	inv, uInv := s.prior(record.StageInvestigate)
	if uDoc.State != record.UpstreamAccepted || uInv.State != record.UpstreamAccepted {
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	if !slices.Contains(inv.Upstream, uDoc) {
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	prior, ok := record.ContractFollowupPrior(doc, inv)
	if !ok {
		return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
	}
	used, err := callsUsed(s.j)
	if err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	if used >= s.h.maxCalls(s.j.Scope) {
		return result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
	}
	if r := s.begin([]record.Upstream{uDoc, uInv}); r != nil {
		return *r
	}
	docCatalog, invCatalog := catalogOf(doc.Evidence), catalogOf(inv.Evidence)
	var chosen []record.Excerpt
	for _, id := range prior.DocumentEvidenceIDs {
		e, ok := docCatalog[id]
		if !ok || e != invCatalog[id] {
			return result(domain.TaskSkippedPolicy, reasonDependency)
		}
		chosen = append(chosen, e)
	}
	for _, id := range prior.CodeEvidenceIDs {
		chosen = append(chosen, invCatalog[id])
	}
	files := s.entries()
	context, err := s.contractRead(chosen, files)
	if err != nil {
		return s.contractInputFailure(err)
	}
	probe := *inv.ContractProbe
	extra, err := s.contractProbeExcerpts(probe, prior)
	if err != nil {
		return s.contractInputFailure(err)
	}
	contract := doc.Contracts[prior.ContractIndex-1]
	selectedWorkflow, missingWorkflow, workflowErr := s.selectedWorkflowRoutes(doc)
	if s.ctx.Err() != nil {
		return s.contractInputFailure(s.ctx.Err())
	}
	if missingWorkflow || workflowErr != nil {
		s.rec.Limitations = addLim(s.rec.Limitations, limWorkflowUnavailable)
	}
	workflow := workflowFollowupLinks(selectedWorkflow, prior)
	keep := len(prior.DocumentEvidenceIDs)
	documents := context[:keep]
	javaBindings, err := s.javaAPIBindings(documents)
	if err != nil {
		return s.contractInputFailure(err)
	}
	javaWindows, err := s.javaContractWindows(prior.AppliesTo, contract.Statement, documents, javaBindings, files)
	if err != nil {
		return s.contractInputFailure(err)
	}
	named, err := s.contractNamedExcerpts(contract, prior.AppliesTo, documents)
	if err != nil {
		return s.contractInputFailure(err)
	}
	named = append(javaWindows, named...)
	callers, err := s.contractCallerExcerpts(prior.AppliesTo, append(slices.Clone(named), extra...), inv.Evidence)
	if err != nil {
		return s.contractInputFailure(err)
	}
	anchors := append(slices.Clone(context[keep:]), named...)
	anchors = append(anchors, extra...)
	linked, err := s.sourceCallContext(prior.AppliesTo, anchors, inv.Evidence, maxContractCallers, 2*roleMaxWindowBytes)
	if err != nil {
		return s.contractInputFailure(err)
	}
	callers = append(linked, callers...)
	callers = callers[:min(len(callers), maxContractCallers)]
	extra = append(extra, named...)
	strengthTerms := contractStrengthTerms(contract.Statement, documents)
	later, err := s.contractSliceExcerpts(probe, prior, extra, uDoc, uInv, files, strengthTerms, inv.Evidence)
	if err != nil {
		return s.contractInputFailure(err)
	}
	// Keep the original promise, then a new decision window and the limited
	// supporting context that fits into the bounded model input.
	seen := map[string]bool{}
	pool := make([]contractExcerpt, 0, len(context)+len(extra)+len(callers)+len(later))
	add := func(e contractExcerpt) {
		if !seen[e.excerpt.ID] && (e.excerpt.Kind == "document" || record.ScopeUnder(e.excerpt.Path, prior.AppliesTo)) {
			seen[e.excerpt.ID] = true
			pool = append(pool, e)
		}
	}
	for _, e := range documents {
		add(contractExcerpt{excerpt: e})
	}
	for _, link := range workflow {
		windows := link.route.outcomeWindows()
		if len(windows) == 0 {
			continue
		}
		add(contractExcerpt{excerpt: link.route.DecisionCall})
		for _, e := range windows {
			add(contractExcerpt{excerpt: e})
		}
	}
	for _, link := range workflow {
		for _, e := range []record.Excerpt{link.route.Decision, link.route.DecisionCall, link.route.Handler, link.route.Registration} {
			if e.ID != "" {
				add(contractExcerpt{excerpt: e})
			}
		}
	}
	for _, link := range workflow {
		if e := link.route.Test; e.ID != "" {
			add(contractExcerpt{excerpt: e})
		}
		if e := link.route.relatedTest.handler; e.ID != "" {
			add(contractExcerpt{excerpt: e})
		}
	}
	for _, e := range named {
		add(contractExcerpt{excerpt: e})
	}
	for _, e := range callers {
		add(contractExcerpt{excerpt: e})
	}
	for _, e := range later {
		if e.fromStrength {
			add(e)
		}
	}
	for _, e := range later {
		if e.selectedPath {
			add(e)
		}
	}
	for _, e := range extra {
		add(contractExcerpt{excerpt: e})
	}
	for _, e := range later {
		add(e)
	}
	for _, e := range context[keep:] {
		add(contractExcerpt{excerpt: e})
	}
	var in contractReviewInput
	in.Contract, in.Prior, in.Scope.Snapshot = contract, prior, s.j.Snapshot.Digest
	if len(javaBindings) == 0 {
		whole, err := s.contractWholeFiles(prior.AppliesTo, extra, inv.Evidence, pool[keep:], files)
		if err != nil {
			return s.contractInputFailure(err)
		}
		if len(whole) > 0 {
			in.Scope.Excerpts = append(slices.Clone(documents), whole[0].excerpt)
			in.Scope.WorkflowRoutes = workflowEvidence(workflow, in.Scope.Excerpts)
			minimum, err := marshal(in)
			if err != nil {
				return s.contractInputFailure(err)
			}
			if _, fits := s.fits(s.instructions(contractOutput), minimum, true); fits {
				code := preferWholeContractFiles(whole, pool[keep:], prior.CodeEvidenceIDs)
				pool = append(pool[:keep], code...)
			}
		}
	}
	validImplementation := make(map[string]bool, len(pool))
	runtimeImplementation := make(map[string]bool, len(pool))
	for _, e := range pool[keep:] {
		runtime, fresh, err := s.contractImplementation(e.excerpt, inv.Evidence, files)
		if err != nil {
			return s.contractInputFailure(err)
		}
		if runtime {
			runtimeImplementation[e.excerpt.ID] = true
		}
		if fresh && (len(javaBindings) == 0 || javaBindingSupports(javaBindings, e.excerpt)) {
			validImplementation[e.excerpt.ID] = true
		}
	}
	fresh := slices.IndexFunc(pool[keep:], func(e contractExcerpt) bool { return validImplementation[e.excerpt.ID] })
	if fresh < 0 {
		return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
	}
	instructions := s.instructions(contractOutput)
	build := func(list []record.Excerpt) ([]byte, error) {
		in.Scope.Excerpts = list
		in.Scope.DirectCalls = javaContractDirectCalls(javaBindings, documents, list)
		in.Scope.WorkflowRoutes = workflowEvidence(workflow, list)
		b, err := marshal(in)
		if err != nil || len(in.Scope.WorkflowRoutes) == 0 {
			return b, err
		}
		if _, fits := s.fits(instructions, b, true); fits {
			return b, nil
		}
		in.Scope.WorkflowRoutes = nil
		return marshal(in)
	}
	if len(workflow) > 0 {
		// A selected implementation may be larger than an ordinary probe.
		// Keep the earliest fresh window that can fit with the promise.
		minimum := append(slices.Clone(documents), record.Excerpt{})
		fresh = slices.IndexFunc(pool[keep:], func(e contractExcerpt) bool {
			if !validImplementation[e.excerpt.ID] {
				return false
			}
			minimum[keep] = e.excerpt
			b, err := build(minimum)
			if err != nil {
				return false
			}
			_, fits := s.fits(instructions, b, true)
			return fits
		})
		if fresh < 0 {
			return s.inputFailure(errTooLarge)
		}
	}
	first := pool[keep+fresh]
	copy(pool[keep+1:], pool[keep:keep+fresh])
	pool[keep] = first
	ordered := make([]record.Excerpt, 0, min(len(pool), maxContractExcerpts))
	for _, e := range pool[:min(len(pool), maxContractExcerpts)] {
		ordered = append(ordered, e.excerpt)
	}
	// A range the overview comparison cited (origin cited) is context
	// only beside the excerpt it lies in; it is never the required fresh
	// window, so dropping one leaves the first keep+1 entries in place.
	ordered = containedRanges(ordered, nil)
	sent, trimmed, err := s.fitExcerpts(instructions, keep+1, ordered, build)
	if err != nil {
		return s.inputFailure(err)
	}
	sent = containedRanges(sent, nil)
	if trimmed || len(ordered) < len(pool) {
		s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed)
	}
	in.Scope.Excerpts = sent
	for _, e := range pool {
		if !slices.ContainsFunc(sent, func(x record.Excerpt) bool { return x.ID == e.excerpt.ID }) {
			continue
		}
		for _, up := range e.sources {
			if !slices.Contains(s.rec.Upstream, up) {
				s.rec.Upstream = append(s.rec.Upstream, up)
			}
		}
	}
	s.setEvidence(sent)
	catalog := catalogOf(s.rec.Evidence)
	var issues []itemIssue
	input := func(repair *outputRepair) ([]byte, error) {
		return s.fitItems(repair, issues, instructions, func(r *outputRepair) ([]byte, error) { in.Repair = r; return marshal(in) })
	}
	parse := func(raw json.RawMessage) []string {
		issues = nil
		var out struct {
			Comparisons []contractModelComparison `json:"contract_comparisons"`
		}
		if errs := decodeOutput(raw, &out, "contract_comparisons"); len(errs) > 0 {
			return errs
		}
		if len(out.Comparisons) != 1 {
			return []string{"contract_comparison_invalid"}
		}
		x := out.Comparisons[0]
		name := func(field, id, code string) {
			issues = append(issues, itemIssue{List: "contract_comparisons", Index: 1, Field: field, ID: id, Error: code})
		}
		if errs := s.textErrors(x.Reason); len(errs) > 0 {
			for _, code := range errs {
				name("reason", "", code)
			}
			return errs
		}
		// A cited range is checked against the sent window and the
		// window's own ID kept: the follow-up compares whole windows. A
		// range of the wrong form names its window too, so after the repair
		// it stands for that window like any range (docs/REVIEW.md#출력-오류-분류).
		reply := newCiter(s.j.Snapshot.Digest, sent)
		var citeIssues []citeIssue
		var fields []string
		x.CodeEvidenceIDs, _, citeIssues = reply.resolveIssues(x.CodeEvidenceIDs, citeStrip)
		for range citeIssues {
			fields = append(fields, "code_evidence_ids")
		}
		if p := x.PartialSupport; p != nil {
			ids, _, more := reply.resolveIssues([]string{p.Matching.EvidenceID, p.Violating.EvidenceID}, citeStrip)
			for _, is := range more {
				fields = append(fields, []string{"partial_support.matching", "partial_support.violating"}[is.pos])
			}
			citeIssues = append(citeIssues, more...)
			support := *p
			support.Matching.EvidenceID, support.Violating.EvidenceID = ids[0], ids[1]
			x.PartialSupport = &support
		}
		var citeErrs []string
		for i, is := range citeIssues {
			citeErrs = addLim(citeErrs, is.code)
			name(fields[i], is.id, is.code)
		}
		named := !slices.ContainsFunc(citeIssues, func(is citeIssue) bool { return is.id == "" })
		if len(citeErrs) > 0 && (!s.lastChance || !formOnly(citeErrs) || !named) {
			s.repairable = formOnly(citeErrs) && named
			return citeErrs
		}
		if record.ForeignInsightProse(x.Reason) {
			name("reason", "", record.InsightLanguageError)
			return []string{record.InsightLanguageError}
		}
		proposed := s.rec
		proposed.ContractComparisons = []record.ContractComparison{x.ContractComparison}
		proposed.PartialSupport = x.PartialSupport
		if !record.ValidateContractFollowup(proposed, doc, inv) || !javaAPIComparisonValid(x.ContractComparison, javaBindings, catalog) {
			// Name what the comparison rules find; the follow-up's own rules
			// decide, so the generic code stays when they find nothing.
			for _, p := range x.ContractComparison.Problems(catalog, s.limit().text) {
				name(p.Field, "", p.Code)
			}
			return []string{"contract_comparison_invalid"}
		}
		s.degraded = citeErrs
		if x.Status == "partial" {
			a, b := x.PartialSupport.Matching.EvidenceID, x.PartialSupport.Violating.EvidenceID
			if !runtimeImplementation[a] || !runtimeImplementation[b] || (!validImplementation[a] && !validImplementation[b]) {
				return []string{"contract_partial_support_invalid"}
			}
		}
		if x.Status != "unknown" && !slices.ContainsFunc(x.CodeEvidenceIDs, func(id string) bool { return validImplementation[id] }) {
			return []string{"contract_evidence_not_new"}
		}
		// A report can preserve an unknown judgment with a new observation.
		// Apply the same source check before exposing such citations.
		if x.Status == "unknown" && slices.ContainsFunc(x.CodeEvidenceIDs, func(id string) bool {
			return record.HasNewContractEvidence(catalog[id], inv.Evidence) && !validImplementation[id]
		}) {
			return []string{"contract_evidence_not_new"}
		}
		s.rec.ContractComparisons = proposed.ContractComparisons
		s.rec.PartialSupport = proposed.PartialSupport
		return nil
	}
	if r := s.exchange(contractOutput, input, parse); r != nil {
		return *r
	}
	if s.rec.ContractComparisons[0].Status == "unknown" {
		s.rec.Limitations = addLim(s.rec.Limitations, limContractInconclusive)
	}
	return s.finish()
}

func (s *roleStage) contractInputFailure(err error) analysis.TaskResult {
	if s.ctx.Err() != nil {
		return *s.fail(result(domain.TaskCancelled, "cancelled"))
	}
	return s.inputFailure(err)
}

// contractRead requires every requested line to exist. Slice and earlier
// document excerpts go through the read filters and current secrets screen.
func (s *roleStage) contractRead(list []record.Excerpt, files map[string]source.Entry) ([]record.Excerpt, error) {
	out := make([]record.Excerpt, 0, len(list))
	for _, x := range list {
		x, _, err := s.contractReadWindow(x, files)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

func (s *roleStage) contractReadWindow(x record.Excerpt, files map[string]source.Entry) (record.Excerpt, string, error) {
	e, ok := files[x.Path]
	if !ok || e.SHA256 != x.SHA256 || !inScope(x.Path, normalizedScope(s.j.Scope)) ||
		x.ID != record.ExcerptID(s.j.Snapshot.Digest, x.Path, x.SHA256, x.Start, x.End) {
		return record.Excerpt{}, "", errSnapshotIntegrity
	}
	text, ok, err := s.readSource(e)
	if err != nil {
		return record.Excerpt{}, "", err
	}
	lines := strings.Split(text, "\n")
	if !ok || x.Start < 1 || x.End < x.Start || x.End > len(lines) {
		return record.Excerpt{}, "", errSnapshotIntegrity
	}
	x.Text = strings.Join(lines[x.Start-1:x.End], "\n")
	if strings.TrimSpace(x.Text) == "" {
		return record.Excerpt{}, "", errSnapshotIntegrity
	}
	return x, text, nil
}

// Only executable-looking new source can justify a replacement. The model
// still has to determine the condition, result and caller within the excerpt;
// this floor excludes comments, declaration files and isolated signatures.
func (s *roleStage) contractImplementation(x record.Excerpt, overview []record.Excerpt, files map[string]source.Entry) (runtime, fresh bool, err error) {
	if !contractRuntimePath(x.Path, x.Kind) {
		return false, false, nil
	}
	e, ok := files[x.Path]
	if !ok || e.SHA256 != x.SHA256 {
		return false, false, errSnapshotIntegrity
	}
	// Scan from the start so a window inside a multiline comment or docstring
	// cannot be promoted by matching a source-like line in that comment.
	text, ok, err := s.readSource(e)
	if err != nil || !ok {
		return false, false, err
	}
	runtime, fresh = contractWindowActivity(x, text, overview)
	return runtime, fresh, nil
}

func contractWindowActivity(x record.Excerpt, text string, overview []record.Excerpt) (runtime, fresh bool) {
	contractExecutableLines(text, x.Path, func(line int, _ bool) bool {
		if line > x.End {
			return true
		}
		if line >= x.Start {
			runtime = true
			fresh = record.NewContractEvidenceLine(x, line, overview)
			return fresh
		}
		return false
	})
	return runtime, fresh
}

func (s *roleStage) contractSliceExcerpts(probe record.ContractProbe, prior record.ContractComparison, anchors []record.Excerpt, docUp, invUp record.Upstream, files map[string]source.Entry, strengthTerms []string, overview []record.Excerpt) ([]contractExcerpt, error) {
	scope, scopeUp := s.readScope()
	if scopeUp.State != record.UpstreamAccepted || !slices.Contains(scope.Upstream, invUp) {
		return nil, nil
	}
	paths := map[string]bool{}
	terms := slices.Clone(probe.Literals)
	for _, candidate := range probe.Candidates {
		paths[candidate.Path] = true
		if candidate.Symbol != "" && !slices.Contains(terms, candidate.Symbol) {
			terms = append(terms, candidate.Symbol)
		}
	}
	for _, x := range anchors {
		paths[x.Path] = true
	}
	stems := map[string]bool{}
	for p := range paths {
		stems[maintenanceStem(p)] = true
	}
	type candidate struct {
		contractExcerpt
		named         bool
		strengthTerms []string
	}
	slots := slices.Clone(scope.Slices)
	namedSlots := map[string]bool{}
	for _, slot := range slots {
		namedSlots[slot.Key] = slices.ContainsFunc(slot.Files, func(f record.ScopeFile) bool { return paths[f.Path] })
	}
	slices.SortStableFunc(slots, func(a, b record.ScopeSlice) int {
		if namedSlots[a.Key] == namedSlots[b.Key] {
			return 0
		}
		if namedSlots[a.Key] {
			return -1
		}
		return 1
	})
	var named, strengths, ordinary []candidate
	queued := map[string]bool{}
	var groups codeGroups
	if len(strengthTerms) > 0 {
		groups = planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	}
	for _, slot := range slots {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if (len(named) >= maxContractSliceReads || (!namedSlots[slot.Key] && len(ordinary) >= maxContractSliceReads)) &&
			(len(strengthTerms) == 0 || len(strengths) >= maxContractSliceReads) {
			break
		}
		inv, iUp := s.prior(record.StageInvestigate + "." + slot.Key)
		if iUp.State != record.UpstreamAccepted || !slices.Contains(inv.Upstream, scopeUp) || !slices.Contains(inv.Upstream, docUp) ||
			!slices.Equal(inv.Slice.Modules, slot.Modules) {
			continue
		}
		roles := []record.Record{inv}
		upstreams := [][]record.Upstream{{scopeUp, iUp}}
		ver, vUp := s.prior(record.StageVerify + "." + slot.Key)
		if vUp.State == record.UpstreamAccepted && slices.Contains(ver.Upstream, iUp) && slices.Contains(ver.Upstream, scopeUp) &&
			slices.Contains(ver.Upstream, docUp) && slices.Equal(ver.Slice.Modules, slot.Modules) {
			roles = append(roles, ver)
			upstreams = append(upstreams, []record.Upstream{scopeUp, iUp, vUp})
		}
		for i, role := range roles {
			if !slices.ContainsFunc(role.Calls, func(call record.Call) bool { return call.RequestDigest != "" }) {
				continue
			}
			cited := contractStrengthCitations(role, strengthTerms)
			for _, e := range role.Evidence {
				if (e.Kind != "code" && e.Kind != "test") || !record.ScopeUnder(e.Path, prior.AppliesTo) || !inScope(e.Path, normalizedScope(s.j.Scope)) ||
					(e.Origin != "caller" && e.Origin != "test" && !slices.ContainsFunc(slot.Files, func(f record.ScopeFile) bool {
						return f.Path == e.Path && f.SHA256 == e.SHA256 && f.State == record.FilePlanned
					})) {
					continue
				}
				matchesPath := paths[e.Path] || (e.Kind == "test" && stems[maintenanceStem(e.Path)])
				item := candidate{contractExcerpt: contractExcerpt{excerpt: e, sources: upstreams[i], selectedPath: paths[e.Path]},
					named: matchesPath}
				if len(strengths) < maxContractSliceReads && len(cited[e.ID]) > 0 && contractRuntimePath(e.Path, e.Kind) &&
					record.HasNewContractEvidence(e, overview) && s.navigationEligible(groups, e.Path) {
					item.strengthTerms = cited[e.ID]
					strengths = append(strengths, item)
				}
				if queued[e.ID] {
					continue
				}
				if matchesPath && len(named) < maxContractSliceReads {
					item.strengthTerms = nil
					named = append(named, item)
					queued[e.ID] = true
				} else if !matchesPath && len(terms) > 0 && len(ordinary) < maxContractSliceReads {
					item.strengthTerms = nil
					ordinary = append(ordinary, item)
					queued[e.ID] = true
				}
			}
		}
	}
	var out []contractExcerpt
	var seenSources []record.Upstream
	seenIDs := map[string]bool{}
	reads, fromStrength := 0, 0
	for _, candidate := range slices.Concat(strengths, named, ordinary) {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) >= maxContractExcerpts || len(seenSources) >= record.MaxContractSliceSources || reads >= maxContractSliceReads {
			break
		}
		e := candidate.excerpt
		if seenIDs[e.ID] || (len(candidate.strengthTerms) > 0 && fromStrength >= maxContractStrengthExcerpts) {
			continue
		}
		needed := 0
		for _, u := range candidate.sources[1:] {
			if !slices.Contains(seenSources, u) {
				needed++
			}
		}
		if len(seenSources)+needed > record.MaxContractSliceSources {
			continue
		}
		reads++
		window, text, err := s.contractReadWindow(e, files)
		if err != nil {
			return nil, err
		}
		if len(candidate.strengthTerms) > 0 {
			if !contractStrengthSource(e, text, candidate.strengthTerms, overview) {
				continue
			}
			candidate.fromStrength = true
			fromStrength++
		} else if !candidate.named && !slices.ContainsFunc(terms, func(term string) bool { return containsContractLiteral(window.Text, term) }) {
			continue
		}
		candidate.excerpt = window
		out = append(out, candidate.contractExcerpt)
		seenIDs[e.ID] = true
		for _, u := range candidate.sources[1:] {
			if !slices.Contains(seenSources, u) {
				seenSources = append(seenSources, u)
			}
		}
	}
	return out, nil
}
