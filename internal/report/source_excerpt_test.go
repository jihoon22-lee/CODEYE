package report

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
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
	attachExcerpts(&r, map[sourceexcerpt.Key][]sourceexcerpt.Excerpt{key: {want(24), want(6)}}, nil, false)
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

func TestMetricEvidenceExcerptsAppearOnlyInCodeDetail(t *testing.T) {
	r := metricsReport(t)
	name := "var snapshotMetricExample = true"
	windows := map[sourceexcerpt.Key][]sourceexcerpt.Excerpt{}
	for _, o := range r.Observations {
		if o.Metric == nil || o.Metric.Counterpart == nil {
			continue
		}
		for _, loc := range []*ObservationLocation{o.Location, o.Metric.Counterpart} {
			key := sourceexcerpt.Key{Path: loc.Path, SHA256: loc.BlobSHA256, Line: loc.StartLine}
			windows[key] = []sourceexcerpt.Excerpt{{StartLine: loc.StartLine, EndLine: loc.StartLine, Text: name}}
		}
	}
	attachExcerpts(&r, windows, nil, false)
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Observations {
		if o.Metric != nil && o.Metric.Counterpart != nil && (o.Metric.Excerpt == nil || o.Metric.CounterpartExcerpt == nil) {
			t.Fatalf("both verified locations should have snippets: %+v", o.Metric)
		}
	}
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	for _, o := range free.Observations {
		if o.Metric != nil && (o.Metric.Excerpt != nil || o.Metric.CounterpartExcerpt != nil) {
			t.Fatal("metric excerpt leaked into a code-free report")
		}
	}
	full, err := RenderHTML(r, strings.Repeat("a", 64), Korean)
	if err != nil || !strings.Contains(string(full), name) || !strings.Contains(string(full), "metrics-detail") {
		t.Fatalf("expected source inside inspectable details, got %v", err)
	}
	summary, err := RenderHTMLAt(r, strings.Repeat("a", 64), Korean, DetailSummary)
	if err != nil || strings.Contains(string(summary), name) {
		t.Fatalf("source appeared in the summary, render error %v", err)
	}
}

func TestLongDocumentExcerptPreservesCitedTailOnlyForDocumentEvidence(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	sha, file := strings.Repeat("a", 64), "docs/REPORT.md"
	body, err := os.ReadFile("../worker/sourceexcerpt/testdata/report-citation.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(body), "\n", 17)
	if len(lines) != 17 || lines[15] != "" {
		t.Fatal("fixture must retain the blank line at the end of the citation")
	}
	text := strings.Join(lines[:16], "\n")
	if record.JSONLen(text) != 1971 {
		t.Fatalf("unexpected encoded citation size: %d", record.JSONLen(text))
	}
	full := sourceexcerpt.Excerpt{StartLine: 1, EndLine: 16, Text: text}
	short := sourceexcerpt.Excerpt{StartLine: 1, EndLine: 13, Text: strings.Join(lines[:13], "\n")}
	if full.ValidCitation(1, 16) || !full.ValidDocumentCitation(1, 16) {
		t.Fatal("the test must distinguish document and general source budgets")
	}
	key := sourceexcerpt.Key{Path: file, SHA256: sha, Line: 1}
	r.Documents.Evidence = []SectionEvidence{
		{Kind: "document", Path: file, BlobSHA256: sha, StartLine: 1, EndLine: 16},
		{Kind: "document", Path: file, BlobSHA256: sha, StartLine: 1, EndLine: 17},
		{Kind: "code", Path: file, BlobSHA256: sha, StartLine: 1, EndLine: 16},
	}
	attachExcerpts(&r, map[sourceexcerpt.Key][]sourceexcerpt.Excerpt{key: {short}},
		map[sourceexcerpt.Key][]sourceexcerpt.Excerpt{key: {full}}, false)
	for i, want := range []sourceexcerpt.Excerpt{full, short, short} {
		got := r.Documents.Evidence[i].Excerpt
		if got == nil || *got != want {
			t.Errorf("citation %d range = %+v, want %d–%d", i, got, want.StartLine, want.EndLine)
		}
	}
	if !validReportCitation(full, 1, 16, "document", BuilderVersion) || validReportCitation(full, 1, 16, "document", "17") ||
		validReportCitation(full, 1, 16, "code", BuilderVersion) || validReportCitation(full, 1, 17, "document", BuilderVersion) {
		t.Fatal("extended document scope or builder bound is invalid")
	}
	free := Project(r, false)
	if free.Documents.Evidence[0].Excerpt != nil || free.Documents.Evidence[0].EndLine != 16 {
		t.Fatal("code-free export should retain the document location only")
	}
}
