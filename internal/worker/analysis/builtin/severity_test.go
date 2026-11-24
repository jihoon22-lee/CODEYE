package builtin

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
)

// TC-289: every diagnostic analyzer of the catalog has a well-formed
// default-severity table next to its pack, written for the rule pack it runs
// now; metric and secret analyzers have none.
func TestSeverityTablesCoverTheCatalog(t *testing.T) {
	tables := SeverityTables()
	seen := map[string]bool{}
	for _, tb := range tables {
		if msg := tb.Validate(); msg != "" {
			t.Fatalf("%s: %s", tb.ID(), msg)
		}
		key := tb.Analyzer + " " + tb.RulePack
		if seen[key] {
			t.Fatalf("two tables for %s", key)
		}
		seen[key] = true
	}
	for _, e := range catalog() {
		has := slices.ContainsFunc(tables, func(tb severity.Table) bool { return tb.Analyzer == e.desc.ID })
		if e.desc.Purpose == "" && !has {
			t.Errorf("diagnostic analyzer %s has no severity table", e.desc.ID)
		}
		if e.desc.Purpose != "" && has {
			t.Errorf("%s analyzer %s has a severity table", e.desc.Purpose, e.desc.ID)
		}
	}
}

func TestDefaultSeverityByFamily(t *testing.T) {
	for _, c := range []struct {
		analyzer, pack, version, rule, grade, want, family string
	}{
		{staticcheck.AnalyzerID, staticcheck.RulePackID, staticcheck.RulePackVersion, "SA5011", "", severity.High, "null_dereference"},
		{staticcheck.AnalyzerID, staticcheck.RulePackID, staticcheck.RulePackVersion, "ST1003", "", severity.Info, "style"},
		{staticcheck.AnalyzerID, staticcheck.RulePackID, staticcheck.RulePackVersion, "U1000", "", severity.Low, "unused_code"},
		{"go.vet", "codeye-go-vet-default", "1", "lostcancel", "", severity.Medium, "resource_leak"},
		{"go.errcheck", "codeye-go-errcheck", "1", "unchecked_error", "", severity.Medium, "unchecked_error"},
		{cpp.CppcheckID, cpp.CppcheckRulePackID, cpp.CppcheckRulePackVersion, "nullPointer", "error", severity.High, "null_dereference"},
		{cpp.CppcheckID, cpp.CppcheckRulePackID, cpp.CppcheckRulePackVersion, "someNewCheck", "style", severity.Info, "style"},
		{cpp.CppcheckID, cpp.CppcheckRulePackID, cpp.CppcheckRulePackVersion, "someNewCheck", "", severity.Low, ""},
		{"python.import_linter", "codeye-import-linter", "1", "any contract", "", severity.Low, "maintainability"},
	} {
		got := DefaultSeverity(c.analyzer, c.pack, c.version, c.rule, c.grade)
		if got.Severity != c.want || got.Family != c.family {
			t.Errorf("%s %s %s: %+v, want %s %s", c.analyzer, c.rule, c.grade, got, c.want, c.family)
		}
	}
	// Another rule-pack version or an unknown analyzer keeps low, and says why.
	if got := DefaultSeverity(staticcheck.AnalyzerID, staticcheck.RulePackID, "99", "SA5011", ""); got.Severity != severity.Low || !got.Mismatch || severity.Reason(got) != severity.ReasonMismatch {
		t.Fatalf("other rule-pack version: %+v", got)
	}
	if got := DefaultSeverity("any.sarif", "x", "1", "r", "error"); got.Severity != severity.Low || got.Known() || severity.Reason(got) != severity.ReasonUnknown {
		t.Fatalf("unknown analyzer: %+v", got)
	}
	for _, r := range severity.Reasons() {
		if r == "" {
			t.Fatal("empty reason")
		}
	}
}
