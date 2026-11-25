package opengrep

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
)

// ruleFamilies maps the last part of a rule ID (codeye.<language>.<rule>)
// to its family and the cap the table holds it to
// (docs/ANALYZERS.md#기본-심각도):
//   - command, SQL, path, code evaluation, deserialization and HTML output
//     put request bytes into a command, query, file name, code or page:
//     injection, the family bandit, SpotBugs, clang-tidy and ESLint use for
//     the same constructs, so triage groups their same-line reports;
//   - SSRF lets the request choose the target of an outbound request; what
//     that exposes depends on what the server can reach, so it is a
//     security weakness and not merged with another tool's rule.
//
// The caps hold every rule below its family: high recall without false
// alarms was measured only on single-file benchmark code, and on 27 real
// applications the pack raised 10 diagnostics, none a real flow (9 false
// alarms, 1 harmless by design). Injection rules are therefore at most
// medium and SSRF at most low; raising a cap needs report-only evidence on
// real applications. A triage problem verdict that verify supports still
// yields a model finding with its own severity.
var ruleFamilies = map[string]severity.Rule{
	"command-injection":      {Family: "injection", Cap: severity.Medium},
	"sql-injection":          {Family: "injection", Cap: severity.Medium},
	"path-traversal":         {Family: "injection", Cap: severity.Medium},
	"code-evaluation":        {Family: "injection", Cap: severity.Medium},
	"unsafe-deserialization": {Family: "injection", Cap: severity.Medium},
	"html-injection":         {Family: "injection", Cap: severity.Medium},
	"ssrf":                   {Family: "security_weakness", Cap: severity.Low},
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
		if r, ok := ruleFamilies[id[strings.LastIndex(id, ".")+1:]]; ok {
			r.Pattern = id
			out = append(out, r)
		}
	}
	return out
}
