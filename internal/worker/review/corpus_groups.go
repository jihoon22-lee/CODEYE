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
// and the slice planner (slices.go) both read it, so they agree on the
// grouping and on which files exist.
type codeGroups struct {
	order []string                  // group keys in visit order (groupOrder)
	files map[string][]source.Entry // group key -> eligible files, rank order
	// eligible counts every eligible file; lims holds
	// source_in_excluded_directories when source in scope was left out.
	eligible int
	lims     []string
}

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
// ecosystem, else "." (sources.go). Within a group files go entry point,
// source, test, then by depth and path.
func planCodeGroups(m source.Manifest, scope domain.Scope) codeGroups {
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
	g := codeGroups{files: map[string][]source.Entry{}}
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
	g.order = groupOrder(g.files)
	for _, k := range g.order {
		list := g.files[k]
		sort.SliceStable(list, func(a, b int) bool {
			x, y := list[a].Path, list[b].Path
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

// moduleID is the module ID (<ecosystem>:<dir>) of a group key: a bare
// directory for Go, groupKey(dir, eco) otherwise.
func moduleID(key string) string {
	if dir, eco, ok := strings.Cut(key, "\x00"); ok {
		return eco + ":" + dir
	}
	return "go:" + key
}
