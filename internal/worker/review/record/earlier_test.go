package record

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Records of the earlier schemas stay readable: the review examples a
// worker wrote before schema 0.7 (testdata/earlier, schema 0.2 roles and
// 0.3 slices) decode against their fixtures and keep their bytes.
func TestEarlierSchemaRecordsStayReadable(t *testing.T) {
	lookup := func(root string) func(string) (string, bool) {
		digests := map[string]string{}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			body, err := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			digests[filepath.ToSlash(rel)] = domain.Digest(body)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return func(p string) (string, bool) { s, ok := digests[p]; return s, ok }
	}
	whole, sliced := lookup("../../../../fixtures/modules/input"), lookup("../../../../fixtures/modules/slices/input")
	for _, c := range []struct {
		file, stage, slice, schema string
		lookup                     func(string) (string, bool)
	}{
		{"environment.json", StageEnvironment, "", SchemaVersion, whole},
		{"documents.json", StageDocuments, "", SchemaVersion, whole},
		{"investigate.json", StageInvestigate, "", SchemaVersion, whole},
		{"interpret.json", StageInterpret, "", SchemaVersion, whole},
		{"verify.json", StageVerify, "", SchemaVersion, whole},
		{"investigate.s001.json", StageInvestigate, "s001", SchemaVersionSlice, sliced},
		{"verify.s001.json", StageVerify, "s001", SchemaVersionSlice, sliced},
	} {
		t.Run(c.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", "earlier", c.file))
			if err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, b); err != nil {
				t.Fatal(err)
			}
			var head Record
			if err := json.Unmarshal(b, &head); err != nil {
				t.Fatal(err)
			}
			want := Expect{Snapshot: head.Snapshot, RunID: head.RunID, AttemptID: head.AttemptID, Stage: c.stage, Slice: c.slice, Lookup: c.lookup}
			r, err := Decode(compact.Bytes(), want)
			if err != nil || r.Schema != c.schema {
				t.Fatalf("earlier record unreadable: %v (schema %s)", err, r.Schema)
			}
			again, err := Marshal(r, want)
			if err != nil || !bytes.Equal(again, compact.Bytes()) {
				t.Fatalf("earlier record bytes changed: %v", err)
			}
			// The fields of 0.7 are not valid in an earlier record: a
			// narrowed citation, a mechanism, a fifth candidate or prose
			// beyond 400 bytes.
			if _, err := Decode(mustMarshalUnchecked(t, r), want); err != nil {
				t.Fatalf("unchanged earlier record rejected: %v", err)
			}
			if len(r.Evidence) > 0 {
				x := r
				x.Evidence = append([]Excerpt(nil), r.Evidence...)
				x.Evidence[0].Origin = OriginCited
				if _, err := Decode(mustMarshalUnchecked(t, x), want); err == nil {
					t.Fatal("earlier record with a cited origin accepted")
				}
			}
			if len(r.Candidates) > 0 {
				for name, edit := range map[string]func(c *Candidate){
					"mechanism": func(c *Candidate) { c.Mechanism = "the cited lines return before the lock is released" },
					"prose":     func(c *Candidate) { c.Impact = string(bytes.Repeat([]byte("a"), 401)) },
					"id":        func(c *Candidate) { c.ID = c.ID[:len(c.ID)-1] + "5" },
				} {
					x := r
					x.Candidates = append([]Candidate(nil), r.Candidates...)
					edit(&x.Candidates[0])
					if _, err := Decode(mustMarshalUnchecked(t, x), want); err == nil {
						t.Fatalf("earlier record with a 0.7 %s accepted", name)
					}
				}
			}
		})
	}
}

// mustMarshalUnchecked encodes r without the version checks of Marshal.
func mustMarshalUnchecked(t *testing.T, r Record) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
