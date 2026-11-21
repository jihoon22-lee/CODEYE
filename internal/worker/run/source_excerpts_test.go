package run

import (
	"fmt"
	"slices"
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
