package javapack

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// The default-severity tables of the Java pack (docs/ANALYZERS.md#기본-심각도):
// PMD rule names of the selection and SpotBugs bug-pattern prefixes.
var (
	PMDSeverity = severity.Table{
		Analyzer: "java." + PMD, RulePack: "codeye-java-" + PMD, RulePackVersions: []string{"1"}, Version: "1",
		Rules: []severity.Rule{
			{Pattern: "BrokenNullCheck", Family: "null_dereference"},
			{Pattern: "CloseResource", Family: "resource_leak"},
			{Pattern: "ReturnFromFinallyBlock", Family: "exception_handling"},
			{Pattern: "PreserveStackTrace", Family: "exception_handling"},
			{Pattern: "UselessOperationOnImmutable", Family: "logic_error"},
			{Pattern: "UseEqualsToCompareStrings", Family: "logic_error"},
			{Pattern: "ImplicitSwitchFallThrough", Family: "logic_error"},
			{Pattern: "UnusedLocalVariable", Family: "unused_code"},
			{Pattern: "DontCallThreadRun", Family: "concurrency"},
			{Pattern: "UnsynchronizedStaticFormatter", Family: "concurrency"},
		},
	}
	SpotBugsSeverity = severity.Table{
		Analyzer: "java." + SpotBugs, RulePack: "codeye-java-" + SpotBugs, RulePackVersions: []string{"1"}, Version: "1",
		Rules: []severity.Rule{
			{Pattern: "NP_*", Family: "null_dereference"}, {Pattern: "RCN_*", Family: "null_dereference"},
			{Pattern: "OBL_*", Family: "resource_leak"}, {Pattern: "OS_OPEN_STREAM*", Family: "resource_leak"},
			{Pattern: "ODR_*", Family: "resource_leak"},
			{Pattern: "IS2_*", Family: "concurrency"}, {Pattern: "IS_*", Family: "concurrency"}, {Pattern: "DC_*", Family: "concurrency"},
			{Pattern: "LI_*", Family: "concurrency"}, {Pattern: "UL_*", Family: "concurrency"}, {Pattern: "SWL_*", Family: "concurrency"},
			{Pattern: "JLM_*", Family: "concurrency"}, {Pattern: "ML_*", Family: "concurrency"}, {Pattern: "STCAL_*", Family: "concurrency"},
			{Pattern: "SQL_*", Family: "injection"}, {Pattern: "COMMAND_INJECTION", Family: "injection"},
			{Pattern: "PATH_TRAVERSAL*", Family: "injection"},
			{Pattern: "DMI_HARDCODED_ABSOLUTE_FILENAME", Family: "maintainability"}, {Pattern: "DMI_CONSTANT_DB_PASSWORD", Family: "security_weakness"},
			{Pattern: "RV_RETURN_VALUE_IGNORED*", Family: "unchecked_error"}, {Pattern: "RV_*", Family: "logic_error"},
			{Pattern: "DE_MIGHT_IGNORE", Family: "exception_handling"}, {Pattern: "REC_*", Family: "exception_handling"},
			{Pattern: "EC_*", Family: "logic_error"}, {Pattern: "ES_*", Family: "logic_error"}, {Pattern: "BC_*", Family: "logic_error"},
			{Pattern: "SF_*", Family: "logic_error"}, {Pattern: "UC_*", Family: "unused_code"}, {Pattern: "DLS_*", Family: "unused_code"},
			{Pattern: "URF_*", Family: "unused_code"}, {Pattern: "UUF_*", Family: "unused_code"},
			{Pattern: "EI_*", Family: "maintainability"}, {Pattern: "EI2_*", Family: "maintainability"}, {Pattern: "MS_*", Family: "maintainability"},
			{Pattern: "SE_*", Family: "maintainability"}, {Pattern: "SIC_*", Family: "performance"}, {Pattern: "DM_*", Family: "performance"},
			{Pattern: "SBSC_*", Family: "performance"}, {Pattern: "WMI_*", Family: "performance"},
			{Pattern: "NM_*", Family: "style"},
		},
	}
)
