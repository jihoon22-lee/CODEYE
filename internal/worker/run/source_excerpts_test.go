package run

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestMetricAnchorsSampleLargestVerifiedPairs(t *testing.T) {
	primary := &obsdoc.Location{Path: "src/primary.go", BlobSHA256: "primary", StartLine: 25}
	counterpart := &obsdoc.Location{Path: "src/counterpart.go", BlobSHA256: "counterpart", StartLine: 42}
	observations := []obsdoc.Observation{{
		Location: primary, Metric: &obsdoc.Metric{Name: "duplicated_lines", Value: 70},
		Related: []obsdoc.Related{{Location: counterpart}},
	}}
	for i := range 10 {
		observations = append(observations, obsdoc.Observation{
			Location: &obsdoc.Location{Path: fmt.Sprintf("src/clone%d.go", i), StartLine: i + 1},
			Metric:   &obsdoc.Metric{Name: "duplicated_lines", Value: i + 15},
			Related:  []obsdoc.Related{{Location: primary}},
		})
	}
	keys := metricAnchors(observations)
	if len(keys) != 8 {
		t.Fatalf("want a bounded sample of eight distinct anchors, got %v", keys)
	}
	want := []sourceexcerpt.Key{
		{Path: primary.Path, SHA256: primary.BlobSHA256, Line: primary.StartLine},
		{Path: counterpart.Path, SHA256: counterpart.BlobSHA256, Line: counterpart.StartLine},
		{Path: "src/clone9.go", Line: 10},
	}
	if !slices.Equal(keys[:len(want)], want) {
		t.Fatalf("first anchors %v, want %v", keys[:len(want)], want)
	}
}

func TestSourceExcerptBudgetKeepsOtherEvidenceAfterOversizedAddition(t *testing.T) {
	doc := sourceexcerpt.Document{SchemaVersion: sourceexcerpt.Version, RunID: "run", AttemptID: "attempt", Snapshot: "snapshot", Items: []sourceexcerpt.Item{}}
	sha := strings.Repeat("a", 64)
	budget := newSourceExcerptBudget(doc)
	for i := range sourceexcerpt.MaxItems {
		item := sourceexcerpt.Item{Kind: sourceexcerpt.DocumentKind, Path: strings.Repeat("deepname/", 270) + fmt.Sprintf("guide-%d.md", i),
			SHA256: sha, Anchor: 1, Excerpt: sourceexcerpt.Excerpt{StartLine: 1, EndLine: 1, Text: strings.Repeat("x", 1900)}}
		if !budget.admit(item, len(doc.Items) > 0) {
			break
		}
		doc.Items = append(doc.Items, item)
	}
	if len(doc.Items) < 2 || len(doc.Items) >= sourceexcerpt.MaxItems-1 {
		t.Fatalf("fixture did not reach the byte budget: %d items", len(doc.Items))
	}
	tail := sourceexcerpt.Item{Path: "short.go", SHA256: sha, Anchor: 1,
		Excerpt: sourceexcerpt.Excerpt{StartLine: 1, EndLine: 1, Text: "package short"}}
	if !budget.admit(tail, true) {
		t.Fatal("a rejected item consumed space needed for another source window")
	}
	doc.SchemaVersion = sourceexcerpt.DocumentVersion
	doc.Items = append(doc.Items, tail)
	body, err := sourceexcerpt.Encode(doc)
	if err != nil || len(body) > sourceexcerpt.MaxBytes {
		t.Fatalf("encoded evidence bundle exceeded byte cap: %d, %v", len(body), err)
	}
	got, ok := sourceexcerpt.Decode(body, doc.RunID, doc.AttemptID, doc.Snapshot, func(string) (string, bool) { return sha, true })
	if !ok || len(got.Items) != len(doc.Items) || got.Items[len(got.Items)-1] != tail {
		t.Fatalf("kept source evidence missing after decode: %t, %d items", ok, len(got.Items))
	}
}
