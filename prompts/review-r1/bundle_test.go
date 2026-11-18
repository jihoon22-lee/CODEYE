package reviewpackr1

import (
	"bytes"
	"os"
	"slices"
	"testing"
)

func TestCandidatePackSelection(t *testing.T) {
	id, version, err := Identity()
	if err != nil || id != "review-r1" || version != "2.0.0-rc1" {
		t.Fatal(id, version, err)
	}
	pack, err := os.ReadFile("pack.yaml")
	if err != nil || !bytes.Contains(pack, []byte("status: candidate-not-promoted")) {
		t.Fatal("pack.yaml must mark the candidate as not promoted")
	}
	digests := map[string]bool{}
	for _, role := range Roles {
		s, err := For(role)
		if err != nil {
			t.Fatal(role, err)
		}
		if len(s.Digest) != 64 || digests[s.Digest] || !slices.IsSorted(s.Files) || !slices.Contains(s.Files, roleFiles[role]) || !slices.Contains(s.Files, "pack.yaml") {
			t.Fatalf("%s: %+v", role, s)
		}
		digests[s.Digest] = true
		again, _ := For(role)
		if again.Digest != s.Digest || again.Text != s.Text {
			t.Fatal("selection is not deterministic")
		}
	}
	if _, err := For("context"); err == nil {
		t.Fatal("review-v1 role accepted by the R1 pack")
	}
	// The shared language and domain packs are copies of review-v1's.
	for _, name := range []string{"domains.md", "languages/go.md"} {
		a, _ := os.ReadFile(name)
		b, _ := os.ReadFile("../review-v1/" + name)
		if len(a) == 0 || !bytes.Equal(a, b) {
			t.Fatalf("%s differs from review-v1", name)
		}
	}
}
