package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

func example(t *testing.T) (Record, Expect) {
	t.Helper()
	b, err := os.ReadFile("../../../../examples/review-record.json")
	if err != nil {
		t.Fatal(err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	// Build the lookup from the fixture independently; the record itself is untrusted.
	lookup := map[string]string{}
	for _, name := range []string{"go.mod", "load.go", "store.go", "store_test.go"} {
		f, err := os.ReadFile(filepath.Join("..", "testdata", "verify", name))
		if err != nil {
			t.Fatal(err)
		}
		lookup[name] = domain.Digest(f)
	}
	want := Expect{RunID: r.RunID, AttemptID: r.AttemptID, Snapshot: r.Snapshot, Stage: r.Stage, Lookup: func(p string) (string, bool) { sha, ok := lookup[p]; return sha, ok }}
	r, err = Decode(b, want)
	if err != nil {
		t.Fatal(err)
	}
	return r, want
}

func TestRecordRejectsUnboundAndInventedData(t *testing.T) {
	mutations := map[string]func(*Record){
		"other attempt":  func(r *Record) { r.AttemptID = domain.NewID() },
		"other run":      func(r *Record) { r.RunID = domain.NewID() },
		"other snapshot": func(r *Record) { r.Snapshot = domain.Digest(nil) },
		"other stage":    func(r *Record) { r.Stage = "investigate" },
		"missing path": func(r *Record) {
			e := &r.Evidence[0]
			e.Path = "other.go"
			e.ID = ExcerptID(r.Snapshot, e.Path, e.SHA256, e.Start, e.End)
		},
		"wrong blob": func(r *Record) {
			e := &r.Evidence[0]
			e.SHA256 = domain.Digest(nil)
			e.ID = ExcerptID(r.Snapshot, e.Path, e.SHA256, e.Start, e.End)
		},
		"bad range": func(r *Record) {
			e := &r.Evidence[0]
			e.End = e.Start - 1
			e.ID = ExcerptID(r.Snapshot, e.Path, e.SHA256, e.Start, e.End)
		},
		"duplicate":                      func(r *Record) { r.Evidence = append(r.Evidence, r.Evidence[0]) },
		"unsupported candidate":          func(r *Record) { r.Candidates[0].Evidence = []string{"ev_other"} },
		"empty context":                  func(r *Record) { r.Facts = []Fact{} },
		"candidate order":                func(r *Record) { r.Verifications[0].CandidateID = "candidate_2" },
		"missing verdict":                func(r *Record) { r.Verifications = nil },
		"refuted without counterexample": func(r *Record) { r.Verifications[0].Counterevidence = nil },
		"forged reproduction":            func(r *Record) { r.Verifications[0].Verdict = "reproduced" },
		"missing prior call":             func(r *Record) { r.Calls = r.Calls[1:] },
		"forged pack path":               func(r *Record) { r.Calls[0].PackFiles = []string{"../secret"} },
		"extra attempt":                  func(r *Record) { r.Calls[len(r.Calls)-1].Attempt = 3 },
		"failed with accepted verdict":   func(r *Record) { r.Outcome, r.FailureReason = "failed", "model_timeout" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r, want := example(t)
			mutate(&r)
			b, _ := json.Marshal(r)
			if _, err := Decode(b, want); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestLegacyRecordsAndNoRequestReceipt(t *testing.T) {
	for i, role := range []string{"context", "investigate"} {
		r, want := example(t)
		r.Stage, want.Stage = role, role
		r.Calls = r.Calls[:i+1]
		for i := range r.Calls {
			r.Calls[i].Attempt = 0
			r.Calls[i].Outcome = ""
		}
		r.Verifications = nil
		r.Outcome = ""
		if i == 0 {
			r.Candidates = []Candidate{}
		}
		var initial []Excerpt
		for _, e := range r.Evidence {
			if e.Origin == "context" {
				e.Origin = ""
				initial = append(initial, e)
			}
		}
		r.Evidence = initial
		b, err := MarshalRecord(r, want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b, want)
		if err != nil || !got.Accepted() {
			t.Fatalf("legacy %s: %v", role, err)
		}
		// Before the model client forms a request it has neither a request digest nor usage.
		r.Outcome, r.FailureReason = "failed", "workspace_unreachable"
		if role == "context" {
			r.Facts = []Fact{}
		} else {
			r.Candidates = []Candidate{}
		}
		call := &r.Calls[i]
		call.Attempt, call.Outcome, call.Version, call.RequestDigest, call.Usage = 1, r.FailureReason, "", "", nil
		b, err = MarshalRecord(r, want)
		if err != nil {
			t.Fatal(err)
		}
		got, err = Decode(b, want)
		if err != nil || got.Accepted() {
			t.Fatalf("failed preflight %s: %v", role, err)
		}
	}
}
