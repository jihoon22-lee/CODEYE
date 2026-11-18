package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

type recordingHandler struct {
	noopHandler
	ran *[]string
}

func (h recordingHandler) Run(_ context.Context, _ analysis.Job, t analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	*h.ran = append(*h.ran, t.Key)
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

// The review handlers import this package (review-r1 reads its artifacts),
// so the composition with the real review plans is tested in package
// review (TestDiscoveryCompositionPlans); the inner plan here stands for it.
func TestPlanPrependsRequiredDiscoveryTasks(t *testing.T) {
	inner := []analysis.TaskSpec{{Key: "go-vet", Required: true, Stage: domain.StageStaticAnalysis},
		{Key: "review.context", Required: true, Stage: domain.StageDocuments}, {Key: "review.investigate", Required: true, Stage: domain.StageCodeReview},
		{Key: "review.verify", Required: true, Stage: domain.StageCodeReview}}
	var ran []string
	h, err := Wrap(recordingHandler{noopHandler{planned: inner}, &ran}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	j := jobOf(t, map[string]string{"go.mod": "module x\n\ngo 1.22\n"}, domain.Scope{Kind: "full"})
	plan, err := h.Plan(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		key      string
		required bool
		stage    domain.Stage
	}
	got := []row{}
	for _, p := range plan {
		got = append(got, row{p.Key, p.Required, p.Stage})
	}
	want := []row{
		{"env.discover", true, domain.StageEnvironment}, {"docs.discover", true, domain.StageDocuments},
		{"go-vet", true, domain.StageStaticAnalysis},
		{"review.context", true, domain.StageDocuments}, {"review.investigate", true, domain.StageCodeReview}, {"review.verify", true, domain.StageCodeReview},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan %v\nwant %v", got, want)
	}
	if h.Info() != (noopHandler{}).Info() {
		t.Fatalf("identity changed: %+v", h.Info())
	}
	if res := h.Run(context.Background(), j, plan[2], nil); res.Status != domain.TaskSucceeded || !reflect.DeepEqual(ran, []string{"go-vet"}) {
		t.Fatalf("inner task not delegated: %+v %v", res, ran)
	}
	if _, err := Wrap(nil, Config{}); err == nil {
		t.Fatal("nil inner accepted")
	}
	clash, _ := Wrap(noopHandler{planned: []analysis.TaskSpec{{Key: "env.discover", Required: true}}}, Config{})
	if _, err := clash.Plan(context.Background(), j); err == nil {
		t.Fatal("duplicate discovery key planned")
	}
}

// A changed snapshot byte fails the discovery task with the integrity reason
// (other tasks are unaffected: the worker runs every planned task).
func TestDiscoveryRejectsChangedSnapshot(t *testing.T) {
	for _, target := range []string{"go.mod", "README.md"} {
		j := jobOf(t, map[string]string{"go.mod": "module x\n\ngo 1.22\n", "README.md": "readme\n"}, domain.Scope{Kind: "full"})
		p := filepath.Join(j.Snapshot.SnapshotDir, target)
		if err := os.Chmod(p, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("changed!\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		h, _ := Wrap(noopHandler{}, Config{})
		task := Tasks()[0]
		if target == "README.md" {
			task = Tasks()[1]
		}
		res := h.Run(context.Background(), j, task, nil)
		if res.Status != domain.TaskFailed || res.Reason != ReasonIntegrity || len(res.Artifacts) != 0 {
			t.Fatalf("%s: %+v", target, res)
		}
	}
}

func TestDiscoveryUnsupportedScopeAndBadJob(t *testing.T) {
	j := jobOf(t, map[string]string{"go.mod": "module x\n\ngo 1.22\n"}, domain.Scope{Kind: "modules"})
	h, _ := Wrap(noopHandler{}, Config{})
	for _, task := range Tasks() {
		if res := h.Run(context.Background(), j, task, nil); res.Status != domain.TaskFailed || res.Reason != ReasonScopeUnsupported {
			t.Fatalf("%s: %+v", task.Key, res)
		}
	}
	j.Scope = domain.Scope{Kind: "full"}
	j.Scratch = "relative"
	if res := h.Run(context.Background(), j, Tasks()[0], nil); res.Status != domain.TaskFailed || res.Reason != ReasonSnapshotUnavailable {
		t.Fatalf("relative scratch: %+v", res)
	}
}

// The environment artifact of an unconfigured node still records every
// declaration and leaves all comparisons unknown.
func TestEnvironmentWithoutObservations(t *testing.T) {
	j := jobOf(t, map[string]string{
		"go.mod": "module x\n\ngo 1.22\ntoolchain go1.31.0\n", "web/package.json": `{"engines":{"node":"^18"}}`,
		"py/pyproject.toml": "[project]\nrequires-python = \">=3.10\"\n", "native/Makefile": "all:\n",
		"bad/go.mod": "module b\ngo 1.21\ngo 1.22\n",
	}, domain.Scope{Kind: "full"})
	res, d := runEnv(t, j, nil, nil)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("%+v", res)
	}
	got := map[string]string{}
	for _, v := range d.Environment.Verdicts {
		key := v.Module + "/" + v.Tool
		if v.Requirement != nil {
			key += "/" + *v.Requirement
		}
		got[key] = v.Status + "/" + *v.Reason
	}
	want := map[string]string{
		"go:./go/go_directive@go.mod":                        "unknown/not_observed",
		"go:./go/go_toolchain@go.mod":                        "unknown/not_observed",
		"javascript:web/node/engines_node@web/package.json":  "unknown/unsupported_constraint",
		"python:py/python/requires_python@py/pyproject.toml": "unknown/not_observed",
		"cpp:native/c_cpp_compiler":                          "unknown/not_declared",
		"go:bad/go":                                          "unknown/declaration_unreadable",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("verdicts %v", got)
	}
	if !reflect.DeepEqual(d.Limitations, []string{LimConstraintNotCompared, LimManifestMalformed, LimToolNotProbed}) {
		t.Fatalf("limitations %v", d.Limitations)
	}
	for _, r := range d.Environment.Requirements {
		if r.ID == "go_toolchain@go.mod" && (r.Comparison != envdoc.ComparePreferredMinimum || r.Source.StartLine != 4) {
			t.Fatalf("toolchain requirement %+v", r)
		}
	}
	for _, m := range d.Environment.Manifests {
		if m.Path == "native/Makefile" && m.Status != envdoc.ManifestNoParser {
			t.Fatalf("Makefile %+v", m)
		}
	}
}

// A preferred toolchain newer than the node is a mismatch of the
// preference only: under GOTOOLCHAIN=local go vet still runs when the go
// directive is satisfied (observed on go1.27.4 with go 1.22 + toolchain
// go1.31.0).
func TestToolchainPreferenceVerdict(t *testing.T) {
	j := jobOf(t, map[string]string{"go.mod": "module x\n\ngo 1.22\n\ntoolchain go1.31.0\n"}, domain.Scope{Kind: "full"})
	_, d := runEnv(t, j, StaticObserver{Versions: map[string]string{"go": "1.27.4"}}, nil)
	got := map[string]string{}
	for _, v := range d.Environment.Verdicts {
		got[*v.Requirement] = v.Status
	}
	if !reflect.DeepEqual(got, map[string]string{"go_directive@go.mod": envdoc.VerdictSatisfied, "go_toolchain@go.mod": envdoc.VerdictMismatch}) {
		t.Fatalf("verdicts %v", got)
	}
}
