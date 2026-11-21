package review

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const workflowReadingFixture = "../../../fixtures/modules/workflow-reading/input"

func archiveWorkflowSource(t *testing.T, name string) string {
	t.Helper()
	return string(mustRead(t, filepath.Join(workflowReadingFixture, "archive", name)))
}

func TestWorkflowCatalogPrioritizesDifferentHandlersBeforeAlternates(t *testing.T) {
	s := roleStage{ctx: t.Context(), j: treeJob(t, map[string]string{"archive/routes.go": archiveWorkflowSource(t, "routes.go")})}
	routes, err := s.workflowRoutes()
	if err != nil || len(routes) != 3 {
		t.Fatalf("routes: %+v, %v", routes, err)
	}
	sorted := diverseWorkflowRoutes(routes)
	if sorted[0].Path != "/browse/{label}" || sorted[1].Method != "DELETE" || sorted[2].Path != "/legacy/{label}" {
		t.Fatalf("same-method registrations crowd out another operation: %+v", sorted)
	}
	catalog := []workflowRouteInput{{ID: "R1"}, {ID: "R2"}, {ID: "R3"}}
	addWorkflowHandlerCues(catalog, sorted, workflowCatalogBytes)
	if catalog[2].SameHandler != "R1" || catalog[0].SameHandler != "" || catalog[1].SameHandler != "" {
		t.Fatalf("shared handler cue is missing or ambiguous: %+v", catalog)
	}
}

func TestWorkflowOverviewKeepsCompleteRelatedEvidenceWhenPreviewSlotsAreFull(t *testing.T) {
	production := strings.Replace(archiveWorkflowSource(t, "routes.go"), "	s.router.HandleFunc", `	s.router.HandleFunc("GET /catalog", status)
	s.router.HandleFunc("POST /catalog", create)
	s.router.HandleFunc`, 1) + `
func status(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
func create(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) }
`
	j := treeJob(t, map[string]string{"archive/routes.go": production, "archive/routes_test.go": archiveWorkflowSource(t, "routes_test.go")})
	s := roleStage{ctx: t.Context(), j: j}
	catalog, err := s.workflowRoutes()
	if err != nil || len(catalog) != 5 {
		t.Fatalf("routes: %+v, %v", catalog, err)
	}
	var digests []string
	var read string
	for _, r := range catalog {
		switch r.Path {
		case "/browse/{label}":
			read = s.workflowRouteDigest(r)
		case "/legacy/{label}":
		default:
			digests = append(digests, s.workflowRouteDigest(r))
		}
	}
	// Put the supported read last, after three independent previews.
	digests = append(digests, read)
	doc := record.Record{RunID: j.RunID, AttemptID: j.AttemptID, Snapshot: j.Snapshot.Digest,
		Stage: record.StageDocuments, Outcome: record.OutcomeAccepted,
		Contracts:          []record.Contract{{AppliesTo: "archive"}},
		WorkflowSelections: []record.WorkflowSelection{{ContractIndex: 1, RouteDigests: digests}}}
	links, missing, err := s.selectedWorkflowRoutes(doc)
	if err != nil || missing || len(links) != 4 {
		t.Fatalf("workflow: %+v missing %v error %v", links, missing, err)
	}
	windows := workflowOverviewWindows(links)
	rows := workflowEvidence(links, windows)
	if len(windows) > maxWorkflowOverviewWindows || !slices.ContainsFunc(rows, func(row workflowEvidenceInput) bool {
		return row.Path == "/browse/{label}" && row.Registration != "" && row.Handler != "" && row.Test != "" &&
			row.RelatedMutation != nil && row.RelatedMutation.Handler != ""
	}) {
		t.Fatalf("route previews displaced the connected read and mutation: %+v", rows)
	}
}

func TestWorkflowUsesRelatedMutationWithSameStoreKeyAndTypedRead(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement string
		production             bool
		setup                  func(*roleStage)
		want                   bool
	}{
		{name: "same checked key", want: true},
		{name: "different key", old: `contents.Lookup("novels")`, replacement: `contents.Lookup("essays")`},
		{name: "different response", old: "rec.Code != http.StatusNoContent", replacement: "rec.Code != http.StatusBadRequest"},
		{name: "different assertion type", old: "err.(EmptyShelf); !ok", replacement: "err.(error); !ok"},
		{name: "ambiguous status", old: "rec.Code != http.StatusNoContent", replacement: "rec.Code != http.StatusNoContent && rec.Code != http.StatusOK"},
		{name: "intervening state operation", old: `_, err = contents.Lookup("novels")`, replacement: `_ = contents.Remove("novels"); _, err = contents.Lookup("novels")`},
		{name: "different injected store", old: "app := New(contents)", replacement: "app := New(displayCase{})"},
		{name: "handler does not use read error", old: "if _, ok := err.(EmptyShelf); ok", replacement: "if err != nil", production: true},
		{name: "handler uses different parameter", old: `label := r.PathValue("label")`, replacement: `label := r.PathValue("other")`, production: true},
		{name: "handler transforms the key", old: `label := r.PathValue("label")`, replacement: `label := "other-" + r.PathValue("label")`, production: true},
		{name: "withheld test", setup: func(s *roleStage) {
			s.screen.withheld = map[string]bool{"archive/routes_test.go": true}
		}},
		{name: "unchanged PR test", setup: func(s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"archive/routes.go"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			production, test := archiveWorkflowSource(t, "routes.go"), archiveWorkflowSource(t, "routes_test.go")
			if tc.production {
				production = strings.Replace(production, tc.old, tc.replacement, 1)
			} else if tc.old != "" {
				test = strings.Replace(test, tc.old, tc.replacement, 1)
			}
			j := treeJob(t, map[string]string{"archive/routes.go": production, "archive/routes_test.go": test})
			s := roleStage{ctx: t.Context(), j: j}
			if tc.setup != nil {
				tc.setup(&s)
			}
			catalog, err := s.workflowRoutes()
			if err != nil || len(catalog) != 3 {
				t.Fatalf("routes: %+v, %v", catalog, err)
			}
			doc := record.Record{RunID: j.RunID, AttemptID: j.AttemptID, Snapshot: j.Snapshot.Digest,
				Stage: record.StageDocuments, Outcome: record.OutcomeAccepted,
				Contracts: []record.Contract{{AppliesTo: "archive"}},
				WorkflowSelections: []record.WorkflowSelection{{ContractIndex: 1,
					RouteDigests: []string{s.workflowRouteDigest(catalog[0]), s.workflowRouteDigest(catalog[1])}}}}
			links, missing, err := s.selectedWorkflowRoutes(doc)
			if err != nil || missing || len(links) != 2 {
				t.Fatalf("replay: %+v, missing=%v, %v", links, missing, err)
			}
			windows := workflowOverviewWindows(links)
			handoff := workflowEvidence(links, windows)
			if len(windows) > maxWorkflowOverviewWindows || len(handoff) != 2 {
				t.Fatalf("workflow changed scope or budget: %+v", handoff)
			}
			read := handoff[0]
			if !tc.want {
				if read.Test != "" || read.RelatedMutation != nil {
					t.Fatalf("unproved postcondition associated with the read: %+v", read)
				}
				return
			}
			byID := catalogOf(windows)
			testWindow := byID[read.Test]
			if read.RelatedMutation == nil || read.RelatedMutation.Method != "DELETE" ||
				!strings.Contains(byID[read.RelatedMutation.Handler].Text, "s.shelf.Remove") ||
				testWindow.Kind != "test" || !strings.Contains(testWindow.Text, `contents := displayCase{"novels": "M"}`) ||
				!strings.Contains(testWindow.Text, `http.NewRequest(http.MethodDelete, "/catalog/novels"`) ||
				!strings.Contains(testWindow.Text, `contents.Lookup("novels")`) ||
				!strings.Contains(testWindow.Text, "err.(EmptyShelf)") {
				t.Fatalf("missing corroborating source: %+v, %+v", read, testWindow)
			}
			trimmed := slices.DeleteFunc(slices.Clone(windows), func(e record.Excerpt) bool { return e.ID == testWindow.ID })
			if row := workflowEvidence(links, trimmed)[0]; row.Test != "" || row.RelatedMutation != nil {
				t.Fatalf("trimmed test left a dangling reference: %+v", row)
			}
			if s.submittedWorkflowReading(links, windows) == nil || s.submittedWorkflowReading(links, trimmed) != nil {
				t.Fatal("workflow context did not follow the exact submitted windows")
			}
		})
	}
}
