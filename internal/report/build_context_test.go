package report

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
)

// addCompileDB appends the C/C++ compile DB generation task: no artifact,
// its limitations only in the result manifest.
func addCompileDB(t *testing.T, f *fixture, status string, reason *string, lims []string) {
	t.Helper()
	id := domain.NewID()
	f.in.Tasks = append(f.in.Tasks, TaskFacts{ID: id, Key: "compile-db", Status: status, Reason: reason, ExecutionOrdinal: 1, Stage: string(domain.StageBuild)})
	var rm analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm); err != nil {
		t.Fatal(err)
	}
	mt := analysis.ManifestTask{TaskID: id, Key: "compile-db", Status: domain.TaskStatus(status), Reason: reason,
		Executions: []analysis.ManifestExecution{}, Artifacts: []analysis.ManifestArtifact{}, Limitations: lims}
	rm.Tasks = append(rm.Tasks, mt)
	f.setManifest(t, rm)
}

// The generation task writes no envelope; its own limitations reach the
// report's limitations from the result manifest, and a generation that did
// not succeed is a static-analysis gap with its reason. Both read as
// sentences in the Korean rendering.
func TestCompileDBTaskLimitationsReachTheReport(t *testing.T) {
	baseline := build(t, newFixture(t, goVet(t)).in, DefaultRegistry())
	f := newFixture(t, goVet(t))
	addCompileDB(t, f, "succeeded", nil, []string{"compile_db_generated", "compile_db_generator_cmake", "repository_code_executed", "workspace_restored_after_task"})
	r := build(t, f.in, DefaultRegistry())
	for _, want := range []string{"compile_db_generated", "repository_code_executed"} {
		if !slices.Contains(r.Limitations, want) {
			t.Fatalf("limitations %v miss %s", r.Limitations, want)
		}
	}
	if slices.Contains(r.Limitations, "compile_db_generator_cmake") || slices.Contains(r.Limitations, "workspace_restored_after_task") {
		t.Fatalf("unrelated task limitations lifted: %v", r.Limitations)
	}
	if r.Status != baseline.Status || r.QualityVerdict != baseline.QualityVerdict || len(subjectGaps(r, "compile-db")) != 0 || len(r.Gaps) != len(baseline.Gaps) {
		t.Fatalf("a succeeded generation changed the report: %s/%s gaps %+v", r.Status, r.QualityVerdict, r.Gaps)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}

	reason := "build_tool_not_configured"
	f = newFixture(t, goVet(t))
	addCompileDB(t, f, "unsupported", &reason, []string{"cmake_not_found"})
	r = build(t, f.in, DefaultRegistry())
	g := subjectGaps(r, "compile-db")
	if len(g) != 1 || g[0].Code != GapCompileDBNotGenerated || *g[0].Subject != "compile-db" || *g[0].Detail != reason || g[0].Section != SectionStaticAnalysis || !slices.Contains(r.Limitations, "cmake_not_found") {
		t.Fatalf("gaps %+v limitations %v", r.Gaps, r.Limitations)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	md := string(RenderMarkdown(r, strings.Repeat("0", 64), Korean))
	for _, want := range []string{`C/C\+\+ compile DB를 만들지 못했다`, "노드 도구 경로에 cmake가 없어"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown misses %q", want)
		}
	}
	// The task appendix lists reason codes for every task by design; the
	// limitation is shown only as its sentence.
	for _, raw := range []string{"cmake_not_found"} {
		if strings.Contains(md, raw) {
			t.Fatalf("markdown shows the raw code %s", raw)
		}
	}
}

func subjectGaps(r Report, subject string) []Gap {
	var out []Gap
	for _, g := range r.Gaps {
		if g.Subject != nil && *g.Subject == subject {
			out = append(out, g)
		}
	}
	return out
}
