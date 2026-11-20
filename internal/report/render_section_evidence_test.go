package report

import (
	"strings"
	"testing"
)

func TestSectionEvidenceUsesContextualCitations(t *testing.T) {
	const source = "https://github.com/example/billing.git"
	sha := strings.Repeat("a", 40)
	for _, mode := range []string{"local", "git", "code-free"} {
		t.Run(mode, func(t *testing.T) {
			r := exampleReport(t)
			if mode != "local" {
				r.Inputs.SourceKind, r.Inputs.CommitSHA, r.Project.Source = "git", &sha, source
			}
			if mode == "code-free" {
				r = Project(r, false)
			}
			for _, detail := range []string{DetailSummary, DetailFull} {
				page, err := RenderHTMLAt(r, "test", Korean, detail)
				if err != nil {
					t.Fatal(err)
				}
				checkInPageLinks(t, page)
				md := string(RenderMarkdownAt(r, "test", Korean, detail))
				for _, section := range []struct {
					key, label, next string
					evidence         []SectionEvidence
				}{
					{SectionEnvironment, "환경 분석", "문서 분석", r.Environment.Evidence},
					{SectionDocuments, "문서 분석", "정적 분석", r.Documents.Evidence},
				} {
					first := `<section id="` + section.key + `" data-section="`
					_, html, ok := strings.Cut(string(page), first)
					if !ok {
						t.Fatalf("%s: section %s missing in HTML", detail, section.key)
					}
					html, _, _ = strings.Cut(html, `<section id="`)
					_, markdown, ok := strings.Cut(md, "\n## "+section.label+"\n")
					if !ok {
						t.Fatalf("%s: section %s missing in Markdown", detail, section.key)
					}
					markdown, _, _ = strings.Cut(markdown, "\n## "+section.next+"\n")
					for _, e := range section.evidence {
						location := sourceRange(e.Path, e.StartLine, e.EndLine)
						if !strings.Contains(html, location) || !strings.Contains(markdown, location) {
							t.Errorf("%s/%s: missing inline citation %q", detail, section.key, location)
						}
						for _, raw := range []string{e.ID, short(e.BlobSHA256)} {
							if strings.Contains(html, raw) || strings.Contains(markdown, raw) {
								t.Errorf("%s/%s: technical evidence data leaked into the section: %q", detail, section.key, raw)
							}
						}
						link := "https://github.com/example/billing/blob/" + sha + "/" + e.Path
						if strings.Contains(html, link) != (mode == "git") {
							t.Errorf("%s/%s: wrong source link visibility for %s", detail, section.key, mode)
						}
					}
					if strings.Contains(html, "근거 ID") || strings.Contains(html, "blob sha256") ||
						(strings.Contains(html, `class="report-excerpt"`) == (mode == "code-free")) {
						t.Errorf("%s/%s: wrong evidence presentation for %s", detail, section.key, mode)
					}
				}
			}
		})
	}
}

func TestUnselectedReviewReasonStaysInDetails(t *testing.T) {
	r := exampleReport(t)
	text := Korean.Code("no_review_scope")
	html, err := RenderHTMLAt(r, "test", Korean, DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	before, details, ok := strings.Cut(string(html), `<section id="code_review"`)
	if !ok || strings.Contains(before, text) || !strings.Contains(details, text) || strings.Contains(details, "보낼 파일이 남지 않아") {
		t.Fatal("the localized review skip reason must appear only in analysis details")
	}
}
