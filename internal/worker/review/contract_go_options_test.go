package review

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	optionDoc  = "The catalogue omits labels when include_labels is disabled."
	optionName = "include_labels"
	optionApp  = "gateway/selection.go"
	optionImpl = "catalogue/selection.go"
	optionType = "catalogue/frame.go"
)

func goOptionTree(value string) map[string]string {
	return map[string]string{
		"go.mod":    "module example.org/gallery\n\ngo 1.23\n",
		"README.md": "# Catalogue\n\n" + optionDoc + "\n",
		optionApp: "package gateway\nimport (\n\"net/http\"\ncore \"example.org/gallery/catalogue\"\n)\n" +
			"type request struct { client *http.Request }\n" +
			strings.Repeat("// other handler context\n", 62) +
			"func pick(q request, frame core.Frame) core.Frame {\n" +
			"values := q.client.URL.Query()\n" +
			"include := false\nswitch values.Get(\"include_labels\") {\ncase \"yes\":\ninclude = true\n" +
			"case \"\", \"no\":\ndefault:\nreturn core.Frame{}\n}\n" +
			strings.Repeat("// skip over unrelated steps\n", 24) +
			"return core.Outline(frame, include)\n}\n",
		optionImpl: "package catalogue\n" + strings.Repeat("// archived catalogue logic\n", 65) +
			"func Outline(frame Frame, labels bool) Frame {\n" +
			"if labels {\nreturn frame\n}\nreduced := frame\n" +
			"reduced.Display = Display{ShowLabels: " + value + "}\n" +
			strings.Repeat("reduced.Count++\n", 30) +
			"return reduced\n}\n",
		optionType: "package catalogue\n" +
			"type Frame struct { Count int; Display Display }\n" +
			"type Display struct {\nShowLabels bool \x60json:\"include_labels\"\x60\n}\n",
		"decoy/catalogue/selection.go": "package catalogue\nfunc Outline(frame int, labels bool) int { if labels { return 4 }; return 5 }\n",
	}
}

func TestDocumentedGoQueryOptionReachesDecisiveProjection(t *testing.T) {
	for _, test := range []struct {
		name, value, verdict string
		workflow             bool
	}{
		{name: "match", value: "false", verdict: "match"},
		{name: "mismatch", value: "true", verdict: "mismatch"},
		{name: "selected HTTP workflow", value: "false", verdict: "match", workflow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := goOptionTree(test.value)
			statement := optionDoc
			if test.workflow {
				statement += " This option belongs to GET /catalogue."
				tree["README.md"] = "# Catalogue\n\n" + statement + "\n"
				tree["gateway/routes.go"] = "package gateway\nimport \"net/http\"\n" +
					"func routes() *http.ServeMux {\nmux := http.NewServeMux()\n" +
					"mux.HandleFunc(\"GET /catalogue\", openCatalogue)\nreturn mux\n}\n"
				tree[optionApp] += "func openCatalogue(w http.ResponseWriter, r *http.Request) {\n" +
					"_ = pick(request{client:r}, core.Frame{})\nw.WriteHeader(http.StatusOK)\n}\n"
			}
			for i := 0; i < 10; i++ {
				tree[fmt.Sprintf("catalogue/entry%02d.go", i)] = "package catalogue\n" +
					fmt.Sprintf("func Count%02d() int { return %d }\n", i, i)
			}
			j := treeJob(t, tree)
			script := coverageScript(t)
			script["documents"] = func(_ int, request opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, request)
				out := docsOutput{Contracts: []record.Contract{{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{docID(in, "README.md")}}},
					Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}}
				if test.workflow {
					at := slices.IndexFunc(in.WorkflowRoutes, func(r workflowRouteInput) bool { return r.Method == "GET" && r.Path == "/catalogue" })
					if at < 0 {
						t.Fatalf("workflow route not offered: %+v", in.WorkflowRoutes)
					}
					out.WorkflowSelections, _ = json.Marshal([]workflowChoice{{ContractIndex: 1, RouteIDs: []string{in.WorkflowRoutes[at].ID}}})
				}
				return asJSON(out)
			}
			script["investigate"] = func(_ int, request opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, request)
				if !in.Scope.WholeRun {
					return asJSON(map[string]any{"candidates": []any{}})
				}
				if len(in.Scope.DirectCalls) != 1 || len(in.Scope.Excerpts) > roleMaxCodeFiles || len(opencode.Prompt(request)) > MinRequestBytes {
					t.Fatalf("incorrect overview bounds or binding: links %+v, %d evidence entries", in.Scope.DirectCalls, len(in.Scope.Excerpts))
				}
				if test.workflow && len(in.Scope.WorkflowRoutes) != 1 {
					t.Fatalf("workflow registration or handler window was displaced: %+v", in.Scope)
				}
				catalog := catalogOf(in.Scope.Excerpts)
				link := in.Scope.DirectCalls[0]
				caller, decision := catalog[link.CallerEvidenceID], catalog[link.CalleeEvidenceID]
				if caller.Path != optionApp || decision.Path != optionImpl || link.Symbol != "Outline" ||
					!strings.Contains(caller.Text, "core.Outline(frame, include)") ||
					!strings.Contains(decision.Text, "Display{ShowLabels: "+test.value+"}") {
					t.Fatalf("implementation was not linked to the bound caller: %+v, %+v", caller, decision)
				}
				for _, want := range []struct{ path, marker string }{
					{optionApp, "switch values.Get(\"include_labels\")"},
					{optionImpl, "return reduced"},
					{optionType, "json:\"include_labels\""},
				} {
					if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == want.path && strings.Contains(e.Text, want.marker) }) {
						t.Fatalf("missing checked evidence %q in %s", want.marker, want.path)
					}
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
					ContractIndex: 1, AppliesTo: ".", Status: test.verdict, Reason: "선택값을 받는 코드의 조건과 반환을 확인했다.",
					DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{caller.ID, decision.ID},
				}}})
			}
			call := &scriptedCaller{script: script}
			_, results := runRoleWith(t, j, Handler{Budget: smallBudget, Static: secretsStub{}, Caller: call}, "1.27.4")
			rec := readRecord(t, j, record.StageInvestigate)
			if results["review.investigate"].Status != domain.TaskSucceeded || len(rec.ContractComparisons) != 1 ||
				rec.ContractComparisons[0].Status != test.verdict || call.calls(record.StageContract) != 0 {
				t.Fatalf("the normal comparison contract was not preserved: %+v", rec.ContractComparisons)
			}
		})
	}
}

func TestDocumentedGoQueryOptionRequiresUnambiguousScopedBindings(t *testing.T) {
	for _, name := range []string{"metadata excluded", "different module", "shadowed option", "shadowed package", "shadowed query", "reassigned option", "ambiguous callee", "withheld callee", "callee outside scope", "unanchored documentation", "reassigned projection"} {
		t.Run(name, func(t *testing.T) {
			tree := goOptionTree("false")
			scope := domain.Scope{Kind: "full"}
			switch name {
			case "metadata excluded":
				scope = domain.Scope{Kind: "paths", Paths: []string{"README.md", "catalogue", "gateway"}}
			case "different module":
				tree["catalogue/go.mod"] = "module example.org/unrelated\n\ngo 1.23\n"
			case "shadowed option":
				tree[optionApp] = strings.Replace(tree[optionApp], "return core.Outline(frame, include)", "include := false\nreturn core.Outline(frame, include)", 1)
			case "shadowed package":
				tree[optionApp] = strings.Replace(tree[optionApp], "return core.Outline(frame, include)", "core := struct{ Outline func(core.Frame, bool) core.Frame }{}\nreturn core.Outline(frame, include)", 1)
			case "shadowed query":
				tree[optionApp] = strings.Replace(tree[optionApp], "values := q.client.URL.Query()", "values := struct{ Get func(string) string }{}", 1)
			case "reassigned option":
				tree[optionApp] = strings.Replace(tree[optionApp], "return core.Outline(frame, include)", "include = !include\nreturn core.Outline(frame, include)", 1)
			case "ambiguous callee":
				tree["catalogue/alternate.go"] = "//go:build custom\n\n" + tree[optionImpl]
			case "callee outside scope":
				scope = domain.Scope{Kind: "paths", Paths: []string{"README.md", "go.mod", "gateway", "decoy"}}
			case "unanchored documentation":
				tree["README.md"] = strings.Replace(tree["README.md"], optionName, "notice_flag", 1)
			case "reassigned projection":
				tree[optionImpl] = strings.Replace(tree[optionImpl], "return reduced", "reduced.Display.ShowLabels = true\nreturn reduced", 1)
			}
			j := treeJob(t, tree)
			j.Scope = scope
			s := roleStage{j: j, ctx: t.Context()}
			if name == "withheld callee" {
				s.screen.withheld = map[string]bool{optionImpl: true}
			}
			e := s.entries()["README.md"]
			x, _, ok, err := s.excerpt(e, "document", "", 1, roleMaxDocLines, roleMaxDocBytes)
			if err != nil || !ok {
				t.Fatalf("document: %v", err)
			}
			result, err := s.contractWitnesses(planCodeGroups(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary), record.Record{
				Contracts: []record.Contract{{Statement: optionDoc, AppliesTo: ".", EvidenceIDs: []string{x.ID}}}, Evidence: []record.Excerpt{x},
			})
			if err != nil || len(result.goOption.windows) != 0 {
				t.Fatalf("unproven option trace: %+v (%v)", result.goOption, err)
			}
		})
	}
}

func TestDocumentedGoQueryOptionRequiresProjectedDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, mutation, written string
		linked                  bool
	}{
		{name: "original response", written: "payload", linked: true},
		{name: "replaced response", mutation: "payload = []byte(\"other content\")\n", written: "payload"},
		{name: "discarded response", mutation: "replacement := []byte(\"other content\")\n", written: "replacement"},
		{name: "aliased response", mutation: "same := payload[:]\nsame[0] = '!'\n", written: "payload"},
		{name: "changed projection", mutation: "view.Count++\n", written: "payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := goOptionTree("false")
			source := strings.Replace(tree[optionApp], "import (\n", "import (\n\"encoding/json\"\n", 1)
			source = strings.Replace(source, "pick(q request, frame core.Frame) core.Frame", "pick(q request, frame core.Frame, w http.ResponseWriter)", 1)
			source = strings.Replace(source, "return core.Frame{}", "return", 1)
			// Leave unrelated work between serialization and the terminal
			// write so a window around the call cannot cover both.
			terminal := "view := core.Outline(frame, include)\npayload := []byte(\"stored content\")\n" +
				"if !include {\npayload, _ = json.Marshal(view)\n}\n" +
				strings.Repeat("// independent work\n", 28) + tc.mutation + "w.Write(" + tc.written + ")"
			tree[optionApp] = strings.Replace(source, "return core.Outline(frame, include)", terminal, 1)
			j := treeJob(t, tree)
			stage := roleStage{j: j, ctx: t.Context()}
			e := stage.entries()["README.md"]
			d, _, ok, err := stage.excerpt(e, "document", "", 1, roleMaxDocLines, roleMaxDocBytes)
			if err != nil || !ok {
				t.Fatalf("document: %v", err)
			}
			out, links, _, err := stage.codeCorpus(record.Record{Evidence: []record.Excerpt{d}, Contracts: []record.Contract{{
				Statement: optionDoc, AppliesTo: ".", EvidenceIDs: []string{d.ID},
			}}})
			if err != nil || (len(links.directCalls) == 1) != tc.linked {
				t.Fatalf("delivery link differs: %+v, %v", links.directCalls, err)
			}
			if tc.linked {
				for _, text := range []string{"json.Marshal(view)", "w.Write(payload)"} {
					if !slices.ContainsFunc(out, func(x record.Excerpt) bool { return x.Path == optionApp && strings.Contains(x.Text, text) }) {
						t.Fatalf("verified delivery omitted %q", text)
					}
				}
			}
		})
	}
}
