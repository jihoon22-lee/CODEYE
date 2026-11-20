package review

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type stubStatic struct{}

func (stubStatic) Info() analysis.HandlerInfo { return analysis.HandlerInfo{Name: "stub"} }
func (stubStatic) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "static", Required: true}}, nil
}
func (stubStatic) Run(context.Context, analysis.Job, analysis.TaskSpec, executor.Runner) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

func verdict(id, v string, mutate func(map[string]any)) map[string]any {
	m := map[string]any{"candidate_id": id, "verdict": v, "category": "resource_lifecycle", "severity": "medium",
		"severity_reason": "파일 descriptor 수명 문제다.", "impact": "장시간 실행 시 descriptor가 고갈될 수 있다.",
		"valid_conditions": []string{}, "invalid_conditions": []string{}, "supporting_evidence_ids": []string{}, "counterevidence_ids": []string{},
		"unresolved_assumptions": []string{}, "next_verification": []string{"다른 호출자의 정리 경로를 읽는다."}}
	if mutate != nil {
		mutate(m)
	}
	return m
}

// A missing credential is "not configured" (unsupported), a rejected key a
// failure; calls through another OpenCode release are marked untested.
func TestModelOutcomeMapping(t *testing.T) {
	for reason, want := range map[string]domain.TaskStatus{
		opencode.ReasonCredentialUnavailable: domain.TaskUnsupported, "model_not_configured": domain.TaskUnsupported,
		opencode.ReasonAuthFailed: domain.TaskFailed, opencode.ReasonModelNotFound: domain.TaskFailed, "model_timeout": domain.TaskTimedOut,
	} {
		if got := failure(opencode.Reply{Reason: reason}); got.Status != want || got.Reason != reason {
			t.Errorf("%s: %+v", reason, got)
		}
	}
	if got := noteVersion([]string{"a"}, opencode.Reply{Version: "1.20.0"}); len(got) != 2 || got[1] != opencode.LimVersionUntested {
		t.Fatal(got)
	}
	for _, v := range []string{opencode.Version, ""} {
		if got := noteVersion(nil, opencode.Reply{Version: v}); len(got) != 0 {
			t.Fatal(v, got)
		}
	}
}

// wholeExcerpts are the named Go files of a snapshot as whole-file
// excerpts with their text, the catalog a verification expands from.
func wholeExcerpts(t *testing.T, j analysis.Job, names ...string) []Excerpt {
	t.Helper()
	var out []Excerpt
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Kind != "file" || !slices.Contains(names, e.Path) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(j.Snapshot.SnapshotDir, filepath.FromSlash(e.Path)))
		if err != nil {
			t.Fatal(err)
		}
		n := strings.Count(strings.TrimSuffix(string(b), "\n"), "\n") + 1
		kind := "code"
		if strings.HasSuffix(e.Path, "_test.go") {
			kind = "test"
		}
		out = append(out, Excerpt{ID: record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, n), Path: e.Path, SHA256: e.SHA256, Start: 1, End: n, Kind: kind, Origin: "context", Text: string(b)})
	}
	return out
}

func TestExpansionScopeAndBounds(t *testing.T) {
	j := verifyJob(t, false)
	catalog := wholeExcerpts(t, j, "store.go")
	store := catalog[0]
	cands := []CandidateText{{ID: "candidate_1", Evidence: []string{store.ID}}}
	extra, lims, err := expand(t.Context(), j.Snapshot, j.Scope, catalog, cands)
	if err != nil || len(extra) != 2 || slices.Contains(lims, limNoCallers) {
		t.Fatal(extra, lims, err)
	}
	for _, e := range extra {
		if e.ID != record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, e.Start, e.End) || e.End-e.Start+1 > maxWindowLines || len(e.Text) > maxWindowBytes {
			t.Fatalf("%+v", e)
		}
	}
	// A scope limited to store.go never reads callers elsewhere.
	extra, lims, err = expand(t.Context(), j.Snapshot, domain.Scope{Kind: "paths", Paths: []string{"store.go"}}, catalog, cands)
	if err != nil || len(extra) != 0 || !slices.Contains(lims, limNoCallers) {
		t.Fatal(extra, lims, err)
	}
	// No declared function in cited code: nothing to expand.
	extra, lims, _ = expand(t.Context(), j.Snapshot, j.Scope, catalog, []CandidateText{{ID: "candidate_1", Evidence: []string{"ev_unknown"}}})
	if len(extra) != 0 || !slices.Contains(lims, limNoSymbol) {
		t.Fatal(extra, lims)
	}
}
