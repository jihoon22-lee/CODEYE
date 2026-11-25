package errcheck

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// Severity is the default-severity table of errcheck
// (docs/ANALYZERS.md#기본-심각도): every diagnostic is an ignored error.
var Severity = severity.Table{
	Analyzer: "go.errcheck", RulePack: "codeye-go-errcheck", RulePackVersions: []string{"1"}, Version: "1",
	Rules: []severity.Rule{{Pattern: rule, Family: "unchecked_error"}},
}
