package review

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// TC-161 (docs/REVIEW.md#계획): the slots the plan makes hold every file
// review.scope packs at run time below the slot limit. Tool diagnostics,
// failed analyzers and generated headers reorder the files (and move some
// to the secondary class), which can need a few more slots than the
// plan-time packing; slotSlack covers that, so no file is left out while
// the limit is not reached.
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
		packed := fillSlots(rest, plan.Slots)
		checkSlots(t, packed)
		worst = max(worst, len(packed)-len(plan.Slices))
		if len(packed) > plan.Slots {
			t.Fatalf("tree %d: run-time packing needs %d slots, plan made %d (%d packed at plan time)", i, len(packed), plan.Slots, len(plan.Slices))
		}
	}
	if tried < 1000 {
		t.Fatalf("only %d trees with slots", tried)
	}
	t.Logf("%d trees: run-time packing needs at most %d slots more than plan time", tried, worst)
}
