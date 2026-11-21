package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const workflowFixture = "../../../fixtures/modules/workflows/input"

func TestWorkflowSelectionsReachReview(t *testing.T) {
	for _, kind := range []string{"selected", "invalid", "omitted"} {
		t.Run(kind, func(t *testing.T) {
			j := dirJob(t, workflowFixture)
			j.Snapshot.Digest, j.RunID, j.AttemptID = exampleSnapshot, exampleRun, exampleAttempt
			const statement = "선반에서 내린 찻잔은 이후 목록에서 제외된다."
			caller := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
				"documents": func(_ int, q opencode.Request) opencode.Reply {
					in := decodeAs[docsInput](t, q)
					var selected []string
					for _, r := range in.WorkflowRoutes {
						if (r.Method == "DELETE" && r.Path == "/cups/{cup}") || (r.Method == "GET" && r.Path == "/cups") {
							selected = append(selected, r.ID)
						}
					}
					if len(in.WorkflowRoutes) != 2 || len(selected) != 2 || in.WorkflowRouteScope != "api" ||
						len(in.Documents) != 1 || !strings.Contains(in.Documents[0].Text, statement) ||
						strings.Contains(string(q.Input), "removeFromShelf") {
						t.Fatalf("document evidence and compact route options: %+v", in)
					}
					result := docsOutput{Contracts: []record.Contract{{
						Statement: statement, AppliesTo: ".", EvidenceIDs: []string{in.Documents[0].EvidenceID},
					}}, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}}
					if kind != "omitted" {
						if kind == "invalid" {
							selected = []string{"R999", "../storage.go"}
						}
						b, err := json.Marshal([]workflowChoice{{ContractIndex: 1, RouteIDs: selected}})
						if err != nil {
							t.Fatal(err)
						}
						result.WorkflowSelections = b
					}
					return asJSON(result)
				},
				"investigate": func(_ int, q opencode.Request) opencode.Reply {
					in := decodeAs[investigateInput](t, q)
					out := map[string]any{"candidates": []any{}}
					if kind != "selected" {
						if len(in.Scope.WorkflowRoutes) != 0 {
							t.Fatalf("unexpected workflow references: %+v", in.Scope.WorkflowRoutes)
						}
						return asJSON(out)
					}
					if len(in.Scope.WorkflowRoutes) != 2 || len(in.Scope.Excerpts) < 3 ||
						in.Scope.Excerpts[0].Path != "api/service.go" || in.Scope.Excerpts[1].Path != "api/service.go" {
						t.Fatalf("workflow windows did not precede generic modules: %+v", in.Scope)
					}
					catalog := catalogOf(in.Scope.Excerpts)
					var evidence []string
					for _, r := range in.Scope.WorkflowRoutes {
						if !slices.Equal(r.ContractIndices, []int{1}) || catalog[r.Registration].Kind != "code" ||
							!strings.Contains(catalog[r.Registration].Text, r.Method+" "+r.Path) ||
							!strings.Contains(catalog[r.Handler].Text, "func ") {
							t.Fatalf("route lacks source attribution: %+v", r)
						}
						evidence = append(evidence, r.Handler)
					}
					out["contract_comparisons"] = []record.ContractComparison{{
						ContractIndex: 1, AppliesTo: ".", Status: "unknown", Reason: "상태 변경 뒤 목록에 반영되는지를 확인해야 한다.",
						DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: evidence,
					}}
					return asJSON(out)
				},
			}}
			_, results := runRole(t, j, vetStub{}, caller, "1.27.4")
			for _, name := range []string{record.StageDocuments, record.StageInvestigate} {
				if r := results["review."+name]; r.Status != domain.TaskSucceeded {
					t.Fatalf("%s status = %+v", name, r)
				}
			}
			doc := readRecord(t, j, record.StageDocuments)
			if len(doc.Contracts) != 1 || len(doc.Calls) != 1 || len(doc.Evidence) != 1 ||
				slices.Contains(doc.Limitations, record.LimWorkflowSelectionInvalid) != (kind == "invalid") {
				t.Fatalf("optional selections disturbed ordinary results: %+v", doc)
			}
			if kind != "selected" {
				if doc.Schema != record.SchemaVersion || len(doc.WorkflowSelections) != 0 {
					t.Fatalf("unexpected selections: %+v", doc)
				}
				return
			}
			if doc.Schema != record.SchemaVersionWorkflow || len(doc.WorkflowSelections) != 1 ||
				len(doc.WorkflowSelections[0].RouteDigests) != 2 {
				t.Fatalf("selection not recorded: %+v", doc)
			}
			inv := readRecord(t, j, record.StageInvestigate)
			if len(inv.ContractComparisons) != 1 || inv.ContractComparisons[0].Status != "unknown" {
				t.Fatalf("navigation changed the comparison: %+v", inv.ContractComparisons)
			}
			b, err := os.ReadFile(recordPath(j, record.StageDocuments))
			if err != nil {
				t.Fatal(err)
			}
			checkWorkflowRecordExample(t, b)
			s := &roleStage{ctx: t.Context(), j: j}
			links, missing, err := s.selectedWorkflowRoutes(doc)
			if err != nil || missing || len(links) != 2 {
				t.Fatalf("valid replay: %+v, missing=%v, err=%v", links, missing, err)
			}
			s.j.AttemptID = domain.NewID()
			if links, missing, err := s.selectedWorkflowRoutes(doc); err != nil || !missing || len(links) != 0 {
				t.Fatalf("replay outside accepted attempt: %+v, missing=%v, err=%v", links, missing, err)
			}
		})
	}
}

func checkWorkflowRecordExample(t *testing.T, compact []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	p := filepath.Join(exampleDir, "documents-workflow.json")
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	compareStoredExample(t, p, pretty.Bytes())
}

func TestWorkflowContractFollowupReadsSelectedImplementation(t *testing.T) {
	const impl = "api/storage.go"
	const statement = "찻잔을 내리면 목록에서 제외된다."
	tree := map[string]string{
		"README.md":      "# 보관함\n\n" + statement + "\n",
		"api/go.mod":     "module example.test/cups\n\ngo 1.27\n",
		"api/service.go": string(mustRead(t, filepath.Join(workflowFixture, "api/service.go"))),
		"app/go.mod":     "module example.test/client\n\ngo 1.27\n",
		"app/main.go":    "package main\n\nfunc main() { println(\"cups\") }\n",
		"api/service_test.go": `package api
import ("net/http/httptest"; "testing")
func TestLowerAndRead(t *testing.T) {
	srv := Routes()
	{
		req := httptest.NewRequest("DELETE", "/cups/blue", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 204 { t.Fatalf("delete status: %d", rec.Code) }
	}
	{
		req := httptest.NewRequest("GET", "/cups", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Body.String() != "[]\n" { t.Fatalf("unexpected response: %q", rec.Body.String()) }
	}
}
`,
	}
	text := string(mustRead(t, filepath.Join(workflowFixture, impl)))
	tree[impl] = strings.Replace(text, "func removeFromShelf(", strings.Repeat("// storage setup\n", 57)+"func removeFromShelf(", 1)
	j := treeJob(t, tree)
	caller := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
		"documents": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[docsInput](t, q)
			if len(in.WorkflowRoutes) != 2 {
				t.Fatalf("missing route choices: %+v", in.WorkflowRoutes)
			}
			return asJSON(docsOutput{Contracts: []record.Contract{
				{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{in.Documents[0].EvidenceID}},
			}, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
				WorkflowSelections: json.RawMessage(`[{"contract_index":1,"route_ids":["R1","R2"]}]`)})
		},
		"investigate": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[investigateInput](t, q)
			i := slices.IndexFunc(in.NavigationCandidates, func(x contractCandidateInput) bool {
				return x.Path == impl && x.Start > 50 && x.WorkflowRoute == "DELETE /cups/{cup}" &&
					x.Symbol == "removeFromShelf" && slices.Equal(x.ContractIndices, []int{1})
			})
			if i < 0 || len(in.Scope.WorkflowRoutes) != 2 {
				t.Fatalf("missing fresh implementation location: %+v", in)
			}
			route := in.Scope.WorkflowRoutes[0]
			test := catalogOf(in.Scope.Excerpts)[route.Test]
			if test.ID == "" || test.Kind != "test" || test.Path != "api/service_test.go" ||
				!strings.Contains(test.Text, "rec.Body.String()") {
				t.Fatalf("workflow request is missing the exact test assertion: %+v", in.Scope.WorkflowRoutes)
			}
			return asJSON(map[string]any{"candidates": []any{},
				"contract_comparisons": []record.ContractComparison{
					{ContractIndex: 1, AppliesTo: ".", Status: "unknown", Reason: "목록이 읽는 저장소의 변경을 확인해야 한다.",
						DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{route.Handler}},
				},
				"contract_probe": map[string]any{"contract_index": 1, "candidate_ids": []string{in.NavigationCandidates[i].ID}},
			})
		},
		"contract": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[contractReviewInput](t, q)
			if len(in.Scope.WorkflowRoutes) != 2 {
				t.Fatalf("missing follow-up route context: %+v", in.Scope.WorkflowRoutes)
			}
			catalog := catalogOf(in.Scope.Excerpts)
			var codes []string
			for _, route := range in.Scope.WorkflowRoutes {
				e := catalog[route.Decision]
				if e.Path != impl || e.Start <= 50 || !record.ScopeUnder(e.Path, in.Prior.AppliesTo) {
					t.Fatalf("decision window unavailable: route=%+v, window=%+v", route, e)
				}
				codes = append(codes, e.ID)
			}
			comp := in.Prior
			comp.Status, comp.Reason = "match", "삭제와 조회가 같은 찻잔 목록을 사용한다."
			comp.CodeEvidenceIDs = codes
			return asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{comp}})
		},
	}}
	_, results := runRole(t, j, vetStub{}, caller, "1.27.4")
	if r := results[record.TaskContract]; r.Status != domain.TaskSucceeded {
		t.Fatalf("contract followup = %+v", r)
	}
	doc, inv, final := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate), readRecord(t, j, record.StageContract)
	if inv.ContractComparisons[0].Status != "unknown" || final.ContractComparisons[0].Status != "match" ||
		len(final.Calls) != 1 || !record.ValidateContractFollowup(final, doc, inv) {
		t.Fatalf("followup comparison or lineage failed: %+v", final)
	}
}

func TestSupplementalWorkflowChoices(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		selections    int
		earlierCalls  int
	}{
		{name: "selected", outcome: "accepted", selections: 1},
		{name: "none", outcome: "accepted"},
		{name: "invalid", outcome: "output_rejected"},
		{name: "refusal", outcome: "output_rejected"},
		{name: "timeout", outcome: "model_timeout"},
		{name: "workspace unavailable", outcome: "workspace_unreachable"},
		{name: "primary selection", selections: 1},
		{name: "core budget reserved", earlierCalls: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const statement = "내린 찻잔은 이후 목록에 표시하지 않는다."
			service := string(mustRead(t, filepath.Join(workflowFixture, "api/service.go")))
			service = strings.Replace(service, "	return mux", `
	mux.HandleFunc("GET /conditions", list)
	mux.HandleFunc("POST /shelves", lower)
	mux.HandleFunc("GET /shelves", list)
	mux.HandleFunc("GET /materials", list)
	return mux`, 1)
			j := treeJob(t, map[string]string{
				"README.md": "# 보관함\n\n" + statement + "\n", "api/service.go": service,
				"api/go.mod":     "module example.test/cups\n\ngo 1.27\n",
				"api/storage.go": string(mustRead(t, filepath.Join(workflowFixture, "api/storage.go"))),
			})
			if tc.earlierCalls > 0 {
				if err := os.WriteFile(callsPath(j), []byte(strings.Repeat("review.environment\n", tc.earlierCalls)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			caller := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
				"documents": func(n int, q opencode.Request) opencode.Reply {
					if n == 1 {
						in := decodeAs[docsInput](t, q)
						if len(in.WorkflowRoutes) != 6 || len(in.Documents) != 1 {
							t.Fatalf("unexpected primary input %+v", in)
						}
						out := docsOutput{Contracts: []record.Contract{{
							Statement: statement, AppliesTo: ".", EvidenceIDs: []string{in.Documents[0].EvidenceID},
						}}, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}}
						if tc.name == "primary selection" {
							out.WorkflowSelections = json.RawMessage(`[{"contract_index":1,"route_ids":["R1"]}]`)
						}
						return asJSON(out)
					}
					in := decodeAs[workflowSelectionInput](t, q)
					if n != 2 || len(in.Contracts) != 1 || in.Contracts[0].Statement != statement || in.Contracts[0].Index != 1 ||
						len(in.Routes) != 6 || len(opencode.Prompt(q)) > PromptBudget || strings.Contains(string(q.Input), "removeFromShelf") {
						t.Fatalf("unexpected supplemental request %+v", in)
					}
					switch tc.name {
					case "selected":
						return asJSON(map[string]any{"workflow_selections": []workflowChoice{{ContractIndex: 1, RouteIDs: []string{"R1", "R2"}}}})
					case "none":
						return asJSON(map[string]any{"workflow_selections": []workflowChoice{}})
					case "invalid":
						return asJSON(map[string]any{"workflow_selections": []workflowChoice{{ContractIndex: 1, RouteIDs: []string{"R100"}}}})
					case "refusal":
						return opencode.Reply{Reason: "model_output_invalid"}
					case "timeout":
						return opencode.Reply{Reason: "model_timeout"}
					case "workspace unavailable":
						return opencode.Reply{Reason: "workspace_unreachable"}
					default:
						t.Fatalf("supplemental call took a reserved or unnecessary slot")
						return opencode.Reply{}
					}
				},
				"investigate": func(_ int, _ opencode.Request) opencode.Reply {
					return asJSON(map[string]any{"candidates": []any{}})
				},
			}}
			h := Handler{Static: vetStub{}, Caller: caller, MaxCalls: record.WholeRunMaxCalls}
			_, results := runRoleWith(t, j, h, "1.27.4")
			if r := results["review.documents"]; r.Status != domain.TaskSucceeded {
				t.Fatalf("supplemental decision displaced primary document result: %+v", r)
			}
			doc := readRecord(t, j, record.StageDocuments)
			if len(doc.Contracts) != 1 || doc.Contracts[0].Statement != statement ||
				len(doc.WorkflowSelections) != tc.selections || doc.Calls[0].Purpose != "" || doc.Calls[0].Outcome != record.OutcomeAccepted {
				t.Fatalf("primary and supplemental results: %+v", doc)
			}
			wantCalls := 1
			if tc.outcome != "" {
				wantCalls++
				if doc.Schema != record.SchemaVersionWorkflow || len(doc.Calls) != wantCalls {
					t.Fatalf("supplemental result lost from record: %+v", doc)
				}
				call := doc.Calls[1]
				if call.Purpose != record.WorkflowSelectionPurpose || call.Outcome != tc.outcome || call.Attempt != 2 || call.RequestDigest == "" {
					t.Fatalf("supplemental call attribution failed: %+v", call)
				}
			} else if len(doc.Calls) != wantCalls || (tc.selections == 0 && doc.Schema != record.SchemaVersion) {
				t.Fatalf("unnecessary call: %+v", doc)
			}
			if used, err := callsUsed(j); err != nil || used != tc.earlierCalls+wantCalls+caller.calls("investigate") {
				t.Fatalf("attempt ledger usage %d: %v", used, err)
			}
		})
	}
}
