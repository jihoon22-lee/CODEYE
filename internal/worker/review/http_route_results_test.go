package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestGoHTTPDecisionWindowsReadResponseAndItsPrecedingStatements(t *testing.T) {
	const header = `w.Header().Set("X-Catalog", "daily")` + "\n"
	for _, tc := range []struct {
		name, prefix, result string
		preferred            bool
	}{
		{name: "serve content", result: `std.ServeContent(w, r, "daily.txt", time.Time{}, bytes.NewReader(data))`, preferred: true},
		{name: "serve file", result: `std.ServeFile(w, r, "daily.txt")`, preferred: true},
		{name: "redirect", result: `std.Redirect(w, r, "/archive", std.StatusFound)`, preferred: true},
		{name: "body", result: `w.Write(data)`, preferred: true},
		{name: "body with status", result: "w.WriteHeader(std.StatusOK)\nw.Write(data)", preferred: true},
		{name: "status only", result: `w.WriteHeader(std.StatusNoContent)`, preferred: true},
		{name: "direct JSON", result: `json.NewEncoder(w).Encode(data)`, preferred: true},
		{name: "bound JSON", result: "enc := json.NewEncoder(w)\nif err := enc.Encode(data); err != nil { return }", preferred: true},
		{name: "short helper", prefix: "short", result: `w.Write(data)`},
		{name: "response in prefix", prefix: `w.Write(data)`, result: ""},
		{name: "two writes", result: "w.Write(data)\nw.Write(other)"},
		{name: "error status only", result: `w.WriteHeader(std.StatusNotFound)`},
		{name: "different writer", result: `other.Write(data)`},
		{name: "wrong encoder target", result: `json.NewEncoder(&buffer).Encode(data)`},
		{name: "replaced encoder", result: "enc := json.NewEncoder(w)\nenc = json.NewEncoder(&buffer)\nenc.Encode(data)"},
		{name: "other HTTP package", result: `fake.ServeContent(w, r, "daily.txt", time.Time{}, bytes.NewReader(data))`},
		{name: "response does not fit", result: fmt.Sprintf("w.Write([]byte(%q))", strings.Repeat("a", roleMaxWindowBytes))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := map[string]string{"service.go": `package archive
import (
	"bytes"
	"encoding/json"
	fake "example.test/fake"
	std "net/http"
	"time"
)
func routes() { std.HandleFunc("GET /daily", open) }
func open(w std.ResponseWriter, r *std.Request) { deliver(w, r) }
func deliver(w std.ResponseWriter, r *std.Request) {
`}
			early := tc.prefix
			if early == "short" {
				early = header
			} else {
				early = strings.TrimSpace(early+"\n") + "\n" + strings.Repeat(header, 22)
			}
			tree["service.go"] += early + `data, err := readArchive()
if err != nil {
	std.Error(w, "archive unavailable", std.StatusInternalServerError)
	return
}
` + tc.result + "\n}\n"
			s := &roleStage{ctx: t.Context(), j: treeJob(t, tree)}
			routes, err := s.workflowRoutes()
			if err != nil || len(routes) != 1 || routes[0].DecisionSymbol != "deliver" {
				t.Fatalf("route and helper = %+v, error = %v", routes, err)
			}
			decision := routes[0].Decision
			includesContext := strings.Contains(decision.Text, "data, err := readArchive()") &&
				strings.Contains(decision.Text, "if err != nil") && strings.Contains(decision.Text, tc.result)
			if tc.preferred && (!includesContext || strings.Contains(decision.Text, "func deliver(")) {
				t.Fatalf("result and its preceding statements missing: %+v", decision)
			}
			if !tc.preferred && !strings.Contains(decision.Text, "func deliver(") {
				t.Fatalf("insufficient or ambiguous response replaced useful prefix: %+v", decision)
			}
			if tc.prefix == "short" && (!includesContext || !strings.HasSuffix(decision.Text, "}")) {
				t.Fatalf("short helper was not kept in full: %+v", decision)
			}
			if decision.End-decision.Start+1 > record.MaxContractLocationLines || record.JSONLen(decision.Text) > roleMaxWindowBytes {
				t.Fatalf("decision window exceeds its limits: %+v", decision)
			}
			evidence, err := s.withText([]record.Excerpt{decision})
			if err != nil || !slices.Equal(evidence, []record.Excerpt{decision}) {
				t.Fatalf("window cannot be reloaded from the snapshot: %+v, %v", evidence, err)
			}
			doc := record.Record{Contracts: []record.Contract{{AppliesTo: "."}}}
			candidates := s.workflowNavigation(doc, []workflowLink{{route: routes[0], indices: []int{1}}}, nil)
			if len(candidates) == 0 || candidates[0].input(0).Symbol != "deliver" {
				t.Fatalf("helper name missing from the navigation choice: %+v", candidates)
			}
			location := candidates[0].location
			if tc.preferred && location.Symbol != "" {
				t.Fatalf("symbol not present in the result window was persisted: %+v", location)
			}
			probe := record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{location}}
			comparison := record.ContractComparison{ContractIndex: 1, AppliesTo: ".", Status: "unknown"}
			evidence, err = s.contractProbeExcerpts(probe, comparison)
			if err != nil || !slices.Equal(evidence, []record.Excerpt{decision}) {
				t.Fatalf("offered location cannot be replayed as a probe: %+v, %v", evidence, err)
			}
		})
	}
}
