package gitleaks

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
)

// TestCapturedColumnsAreOffByOne records why observations keep no columns
// (LimColumnsNotRecorded): in the bytes captured from gitleaks 8.30.1, the
// byte columns of a match on the first line of a file are exact, and on
// any later line both start and end are one too high. The expected place
// is where the fixture wrote the value (same length as the placeholder).
func TestCapturedColumnsAreOffByOne(t *testing.T) {
	files := fixtureFiles(anypacktest.Placeholder)
	rec := anypacktest.LoadCapture(t, "findings")
	seen := 0
	for _, line := range bytes.Split(rec.Stdout, []byte("\n")) {
		var r struct {
			File      *string `json:"file"`
			StartLine int     `json:"start_line"`
			StartCol  int     `json:"start_column"`
			EndCol    int     `json:"end_column"`
		}
		if len(line) == 0 || json.Unmarshal(line, &r) != nil || r.File == nil {
			continue
		}
		content, ok := files[*r.File]
		if !ok {
			t.Fatalf("captured file %q not in the fixture", *r.File)
		}
		lines := bytes.Split(content, []byte("\n"))
		at := bytes.Index(lines[r.StartLine-1], []byte(anypacktest.Placeholder))
		if at < 0 {
			t.Fatalf("%s:%d holds no value", *r.File, r.StartLine)
		}
		start, end := at+1, at+len(anypacktest.Placeholder) // 1-based, inclusive
		off := 0
		if r.StartLine > 1 {
			off = 1
		}
		if r.StartCol != start+off || r.EndCol != end+off {
			t.Fatalf("%s:%d columns %d..%d, value at %d..%d (expected offset %d)", *r.File, r.StartLine, r.StartCol, r.EndCol, start, end, off)
		}
		seen++
	}
	if seen != 4 {
		t.Fatalf("checked %d records", seen)
	}
}
