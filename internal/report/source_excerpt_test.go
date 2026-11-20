package report

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestModelReferencesSharingAStartUseTheirOwnCitationEnd(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	sha, file := strings.Repeat("a", 64), "route.ts"
	lines := make([]string, 24)
	for i := range lines {
		lines[i] = fmt.Sprintf("// route line %d", i+1)
	}
	lines[12], lines[21] = "  const parsed = await input.json();", "  return Response.json(result, { status: 500 });"
	want := func(end int) sourceexcerpt.Excerpt {
		return sourceexcerpt.Excerpt{StartLine: 1, EndLine: end, Text: strings.Join(lines[:end], "\n")}
	}
	finding := func(id string, end int) Finding {
		return Finding{ID: id, Evidence: []Evidence{{
			Kind: "code", Location: &Location{Path: file, StartLine: 1, EndLine: end}, BlobSHA256: sha,
		}}}
	}
	r.Findings = []Finding{finding("model", 24), finding("tool", 1)}
	r.CodeReview.FindingIDs = []string{"model"}
	r.Documents.Evidence = []SectionEvidence{{Kind: "code", Path: file, BlobSHA256: sha, StartLine: 1, EndLine: 14}}
	key := sourceexcerpt.Key{Path: file, SHA256: sha, Line: 1}
	attachExcerpts(&r, map[sourceexcerpt.Key][]sourceexcerpt.Excerpt{key: {want(24), want(6)}}, false)
	for _, tc := range []struct {
		excerpt *sourceexcerpt.Excerpt
		end     int
	}{{r.Findings[0].Evidence[0].Excerpt, 24}, {r.Findings[1].Evidence[0].Excerpt, 6}, {r.Documents.Evidence[0].Excerpt, 14}} {
		if tc.excerpt == nil || *tc.excerpt != want(tc.end) {
			t.Errorf("want exact excerpt through line %d, got %+v", tc.end, tc.excerpt)
		}
	}
	free := Project(r, false)
	if free.Findings[0].Evidence[0].Excerpt != nil || free.Findings[1].Evidence[0].Excerpt != nil || free.Documents.Evidence[0].Excerpt != nil {
		t.Fatal("source text survived code-free projection")
	}
	if got := preferredExcerpt([]sourceexcerpt.Excerpt{want(6)}, 1, 24, true, true); got == nil || *got != want(6) {
		t.Fatal("the historical artifact should keep its originally stored lines")
	}
	if want(24).ValidDiagnostic(1) || want(24).ValidCitation(1, 23) {
		t.Fatal("a long model excerpt was accepted as a diagnostic or beyond a citation")
	}
}
