package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func routeOutcomeTree() map[string]string {
	return map[string]string{
		"go.mod": "module example.test/dispatch\n",
		"api/routes.go": `package api
import (
	"net/http"
	"example.test/dispatch/ledger"
)
type Desk struct { store *ledger.Ledger }
func (d *Desk) routes(m *http.ServeMux) {
	m.HandleFunc("POST /slots", d.save)
	m.HandleFunc("GET /slots/{slot}", d.lookup)
}
func (d *Desk) save(w http.ResponseWriter, r *http.Request) {
	_, _ = d.store.Reserve(r.PathValue("slot"))
}
func (d *Desk) lookup(w http.ResponseWriter, r *http.Request) {
	_, _ = d.store.Lookup(r.PathValue("slot"))
}
`,
		"ledger/wrapper.go": `package ledger
import "database/sql"
type Entry struct { Slot string }
type Ledger struct { tx *sql.Tx }
func inBatch[T any](s *Ledger, cb func(*sql.Tx) (T, error)) (T, error) {
	v, err := cb(s.tx)
	return v, err
}
func (s *Ledger) Reserve(slot string) (Entry, error) {
` + strings.Repeat("\tif slot == \"missing\" { return Entry{}, nil }\n", 18) + `
	return inBatch[Entry](s, func(tx *sql.Tx) (Entry, error) {
		if slot == "walk-in" { return Entry{}, nil }
		return putSlot(tx, slot)
	})
}
func (s *Ledger) Lookup(slot string) (Entry, error) {
	return s.loadSlot(slot)
}
`,
		"ledger/state.go": `package ledger
import "database/sql"
func putSlot(tx *sql.Tx, slot string) (Entry, error) {
	entry := Entry{Slot: slot}
	if _, err := tx.Exec("INSERT INTO slots VALUES (?)", slot); err != nil {
		return Entry{}, err
	}
	return entry, nil
}
func (s *Ledger) loadSlot(slot string) (Entry, error) {
` + strings.Repeat("\tif slot == \"missing\" { return Entry{}, nil }\n", 18) + `
	var found Entry
	if err := s.tx.QueryRow("SELECT slot FROM slots WHERE slot = ?", slot).Scan(&found.Slot); err != nil {
		return Entry{}, err
	}
	return found, nil
}
`,
	}
}

func TestGoHTTPWorkflowOutcomeLinksReturnedStoreOperations(t *testing.T) {
	s := roleStage{ctx: t.Context(), j: treeJob(t, routeOutcomeTree())}
	routes, err := s.workflowRoutesFor(func(workflowRoute) bool { return true })
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes %+v, error %v", routes, err)
	}
	for i, tc := range []struct{ bridge, terminal, marker string }{
		{"return putSlot(tx, slot)", "putSlot", `tx.Exec("INSERT INTO slots`},
		{"return s.loadSlot(slot)", "loadSlot", `s.tx.QueryRow("SELECT slot FROM slots`},
	} {
		r := routes[i]
		if r.OutcomeDepth != 1 || r.Outcome.Path != "ledger/state.go" || r.OutcomeSymbol != tc.terminal ||
			!strings.Contains(r.OutcomeCall.Text, tc.bridge) || !strings.Contains(r.Outcome.Text, tc.marker) ||
			!strings.Contains(r.Outcome.Text, "return") || strings.Contains(r.Decision.Text, tc.marker) {
			t.Fatalf("route lost a link or state operation: %+v", r)
		}
		if r.Outcome.End-r.Outcome.Start+1 > record.MaxContractLocationLines || record.JSONLen(r.Outcome.Text) > roleMaxWindowBytes {
			t.Fatalf("outcome exceeded evidence window: %+v", r.Outcome)
		}
		if got, err := s.withText(r.outcomeWindows()); err != nil || !slices.Equal(got, r.outcomeWindows()) {
			t.Fatalf("outcome did not replay at exact snapshot lines: %+v, %v", got, err)
		}
	}
}

func TestGoHTTPWorkflowOutcomeRequiresAnExactBoundedChain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]string, *roleStage)
	}{
		{name: "ignored callback", change: func(f map[string]string, _ *roleStage) {
			f["ledger/wrapper.go"] = strings.Replace(f["ledger/wrapper.go"], "v, err := cb(s.tx)", "var v T; var err error", 1)
		}},
		{name: "replaced callback", change: func(f map[string]string, _ *roleStage) {
			f["ledger/wrapper.go"] = strings.Replace(f["ledger/wrapper.go"], "v, err := cb(s.tx)", "cb = func(*sql.Tx) (T, error) { return *new(T), nil }; v, err := cb(s.tx)", 1)
		}},
		{name: "local shadow", change: func(f map[string]string, _ *roleStage) {
			f["ledger/wrapper.go"] = strings.Replace(f["ledger/wrapper.go"], "return putSlot(tx, slot)", "putSlot := fallback; return putSlot(tx, slot)", 1)
		}},
		{name: "malformed helper call", change: func(f map[string]string, _ *roleStage) {
			f["ledger/wrapper.go"] = strings.Replace(f["ledger/wrapper.go"], "return putSlot(tx, slot)", "return putSlot(tx, slot, extra)", 1)
		}},
		{name: "duplicate target", change: func(f map[string]string, _ *roleStage) {
			f["ledger/duplicate.go"] = `package ledger
import "database/sql"
func putSlot(tx *sql.Tx, slot string) (Entry, error) { return Entry{}, nil }
`
		}},
		{name: "untyped SQL lookalike", change: func(f map[string]string, _ *roleStage) {
			f["ledger/state.go"] = strings.Replace(f["ledger/state.go"], "func putSlot(tx *sql.Tx", "func putSlot(tx *Fake", 1)
		}},
		{name: "detached effect", change: func(f map[string]string, _ *roleStage) {
			f["ledger/state.go"] = strings.Replace(f["ledger/state.go"], `if _, err := tx.Exec("INSERT INTO slots VALUES (?)", slot); err != nil {
		return Entry{}, err
	}`, `go tx.Exec("INSERT INTO slots VALUES (?)", slot)`, 1)
		}},
		{name: "overlarge terminal statement", change: func(f map[string]string, _ *roleStage) {
			f["ledger/state.go"] = strings.Replace(f["ledger/state.go"], `"INSERT INTO slots VALUES (?)"`, fmt.Sprintf(`"INSERT INTO slots VALUES (%s)"`, strings.Repeat("x", roleMaxWindowBytes)), 1)
		}},
		{name: "screened terminal", change: func(_ map[string]string, s *roleStage) {
			s.screen.withheld = map[string]bool{"ledger/state.go": true}
		}},
		{name: "terminal outside PR changes", change: func(_ map[string]string, s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"api/routes.go", "ledger/wrapper.go"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := routeOutcomeTree()
			s := roleStage{ctx: t.Context()}
			tc.change(f, &s)
			j := treeJob(t, f)
			j.PullRequest = s.j.PullRequest
			s.j = j
			routes, err := s.workflowRoutesFor(func(workflowRoute) bool { return true })
			if err != nil || len(routes) != 2 || routes[0].Outcome.ID != "" {
				t.Fatalf("unsupported outcome link: %+v, %v", routes, err)
			}
		})
	}
}

func TestGoHTTPWorkflowOutcomeReplaysWithScopedConditionalEvidence(t *testing.T) {
	s := roleStage{ctx: t.Context(), j: treeJob(t, routeOutcomeTree())}
	routes, err := s.workflowRoutes()
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes %+v, error %v", routes, err)
	}
	for _, scope := range []string{".", "api"} {
		t.Run(scope, func(t *testing.T) {
			catalogRoute := routes[0]
			if catalogRoute.Outcome.ID != "" {
				t.Fatal("document discovery scanned outcomes before a route was selected")
			}
			doc := record.Record{Stage: record.StageDocuments, Outcome: record.OutcomeAccepted,
				RunID: s.j.RunID, AttemptID: s.j.AttemptID, Snapshot: s.j.Snapshot.Digest,
				Contracts: []record.Contract{{AppliesTo: scope}}, WorkflowSelections: []record.WorkflowSelection{{ContractIndex: 1, RouteDigests: []string{s.workflowRouteDigest(catalogRoute)}}}}
			links, missing, err := s.selectedWorkflowRoutes(doc)
			if err != nil || missing || len(links) != 1 {
				t.Fatalf("selection did not replay: %+v, %t, %v", links, missing, err)
			}
			original := links[0].route
			if s.workflowRouteDigest(original) != s.workflowRouteDigest(catalogRoute) {
				t.Fatal("optional outcome changed the accepted route digest")
			}
			sent := workflowOverviewWindows(links)
			rows := workflowEvidence(links, sent)
			if len(rows) != 1 {
				t.Fatalf("expected one workflow row: %+v", rows)
			}
			row := rows[0]
			if scope == "." {
				if row.Outcome != original.Outcome.ID || row.OutcomeCall != original.OutcomeCall.ID || row.OutcomeBasis != "conditional_source_unexecuted" {
					t.Fatalf("missing linked conditional outcome annotation: %+v", row)
				}
				rows = workflowEvidence(links, []record.Excerpt{original.Handler, original.Outcome})
				if len(rows) != 1 || rows[0].Outcome != "" {
					t.Fatalf("outcome link was annotated without its forwarding call: %+v", rows)
				}
			} else if row.Outcome != "" || links[0].route.Outcome.ID != "" {
				t.Fatalf("out-of-scope outcome persisted: %+v", row)
			}
		})
	}
}

func TestGoHTTPWorkflowOutcomeKeepsTwoBoundedStepsIntoVersionedSQL(t *testing.T) {
	tree := routeOutcomeTree()
	for _, p := range []string{"ledger/wrapper.go", "ledger/state.go"} {
		text := strings.NewReplacer(`"database/sql"`, `"github.com/jackc/pgx/v5"`, "*sql.Tx", "pgx.Tx", `tx.Exec("`, `tx.Exec(nil, "`, `tx.QueryRow("`, `tx.QueryRow(nil, "`).Replace(tree[p])
		tree[p] = text
	}
	tree["ledger/wrapper.go"] = strings.Replace(tree["ledger/wrapper.go"], "func (s *Ledger) Reserve", `func (s *Ledger) Reserve(slot string) (Entry, error) { return s.reserveSlot(slot) }
func (s *Ledger) reserveSlot`, 1)
	s := roleStage{ctx: t.Context(), j: treeJob(t, tree)}
	routes, err := s.workflowRoutesFor(func(workflowRoute) bool { return true })
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes: %+v, %v", routes, err)
	}
	r := routes[0]
	if r.OutcomeDepth != 2 || !strings.Contains(r.Decision.Text, "return s.reserveSlot(slot)") ||
		!strings.Contains(r.OutcomeCall.Text, "return putSlot(tx, slot)") || !strings.Contains(r.Outcome.Text, "INSERT INTO slots") {
		t.Fatalf("missing two-hop state chain: %+v", r)
	}
	links := []workflowLink{{route: r, indices: []int{1}}}
	row := workflowEvidence(links, workflowOverviewWindows(links))
	if len(row) != 1 || row[0].Outcome == "" || row[0].Decision == "" {
		t.Fatalf("full bounded evidence chain did not fit overview: %+v", row)
	}
	row = workflowEvidence(links, []record.Excerpt{r.Handler, r.OutcomeCall, r.Outcome})
	if len(row) != 1 || row[0].Outcome != "" {
		t.Fatalf("omitted first forwarding statement still advertised an outcome chain: %+v", row)
	}
}
