package review

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// TC-161 (docs/REVIEW.md#검토-우선순위): use the runtime scheduler on
// generated trees with tool diagnostics and generated files.
func TestSlotSlackCoversRunTimeOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	worst, tried := 0, 0
	for i := range 3000 {
		files := randomTree(r)
		for f := range r.IntN(30) {
			files[fmt.Sprintf("m0/fixtures/c%d/f%02d.go", f%3, f)] = int64(r.IntN(3000))
		}
		plan := planSlices(syntheticManifest(files), domain.Scope{Kind: "full"}, record.DefaultSecondary)
		if plan.Slots == 0 || plan.Slots >= MaxSlices {
			continue
		}
		tried++
		rest := append([]plannedFile{}, plan.Rest...)
		failed := map[string]bool{}
		for _, f := range rest {
			if r.IntN(8) == 0 {
				failed[f.Module] = true
			}
		}
		for k := range rest {
			f := &rest[k]
			switch n := r.IntN(10); {
			case n < 3:
				f.Rank, f.Observations, f.Severity = record.RankToolObservations, 1+r.IntN(20), r.IntN(4)
			case failed[f.Module]:
				f.Rank = record.RankAnalyzerNotSucceeded
			}
			if f.Class == record.ClassPrimary && r.IntN(15) == 0 {
				f.Class, f.Secondary = record.ClassSecondary, record.ReasonGeneratedFile
			}
		}
		cursors := make([]*coverageCursor, 0, len(rest))
		for _, f := range rest {
			sf := record.ScopeFile{Path: f.Path, Module: f.Module, Class: f.Class, Rank: f.Rank,
				StartLine: 1, PlannedLines: 1, PlannedBytes: f.Bound, Observations: f.Observations}
			cursors = append(cursors, &coverageCursor{file: sf, tier: f.Tier, severity: f.Severity, windows: []record.ScopeFile{sf}})
		}
		doc := record.ScopeDoc{Limits: record.ScopeLimits{Slots: coverageSlots(plan)}}
		scheduleCoverageWithin(&doc, cursors, nil, nil)
		worst = max(worst, len(doc.Slices)-len(plan.Slices))
		for _, cur := range cursors {
			if len(cur.windows) != 0 && len(doc.Slices) != doc.Limits.Slots {
				t.Fatalf("tree %d: %s left behind in %d of %d slots", i, cur.file.Path, len(doc.Slices), doc.Limits.Slots)
			}
		}
	}
	if tried < 1000 {
		t.Fatalf("only %d trees with slots", tried)
	}
	t.Logf("%d trees: run-time packing needs at most %d slots more than plan time", tried, worst)
}
