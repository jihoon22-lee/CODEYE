package review

import (
	"fmt"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Module-sliced review (docs/REVIEW.md#모듈-단위-검토-분할). The overview
// investigate keeps its breadth-first selection (codeCorpus); the files it
// cannot offer are packed into slices, each reviewed by its own
// investigate/verify pair under the same caps. The plan is a pure function
// of the snapshot manifest, the scope and the constants below: never of the
// operator's call ceiling, so a retry that resets every task row re-plans
// exactly the same task keys.
const (
	sliceMaxFiles = roleMaxCodeFiles
	sliceMaxBytes = roleMaxCodeBytes
	// MaxSlices bounds the slice tasks of one run; files beyond it are not
	// planned (review_slice_limit).
	MaxSlices = record.MaxSlices
	// limSliceLimit marks eligible files left out because MaxSlices slices
	// were already planned.
	limSliceLimit = "review_slice_limit"
)

// excerptBound is the most JSON-encoded excerpt bytes a file of size bytes
// can cost the corpus: encoding/json writes at most six bytes per input byte
// (\u00XX, and <, > and & escaped), plus the two quotes, and an excerpt is
// capped at roleMaxCodeFile. It never falls below the real excerpt bytes
// (the planner tests check it on generated content).
func excerptBound(size int64) int {
	return int(min(int64(roleMaxCodeFile), 6*size+2))
}

// plannedFile is one eligible code or test file of the plan.
type plannedFile struct {
	Path, SHA256 string
	Module       string // module ID <ecosystem>:<dir>
	Size         int64
	Bound        int // excerptBound(Size)
}

// reviewSlice is one planned slice: up to sliceMaxFiles files and
// sliceMaxBytes bound bytes of one or more modules.
type reviewSlice struct {
	Key     string   // s001..s032
	Modules []string // sorted module IDs
	Files   []plannedFile
}

// TaskKeys are the slice's investigate and verify task keys.
func (s reviewSlice) TaskKeys() (string, string) {
	return "review.investigate." + s.Key, "review.verify." + s.Key
}

// planGroup is one module of the corpus with its eligible file count.
type planGroup struct {
	Module   string
	Eligible int
}

// slicePlan partitions every eligible file into exactly one of: the
// overview prefix (files the overview is certain to offer when readable),
// a slice, or NotPlanned (beyond MaxSlices).
type slicePlan struct {
	Groups     []planGroup // group visit order
	Overview   []plannedFile
	Slices     []reviewSlice
	NotPlanned []plannedFile
}

// sliceKey is the key of the i-th slice (0-based).
func sliceKey(i int) string { return fmt.Sprintf("s%03d", i+1) }

// planSlices is the deterministic slice plan of a snapshot.
//
//   - The overview prefix P simulates codeCorpus with every file at its
//     bound: round by round over the groups, a file belongs to P while
//     fewer than roleMaxCodeFiles files are taken, at least 128 bytes remain
//     and its bound fits the remaining roleMaxCodeBytes. The first file that
//     does not fit ends P. The overview visits files in the same order and
//     never spends more than the bound, so it reaches every file of P with
//     room for its whole excerpt.
//   - The remaining files of each group, in rank order, are cut into
//     consecutive chunks of at most sliceMaxFiles files and sliceMaxBytes
//     bound bytes (a group that fits stays whole).
//   - Chunks are emitted round by round (the first chunk of every group in
//     group order, then the second, ...) so breadth comes before depth, and
//     consecutive chunks share a slice while the caps allow.
//   - At most MaxSlices slices; the rest is NotPlanned.
//
// At run time review.scope drops from every slice the files the overview
// actually offered (it may offer more than P when a file of P was
// unreadable), so no file is reviewed twice.
func planSlices(m source.Manifest, scope domain.Scope) slicePlan {
	g := planCodeGroups(m, scope)
	var plan slicePlan
	file := func(key string, e source.Entry) plannedFile {
		return plannedFile{Path: e.Path, SHA256: e.SHA256, Module: moduleID(key), Size: *e.Size, Bound: excerptBound(*e.Size)}
	}
	for _, k := range g.order {
		plan.Groups = append(plan.Groups, planGroup{Module: moduleID(k), Eligible: len(g.files[k])})
	}
	inOverview := map[string]bool{}
	remain, open := roleMaxCodeBytes, true
	for round := 0; open; round++ {
		progressed := false
		for _, k := range g.order {
			if round >= len(g.files[k]) {
				continue
			}
			progressed = true
			f := file(k, g.files[k][round])
			if len(plan.Overview) >= roleMaxCodeFiles || remain < 128 || f.Bound > remain {
				open = false
				break
			}
			plan.Overview = append(plan.Overview, f)
			inOverview[f.Path] = true
			remain -= f.Bound
		}
		open = open && progressed
	}
	// Chunks of the remaining files, per group.
	chunks := map[string][][]plannedFile{}
	rounds := 0
	for _, k := range g.order {
		var cur []plannedFile
		used := 0
		for _, e := range g.files[k] {
			if inOverview[e.Path] {
				continue
			}
			f := file(k, e)
			if len(cur) > 0 && (len(cur) >= sliceMaxFiles || used+f.Bound > sliceMaxBytes) {
				chunks[k] = append(chunks[k], cur)
				cur, used = nil, 0
			}
			cur = append(cur, f)
			used += f.Bound
		}
		if len(cur) > 0 {
			chunks[k] = append(chunks[k], cur)
		}
		rounds = max(rounds, len(chunks[k]))
	}
	var cur []plannedFile
	used := 0
	closeSlice := func() {
		if len(cur) == 0 {
			return
		}
		s := reviewSlice{Key: sliceKey(len(plan.Slices)), Files: cur}
		for _, f := range cur {
			if !slices.Contains(s.Modules, f.Module) {
				s.Modules = append(s.Modules, f.Module)
			}
		}
		sort.Strings(s.Modules)
		plan.Slices = append(plan.Slices, s)
		cur, used = nil, 0
	}
	for round := range rounds {
		for _, k := range g.order {
			if round >= len(chunks[k]) {
				continue
			}
			chunk := chunks[k][round]
			bytes := 0
			for _, f := range chunk {
				bytes += f.Bound
			}
			if len(cur)+len(chunk) > sliceMaxFiles || used+bytes > sliceMaxBytes {
				closeSlice()
			}
			if len(plan.Slices) >= MaxSlices {
				plan.NotPlanned = append(plan.NotPlanned, chunk...)
				continue
			}
			cur = append(cur, chunk...)
			used += bytes
		}
	}
	closeSlice()
	return plan
}
