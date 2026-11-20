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

const contractOutput = `Return exactly {"contract_comparisons":[{"contract_index":<input.prior.contract_index>,"applies_to":"<input.prior.applies_to>","status":"match|partial|mismatch|unknown","reason":"...","document_evidence_ids":["ev_..."],"code_evidence_ids":["ev_..."]}]}. Copy the prior index, scope and document IDs exactly. Cite newly read implementation for any status other than unknown. For partial only, add "partial_support":{"matching":{"condition":"...","evidence_id":"ev_..."},"violating":{"condition":"...","evidence_id":"ev_..."}} inside the comparison. Give distinct conditions and distinct cited implementation excerpts; each must show the stated matching or violating outcome. One confirmed condition and a second unresolved condition leave the contract unknown; mention the confirmed part and cite it if new. A test excerpt is supporting evidence and does not establish an implementation or executed result. For unknown, an empty code_evidence_ids array is allowed.`

const (
	maxContractExcerpts     = 12
	maxContractSliceReads   = 80
	limContractInconclusive = "contract_comparison_inconclusive"
)

type contractReviewInput struct {
	Contract record.Contract           `json:"contract"`
	Prior    record.ContractComparison `json:"prior"`
	Scope    struct {
		Snapshot string           `json:"snapshot_manifest_digest"`
		Excerpts []record.Excerpt `json:"excerpts"`
	} `json:"scope"`
	Repair *outputRepair `json:"repair,omitempty"`
}

type contractExcerpt struct {
	excerpt record.Excerpt
	sources []record.Upstream
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
	if used >= s.h.maxCalls() {
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
	named, err := s.contractNamedExcerpts(contract, prior.AppliesTo, context[:len(prior.DocumentEvidenceIDs)])
	if err != nil {
		return s.contractInputFailure(err)
	}
	extra = append(extra, named...)
	later, err := s.contractSliceExcerpts(probe, prior, extra, uDoc, uInv, files)
	if err != nil {
		return s.contractInputFailure(err)
	}
	// Keep the original promise, then a new decision window and the limited
	// supporting context that fits into the bounded model input.
	seen := map[string]bool{}
	pool := make([]contractExcerpt, 0, len(context)+len(extra)+len(later))
	add := func(e contractExcerpt) {
		if !seen[e.excerpt.ID] && (e.excerpt.Kind == "document" || record.ScopeUnder(e.excerpt.Path, prior.AppliesTo)) {
			seen[e.excerpt.ID] = true
			pool = append(pool, e)
		}
	}
	for _, e := range context[:len(prior.DocumentEvidenceIDs)] {
		add(contractExcerpt{excerpt: e})
	}
	for _, e := range named {
		add(contractExcerpt{excerpt: e})
	}
	for _, e := range extra {
		add(contractExcerpt{excerpt: e})
	}
	for _, e := range later {
		add(e)
	}
	for _, e := range context[len(prior.DocumentEvidenceIDs):] {
		add(contractExcerpt{excerpt: e})
	}
	keep := len(prior.DocumentEvidenceIDs)
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
		if fresh {
			validImplementation[e.excerpt.ID] = true
		}
	}
	fresh := slices.IndexFunc(pool[keep:], func(e contractExcerpt) bool { return validImplementation[e.excerpt.ID] })
	if fresh < 0 {
		return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
	}
	first := pool[keep+fresh]
	copy(pool[keep+1:], pool[keep:keep+fresh])
	pool[keep] = first
	var in contractReviewInput
	in.Contract, in.Prior, in.Scope.Snapshot = contract, prior, s.j.Snapshot.Digest
	ordered := make([]record.Excerpt, 0, min(len(pool), maxContractExcerpts))
	for _, e := range pool[:min(len(pool), maxContractExcerpts)] {
		ordered = append(ordered, e.excerpt)
	}
	build := func(list []record.Excerpt) ([]byte, error) {
		in.Scope.Excerpts = list
		return marshal(in)
	}
	sent, trimmed, err := s.fitExcerpts(s.instructions(contractOutput), keep+1, ordered, build)
	if err != nil {
		return s.inputFailure(err)
	}
	if trimmed || len(ordered) < len(pool) {
		s.rec.Limitations = addLim(s.rec.Limitations, limInputTrimmed)
	}
	in.Scope.Excerpts = sent
	for _, e := range pool[:len(sent)] {
		for _, up := range e.sources {
			if !slices.Contains(s.rec.Upstream, up) {
				s.rec.Upstream = append(s.rec.Upstream, up)
			}
		}
	}
	s.setEvidence(sent)
	catalog := catalogOf(s.rec.Evidence)
	input := func(repair *outputRepair) ([]byte, error) {
		in.Repair = repair
		return marshal(in)
	}
	parse := func(raw json.RawMessage) []string {
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
		if errs := textErrors(x.Reason); len(errs) > 0 {
			return errs
		}
		if record.ForeignInsightProse(x.Reason) {
			return []string{record.InsightLanguageError}
		}
		proposed := s.rec
		proposed.ContractComparisons = []record.ContractComparison{x.ContractComparison}
		proposed.PartialSupport = x.PartialSupport
		if !record.ValidateContractFollowup(proposed, doc, inv) {
			return []string{"contract_comparison_invalid"}
		}
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
		e, ok := files[x.Path]
		if !ok || e.SHA256 != x.SHA256 || !inScope(x.Path, normalizedScope(s.j.Scope)) ||
			x.ID != record.ExcerptID(s.j.Snapshot.Digest, x.Path, x.SHA256, x.Start, x.End) {
			return nil, errSnapshotIntegrity
		}
		text, total, ok, err := s.readLines(e, x.Start, x.End)
		if err != nil {
			return nil, err
		}
		if !ok || x.End > total || strings.TrimSpace(text) == "" {
			return nil, errSnapshotIntegrity
		}
		x.Text = text
		out = append(out, x)
	}
	return out, nil
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
	return runtime, fresh, nil
}

func (s *roleStage) contractSliceExcerpts(probe record.ContractProbe, prior record.ContractComparison, anchors []record.Excerpt, docUp, invUp record.Upstream, files map[string]source.Entry) ([]contractExcerpt, error) {
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
	var out []contractExcerpt
	var seenSources []record.Upstream
	reads := 0
	for _, slot := range scope.Slices {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) >= maxContractExcerpts || len(seenSources) >= record.MaxContractSliceSources || reads >= maxContractSliceReads {
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
			needed := 0
			for _, u := range upstreams[i][1:] {
				if !slices.Contains(seenSources, u) {
					needed++
				}
			}
			if len(seenSources)+needed > record.MaxContractSliceSources {
				continue
			}
			for _, e := range role.Evidence {
				if len(out) >= maxContractExcerpts || reads >= maxContractSliceReads {
					break
				}
				if (e.Kind != "code" && e.Kind != "test") || !record.ScopeUnder(e.Path, prior.AppliesTo) || !inScope(e.Path, normalizedScope(s.j.Scope)) ||
					(e.Origin != "caller" && e.Origin != "test" && !slices.ContainsFunc(slot.Files, func(f record.ScopeFile) bool {
						return f.Path == e.Path && f.SHA256 == e.SHA256 && f.State == record.FilePlanned
					})) {
					continue
				}
				named := paths[e.Path] || (e.Kind == "test" && stems[maintenanceStem(e.Path)])
				if !named && len(terms) == 0 {
					continue
				}
				reads++
				window, err := s.contractRead([]record.Excerpt{e}, files)
				if err != nil {
					return nil, err
				}
				if !named && !slices.ContainsFunc(terms, func(term string) bool { return containsContractLiteral(window[0].Text, term) }) {
					continue
				}
				out = append(out, contractExcerpt{excerpt: window[0], sources: upstreams[i]})
				for _, u := range upstreams[i][1:] {
					if !slices.Contains(seenSources, u) {
						seenSources = append(seenSources, u)
					}
				}
			}
		}
	}
	return out, nil
}
