package report

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// CODEYE_REAL_REPORT names a stored report.json of a real run (kept outside
// the repository). It must still read and render; the test logs the line
// count of each Markdown form.
func TestRealReportRenders(t *testing.T) {
	path := os.Getenv("CODEYE_REAL_REPORT")
	if path == "" {
		t.Skip("CODEYE_REAL_REPORT not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest(raw)
	for _, detail := range []string{DetailSummary, DetailFull} {
		md := RenderMarkdownAt(r, digest, Korean, detail)
		html, err := RenderHTMLAt(r, digest, Korean, detail)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(md, []byte("%!")) || bytes.Contains(html, []byte("%!")) {
			t.Fatalf("format error in the %s rendering", detail)
		}
		checkInPageLinks(t, html)
		checkTableWidths(t, string(md))
		t.Logf("builder %s, %s: markdown %d lines, html %d bytes", r.Builder.Version, detail, strings.Count(string(md), "\n"), len(html))
		if out := os.Getenv("CODEYE_REAL_REPORT_OUT"); out != "" {
			if err := os.WriteFile(out+"."+detail+".md", md, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
