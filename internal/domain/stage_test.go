package domain

import "testing"

func TestStageValid(t *testing.T) {
	for _, s := range TaskStages {
		if !s.Valid() {
			t.Fatalf("%q must be valid", s)
		}
	}
	if len(TaskStages) != 6 {
		t.Fatalf("task stages = %d", len(TaskStages))
	}
	for _, s := range []Stage{"", "source", "report", "Environment", "code-review"} {
		if s.Valid() {
			t.Fatalf("%q must be invalid", s)
		}
	}
}
