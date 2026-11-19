package review

import (
	"fmt"
	"path"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Module-sliced review (docs/REVIEW.md#모듈-단위-검토-분할). The overview
// investigate keeps its breadth-first selection (codeCorpus); the files it
// cannot offer are reviewed in slots, each slot one investigate/verify pair
// under the same caps.
//
// The plan fixes only how many slots exist: a pure function of the snapshot
// manifest, the scope, the secondary path rule and the constants below,
// never of the operator's call ceiling or of analysis results, so a retry
// that resets every task row re-plans exactly the same task keys.
// review.scope, which runs after static analysis, fills the slots in
// priority order (packSlots over the files with their tool observations,
// docs/REVIEW.md#검토-우선순위); the call ceiling then admits slots in that
// order.
const (
	sliceMaxFiles = roleMaxCodeFiles
	sliceMaxBytes = roleMaxCodeBytes
	// MaxSlices bounds the slots of one run; files beyond them are not
	// planned (review_slice_limit).
	MaxSlices = record.MaxSlices
	// limSliceLimit marks eligible files left out because every slot was
	// already filled.
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

// plannedFile is one eligible code or test file with what decides its
// place: its class (primary or secondary) and rank, and for a file with
// tool diagnostics their count and highest severity rank.
type plannedFile struct {
	Path, SHA256 string
	Module       string // module ID <ecosystem>:<dir>
	Size         int64
	Bound        int // excerptBound(Size)
	// Class is record.ClassPrimary or record.ClassSecondary; Secondary is
	// the reason of a secondary file (secondary_path:<segment>,
	// generated_file).
	Class, Secondary string
	Rank             string // record.Ranks
	Observations     int    // non-metric tool diagnostics located in the file
	Severity         int    // highest record.SeverityRank of those diagnostics
	// Tier is the pull-request tier (tierChanged for every file of a run
	// without a pull request) and Change its label (changed, neighbor, "").
	Tier   int
	Change string
	group  int // index of the module in the visit order
	pos    int // position in the module's rank order
}

// reviewSlice is one filled slot: up to sliceMaxFiles files and
// sliceMaxBytes bound bytes of one or more modules of one class.
type reviewSlice struct {
	Key     string   // s001..s096
	Modules []string // sorted module IDs
	Files   []plannedFile
	// Class, Rank and Change are those of the slot's first
	// (highest-priority) file.
	Class, Rank, Change string
}

// slotTaskKeys are the investigate and verify task keys of a slot.
func slotTaskKeys(key string) (string, string) {
	return "review.investigate." + key, "review.verify." + key
}

// TaskKeys are the slice's investigate and verify task keys.
func (s reviewSlice) TaskKeys() (string, string) { return slotTaskKeys(s.Key) }

// planGroup is one module of the corpus with its eligible file count.
type planGroup struct {
	Module   string
	Eligible int
}

// slicePlan is the plan-time view of the slots. Every eligible file is in
// exactly one of the overview prefix (files the overview is certain to offer
// when readable), a slice or NotPlanned. Slices and NotPlanned are the
// packing with what the manifest alone tells (path class, entry point,
// source, test); review.scope packs the same files again with the tool
// observations and may order them differently. Slots is the number of slot
// tasks: the plan-time packing plus slack for that re-ordering, at most
// MaxSlices, 0 when every file fits the overview.
type slicePlan struct {
	Groups     []planGroup // group visit order
	Overview   []plannedFile
	Rest       []plannedFile // the eligible files beyond the prefix, group order
	Slices     []reviewSlice
	NotPlanned []plannedFile
	Slots      int
}

// sliceKey is the key of the i-th slot (0-based).
func sliceKey(i int) string { return fmt.Sprintf("s%03d", i+1) }

// slotSlack is the number of slots planned beyond the plan-time packing of
// n slots: filling them in priority order can need a few more (diagnosed
// files form chunks of their own), so one more slot per eight, at least
// two. When the strict packing still needs more, fillSlots relaxes it.
func slotSlack(n int) int { return max(2, (n+7)/8) }

// planSlices is the deterministic slot plan of a snapshot.
//
//   - The overview prefix P simulates codeCorpus with every file at its
//     bound: round by round over the groups, a file belongs to P while
//     fewer than roleMaxCodeFiles files are taken, at least 128 bytes remain
//     and its bound fits the remaining roleMaxCodeBytes. The first file that
//     does not fit ends P. The overview visits files in the same order and
//     never spends more than the bound, so it reaches every file of P with
//     room for its whole excerpt.
//   - The other files are packed by packSlots with their path class and
//     kind; the slot count is that packing plus slotSlack, at most MaxSlices.
//
// At run time review.scope drops the files the overview actually offered (it
// may offer more than P when a file of P was unreadable), so no file is
// reviewed twice.
func planSlices(m source.Manifest, scope domain.Scope, rule record.SecondaryRule) slicePlan {
	return planSlicesFor(m, scope, rule, nil)
}

// planSlicesFor is planSlices of a pull-request run (changed not nil, see
// planCodeGroupsFor): the overview prefix takes the changed files first and
// the slots are packed changed files first, then their neighbors.
func planSlicesFor(m source.Manifest, scope domain.Scope, rule record.SecondaryRule, changed []string) slicePlan {
	g := planCodeGroupsFor(m, scope, rule, changed)
	var plan slicePlan
	position := map[string]int{}
	for _, k := range g.order {
		for i, e := range g.files[k] {
			position[e.Path] = i
		}
	}
	file := func(gi, pos int, key string, e source.Entry) plannedFile {
		f := plannedFile{Path: e.Path, SHA256: e.SHA256, Module: moduleID(key), Size: *e.Size, Bound: excerptBound(*e.Size), Class: record.ClassPrimary, group: gi, pos: pos,
			Tier: g.tierOf(e.Path), Change: g.change(e.Path)}
		if seg := g.secondary[e.Path]; seg != "" {
			f.Class, f.Secondary = record.ClassSecondary, record.ReasonSecondaryPath+":"+seg
		}
		f.Rank = []string{record.RankEntryPoint, record.RankSource, record.RankTest}[g.rank[e.Path]]
		return f
	}
	for _, k := range g.order {
		plan.Groups = append(plan.Groups, planGroup{Module: moduleID(k), Eligible: len(g.files[k])})
	}
	inOverview := map[string]bool{}
	index := map[string]int{}
	for gi, k := range g.order {
		index[k] = gi
	}
	remain, open := roleMaxCodeBytes, true
	for _, ph := range g.phases() {
		more := open
		for round := 0; more; round++ {
			progressed := false
			for _, k := range ph.order {
				if round >= len(ph.files[k]) {
					continue
				}
				progressed = true
				e := ph.files[k][round]
				f := file(index[k], position[e.Path], k, e)
				if len(plan.Overview) >= roleMaxCodeFiles || remain < 128 || f.Bound > remain {
					open = false
					break
				}
				plan.Overview = append(plan.Overview, f)
				inOverview[f.Path] = true
				remain -= f.Bound
			}
			more = open && progressed
		}
	}
	for gi, k := range g.order {
		for pos, e := range g.files[k] {
			if !inOverview[e.Path] {
				plan.Rest = append(plan.Rest, file(gi, pos, k, e))
			}
		}
	}
	all := packSlots(plan.Rest)
	if len(all) == 0 {
		return plan
	}
	plan.Slots = min(MaxSlices, len(all)+slotSlack(len(all)))
	plan.Slices = all[:min(len(all), plan.Slots)]
	for _, s := range all[len(plan.Slices):] {
		plan.NotPlanned = append(plan.NotPlanned, s.Files...)
	}
	return plan
}

// packSlots orders files by review priority and packs them into slots
// (docs/REVIEW.md#검토-우선순위). It is deterministic: the path is the last
// tie-break.
//
//  1. Within each module, files go by class (primary before secondary),
//     rank (tool diagnostics, analyzer not succeeded, entry point, source,
//     test), then for diagnosed files the highest severity and the count,
//     then directory and the module's rank order, so a directory's files
//     stay together.
//  2. Each module's files are cut, in that order, into consecutive chunks of
//     at most sliceMaxFiles files and sliceMaxBytes bound bytes of one class
//     and rank; a directory that cannot finish in the current chunk starts
//     a new one.
//  3. Chunks are merged across modules: the next chunk is the best head
//     chunk of any module by class, rank, then (diagnosed chunks) severity
//     and count, then every module's first chunk of the class before a
//     second chunk of any, then a weighted round robin by the module's bytes
//     of that class (the module with the largest bytes per chunk already
//     taken goes first), then module order. So a large module takes most
//     slots without starving a second primary module.
//  4. Consecutive chunks of one class share a slot while the caps allow (at
//     most MaxSliceModules modules), so small modules do not take one slot
//     each.
func packSlots(files []plannedFile) []reviewSlice { return packSlotsAt(files, packStrict) }

// Packing levels: strict keeps directories and ranks apart in chunks;
// packAnyDir drops the directory rule; packAnyRank also lets a chunk run
// across ranks (its rank is that of its first file). The order of the files
// is the same at every level; only the chunk boundaries move.
const (
	packStrict = iota
	packAnyDir
	packAnyRank
)

// fillSlots packs files into at most n slots in priority order: the strict
// packing when it fits, else the first relaxed level that fits. When none
// fits (the files outnumber the slot limit), it is the strict packing, whose
// slots beyond n the caller counts as not planned (the lowest-priority
// files): relaxing would only trade cohesion for a few more files.
func fillSlots(files []plannedFile, n int) []reviewSlice {
	strict := packSlotsAt(files, packStrict)
	if len(strict) <= n {
		return strict
	}
	for _, level := range []int{packAnyDir, packAnyRank} {
		if out := packSlotsAt(files, level); len(out) <= n {
			return out
		}
	}
	return strict
}

func packSlotsAt(files []plannedFile, level int) []reviewSlice {
	byModule := map[int][]plannedFile{}
	var groups []int
	for _, f := range files {
		if _, ok := byModule[f.group]; !ok {
			groups = append(groups, f.group)
		}
		byModule[f.group] = append(byModule[f.group], f)
	}
	sort.Ints(groups)
	type chunk struct {
		files      []plannedFile
		bytes      int
		tier       int
		class      string
		rank       int
		sev, count int
	}
	chunks := map[int][]chunk{}
	weight := map[int]map[string]int{}
	for _, gi := range groups {
		list := byModule[gi]
		sort.SliceStable(list, func(a, b int) bool {
			x, y := list[a], list[b]
			switch {
			case x.Tier != y.Tier:
				return x.Tier < y.Tier
			case x.Class != y.Class:
				return x.Class == record.ClassPrimary
			case record.RankIndex(x.Rank) != record.RankIndex(y.Rank):
				return record.RankIndex(x.Rank) < record.RankIndex(y.Rank)
			case x.Severity != y.Severity:
				return x.Severity > y.Severity
			case x.Observations != y.Observations:
				return x.Observations > y.Observations
			case path.Dir(x.Path) != path.Dir(y.Path):
				return path.Dir(x.Path) < path.Dir(y.Path)
			case x.pos != y.pos:
				return x.pos < y.pos
			}
			return x.Path < y.Path
		})
		weight[gi] = map[string]int{}
		// run[i] is the files and bytes from i to the end of its directory
		// (within one class and rank): a directory that cannot finish in the
		// current chunk starts a new one, so a chunk never holds the
		// beginning of one directory after another directory's files.
		type run struct{ files, bytes int }
		runs := make([]run, len(list))
		for i := len(list) - 1; i >= 0; i-- {
			runs[i] = run{1, list[i].Bound}
			if i+1 < len(list) && path.Dir(list[i+1].Path) == path.Dir(list[i].Path) && list[i+1].Tier == list[i].Tier && list[i+1].Class == list[i].Class && list[i+1].Rank == list[i].Rank {
				runs[i].files += runs[i+1].files
				runs[i].bytes += runs[i+1].bytes
			}
		}
		var cur chunk
		for i, f := range list {
			weight[gi][f.Class] += f.Bound
			newDir := level == packStrict && i > 0 && path.Dir(list[i-1].Path) != path.Dir(f.Path)
			newRank := level != packAnyRank && cur.rank != record.RankIndex(f.Rank)
			if len(cur.files) > 0 && (len(cur.files) >= sliceMaxFiles || cur.bytes+f.Bound > sliceMaxBytes || cur.tier != f.Tier || cur.class != f.Class || newRank ||
				(newDir && (len(cur.files)+runs[i].files > sliceMaxFiles || cur.bytes+runs[i].bytes > sliceMaxBytes))) {
				chunks[gi] = append(chunks[gi], cur)
				cur = chunk{}
			}
			if len(cur.files) == 0 {
				cur.tier, cur.class, cur.rank = f.Tier, f.Class, record.RankIndex(f.Rank)
			}
			cur.files = append(cur.files, f)
			cur.bytes += f.Bound
			if f.Rank == record.RankToolObservations && record.RankIndex(f.Rank) == cur.rank {
				cur.sev, cur.count = max(cur.sev, f.Severity), cur.count+f.Observations
			}
		}
		if len(cur.files) > 0 {
			chunks[gi] = append(chunks[gi], cur)
		}
	}
	// Merge the modules' chunk sequences; each sequence is already in
	// priority order.
	head := map[int]int{}
	picks := map[int]map[string]int{}
	for _, gi := range groups {
		picks[gi] = map[string]int{}
	}
	better := func(a, b int) bool {
		x, y := chunks[a][head[a]], chunks[b][head[b]]
		switch {
		case x.tier != y.tier:
			return x.tier < y.tier
		case x.class != y.class:
			return x.class == record.ClassPrimary
		case x.rank != y.rank:
			return x.rank < y.rank
		case x.sev != y.sev:
			return x.sev > y.sev
		case x.count != y.count:
			return x.count > y.count
		}
		// Every module's first chunk of the class before a second chunk of
		// any, then a weighted round robin: the module bytes of this class
		// per chunk already taken (+1), largest first.
		if pa, pb := picks[a][x.class], picks[b][y.class]; (pa == 0) != (pb == 0) {
			return pa == 0
		}
		wx, wy := weight[a][x.class]*(picks[b][y.class]+1), weight[b][y.class]*(picks[a][x.class]+1)
		if wx != wy {
			return wx > wy
		}
		return a < b
	}
	var out []reviewSlice
	var cur []plannedFile
	used, tier, class, rank := 0, 0, "", 0
	modules := func(list []plannedFile) []string {
		var ms []string
		for _, f := range list {
			if !slices.Contains(ms, f.Module) {
				ms = append(ms, f.Module)
			}
		}
		sort.Strings(ms)
		return ms
	}
	closeSlot := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, reviewSlice{Key: sliceKey(len(out)), Modules: modules(cur), Files: cur, Class: class, Rank: record.Ranks[rank], Change: cur[0].Change})
		cur, used = nil, 0
	}
	for {
		best := -1
		for _, gi := range groups {
			if head[gi] < len(chunks[gi]) && (best < 0 || better(gi, best)) {
				best = gi
			}
		}
		if best < 0 {
			break
		}
		c := chunks[best][head[best]]
		head[best]++
		picks[best][c.class]++
		if len(cur) > 0 && (c.tier != tier || c.class != class || len(cur)+len(c.files) > sliceMaxFiles || used+c.bytes > sliceMaxBytes ||
			len(modules(append(slices.Clip(cur), c.files...))) > record.MaxSliceModules) {
			closeSlot()
		}
		if len(cur) == 0 {
			tier, class, rank = c.tier, c.class, c.rank
		}
		cur = append(cur, c.files...)
		used += c.bytes
	}
	closeSlot()
	return out
}
