package opengrep

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
)

// Every rule of the pack has a family: SSRF is medium, the other flows into
// a command, query, path, code or page are high injection, merged with other
// tools' same-line reports; none is critical, info or unknown.
func TestSeverityCoversEveryRule(t *testing.T) {
	if msg := Severity.Validate(); msg != "" {
		t.Fatal(msg)
	}
	tables := []severity.Table{Severity}
	for _, id := range RuleIDs() {
		got := severity.Lookup(tables, AnalyzerID, RulePackID, RulePackVersion, id, "error")
		want, family := severity.High, "injection"
		if strings.HasSuffix(id, ".ssrf") {
			want, family = severity.Medium, "security_weakness"
		}
		if got.Severity != want || got.Family != family {
			t.Errorf("%s: %+v, want %s %s", id, got, want, family)
		}
		f, _ := severity.FamilyOf(got.Family)
		if f.Merge != (family == "injection") {
			t.Errorf("%s: merge %v", id, f.Merge)
		}
	}
	// The table is written for this rule-pack version only.
	if got := severity.Lookup(tables, AnalyzerID, RulePackID, "1", "codeye.python.command-injection", ""); got.Severity != severity.Low || !got.Mismatch {
		t.Fatalf("other rule-pack version: %+v", got)
	}
}
