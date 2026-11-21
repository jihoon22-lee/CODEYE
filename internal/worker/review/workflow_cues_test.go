package review

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review"
)

func TestWorkflowChoiceInstructionsContainNoEligibleAnswer(t *testing.T) {
	eligibleChoice := regexp.MustCompile(`"R[1-9][0-9]*"|"contract_index"\s*:\s*[1-6]`)
	for _, contract := range []string{docsWorkflowContract, workflowSelectionContract} {
		if eligibleChoice.MatchString(contract) || !strings.Contains(contract, "contract_index") ||
			!strings.Contains(contract, "route_ids") || !strings.Contains(contract, "workflow_selections") {
			t.Fatalf("workflow format must name the fields without giving a selectable answer: %s", contract)
		}
	}
}

func TestWorkflowCuesDistinguishIdenticalPatterns(t *testing.T) {
	server := func(pkg, handler string) string {
		return "package " + pkg + "\nimport \"net/http\"\n" +
			"func Routes(m *http.ServeMux) { m.HandleFunc(\"GET /{id}\", " + handler + ") }\n" +
			"func " + handler + "(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }\n"
	}
	j := treeJob(t, map[string]string{
		"storage/material.go":   server("storage", "showPDFCopy"),
		"public/reading.go":     server("public", "readPage"),
		"hidden/unpublished.go": server("hidden", "unlistedFeature"),
	})
	s := roleStage{ctx: t.Context(), j: j, role: record.StageDocuments, screen: secretScreen{
		withheld: map[string]bool{"hidden/unpublished.go": true},
	}}
	s.pack, _ = reviewpack.For(record.StageDocuments, []string{"go"})
	routes, err := s.workflowRoutes()
	if err != nil {
		t.Fatal(err)
	}
	in, offered := s.workflowDocumentInput(docsInput{}, routes, s.instructions(docsWorkflowContract))
	if len(offered) != 2 || len(in.WorkflowRoutes) != 2 {
		t.Fatalf("expected two eligible routes with the same pattern: %+v", in.WorkflowRoutes)
	}
	i := slices.IndexFunc(in.WorkflowRoutes, func(route workflowRouteInput) bool {
		return route.Handler == "material.go show pdf copy"
	})
	if i < 0 || in.WorkflowRoutes[i].Path != "/{id}" || in.WorkflowRoutes[i].Scope != "storage" ||
		in.WorkflowRoutes[1-i].Handler != "reading.go read page" {
		t.Fatalf("handler names do not distinguish routes: %+v", in.WorkflowRoutes)
	}
	contracts := []record.Contract{{AppliesTo: "storage"}}
	indices := map[int]int{1: 1}
	selectRoutes := func(ids ...string) ([]record.WorkflowSelection, bool) {
		t.Helper()
		b, err := json.Marshal([]workflowChoice{{ContractIndex: 1, RouteIDs: ids}})
		if err != nil {
			t.Fatal(err)
		}
		return s.workflowSelections(b, in.WorkflowRoutes, offered, indices, contracts)
	}
	if selected, invalid := selectRoutes(in.WorkflowRoutes[i].ID); invalid || len(selected) != 1 ||
		!slices.Equal(selected[0].RouteDigests, []string{s.workflowRouteDigest(offered[i])}) {
		t.Fatalf("selection did not bind to the proved route: %+v invalid=%v", selected, invalid)
	}
	if selected, invalid := selectRoutes(in.WorkflowRoutes[1-i].ID, in.WorkflowRoutes[i].Handler); !invalid || len(selected) != 0 {
		t.Fatalf("out-of-scope ID or text cue selected a route: %+v invalid=%v", selected, invalid)
	}
}

func TestWorkflowCuesAreBoundedAndOptional(t *testing.T) {
	makeRoute := func(pattern, file, symbol string) workflowRoute {
		return workflowRoute{Method: "GET", Path: pattern, Symbol: symbol, Handler: record.Excerpt{ID: "source", Path: file}}
	}
	routes := []workflowRoute{
		makeRoute("/named", "svc/nav.go", "openPanel"),
		makeRoute("/-{id}", "svc/read.go", "showPage"),
		makeRoute("/other", "svc/state.go", "ghp_0123456789abcdef1234"),
		makeRoute("/other", "svc/sk-0123456789abcdef1234.go", "status"),
		makeRoute("/long", "svc/read.go", strings.Repeat("x", 128)),
	}
	var output = make([]workflowRouteInput, len(routes))
	cueCost := len(`,"handler":`) + record.JSONLen("read.go show page")
	addWorkflowHandlerCues(output, routes, cueCost)
	if output[0].Handler != "" || output[1].Handler != "read.go show page" || output[2].Handler != "" || output[3].Handler != "" ||
		output[4].Handler != "" {
		t.Fatalf("opaque path should have the only cue that fits: %+v", output)
	}
	addWorkflowHandlerCues(output, routes, workflowCatalogBytes)
	if output[0].Handler != "nav.go open panel" || output[2].Handler != "" || output[3].Handler != "" ||
		output[4].Handler != "read.go" {
		t.Fatalf("cue leaked an ineligible identifier or lost a safe one: %+v", output)
	}
}
