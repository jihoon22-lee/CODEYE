package architecture

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// The default-severity tables of the dependency-rule analyzers
// (docs/ANALYZERS.md#기본-심각도): a broken dependency rule is maintainability.
var (
	ImportLinterSeverity = severity.Table{
		Analyzer: "python.import_linter", RulePack: "codeye-" + ImportLinter, RulePackVersions: []string{"1"}, Version: "1",
		Default: "maintainability",
	}
	DependencyCruiserSeverity = severity.Table{
		Analyzer: "javascript.dependency_cruiser", RulePack: "codeye-" + DependencyCruiser, RulePackVersions: []string{"1"}, Version: "1",
		Default: "maintainability",
	}
)
