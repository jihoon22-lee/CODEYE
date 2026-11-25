package report

import (
	"encoding/json"
	"testing"
)

// Two diagnostics at one position with one message and rule but different
// tool grades are one tool finding with the higher default severity, in
// either order.
func TestToolFindingTakesTheHigherGradeOfItsDiagnostics(t *testing.T) {
	for _, grades := range [][2]string{{"style", "error"}, {"error", "style"}} {
		doc := readExample(t, "cppcheck")
		obs := doc["observations"].([]any)
		first := obs[0].(map[string]any)
		first["rule_id"], first["raw_severity"] = "unlistedCheck", grades[0]
		var second map[string]any
		b, _ := json.Marshal(first)
		if err := json.Unmarshal(b, &second); err != nil {
			t.Fatal(err)
		}
		second["id"], second["raw_severity"] = "obs_07764acd2d1602ace577", grades[1]
		doc["observations"] = append([]any{first, second}, obs[1:]...)
		if parse, ok := doc["parse"].(map[string]any); ok {
			parse["observations"] = parse["observations"].(float64) + 1
		}
		r := build(t, analyzerFixture(t, nil, taskSpec{key: "cppcheck", status: "succeeded", required: true, doc: doc}).in, DefaultRegistry())
		if err := ValidateReport(r); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range r.Findings {
			for _, id := range f.ObservationIDs {
				if id == first["id"] || id == second["id"] {
					got = append(got, f.ID+"="+f.Severity)
					break
				}
			}
		}
		if len(got) != 1 || got[0][len(got[0])-len("medium"):] != "medium" {
			t.Fatalf("grades %v: findings %v (limitations %v)", grades, got, r.Limitations)
		}
	}
}
