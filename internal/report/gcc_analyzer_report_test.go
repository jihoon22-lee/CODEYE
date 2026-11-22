package report

import (
	"strings"
	"testing"
)

func TestGCCAnalyzerPathEvidenceSurvivesReportProjection(t *testing.T) {
	f := analyzerFixture(t, nil, taskSpec{key: "gcc-analyzer", status: "succeeded", doc: readExample(t, "gcc-analyzer")})
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if len(r.Observations) != 1 || r.Summary.ActionableFindings != 0 || len(r.Findings) != 1 ||
		r.Findings[0].Validation != "unreviewed" {
		t.Fatalf("GCC diagnostics and findings: %+v %+v", r.Observations, r.Findings)
	}
	o := r.Observations[0]
	if o.Location == nil || o.Location.StartLine != 8 || o.Tool != "cpp.gcc_analyzer" || len(o.Related) != 6 {
		t.Fatalf("missing GCC flow evidence: %+v", o)
	}
	close := o.Related[4]
	if close.Location == nil || close.Location.StartLine != 7 || close.RawMessage == nil || *close.RawMessage != "closed here" ||
		close.RawRef == nil || close.RawRef.ArtifactID != o.RawRef.ArtifactID {
		t.Fatalf("missing verified close location: %+v", close)
	}
	full, err := RenderHTML(r, "test", Korean)
	if err != nil || !strings.Contains(string(full), "closed here") {
		t.Fatalf("render lost GCC path notes: %v", err)
	}
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	close = free.Observations[0].Related[4]
	if close.Location == nil || close.Location.StartLine != 7 || close.RawMessage != nil || close.Excerpt != nil {
		t.Fatalf("code-free GCC evidence differs: %+v", close)
	}
}
