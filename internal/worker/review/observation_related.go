package review

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	maxRelatedObservationLinks = 2
	maxRelatedObservationReads = 4
)

// Keep the existing primary windows unless a checked note can be paired
// with a primary. A full request can trade one lower-priority window for
// the first note, without adding to the request's ordinary window budget.
func (s *roleStage) relatedObservationWindows(all []toolObservation, obs []obsInput, windows []record.Excerpt,
	files map[string]source.Entry, lims []string) ([]record.Excerpt, []string) {
	links, reads := 0, 0
	scope := normalizedScope(s.j.Scope)
	for i, o := range all {
		if len(o.Related) == 0 || len(obs[i].EvidenceIDs) == 0 || o.Location == nil || !inScope(o.Location.Path, scope) {
			continue
		}
		for _, note := range o.Related {
			if links == maxRelatedObservationLinks || s.ctx.Err() != nil {
				return windows, lims
			}
			location := note.Location
			e, ok := files[location.Path]
			kind := windowKind(location.Path)
			if !ok || e.SHA256 != location.BlobSHA256 || kind == "" ||
				!inScope(location.Path, scope) || s.secretFiles()[location.Path] {
				continue
			}
			at := slices.IndexFunc(windows, func(x record.Excerpt) bool {
				return x.Path == location.Path && x.Start <= location.StartLine && location.EndLine <= x.End
			})
			var excerpt record.Excerpt
			if at >= 0 {
				excerpt = windows[at]
			} else {
				if reads == maxRelatedObservationReads {
					continue
				}
				drop := -1
				if len(windows) == roleMaxWindows {
					if links > 0 {
						continue
					}
					for pos := len(windows) - 1; pos >= 0; pos-- {
						if !slices.Contains(obs[i].EvidenceIDs, windows[pos].ID) {
							drop = pos
							break
						}
					}
					if drop < 0 {
						continue
					}
				}
				reads++
				excerpt = s.relatedObservationExcerpt(e, *location, kind)
				if excerpt.ID == "" {
					continue
				}
				if drop >= 0 {
					id := windows[drop].ID
					for j := range obs {
						obs[j].EvidenceIDs = slices.DeleteFunc(obs[j].EvidenceIDs, func(ref string) bool { return ref == id })
					}
					windows = slices.Delete(windows, drop, drop+1)
					lims = append(lims, "observation_context_outside_input")
				}
				windows = append(windows, excerpt)
			}
			link := obsRelatedInput{Kind: note.Kind, EvidenceID: excerpt.ID, StartLine: location.StartLine, EndLine: location.EndLine}
			if !slices.Contains(obs[i].Related, link) {
				obs[i].Related = append(obs[i].Related, link)
				if !slices.Contains(obs[i].EvidenceIDs, excerpt.ID) {
					obs[i].EvidenceIDs = append(obs[i].EvidenceIDs, excerpt.ID)
				}
				links++
			}
		}
	}
	return windows, lims
}

func (s *roleStage) relatedObservationExcerpt(e source.Entry, loc obsdoc.Location, kind string) record.Excerpt {
	lines, ok, err := s.sourceLines(e, nil)
	if err != nil || !ok || loc.StartLine < 1 || loc.EndLine < loc.StartLine || loc.EndLine > len(lines) ||
		loc.EndLine-loc.StartLine >= 2*roleWindowRadius+1 {
		return record.Excerpt{}
	}
	for _, start := range []int{max(1, loc.StartLine-roleWindowRadius), loc.StartLine} {
		end := min(len(lines), max(loc.EndLine, loc.StartLine+roleWindowRadius), start+2*roleWindowRadius)
		x, ok := s.boundedExcerpt(e, kind, "observation", start, lines[start-1:end], roleMaxWindowBytes)
		if ok && loc.EndLine <= x.End {
			return x
		}
	}
	return record.Excerpt{}
}
