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

const workflowSelectionContract = `For this supplemental documents request use exactly {"workflow_selections":[{"contract_index":1,"route_ids":["R1"]}]}. Copy only contract indices and route IDs from this input. Use at most four route IDs in total. Return an empty array if no route applies.`

type workflowRouteInput struct {
	ID          string `json:"id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Scope       string `json:"scope,omitempty"`
	PatternKind string `json:"pattern_kind,omitempty"`
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
	Method          string `json:"method"`
	Path            string `json:"path"`
	PatternKind     string `json:"pattern_kind,omitempty"`
	ContractIndices []int  `json:"contract_indices"`
	Registration    string `json:"registration_evidence_id,omitempty"`
	Handler         string `json:"handler_evidence_id,omitempty"`
	DecisionCall    string `json:"decision_call_evidence_id,omitempty"`
	Decision        string `json:"decision_evidence_id,omitempty"`
	Test            string `json:"test_evidence_id,omitempty"`
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

// Round-robin route families so a long set of registrations for the same
// resource does not crowd out other parts of the service.
func diverseWorkflowRoutes(routes []workflowRoute) []workflowRoute {
	type family struct{ dir, resource string }
	groups := map[family][]workflowRoute{}
	var order []family
	for _, route := range routes {
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
	for i := 0; len(out) < len(routes); i++ {
		for _, key := range order {
			if i < len(groups[key]) {
				out = append(out, groups[key][i])
			}
		}
	}
	return out
}

func (s *roleStage) workflowDocumentInput(in docsInput, routes []workflowRoute, instructions string) (docsInput, []workflowRoute) {
	routes = diverseWorkflowRoutes(routes)
	fitted := in
	sent := s.fitWorkflowRoutes(&fitted, routes, instructions)
	for len(sent) < len(routes) && in.shortenOptionalLists() {
		other := in
		more := s.fitWorkflowRoutes(&other, routes, instructions)
		if len(more) > len(sent) {
			fitted, sent = other, more
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
		if _, fits := s.fits(instructions, b, true); !fits {
			in.WorkflowRoutes = in.WorkflowRoutes[:len(sent)]
			continue
		}
		sent = append(sent, route)
	}
	if len(sent) == 0 {
		in.WorkflowRouteScope = ""
	}
	return sent
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
	if err != nil || s.h.maxCalls()-used <= remainingCoreCalls {
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
	routes, err := s.workflowRoutes()
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
	tests, err := s.workflowRouteTests(selected, routes)
	if err != nil {
		return out, missing, err
	}
	for i, test := range tests {
		if test.ID != "" && !slices.ContainsFunc(out[i].indices, func(index int) bool {
			return !record.ScopeUnder(test.Path, doc.Contracts[index-1].AppliesTo)
		}) {
			out[i].route.Test = test
		}
	}
	return out, missing, nil
}

func workflowEvidence(links []workflowLink, sent []record.Excerpt) []workflowEvidenceInput {
	var out []workflowEvidenceInput
	for _, link := range links {
		r := link.route
		id := func(x record.Excerpt) string {
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
		row := workflowEvidenceInput{Method: r.Method, Path: r.Path, PatternKind: r.PatternKind, ContractIndices: link.indices,
			Registration: id(r.Registration), Handler: id(r.Handler), DecisionCall: id(r.DecisionCall), Decision: id(r.Decision), Test: id(r.Test)}
		if row.Handler != "" || row.Decision != "" || row.DecisionCall != "" {
			out = append(out, row)
		}
	}
	return out
}

// Only offer locations with unseen source in the same contract scope.
// The overview still decides whether a fresh read can resolve its question.
func (s *roleStage) workflowNavigation(docs record.Record, links []workflowLink, sent []record.Excerpt) []contractNavigationCandidate {
	var out []contractNavigationCandidate
	for _, link := range links {
		label := strings.TrimSpace(link.route.Method + " " + link.route.Path)
		for _, e := range []record.Excerpt{link.route.Decision, link.route.DecisionCall} {
			if e.ID == "" || !record.HasNewContractEvidence(e, sent) {
				continue
			}
			location := record.ContractLocation{Path: e.Path, SHA256: e.SHA256, Start: e.Start,
				End: e.End, Kind: e.Kind}
			var symbolHint string
			if e.ID == link.route.Decision.ID {
				symbolHint = link.route.DecisionSymbol
				// Only persist a symbol the follow-up can verify in this range.
				if containsContractLiteral(e.Text, symbolHint) {
					location.Symbol = symbolHint
				}
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
			if i := slices.IndexFunc(out, func(c contractNavigationCandidate) bool { return c.location.ID == location.ID }); i >= 0 {
				union := slices.Concat(out[i].indices, indices)
				slices.Sort(union)
				out[i].indices = slices.Compact(union)
				if out[i].workflowRoute != label {
					out[i].workflowRoute = ""
				}
			} else {
				out = append(out, contractNavigationCandidate{location: location, indices: indices, workflowRoute: label, symbolHint: symbolHint})
			}
		}
	}
	return out
}

func workflowOverviewWindows(links []workflowLink) []record.Excerpt {
	var out []record.Excerpt
	add := func(x record.Excerpt) {
		if x.ID == "" || len(out) == maxWorkflowOverviewWindows {
			return
		}
		lines, bytes := x.End-x.Start+1, record.JSONLen(x.Text)
		for _, e := range out {
			if e.Path != x.Path {
				continue
			}
			if e.Start <= x.End && x.Start <= e.End {
				return
			}
			lines += e.End - e.Start + 1
			bytes += record.JSONLen(e.Text)
		}
		if lines <= roleMaxCodeLines && bytes <= roleMaxCodeFile {
			out = append(out, x)
		}
	}
	for _, link := range links {
		add(link.route.Handler)
	}
	for _, link := range links {
		add(link.route.Registration)
	}
	for _, link := range links {
		add(link.route.Test)
	}
	return out
}

func workflowFollowupLinks(selected []workflowLink, prior record.ContractComparison) []workflowLink {
	var out []workflowLink
	for _, link := range selected {
		if slices.Contains(link.indices, prior.ContractIndex) && workflowRouteUnder(link.route, prior.AppliesTo) {
			link.indices = []int{prior.ContractIndex}
			// Registration and handler are already scoped above. A downstream
			// call or definition can belong to a different directory.
			if !record.ScopeUnder(link.route.DecisionCall.Path, prior.AppliesTo) {
				link.route.DecisionCall = record.Excerpt{}
			}
			if !record.ScopeUnder(link.route.Decision.Path, prior.AppliesTo) {
				link.route.Decision = record.Excerpt{}
			}
			if !record.ScopeUnder(link.route.Test.Path, prior.AppliesTo) {
				link.route.Test = record.Excerpt{}
			}
			out = append(out, link)
		}
	}
	return out
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
