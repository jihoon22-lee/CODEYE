package report

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/eslint"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The real ESLint and tsc envelopes (docs/ANALYZERS.md; examples/, and the
// tsc solution run with .rNN executions in testdata/) go through the analyzer
// decoder: observations bound to the recorded stdout, unreviewed tool
// findings with the shared ToolFingerprint, Required as recorded.

func readAnalyzerDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAnalyzerDecoderJSTS(t *testing.T) {
	es := taskSpec{key: "eslint.m02", status: "succeeded", required: true, doc: readAnalyzerDoc(t, "../../examples/observations-eslint.json")}
	tc := taskSpec{key: "tsc.m01", status: "succeeded", required: false, doc: readAnalyzerDoc(t, "../../examples/observations-tsc.json")}
	sol := taskSpec{key: "tsc.m02", status: "succeeded", required: false, doc: readAnalyzerDoc(t, "testdata/tsc-solution.json")}
	sol.doc["module"].(map[string]any)["dir"] = "."
	f := analyzerFixture(t, nil, es, tc, sol)
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if r.Summary.RawObservations != 5+1+1 || r.Summary.TotalFindings != 7 || r.Summary.ActionableFindings != 0 {
		t.Fatalf("summary %+v", r.Summary)
	}
	for _, x := range r.Findings {
		if x.Validation != "unreviewed" || !defaultSeverityOK(x) || x.FingerprintBasis == nil {
			t.Fatalf("tool finding %+v", x)
		}
		analyzer := x.FingerprintBasis.Analyzer
		want, _ := ToolFingerprint(analyzer, x.FingerprintBasis.Rule, x.Locations[0].Path, "", strings.TrimPrefix(x.Title, analyzer+" "+x.FingerprintBasis.Rule+": "))
		if len(x.Title) < 160 && x.Fingerprint != want {
			t.Fatalf("fingerprint %s %+v", x.Fingerprint, x.FingerprintBasis)
		}
	}
	bound := map[string]string{"javascript.eslint": f.stdout["eslint.m02/eslint.check"], "javascript.tsc": ""}
	for _, o := range r.Observations {
		if o.EvidenceState != EvidenceVerified || o.ToolVersion == nil {
			t.Fatalf("observation %+v", o)
		}
		if want := bound[o.Tool]; want != "" && o.RawRef.ArtifactID != want {
			t.Fatalf("eslint observation bound to %s, want %s", o.RawRef.ArtifactID, want)
		}
		if o.Tool == "javascript.tsc" && o.RawRef.ArtifactID != f.stdout["tsc.m01/tsc.check"] && o.RawRef.ArtifactID != f.stdout["tsc.m02/tsc.check.r01"] {
			t.Fatalf("tsc observation bound to %s", o.RawRef.ArtifactID)
		}
	}
	for key, analyzer := range map[string]string{"eslint.m02": "javascript.eslint", "tsc.m01": "javascript.tsc", "tsc.m02": "javascript.tsc"} {
		row := toolRow(r, key)
		if row == nil || row.Analyzer != analyzer || row.ExitClass != "success_with_findings" || row.ProcessedFiles == nil || *row.ProcessedFiles == 0 {
			t.Fatalf("%s row %+v", key, row)
		}
	}
	if rc := toolRow(r, "eslint.m02").RuleContext; rc == nil || rc.RulePackVersion != "1" {
		t.Fatalf("older ESLint context changed: %+v", rc)
	}
	if has(r.Limitations, "artifact_rejected") {
		t.Fatalf("limitations %v", r.Limitations)
	}
	// A Required mismatch between the task and the envelope is rejected.
	bad := taskSpec{key: "eslint.m02", status: "succeeded", required: false, doc: readAnalyzerDoc(t, "../../examples/observations-eslint.json")}
	r = build(t, analyzerFixture(t, nil, bad).in, DefaultRegistry())
	if !has(r.Limitations, "artifact_rejected:eslint.m02") || len(r.Observations) != 0 {
		t.Fatalf("required mismatch accepted: %v", r.Limitations)
	}
}

func TestESLintPromiseRejectionGuidance(t *testing.T) {
	const rawMessage = "Expected the Promise rejection reason to be an Error."
	es := taskSpec{key: "eslint.m02", status: "succeeded", required: true, doc: readAnalyzerDoc(t, "../../examples/observations-eslint.json")}
	pack := es.doc["rule_pack"].(map[string]any)
	pack["version"] = "2"
	pack["selection"] = append(pack["selection"].([]any), "prefer-promise-reject-errors warn (JS files, allowEmptyReject=true)")
	record := es.doc["observations"].([]any)[0].(map[string]any)
	record["rule_id"], record["category"], record["raw_message"], record["raw_severity"] =
		"prefer-promise-reject-errors", "error_handling", rawMessage, "warning"
	id := record["id"].(string)

	r := build(t, analyzerFixture(t, nil, es).in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if r.Summary.ActionableFindings != 0 {
		t.Fatalf("the tool warning is unreviewed: %+v", r.Summary)
	}
	if r.Observations[0].ID != id || *r.Observations[0].RawMessage != rawMessage || *r.Observations[0].RawSeverity != "warning" {
		t.Fatalf("raw diagnostics changed: %+v", r.Observations[0])
	}
	for _, withCode := range []bool{true, false} {
		report := Project(r, withCode)
		var found bool
		for _, x := range report.Findings {
			if x.ObservationIDs[0] != id {
				continue
			}
			found = true
			if !defaultSeverityOK(x) || x.Severity != "medium" || x.Validation != "unreviewed" || x.Impact != textRejectImpact ||
				x.Recommendation.Summary != textRejectAction || len(x.Verification) != 1 || x.Verification[0] != textRejectVerification {
				t.Fatalf("warning guidance %+v", x)
			}
		}
		if !found {
			t.Fatal("rejection finding missing")
		}
		summary := renderMarkdownSummary(report, "", Korean)
		if !strings.Contains(string(summary), mdText(Korean.Label("rule.promise_rejection"))) {
			t.Fatal("rule explanation missing from summary")
		}
	}
}

func TestManagedJavaScriptRejectionsDeduplicateOnlyAcceptedBaseline(t *testing.T) {
	const baselineRule = "prefer-promise-reject-errors"
	const supplementRule = "@typescript-eslint/" + baselineRule
	makeTask := func(t *testing.T, key, analyzer, rule, pack string, required bool, col int) taskSpec {
		d := readAnalyzerDoc(t, "../../examples/observations-eslint.json")
		d["analyzer"] = map[string]any{"id": analyzer, "ecosystem": "javascript", "required": required}
		d["rule_pack"].(map[string]any)["id"] = pack
		d["parse"].(map[string]any)["observations"] = 1
		o := d["observations"].([]any)[0].(map[string]any)
		d["observations"] = []any{o}
		o["rule_id"], o["raw_message"] = rule, "Expected the Promise rejection reason to be an Error."
		o["location"].(map[string]any)["start_col"] = col
		if !required {
			o["id"] = "obs_0123456789abcdef0123"
		}
		return taskSpec{key: key, status: "succeeded", required: required, doc: d}
	}
	for _, tc := range []struct {
		name, rule                     string
		col                            int
		line                           int
		baselineUnavailable            bool
		baselineFailed                 bool
		wantObservations, wantFindings int
		wantSupplement                 bool
	}{
		{name: "accepted matching observation", rule: baselineRule, col: 7, wantObservations: 2, wantFindings: 1},
		{name: "unavailable baseline", rule: baselineRule, col: 7, baselineUnavailable: true, wantObservations: 1, wantFindings: 1, wantSupplement: true},
		{name: "failed baseline with a partial observation", rule: baselineRule, col: 7, baselineFailed: true, wantObservations: 2, wantFindings: 2, wantSupplement: true},
		{name: "different column", rule: baselineRule, col: 8, wantObservations: 2, wantFindings: 2, wantSupplement: true},
		{name: "different line", rule: baselineRule, col: 7, line: 4, wantObservations: 2, wantFindings: 2, wantSupplement: true},
		{name: "different rule", rule: "no-unused-vars", col: 7, wantObservations: 2, wantFindings: 2, wantSupplement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := makeTask(t, "eslint.m02", eslint.AnalyzerID, tc.rule, eslint.RulePackID, true, tc.col)
			typed := makeTask(t, "eslint-typed.m02", eslint.TypedAnalyzerID, supplementRule, eslint.ManagedJSRulePackID, false, 7)
			if tc.line > 0 {
				loc := base.doc["observations"].([]any)[0].(map[string]any)["location"].(map[string]any)
				loc["start_line"], loc["end_line"] = tc.line, tc.line
			}
			if tc.baselineUnavailable || tc.baselineFailed {
				base.status = "failed"
			}
			if tc.baselineUnavailable {
				base.doc = nil
			} else if tc.baselineFailed {
				base.doc["outcome"] = map[string]any{"task_status": "failed", "reason": "output_truncated", "exit_class": "execution_error", "partial_parse": true}
				base.doc["coverage"].(map[string]any)["processed_paths"] = nil
				base.doc["parse"].(map[string]any)["ok"] = false
			}
			r := build(t, analyzerFixture(t, nil, base, typed).in, DefaultRegistry())
			if r.Summary.RawObservations != tc.wantObservations || r.Summary.TotalFindings != tc.wantFindings {
				t.Fatalf("summary %+v; limitations %v", r.Summary, r.Limitations)
			}
			gotSupplement := false
			for _, f := range r.Findings {
				gotSupplement = gotSupplement || f.FingerprintBasis.Analyzer == eslint.TypedAnalyzerID
			}
			if gotSupplement != tc.wantSupplement {
				t.Fatalf("supplemental finding present: %t, want %t", gotSupplement, tc.wantSupplement)
			}
			tier := staticTool(t, &r, typed.key)
			if len(tier.ObservationIDs) != 1 || (len(tier.FindingIDs) > 0) != tc.wantSupplement {
				t.Fatalf("supplement counts %+v", tier)
			}
			if !tc.wantSupplement {
				assertManagedRejectionReviewLineage(t, r, tier.ObservationIDs[0])
			}
		})
	}
}

func assertManagedRejectionReviewLineage(t *testing.T, r Report, supplementID string) {
	t.Helper()
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	typed, ok := obs[supplementID]
	if !ok || typed.Location == nil || typed.RawRef.ArtifactID == "" || typed.RawRef.JSONPointer == "" {
		t.Fatalf("the supplemental raw diagnostic was lost: %+v", typed)
	}
	states, _ := exampleStages(t)
	states[typed.TaskKey] = states["go-vet"]
	ref := record.ObservationRef{ID: typed.ID, TaskKey: typed.TaskKey, Tool: typed.Tool, RuleID: typed.RuleID,
		Path: typed.Location.Path, StartLine: typed.Location.StartLine}
	interpret := states["review.interpret"].reviewResult.Record
	interpret.Upstream[0].Source = typed.TaskKey
	interpret.Observations[0] = ref
	verify := states["review.verify"].reviewResult.Record
	verify.Observations[0] = ref
	reviewed, _, limits := compose(states, r)
	if len(limits) != 0 || !slices.ContainsFunc(reviewed.Findings, func(f Finding) bool {
		return f.ID == "review_interpret_1" && slices.Equal(f.ObservationIDs, []string{typed.ID})
	}) {
		t.Fatalf("interpret or verify lost the supplemental reference: %v", limits)
	}
}
