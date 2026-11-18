package jscpd

import (
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
)

// jscpd prints the native absolute paths CODEYE passed, masked to the
// redaction marker: on Windows the separators are backslashes and are read
// in slash form; elsewhere a backslash is part of a file name.
func TestRelativePathsPerPlatform(t *testing.T) {
	listed := map[string]bool{"src/a.js": true}
	if r, ok := rel(analysis.RedactedMarker+"/src/a.js", listed); !ok || r != "src/a.js" {
		t.Fatalf("slash path %q %v", r, ok)
	}
	r, ok := rel(analysis.RedactedMarker+`\src\a.js`, listed)
	if want := runtime.GOOS == "windows"; ok != want || (want && r != "src/a.js") {
		t.Fatalf("backslash path on %s: %q %v", runtime.GOOS, r, ok)
	}
	if _, ok := rel(analysis.RedactedMarker+"/src/other.js", listed); ok {
		t.Fatal("a file CODEYE did not list was accepted")
	}
}
