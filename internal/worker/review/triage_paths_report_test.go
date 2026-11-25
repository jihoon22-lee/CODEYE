package review

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// TC-303: the diagnostics fold says how many of the
// diagnostics are on paths that are not shipped code and how many on shipped
// code, in the fold as a whole and in each triage bucket, in Markdown and
// HTML, counted from the observations' stored path class (the overview count
// is checked with the report builder's tests). A revision before
// builder 23 renders without the line.
func TestTriageFoldSplitsDiagnosticsByPath(t *testing.T) {
	run := runTriageReport(t, triageReviewScript(t), 0, Budget{})
	r := run.report
	d := r.CodeReview.DiagnosticTriage
	if d == nil {
		t.Fatal("no diagnostic triage")
	}
	// b.go and c.go stand for test files: the report's own observations
	// carry the class, so the fixture's source files stay as they are.
	for i := range r.Observations {
		if o := &r.Observations[i]; o.Location != nil && (o.Location.Path == "b.go" || o.Location.Path == "c.go") {
			o.PathClass = record.PathTest
		}
	}
	bucketOf := map[string]string{}
	for _, g := range d.Groups {
		for _, id := range g.ObservationIDs {
			bucketOf[id] = g.Bucket
		}
	}
	type split struct{ off, shipped int }
	want := map[string]*split{"": {}, report.TriageAttention: {}, report.TriageRefuted: {}, report.TriageHarmless: {}}
	total := split{}
	for _, o := range r.Observations {
		if o.Suppressed || o.Metric != nil {
			continue
		}
		s := want[bucketOf[o.ID]]
		if o.PathClass != "" {
			s.off++
			total.off++
		} else {
			s.shipped++
			total.shipped++
		}
	}
	if total.off == 0 || total.shipped == 0 {
		t.Fatalf("fixture split %+v", total)
	}
	line := func(s split) string {
		return fmt.Sprintf("경로별: 시험·예제 등 배포 코드가 아닌 경로 %d건, 배포 코드 %d건.", s.off, s.shipped)
	}
	// Markdown escapes punctuation; the text is what is left without it.
	md := strings.ReplaceAll(string(report.RenderMarkdownAt(r, "", report.Korean, report.DetailSummary)), `\`, "")
	html, err := report.RenderHTMLAt(r, "", report.Korean, report.DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, line(total)) || !strings.Contains(string(html), line(total)) {
		t.Fatalf("no fold total %q", line(total))
	}
	for b, s := range want {
		if s.off == 0 {
			continue
		}
		if !strings.Contains(md, line(*s)) || !strings.Contains(string(html), line(*s)) {
			t.Fatalf("bucket %q lacks %q", b, line(*s))
		}
	}
	if !strings.Contains(string(html), `class="report-diag-paths"`) {
		t.Fatal("HTML lacks the paths row")
	}
	// A revision before builder 23 has no classes and no line: its render
	// does not depend on the classes at all.
	old := r
	old.Builder.Version = "22"
	oldMD := report.RenderMarkdownAt(old, "", report.Korean, report.DetailSummary)
	oldHTML, err := report.RenderHTMLAt(old, "", report.Korean, report.DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	plain := old
	plain.Observations = slices.Clone(old.Observations)
	for i := range plain.Observations {
		plain.Observations[i].PathClass = ""
	}
	plainMD := report.RenderMarkdownAt(plain, "", report.Korean, report.DetailSummary)
	plainHTML, err := report.RenderHTMLAt(plain, "", report.Korean, report.DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(oldMD), "경로별") || !bytes.Equal(oldMD, plainMD) || !bytes.Equal(oldHTML, plainHTML) {
		t.Fatal("a builder 22 render depends on the path classes")
	}
}
