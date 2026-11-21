package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	workflowCatalogBytes        = 5000
	maxWorkflowOverviewWindows  = 6
	minSupplementWorkflowRoutes = 6
	limWorkflowUnavailable      = "workflow_evidence_unavailable"
)

const workflowSelectionContract = `Return a JSON object containing only workflow_selections, an array. For each selection, set contract_index to an integer from input.workflow_contracts and route_ids to an array of IDs from input.workflow_routes. Choose routes for the behavior described by that contract. Use at most four route IDs across the response.`

type workflowRouteInput struct {
	ID          string `json:"id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Scope       string `json:"scope,omitempty"`
	PatternKind string `json:"pattern_kind,omitempty"`
	Handler     string `json:"handler,omitempty"`
	SameHandler string `json:"same_handler_as,omitempty"`
}

type workflowChoice struct {
	ContractIndex int      `json:"contract_index"`
	RouteIDs      []string `json:"route_ids"`
}

type workflowContractInput struct {
	Index     int    `json:"contract_index"`
	Statement string `json:"statement"`
	Scope     string `json:"applies_to"`
}

type workflowSelectionInput struct {
	Contracts []workflowContractInput `json:"workflow_contracts"`
	Routes    []workflowRouteInput    `json:"workflow_routes"`
	Scope     string                  `json:"workflow_route_scope,omitempty"`
}

type workflowLink struct {
	route   workflowRoute
	indices []int
}

type workflowEvidenceInput struct {
	Method          string                        `json:"method"`
	Path            string                        `json:"path"`
	PatternKind     string                        `json:"pattern_kind,omitempty"`
	ContractIndices []int                         `json:"contract_indices"`
	Registration    string                        `json:"registration_evidence_id,omitempty"`
	Handler         string                        `json:"handler_evidence_id,omitempty"`
	DecisionCall    string                        `json:"decision_call_evidence_id,omitempty"`
	Decision        string                        `json:"decision_evidence_id,omitempty"`
	OutcomeCall     string                        `json:"outcome_call_evidence_id,omitempty"`
	Outcome         string                        `json:"outcome_evidence_id,omitempty"`
	OutcomeBasis    string                        `json:"outcome_basis,omitempty"`
	Test            string                        `json:"test_evidence_id,omitempty"`
	RelatedMutation *workflowRelatedMutationInput `json:"related_mutation,omitempty"`
}

type workflowRelatedMutationInput struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	Registration string `json:"registration_evidence_id,omitempty"`
	Handler      string `json:"handler_evidence_id,omitempty"`
}

func workflowRouteScope(r workflowRoute) string {
	dir := path.Dir(r.Handler.Path)
	for !record.ScopeUnder(r.Registration.Path, dir) {
		dir = path.Dir(dir)
	}
	return dir
}

func workflowRouteUnder(r workflowRoute, scope string) bool {
	return record.ScopeUnder(r.Registration.Path, scope) && record.ScopeUnder(r.Handler.Path, scope)
}

func (s *roleStage) workflowRouteDigest(r workflowRoute) string {
	return digestOf(fmt.Appendf(nil, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		s.j.RunID, s.j.AttemptID, s.j.Snapshot.Digest, r.Method, r.Path, r.PatternKind, r.Registration.ID, r.Handler.ID, r.DecisionCall.ID, r.Decision.ID))
}

type workflowHandlerKey struct {
	method, registration, handler, contracts string
	offset                                   int
}

func (r workflowRoute) handlerKey() workflowHandlerKey {
	// An unspecified method may share paths with method-specific behavior.
	if r.Method == "" || r.handlerOffset == 0 {
		return workflowHandlerKey{}
	}
	return workflowHandlerKey{method: r.Method, registration: r.Registration.Path, handler: r.Handler.Path, offset: r.handlerOffset}
}

// Round-robin route families so registrations for the same resource do not
// crowd out other parts of the service. Retain alternate patterns after one
// registration per handler and method.
func diverseWorkflowRoutes(routes []workflowRoute) []workflowRoute {
	type family struct{ dir, resource string }
	groups := map[family][]workflowRoute{}
	var order []family
	distinct, alternatives := partitionWorkflowRoutes(routes, workflowRoute.handlerKey)
	for _, route := range distinct {
		literals := make([]string, 0, 2)
		for _, part := range strings.Split(route.Path, "/") {
			if part != "" && !strings.ContainsAny(part, "{}:*") {
				literals = append(literals, part)
				if len(literals) == 2 {
					break
				}
			}
		}
		key := family{dir: path.Dir(route.Registration.Path), resource: strings.Join(literals, "/")}
		if len(groups[key]) == 0 {
			order = append(order, key)
		}
		groups[key] = append(groups[key], route)
	}
	out := make([]workflowRoute, 0, len(routes))
	for i := 0; len(out) < len(distinct); i++ {
		for _, key := range order {
			if i < len(groups[key]) {
				out = append(out, groups[key][i])
			}
		}
	}
	return append(out, alternatives...)
}

func partitionWorkflowRoutes[T any](routes []T, keyOf func(T) workflowHandlerKey) (distinct, alternatives []T) {
	seen := map[workflowHandlerKey]bool{}
	for _, row := range routes {
		key := keyOf(row)
		if key != (workflowHandlerKey{}) {
			if seen[key] {
				alternatives = append(alternatives, row)
				continue
			}
			seen[key] = true
		}
		distinct = append(distinct, row)
	}
	return distinct, alternatives
}

func (s *roleStage) workflowDocumentInput(in docsInput, routes []workflowRoute, instructions string) (docsInput, []workflowRoute) {
	routes = diverseWorkflowRoutes(routes)
	fitted := in
	sent := s.fitWorkflowRoutes(&fitted, routes, instructions)
	countCues := func(list []workflowRouteInput) int {
		n := 0
		for _, r := range list {
			if r.Handler != "" || r.SameHandler != "" {
				n++
			}
		}
		return n
	}
	cues := countCues(fitted.WorkflowRoutes)
	for (len(sent) < len(routes) || cues < len(sent)) && in.shortenOptionalLists() {
		other := in
		more := s.fitWorkflowRoutes(&other, routes, instructions)
		otherCues := countCues(other.WorkflowRoutes)
		if len(more) > len(sent) || (len(more) == len(sent) && otherCues > cues) {
			fitted, sent = other, more
			cues = otherCues
		}
	}
	return fitted, sent
}

// The document input has already been fitted. Route hints use its remaining
// space, including repair allowance, without removing any documentation.
func (s *roleStage) fitWorkflowRoutes(in *docsInput, routes []workflowRoute, instructions string) []workflowRoute {
	base, err := marshal(in)
	if err != nil {
		return nil
	}
	if len(routes) > 0 {
		scope := workflowRouteScope(routes[0])
		if len(scope) <= 160 && !slices.ContainsFunc(routes[1:], func(route workflowRoute) bool { return workflowRouteScope(route) != scope }) {
			in.WorkflowRouteScope = scope
		}
	}
	var sent []workflowRoute
	remaining := 0
	for _, route := range routes {
		scope := workflowRouteScope(route)
		if len(scope) > 160 {
			continue
		}
		if scope == in.WorkflowRouteScope {
			scope = ""
		}
		in.WorkflowRoutes = append(in.WorkflowRoutes, workflowRouteInput{
			ID: fmt.Sprintf("R%d", len(sent)+1), Method: route.Method, Path: route.Path, Scope: scope, PatternKind: route.PatternKind,
		})
		b, err := marshal(in)
		if err != nil || len(b)-len(base) > workflowCatalogBytes {
			in.WorkflowRoutes = in.WorkflowRoutes[:len(sent)]
			continue
		}
		promptSize, fits := s.fits(instructions, b, true)
		if !fits {
			in.WorkflowRoutes = in.WorkflowRoutes[:len(sent)]
			continue
		}
		sent = append(sent, route)
		remaining = min(workflowCatalogBytes-(len(b)-len(base)), PromptBudget-promptSize)
	}
	if len(sent) == 0 {
		in.WorkflowRouteScope = ""
	}
	addWorkflowHandlerCues(in.WorkflowRoutes, sent, remaining)
	return sent
}

// Spend only remaining catalog space on optional cues. Duplicate method and
// path pairs and parameter-only paths have the least context in the base list.
func addWorkflowHandlerCues(input []workflowRouteInput, routes []workflowRoute, remaining int) {
	type pattern struct{ method, path string }
	counts := map[pattern]int{}
	for _, route := range routes {
		counts[pattern{route.Method, route.Path}]++
	}
	first := map[workflowHandlerKey]string{}
	for i, route := range routes {
		key := route.handlerKey()
		if key == (workflowHandlerKey{}) {
			continue
		}
		if earlier := first[key]; earlier != "" {
			cost := len(`,"same_handler_as":`) + record.JSONLen(earlier)
			if cost <= remaining {
				input[i].SameHandler = earlier
				remaining -= cost
			}
		} else {
			first[key] = input[i].ID
		}
	}
	for _, preferred := range []bool{true, false} {
		for i, route := range routes {
			if remaining <= len(`,"handler":""`) {
				return
			}
			opaque := !slices.ContainsFunc(strings.Split(route.Path, "/"), func(segment string) bool {
				return segment != "" && !strings.ContainsAny(segment, "{}:*")
			})
			if (opaque || counts[pattern{route.Method, route.Path}] > 1) != preferred {
				continue
			}
			hint := workflowHandlerCue(route)
			cost := len(`,"handler":`) + record.JSONLen(hint)
			if hint == "" || cost > remaining {
				continue
			}
			input[i].Handler = hint
			remaining -= cost
		}
	}
}

func workflowHandlerCue(route workflowRoute) string {
	if route.Handler.ID == "" || record.Sensitive([]byte(route.Handler.Path)) || record.Sensitive([]byte(route.Symbol)) {
		return ""
	}
	file := workflowHandlerWords(strings.TrimSuffix(path.Base(route.Handler.Path), ".go"))
	name := workflowHandlerWords(route.Symbol)
	switch {
	case file == "":
		return name
	case name == "" || name == file:
		return file + ".go"
	default:
		return file + ".go " + name
	}
}

// The cue uses only short ASCII names. Case and identifier separators
// become word breaks so the model can read acronyms and compound names.
func workflowHandlerWords(value string) string {
	const maxChars = 30
	if len(value) > maxChars {
		return ""
	}
	upper := func(c byte) bool { return c >= 'A' && c <= 'Z' }
	lower := func(c byte) bool { return c >= 'a' && c <= 'z' }
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	var out strings.Builder
	for i := range len(value) {
		c := value[i]
		switch {
		case upper(c):
			if i > 0 && (lower(value[i-1]) || digit(value[i-1]) ||
				(upper(value[i-1]) && i+1 < len(value) && lower(value[i+1]))) {
				out.WriteByte(' ')
			}
			out.WriteByte(c + ('a' - 'A'))
		case lower(c) || digit(c):
			out.WriteByte(c)
		case c == '_' || c == '-':
			out.WriteByte(' ')
		default:
			return ""
		}
	}
	words := strings.Join(strings.Fields(out.String()), " ")
	if len(words) > maxChars {
		return ""
	}
	return words
}

func (s *roleStage) workflowSelections(raw json.RawMessage, offered []workflowRouteInput, routes []workflowRoute, acceptedIndices map[int]int, contracts []record.Contract) ([]record.WorkflowSelection, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) > record.MaxWorkflowRoutes {
		return nil, true
	}
	byID := make(map[string]workflowRoute, len(offered))
	for i, candidate := range offered {
		byID[candidate.ID] = routes[i]
	}
	var out []record.WorkflowSelection
	total, invalid := 0, false
	for _, row := range rows {
		var choice workflowChoice
		if err := domain.DecodeStrict(row, &choice); err != nil || len(choice.RouteIDs) == 0 ||
			len(choice.RouteIDs) > record.MaxWorkflowRoutes-total {
			invalid = true
			continue
		}
		index := acceptedIndices[choice.ContractIndex]
		if index == 0 || slices.ContainsFunc(out, func(x record.WorkflowSelection) bool { return x.ContractIndex == index }) {
			invalid = true
			continue
		}
		digests := make([]string, 0, len(choice.RouteIDs))
		for _, id := range choice.RouteIDs {
			route, ok := byID[id]
			if !ok || !workflowRouteUnder(route, contracts[index-1].AppliesTo) {
				invalid = true
				continue
			}
			digest := s.workflowRouteDigest(route)
			if slices.Contains(digests, digest) {
				invalid = true
				continue
			}
			digests = append(digests, digest)
		}
		if len(digests) > 0 {
			total += len(digests)
			out = append(out, record.WorkflowSelection{ContractIndex: index, RouteDigests: digests})
		}
	}
	slices.SortFunc(out, func(a, b record.WorkflowSelection) int { return a.ContractIndex - b.ContractIndex })
	return out, invalid
}

// A broad route catalog can warrant one compact request when the document
// response did not supply usable navigation. Keep the complete call allowance
// for the three core review roles still ahead and finish the primary result
// even when the optional request is unavailable or rejected.
func (s *roleStage) supplementWorkflowChoices(routes []workflowRoute, contracts []record.Contract) []record.WorkflowSelection {
	if len(routes) < minSupplementWorkflowRoutes || len(contracts) == 0 || len(s.rec.Calls) >= record.RoleMaxCalls || s.ctx.Err() != nil {
		return nil
	}
	const remainingCoreCalls = 3 * record.RoleMaxCalls
	used, err := callsUsed(s.j)
	if err != nil || s.h.maxCalls(s.j.Scope)-used <= remainingCoreCalls {
		return nil
	}
	instructions := s.instructions(workflowSelectionContract)
	var catalog docsInput
	offered := s.fitWorkflowRoutes(&catalog, diverseWorkflowRoutes(routes), instructions)
	if len(offered) < minSupplementWorkflowRoutes {
		return nil
	}
	in := workflowSelectionInput{Routes: catalog.WorkflowRoutes, Scope: catalog.WorkflowRouteScope}
	indices := make(map[int]int, len(contracts))
	for i, contract := range contracts {
		index := i + 1
		in.Contracts = append(in.Contracts, workflowContractInput{Index: index, Statement: contract.Statement, Scope: contract.AppliesTo})
		indices[index] = index
	}
	input, err := marshal(in)
	if err != nil {
		return nil
	}
	q := opencode.Request{Role: s.role, Scratch: s.j.Scratch, SnapshotDigest: s.j.Snapshot.Digest, Instructions: instructions,
		Input: input, ProtectedRoots: []string{s.j.Snapshot.SnapshotDir}}
	size := len(opencode.Prompt(q))
	if size > PromptBudget || s.reserveCall() != nil {
		return nil
	}
	reply := s.h.Caller.Review(s.ctx, s.runner, q)
	call := s.recordCall(reply, size, len(s.rec.Calls)+1)
	call.Purpose = record.WorkflowSelectionPurpose
	call.Outcome = reply.Reason
	var output struct {
		Selections json.RawMessage `json:"workflow_selections"`
	}
	var selected []record.WorkflowSelection
	switch {
	case call.Outcome == "model_output_invalid":
		call.Outcome, call.OutputErrors = callRejected, []string{"output_not_json"}
	case call.Outcome == "":
		call.Outcome = callAccepted
		if errs := decodeOutput(reply.Output, &output, "workflow_selections"); errs != nil {
			call.Outcome, call.OutputErrors = callRejected, errs
		} else {
			var invalid bool
			selected, invalid = s.workflowSelections(output.Selections, in.Routes, offered, indices, contracts)
			if invalid {
				s.rec.Limitations = addLim(s.rec.Limitations, record.LimWorkflowSelectionInvalid)
				if len(selected) == 0 {
					call.Outcome, call.OutputErrors = callRejected, []string{record.LimWorkflowSelectionInvalid}
				}
			}
		}
	case !record.ValidCode(call.Outcome):
		call.Outcome = "model_unavailable"
	}
	s.rec.Calls = append(s.rec.Calls, call)
	s.rec.Schema = record.SchemaVersionWorkflow
	return selected
}

// Replay selections against the same attempt's eligible, verified route
// registrations and implementations. An unavailable hint leaves regular
// corpus selection in place.
func (s *roleStage) selectedWorkflowRoutes(doc record.Record) ([]workflowLink, bool, error) {
	if len(doc.WorkflowSelections) == 0 {
		return nil, false, nil
	}
	if !doc.Accepted() || doc.Stage != record.StageDocuments || doc.RunID != s.j.RunID ||
		doc.AttemptID != s.j.AttemptID || doc.Snapshot != s.j.Snapshot.Digest {
		return nil, true, nil
	}
	selectedDigests := map[string]bool{}
	for _, selection := range doc.WorkflowSelections {
		for _, digest := range selection.RouteDigests {
			selectedDigests[digest] = true
		}
	}
	routes, err := s.workflowRoutesFor(func(route workflowRoute) bool {
		return selectedDigests[s.workflowRouteDigest(route)]
	})
	if err != nil {
		return nil, false, err
	}
	catalog := make(map[string]workflowRoute, len(routes))
	for _, route := range routes {
		catalog[s.workflowRouteDigest(route)] = route
	}
	var out []workflowLink
	linkIndex := map[string]int{}
	missing := false
	for _, row := range doc.WorkflowSelections {
		contract := doc.Contracts[row.ContractIndex-1]
		for _, digest := range row.RouteDigests {
			route, ok := catalog[digest]
			if !ok || !workflowRouteUnder(route, contract.AppliesTo) {
				missing = true
				continue
			}
			if i, exists := linkIndex[digest]; exists {
				out[i].indices = append(out[i].indices, row.ContractIndex)
			} else {
				linkIndex[digest] = len(out)
				out = append(out, workflowLink{route: route, indices: []int{row.ContractIndex}})
			}
		}
	}
	selected := make([]workflowRoute, 0, len(out))
	for _, link := range out {
		selected = append(selected, link.route)
	}
	tests, err := s.workflowTestWitnesses(selected, routes)
	if err != nil {
		return out, missing, err
	}
	for i, test := range tests {
		if test.excerpt.ID != "" && !slices.ContainsFunc(out[i].indices, func(index int) bool {
			scope := doc.Contracts[index-1].AppliesTo
			return !record.ScopeUnder(test.excerpt.Path, scope) || !test.related.inScope(scope)
		}) {
			out[i].route.Test = test.excerpt
			out[i].route.relatedTest = test.related
		}
		for _, index := range out[i].indices {
			out[i].route.scopeOutcome(doc.Contracts[index-1].AppliesTo)
		}
	}
	return out, missing, nil
}

func submittedWorkflowID(x record.Excerpt, sent []record.Excerpt) string {
	if x.ID == "" {
		return ""
	}
	for _, e := range sent {
		if e.Path == x.Path && e.SHA256 == x.SHA256 && e.Kind == x.Kind && e.Start <= x.Start && x.End <= e.End {
			return e.ID
		}
	}
	return ""
}

func workflowEvidence(links []workflowLink, sent []record.Excerpt) []workflowEvidenceInput {
	var out []workflowEvidenceInput
	for _, link := range links {
		r := link.route
		id := func(x record.Excerpt) string {
			return submittedWorkflowID(x, sent)
		}
		row := workflowEvidenceInput{Method: r.Method, Path: r.Path, PatternKind: r.PatternKind, ContractIndices: link.indices,
			Registration: id(r.Registration), Handler: id(r.Handler), DecisionCall: id(r.DecisionCall), Decision: id(r.Decision), Test: id(r.Test)}
		if outcome, call := id(r.Outcome), id(r.OutcomeCall); outcome != "" && row.DecisionCall != "" &&
			(r.OutcomeDepth == 0 || call != "") && (r.OutcomeDepth < 2 || row.Decision != "") {
			row.Outcome, row.OutcomeCall, row.OutcomeBasis = outcome, call, "conditional_source_unexecuted"
		}
		if related := r.relatedTest; row.Test != "" && row.Handler != "" && related.method != "" {
			row.RelatedMutation = &workflowRelatedMutationInput{Method: related.method, Path: related.pattern,
				Registration: id(related.registration), Handler: id(related.handler)}
		}
		if row.Handler != "" || row.Decision != "" || row.DecisionCall != "" || row.Outcome != "" {
			out = append(out, row)
		}
	}
	return out
}

func (s *roleStage) submittedWorkflowReading(links []workflowLink, sent []record.Excerpt) *record.WorkflowReading {
	for _, link := range links {
		r := link.route
		if r.relatedTest.method == "" || len(link.indices) == 0 || submittedWorkflowID(r.Registration, sent) == "" {
			continue
		}
		read := submittedWorkflowID(r.Handler, sent)
		change := submittedWorkflowID(r.relatedTest.handler, sent)
		test := submittedWorkflowID(r.Test, sent)
		if read != "" && change != "" && read != change && test != "" {
			return &record.WorkflowReading{ContractIndex: link.indices[0], RouteDigest: s.workflowRouteDigest(r),
				ReadEvidenceID: read, ChangeEvidenceID: change, TestEvidenceID: test}
		}
	}
	return nil
}

// Only offer locations with unseen source in the same contract scope.
// The overview still decides whether a fresh read can resolve its question.
func (s *roleStage) workflowNavigation(docs record.Record, links []workflowLink, sent []record.Excerpt) []contractNavigationCandidate {
	var out []contractNavigationCandidate
	for _, link := range links {
		label := strings.TrimSpace(link.route.Method + " " + link.route.Path)
		for _, e := range []record.Excerpt{link.route.Outcome, link.route.OutcomeCall, link.route.Decision, link.route.DecisionCall,
			link.route.relatedTest.handler, link.route.relatedTest.registration} {
			if e.ID == "" || !record.HasNewContractEvidence(e, sent) {
				continue
			}
			location := record.ContractLocation{Path: e.Path, SHA256: e.SHA256, Start: e.Start,
				End: e.End, Kind: e.Kind}
			var symbolHint string
			if e.ID == link.route.Decision.ID {
				symbolHint = link.route.DecisionSymbol
			}
			if e.ID == link.route.Outcome.ID {
				symbolHint = link.route.OutcomeSymbol
			}
			// Only persist a symbol the follow-up can verify in this range.
			if record.ValidContractLiteral(symbolHint) && containsContractLiteral(e.Text, symbolHint) {
				location.Symbol = symbolHint
			}
			location.ID = record.ContractLocationID(s.j.Snapshot.Digest, e.Path, e.SHA256, e.Start, e.End)
			var indices []int
			for _, i := range link.indices {
				if location.Valid(s.j.Snapshot.Digest, docs.Contracts[i-1].AppliesTo) {
					indices = append(indices, i)
				}
			}
			if len(indices) == 0 {
				continue
			}
			routeLabel := label
			if e.ID == link.route.relatedTest.handler.ID || e.ID == link.route.relatedTest.registration.ID {
				routeLabel = strings.TrimSpace(link.route.relatedTest.method + " " + link.route.relatedTest.pattern)
			}
			if i := slices.IndexFunc(out, func(c contractNavigationCandidate) bool { return c.location.ID == location.ID }); i >= 0 {
				union := slices.Concat(out[i].indices, indices)
				slices.Sort(union)
				out[i].indices = slices.Compact(union)
				if out[i].workflowRoute != routeLabel {
					out[i].workflowRoute = ""
				}
			} else {
				out = append(out, contractNavigationCandidate{location: location, indices: indices, workflowRoute: routeLabel, symbolHint: symbolHint})
			}
		}
	}
	return out
}

func workflowOverviewWindows(links []workflowLink) []record.Excerpt {
	var out []record.Excerpt
	add := func(x record.Excerpt) bool {
		if x.ID == "" {
			return false
		}
		lines, bytes := x.End-x.Start+1, record.JSONLen(x.Text)
		for _, e := range out {
			if httpContainsWindow(e, x) {
				return true
			}
			if e.Path != x.Path {
				continue
			}
			if e.Start <= x.End && x.Start <= e.End {
				return false
			}
			lines += e.End - e.Start + 1
			bytes += record.JSONLen(e.Text)
		}
		if len(out) < maxWorkflowOverviewWindows && lines <= roleMaxCodeLines && bytes <= roleMaxCodeFile {
			out = append(out, x)
			return true
		}
		return false
	}
	distinct, alternatives := partitionWorkflowRoutes(links, func(link workflowLink) workflowHandlerKey {
		key := link.route.handlerKey()
		if key.offset != 0 {
			key.contracts = fmt.Sprint(link.indices)
		}
		return key
	})
	// Keep one complete read and corroborating mutation together before
	// filling the remaining slots with independent handler previews.
	for _, link := range distinct {
		r := link.route
		if r.relatedTest.method == "" {
			continue
		}
		if add(r.Handler) && add(r.Registration) && add(r.Test) && add(r.relatedTest.handler) {
			break
		}
		out = out[:0]
	}
	for _, link := range distinct {
		add(link.route.Handler)
	}
	for _, link := range distinct {
		add(link.route.Registration)
	}
	for _, link := range distinct {
		windows := link.route.outcomeWindows()
		if len(windows) == 0 {
			continue
		}
		keep := len(out)
		for _, x := range append([]record.Excerpt{link.route.DecisionCall}, windows...) {
			if !add(x) {
				out = out[:keep]
				break
			}
		}
	}
	for _, link := range distinct {
		if r := link.route; r.relatedTest.method != "" && add(r.Test) {
			add(r.relatedTest.handler)
		}
	}
	for _, link := range links {
		add(link.route.Test)
	}
	for _, link := range distinct {
		if r := link.route; r.relatedTest.method != "" && add(r.Test) {
			add(r.relatedTest.registration)
		}
	}
	for _, link := range alternatives {
		add(link.route.Registration)
	}
	return out
}

func workflowFollowupLinks(selected []workflowLink, prior record.ContractComparison) []workflowLink {
	var out []workflowLink
	for _, link := range selected {
		if slices.Contains(link.indices, prior.ContractIndex) && workflowRouteUnder(link.route, prior.AppliesTo) {
			link.indices = []int{prior.ContractIndex}
			link.route.scopeOutcome(prior.AppliesTo)
			// Registration and handler are already scoped above. A downstream
			// call or definition can belong to a different directory.
			if !record.ScopeUnder(link.route.DecisionCall.Path, prior.AppliesTo) {
				link.route.DecisionCall = record.Excerpt{}
			}
			if !record.ScopeUnder(link.route.Decision.Path, prior.AppliesTo) {
				link.route.Decision = record.Excerpt{}
			}
			if !record.ScopeUnder(link.route.Test.Path, prior.AppliesTo) || !link.route.relatedTest.inScope(prior.AppliesTo) {
				link.route.Test = record.Excerpt{}
				link.route.relatedTest = httpRelatedTest{}
			}
			out = append(out, link)
		}
	}
	return out
}

func (r *workflowRoute) scopeOutcome(scope string) {
	if r.Outcome.ID != "" && !slices.ContainsFunc(r.outcomeWindows(), func(e record.Excerpt) bool {
		return !record.ScopeUnder(e.Path, scope)
	}) && record.ScopeUnder(r.Decision.Path, scope) {
		return
	}
	r.Outcome, r.OutcomeCall, r.OutcomeSymbol, r.OutcomeDepth = record.Excerpt{}, record.Excerpt{}, "", 0
}

func (r workflowRoute) outcomeWindows() []record.Excerpt {
	if r.Outcome.ID == "" {
		return nil
	}
	switch r.OutcomeDepth {
	case 2:
		return []record.Excerpt{r.Decision, r.OutcomeCall, r.Outcome}
	case 1:
		return []record.Excerpt{r.OutcomeCall, r.Outcome}
	default:
		return []record.Excerpt{r.Outcome}
	}
}

// Merge structural choices with existing navigation, keeping one location
// per bounded source range.
func mergeWorkflowNavigation(routes, ordinary []contractNavigationCandidate) []contractNavigationCandidate {
	for _, c := range ordinary {
		if len(routes) == navigationMaxLocations {
			break
		}
		i := slices.IndexFunc(routes, func(x contractNavigationCandidate) bool { return x.location.ID == c.location.ID })
		if i < 0 {
			routes = append(routes, c)
			continue
		}
		indices := slices.Concat(routes[i].indices, c.indices)
		slices.Sort(indices)
		routes[i].indices = slices.Compact(indices)
	}
	return routes
}
