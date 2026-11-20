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

func TestDecodePersistedDocumentScope(t *testing.T) {
	_, original, want := example(t, StageDocuments)
	cases := []struct {
		path, scope string
		valid       bool
	}{
		{"docs/design.md", "docs", true},
		{"docs/design.md", ".", true},
		{"docs/design.md", "svc/a", true},
		{"svc/b/README.md", "svc", false},
	}
	for _, c := range cases {
		t.Run(c.path+":"+c.scope, func(t *testing.T) {
			r := original
			sha, ok := want.Lookup(c.path)
			if !ok {
				t.Fatal("missing fixture path")
			}
			e := Excerpt{Path: c.path, SHA256: sha, Kind: "document", Start: 1, End: 1}
			e.ID = ExcerptID(r.Snapshot, e.Path, e.SHA256, e.Start, e.End)
			r.Evidence = []Excerpt{e}
			r.Contracts = []Contract{{Statement: "문서에 적힌 동작을 확인한다.", AppliesTo: c.scope, EvidenceIDs: []string{e.ID}}}
			r.Conflicts, r.Questions, r.InstructionNotes, r.DocumentQuality = nil, nil, nil, nil
			body, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(body, want)
			if (err == nil) != c.valid {
				t.Fatalf("decode error: %v", err)
			}
			if c.valid && got.Contracts[0].AppliesTo != c.scope {
				t.Fatalf("persisted scope changed to %q", got.Contracts[0].AppliesTo)
			}
		})
	}
}
