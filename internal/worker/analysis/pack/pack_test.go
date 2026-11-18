package pack

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func job(t *testing.T) analysis.Job {
	t.Helper()
	src, data := t.TempDir(), t.TempDir()
	for rel, body := range map[string]string{"go.mod": "module x\n\ngo 1.22\n", "a.go": "package x\n", "py/m.py": "print(1)\n"} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := source.AcquireLocal(context.Background(), src, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := source.Load(context.Background(), snap.Root, snap.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: "r", AttemptID: "a", Epoch: 1, Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, Snapshot: loaded, Scratch: t.TempDir()}
}

func version(b []byte) (string, bool) { return strings.TrimSpace(string(b)), len(b) > 0 }

func desc(id, name string, required bool) Descriptor {
	return Descriptor{ID: id, Name: name, Ecosystem: "go", Required: required, Tool: name, VersionArgs: []string{"--version"}, ParseVersion: version}
}

// fake plans the given keys and records which keys it ran.
type fake struct {
	d     Descriptor
	keys  []string
	ran   []string
	stage domain.Stage
	req   *bool
}

func (f *fake) Descriptor() Descriptor { return f.d }

func (f *fake) Plan(ctx context.Context, job analysis.Job) ([]Task, error) {
	var out []Task
	for _, k := range f.keys {
		req := f.d.Required
		if f.req != nil {
			req = *f.req
		}
		stage := domain.StageStaticAnalysis
		if f.stage != "" {
			stage = f.stage
		}
		out = append(out, Task{Spec: analysis.TaskSpec{Key: k, Required: req, Title: k, Stage: stage}, Module: ".", Patterns: []string{"./..."}})
	}
	return out, nil
}

func (f *fake) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	f.ran = append(f.ran, task.Key)
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

// Plans are concatenated in member order; Run goes to the owner only.
func TestCompositePlanAndDispatch(t *testing.T) {
	vet := &fake{d: desc("go.vet", "go-vet", true), keys: []string{"go-vet", "go-vet.m01"}}
	sc := &fake{d: desc("go.staticcheck", "staticcheck", false), keys: []string{"staticcheck", "staticcheck.m01"}}
	c, err := NewComposite(vet, sc)
	if err != nil {
		t.Fatal(err)
	}
	j := job(t)
	plan, err := c.Plan(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range plan {
		keys = append(keys, p.Key)
	}
	if !slices.Equal(keys, []string{"go-vet", "go-vet.m01", "staticcheck", "staticcheck.m01"}) || !plan[0].Required || plan[2].Required {
		t.Fatalf("plan %+v", plan)
	}
	for _, k := range keys {
		c.Run(context.Background(), j, analysis.TaskSpec{Key: k}, nil)
	}
	if !slices.Equal(vet.ran, []string{"go-vet", "go-vet.m01"}) || !slices.Equal(sc.ran, []string{"staticcheck", "staticcheck.m01"}) {
		t.Fatalf("dispatch vet=%v sc=%v", vet.ran, sc.ran)
	}
	if r := c.Run(context.Background(), j, analysis.TaskSpec{Key: "go-vetx"}, nil); r.Status != domain.TaskFailed || !slices.Contains(r.Limitations, ReasonUnknownTask) {
		t.Fatalf("bare prefix dispatched: %+v", r)
	}
	if info := c.Info(); info.Name != CompositeName || info.Capability != Capability {
		t.Fatalf("info %+v", info)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Plan(ctx, j); err == nil {
		t.Fatal("cancelled plan")
	}
}

func TestCompositeRejectsAmbiguousMembers(t *testing.T) {
	bad := map[string][]Analyzer{
		"empty":            nil,
		"duplicate id":     {&fake{d: desc("go.vet", "go-vet", true)}, &fake{d: desc("go.vet", "vet2", false)}},
		"nested names":     {&fake{d: desc("go.vet", "go", true)}, &fake{d: desc("go.vet2", "go.vet", false)}},
		"same name":        {&fake{d: desc("go.vet", "go-vet", true)}, &fake{d: desc("go.vet2", "go-vet", false)}},
		"review name":      {&fake{d: desc("go.review", "review", true)}},
		"discovery name":   {&fake{d: desc("go.env", "env", true)}},
		"invalid id":       {&fake{d: desc("govet", "go-vet", true)}},
		"invalid tool":     {&fake{d: Descriptor{ID: "go.vet", Name: "go-vet", Ecosystem: "go", Tool: "Go Vet", ParseVersion: version}}},
		"no version parse": {&fake{d: Descriptor{ID: "go.vet", Name: "go-vet", Ecosystem: "go", Tool: "go"}}},
		"bad ecosystem":    {&fake{d: Descriptor{ID: "go.vet", Name: "go-vet", Ecosystem: "golang", Tool: "go", ParseVersion: version}}},
	}
	for name, members := range bad {
		if _, err := NewComposite(members...); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	j := job(t)
	notRequired := false
	planBad := map[string]*fake{
		"foreign key":   {d: desc("go.vet", "go-vet", true), keys: []string{"staticcheck"}},
		"bare prefix":   {d: desc("go.vet", "go-vet", true), keys: []string{"go-vetx"}},
		"required flag": {d: desc("go.vet", "go-vet", true), keys: []string{"go-vet"}, req: &notRequired},
		"stage":         {d: desc("go.vet", "go-vet", true), keys: []string{"go-vet"}, stage: domain.StageCodeReview},
		"duplicate key": {d: desc("go.vet", "go-vet", true), keys: []string{"go-vet", "go-vet"}},
		"invalid key":   {d: desc("go.vet", "go-vet", true), keys: []string{"go-vet.M01"}},
	}
	for name, f := range planBad {
		c, err := NewComposite(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Plan(context.Background(), j); err == nil {
			t.Fatalf("%s planned", name)
		}
	}
}

// An unconfigured analyzer plans exactly like the real one and finishes
// unsupported with a valid not-run envelope: nothing processed (known zero),
// no observation, no execution.
func TestNotConfiguredIsVisible(t *testing.T) {
	d := desc("go.staticcheck", "staticcheck", false)
	real := &fake{d: d, keys: []string{"staticcheck"}}
	if _, err := NotConfigured(d, real.Plan, "missing"); err == nil {
		t.Fatal("unknown reason accepted")
	}
	if _, err := NotConfigured(d, nil, ReasonToolNotConfigured); err == nil {
		t.Fatal("nil planner accepted")
	}
	for _, reason := range []string{ReasonToolNotConfigured, ReasonToolUnavailable, ReasonToolchainNotConfigured} {
		stub, err := NotConfigured(d, real.Plan, reason)
		if err != nil {
			t.Fatal(err)
		}
		j := job(t)
		c, err := NewComposite(&fake{d: desc("go.vet", "go-vet", true), keys: []string{"go-vet"}}, stub)
		if err != nil {
			t.Fatal(err)
		}
		plan, _ := c.Plan(context.Background(), j)
		if len(plan) != 2 || plan[1].Key != "staticcheck" || plan[1].Required {
			t.Fatalf("plan %+v", plan)
		}
		res := c.Run(context.Background(), j, plan[1], nil)
		if res.Status != domain.TaskUnsupported || res.Reason != reason || len(res.Executions) != 0 || len(res.Artifacts) != 1 {
			t.Fatalf("result %+v", res)
		}
		if res.Artifacts[0].Path != obsdoc.ArtifactPath(j.Scratch, "staticcheck") || res.Artifacts[0].MediaType != analysis.MediaObservations || res.Artifacts[0].Role != analysis.RoleObservations {
			t.Fatalf("artifact %+v", res.Artifacts[0])
		}
		b, err := os.ReadFile(res.Artifacts[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := obsdoc.Decode(b, Expect(j, "staticcheck"))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Outcome.ExitClass != obsdoc.ExitNotRun || doc.Coverage.ProcessedPaths == nil || len(doc.Coverage.ProcessedPaths) != 0 || doc.Analyzer.Required || doc.Tool.Version != nil ||
			doc.Adapter != NotRunAdapter || !slices.Equal(doc.Limitations, []string{reason}) {
			t.Fatalf("envelope %+v", doc)
		}
		if r := stub.Run(context.Background(), j, analysis.TaskSpec{Key: "staticcheck.m09"}, nil); r.Status != domain.TaskFailed {
			t.Fatalf("unplanned key %+v", r)
		}
	}
}

func TestWriteEnvelopeIsExclusive(t *testing.T) {
	j := job(t)
	stub, _ := NotConfigured(desc("go.staticcheck", "staticcheck", false), (&fake{keys: []string{"staticcheck"}, d: desc("go.staticcheck", "staticcheck", false)}).Plan, ReasonToolNotConfigured)
	if r := stub.Run(context.Background(), j, analysis.TaskSpec{Key: "staticcheck"}, nil); r.Status != domain.TaskUnsupported {
		t.Fatal(r)
	}
	// A second write to the same task path is refused, never overwritten.
	if r := stub.Run(context.Background(), j, analysis.TaskSpec{Key: "staticcheck"}, nil); r.Status != domain.TaskFailed || !slices.Contains(r.Limitations, "observations_write_failed") {
		t.Fatalf("overwrite %+v", r)
	}
	if _, err := WriteEnvelope(analysis.Job{Scratch: "relative"}, obsdoc.Document{}); err == nil {
		t.Fatal("relative scratch accepted")
	}
}
