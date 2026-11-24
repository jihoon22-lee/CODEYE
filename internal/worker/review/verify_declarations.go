package review

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// enclosingDeclarations are, for every cited code or test range, the
// declarations of its file that enclose it: from the declaration holding
// its first line to the one holding its last (declarationCuts), when that
// is wider than the range and fits one unit. The verifier reads the whole
// function, method or type around a cited line, not only the lines the
// investigator chose. Each is bound to the snapshot blob (origin
// declaration) and read through the same filters as every model input.
func (s *roleStage) enclosingDeclarations(cited []record.Excerpt) ([]record.Excerpt, error) {
	files := s.entries()
	cached := map[string][]string{}
	cuts := map[string]map[int]bool{}
	seen := map[string]bool{}
	var out []record.Excerpt
	for _, x := range cited {
		if x.Kind != "code" && x.Kind != "test" {
			continue
		}
		e, ok := files[x.Path]
		if !ok || e.SHA256 != x.SHA256 {
			continue
		}
		lines, ok, err := s.sourceLines(e, cached)
		if err != nil {
			return nil, err
		}
		if !ok || x.Start < 1 || x.End > len(lines) {
			continue
		}
		c, ok := cuts[x.Path]
		if !ok {
			c = declarationCuts(x.Path, lines, s.limit().unit)
			cuts[x.Path] = c
		}
		start, end := x.Start, x.End
		for start > 1 && !c[start] {
			start--
		}
		for end < len(lines) && !c[end+1] {
			end++
		}
		for end > x.End && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		if (start == x.Start && end == x.End) || numberedSize(lines, start, end) > s.limit().unit {
			continue
		}
		id := record.ExcerptID(s.j.Snapshot.Digest, x.Path, x.SHA256, start, end)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, record.Excerpt{ID: id, Path: x.Path, SHA256: x.SHA256, Start: start, End: end, Kind: x.Kind,
			Origin: record.OriginDeclaration, Text: strings.Join(lines[start-1:end], "\n")})
	}
	return out, nil
}
