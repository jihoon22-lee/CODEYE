package lizard

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real lizard tests run only with CODEYE_TEST_LIZARD. CODEYE_STRUCTURE_CAPTURE=DIR
// also writes the lizard.run streams and exit code of each case to DIR,
// which is how testdata/*.stdout|stderr|exit were captured:
//
//	CODEYE_TEST_LIZARD=/abs/lizard CODEYE_STRUCTURE_CAPTURE=$PWD/internal/worker/analysis/lizard/testdata \
//	  go test -count=1 -run TestRealLizard ./internal/worker/analysis/lizard/

type recordingRunner struct {
	inner executor.Runner
	specs []executor.Spec
}

func (r *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	r.specs = append(r.specs, spec)
	return r.inner.Run(ctx, spec)
}

type realResult struct {
	fx    anypacktest.Fixture
	res   analysis.TaskResult
	doc   obsdoc.Document
	raw   []byte
	specs []executor.Spec
}

func realRun(t *testing.T, files map[string][]byte) realResult {
	t.Helper()
	a, err := New(Config{Executable: testLizard(t)})
	if err != nil {
		t.Fatal(err)
	}
	fx := anypacktest.New(t, filepath.Join(t.TempDir(), "공백 & (dir)"), files, domain.Scope{})
	before, snapBefore := anypacktest.TreeHash(t, fx.SourceRoot), anypacktest.TreeHash(t, fx.Job.Snapshot.SnapshotDir)
	rr := &recordingRunner{inner: &executor.Executor{}}
	res := anypacktest.RunTask(t, a, fx.Job, Name, rr)
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	if anypacktest.TreeHash(t, fx.SourceRoot) != before || anypacktest.TreeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("lizard changed the source or the snapshot")
	}
	return realResult{fx: fx, res: res, doc: doc, raw: raw, specs: rr.specs}
}

func itoa(n int) string { return strconv.Itoa(n) }

func metricsOf(t *testing.T, doc obsdoc.Document) []string {
	t.Helper()
	var got []string
	for _, o := range doc.Observations {
		if o.Metric == nil || o.Metric.Name != MetricCCN || o.Metric.Threshold != CCNThreshold || o.Metric.Unit != "count" || o.Category != "maintainability" || o.Suppressed ||
			o.LocationState != obsdoc.LocationResolved || o.RawRef.Execution != execRun || o.RawRef.Stream != "stdout" || o.RuleID != RuleCCN {
			t.Fatalf("observation %+v", o)
		}
		got = append(got, o.Location.Path+":"+itoa(o.Location.StartLine)+"="+itoa(o.Metric.Value))
	}
	sort.Strings(got)
	return got
}

// The default argument value holding a generated secret never reaches a
// stream, the scratch directory or the envelope (XML carries bare function
// names); the function over the threshold is one metric observation, never
// a finding, and the forgiven, simple and data-directory functions none.
func TestRealLizardMetrics(t *testing.T) {
	secret := anypacktest.Secret(t)
	r := realRun(t, fixtureFiles(secret, false))
	doc := r.doc
	if r.res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || doc.Active() != 0 || !doc.Parse.OK {
		t.Fatalf("outcome %+v parse %+v errors %+v", doc.Outcome, doc.Parse, doc.ToolErrors)
	}
	if got, want := metricsOf(t, doc), []string{"src/complex.py:4=17", "src/route/route.go:3=17"}; !slices.Equal(got, want) {
		t.Fatalf("metrics %v, want %v", got, want)
	}
	if want := []string{"src/complex.py", "src/route/route.go", "web/add.js"}; !slices.Equal(doc.Coverage.ProcessedPaths, want) || *doc.Coverage.FilesInScope != 3 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	if doc.Tool.Version == nil || !VersionTested(*doc.Tool.Version) || doc.RulePack.SourceSuppressions != obsdoc.SuppressHonored || doc.RulePack.RepositoryConfig != obsdoc.RepoConfigOverridden {
		t.Fatalf("tool %+v rule pack %+v", doc.Tool, doc.RulePack)
	}
	if !slices.Contains(doc.Limitations, LimMetricsNotDefects) {
		t.Fatalf("limitations %v", doc.Limitations)
	}
	run := r.specs[len(r.specs)-1]
	if run.Dir != r.fx.Job.Snapshot.WorkspaceDir || run.WorkspaceRoot != r.fx.Job.Snapshot.WorkspaceDir || !slices.Contains(run.ProtectedRoots, r.fx.Job.Snapshot.SnapshotDir) || run.Timeout != anypack.DefaultTimeout {
		t.Fatalf("run spec %+v", run)
	}
	keys := []string{}
	for k := range run.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !slices.Equal(keys, []string{"LANG", "PATH", "PYTHONDONTWRITEBYTECODE", "PYTHONIOENCODING", "PYTHONNOUSERSITE"}) || strings.Contains(run.Env["PATH"], string(filepath.ListSeparator)) {
		t.Fatalf("env %v", run.Env)
	}
	anypacktest.AssertAbsent(t, secret, "envelope", r.raw)
	anypacktest.AssertTreeAbsent(t, secret, r.fx.Job.Scratch)
	anypacktest.AssertResultAbsent(t, secret, r.res)
	anypacktest.Capture(t, "metrics", execRun, r.res, secret)
}

// lizard 1.26.1 decodes a Latin-1 file and measures it (measured: its
// function is in the Function measure, nothing on stderr), so the file is
// processed. The stderr "skip" lines are covered by the replay tests.
func TestRealLizardReadsNonUTF8(t *testing.T) {
	r := realRun(t, fixtureFiles(anypacktest.Placeholder, true))
	doc := r.doc
	if r.res.Status != domain.TaskSucceeded || doc.Coverage.FilesSkipped != 0 || !slices.Contains(doc.Coverage.ProcessedPaths, "native/latin1.c") || len(doc.ToolErrors) != 0 {
		t.Fatalf("outcome %+v coverage %+v limitations %v errors %+v", doc.Outcome, doc.Coverage, doc.Limitations, doc.ToolErrors)
	}
	anypacktest.Capture(t, "latin1", execRun, r.res)
}
