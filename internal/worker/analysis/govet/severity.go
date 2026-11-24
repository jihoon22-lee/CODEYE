package govet

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// Severity is the default-severity table of go vet (docs/ANALYZERS.md#기본-심각도).
// go vet has no grade; its rule is the analyzer name. The rule pack is the
// one the report synthesizes for go vet.
var Severity = severity.Table{
	Analyzer: AnalyzerID, RulePack: "codeye-go-vet-default", RulePackVersions: []string{"1"}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "nilness", Family: "null_dereference"},
		{Pattern: "nilfunc", Family: "logic_error"},
		{Pattern: "copylocks", Family: "concurrency"}, {Pattern: "atomic", Family: "concurrency"},
		{Pattern: "loopclosure", Family: "concurrency"}, {Pattern: "testinggoroutine", Family: "concurrency"},
		{Pattern: "waitgroup", Family: "concurrency"}, {Pattern: "sigchanyzer", Family: "concurrency"},
		{Pattern: "unsafeptr", Family: "memory_safety"}, {Pattern: "cgocall", Family: "memory_safety"},
		{Pattern: "lostcancel", Family: "resource_leak"}, {Pattern: "httpresponse", Family: "resource_leak"},
		{Pattern: "errorsas", Family: "api_misuse"}, {Pattern: "unmarshal", Family: "api_misuse"},
		{Pattern: "printf", Family: "api_misuse"}, {Pattern: "slog", Family: "api_misuse"},
		{Pattern: "stdmethods", Family: "api_misuse"}, {Pattern: "timeformat", Family: "api_misuse"},
		{Pattern: "hostport", Family: "api_misuse"}, {Pattern: "unusedresult", Family: "unchecked_error"},
		{Pattern: "bools", Family: "logic_error"}, {Pattern: "assign", Family: "logic_error"},
		{Pattern: "shift", Family: "logic_error"}, {Pattern: "stringintconv", Family: "logic_error"},
		{Pattern: "ifaceassert", Family: "logic_error"}, {Pattern: "defers", Family: "logic_error"},
		{Pattern: "appends", Family: "logic_error"}, {Pattern: "framepointer", Family: "undefined_behavior"},
		{Pattern: "asmdecl", Family: "undefined_behavior"},
		{Pattern: "unreachable", Family: "unused_code"},
		{Pattern: "structtag", Family: "maintainability"}, {Pattern: "tests", Family: "maintainability"},
		{Pattern: "stdversion", Family: "maintainability"},
		{Pattern: "composites", Family: "style"}, {Pattern: "buildtag", Family: "style"}, {Pattern: "directive", Family: "style"},
	},
}
