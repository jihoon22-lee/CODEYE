package review

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// packUnits adds the review units (units.go) of the overview after the
// selector windows: the files in the overview's round-robin order over the
// modules (codeGroups.orderedFiles), those no selector window shows first,
// then the files the selectors chose, whose windows then travel as
// references inside their units, while the excerpt count and the numbered
// source bytes fit the request budget. Every file's first unit comes
// before any file's second; a file's units are taken in order and the
// first that does not fit ends that file. The units are listed by module,
// directory and path after the selector windows, so related code travels
// together.
func (s *roleStage) packUnits(g codeGroups, out []record.Excerpt, offered map[string]string) ([]record.Excerpt, error) {
	used := 0
	ids := map[string]bool{}
	for _, x := range out {
		used += record.NumberedLen(x.Text, x.Start)
		ids[x.ID] = true
	}
	module := map[string]int{}
	var ordered []source.Entry
	for i, key := range g.order {
		for _, e := range g.files[key] {
			module[e.Path] = i
		}
	}
	for e := range g.orderedFiles() {
		ordered = append(ordered, e)
	}
	// Files no selector window shows come first (breadth), then the files
	// the selectors chose, whose windows then travel as references inside
	// their units.
	visit := make([]source.Entry, 0, len(ordered))
	shown := map[string]bool{}
	for _, x := range out {
		shown[x.Path] = true
	}
	for _, e := range ordered {
		if !shown[e.Path] {
			visit = append(visit, e)
		}
	}
	for _, e := range ordered {
		if shown[e.Path] {
			visit = append(visit, e)
		}
	}
	var units []record.Excerpt
	count := len(out)
	lim := s.limit()
	cached := map[string][]string{}
	split := map[string][]lineSpan{}
	next := map[string]int{} // the next unit of a file; -1 once a unit did not fit
	// The first pass takes each file's first unit (a whole file when it
	// fits one), the second the rest of each file in order: every module
	// and file is represented before any file gets its second unit.
	for pass := 0; pass < 2; pass++ {
		for _, e := range visit {
			if count >= lim.excerpts || lim.source-used < 128 {
				break
			}
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
			if next[e.Path] < 0 {
				continue
			}
			lines, ok, err := s.sourceLines(e, cached)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			spans, known := split[e.Path]
			if !known {
				fu, _ := unitsOf(e.Path, lines, 1, len(lines), lim.unit)
				spans = fu.units
				split[e.Path] = spans
			}
			for next[e.Path] >= 0 && next[e.Path] < len(spans) && (pass == 1 || next[e.Path] == 0) {
				u := spans[next[e.Path]]
				text := strings.Join(lines[u.start-1:u.end], "\n")
				id := record.ExcerptID(s.j.Snapshot.Digest, e.Path, e.SHA256, u.start, u.end)
				if strings.TrimSpace(text) == "" || ids[id] {
					next[e.Path]++
					continue
				}
				// Selector windows inside the unit become references.
				saved := 0
				for _, x := range out {
					if x.Path == e.Path && u.start <= x.Start && x.End <= u.end {
						saved += record.NumberedLen(x.Text, x.Start)
					}
				}
				size := record.NumberedLen(text, u.start) - saved
				if count >= lim.excerpts || used+size > lim.source {
					next[e.Path] = -1
					break
				}
				units = append(units, record.Excerpt{ID: id, Path: e.Path, SHA256: e.SHA256, Start: u.start, End: u.end,
					Kind: windowKind(e.Path), Origin: "context", Text: text})
				ids[id] = true
				count++
				used += size
				next[e.Path]++
				if offered[e.Path] == "" {
					offered[e.Path] = id
				}
			}
		}
	}
	slices.SortStableFunc(units, func(a, b record.Excerpt) int {
		return cmp.Or(cmp.Compare(module[a.Path], module[b.Path]), cmp.Compare(path.Dir(a.Path), path.Dir(b.Path)),
			cmp.Compare(a.Path, b.Path), cmp.Compare(a.Start, b.Start))
	})
	return append(out, units...), nil
}

// partialFiles reports whether some code or test file of list is not
// covered from its first to its last line by the excerpts of list
// (excerpt_only).
func (s *roleStage) partialFiles(list []record.Excerpt) bool {
	files := s.entries()
	byPath := map[string][]record.Excerpt{}
	for _, x := range list {
		if x.Kind == "code" || x.Kind == "test" {
			byPath[x.Path] = append(byPath[x.Path], x)
		}
	}
	for p, xs := range byPath {
		e, ok := files[p]
		if !ok {
			return true
		}
		lines, ok, err := s.sourceLines(e, nil)
		if err != nil || !ok {
			return true
		}
		covered := make([]bool, len(lines)+1)
		for _, x := range xs {
			for n := max(1, x.Start); n <= min(x.End, len(lines)); n++ {
				covered[n] = true
			}
		}
		for n := 1; n <= len(lines); n++ {
			if !covered[n] {
				return true
			}
		}
	}
	return false
}
