package history

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// W20 cargo check findings compare like the other packs'. The Rust rule
// context honors the repository's Cargo configuration and in-source
// #[allow]: a vanished finding is never resolved (a suppression hides the
// diagnostic and nothing reports it, docs/ANALYZERS.md#rust-규칙-문맥과-억제), and a context that lists
// the configuration files it read is compared by their digest.
func rsContext(cfg string) report.RuleContext {
	c := report.RuleContext{RulePack: "codeye-rust-cargo-check", RulePackVersion: "1", Selection: []string{"--workspace", "--all-targets"},
		RepositoryConfig: "honored", SourceSuppressions: "honored", Config: report.ConfigRecorded, ConfigPaths: []string{"Cargo.toml"}, ConfigDigest: strings.Repeat(cfg, 64)}
	return c
}

func rsFinding(id, path string, line int, message string) report.Finding {
	fp, b := report.ToolFingerprint("rust.cargocheck", "unused_variables", path, "", message)
	return report.Finding{ID: id, Fingerprint: fp, FingerprintBasis: &b, Severity: "low", Validation: "unreviewed", Category: "maintainability", Disposition: "open",
		Locations: []report.Location{{Path: path, StartLine: line, EndLine: line}}}
}

func rsRun(run, status, exit string, processed []string, ctx *report.RuleContext, findings ...report.Finding) Side {
	s := vetRun(run, "succeeded", "go1.27.4", nil)
	tc := report.ToolCoverage{TaskKey: "cargo-check", Analyzer: "rust.cargocheck", ToolVersion: strp("1.99.1 (5f94df478 2026-08-27)"), ExitClass: exit, FilesInScope: 1,
		ProcessedFiles: intp(len(processed)), ProcessedPaths: processed, RuleContext: ctx}
	if exit == "not_run" {
		tc.ToolVersion = nil
	}
	s.Report.Coverage.Tools = append(s.Report.Coverage.Tools, tc)
	s.Report.Tasks = append(s.Report.Tasks, report.Task{Key: "cargo-check", Status: status})
	if findings != nil {
		s.Report.Findings = findings
	}
	return s
}

func TestRustFindingsCompare(t *testing.T) {
	const m = "unused variable: `x`"
	ctx := func(cfg string) *report.RuleContext { c := rsContext(cfg); return &c }
	base := rsRun("base", "succeeded", "success_with_findings", []string{"src/lib.rs"}, ctx("a"), rsFinding("f1", "src/lib.rs", 5, m))
	// Moved: the same identity persists.
	if c := Compare(rsRun("cur", "succeeded", "success_with_findings", []string{"src/lib.rs"}, ctx("a"), rsFinding("f2", "src/lib.rs", 9, m)), base); c.Counts[StatePersisting] != 1 {
		t.Fatalf("persisting %v", c.Counts)
	}
	clean := func(c *report.RuleContext) Side {
		return rsRun("cur", "succeeded", "success_without_findings", []string{"src/lib.rs"}, c)
	}
	for _, tc := range []struct {
		name string
		cur  Side
		base Side
		want string
	}{
		// Honored suppressions: fixed cannot be told from #[allow]ed.
		{"suppressions are honored, so a vanished finding is never resolved", clean(ctx("a")), base, ReasonSuppressionsHidden},
		{"Cargo configuration changed", clean(ctx("b")), base, ReasonRuleContextChanged},
		{"configuration not enumerated (external Cargo configuration)", func() Side {
			c := ctx("a")
			c.Config, c.ConfigPaths, c.ConfigDigest = report.ConfigUnrecorded, nil, ""
			return clean(c)
		}(), base, ReasonRuleContextUnknown},
		{"baseline without a context (builder 3)", clean(ctx("a")), func() Side {
			s := rsRun("base", "succeeded", "success_with_findings", []string{"src/lib.rs"}, nil, rsFinding("f1", "src/lib.rs", 5, m))
			s.Report.Builder.Version = "3"
			return s
		}(), ReasonRuleContextUnknown},
		{"cargo not run on the new node", rsRun("cur", "unsupported", "not_run", nil, ctx("a")), base, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Compare(tc.cur, tc.base)
			e := only(t, c, StateAbsentUnverified)
			if c.Counts[StateResolvedObserved] != 0 || tc.want != "" && reason(e) != tc.want || reason(e) == "" {
				t.Fatalf("reason %q want %q counts %v", reason(e), tc.want, c.Counts)
			}
		})
	}
}
