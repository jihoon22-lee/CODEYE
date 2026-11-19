package review

import (
	"path"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// codeGroups is the module grouping of the role-pack code corpus, decided
// from the snapshot manifest and the scope alone (no file is read): the
// eligible code and test files of every group in rank order and the order
// in which a round visits the groups. The overview investigate (codeCorpus)
// and the slice planner (slice_plan.go) both read it, so they agree on the
// grouping and on which files exist.
type codeGroups struct {
	order []string                  // group keys in visit order (groupOrder)
	files map[string][]source.Entry // group key -> eligible files, rank order
	// rank is a file's kind within its group: 0 entry point, 1 other
	// source, 2 test.
	rank map[string]int
	// secondary maps a file below a secondary path (record.SecondaryRule)
	// to the segment that matched.
	secondary map[string]string
	// primary is the number of primary groups; they come first in order.
	primary int
	// eligible counts every eligible file; lims holds
	// source_in_excluded_directories when source in scope was left out.
	eligible int
	lims     []string
	// tier is a file's pull-request tier (tierChanged, tierNeighbor,
	// tierOther); nil for a run without a pull request, whose order does
	// not depend on it.
	tier map[string]int
}

// Pull-request tiers of a file (docs/REVIEW.md#검토-우선순위): a file the
// pull request changes, a file in the directory of a changed file, any other
// file. They come before class and rank.
const (
	tierChanged = iota
	tierNeighbor
	tierOther
)

// Pull-request labels of a scope file (record.ScopeFile.PullRequest).
const (
	changeChanged  = "changed"
	changeNeighbor = "neighbor"
)

// includedFiles are the manifest entries a review may read: included regular
// files with a digest and a size.
func includedFiles(m source.Manifest) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range m.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil {
			out[e.Path] = e
		}
	}
	return out
}

// planCodeGroups groups the eligible files: a Go file belongs to its deepest
// go.mod directory (outside vendor, testdata, "." and "_" directories); a
// file of another ecosystem to its deepest build manifest directory of that
// ecosystem, else "." (sources.go). Within a group the project's own files
// come before files below a secondary path (rule), then entry point,
// source, test, then by depth and path; groups of secondary modules are
// visited after every primary group (groupOrder). A snapshot without
// secondary paths keeps the order it had before the rule existed.
func planCodeGroups(m source.Manifest, scope domain.Scope, rule record.SecondaryRule) codeGroups {
	return planCodeGroupsFor(m, scope, rule, nil)
}

// planCodeGroupsFor is planCodeGroups of a pull-request run when changed
// (the changed paths that exist at the head commit) is not nil: within a
// group the changed files come first, then the files in their directories,
// then the rest in the usual order, and phases visits the changed files of
// every group before any other file. A nil changed list is planCodeGroups
// exactly.
func planCodeGroupsFor(m source.Manifest, scope domain.Scope, rule record.SecondaryRule, changed []string) codeGroups {
	files := includedFiles(m)
	scope = normalizedScope(scope)
	var modules []string
	for p := range files {
		if path.Base(p) == "go.mod" && !goIgnored(p) {
			modules = append(modules, record.DocumentDir(p))
		}
	}
	sort.Strings(modules)
	owner := func(p string) string {
		best := "."
		for _, m := range modules {
			if record.ScopeUnder(p, m) && len(m) >= len(best) {
				best = m
			}
		}
		return best
	}
	g := codeGroups{files: map[string][]source.Entry{}, rank: map[string]int{}, secondary: map[string]string{}}
	others := otherModuleRoots(files)
	for _, p := range sortedKeys(files) {
		e := files[p]
		if !inScope(p, scope) || *e.Size > maxFileBytes {
			continue
		}
		key := ""
		switch eco := otherSource(p); {
		case strings.HasSuffix(p, ".go") && !goIgnored(p):
			key = owner(p)
		case !strings.HasSuffix(p, ".go") && eco != "":
			key = groupKey(others.owner(eco, p), eco)
		default:
			if excludedSource(p) {
				g.lims = append(g.lims, limExcludedSource)
			}
			continue
		}
		g.eligible++
		g.files[key] = append(g.files[key], e)
		if seg := rule.Match(p); seg != "" {
			g.secondary[p] = seg
		}
	}
	rank := func(p string) int {
		switch {
		case !strings.HasSuffix(p, ".go"):
			return otherRank(p, others)
		case path.Base(p) == "main.go":
			return 0
		case strings.HasSuffix(p, "_test.go"):
			return 2
		}
		return 1
	}
	if changed != nil {
		g.tier = map[string]int{}
		isChanged, dirs := map[string]bool{}, map[string]bool{}
		for _, p := range changed {
			isChanged[p] = true
			dirs[path.Dir(p)] = true
		}
		for _, list := range g.files {
			for _, e := range list {
				switch {
				case isChanged[e.Path]:
					g.tier[e.Path] = tierChanged
				case dirs[path.Dir(e.Path)]:
					g.tier[e.Path] = tierNeighbor
				default:
					g.tier[e.Path] = tierOther
				}
			}
		}
	}
	g.order = groupOrder(g.files, func(k string) bool { return rule.ModuleClass(moduleID(k)) == record.ClassSecondary })
	for _, k := range g.order {
		if rule.ModuleClass(moduleID(k)) == record.ClassPrimary {
			g.primary++
		}
	}
	for _, k := range g.order {
		list := g.files[k]
		for _, e := range list {
			g.rank[e.Path] = rank(e.Path)
		}
		sort.SliceStable(list, func(a, b int) bool {
			x, y := list[a].Path, list[b].Path
			if tx, ty := g.tierOf(x), g.tierOf(y); tx != ty {
				return tx < ty
			}
			if sx, sy := g.secondary[x] != "", g.secondary[y] != ""; sx != sy {
				return sy
			}
			if rank(x) != rank(y) {
				return rank(x) < rank(y)
			}
			if strings.Count(x, "/") != strings.Count(y, "/") {
				return strings.Count(x, "/") < strings.Count(y, "/")
			}
			return x < y
		})
	}
	return g
}

// tierOf is a file's pull-request tier (tierChanged for every file of a run
// without a pull request: one tier, no effect on the order).
func (g codeGroups) tierOf(p string) int {
	if g.tier == nil {
		return tierChanged
	}
	return g.tier[p]
}

// change is a file's pull-request label: changed, neighbor or "" (another
// file, or a run without a pull request).
func (g codeGroups) change(p string) string {
	if g.tier == nil {
		return ""
	}
	switch g.tier[p] {
	case tierChanged:
		return changeChanged
	case tierNeighbor:
		return changeNeighbor
	}
	return ""
}

// phase is one pass of the overview's rounds: the groups it visits, in
// order, and the files of each group it offers, in the group's order.
type phase struct {
	order []string
	files map[string][]source.Entry
}

// phases are the passes the overview's rounds go through: the primary
// groups until their files run out, then the secondary groups. A snapshot
// without secondary modules has one phase, the whole order. A pull-request
// run first passes over the changed files of every group, then over the
// files in their directories, then over the rest as above.
func (g codeGroups) phases() []phase {
	var out []phase
	add := func(order []string, keep func(string) bool) {
		ph := phase{files: map[string][]source.Entry{}}
		for _, k := range order {
			for _, e := range g.files[k] {
				if keep(e.Path) {
					ph.files[k] = append(ph.files[k], e)
				}
			}
			if len(ph.files[k]) > 0 {
				ph.order = append(ph.order, k)
			}
		}
		if len(ph.order) > 0 {
			out = append(out, ph)
		}
	}
	if g.tier == nil {
		for _, p := range [][]string{g.order[:g.primary], g.order[g.primary:]} {
			if len(p) > 0 {
				out = append(out, phase{order: p, files: g.files})
			}
		}
		return out
	}
	add(g.order, func(p string) bool { return g.tier[p] == tierChanged })
	add(g.order, func(p string) bool { return g.tier[p] == tierNeighbor })
	add(g.order[:g.primary], func(p string) bool { return g.tier[p] == tierOther })
	add(g.order[g.primary:], func(p string) bool { return g.tier[p] == tierOther })
	return out
}

// moduleID is the module ID (<ecosystem>:<dir>) of a group key: a bare
// directory for Go, groupKey(dir, eco) otherwise.
func moduleID(key string) string {
	if dir, eco, ok := strings.Cut(key, "\x00"); ok {
		return eco + ":" + dir
	}
	return "go:" + key
}
