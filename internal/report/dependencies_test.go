package report

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
)

// addDeps appends a non-required deps task with its dependency record; edit
// may alter the record after it was made valid for the task.
func addDeps(t *testing.T, f *fixture, key, status, state string, reason *string, edit func(*depdoc.Record), role string) {
	t.Helper()
	cacheKey := "go/mod"
	rec := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: f.in.Snapshot.ManifestDigest, RunID: f.runID, AttemptID: f.attempt,
		TaskKey: key, Module: depdoc.Module{ID: "go:.", Ecosystem: "go", Path: "."}, Manager: depdoc.Manager{Name: "go"}, Pinned: true,
		State: state, Reason: reason, TaskStatus: status, Counts: depdoc.Counts{Prepared: 3},
		Cache: depdoc.Cache{Kind: depdoc.CacheShared, Key: &cacheKey}, DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	if state != depdoc.StatePrepared {
		rec.Counts = depdoc.Counts{Prepared: 1, Failed: 2}
		rec.FailedItems = []depdoc.Failed{{Name: "example.com/a@v1.0.0", Reason: "module_not_on_proxy"}}
	}
	if edit != nil {
		edit(&rec)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	body, err := domain.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	f.in.Tasks = append(f.in.Tasks, TaskFacts{ID: id, Key: key, Status: status, Reason: reason, ExecutionOrdinal: 1, Stage: string(domain.StageEnvironment)})
	a := ArtifactRef{ID: domain.NewID(), RunID: f.runID, AttemptID: f.attempt, Role: role, MediaType: depdoc.MediaType, SHA256: domain.Digest(body), SizeBytes: int64(len(body))}
	f.in.Artifacts = append(f.in.Artifacts, a)
	f.in.Bodies[a.ID] = body
	var rm analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm); err != nil {
		t.Fatal(err)
	}
	mt := analysis.ManifestTask{TaskID: id, Key: key, Status: domain.TaskStatus(status), Executions: []analysis.ManifestExecution{},
		Artifacts: []analysis.ManifestArtifact{{ArtifactID: a.ID, Role: a.Role, MediaType: a.MediaType, SHA256: a.SHA256, SizeBytes: a.SizeBytes}}, Limitations: []string{}}
	mt.Reason = reason
	rm.Tasks = append(rm.Tasks, mt)
	f.setManifest(t, rm)
}

func envCounts(t *testing.T, r Report) map[string]int {
	t.Helper()
	for _, s := range r.Overview.Sections {
		if s.Key == SectionEnvironment {
			return s.Counts
		}
	}
	t.Fatal("no environment overview")
	return nil
}

func gapsOf(r Report, code string) []Gap {
	var out []Gap
	for _, g := range r.Gaps {
		if g.Code == code {
			out = append(out, g)
		}
	}
	return out
}

// An accepted record is one environment row and changes nothing else: the
// deps task is not required, so even its failure leaves status and verdict.
func TestDependencyRecordsInTheEnvironmentSection(t *testing.T) {
	baseline := build(t, newFixture(t, goVet(t)).in, DefaultRegistry())
	if baseline.Environment == nil || baseline.Environment.Dependencies != nil {
		t.Fatalf("a run without deps tasks lists dependencies: %+v", baseline.Environment)
	}
	if _, ok := envCounts(t, baseline)["dependencies"]; ok {
		t.Fatal("dependency counts without deps tasks")
	}
	unreachable, nothing := "registry_unreachable", "no_dependencies"
	f := newFixture(t, goVet(t))
	addDeps(t, f, "deps.go", "succeeded", depdoc.StatePrepared, nil, nil, "other")
	addDeps(t, f, "deps.go.m01", "failed", depdoc.StatePartial, &unreachable, func(r *depdoc.Record) {
		r.Module = depdoc.Module{ID: "go:svc", Ecosystem: "go", Path: "svc"}
	}, "other")
	addDeps(t, f, "deps.go.m02", "skipped_policy", depdoc.StateSkipped, &nothing, func(r *depdoc.Record) {
		r.Module = depdoc.Module{ID: "go:tool", Ecosystem: "go", Path: "tool"}
		r.Counts, r.Cache, r.FailedItems = depdoc.Counts{}, depdoc.Cache{Kind: depdoc.CacheNone}, []depdoc.Failed{}
	}, "other")
	r := build(t, f.in, DefaultRegistry())
	if r.Status != baseline.Status || r.QualityVerdict != baseline.QualityVerdict || !reflect.DeepEqual(r.Summary, baseline.Summary) || !reflect.DeepEqual(r.Limitations, baseline.Limitations) {
		t.Fatalf("deps tasks changed the report: %s/%s %v (baseline %s/%s %v)", r.Status, r.QualityVerdict, r.Limitations, baseline.Status, baseline.QualityVerdict, baseline.Limitations)
	}
	deps := r.Environment.Dependencies
	if len(deps) != 3 || deps[0].TaskKey != "deps.go" || deps[1].TaskKey != "deps.go.m01" {
		t.Fatalf("rows %+v", deps)
	}
	for _, d := range deps {
		if d.Result != DiscoveryAccepted || d.State == nil || d.Module == nil {
			t.Fatalf("row not accepted: %+v", d)
		}
	}
	if *deps[1].State != depdoc.StatePartial || *deps[1].Prepared != 1 || *deps[1].Failed != 2 || len(deps[1].FailedItems) != 1 {
		t.Fatalf("partial row %+v", deps[1])
	}
	if c := envCounts(t, r); c["dependencies"] != 3 || c["dependencies_prepared"] != 1 {
		t.Fatalf("overview counts %v", c)
	}
	// Only the task that did not prepare is a gap; nothing to fetch is not.
	g := gapsOf(r, GapDependenciesNotPrepared)
	if len(g) != 1 || *g[0].Subject != "deps.go.m01" || *g[0].Detail != unreachable || g[0].Section != SectionEnvironment {
		t.Fatalf("gaps %+v", r.Gaps)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatalf("report does not validate: %v", err)
	}
	md := string(RenderMarkdown(r, strings.Repeat("0", 64), Korean))
	for _, want := range []string{"deps.go.m01", "go:svc", "registry·proxy", "### 의존성 받기"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown misses %q", want)
		}
	}
}

func TestDependencyRecordRejected(t *testing.T) {
	cases := map[string]struct {
		edit func(*depdoc.Record)
		role string
	}{
		"other attempt":  {func(r *depdoc.Record) { r.AttemptID = domain.NewID() }, "other"},
		"other snapshot": {func(r *depdoc.Record) { r.Snapshot = domain.Digest([]byte("other")) }, "other"},
		"status differs": {func(r *depdoc.Record) {
			r.TaskStatus, r.State, r.Reason = "failed", depdoc.StateFailed, strPtr("timeout")
		}, "other"},
		"wrong role":      {nil, "observations"},
		"lockfile absent": {func(r *depdoc.Record) { r.Lockfile = &depdoc.Lockfile{Path: "go.sum", SHA256: strings.Repeat("b", 64)} }, "other"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, goVet(t))
			addDeps(t, f, "deps.go", "succeeded", depdoc.StatePrepared, nil, c.edit, c.role)
			r := build(t, f.in, DefaultRegistry())
			if !has(r.Limitations, LimArtifactRejected+":deps.go") {
				t.Fatalf("not rejected: %v", r.Limitations)
			}
			if d := r.Environment.Dependencies; len(d) != 1 || d[0].Result != DiscoveryRejected || d[0].State != nil {
				t.Fatalf("rows %+v", d)
			}
		})
	}
}

// An analyzer that could not resolve dependencies in an ecosystem no deps
// task covered is the "turn fetching on" gap; a deps task of the ecosystem
// (whatever it ended with) replaces it with its own row.
func TestDependenciesNotFetchedGap(t *testing.T) {
	reason := "dependency_unavailable"
	goEco := "go"
	r := &Report{Environment: &EnvironmentSection{}, StaticAnalysis: &StaticAnalysisSection{Languages: []StaticLanguage{
		{Ecosystem: &goEco, Tools: []StaticTool{{TaskKey: "go-vet", TaskReason: &reason}, {TaskKey: "staticcheck", TaskReason: &reason}}}}}}
	collect := func(keys []string) []Gap {
		var out []Gap
		sectionContext{r: r, keys: keys}.dependencyGaps(func(section, code string, subject, detail *string) {
			out = append(out, Gap{Section: section, Code: code, Subject: subject, Detail: detail})
		})
		return out
	}
	g := collect([]string{"go-vet", "staticcheck"})
	if len(g) != 2 || g[0].Code != GapDependenciesNotFetched || *g[0].Subject != "go-vet" || *g[1].Subject != "staticcheck" {
		t.Fatalf("gaps %+v", g)
	}
	if g := collect([]string{"deps.go.m01", "go-vet", "staticcheck"}); len(g) != 0 {
		t.Fatalf("gap although a deps task ran: %+v", g)
	}
	pinned := false
	prepared := "prepared"
	r.Environment.Dependencies = []EnvDependency{{TaskKey: "deps.go", TaskStatus: "succeeded", Ecosystem: &goEco, Pinned: &pinned, State: &prepared}}
	if g := collect([]string{"deps.go", "go-vet"}); len(g) != 1 || g[0].Code != GapVersionsNotPinned {
		t.Fatalf("gaps %+v", g)
	}
	// A task skipped before it read the lockfile fetched nothing: no
	// versions were picked.
	skipped, notAllowed, rustEco := "skipped", "dependency_consumers_not_allowed", "rust"
	r.Environment.Dependencies = []EnvDependency{{TaskKey: "deps.rust", TaskStatus: "skipped_policy", TaskReason: &notAllowed, Ecosystem: &rustEco, Pinned: &pinned, State: &skipped}}
	for _, g := range collect([]string{"deps.rust"}) {
		if g.Code == GapVersionsNotPinned || g.Code == GapDependenciesNotPrepared {
			t.Fatalf("skipped task gap %s", g.Code)
		}
	}
}

// A Python type checker that left imports unresolved because no package was
// installed is the same gap; one that read prepared packages, or that had
// nothing unresolved, is not.
func TestDependenciesNotFetchedGapPython(t *testing.T) {
	py := "python"
	r := &Report{Environment: &EnvironmentSection{}, StaticAnalysis: &StaticAnalysisSection{Languages: []StaticLanguage{
		{Ecosystem: &py, Tools: []StaticTool{{TaskKey: "mypy"}, {TaskKey: "pyright"}, {TaskKey: "ruff"}}}}},
		Coverage: Coverage{Tools: []ToolCoverage{
			{TaskKey: "mypy", Packages: map[string]int{"import_unresolved": 3}, Limitations: []string{"imports_unresolved", "third_party_packages_not_installed"}},
			{TaskKey: "pyright", Packages: map[string]int{"import_unresolved": 2}, Limitations: []string{"imports_unresolved", "third_party_packages_prepared"}},
			{TaskKey: "ruff", Packages: map[string]int{}, Limitations: []string{}},
		}}}
	collect := func(keys []string) []Gap {
		var out []Gap
		sectionContext{r: r, keys: keys}.dependencyGaps(func(section, code string, subject, detail *string) {
			out = append(out, Gap{Section: section, Code: code, Subject: subject, Detail: detail})
		})
		return out
	}
	g := collect([]string{"mypy", "pyright", "ruff"})
	if len(g) != 1 || g[0].Code != GapDependenciesNotFetched || *g[0].Subject != "mypy" || *g[0].Detail != "imports_unresolved" {
		t.Fatalf("gaps %+v", g)
	}
	if g := collect([]string{"deps.python", "mypy", "pyright"}); len(g) != 0 {
		t.Fatalf("gap although a deps task ran: %+v", g)
	}
}
