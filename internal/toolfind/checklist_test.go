package toolfind

import (
	"bytes"
	"strings"
	"testing"
)

// Every tool has an offline place, and the checklist checks found tools and
// says where a missing one goes, with the tested series.
func TestChecklistCoversEveryTool(t *testing.T) {
	var res Result
	for _, name := range Names() {
		if offlinePlaces[name] == "" {
			t.Errorf("%s has no offline place", name)
		}
		res.Tools = append(res.Tools, Tool{Name: name, Status: NotFound})
	}
	res.Tools[0] = Tool{Name: "go", Status: Found, Path: "/home/u/go/bin/go", Version: "1.27.4", VersionTested: true}
	var out bytes.Buffer
	if err := WriteChecklist(&out, res, "/home/u"); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"- [x] go: 1.27.4 (tested 1.27) - ~/go/bin/go\n", "- [ ] staticcheck: missing (tested 2026.2) - copy the staticcheck binary", "- [ ] opencode: missing (tested 1.19.5)", "1 found, 22 missing, 0 unusable."} {
		if !strings.Contains(text, want) {
			t.Errorf("checklist lacks %q:\n%s", want, text)
		}
	}
}
