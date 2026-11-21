package report

import "testing"

func TestReadingGroupsOnlyTheSameRecordedIssue(t *testing.T) {
	location := Location{Path: "fixtures/sample.go", StartLine: 4, EndLine: 8}
	refs := []Evidence{{ID: "evidence", Kind: "code", Location: &location}}
	base := Finding{ID: "observed", Category: "correctness", Validation: ValidationSupported, Disposition: "open",
		Title: "fmt.Printf 서식과 인자 형식 불일치", ObservationIDs: []string{"observation"}, Evidence: refs, Locations: []Location{location}}
	linked, sameEvidence, sharedLocation, differentIssue := base, base, base, base
	linked.ID = "same-observation"
	sameEvidence.ID, sameEvidence.Title, sameEvidence.ObservationIDs = "same-evidence", "Printf 서식 지정자와 인자 형식 불일치", nil
	sharedLocation = sameEvidence
	sharedLocation.ID = "different-evidence"
	sharedLocation.Evidence = []Evidence{{ID: "different", Kind: "code", Location: &location}}
	differentIssue = sameEvidence
	differentIssue.ID, differentIssue.Title = "different-issue", "Printf 호출이 동시 쓰기에서 순서를 보장하지 않는다"
	duplicates := duplicateReadingFindings([]Finding{base, linked, sameEvidence, sharedLocation, differentIssue}, []Observation{{ID: "observation", RuleID: "PRINTF"}})
	if len(duplicates) != 2 || !duplicates[linked.ID] || !duplicates[sameEvidence.ID] {
		t.Fatalf("expected the two matched records only: %v", duplicates)
	}
}
