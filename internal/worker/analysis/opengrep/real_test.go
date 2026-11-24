package opengrep

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real Opengrep tests run only with CODEYE_TEST_OPENGREP=/abs/opengrep.
// CODEYE_OPENGREP_CAPTURE=DIR also writes the scan's stdout, stderr, exit
// code and JSON copy to DIR/fixtures.*, which is how testdata/ was captured
// for the replay tests:
//
//	CODEYE_TEST_OPENGREP=/abs/opengrep CODEYE_OPENGREP_CAPTURE=$PWD/internal/worker/analysis/opengrep/testdata \
//	  go test -count=1 -run TestRealOpengrep ./internal/worker/analysis/opengrep/
//
// CODEYE_TEST_STRACE=/abs/strace adds the traced run that shows Opengrep
// opens no socket (TestRealOpengrepNoNetwork).

func testOpengrep(t *testing.T) string {
	t.Helper()
	p := os.Getenv("CODEYE_TEST_OPENGREP")
	if p == "" {
		t.Skip("CODEYE_TEST_OPENGREP not set")
	}
	return p
}

// TC-139: every positive fixture line of the rule pack is found with its
// tool flow, no negative fixture yields a finding, nosem comments are
// reported as suppressed, and the source and snapshot are not changed.
func TestRealOpengrepFixtures(t *testing.T) {
	a, err := New(Settings{Executable: testOpengrep(t)})
	if err != nil {
		t.Fatal(err)
	}
	files := fixtureFiles(t)
	fx := anypacktest.New(t, filepath.Join(t.TempDir(), "공백 & (dir)"), files, domain.Scope{})
	before, snapBefore := packtest.TreeHash(t, fx.SourceRoot), packtest.TreeHash(t, fx.Job.Snapshot.SnapshotDir)
	res := packtest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	if packtest.TreeHash(t, fx.SourceRoot) != before || packtest.TreeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("opengrep changed the source or the snapshot")
	}
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !doc.Parse.OK {
		t.Fatalf("%s/%s outcome %+v parse %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.Parse, doc.ToolErrors)
	}
	assertFixtureResult(t, files, doc)
	packtest.AssertNoNodePaths(t, raw, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir)
	capture(t, fx.Job.Scratch, res.Executions[len(res.Executions)-1].Result)
}

func assertFixtureResult(t *testing.T, files map[string][]byte, doc obsdoc.Document) {
	t.Helper()
	active, suppressed := places(doc)
	if want := expectedPlaces(files); !slices.Equal(active, want) {
		t.Fatalf("active findings\n got %v\nwant %v", active, want)
	}
	if !slices.Equal(suppressed, suppressedWant) {
		t.Fatalf("suppressed %v, want %v", suppressed, suppressedWant)
	}
	checkObservations(t, doc)
	var ids []string
	for _, r := range doc.Rules {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, RuleIDs()) {
		t.Fatalf("rules %v, want %v", ids, RuleIDs())
	}
	if doc.RulePack.SourceSuppressions != obsdoc.SuppressReported || doc.RulePack.RepositoryConfig != obsdoc.RepoConfigOverridden {
		t.Fatalf("rule pack %+v", doc.RulePack)
	}
	// Every selected file of a rule language was scanned and claimed.
	n := 0
	for p := range files {
		if LanguageOf(Languages(), p) != "" {
			n++
		}
	}
	if len(doc.Coverage.ProcessedPaths) != n {
		t.Fatalf("processed %d of %d: %+v", len(doc.Coverage.ProcessedPaths), n, doc.Coverage)
	}
}

func capture(t *testing.T, scratch string, r executor.Result) {
	t.Helper()
	dir := os.Getenv("CODEYE_OPENGREP_CAPTURE")
	if dir == "" {
		return
	}
	copyData, err := os.ReadFile(filepath.Join(scratch, "analyzers", Name, execScan+".json"))
	if err != nil {
		t.Fatal(err)
	}
	code := "none"
	if r.ExitCode != nil {
		code = strconv.Itoa(*r.ExitCode)
	}
	for suffix, data := range map[string][]byte{".stdout": r.Stdout.Data, ".stderr": r.Stderr.Data, ".exit": []byte(code + "\n"), ".json": copyData} {
		if bytes.Contains(data, []byte("/home/")) || bytes.Contains(data, []byte("/tmp/")) {
			t.Fatalf("capture fixtures%s holds a node path", suffix)
		}
		if err := os.WriteFile(filepath.Join(dir, "fixtures"+suffix), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TC-140: a traced run of the version probe and of the scan argv the pack
// builds, with the pack's environment, makes no network system call and
// starts no other program.
func TestRealOpengrepNoNetwork(t *testing.T) {
	tool := testOpengrep(t)
	strace := os.Getenv("CODEYE_TEST_STRACE")
	if strace == "" {
		t.Skip("CODEYE_TEST_STRACE not set")
	}
	a, err := New(Settings{Executable: tool})
	if err != nil {
		t.Fatal(err)
	}
	fx := anypacktest.New(t, "", fixtureFiles(t), domain.Scope{})
	rr := &recordingRunner{inner: &executor.Executor{}}
	if res := packtest.RunTask(t, a, fx.Job, Name, rr); res.Status != domain.TaskSucceeded {
		t.Fatalf("task %s/%s", res.Status, res.Reason)
	}
	if len(rr.specs) < 2 {
		t.Fatalf("specs %d", len(rr.specs))
	}
	// The scan's rule files and JSON copy were in the finished task's
	// scratch; trace the same argv against fresh copies.
	for i, spec := range rr.specs {
		traceFile := filepath.Join(t.TempDir(), "trace")
		args := append([]string{"-f", "-qq", "-e", "trace=network,process", "-o", traceFile, "--", spec.Executable}, spec.Args...)
		s := spec
		s.Executable, s.Args = strace, args
		s.TempParent = t.TempDir()
		s.Env = map[string]string{}
		for k, v := range spec.Env {
			s.Env[k] = v
		}
		if i > 0 {
			s.Args = retarget(t, args, fx.Job.Scratch)
		}
		res := (&executor.Executor{}).Run(context.Background(), s)
		if res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 {
			t.Fatalf("traced run %d: %s %s %v stderr %s", i, res.Status, res.Reason, res.ExitCode, res.Stderr.Data)
		}
		b, err := os.ReadFile(traceFile)
		if err != nil {
			t.Fatal(err)
		}
		execs := 0
		for _, line := range strings.Split(string(b), "\n") {
			switch {
			case strings.Contains(line, "socket(") || strings.Contains(line, "connect(") || strings.Contains(line, "sendto(") || strings.Contains(line, "bind("):
				t.Fatalf("network system call in run %d: %s", i, line)
			case strings.Contains(line, "execve("):
				execs++
			}
		}
		if execs != 1 {
			t.Fatalf("run %d started %d programs:\n%s", i, execs, b)
		}
	}
}

type recordingRunner struct {
	inner executor.Runner
	specs []executor.Spec
}

func (r *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	r.specs = append(r.specs, spec)
	return r.inner.Run(ctx, spec)
}

// retarget rewrites the scan's scratch paths (rule files, JSON copy) to a
// fresh directory holding the rule files again.
func retarget(t *testing.T, args []string, scratch string) []string {
	t.Helper()
	dir := t.TempDir()
	old := filepath.Join(scratch, "analyzers", Name)
	if err := os.Mkdir(filepath.Join(dir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, l := range Languages() {
		if err := os.WriteFile(filepath.Join(dir, "rules", l.Name+".yaml"), l.File, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.Replace(a, old, dir, 1)
	}
	return out
}
