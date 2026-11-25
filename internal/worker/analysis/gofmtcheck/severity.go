package gofmtcheck

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// Severity is the default-severity table of the gofmt check
// (docs/ANALYZERS.md#기본-심각도): a format difference is formatting.
var Severity = severity.Table{
	Analyzer: AnalyzerID, RulePack: "codeye-go-format", RulePackVersions: []string{"1"}, Version: "1",
	Rules: []severity.Rule{{Pattern: Rule, Family: "formatting"}},
}
