package report

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestDocumentReadingUsesOneStoredWindowAndKeepsExactRoutes(t *testing.T) {
	r := exampleReport(t)
	d := r.Documents
	document := func(id, path string, start, end int, lines string) SectionEvidence {
		return SectionEvidence{ID: id, Kind: "document", Path: path, StartLine: start, EndLine: end, BlobSHA256: "snapshot",
			Excerpt: &sourceexcerpt.Excerpt{StartLine: start, EndLine: end, Text: lines}}
	}
	d.Evidence = append(d.Evidence, document("wide", "GUIDE.md", 1, 3, "opening\ncanonical marker\nclosing"),
		document("narrow", "GUIDE.md", 2, 2, "canonical marker"),
		document("details", "docs/config.md", 4, 4, "configuration marker"))
	d.Comparisons = []DocComparison{
		{Statement: "파일을 읽는다", AppliesTo: ".", Status: "unknown", Reason: "코드 근거가 부족하다", DocumentEvidenceIDs: []string{"wide", "details"}},
		{Statement: "설정을 읽는다", AppliesTo: "svc", Status: "unknown", Reason: "확인하지 못한 분기가 있다", DocumentEvidenceIDs: []string{"narrow"}},
	}
	d.Quality = []record.DocumentQuality{{Aspect: "clarity", Assessment: "weakness", Statement: "실행 조건을 설명해야 한다", EvidenceIDs: []string{"narrow"}}}
	d.Contracts, d.Conflicts, d.Questions, d.InstructionNotes = nil, nil, nil, nil
	for _, include := range []bool{true, false} {
		report := Project(r, include)
		for _, level := range []string{DetailSummary, DetailFull} {
			page, err := RenderHTMLAt(report, "test", Korean, level)
			if err != nil {
				t.Fatal(err)
			}
			checkInPageLinks(t, page)
			_, html, _ := strings.Cut(string(page), `data-section="documents"`)
			html, _, _ = strings.Cut(html, `data-section="static_analysis"`)
			md := string(RenderMarkdownAt(report, "test", Korean, level))
			_, md, _ = strings.Cut(md, "\n## 문서 분석\n")
			md, _, _ = strings.Cut(md, "\n## 정적 분석\n")
			for _, body := range []string{html, md} {
				for _, want := range []string{"GUIDE.md:1-3", "GUIDE.md:2-2", "docs/config.md:4-4", "문서 품질",
					"문서와 구현", `href="#document-comparison-1"`} {
					if strings.HasPrefix(want, "href=") && body == md {
						want = "](#document-comparison-1)"
					}
					if !strings.Contains(body, want) {
						t.Fatalf("level %s, includes code %t: no %q", level, include, want)
					}
				}
				count := 0
				if include {
					count = 1
				}
				if strings.Count(body, "canonical marker") != count || strings.Contains(body, "파일을 읽는다") != include {
					t.Fatalf("level %s, includes code %t: wrong canonical excerpt or model statement", level, include)
				}
			}
		}
	}
}

func TestCodeReadingKeepsSupportedActionsBeforeFoldedObservations(t *testing.T) {
	r := exampleReport(t)
	var sampled Finding
	for _, f := range r.Findings {
		if f.Validation == ValidationSupported {
			sampled = f
			break
		}
	}
	low, high, pending := sampled, sampled, sampled
	low.ID, low.Title, low.Severity = "reading-low", "낮은 우선순위 발견", "low"
	high.ID, high.Title, high.Severity = "reading-high", "높은 우선순위 발견", "high"
	pending.ID, pending.Title, pending.Severity, pending.Validation = "reading-pending", "추가 확인이 필요한 발견", "critical", ValidationInconclusive
	r.Findings = append(r.Findings, low, pending, high)
	r.CodeReview.FindingIDs = []string{low.ID, pending.ID, high.ID}
	strength := r.CodeReview.Strengths[0]
	strength.Title = "기록된 구현 관찰"
	r.CodeReview.Strengths = append(r.CodeReview.Strengths, strength, strength)
	for _, level := range []string{DetailSummary, DetailFull} {
		html, err := RenderHTMLAt(r, "test", Korean, level)
		if err != nil {
			t.Fatal(err)
		}
		_, codeHTML, _ := strings.Cut(string(html), `data-section="code_review"`)
		codeHTML, _, _ = strings.Cut(codeHTML, `data-section="gaps"`)
		_, codeMD, _ := strings.Cut(string(RenderMarkdownAt(r, "test", Korean, level)), "\n## 코드 분석")
		codeMD, _, _ = strings.Cut(codeMD, "\n## 분석 공백")
		for _, content := range []string{codeHTML, codeMD} {
			previous := -1
			for _, want := range []string{high.Title, low.Title, Korean.Label("review.reading_strengths"), strength.Title, pending.Title} {
				index := strings.Index(content, want)
				if index <= previous {
					t.Fatalf("level %s: observations or findings are out of order around %q", level, want)
				}
				previous = index
			}
			if strings.Count(content, strength.Title) != 1 || !strings.Contains(content, "<details") {
				t.Fatalf("level %s: repeated or unfolded observation", level)
			}
		}
	}
}

func TestEmptyCodeReadingUsesAcceptedCodeEvidence(t *testing.T) {
	r := exampleReport(t)
	r.CodeReview = &CodeReviewSection{AcceptedCalls: 1}
	v := secView{view: view{R: r, L: Korean}}
	want := Korean.Label("review.reading_no_evidence")
	if notes := v.reviewFindingParts(nil)[0].Paras; len(notes) != 1 || notes[0] != want {
		t.Fatalf("global call count claimed a code result: %v", notes)
	}
	r.CodeReview.Roles = []AIReviewTask{{Key: "review.investigate", Result: "accepted"}}
	want = Korean.Label("review.reading_none")
	if notes := v.reviewFindingParts(nil)[0].Paras; len(notes) != 1 || notes[0] != want {
		t.Fatalf("accepted code investigation did not report its empty action list: %v", notes)
	}
}
