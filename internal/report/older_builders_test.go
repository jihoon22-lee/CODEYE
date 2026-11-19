package report

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// A report stored by an earlier builder stays readable and renders after the
// builder moves on: testdata/report-slices-builder7.json is
// examples/report-slices.json as builder 7 wrote it (a ledger without
// classes, a schema 0.1 scope artifact, slices s001..s007).
func TestBuilderSevenSliceReportStillReads(t *testing.T) {
	r := storedExample(t, "testdata/report-slices-builder7.json")
	if r.Builder.Version != "7" || r.CodeReview == nil || r.CodeReview.Ledger == nil {
		t.Fatalf("builder %s ledger %v", r.Builder.Version, r.CodeReview)
	}
	if ledgerClasses(r.CodeReview.Ledger) || r.CodeReview.Ledger.PrimarySlotsBudgetExhausted != 0 {
		t.Fatalf("builder-7 ledger with classes: %+v", r.CodeReview.Ledger)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	digest := Digest(mustMarshal(t, r))
	html, err := RenderHTML(r, digest, Korean)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range [][]byte{RenderMarkdown(r, digest, Korean), html} {
		if bytes.Contains(out, []byte("%!")) {
			t.Fatal("format error in a builder-7 rendering")
		}
	}
}

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
	md := RenderMarkdown(r, digest, Korean)
	if bytes.Contains(md, []byte("%!")) {
		t.Fatal("format error in the rendering")
	}
	t.Logf("builder %s: markdown %d lines", r.Builder.Version, strings.Count(string(md), "\n"))
	if out := os.Getenv("CODEYE_REAL_REPORT_OUT"); out != "" {
		if err := os.WriteFile(out, md, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func mustMarshal(t *testing.T, r Report) []byte {
	t.Helper()
	b, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
