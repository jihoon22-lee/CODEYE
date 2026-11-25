package record

import "testing"

// TestTriageCrossToolMatchesItsTools: a triage group is cross_tool exactly
// when its observations come from two or more tools, the rule the bundle
// check applies too.
func TestTriageCrossToolMatchesItsTools(t *testing.T) {
	catalog := map[string]Excerpt{"e1": {ID: "e1", Kind: "code"}}
	observations := map[string]ObservationRef{"o1": {ID: "o1", Tool: "go.staticcheck"}, "o2": {ID: "o2", Tool: "go.staticcheck"}, "o3": {ID: "o3", Tool: "go.vet"}}
	for _, c := range []struct {
		name  string
		ids   []string
		cross bool
		ok    bool
	}{
		{"one tool", []string{"o1", "o2"}, false, true},
		{"one tool marked cross_tool", []string{"o1", "o2"}, true, false},
		{"two tools", []string{"o1", "o3"}, true, true},
		{"two tools not marked", []string{"o1", "o3"}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := Record{Stage: StageTriage, Outcome: OutcomeFailed, TriageGroups: []TriageGroup{{ID: "g1", ObservationIDs: c.ids, ContextIDs: []string{"e1"},
				CrossTool: c.cross, Omitted: LimTriageGroupsNotTriaged}}}
			for _, id := range c.ids {
				r.Observations = append(r.Observations, observations[id])
			}
			if err := checkTriage(r, catalog, observations, bounds{}); (err == nil) != c.ok {
				t.Fatalf("checkTriage: %v, want ok=%v", err, c.ok)
			}
		})
	}
}

// TestTriageRecordWithoutGroups: a failed triage record with no group (its
// request could not be built) is storable; an accepted one is not.
func TestTriageRecordWithoutGroups(t *testing.T) {
	for _, outcome := range []string{OutcomeFailed, OutcomeAccepted} {
		err := checkStageContent(Record{Stage: StageTriage, Outcome: outcome}, nil, nil, bounds{})
		if (err == nil) != (outcome == OutcomeFailed) {
			t.Fatalf("%s triage record without groups: %v", outcome, err)
		}
	}
}
