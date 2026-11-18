package history

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// W19 findings take part in W23 comparison like go vet's: identity from the
// shared report.ToolFingerprint, inspection from the staticcheck row's
// processed paths. A not-configured staticcheck (not_run, a known zero
// without paths) never resolves a vanished finding.
func scRun(run, status, exit string, processed []string, findings ...report.Finding) Side {
	s := vetRun(run, "succeeded", "go1.27.4", nil)
	ctx := scContext()
	tc := report.ToolCoverage{TaskKey: "staticcheck", Analyzer: "go.staticcheck", ToolVersion: strp("2026.2.2 (0.8.2)"), ExitClass: exit, FilesInScope: 1, ProcessedFiles: intp(len(processed)), ProcessedPaths: processed,
		RuleContext: &ctx}
	if exit == "not_run" {
		tc.ToolVersion = nil
	}
	s.Report.Coverage.Tools = append(s.Report.Coverage.Tools, tc)
	s.Report.Tasks = append(s.Report.Tasks, report.Task{Key: "staticcheck", Status: status})
	if findings != nil {
		s.Report.Findings = findings
	}
	return s
}

// scContext is the rule context the staticcheck pack records (builder 4).
func scContext() report.RuleContext {
	return report.RuleContext{RulePack: "codeye-go-staticcheck", RulePackVersion: "2", Selection: []string{"-checks", "SA*,U1000", "-tests=true", "-show-ignored"},
		RepositoryConfig: "overridden", SourceSuppressions: "reported", Config: report.ConfigNotRead, UnreportedSuppressionRules: []string{"U1000"}}
}

func scFinding(id, path string, line int, message string) report.Finding {
	return scRuleFinding(id, "SA4006", path, line, message)
}

func scRuleFinding(id, rule, path string, line int, message string) report.Finding {
	fp, b := report.ToolFingerprint("go.staticcheck", rule, path, "", message)
	return report.Finding{ID: id, Fingerprint: fp, FingerprintBasis: &b, Severity: "low", Validation: "unreviewed", Category: "correctness", Disposition: "open",
		Locations: []report.Location{{Path: path, StartLine: line, EndLine: line}}}
}

// scSuppressed adds the observation of a diagnostic the tool reported as
// suppressed in source (staticcheck -show-ignored, severity "ignored").
func scSuppressed(s Side, path string, line int, message string) Side {
	m := message
	s.Report.Observations = append(s.Report.Observations, report.Observation{ID: "obs_s" + path, Tool: "go.staticcheck", RuleID: "SA4006", RawMessage: &m,
		Location: &report.ObservationLocation{Path: path, StartLine: line, EndLine: line}, Suppressed: true})
	return s
}

func scRow(s *Side) *report.ToolCoverage {
	for i := range s.Report.Coverage.Tools {
		if s.Report.Coverage.Tools[i].Analyzer == "go.staticcheck" {
			return &s.Report.Coverage.Tools[i]
		}
	}
	return nil
}

func TestStaticcheckFindingsCompare(t *testing.T) {
	const m = "this value of err is never used"
	base := scRun("base", "succeeded", "success_with_findings", []string{"a.go"}, scFinding("f1", "a.go", 5, m))
	// Moved two lines: the same identity persists.
	c := Compare(scRun("cur", "succeeded", "success_with_findings", []string{"a.go"}, scFinding("f2", "a.go", 7, m)), base)
	if c.Counts[StatePersisting] != 1 {
		t.Fatalf("persisting %v", c.Counts)
	}
	// Re-checked by a successful staticcheck that processed a.go with the same
	// rule context: resolved.
	c = Compare(scRun("cur", "succeeded", "success_without_findings", []string{"a.go"}), base)
	if e := only(t, c, StateResolvedObserved); e.Reason != nil {
		t.Fatalf("resolved %+v", e)
	}
	// Staticcheck not configured on the new node: absent, not resolved.
	c = Compare(scRun("cur", "unsupported", "not_run", nil), base)
	if e := only(t, c, StateAbsentUnverified); reason(e) == "" || c.Counts[StateResolvedObserved] != 0 {
		t.Fatalf("not configured %+v %v", e, c.Counts)
	}
}

// TC-104: a vanished tool finding is resolved_observed only when the rule
// context (rule pack, version, selection, repository configuration, in-source
// suppression handling and, where configuration is honored, the digest of the
// files read) is known and equal on both sides, and the diagnostic was not
// reported as suppressed.
func TestTC104RuleContextGatesResolution(t *testing.T) {
	const m = "this value of err is never used"
	base := func() Side {
		return scRun("base", "succeeded", "success_with_findings", []string{"a.go"}, scFinding("f1", "a.go", 5, m))
	}
	fixed := func() Side { return scRun("cur", "succeeded", "success_without_findings", []string{"a.go"}) }
	honored := func(s Side, cfg string) Side {
		c := scRow(&s).RuleContext
		c.RepositoryConfig, c.Config = "honored", report.ConfigRecorded
		c.ConfigPaths, c.ConfigDigest = []string{"staticcheck.conf"}, strings.Repeat(cfg, 64)
		return s
	}
	for _, tc := range []struct {
		name      string
		cur, base Side
		want      string
	}{
		{"builder 3 staticcheck baseline has no context", fixed(), func() Side {
			s := base()
			s.Report.Builder.Version = "3"
			scRow(&s).RuleContext = nil
			return s
		}(), ReasonRuleContextUnknown},
		{"current row without context", func() Side { s := fixed(); scRow(&s).RuleContext = nil; return s }(), base(), ReasonRuleContextUnknown},
		{"selection changed", func() Side {
			s := fixed()
			scRow(&s).RuleContext.Selection = []string{"-checks", "SA*", "-tests=true", "-show-ignored"}
			return s
		}(), base(), ReasonRuleContextChanged},
		{"rule pack version changed", func() Side { s := fixed(); scRow(&s).RuleContext.RulePackVersion = "3"; return s }(), base(), ReasonRuleContextChanged},
		{"repository configuration now honored", honored(fixed(), "a"), base(), ReasonRuleContextChanged},
		{"configuration files changed", honored(fixed(), "b"), honored(base(), "a"), ReasonRuleContextChanged},
		{"honored configuration not recorded", func() Side {
			s := honored(fixed(), "a")
			c := scRow(&s).RuleContext
			c.Config, c.ConfigPaths, c.ConfigDigest = report.ConfigUnrecorded, nil, ""
			return s
		}(), honored(base(), "a"), ReasonRuleContextUnknown},
		// The finding vanished because the diagnostic is now suppressed in
		// source: the tool reported it as suppressed.
		{"suppressed in source", scSuppressed(fixed(), "a.go", 6, m), base(), ReasonSuppressedInSource},
		// The direct observation wins over a version change.
		{"suppressed and version changed", func() Side {
			s := scSuppressed(fixed(), "a.go", 6, m)
			scRow(&s).ToolVersion = strp("2026.3")
			return s
		}(), base(), ReasonSuppressedInSource},
		// A tool that hides suppressed diagnostics cannot show the finding
		// was fixed rather than suppressed.
		{"suppressions honored, not reported", func() Side { s := fixed(); scRow(&s).RuleContext.SourceSuppressions = "honored"; return s }(), func() Side {
			s := base()
			scRow(&s).RuleContext.SourceSuppressions = "honored"
			return s
		}(), ReasonSuppressionsHidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Compare(tc.cur, tc.base)
			e := only(t, c, StateAbsentUnverified)
			if reason(e) != tc.want || c.Counts[StateResolvedObserved] != 0 {
				t.Fatalf("reason %q want %q counts %v", reason(e), tc.want, c.Counts)
			}
		})
	}
	// U1000 hides its suppressions even with -show-ignored (measured): a
	// vanished U1000 finding is never resolved, while an SA finding of the
	// same run is.
	u := scRun("base", "succeeded", "success_with_findings", []string{"a.go"}, scRuleFinding("f1", "U1000", "a.go", 5, "func unusedX is unused"), scFinding("f2", "a.go", 9, m))
	c := Compare(fixed(), u)
	for _, e := range c.Entries {
		want := map[string]string{"U1000": ReasonSuppressionsHidden, "SA4006": ""}[e.Rule]
		if reason(e) != want || (want == "" && e.State != StateResolvedObserved) {
			t.Fatalf("%s: %s %q", e.Rule, e.State, reason(e))
		}
	}
	// Equal recorded configuration resolves.
	if e := only(t, Compare(honored(fixed(), "a"), honored(base(), "a")), StateResolvedObserved); e.Reason != nil {
		t.Fatalf("equal honored %+v", e)
	}
	// A suppressed diagnostic of another message does not block resolution.
	if e := only(t, Compare(scSuppressed(fixed(), "a.go", 6, "other"), base()), StateResolvedObserved); e.Reason != nil {
		t.Fatalf("unrelated suppression %+v", e)
	}
	// Removing the suppression shows the diagnostic again: new, and the
	// baseline could not have seen it as a finding.
	sup := scSuppressed(scRun("base", "succeeded", "success_without_findings", []string{"a.go"}), "a.go", 5, m)
	c = Compare(scRun("cur", "succeeded", "success_with_findings", []string{"a.go"}, scFinding("f2", "a.go", 5, m)), sup)
	if e := only(t, c, StateNew); reason(e) != ReasonSuppressedInSource {
		t.Fatalf("unsuppressed %+v", e)
	}
	// The difference is shown with the analyzer.
	c = Compare(func() Side { s := fixed(); scRow(&s).RuleContext.RulePackVersion = "3"; return s }(), base())
	if !slices.ContainsFunc(c.Differences, func(d Difference) bool {
		return d.Key == "tool:go.staticcheck:rule_context" && d.Baseline != nil && strings.Contains(*d.Baseline, "codeye-go-staticcheck@2") && d.Current != nil && strings.Contains(*d.Current, "@3")
	}) {
		t.Fatalf("differences %+v", c.Differences)
	}
}

// The go vet document has no rule pack: the builder-4 decoder writes the
// synthesized context on the row, and a builder-3 row without one gets the
// same context in the comparison. So builder-3 and builder-4 go vet
// revisions still resolve (the W23 three-run smoke) and show no rule-context
// difference.
func TestGoVetRuleContextIsSynthesized(t *testing.T) {
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go"}, vetFinding("f1", "a.go", 5, msg))
	base.Report.Builder.Version = "3"
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
	rc := report.GoVetRuleContext()
	cur.Report.Coverage.Tools[0].RuleContext = &rc
	c := Compare(cur, base)
	if e := only(t, c, StateResolvedObserved); e.Reason != nil {
		t.Fatalf("go vet %+v", e)
	}
	for _, d := range c.Differences {
		if d.Key == "tool:go.vet:rule_context" {
			t.Fatalf("synthesized context differs %+v", d)
		}
	}
}
