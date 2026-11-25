package eslint

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// eslintRules are the families of the managed ESLint selections
// (docs/ANALYZERS.md#기본-심각도): core and typescript-eslint rule names.
var eslintRules = []severity.Rule{
	{Pattern: "@typescript-eslint/no-floating-promises", Family: "concurrency"},
	{Pattern: "@typescript-eslint/no-misused-promises", Family: "concurrency"},
	{Pattern: "require-atomic-updates", Family: "concurrency"},
	{Pattern: "no-async-promise-executor", Family: "concurrency"},
	{Pattern: "@typescript-eslint/no-non-null-assertion", Family: "null_dereference"},
	{Pattern: "@typescript-eslint/no-unsafe-*", Family: "type_error"},
	{Pattern: "codeye/program-diagnostics", Family: "type_error"},
	{Pattern: "no-undef", Family: "type_error"},
	{Pattern: "no-eval", Family: "injection"}, {Pattern: "no-implied-eval", Family: "injection"},
	{Pattern: "@typescript-eslint/no-implied-eval", Family: "injection"}, {Pattern: "no-new-func", Family: "injection"},
	{Pattern: "prefer-promise-reject-errors", Family: "exception_handling"},
	{Pattern: "@typescript-eslint/prefer-promise-reject-errors", Family: "exception_handling"},
	{Pattern: "@typescript-eslint/only-throw-error", Family: "exception_handling"},
	{Pattern: "no-empty", Family: "exception_handling"}, {Pattern: "no-unsafe-finally", Family: "exception_handling"},
	{Pattern: "no-unused-vars", Family: "unused_code"}, {Pattern: "@typescript-eslint/no-unused-vars", Family: "unused_code"},
	{Pattern: "no-unreachable", Family: "unused_code"}, {Pattern: "no-unused-labels", Family: "unused_code"},
	{Pattern: "no-unused-private-class-members", Family: "unused_code"}, {Pattern: "no-useless-*", Family: "style"},
	{Pattern: "@typescript-eslint/no-deprecated", Family: "deprecated_api"},
	{Pattern: "no-prototype-builtins", Family: "api_misuse"}, {Pattern: "use-isnan", Family: "logic_error"},
	{Pattern: "valid-typeof", Family: "logic_error"}, {Pattern: "no-self-assign", Family: "logic_error"},
	{Pattern: "no-self-compare", Family: "logic_error"}, {Pattern: "no-dupe-*", Family: "logic_error"},
	{Pattern: "no-duplicate-case", Family: "logic_error"}, {Pattern: "no-fallthrough", Family: "logic_error"},
	{Pattern: "no-cond-assign", Family: "logic_error"}, {Pattern: "no-constant-condition", Family: "logic_error"},
	{Pattern: "no-constant-binary-expression", Family: "logic_error"}, {Pattern: "no-unsafe-negation", Family: "logic_error"},
	{Pattern: "no-unsafe-optional-chaining", Family: "null_dereference"}, {Pattern: "getter-return", Family: "logic_error"},
	{Pattern: "no-func-assign", Family: "logic_error"}, {Pattern: "no-import-assign", Family: "logic_error"},
	{Pattern: "no-const-assign", Family: "logic_error"}, {Pattern: "no-class-assign", Family: "logic_error"},
	{Pattern: "for-direction", Family: "logic_error"}, {Pattern: "no-sparse-arrays", Family: "logic_error"},
	{Pattern: "no-control-regex", Family: "style"}, {Pattern: "no-empty-pattern", Family: "style"},
	{Pattern: "no-extra-boolean-cast", Family: "style"}, {Pattern: "no-regex-spaces", Family: "style"},
	{Pattern: "no-irregular-whitespace", Family: "formatting"}, {Pattern: "no-mixed-spaces-and-tabs", Family: "formatting"},
	{Pattern: "no-case-declarations", Family: "logic_error"}, {Pattern: "no-inner-declarations", Family: "style"},
	{Pattern: "@typescript-eslint/*", Family: "maintainability"},
}

// Severity and TypedSeverity are the default-severity tables of the two
// ESLint analyzers; the type-aware analyzer runs either selection.
var (
	Severity = severity.Table{
		Analyzer: AnalyzerID, RulePack: RulePackID, RulePackVersions: []string{RulePackVersion}, Version: "1",
		Rules: eslintRules, Grades: map[string]string{"error": "logic_error", "warning": "maintainability"},
	}
	TypedSeverity = severity.Table{
		Analyzer: TypedAnalyzerID, RulePack: TypedRulePackID, RulePackVersions: []string{TypedRulePackVersion}, Version: "1",
		Rules: eslintRules, Grades: map[string]string{"error": "logic_error", "warning": "maintainability"},
	}
	ManagedJSSeverity = severity.Table{
		Analyzer: TypedAnalyzerID, RulePack: ManagedJSRulePackID, RulePackVersions: []string{managedJSRulePackVersion}, Version: "1",
		Rules: eslintRules, Grades: map[string]string{"error": "logic_error", "warning": "maintainability"},
	}
)
