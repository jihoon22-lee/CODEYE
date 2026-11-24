package staticcheck

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// Severity is the default-severity table of Staticcheck
// (docs/ANALYZERS.md#기본-심각도). Staticcheck's checks are grouped by their
// code prefix; a few known checks are named first.
var Severity = severity.Table{
	Analyzer: AnalyzerID, RulePack: RulePackID, RulePackVersions: []string{RulePackVersion}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "SA5011", Family: "null_dereference"},
		{Pattern: "SA1019", Family: "deprecated_api"},
		{Pattern: "SA5001", Family: "resource_leak"},
		{Pattern: "SA9001", Family: "resource_leak"},
		{Pattern: "SA4006", Family: "unused_code"}, {Pattern: "SA4010", Family: "unused_code"},
		{Pattern: "U1000", Family: "unused_code"},
		{Pattern: "SA1*", Family: "api_misuse"},
		{Pattern: "SA2*", Family: "concurrency"},
		{Pattern: "SA3*", Family: "maintainability"},
		{Pattern: "SA4*", Family: "logic_error"},
		{Pattern: "SA5*", Family: "logic_error"},
		{Pattern: "SA6*", Family: "performance"},
		{Pattern: "SA9*", Family: "logic_error"},
		{Pattern: "S1*", Family: "style"},
		{Pattern: "ST1*", Family: "style"},
		{Pattern: "QF*", Family: "style"},
	},
}
