package review

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func interpretOf(t *testing.T, files ...string) roleStage {
	t.Helper()
	return roleStage{ctx: t.Context(), j: treeJob(t, pathTree(files...)), task: "review.interpret", role: record.StageInterpret}
}

func observationIDs(obs []obsInput) []string {
	var out []string
	for _, o := range obs {
		out = append(out, o.ID)
	}
	return out
}

// TC-315: the interpretation request carries the observations the triage plan
// would classify first. Observations on test paths sort first by path, so
// the earlier path order filled the request with them while the diagnostics
// of shipped code waited for the triage; the request now takes shipped code
// first, then the more severe finding, then path, line and ID.
func TestInterpretTakesShippedObservationsBeforeOtherPaths(t *testing.T) {
	tests := []string{"a/a_test.go", "a/b_test.go", "a/c_test.go", "a/d_test.go", "a/e_test.go", "a/f_test.go"}
	shipped := []string{"svc/handler.go", "svc/store.go", "svc/queue.go", "svc/cache.go"}
	s := interpretOf(t, append(slices.Clone(tests), shipped...)...)
	var all []toolObservation
	for _, p := range tests {
		all = append(all, triagePathDiags("SA5011", 1, p)...)
	}
	for _, p := range shipped {
		all = append(all, triagePathDiags("SA5011", 1, p)...)
	}
	_, obs, refs, windows, lims, err := s.observationWindows([]toolDoc{staticcheckDoc(all...)})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != maxObservations || len(refs) != maxObservations || !slices.Contains(lims, "observations_outside_interpret_input") {
		t.Fatalf("%d observations, limitations %v", len(obs), lims)
	}
	var paths []string
	for _, o := range obs {
		paths = append(paths, o.Path)
	}
	// Shipped paths first, each group in path order, then the first test
	// paths in path order.
	want := []string{"svc/cache.go", "svc/handler.go", "svc/queue.go", "svc/store.go", "a/a_test.go", "a/b_test.go", "a/c_test.go", "a/d_test.go"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths %v, want %v", paths, want)
	}
	// The code windows go to the first observations, so to shipped code.
	if len(windows) != roleMaxWindows {
		t.Fatalf("%d windows", len(windows))
	}
	for i, w := range windows {
		if w.Path != want[i] {
			t.Fatalf("window %d is %s, want %s", i, w.Path, want[i])
		}
	}
	for _, o := range obs[roleMaxWindows:] {
		if len(o.EvidenceIDs) != 0 {
			t.Fatalf("observation %s got a window: %v", o.ID, o.EvidenceIDs)
		}
	}
}

// The severity of the finding, after the path rule, orders the observations
// of one class: a rule that is high on shipped code goes before a low one,
// and among equal ones path, line and ID decide as before.
func TestInterpretOrdersBySeverityWithinAClass(t *testing.T) {
	files := []string{"svc/a.go", "svc/b.go", "svc/c.go", "x/a_test.go", "x/b_test.go"}
	s := interpretOf(t, files...)
	var all []toolObservation
	all = append(all, triagePathDiags("SA1019", 1, "svc/a.go", "svc/b.go")...) // low
	all = append(all, triagePathDiags("SA5011", 1, "svc/c.go")...)             // high on shipped code
	all = append(all, triagePathDiags("SA5011", 1, "x/a_test.go")...)          // lowered
	all = append(all, triagePathDiags("SA1019", 1, "x/b_test.go")...)
	_, obs, _, _, lims, err := s.observationWindows([]toolDoc{staticcheckDoc(all...)})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, o := range obs {
		paths = append(paths, o.Path)
	}
	if want := []string{"svc/c.go", "svc/a.go", "svc/b.go", "x/a_test.go", "x/b_test.go"}; !slices.Equal(paths, want) {
		t.Fatalf("paths %v, want %v", paths, want)
	}
	if slices.Contains(lims, "observations_outside_interpret_input") {
		t.Fatalf("limitations %v", lims)
	}
}

// An observation without a location is not on shipped code: it sorts after
// the located shipped ones and, among equals, last.
func TestInterpretPutsObservationsWithoutLocationAfterShippedCode(t *testing.T) {
	s := interpretOf(t, "svc/a.go", "svc/z.go")
	unlocated := toolObservation{ID: "no-location", Tool: staticcheck.AnalyzerID, RuleID: "SA1019", RawMessage: "synthetic message", LocationState: obsdoc.LocationNoPosition}
	all := append([]toolObservation{unlocated}, triagePathDiags("SA1019", 1, "svc/z.go", "svc/a.go")...)
	_, obs, _, _, _, err := s.observationWindows([]toolDoc{staticcheckDoc(all...)})
	if err != nil {
		t.Fatal(err)
	}
	if got := observationIDs(obs); len(got) != 3 || got[2] != "no-location" {
		t.Fatalf("order %v", got)
	}
}

// The interpretation and the triage choose by one comparison: the same
// diagnostics, ranked by the triage plan as groups of one file each, come in
// the order the interpretation request lists them. The diagnostics the
// accepted hypotheses carry are the only ones the triage leaves out, so every
// diagnostic is read by one role at most and none is dropped.
func TestInterpretAndTriageShareOneOrderAndSplit(t *testing.T) {
	files := []string{"svc/a.go", "svc/b.go", "svc/c.go", "svc/d.go", "x/a_test.go", "x/b_test.go", "examples/e/main.go"}
	j := treeJob(t, pathTree(files...))
	var all []toolObservation
	all = append(all, triagePathDiags("SA1019", 1, "x/a_test.go", "examples/e/main.go", "svc/a.go", "svc/b.go")...)
	all = append(all, triagePathDiags("SA5011", 1, "svc/c.go", "svc/d.go", "x/b_test.go")...)
	docs := []toolDoc{staticcheckDoc(all...)}

	interp := roleStage{ctx: t.Context(), j: j, task: "review.interpret", role: record.StageInterpret}
	_, obs, _, _, _, err := interp.observationWindows(docs)
	if err != nil {
		t.Fatal(err)
	}
	tri := roleStage{ctx: t.Context(), j: j, task: "review.triage.t001", role: record.StageTriage, triage: "t001"}
	// Each triage group holds one diagnostic here, so the plan's order of
	// groups is the order of single diagnostics.
	plan := tri.planTriage(docs, nil, 24)
	var triaged []string
	for _, bin := range plan.bins {
		for _, g := range bin {
			for _, d := range g.diags {
				triaged = append(triaged, d.obs.ID)
			}
		}
	}
	// Only the priority is shared, not the packing: compare the leading
	// observations of both lists up to the first path-order tiebreak (the
	// triage breaks ties by path, line, tool and rule as well).
	if len(triaged) != len(all) || len(obs) != len(all) {
		t.Fatalf("triaged %d, interpreted %d of %d", len(triaged), len(obs), len(all))
	}
	if got := observationIDs(obs); !slices.Equal(got, triaged) {
		t.Fatalf("interpretation order %v, triage order %v", got, triaged)
	}

	// The hypotheses carry some of the observations; the triage leaves out
	// exactly those and classifies every other diagnostic once.
	interpreted := map[string]bool{obs[0].ID: true, obs[len(obs)-1].ID: true}
	plan = tri.planTriage(docs, interpreted, 24)
	if plan.eligible != len(all) || !slices.Contains(plan.lims, limDiagnosticsInterpreted) {
		t.Fatalf("eligible %d, limitations %v", plan.eligible, plan.lims)
	}
	seen := map[string]int{}
	for _, bin := range plan.bins {
		for _, g := range bin {
			for _, d := range g.diags {
				seen[d.obs.ID]++
			}
		}
	}
	for _, o := range all {
		want := 1
		if interpreted[o.ID] {
			want = 0
		}
		if seen[o.ID] != want {
			t.Fatalf("diagnostic %s triaged %d times, want %d", o.ID, seen[o.ID], want)
		}
	}
	if len(seen) != len(all)-len(interpreted) {
		t.Fatalf("triaged %d diagnostics, want %d", len(seen), len(all)-len(interpreted))
	}
}
