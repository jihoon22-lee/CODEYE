package report

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func displayDocumentScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "." {
		return ""
	}
	return scope
}

func scopedDocumentText(scope, text string) string {
	if scope = displayDocumentScope(scope); scope != "" {
		return "[" + scope + "] " + text
	}
	return text
}

func comparisonCoversContract(comparisons []DocComparison, contract DocContract) bool {
	if openingText(contract.Statement, "") == "" {
		return false
	}
	for _, comparison := range comparisons {
		if comparison.Statement == contract.Statement && comparison.AppliesTo == contract.AppliesTo &&
			!slices.ContainsFunc(contract.EvidenceIDs, func(id string) bool { return !slices.Contains(comparison.DocumentEvidenceIDs, id) }) {
			return true
		}
	}
	return false
}

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
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight."+q.Aspect) + " · " + l.Label("insight."+q.Assessment), Paras: []string{q.Statement}, Sources: v.sourceCitations(q.EvidenceIDs, doc.Evidence)})
			}
			for i, c := range doc.Comparisons {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Anchor: comparisonAnchor(i), Heading: l.Label("insight.comparison") + " · " + l.Label("insight."+c.Status), Paras: []string{c.Statement}, List: v.comparisonDetails(c, doc.Evidence)})
			}
		case SectionCodeReview:
			for i, s := range v.R.CodeReview.Strengths {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Anchor: strengthAnchor(i), Heading: l.Label("insight.strength") + " · " + s.Title, Paras: []string{s.Benefit}, Sources: v.sourceCitations(s.EvidenceIDs, v.R.CodeReview.Evidence)})
			}
		}
		block.Parts = append(parts, block.Parts...)
	}
	return v
}

func comparisonAnchor(i int) string { return "document-comparison-" + strconv.Itoa(i+1) }
func strengthAnchor(i int) string   { return "code-strength-" + strconv.Itoa(i+1) }

type sourceCitation struct {
	File, Range, Location    string
	ExcerptLocation, Text    string
	URL, LinkLabel           string
	ActionLabel, StoredLabel string
}

func (s sourceCitation) Brief() sourceCitation {
	if s.URL != "" {
		s.Text = ""
	}
	return s
}

func (v secView) sourceCitations(ids []string, evidence []SectionEvidence) []sourceCitation {
	var list []sourceCitation
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		for _, e := range evidence {
			if e.ID != id {
				continue
			}
			source := v.citeSectionEvidence(e)
			if !seen[source.Location] {
				list = append(list, source)
				seen[source.Location] = true
			}
			break
		}
	}
	return list
}

func (v secView) citeSectionEvidence(e SectionEvidence) sourceCitation {
	return v.citeEvidence(e.Kind, e.Path, e.StartLine, e.EndLine, e.Excerpt)
}

func newSourceCitation(l Locale, kind, path string, start, end int, excerpt *sourceexcerpt.Excerpt) sourceCitation {
	s := citeSource(path, start, end, excerpt)
	s.ActionLabel = l.Label("evidence.view_code")
	if kind == "document" {
		s.ActionLabel = l.Label("evidence.view_document")
	}
	s.StoredLabel = l.Label("evidence.stored_excerpt")
	return s
}

func (v secView) citeEvidence(kind, path string, start, end int, excerpt *sourceexcerpt.Excerpt) sourceCitation {
	s := newSourceCitation(v.L, kind, path, start, end, excerpt)
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
	s := sourceCitation{File: path, Range: strconv.Itoa(start) + "-" + strconv.Itoa(end), Location: sourceRange(path, start, end)}
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
