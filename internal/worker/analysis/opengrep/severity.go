package opengrep

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
)

// ruleFamilies maps the last part of a rule ID (codeye.<language>.<rule>)
// to its family (docs/ANALYZERS.md#기본-심각도). Every rule reports a flow
// from request input to a dangerous call, so none is a style note:
//   - command, SQL, path, code evaluation, deserialization and HTML output
//     put request bytes into a command, query, file name, code or page:
//     injection (high), the family bandit, SpotBugs, clang-tidy and ESLint
//     use for the same constructs, so triage groups their same-line reports;
//   - SSRF lets the request choose the target of an outbound request; what
//     that exposes depends on what the server can reach, so it is a
//     security weakness (medium) and not merged with another tool's rule.
var ruleFamilies = map[string]string{
	"command-injection":      "injection",
	"sql-injection":          "injection",
	"path-traversal":         "injection",
	"code-evaluation":        "injection",
	"unsafe-deserialization": "injection",
	"html-injection":         "injection",
	"ssrf":                   "security_weakness",
}

// Severity is the default-severity table of the codeye-taint rule pack:
// one entry per rule of the pack, so a rule added without a family keeps
// low as unknown (TestSeverityCoversEveryRule).
var Severity = severity.Table{
	Analyzer: AnalyzerID, RulePack: RulePackID, RulePackVersions: []string{RulePackVersion}, Version: "1",
	Rules: severityRules(),
}

func severityRules() []severity.Rule {
	var out []severity.Rule
	for _, id := range RuleIDs() {
		if f, ok := ruleFamilies[id[strings.LastIndex(id, ".")+1:]]; ok {
			out = append(out, severity.Rule{Pattern: id, Family: f})
		}
	}
	return out
}
