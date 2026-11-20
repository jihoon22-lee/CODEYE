package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// contractWholeFiles connects already selected parts of a short runtime file
// to the signatures and branches outside the original windows.
func (s *roleStage) contractWholeFiles(scope string, anchors, overview []record.Excerpt, pool []contractExcerpt, files map[string]source.Entry) ([]contractExcerpt, error) {
	var whole []contractExcerpt
	seen := map[string]bool{}
	for _, anchor := range anchors {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		entry, exists := files[anchor.Path]
		if !exists || seen[anchor.Path] || !contractRuntimePath(anchor.Path, anchor.Kind) || record.SourceKind(anchor.Path) != "code" ||
			!record.ScopeUnder(anchor.Path, scope) || !inScope(anchor.Path, normalizedScope(s.j.Scope)) || s.secretFiles()[anchor.Path] ||
			benchmarkPath(anchor.Path) || minifiedSource(anchor.Path) || !record.HasNewContractEvidence(anchor, overview) {
			continue
		}
		refersToSameFile := func(e record.Excerpt) bool {
			return e.Kind == "code" && e.ID != anchor.ID && e.Path == anchor.Path && e.SHA256 == anchor.SHA256
		}
		if !slices.ContainsFunc(overview, refersToSameFile) &&
			!slices.ContainsFunc(pool, func(e contractExcerpt) bool { return refersToSameFile(e.excerpt) }) {
			continue
		}
		anchor, text, err := s.contractReadWindow(anchor, files)
		if err != nil {
			return nil, err
		}
		if !shortReviewSource(text, roleMaxWindowBytes) || record.GeneratedHeader([]byte(text)) {
			continue
		}
		lines := strings.Split(text, "\n")
		x, ok := s.boundedExcerpt(entry, "code", "context", 1, lines, roleMaxWindowBytes)
		if !ok || x.End != len(lines) || x.ID == anchor.ID {
			continue
		}
		_, fresh := contractWindowActivity(anchor, text, overview)
		if fresh {
			whole = append(whole, contractExcerpt{excerpt: x})
			seen[anchor.Path] = true
			if len(whole) == record.MaxContractProbeCandidates {
				break
			}
		}
	}
	return whole, nil
}

func preferWholeContractFiles(whole, pool []contractExcerpt, priorCodeIDs []string) []contractExcerpt {
	combined := make([]contractExcerpt, 0, len(whole)+len(pool))
	for _, full := range whole {
		if existing := slices.IndexFunc(pool, func(e contractExcerpt) bool { return e.excerpt.ID == full.excerpt.ID }); existing >= 0 {
			full = pool[existing]
		}
		combined = append(combined, full)
	}
	for _, id := range priorCodeIDs {
		if at := slices.IndexFunc(pool, func(e contractExcerpt) bool { return e.excerpt.ID == id }); at >= 0 &&
			!slices.ContainsFunc(combined, func(e contractExcerpt) bool { return e.excerpt.ID == id }) {
			combined = append(combined, pool[at])
		}
	}
	for _, window := range pool {
		x := window.excerpt
		if slices.Contains(priorCodeIDs, x.ID) || slices.ContainsFunc(whole, func(full contractExcerpt) bool { return full.excerpt.ID == x.ID }) {
			continue
		}
		// Preserve exact prior citations and slice lineage. Keep distinct branch
		// windows so a partial judgment can still cite separate conditions.
		if x.Kind == "code" && x.Origin == "context" && len(window.sources) == 0 &&
			slices.ContainsFunc(whole, func(full contractExcerpt) bool {
				return full.excerpt.Path == x.Path && full.excerpt.SHA256 == x.SHA256
			}) &&
			slices.ContainsFunc(pool, func(other contractExcerpt) bool {
				e := other.excerpt
				return e.ID != x.ID && e.Kind == "code" && e.Path == x.Path && e.SHA256 == x.SHA256 && e.Start <= x.Start && x.End <= e.End
			}) {
			continue
		}
		combined = append(combined, window)
	}
	return combined
}
