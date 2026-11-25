package python

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// The default-severity tables of the Python pack (docs/ANALYZERS.md#기본-심각도).

// RuffSeverity maps the selected Ruff rules (pyflakes, bugbear, comparisons,
// bare and blind except, pylint errors, async).
var RuffSeverity = severity.Table{
	Analyzer: RuffID, RulePack: RuffRulePackID, RulePackVersions: []string{RuffRulePackVer}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "F821", Family: "type_error"}, {Pattern: "F822", Family: "type_error"}, {Pattern: "F823", Family: "type_error"},
		{Pattern: "F401", Family: "unused_code"}, {Pattern: "F841", Family: "unused_code"}, {Pattern: "F811", Family: "unused_code"},
		{Pattern: "F5*", Family: "api_misuse"}, {Pattern: "F6*", Family: "logic_error"}, {Pattern: "F7*", Family: "logic_error"},
		{Pattern: "F*", Family: "logic_error"},
		{Pattern: "E711", Family: "logic_error"}, {Pattern: "E712", Family: "logic_error"}, {Pattern: "E713", Family: "style"},
		{Pattern: "E714", Family: "style"}, {Pattern: "E721", Family: "logic_error"},
		{Pattern: "E722", Family: "exception_handling"}, {Pattern: "BLE*", Family: "exception_handling"},
		{Pattern: "E9*", Family: "type_error"},
		{Pattern: "B006", Family: "logic_error"}, {Pattern: "B008", Family: "logic_error"},
		{Pattern: "B012", Family: "exception_handling"}, {Pattern: "B014", Family: "exception_handling"},
		{Pattern: "B017", Family: "exception_handling"}, {Pattern: "B904", Family: "exception_handling"},
		{Pattern: "B007", Family: "unused_code"}, {Pattern: "B018", Family: "unused_code"},
		{Pattern: "B*", Family: "logic_error"},
		{Pattern: "PLE*", Family: "logic_error"},
		{Pattern: "ASYNC*", Family: "concurrency"},
	},
}

// MypySeverity maps mypy error codes; a note is documentation.
var MypySeverity = severity.Table{
	Analyzer: MypyID, RulePack: MypyRulePackID, RulePackVersions: []string{MypyRulePackVer}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "union-attr", Family: "null_dereference"},
		{Pattern: "import-not-found", Family: "type_error"}, {Pattern: "import-untyped", Family: "type_error"},
		{Pattern: "import", Family: "type_error"},
		{Pattern: "unused-ignore", Family: "style"}, {Pattern: "unused-coroutine", Family: "concurrency"},
		{Pattern: "unreachable", Family: "unused_code"}, {Pattern: "redundant-cast", Family: "style"},
		{Pattern: "deprecated", Family: "deprecated_api"},
	},
	Grades: map[string]string{"error": "type_error", "note": "documentation"},
}

// PyrightSeverity maps Pyright rules (reportXxx).
var PyrightSeverity = severity.Table{
	Analyzer: PyrightID, RulePack: PyrightRulePackID, RulePackVersions: []string{PyrightRulePackVer}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "reportOptional*", Family: "null_dereference"},
		{Pattern: "reportPossiblyUnbound", Family: "logic_error"},
		{Pattern: "reportUnusedCoroutine", Family: "concurrency"},
		{Pattern: "reportMissingImports", Family: "type_error"}, {Pattern: "reportMissingModuleSource", Family: "type_error"},
		{Pattern: "reportUnused*", Family: "unused_code"},
		{Pattern: "reportDeprecated", Family: "deprecated_api"},
		{Pattern: "reportUnnecessary*", Family: "style"},
	},
	Grades: map[string]string{"error": "type_error", "warning": "type_error", "information": "documentation"},
}

// BanditSeverity maps Bandit tests by group; Bandit's own grade decides an
// unlisted test.
var BanditSeverity = severity.Table{
	Analyzer: BanditID, RulePack: BanditRulePackID, RulePackVersions: []string{BanditRulePackVer}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "B102", Family: "injection"}, {Pattern: "B307", Family: "injection"},
		{Pattern: "B105", Family: "security_weakness"}, {Pattern: "B106", Family: "security_weakness"}, {Pattern: "B107", Family: "security_weakness"},
		{Pattern: "B101", Family: "panic_path"},
		{Pattern: "B110", Family: "exception_handling"}, {Pattern: "B112", Family: "exception_handling"},
		{Pattern: "B301", Family: "injection"}, {Pattern: "B403", Family: "security_weakness"},
		{Pattern: "B506", Family: "injection"}, {Pattern: "B608", Family: "injection"},
		{Pattern: "B60*", Family: "injection"}, {Pattern: "B609", Family: "injection"},
		{Pattern: "B3*", Family: "security_weakness"}, {Pattern: "B4*", Family: "security_weakness"},
		{Pattern: "B5*", Family: "security_weakness"}, {Pattern: "B7*", Family: "injection"},
	},
	Grades: map[string]string{"high": "security_weakness", "medium": "security_weakness", "low": "maintainability"},
}
