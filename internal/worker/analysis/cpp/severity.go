package cpp

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// CppcheckSeverity is the default-severity table of Cppcheck
// (docs/ANALYZERS.md#기본-심각도). Known checks first; Cppcheck's own grade
// decides the rest (an "error" is a defect Cppcheck is sure about).
var CppcheckSeverity = severity.Table{
	Analyzer: CppcheckID, RulePack: CppcheckRulePackID, RulePackVersions: []string{CppcheckRulePackVersion}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "nullPointer*", Family: "null_dereference"}, {Pattern: "ctunullpointer*", Family: "null_dereference"},
		{Pattern: "arrayIndexOutOfBounds*", Family: "memory_safety"}, {Pattern: "bufferAccessOutOfBounds", Family: "memory_safety"},
		{Pattern: "outOfBounds", Family: "memory_safety"}, {Pattern: "negativeIndex", Family: "memory_safety"},
		{Pattern: "uninitvar", Family: "memory_safety"}, {Pattern: "uninitdata", Family: "memory_safety"},
		{Pattern: "uninitMemberVar", Family: "memory_safety"}, {Pattern: "uninitStructMember", Family: "memory_safety"},
		{Pattern: "deallocuse", Family: "memory_safety"}, {Pattern: "doubleFree", Family: "memory_safety"},
		{Pattern: "invalidLifetime", Family: "memory_safety"}, {Pattern: "danglingLifetime", Family: "memory_safety"},
		{Pattern: "danglingTemporaryLifetime", Family: "memory_safety"}, {Pattern: "returnDanglingLifetime", Family: "memory_safety"},
		{Pattern: "autoVariables", Family: "memory_safety"}, {Pattern: "mismatchAllocDealloc", Family: "memory_safety"},
		{Pattern: "memleak*", Family: "resource_leak"}, {Pattern: "resourceLeak", Family: "resource_leak"},
		{Pattern: "leak*", Family: "resource_leak"}, {Pattern: "deallocret", Family: "resource_leak"},
		{Pattern: "zerodiv*", Family: "undefined_behavior"}, {Pattern: "integerOverflow*", Family: "undefined_behavior"},
		{Pattern: "shiftTooManyBits*", Family: "undefined_behavior"}, {Pattern: "shiftNegative*", Family: "undefined_behavior"},
		{Pattern: "invalidFunctionArg*", Family: "api_misuse"}, {Pattern: "wrongPrintfScanfArgNum", Family: "api_misuse"},
		{Pattern: "invalidPrintfArgType*", Family: "api_misuse"}, {Pattern: "invalidScanfArgType*", Family: "api_misuse"},
		{Pattern: "ignoredReturnValue", Family: "unchecked_error"}, {Pattern: "checkReturnValue", Family: "unchecked_error"},
		{Pattern: "except*", Family: "exception_handling"}, {Pattern: "throw*", Family: "exception_handling"},
		{Pattern: "rethrow*", Family: "exception_handling"}, {Pattern: "catchException*", Family: "exception_handling"},
		{Pattern: "unusedVariable", Family: "unused_code"}, {Pattern: "unreadVariable", Family: "unused_code"},
		{Pattern: "unusedFunction", Family: "unused_code"}, {Pattern: "unusedStructMember", Family: "unused_code"},
		{Pattern: "missingInclude*", Family: "maintainability"}, {Pattern: "missingReturn", Family: "undefined_behavior"},
	},
	Grades: map[string]string{
		"error": "logic_error", "warning": "logic_error", "style": "style", "performance": "performance",
		"portability": "maintainability", "information": "documentation",
	},
}

// ClangTidySeverity is the default-severity table of clang-tidy
// (docs/ANALYZERS.md#기본-심각도). The check name says what it is about.
var ClangTidySeverity = severity.Table{
	Analyzer: ClangTidyID, RulePack: ClangTidyRulePackID, RulePackVersions: []string{ClangTidyRulePackVersion}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "clang-analyzer-core.NullDereference", Family: "null_dereference"},
		{Pattern: "clang-analyzer-core.NonNullParamChecker", Family: "null_dereference"},
		{Pattern: "clang-analyzer-core.CallAndMessage", Family: "null_dereference"},
		{Pattern: "clang-analyzer-cplusplus.NewDeleteLeaks", Family: "resource_leak"},
		{Pattern: "clang-analyzer-unix.Malloc", Family: "memory_safety"},
		{Pattern: "clang-analyzer-unix.MismatchedDeallocator", Family: "memory_safety"},
		{Pattern: "clang-analyzer-cplusplus.NewDelete", Family: "memory_safety"},
		{Pattern: "clang-analyzer-cplusplus.ArrayDelete", Family: "memory_safety"},
		{Pattern: "clang-analyzer-cplusplus.InnerPointer", Family: "memory_safety"},
		{Pattern: "clang-analyzer-unix.Stream", Family: "resource_leak"},
		{Pattern: "clang-analyzer-core.uninitialized.*", Family: "memory_safety"},
		{Pattern: "clang-analyzer-core.UndefinedBinaryOperatorResult", Family: "undefined_behavior"},
		{Pattern: "clang-analyzer-core.DivideZero", Family: "undefined_behavior"},
		{Pattern: "clang-analyzer-core.BitwiseShift", Family: "undefined_behavior"},
		{Pattern: "clang-analyzer-core.StackAddressEscape", Family: "memory_safety"},
		{Pattern: "clang-analyzer-security.*", Family: "security_weakness"},
		{Pattern: "clang-analyzer-deadcode.*", Family: "unused_code"},
		{Pattern: "clang-analyzer-unix.BlockInCriticalSection", Family: "concurrency"},
		{Pattern: "clang-analyzer-*", Family: "logic_error"},
		{Pattern: "bugprone-use-after-move", Family: "memory_safety"},
		{Pattern: "bugprone-dangling-handle", Family: "memory_safety"},
		{Pattern: "bugprone-unique-ptr-array-mismatch", Family: "memory_safety"},
		{Pattern: "bugprone-shared-ptr-array-mismatch", Family: "memory_safety"},
		{Pattern: "bugprone-suspicious-realloc-usage", Family: "resource_leak"},
		{Pattern: "bugprone-not-null-terminated-result", Family: "memory_safety"},
		{Pattern: "bugprone-command-processor", Family: "injection"},
		{Pattern: "bugprone-unsafe-functions", Family: "security_weakness"},
		{Pattern: "bugprone-random-generator-seed", Family: "security_weakness"},
		{Pattern: "bugprone-unused-return-value", Family: "unchecked_error"},
		{Pattern: "bugprone-posix-return", Family: "unchecked_error"},
		{Pattern: "bugprone-unchecked-string-to-number-conversion", Family: "unchecked_error"},
		{Pattern: "bugprone-exception-escape", Family: "exception_handling"},
		{Pattern: "bugprone-empty-catch", Family: "exception_handling"},
		{Pattern: "bugprone-unhandled-exception-at-new", Family: "exception_handling"},
		{Pattern: "bugprone-signal-handler", Family: "concurrency"},
		{Pattern: "bugprone-spuriously-wake-up-functions", Family: "concurrency"},
		{Pattern: "bugprone-bad-signal-to-kill-thread", Family: "concurrency"},
		{Pattern: "concurrency-*", Family: "concurrency"},
		{Pattern: "bugprone-*", Family: "logic_error"},
		{Pattern: "cert-*", Family: "security_weakness"},
		{Pattern: "misc-throw-by-value-catch-by-reference", Family: "exception_handling"},
		{Pattern: "performance-*", Family: "performance"},
		{Pattern: "modernize-*", Family: "style"},
		{Pattern: "readability-*", Family: "style"},
		{Pattern: "misc-*", Family: "maintainability"},
	},
}

// GCCAnalyzerSeverity is the default-severity table of the GCC file
// descriptor checks (docs/ANALYZERS.md#기본-심각도).
var GCCAnalyzerSeverity = severity.Table{
	Analyzer: GCCAnalyzerID, RulePack: GCCAnalyzerRulePackID, RulePackVersions: []string{GCCAnalyzerRulePackVersion}, Version: "1",
	Rules: []severity.Rule{
		{Pattern: "analyzer-fd-leak", Family: "resource_leak"},
		{Pattern: "analyzer-fd-double-close", Family: "memory_safety"},
		{Pattern: "analyzer-fd-use-after-close", Family: "memory_safety"},
		{Pattern: "analyzer-fd-use-without-check", Family: "unchecked_error"},
		{Pattern: "analyzer-fd-*", Family: "api_misuse"},
	},
}
