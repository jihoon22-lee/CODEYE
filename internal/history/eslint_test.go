package history

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

const (
	typedESLint        = "javascript.eslint_typed"
	tsPromisePath      = "packages/backend/src/queue.ts"
	jsPromisePath      = "packages/browser/src/request.js"
	promiseRejectRule  = "@typescript-eslint/prefer-promise-reject-errors"
	promiseRejectError = "Expected the Promise rejection reason to be an Error."
)

func promiseFinding(id, path string, line int) report.Finding {
	fp, basis := report.ToolFingerprint(typedESLint, promiseRejectRule, path, "", promiseRejectError)
	return report.Finding{ID: id, Fingerprint: fp, FingerprintBasis: &basis, Disposition: "open",
		Severity: "low", Validation: "unreviewed", Category: "maintainability",
		Locations: []report.Location{{Path: path, StartLine: line, EndLine: line}}}
}

func typedESLintRow(key, path string) report.ToolCoverage {
	config := &report.RuleContext{
		RulePack: "codeye-ts-eslint-typed", RulePackVersion: "1", SourceSuppressions: "disabled",
		RepositoryConfig: "honored", Config: report.ConfigRecorded,
		ConfigPaths: []string{"packages/backend/tsconfig.json"}, ConfigDigest: strings.Repeat("a", 64),
	}
	if path == jsPromisePath {
		config = &report.RuleContext{RulePack: "codeye-js-eslint-rejection-types", RulePackVersion: "1",
			SourceSuppressions: "disabled", RepositoryConfig: "overridden", Config: report.ConfigNotRead}
	}
	return report.ToolCoverage{TaskKey: key, Analyzer: typedESLint, ToolVersion: strp("10.0.1"),
		ExitClass: "success_with_findings", FilesInScope: 1, ProcessedFiles: intp(1), ProcessedPaths: []string{path}, RuleContext: config}
}

func typedRun(run, version string, findings []report.Finding, rows ...report.ToolCoverage) Side {
	s := vetRun(run, "succeeded", "go1.27.4", nil, findings...)
	s.Report.Builder.Version = version
	s.Report.Coverage.Tools = rows
	s.Report.Tasks = nil
	for _, row := range rows {
		task := report.Task{Key: row.TaskKey, Status: "succeeded"}
		if row.ExitClass == "not_run" {
			task.Status, task.Reason = "skipped_policy", strp("javascript_type_input_budget_exceeded")
		}
		s.Report.Tasks = append(s.Report.Tasks, task)
	}
	return s
}

func TestTypedESLintUnrelatedManagedJavaScriptDoesNotMaskTypeScriptChanges(t *testing.T) {
	ts := typedESLintRow("eslint-typed", tsPromisePath)
	fresh := promiseFinding("new-ts", "packages/backend/src/other.ts", 40)
	ts.ProcessedPaths, ts.ProcessedFiles, ts.FilesInScope = append(ts.ProcessedPaths, fresh.Locations[0].Path), intp(2), 2
	refreshedTS := ts
	refreshedTS.TaskKey = "eslint-typed.m01"
	managedJS := typedESLintRow("eslint-typed", jsPromisePath)
	skippedJS := managedJS
	skippedJS.ExitClass, skippedJS.FilesInScope = "not_run", 49
	skippedJS.ToolVersion, skippedJS.ProcessedPaths, skippedJS.ProcessedFiles = nil, nil, intp(0)

	base := typedRun("base", "14", []report.Finding{promiseFinding("old-ts", tsPromisePath, 20)}, ts)

	for _, test := range []struct {
		name string
		row  report.ToolCoverage
	}{
		{"skipped JS module exceeds 48 file budget", skippedJS},
		{"successful JS supplement has a distinct rule pack", managedJS},
	} {
		t.Run(test.name, func(t *testing.T) {
			cur := typedRun("cur", report.BuilderVersion, []report.Finding{fresh}, test.row, refreshedTS)
			comparison := Compare(cur, base)
			if entry := only(t, comparison, StateResolvedObserved); entry.Baseline[0].FindingID != "old-ts" || entry.Reason != nil {
				t.Fatalf("resolved entry %+v", entry)
			}
			if entry := only(t, comparison, StateNew); entry.Current[0].FindingID != "new-ts" || entry.Reason != nil {
				t.Fatalf("new entry %+v", entry)
			}
			// Run-level differences still report all planned analyzer rows.
			hasRuleDifference := false
			for _, difference := range comparison.Differences {
				hasRuleDifference = hasRuleDifference || difference.Key == "tool:"+typedESLint+":rule_context"
			}
			if !hasRuleDifference {
				t.Fatal("the run-level rule context change was lost")
			}
		})
	}
}

func TestTypedESLintPathChangesStayUnverifiedWhenCoverageOrRulesDiffer(t *testing.T) {
	ts := typedESLintRow("eslint-typed", tsPromisePath)
	js := typedESLintRow("eslint-typed.m01", jsPromisePath)
	skippedJS := js
	skippedJS.ExitClass, skippedJS.FilesInScope = "not_run", 49
	skippedJS.ToolVersion, skippedJS.ProcessedFiles, skippedJS.ProcessedPaths = nil, intp(0), nil
	old := typedRun("base", report.BuilderVersion, []report.Finding{promiseFinding("old-ts", tsPromisePath, 20), promiseFinding("old-js", jsPromisePath, 20)}, ts, js)
	cur := typedRun("cur", report.BuilderVersion, nil, ts, skippedJS)
	comparison := Compare(cur, old)
	resolved := only(t, comparison, StateResolvedObserved)
	if resolved.Baseline[0].FindingID != "old-ts" {
		t.Fatalf("unexpected resolved entry %+v", resolved)
	}
	unverified := only(t, comparison, StateAbsentUnverified)
	if unverified.Baseline[0].FindingID != "old-js" || reason(unverified) != ReasonCheckFailed {
		t.Fatalf("unchecked JS issue %+v", unverified)
	}

	baseTS := typedRun("base", "14", []report.Finding{promiseFinding("old-ts", tsPromisePath, 20)}, ts)
	changedTS := typedESLintRow(ts.TaskKey, tsPromisePath)
	changedTS.RuleContext.ConfigDigest = strings.Repeat("b", 64)
	legacyTS := ts
	legacyTS.ProcessedPaths = nil
	partialRow := js
	partialRow.ProcessedFiles = intp(2) // one additional path was not recorded
	for _, test := range []struct {
		name  string
		cur   Side
		other Side
		want  string
	}{
		{"actual TypeScript configuration changed", typedRun("cur", report.BuilderVersion, nil, changedTS, skippedJS), baseTS, ReasonRuleContextChanged},
		{"current TypeScript path missing", typedRun("cur", report.BuilderVersion, nil, legacyTS, js), baseTS, ReasonLocationNotChecked},
		{"legacy baseline without processed paths keeps broad context", typedRun("cur", report.BuilderVersion, nil, ts, skippedJS),
			typedRun("base", "14", baseTS.Report.Findings, legacyTS), ReasonRulesUnknown},
		{"possibly covering row has incomplete paths and different context", typedRun("cur", report.BuilderVersion, nil, ts, partialRow), baseTS, ReasonRuleContextChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := only(t, Compare(test.cur, test.other), StateAbsentUnverified)
			if reason(entry) != test.want {
				t.Fatalf("reason %s, want %s", reason(entry), test.want)
			}
		})
	}
}

func TestTypedESLintRawObservationPreventsFalseResolutionWhenCoreFindingReplacesIt(t *testing.T) {
	const core = "javascript.eslint"
	const coreRule = "prefer-promise-reject-errors"
	typed := typedESLintRow("eslint-typed", jsPromisePath)
	baseline := typedRun("base", report.BuilderVersion, []report.Finding{promiseFinding("old-js", jsPromisePath, 20)}, typed)
	baseline.Report.Tasks = append(baseline.Report.Tasks, report.Task{Key: "eslint", Status: "failed"})
	coreRow := typedESLintRow("eslint", jsPromisePath)
	coreRow.Analyzer = core
	coreFinding := promiseFinding("core-js", jsPromisePath, 20)
	fp, basis := report.ToolFingerprint(core, coreRule, jsPromisePath, "", promiseRejectError)
	coreFinding.Fingerprint, coreFinding.FingerprintBasis = fp, &basis
	current := typedRun("cur", report.BuilderVersion, []report.Finding{coreFinding}, typed, coreRow)
	typedObservation := report.Observation{TaskKey: typed.TaskKey, Tool: typedESLint, RuleID: promiseRejectRule,
		RawMessage: strp(promiseRejectError), EvidenceState: report.EvidenceVerified,
		Location: &report.ObservationLocation{Path: jsPromisePath, StartLine: 20, EndLine: 20, StartCol: 9, EndCol: 40}}
	coreObservation := typedObservation
	coreObservation.TaskKey, coreObservation.Tool, coreObservation.RuleID = coreRow.TaskKey, core, coreRule
	current.Report.Observations = []report.Observation{typedObservation, coreObservation}
	comparison := Compare(current, baseline)
	entry := only(t, comparison, StateAbsentUnverified)
	if entry.Baseline[0].FindingID != "old-js" || reason(entry) != ReasonActiveObservation || comparison.Counts[StateResolvedObserved] != 0 {
		t.Fatalf("still-reported typed diagnostic was marked resolved: %+v", comparison)
	}
	if survived := only(t, comparison, StateNew); survived.Current[0].FindingID != "core-js" {
		t.Fatalf("visible core diagnostic was lost: %+v", survived)
	}
	current.Report.Coverage.Tools[0].ToolVersion = strp("10.1.0")
	if entry := only(t, Compare(current, baseline), StateAbsentUnverified); reason(entry) != ReasonActiveObservation {
		t.Fatalf("direct observation did not take precedence over the tool version: %+v", entry)
	}
	current.Report.Coverage.Tools[0].ToolVersion = typed.ToolVersion
	current.Report.Observations = []report.Observation{coreObservation}
	if entry := only(t, Compare(current, baseline), StateResolvedObserved); entry.Baseline[0].FindingID != "old-js" {
		t.Fatalf("an unrelated raw rule prevented resolution: %+v", entry)
	}
	typedObservation.TaskKey = "eslint-typed.unaccepted"
	current.Report.Observations = append(current.Report.Observations, typedObservation)
	if entry := only(t, Compare(current, baseline), StateResolvedObserved); entry.Baseline[0].FindingID != "old-js" {
		t.Fatalf("an unaccepted row prevented resolution: %+v", entry)
	}
}
