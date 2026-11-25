package opengrep

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
)

// Every rule of the pack has a family and a cap below it: flows into a
// command, query, path, code or page are injection held at medium and
// merged with other tools' same-line reports, SSRF is a security weakness
// held at low; none is high, critical, info or unknown, and the reason says
// the table held the severity down. On a path that is not shipped code the
// path rule lowers an injection flow to low like any other diagnostic.
func TestSeverityCoversEveryRule(t *testing.T) {
	if msg := Severity.Validate(); msg != "" {
		t.Fatal(msg)
	}
	tables := []severity.Table{Severity}
	for _, id := range RuleIDs() {
		got := severity.Lookup(tables, AnalyzerID, RulePackID, RulePackVersion, id, "error", false)
		want, family := severity.Medium, "injection"
		if strings.HasSuffix(id, ".ssrf") {
			want, family = severity.Low, "security_weakness"
		}
		if got.Severity != want || got.Family != family || !got.Capped || got.Lowered {
			t.Errorf("%s: %+v, want %s %s capped", id, got, want, family)
		}
		f, _ := severity.FamilyOf(got.Family)
		if f.Merge != (family == "injection") {
			t.Errorf("%s: merge %v", id, f.Merge)
		}
		if r := severity.Reason(got); !strings.Contains(r, "측정이 없어") {
			t.Errorf("%s: reason %q", id, r)
		}
		// A test, example or vendored file: injection drops to low by the
		// path rule (its reason names the rule); SSRF is low already.
		low := severity.Lookup(tables, AnalyzerID, RulePackID, RulePackVersion, id, "error", true)
		if low.Severity != severity.Low || low.Lowered != (family == "injection") {
			t.Errorf("%s on a test path: %+v", id, low)
		}
		if family == "injection" && !strings.Contains(severity.Reason(low), severity.PathRule) {
			t.Errorf("%s on a test path: reason %q", id, severity.Reason(low))
		}
	}
	// The table is written for this rule-pack version only.
	if got := severity.Lookup(tables, AnalyzerID, RulePackID, "1", "codeye.python.command-injection", "", false); got.Severity != severity.Low || !got.Mismatch {
		t.Fatalf("other rule-pack version: %+v", got)
	}
}
