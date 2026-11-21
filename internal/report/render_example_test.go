package report

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestCitedDocumentExamples(t *testing.T) {
	var cases []struct {
		Name, Statement, Text string
		Example               *documentExample
	}
	raw, err := os.ReadFile("testdata/document_examples.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evidence := SectionEvidence{Kind: "document", Path: "README.md", StartLine: 1, EndLine: strings.Count(tc.Text, "\n") + 1}
			evidence.Excerpt = &sourceexcerpt.Excerpt{StartLine: 1, EndLine: evidence.EndLine, Text: tc.Text}
			got := citedDocumentExample(tc.Statement, &evidence)
			if !reflect.DeepEqual(got, tc.Example) {
				t.Fatalf("example = %+v, want %+v", got, tc.Example)
			}
			evidence.EndLine--
			if got := citedDocumentExample(tc.Statement, &evidence); got != nil {
				t.Fatalf("example outside cited lines: %+v", got)
			}
		})
	}
}

func TestDocumentExampleKeepsCitationAndExportPolicy(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths = nil, nil
	c := r.Documents.Comparisons[0]
	c.Status = "unknown"
	c.Statement = "preview('raw') returns '<img src=x>'."
	r.Documents.Comparisons = []DocComparison{c}
	for i := range r.Documents.Evidence {
		e := &r.Documents.Evidence[i]
		if e.Kind == "document" && e.ID == c.DocumentEvidenceIDs[0] {
			e.Excerpt = &sourceexcerpt.Excerpt{StartLine: e.StartLine, EndLine: e.StartLine + 3,
				Text: "```js\npreview('raw');\n//=> '<img src=x>'\n```"}
		}
	}
	for _, detail := range []string{DetailSummary, DetailFull} {
		for _, include := range []bool{true, false} {
			p := Project(r, include)
			raw, err := RenderHTMLAt(p, "0", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			html := string(raw)
			markdown := string(RenderMarkdownAt(p, "0", Korean, detail))
			if !include {
				if strings.Contains(html, "설명서 예시") || strings.Contains(markdown, "설명서 예시") ||
					strings.Contains(html, "preview(") || strings.Contains(markdown, "preview(") {
					t.Fatal("code-free export exposed the example")
				}
				continue
			}
			_, end, _ := strings.Cut(html, "<p class=\"report-document-example\">")
			example, citation, _ := strings.Cut(end, "</p>")
			if strings.Count(html, "설명서 예시") != 1 || !strings.Contains(example, "→ <code>&#39;&lt;img src=x&gt;&#39;</code>") ||
				!strings.HasPrefix(strings.TrimSpace(citation), "<details class=\"report-excerpt\">") {
				t.Fatalf("%s HTML shows the example without a safely escaped result and nearby citation: %q", detail, example)
			}
			if strings.Count(markdown, "설명서 예시") != 1 || !strings.Contains(markdown, "**설명서 예시** `preview('raw')` → `'<img src=x>'`\n\n`README.md:") {
				t.Fatalf("%s Markdown lost the literal pair or its citation", detail)
			}
		}
	}
}
