package report

import (
	"fmt"
	"testing"
)

// The summary's priorities rank the validation class before severity: an
// unreviewed high tool finding comes after a supported medium and after a
// triage problem not yet confirmed. Reproduced and supported stay one class
// ordered by severity, as the stored summaries of earlier builders are
// (ValidateReport recomputes them).
func TestSummaryPrioritiesRankValidationBeforeSeverity(t *testing.T) {
	var fs []Finding
	for i := range 4 {
		fs = append(fs, Finding{ID: fmt.Sprintf("static_%d", i), Severity: "critical", Validation: ValidationUnreviewed, Disposition: "open"})
	}
	fs = append(fs,
		Finding{ID: "review_t002_0001", Severity: "medium", Validation: ValidationUnreviewed, Disposition: "open"},
		Finding{ID: "review_s001_0001", Severity: "medium", Validation: ValidationSupported, Disposition: "open"},
		Finding{ID: "review_s002_0001", Severity: "low", Validation: ValidationReproduced, Disposition: "open"},
		Finding{ID: "review_s003_0001", Severity: "critical", Validation: ValidationRefuted, Disposition: "open"})
	got := ComputeSummary(fs, nil).Priorities
	want := []string{"review_s001_0001", "review_s002_0001", "review_t002_0001", "static_0", "static_1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("priorities %v, want %v", got, want)
	}
}
