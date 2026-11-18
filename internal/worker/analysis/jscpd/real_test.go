package jscpd

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real jscpd tests run only with CODEYE_TEST_JSCPD and CODEYE_TEST_NODE.
// CODEYE_W21_CAPTURE=DIR also writes the jscpd.run streams and exit code to
// DIR, which is how testdata/*.stdout|stderr|exit were captured:
//
//	CODEYE_TEST_JSCPD=/abs/node_modules/jscpd/run-jscpd.js CODEYE_TEST_NODE=/abs/bin/node \
//	  CODEYE_W21_CAPTURE=$PWD/internal/worker/analysis/jscpd/testdata \
//	  go test -count=1 -run TestRealJscpd ./internal/worker/analysis/jscpd/

type recordingRunner struct {
	inner executor.Runner
	specs []executor.Spec
}

func (r *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	r.specs = append(r.specs, spec)
	return r.inner.Run(ctx, spec)
}

func clonesOf(t *testing.T, doc obsdoc.Document) []string {
	t.Helper()
	var got []string
	for _, o := range doc.Observations {
		if o.Metric == nil || o.Metric.Name != MetricDuplicate || o.Metric.Unit != "lines" || o.Metric.Threshold != MinLines || o.Metric.Value < MinLines || o.Category != "maintainability" ||
			o.RuleID != RuleDuplicate || o.LocationState != obsdoc.LocationResolved || len(o.Related) != 1 || o.Related[0].LocationState != obsdoc.LocationResolved {
			t.Fatalf("observation %+v", o)
		}
		l, r := o.Location, o.Related[0].Location
		got = append(got, l.Path+":"+strconv.Itoa(l.StartLine)+"-"+strconv.Itoa(l.EndLine)+"~"+r.Path+":"+strconv.Itoa(r.StartLine)+"-"+strconv.Itoa(r.EndLine)+"="+strconv.Itoa(o.Metric.Value))
	}
	sort.Strings(got)
	return got
}

// TC-117: the block duplicated in src/a.js and lib/b.js is one metric
// observation with its other place, never a finding; the block between
// jscpd:ignore markers, the vendored copy and the Markdown copy are not
// compared; the files jscpd silently skips are recorded as skipped; the
// generated secret inside the block reaches no stream, scratch file or
// envelope; repository .jscpd.json and package.json settings are not read.
func TestRealJscpdClones(t *testing.T) {
	script, node := testTools(t)
	a, err := New(Config{Executable: script, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	secret := anypacktest.Secret(t)
	fx := anypacktest.New(t, filepath.Join(t.TempDir(), "공백 & (dir)"), fixtureFiles(secret), domain.Scope{})
	before, snapBefore := anypacktest.TreeHash(t, fx.SourceRoot), anypacktest.TreeHash(t, fx.Job.Snapshot.SnapshotDir)
	rr := &recordingRunner{inner: &executor.Executor{}}
	res := anypacktest.RunTask(t, a, fx.Job, Name, rr)
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	if anypacktest.TreeHash(t, fx.SourceRoot) != before || anypacktest.TreeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("jscpd changed the source or the snapshot")
	}
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || doc.Active() != 0 || !doc.Parse.OK {
		t.Fatalf("%s/%s outcome %+v parse %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.Parse, doc.ToolErrors)
	}
	if got := clonesOf(t, doc); len(got) != 1 || got[0] != "lib/b.js:1-17~src/a.js:1-17=16" {
		t.Fatalf("clones %v", got)
	}
	if want := []string{"lib/b.js", "src/a.js", "src/ignored.js", "src/types/a.d.ts", "tools/small.py"}; !slices.Equal(doc.Coverage.ProcessedPaths, want) {
		t.Fatalf("processed %v", doc.Coverage.ProcessedPaths)
	}
	if doc.Coverage.Units[SkipTooLarge] != 1 || doc.Coverage.Units[SkipNotUTF8] != 1 || doc.Coverage.FilesSkipped != 2 || !slices.Contains(doc.Limitations, LimFilesSkipped) {
		t.Fatalf("coverage %+v limitations %v", doc.Coverage, doc.Limitations)
	}
	if doc.Tool.Version == nil || !VersionTested(*doc.Tool.Version) || doc.RulePack.RepositoryConfig != obsdoc.RepoConfigOverridden || doc.RulePack.SourceSuppressions != obsdoc.SuppressHonored {
		t.Fatalf("tool %+v rule pack %+v", doc.Tool, doc.RulePack)
	}
	run := rr.specs[len(rr.specs)-1]
	if run.Executable != node.Executable || run.Args[0] != script || run.Env["PATH"] != node.Dir || run.Dir != fx.Job.Snapshot.WorkspaceDir {
		t.Fatalf("run spec %+v", run)
	}
	for _, k := range []string{"JSCPD_CACHE_DIR", "XDG_CACHE_HOME"} {
		if rel, err := filepath.Rel(fx.Job.Scratch, run.Env[k]); err != nil || filepath.IsAbs(rel) || rel == ".." || len(rel) > 1 && rel[:2] == ".." {
			t.Fatalf("%s=%q is not in scratch", k, run.Env[k])
		}
	}
	anypacktest.AssertAbsent(t, secret, "envelope", raw)
	anypacktest.AssertTreeAbsent(t, secret, fx.Job.Scratch)
	anypacktest.AssertResultAbsent(t, secret, res)
	anypacktest.Capture(t, "clones", execRun, res, secret)
}

// Without a duplicated block jscpd exits 0 with "Found 0 clones.": success
// without observations over the files handed to it.
func TestRealJscpdNoClones(t *testing.T) {
	script, node := testTools(t)
	a, err := New(Config{Executable: script, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	fx := anypacktest.New(t, "", noCloneFiles(), domain.Scope{})
	res := anypacktest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, _ := anypacktest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || len(doc.Observations) != 0 || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/one.js", "tools/small.py"}) {
		t.Fatalf("%s/%s %+v %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage, doc.ToolErrors)
	}
	anypacktest.Capture(t, "noclones", execRun, res)
}
