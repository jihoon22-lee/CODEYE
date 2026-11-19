package record

import (
	"encoding/json"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// A verify record is bound to its attempt, snapshot and stage, and every
// citation and verdict is checked again when it is decoded.
func TestRecordRejectsUnboundAndInventedData(t *testing.T) {
	mutations := map[string]func(*Record){
		"other attempt":  func(r *Record) { r.AttemptID = domain.NewID() },
		"other run":      func(r *Record) { r.RunID = domain.NewID() },
		"other snapshot": func(r *Record) { r.Snapshot = domain.Digest(nil) },
		"other stage":    func(r *Record) { r.Stage = StageInvestigate },
		"wrong blob": func(r *Record) {
			r.Evidence = append([]Excerpt{}, r.Evidence...)
			r.Evidence[0].SHA256 = domain.Digest([]byte("other"))
		},
		"bad range": func(r *Record) {
			r.Evidence = append([]Excerpt{}, r.Evidence...)
			r.Evidence[0].End = r.Evidence[0].Start - 1
		},
		"duplicate evidence": func(r *Record) { r.Evidence = append(append([]Excerpt{}, r.Evidence...), r.Evidence[0]) },
		"candidate order": func(r *Record) {
			r.Verifications = append([]Verification{}, r.Verifications...)
			r.Verifications[0].CandidateID = "investigate_4"
		},
		"missing verdict": func(r *Record) { r.Verifications = nil },
		"forged reproduction": func(r *Record) {
			r.Verifications = append([]Verification{}, r.Verifications...)
			r.Verifications[0].Verdict = "reproduced"
		},
		"forged pack path": func(r *Record) {
			r.Calls = append([]Call{}, r.Calls...)
			r.Calls[0].PackFiles = []string{"../secret"}
		},
		"failed with accepted verdict": func(r *Record) { r.Outcome, r.FailureReason = OutcomeFailed, "model_timeout" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			_, r, want := example(t, StageVerify)
			mutate(&r)
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(b, want); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
