package report

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// W20: the real cargo check and clippy envelopes (examples/observations-
// cargo-check.json, observations-clippy.json, captured from cargo 1.99.1 and
// clippy 0.1.99) decode through the generic analyzer decoder like the other
// packs'. The rule context honors the repository configuration and in-source
// suppressions (docs/ANALYZERS.md#rust-규칙-문맥과-억제).

func rustDoc(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../examples/observations-" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAnalyzerDecoderRustPack(t *testing.T) {
	check := taskSpec{key: "cargo-check", status: "succeeded", required: true, doc: rustDoc(t, "cargo-check")}
	clippy := taskSpec{key: "clippy", status: "succeeded", required: false, doc: rustDoc(t, "clippy")}
	nCheck, nClippy := len(check.doc["observations"].([]any)), len(clippy.doc["observations"].([]any))
	f := analyzerFixture(t, nil, check, clippy)
	r := build(t, f.in, DefaultRegistry())
	if r.Summary.RawObservations != nCheck+nClippy || r.Summary.TotalFindings != nCheck+nClippy || r.Summary.ActionableFindings != 0 {
		t.Fatalf("summary %+v (check %d clippy %d)", r.Summary, nCheck, nClippy)
	}
	for _, x := range r.Findings {
		if x.Validation != "unreviewed" || x.Severity != "low" || x.Disposition != "open" || !ValidFingerprint(x.Fingerprint) || x.FingerprintBasis == nil {
			t.Fatalf("tool finding %+v", x)
		}
		// W23 identity: the shared ToolFingerprint with the analyzer ID.
		basis := x.FingerprintBasis
		if basis.Analyzer != "rust.cargocheck" && basis.Analyzer != "rust.clippy" || basis.Producer != ProducerTool {
			t.Fatalf("fingerprint basis %+v", basis)
		}
		want, _ := ToolFingerprint(basis.Analyzer, basis.Rule, x.Locations[0].Path, "", strings.TrimPrefix(x.Title, basis.Analyzer+" "+basis.Rule+": "))
		if len(x.Title) < 160 && x.Fingerprint != want {
			t.Fatalf("fingerprint %s, want %s", x.Fingerprint, want)
		}
	}
	for key, want := range map[string]struct {
		analyzer string
		pack     string
		required bool
	}{"cargo-check": {"rust.cargocheck", "codeye-rust-cargo-check", true}, "clippy": {"rust.clippy", "codeye-rust-clippy", false}} {
		row := toolRow(r, key)
		if row == nil || row.Analyzer != want.analyzer || row.ExitClass != "success_with_findings" || row.ProcessedFiles == nil || *row.ProcessedFiles != 3 || row.RuleContext == nil {
			t.Fatalf("%s row %+v", key, row)
		}
		c := row.RuleContext
		if c.RulePack != want.pack || c.RepositoryConfig != "honored" || c.SourceSuppressions != "honored" || len(c.UnreportedSuppressionRules) != 0 || c.Config != ConfigRecorded || c.ConfigDigest == "" {
			t.Fatalf("%s rule context %+v", key, c)
		}
	}
	// The union counts the files both analyzers processed once.
	if r.Coverage.ToolProcessedFiles == nil || *r.Coverage.ToolProcessedFiles != 3 {
		t.Fatalf("processed union %v", r.Coverage.ToolProcessedFiles)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(Project(r, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Observations {
		if strings.Contains(string(b), *o.RawMessage) {
			t.Fatalf("code-free export carries %q", *o.RawMessage)
		}
	}
}

// A configuration the pack could not enumerate (Cargo configuration outside
// the snapshot) is recorded as unrecorded: the comparison never resolves a
// finding on it.
func TestAnalyzerDecoderRustUnenumeratedConfig(t *testing.T) {
	check := taskSpec{key: "cargo-check", status: "succeeded", required: true, doc: rustDoc(t, "cargo-check")}
	check.doc["rule_pack"].(map[string]any)["config_files"] = nil
	check.doc["limitations"] = append(check.doc["limitations"].([]any), "repository_config_not_enumerated")
	slices.SortFunc(check.doc["limitations"].([]any), func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
	r := build(t, analyzerFixture(t, nil, check).in, DefaultRegistry())
	row := toolRow(r, "cargo-check")
	if row == nil || row.RuleContext == nil || row.RuleContext.Config != ConfigUnrecorded || row.RuleContext.ConfigDigest != "" {
		t.Fatalf("row %+v", row)
	}
}

// A task the node did not run (no repository-code grant) is a not-run row with
// a known zero: its findings are never inspected (docs/ANALYZERS.md#저장소-코드-실행-허가).
func TestAnalyzerDecoderRustNotRun(t *testing.T) {
	d := rustDoc(t, "cargo-check")
	d["adapter"] = map[string]any{"id": "codeye-not-run", "version": "1"}
	d["parser"] = map[string]any{"id": "codeye-not-run", "version": "1"}
	d["outcome"] = map[string]any{"task_status": "unsupported", "reason": "repository_code_not_allowed", "exit_class": "not_run", "partial_parse": false}
	d["executions"], d["observations"], d["tool_errors"] = []any{}, []any{}, []any{}
	d["tool"] = map[string]any{"name": "cargo", "version": nil, "version_tested": false}
	d["target"] = map[string]any{"platform": nil, "properties": map[string]any{}}
	d["rule_pack"] = map[string]any{"id": "none", "version": "1", "selection": []any{}, "repository_config": "none", "source_suppressions": "none", "config_files": []any{}, "unreported_suppression_rules": []any{}}
	cov := d["coverage"].(map[string]any)
	cov["processed_paths"], cov["files_skipped"], cov["files_unlisted"], cov["units"] = []any{}, 0, 0, map[string]any{}
	d["parse"] = map[string]any{"ok": false, "records": 0, "unparsed_bytes": 0, "shape_errors": 0, "observations": 0, "unresolved_observations": 0, "tool_errors": 0}
	d["limitations"] = []any{"repository_code_not_allowed"}
	task := taskSpec{key: "cargo-check", status: "unsupported", required: true, doc: d}
	r := build(t, analyzerFixture(t, nil, task).in, DefaultRegistry())
	row := toolRow(r, "cargo-check")
	if row == nil || row.ExitClass != "not_run" || row.ProcessedFiles == nil || *row.ProcessedFiles != 0 || r.Summary.RawObservations != 0 || !slices.Contains(r.Limitations, LimRequiredTaskIncomplete) || r.Status == StatusCompleted {
		t.Fatalf("row %+v summary %+v status %s limitations %v", row, r.Summary, r.Status, r.Limitations)
	}
}
