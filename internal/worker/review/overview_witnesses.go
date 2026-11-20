package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func (c *corpusLinks) addWholeWitness(snapshot string, x record.Excerpt, text string) {
	if text == "" || !contractRuntimePath(x.Path, x.Kind) || benchmarkPath(x.Path) ||
		!shortReviewSource(text, roleMaxCodeFile) || record.GeneratedHeader([]byte(text)) {
		return
	}
	implementation := false
	contractExecutableLines(x.Text, x.Path, func(_ int, _ bool) bool {
		implementation = true
		return true
	})
	if !implementation {
		return
	}
	if c.wholeWitness == nil {
		c.wholeWitness = map[string]record.Excerpt{}
	}
	id := x.ID
	x.Start, x.End, x.Text = 1, 1+strings.Count(text, "\n"), text
	x.ID = record.ExcerptID(snapshot, x.Path, x.SHA256, x.Start, x.End)
	c.wholeWitness[id] = x
}

// Widen only after the request's normal files fit. Exact call and test
// links keep the excerpts that justified them, and a second window on the
// same file keeps its own evidence ID.
func (s *roleStage) widenOverviewWitnesses(sent []record.Excerpt, links corpusLinks, instructions string,
	build func([]record.Excerpt) ([]byte, error)) ([]record.Excerpt, bool) {
	sent = slices.Clone(sent)
	remaining := roleMaxCodeBytes
	counts := map[string]int{}
	for _, x := range sent {
		remaining -= record.JSONLen(x.Text)
		counts[x.Path]++
	}
	member := links.member.hint(sent)
	widened := false
	for i, x := range sent {
		full, ok := links.wholeWitness[x.ID]
		if !ok || counts[x.Path] != 1 || (member != nil && member.ImplementationEvidenceID == x.ID) ||
			slices.ContainsFunc(links.directCalls, func(link directCall) bool {
				return link.CallerEvidenceID == x.ID || link.CalleeEvidenceID == x.ID
			}) {
			continue
		}
		extra := record.JSONLen(full.Text) - record.JSONLen(x.Text)
		if extra > remaining {
			continue
		}
		sent[i] = full
		b, err := build(sent)
		if err != nil {
			sent[i] = x
			continue
		}
		if _, ok := s.fits(instructions, b, true); !ok {
			sent[i] = x
			continue
		}
		widened = true
		remaining -= extra
	}
	return sent, widened
}
