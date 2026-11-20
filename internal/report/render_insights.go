package report

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// withInsights adds the same evidence-backed assessments to both export layouts.
func withInsights(v secView) secView {
	l := v.L
	for i := range v.Sections {
		block := &v.Sections[i]
		var parts []secPart
		switch block.ID {
		case SectionDocuments:
			doc := v.R.Documents
			for _, q := range doc.Quality {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight."+q.Aspect) + " · " + l.Label("insight."+q.Assessment), Paras: []string{q.Statement}, Sources: v.sourceCitations(q.EvidenceIDs, doc.Evidence, true)})
			}
			for _, c := range doc.Comparisons {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight.comparison") + " · " + l.Label("insight."+c.Status), Paras: []string{l.Label("insight.scope") + " · " + c.AppliesTo, c.Statement}, List: v.comparisonDetails(c, doc.Evidence, true)})
			}
		case SectionCodeReview:
			for _, s := range v.R.CodeReview.Strengths {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight.strength") + " · " + s.Title, Paras: []string{l.Label("insight.scope") + " · " + s.Scope, s.Benefit}, Sources: v.sourceCitations(s.EvidenceIDs, v.R.CodeReview.Evidence, true)})
			}
		}
		block.Parts = append(parts, block.Parts...)
	}
	return v
}

type sourceCitation struct{ Location, ExcerptLocation, Text, URL, LinkLabel string }

func (v secView) sourceCitations(ids []string, evidence []SectionEvidence, excerpts bool) []sourceCitation {
	var list []sourceCitation
	for _, id := range ids {
		for _, e := range evidence {
			if e.ID != id {
				continue
			}
			excerpt := e.Excerpt
			if !excerpts {
				excerpt = nil
			}
			list = append(list, v.citeSource(e.Path, e.StartLine, e.EndLine, excerpt))
			break
		}
	}
	return list
}

func (v secView) citeSource(path string, start, end int, excerpt *sourceexcerpt.Excerpt) sourceCitation {
	s := citeSource(path, start, end, excerpt)
	if v.Summary {
		s.URL = snapshotBrowserURL(v.R, path, start, end)
		s.LinkLabel = v.L.Label("opening.open_source")
	}
	return s
}

// snapshotBrowserURL derives only a link to the stored Git snapshot. URLs in
// model content are never used as navigation targets.
func snapshotBrowserURL(r Report, path string, start, end int) string {
	if !r.Export.IncludeCode || r.Project == nil || r.Inputs.SourceKind != string(domain.SourceGit) || r.Inputs.CommitSHA == nil ||
		!domain.ValidObjectID(*r.Inputs.CommitSHA) || domain.ValidateEvidencePath(path) != nil || start < 1 || end < start {
		return ""
	}
	host, err := domain.RepositoryHost(r.Project.Source)
	if err != nil {
		return ""
	}
	repo := domain.RepositoryPath(r.Project.Source)
	if pr := r.PullRequest; pr != nil && pr.HeadRepository != "" && !strings.EqualFold(pr.HeadRepository, repo) {
		return ""
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "https://" + host + "/" + repo + "/blob/" + *r.Inputs.CommitSHA + "/" + strings.Join(segments, "/") +
		"#L" + strconv.Itoa(start) + "-L" + strconv.Itoa(end)
}

func sourceRange(path string, start, end int) string {
	return path + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

func citeSource(path string, start, end int, excerpt *sourceexcerpt.Excerpt) sourceCitation {
	s := sourceCitation{Location: sourceRange(path, start, end)}
	if excerpt != nil {
		s.ExcerptLocation = sourceRange(path, excerpt.StartLine, excerpt.EndLine)
		s.Text = excerpt.Text
	}
	return s
}

// A fence longer than any backtick run in the snapshot keeps its lines literal.
func mdCitations(p func(...string), sources []sourceCitation) {
	for _, x := range sources {
		if x.URL == "" {
			p(mdCode(x.Location))
		} else {
			p(mdCode(x.Location), " · [", mdText(x.LinkLabel), "](<", x.URL, ">)")
		}
		p()
		if x.Text != "" {
			if x.ExcerptLocation != "" && x.ExcerptLocation != x.Location {
				p(mdCode(x.ExcerptLocation))
				p()
			}
			fence := strings.Repeat("`", max(3, longestBacktickRun(x.Text)+1))
			p(fence)
			p(x.Text)
			p(fence)
			p()
		}
	}
}
