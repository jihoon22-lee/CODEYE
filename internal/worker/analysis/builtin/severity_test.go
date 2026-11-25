package builtin

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
)

// TC-292: every diagnostic analyzer of the catalog has a well-formed
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
		// SpotBugs path and servlet XSS reports merge as injection but keep
		// the low they had as unknown rules; SQL keeps high.
		{"java.spotbugs", "codeye-java-spotbugs", "1", "PT_RELATIVE_PATH_TRAVERSAL", "", severity.Low, "injection"},
		{"java.spotbugs", "codeye-java-spotbugs", "1", "XSS_REQUEST_PARAMETER_TO_SERVLET_WRITER", "", severity.Low, "injection"},
		{"java.spotbugs", "codeye-java-spotbugs", "1", "SQL_NONCONSTANT_STRING_PASSED_TO_EXECUTE", "", severity.High, "injection"},
	} {
		got := DefaultSeverity(c.analyzer, c.pack, c.version, c.rule, c.grade, "")
		if got.Severity != c.want || got.Family != c.family {
			t.Errorf("%s %s %s: %+v, want %s %s", c.analyzer, c.rule, c.grade, got, c.want, c.family)
		}
	}
	// Another rule-pack version or an unknown analyzer keeps low, and says why.
	if got := DefaultSeverity(staticcheck.AnalyzerID, staticcheck.RulePackID, "99", "SA5011", "", ""); got.Severity != severity.Low || !got.Mismatch || severity.Reason(got) != severity.ReasonMismatch {
		t.Fatalf("other rule-pack version: %+v", got)
	}
	if got := DefaultSeverity("any.sarif", "x", "1", "r", "error", ""); got.Severity != severity.Low || got.Known() || severity.Reason(got) != severity.ReasonUnknown {
		t.Fatalf("unknown analyzer: %+v", got)
	}
	for _, r := range severity.Reasons() {
		if r == "" {
			t.Fatal("empty reason")
		}
	}
	// A capped severity has its own fixed reason, one Reasons lists.
	capped := DefaultSeverity("java.spotbugs", "codeye-java-spotbugs", "1", "PT_ABSOLUTE_PATH_TRAVERSAL", "", "")
	if !capped.Capped || !slices.Contains(severity.Reasons(), severity.Reason(capped)) || severity.Reason(capped) == severity.Reason(severity.Result{Family: "injection"}) {
		t.Fatalf("capped: %+v %q", capped, severity.Reason(capped))
	}
	// A cap must be a severity below the family's.
	for _, c := range []string{severity.High, "critical", "x"} {
		tb := severity.Table{Analyzer: "a", RulePack: "p", RulePackVersions: []string{"1"}, Version: "1", Rules: []severity.Rule{{Pattern: "r", Family: "injection", Cap: c}}}
		if tb.Validate() == "" {
			t.Errorf("cap %s accepted", c)
		}
	}
}

// The path rule lowers a family's severity on a path that is not shipped
// code, by the family's own Lowered level, and says so in a fixed reason the
// code-free export keeps; a rule no table knows and a family already at its
// lowered level stay as they are.
func TestDefaultSeverityOnPathsThatAreNotShipped(t *testing.T) {
	sc := func(rule, class string) severity.Result {
		return DefaultSeverity(staticcheck.AnalyzerID, staticcheck.RulePackID, staticcheck.RulePackVersion, rule, "", class)
	}
	for _, c := range []struct {
		rule, class, want string
		lowered           bool
	}{
		{"SA5011", "", severity.High, false},
		{"SA5011", "test", severity.Low, true},
		{"SA5011", "vendored", severity.Low, true},
		{"SA1019", "example", severity.Low, false},
		{"U1000", "generated", severity.Low, false},
		{"ST1003", "test", severity.Info, false},
		{"SA6000", "benchmark", severity.Info, true},
	} {
		got := sc(c.rule, c.class)
		if got.Severity != c.want || got.Lowered != c.lowered {
			t.Errorf("%s on %q: %+v, want %s lowered=%v", c.rule, c.class, got, c.want, c.lowered)
		}
	}
	got := sc("SA5011", "test")
	if !slices.Contains(severity.Reasons(), severity.Reason(got)) || severity.Reason(got) == severity.Reason(sc("SA5011", "")) {
		t.Fatalf("lowered reason %q", severity.Reason(got))
	}
	if got := DefaultSeverity("any.sarif", "x", "1", "r", "error", "test"); got.Severity != severity.Low || got.Lowered || got.Known() {
		t.Fatalf("unknown analyzer on a test path: %+v", got)
	}
	// Every family lowers to a level that is not above its own.
	for _, f := range severity.Families() {
		if severity.Rank(f.Lowered) == 0 || severity.Rank(f.Lowered) > severity.Rank(f.Severity) {
			t.Errorf("family %s lowers %s to %s", f.Name, f.Severity, f.Lowered)
		}
	}
	tb, _ := severity.TableFor(SeverityTables(), staticcheck.AnalyzerID, staticcheck.RulePackID, staticcheck.RulePackVersion)
	if tb.RecordedID() != tb.ID()+"+"+severity.PathRule {
		t.Fatalf("recorded id %s", tb.RecordedID())
	}
}
