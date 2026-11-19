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
	return jobFiles(t, map[string]string{"go.mod": "module x\n\ngo 1.22\n", "a.go": "package x\n", "README.md": "x\n"})
}

func jobFiles(t *testing.T, files map[string]string) analysis.Job {
	t.Helper()
	src, data := t.TempDir(), t.TempDir()
	for rel, body := range files {
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

// fake plans the given keys and records which keys it ran. Keys in code
// are declared to run repository code; Run starts one process through the
// runner when it has one.
type fake struct {
	d     Descriptor
	keys  []string
	ran   []string
	stage domain.Stage
	req   *bool
	code  map[string]bool
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
		out = append(out, Task{Spec: analysis.TaskSpec{Key: k, Required: req, Title: k, Stage: stage}, Module: ".", Patterns: []string{"./..."}, RepositoryCode: f.code[k]})
	}
	return out, nil
}

func (f *fake) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	f.ran = append(f.ran, task.Key)
	r := analysis.TaskResult{Status: domain.TaskSucceeded}
	if runner != nil {
		r.Executions = append(r.Executions, analysis.Execution{Name: "tool.run", Result: runner.Run(ctx, executor.Spec{Executable: "/bin/tool"})})
	}
	return r
}

// countingRunner records every process start.
type countingRunner struct{ starts int }

func (r *countingRunner) Run(context.Context, executor.Spec) executor.Result {
	r.starts++
	zero := 0
	return executor.Result{Status: executor.StatusExited, ExitCode: &zero, Cleanup: executor.CleanupConfirmed}
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

func readEnvelope(t *testing.T, j analysis.Job, res analysis.TaskResult, key string) obsdoc.Document {
	t.Helper()
	if len(res.Artifacts) != 1 {
		t.Fatalf("artifacts %+v", res.Artifacts)
	}
	b, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := obsdoc.Decode(b, Expect(j, key))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TC-103: the repository-code grant (docs/ANALYZERS.md#저장소-코드-실행-허가) is enforced once in
// Composite.Run before dispatch. A task declared to run repository code
// ends unsupported/repository_code_not_allowed without the grant: no process
// started, the analyzer never called, a not-run envelope with zero processed
// paths. One tool plans both modes; the undeclared task is unaffected. The
// declaration comes from the owner's plan, so a stand-in declares it alike,
// the policy reason wins over the tool reason, and an unplanned key fails
// closed.
func TestRepositoryCodeGrantIsEnforcedBeforeDispatch(t *testing.T) {
	d := desc("go.build", "build", true)
	newTool := func() *fake {
		return &fake{d: d, keys: []string{"build", "build.configure"}, code: map[string]bool{"build.configure": true}}
	}
	ctx := context.Background()
	for _, granted := range []bool{false, true} {
		tool := newTool()
		c, err := NewComposite(tool)
		if err != nil {
			t.Fatal(err)
		}
		j := job(t)
		j.AllowRepositoryCode = granted
		tasks, err := c.PlanTasks(ctx, j)
		if err != nil || len(tasks) != 2 || tasks[0].RepositoryCode || !tasks[1].RepositoryCode {
			t.Fatalf("plan %+v %v", tasks, err)
		}
		runner := &countingRunner{}
		safe := c.Run(ctx, j, tasks[0].Spec, runner)
		code := c.Run(ctx, j, tasks[1].Spec, runner)
		if safe.Status != domain.TaskSucceeded || slices.Contains(safe.Limitations, LimRepositoryCodeExecuted) {
			t.Fatalf("undeclared task %+v", safe)
		}
		if granted {
			if code.Status != domain.TaskSucceeded || !slices.Equal(tool.ran, []string{"build", "build.configure"}) || runner.starts != 2 ||
				!slices.Contains(code.Limitations, LimRepositoryCodeExecuted) {
				t.Fatalf("granted: %+v ran=%v starts=%d", code, tool.ran, runner.starts)
			}
			continue
		}
		if code.Status != domain.TaskUnsupported || code.Reason != ReasonRepositoryCodeNotAllowed || len(code.Executions) != 0 ||
			!slices.Equal(tool.ran, []string{"build"}) || runner.starts != 1 {
			t.Fatalf("not granted: %+v ran=%v starts=%d", code, tool.ran, runner.starts)
		}
		doc := readEnvelope(t, j, code, "build.configure")
		if doc.Outcome.ExitClass != obsdoc.ExitNotRun || doc.Outcome.TaskStatus != domain.TaskUnsupported || len(doc.Executions) != 0 ||
			doc.Coverage.ProcessedPaths == nil || len(doc.Coverage.ProcessedPaths) != 0 || !doc.Analyzer.Required ||
			!slices.Equal(doc.Limitations, []string{ReasonRepositoryCodeNotAllowed}) {
			t.Fatalf("envelope %+v", doc)
		}
	}

	// The stand-in plans the same declaration. Without the grant the policy
	// reason is the task reason and the missing tool stays a limitation;
	// with it the stand-in's own reason applies and nothing is executed.
	for _, granted := range []bool{false, true} {
		stub, err := NotConfigured(d, newTool().Plan, ReasonToolNotConfigured)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := NewComposite(stub)
		j := job(t)
		j.AllowRepositoryCode = granted
		runner := &countingRunner{}
		res := c.Run(ctx, j, analysis.TaskSpec{Key: "build.configure"}, runner)
		want, lims := ReasonRepositoryCodeNotAllowed, []string{ReasonRepositoryCodeNotAllowed, ReasonToolNotConfigured}
		if granted {
			want, lims = ReasonToolNotConfigured, []string{ReasonToolNotConfigured}
		}
		if res.Status != domain.TaskUnsupported || res.Reason != want || runner.starts != 0 || !slices.Equal(res.Limitations, lims) {
			t.Fatalf("stand-in granted=%v: %+v", granted, res)
		}
		if doc := readEnvelope(t, j, res, "build.configure"); !slices.Equal(doc.Limitations, lims) || *doc.Outcome.Reason != want {
			t.Fatalf("stand-in envelope %+v", doc)
		}
	}

	// A key the owner does not plan is never dispatched, granted or not.
	tool := newTool()
	c, _ := NewComposite(tool)
	j := job(t)
	j.AllowRepositoryCode = true
	if r := c.Run(ctx, j, analysis.TaskSpec{Key: "build.m09"}, &countingRunner{}); r.Status != domain.TaskFailed || !slices.Contains(r.Limitations, ReasonUnknownTask) || len(tool.ran) != 0 {
		t.Fatalf("unplanned key %+v ran=%v", r, tool.ran)
	}
}

// TC-69 (composite): an ecosystem whose source is in scope but which has
// no analyzer in this build plans one visible, not-required gap task per
// ecosystem; it ends unsupported/analyzer_pack_missing with a not-run
// envelope. Build files alone, vendored, hidden and testdata files and
// out-of-scope source do not create a gap, and an "any" analyzer does not
// fill a language gap.
func TestGapsForEcosystemsWithoutAnalyzer(t *testing.T) {
	ctx := context.Background()
	j := jobFiles(t, map[string]string{
		"go.mod": "module x\n\ngo 1.22\n", "a.go": "package x\n", "Makefile": "all:\n", "package.json": "{}\n",
		"py/m.py": "print(1)\n", "py/n.py": "print(2)\n", "web/app.ts": "export {}\n",
		"vendor/v.rs": "fn main() {}\n", ".hidden/h.c": "int x;\n", "py/testdata/t.cc": "int y;\n", "node_modules/m/i.js": "x\n",
	})
	vet := &fake{d: desc("go.vet", "go-vet", true), keys: []string{"go-vet"}}
	secrets := &fake{d: Descriptor{ID: "any.secrets", Name: "secrets", Ecosystem: "any", Tool: "secrets", ParseVersion: version}}
	c, err := NewComposite(vet, secrets)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := c.PlanTasks(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, task := range tasks {
		keys = append(keys, task.Spec.Key)
	}
	if !slices.Equal(keys, []string{"go-vet", "pack-gap.javascript", "pack-gap.python"}) {
		t.Fatalf("keys %v", keys)
	}
	py := tasks[2]
	if py.Spec.Required || py.Spec.Stage != domain.StageStaticAnalysis || py.FilesInScope == nil || *py.FilesInScope != 2 || py.RepositoryCode {
		t.Fatalf("gap task %+v", py)
	}
	runner := &countingRunner{}
	res := c.Run(ctx, j, py.Spec, runner)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonAnalyzerPackMissing || runner.starts != 0 || len(res.Executions) != 0 {
		t.Fatalf("gap result %+v", res)
	}
	doc := readEnvelope(t, j, res, "pack-gap.python")
	if doc.Analyzer != (obsdoc.Analyzer{ID: "python.none", Ecosystem: "python", Required: false}) || doc.Outcome.ExitClass != obsdoc.ExitNotRun ||
		len(doc.Coverage.ProcessedPaths) != 0 || doc.Coverage.ProcessedPaths == nil || *doc.Coverage.FilesInScope != 2 ||
		!slices.Equal(doc.Limitations, []string{ReasonAnalyzerPackMissing}) {
		t.Fatalf("gap envelope %+v", doc)
	}
	for _, key := range []string{"pack-gap.rust", "pack-gap", "pack-gap.python.x"} {
		if r := c.Run(ctx, j, analysis.TaskSpec{Key: key}, runner); r.Status != domain.TaskFailed || !slices.Contains(r.Limitations, ReasonUnknownTask) {
			t.Fatalf("%s: %+v", key, r)
		}
	}

	// Scope decides: only the scoped ecosystem's gap remains.
	j.Scope = domain.Scope{Kind: "paths", Paths: []string{"py"}}
	if tasks, _ := c.PlanTasks(ctx, j); len(tasks) != 2 || tasks[1].Spec.Key != "pack-gap.python" {
		t.Fatalf("scoped plan %+v", tasks)
	}
	// An ecosystem with an analyzer is the analyzer's to plan: no gap even
	// when it plans nothing.
	pyAnalyzer := &fake{d: Descriptor{ID: "python.lint", Name: "py-lint", Ecosystem: "python", Required: true, Tool: "lint", ParseVersion: version}}
	c2, _ := NewComposite(vet, pyAnalyzer)
	if tasks, _ := c2.PlanTasks(ctx, j); len(tasks) != 1 || tasks[0].Spec.Key != "go-vet" {
		t.Fatalf("covered ecosystem %+v", tasks)
	}
	if _, err := NewComposite(&fake{d: desc("go.gap", "pack-gap", true)}); err == nil {
		t.Fatal("reserved gap name accepted")
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
