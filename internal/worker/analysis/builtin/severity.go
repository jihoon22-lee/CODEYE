package builtin

import (
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/architecture"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/errcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/eslint"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gofmtcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/javapack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/opengrep"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/tsc"
)

// SeverityTables are the default-severity tables of the diagnostic
// analyzers, each kept next to its pack (docs/ANALYZERS.md#기본-심각도). The
// report builder and the review triage read the same list. Metric and
// secret analyzers have none: their observations are no defect candidates.
// An analyzer without a table keeps low for every rule.
func SeverityTables() []severity.Table {
	return []severity.Table{
		govet.Severity, staticcheck.Severity, gofmtcheck.Severity, errcheck.Severity,
		cpp.CppcheckSeverity, cpp.ClangTidySeverity, cpp.GCCAnalyzerSeverity,
		python.RuffSeverity, python.MypySeverity, python.PyrightSeverity, architecture.ImportLinterSeverity, python.BanditSeverity,
		eslint.Severity, tsc.Severity, architecture.DependencyCruiserSeverity, eslint.TypedSeverity, eslint.ManagedJSSeverity,
		rustpack.CheckSeverity, rustpack.ClippySeverity, rustpack.DocSeverity,
		javapack.PMDSeverity, javapack.SpotBugsSeverity,
		opengrep.Severity,
	}
}

// DefaultSeverity is the default severity of one diagnostic under these
// tables. pathClass is record.PathClass of the diagnostic's file ("" for
// shipped code or a diagnostic without a location): any other class lowers
// the severity by the path rule (severity.PathRule).
func DefaultSeverity(analyzer, rulePack, rulePackVersion, rule, grade, pathClass string) severity.Result {
	return severity.Lookup(SeverityTables(), analyzer, rulePack, rulePackVersion, rule, grade, pathClass != "")
}
