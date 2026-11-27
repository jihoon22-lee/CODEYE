package record

import (
	"strings"
	"testing"
)

// The new lines of a follow-up are the union, per file blob, of the lines
// its new excerpts hold that no excerpt of the investigation does: two
// overlapping windows count their shared lines once.
func TestLeadNewLinesCountTheUnion(t *testing.T) {
	sha, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
	excerpt := func(path, blob string, start, end int) Excerpt {
		return Excerpt{ID: ExcerptID(strings.Repeat("c", 64), path, blob, start, end), Path: path, SHA256: blob, Start: start, End: end, Kind: "code", Origin: "context"}
	}
	a, b := excerpt("p/f.go", sha, 10, 20), excerpt("p/f.go", sha, 15, 25)
	// The same path at another blob is another file.
	c := excerpt("p/f.go", other, 15, 16)
	catalog := catalogOf([]Excerpt{a, b, c})
	investigation := []Excerpt{excerpt("p/f.go", sha, 10, 12)}
	set := LeadNewLineSet([]string{a.ID, b.ID, c.ID}, catalog, investigation)
	// 10..25 less 10..12, plus two lines of the other blob.
	if n := LineCount(set); n != 13+2 {
		t.Fatalf("new lines %d: %v", n, set)
	}
	if n := LeadLines([]string{a.ID, b.ID}, catalog); n != 16 {
		t.Fatalf("distinct lines %d", n)
	}
}
