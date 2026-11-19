package report

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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
		if x.Validation != "unreviewed" || x.Severity != "low" || x.FingerprintBasis == nil {
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
