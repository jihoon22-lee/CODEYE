package report

import (
	"net/url"
	"regexp"
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
		if strings.TrimSpace(comparison.Statement) == strings.TrimSpace(contract.Statement) &&
			displayDocumentScope(comparison.AppliesTo) == displayDocumentScope(contract.AppliesTo) &&
			!slices.ContainsFunc(contract.EvidenceIDs, func(id string) bool { return !slices.Contains(comparison.DocumentEvidenceIDs, id) }) {
			return true
		}
	}
	return false
}

// withInsights adds the same evidence-backed assessments to both export layouts.
func withInsights(v secView) secView {
	for i := range v.Sections {
		block := &v.Sections[i]
		switch block.ID {
		case SectionDocuments:
			lead, records := readingLead(block.Parts)
			block.Parts = []secPart{lead}
			if reading := v.documentReading(v.R.Documents); reading != nil {
				block.Parts = append(block.Parts, secPart{Reading: reading})
			}
			block.Parts = append(block.Parts, secPart{Fold: v.L.Label("docs.reading_discovery"), Parts: records})
		case SectionCodeReview:
			block.Parts = v.codeReviewReadingOrder(block.Parts)
		}
	}
	return v
}

func readingLead(parts []secPart) (secPart, []secPart) {
	records := slices.Clone(parts)
	lead := secPart{Paras: records[0].Paras[:1]}
	records[0].Paras = records[0].Paras[1:]
	return lead, records
}

type workflowReadingView struct {
	Heading, Context string
	Items            []secItem
}

func (v secView) workflowCitations(w *RelatedWorkflow, evidence []SectionEvidence) *workflowReadingView {
	if w == nil {
		return nil
	}
	ids := w.evidenceIDs()
	items := make([]secItem, 0, len(ids))
	for i, role := range []string{"read", "change", "test"} {
		sources := v.sourceCitations([]string{ids[i]}, evidence)
		if len(sources) > 0 {
			items = append(items, secItem{Label: v.L.Label("workflow." + role), Sources: sources})
		}
	}
	return &workflowReadingView{Heading: v.L.Label("workflow.heading"), Context: v.L.Label("workflow.context"), Items: items}
}

func mdWorkflowCitations(p func(...string), reading *workflowReadingView) {
	if reading == nil {
		return
	}
	p("**", mdText(reading.Heading), "**")
	p()
	p(mdText(reading.Context))
	p()
	for _, item := range reading.Items {
		p(mdText(item.Label))
		mdCitations(p, item.Sources)
	}
}

func comparisonAnchor(i int) string { return "document-comparison-" + strconv.Itoa(i+1) }
func strengthAnchor(i int) string   { return "code-strength-" + strconv.Itoa(i+1) }

type sourceCitation struct {
	File, Range, Location            string
	ID, Anchor, Jump                 string
	ExcerptLocation, Text            string
	URL, LinkLabel                   string
	ActionLabel, StoredLabel         string
	Example                          *documentExample
	ExampleLabel                     string
	StartLine, EndLine, ExcerptStart int
}

type sourcePreview struct {
	Location string
	Lines    []sourcePreviewLine
}

type sourcePreviewLine struct {
	Number int
	Text   string
	Focus  bool
}

func (s sourceCitation) Preview() *sourcePreview {
	return s.previewFor("")
}

var previewIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*$`)

func (s sourceCitation) previewFor(title string) *sourcePreview {
	if s.Text == "" || s.ExcerptStart < 1 {
		return nil
	}
	lines := strings.Split(s.Text, "\n")
	focus := previewSymbolLine(lines, title)
	if focus < 0 && s.StartLine >= s.ExcerptStart && s.EndLine < s.ExcerptStart+len(lines) && s.EndLine-s.StartLine < 5 {
		focus = s.StartLine - s.ExcerptStart
	}
	first, last := 0, len(lines)
	if focus >= 0 {
		first = max(0, min(focus-1, len(lines)-5))
		last = min(first+5, len(lines))
	}
	preview := &sourcePreview{Location: sourceRange(s.File, s.ExcerptStart+first, s.ExcerptStart+last-1)}
	for index := first; index < last; index++ {
		preview.Lines = append(preview.Lines, sourcePreviewLine{s.ExcerptStart + index, lines[index], index == focus})
	}
	return preview
}

func previewSymbolLine(lines []string, title string) int {
	for _, part := range inlineCodeParts(title) {
		if !part.Code || !previewIdentifier.MatchString(part.Text) {
			continue
		}
		token := regexp.MustCompile(`\b` + part.Text + `\b`)
		found := -1
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "# ") || !token.MatchString(line) {
				continue
			}
			if found >= 0 {
				found = -1
				break
			}
			found = i
		}
		if found >= 0 {
			return found
		}
	}
	return -1
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
	s := newSourceCitation(v.L, e.Kind, e.Path, e.StartLine, e.EndLine, e.Excerpt)
	s.ID = e.ID
	s.URL = snapshotBrowserURL(v.R, e.Path, e.StartLine, e.EndLine)
	s.LinkLabel = v.L.Label("opening.open_source")
	return s
}

func newSourceCitation(l Locale, kind, path string, start, end int, excerpt *sourceexcerpt.Excerpt) sourceCitation {
	s := citeSource(path, start, end, excerpt)
	s.ActionLabel = l.Label("evidence.view_code")
	switch kind {
	case "document":
		s.ActionLabel = l.Label("evidence.view_document")
	case "test":
		s.ActionLabel = l.Label("evidence.view_test_code")
	case "manifest":
		s.ActionLabel = l.Label("evidence.view_manifest")
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
	s := sourceCitation{File: path, Range: strconv.Itoa(start) + "-" + strconv.Itoa(end), Location: sourceRange(path, start, end), StartLine: start, EndLine: end}
	if excerpt != nil {
		s.ExcerptStart = excerpt.StartLine
		s.ExcerptLocation = sourceRange(path, excerpt.StartLine, excerpt.EndLine)
		s.Text = excerpt.Text
	}
	return s
}

// A fence longer than any backtick run in the snapshot keeps its lines literal.
func mdCitations(p func(...string), sources []sourceCitation) {
	for _, x := range sources {
		mdReadingAnchor(p, x.Anchor)
		if x.Example != nil {
			p("**", mdText(x.ExampleLabel), "** ", mdCodeSpan(x.Example.Call), " → ", mdCodeSpan(x.Example.Output))
			p()
		}
		if x.Jump != "" {
			if x.URL != "" {
				p("[", mdCode(x.Location), "](#", x.Jump, ") · [", mdText(x.LinkLabel), "](<", x.URL, ">)")
			} else {
				p("[", mdCode(x.Location), "](#", x.Jump, ")")
			}
		} else if x.URL == "" {
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
