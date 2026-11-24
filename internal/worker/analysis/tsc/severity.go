package tsc

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// Severity is the default-severity table of tsc (docs/ANALYZERS.md#기본-심각도).
// Possibly-undefined access codes are null dereferences, unused-declaration
// codes (6133–6205) unused code, implicit-any codes (7005–7053) style and
// missing modules or types a type error that may be an analysis gap.
var Severity = severity.Table{
	Analyzer: AnalyzerID, RulePack: RulePackID, RulePackVersions: []string{RulePackVersion}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "TS2531", Family: "null_dereference"}, {Pattern: "TS2532", Family: "null_dereference"},
		{Pattern: "TS2533", Family: "null_dereference"}, {Pattern: "TS18047", Family: "null_dereference"},
		{Pattern: "TS18048", Family: "null_dereference"}, {Pattern: "TS18049", Family: "null_dereference"},
		{Pattern: "TS2801", Family: "concurrency"}, {Pattern: "TS1308", Family: "concurrency"},
		{Pattern: "TS6133", Family: "unused_code"}, {Pattern: "TS6138", Family: "unused_code"},
		{Pattern: "TS6192", Family: "unused_code"}, {Pattern: "TS6196", Family: "unused_code"},
		{Pattern: "TS6198", Family: "unused_code"}, {Pattern: "TS6205", Family: "unused_code"},
		{Pattern: "TS7027", Family: "unused_code"}, {Pattern: "TS7028", Family: "unused_code"},
		{Pattern: "TS2578", Family: "style"}, {Pattern: "TS70*", Family: "style"},
		{Pattern: "TS6385", Family: "deprecated_api"}, {Pattern: "TS6387", Family: "deprecated_api"},
		{Pattern: "TS*", Family: "type_error"},
	},
}
