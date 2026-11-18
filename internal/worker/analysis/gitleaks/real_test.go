package gitleaks

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real gitleaks tests run only with CODEYE_TEST_GITLEAKS.
// CODEYE_W21_CAPTURE=DIR also writes the scan streams and exit code to DIR
// (refusing any capture that holds the generated secret), which is how
// testdata/*.stdout|stderr|exit were captured:
//
//	CODEYE_TEST_GITLEAKS=/abs/gitleaks CODEYE_W21_CAPTURE=$PWD/internal/worker/analysis/gitleaks/testdata \
//	  go test -count=1 -run TestRealGitleaks ./internal/worker/analysis/gitleaks/

type recordingRunner struct {
	inner executor.Runner
	specs []executor.Spec
}

func (r *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	r.specs = append(r.specs, spec)
	return r.inner.Run(ctx, spec)
}

func placesOf(t *testing.T, doc obsdoc.Document) []string {
	t.Helper()
	var got []string
	for _, o := range doc.Observations {
		if o.Metric != nil || o.Category != "security" || o.RawMessage != Message(o.RuleID) || o.Suppressed || o.LocationState != obsdoc.LocationResolved || o.Location.ColumnUnit != "byte" {
			t.Fatalf("observation %+v", o)
		}
		got = append(got, o.Location.Path+":"+strconv.Itoa(o.Location.StartLine)+":"+o.RuleID)
	}
	sort.Strings(got)
	return got
}

// TC-118: the generated secret is reported where it is, without the value:
// inline gitleaks:allow and a repository .gitleaks.toml do not hide it, the
// repository .gitleaksignore does (honored, bound to its manifest digest),
// gitleaks' default allowlist skips go.sum, package-lock.json and
// vendor/modules.txt (CODEYE's replica agrees), and the value appears in no
// stream, argv, scratch file or envelope.
func TestRealGitleaksFindsWithoutValues(t *testing.T) {
	a, err := New(Settings{Executable: testGitleaks(t)})
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
		t.Fatal("gitleaks changed the source or the snapshot")
	}
	anypacktest.AssertAbsent(t, secret, "envelope", raw)
	anypacktest.AssertTreeAbsent(t, secret, fx.Job.Scratch)
	anypacktest.AssertResultAbsent(t, secret, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !doc.Parse.OK {
		t.Fatalf("%s/%s outcome %+v parse %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.Parse, doc.ToolErrors)
	}
	// gitleaks also reads the binary blob (measured): the location is
	// reported although the file is not claimed processed.
	if got, want := placesOf(t, doc), []string{"assets/blob.dat:1:aws-access-token", "config/settings.yaml:2:aws-access-token", "src/allow.py:1:aws-access-token", "src/app.py:3:aws-access-token"}; !slices.Equal(got, want) {
		t.Fatalf("places %v, want %v", got, want)
	}
	if rp := doc.RulePack; rp.RepositoryConfig != obsdoc.RepoConfigHonored || rp.SourceSuppressions != obsdoc.SuppressDisabled || len(rp.ConfigFiles) != 1 || rp.ConfigFiles[0].Path != ".gitleaksignore" {
		t.Fatalf("rule pack %+v", rp)
	}
	for _, p := range []string{"go.sum", "package-lock.json", "vendor/modules.txt", ".gitleaks.toml"} {
		if !Allowlisted(p) || slices.Contains(doc.Coverage.ProcessedPaths, p) {
			t.Fatalf("%s: allowlist replica or processed paths disagree with gitleaks", p)
		}
	}
	if want := []string{".gitleaksignore", "README.md", "config/settings.yaml", "main.go", "src/allow.py", "src/app.py", "src/ignored.py"}; !slices.Equal(doc.Coverage.ProcessedPaths, want) {
		t.Fatalf("processed %v", doc.Coverage.ProcessedPaths)
	}
	if doc.Coverage.Units[UnitAllowlisted] != 4 || doc.Coverage.Units[UnitEmpty] != 1 || doc.Coverage.Units[UnitNotText] != 1 || !slices.Contains(doc.Limitations, LimHistoryNotScanned) || !slices.Contains(doc.Limitations, LimIgnoreFileHonored) {
		t.Fatalf("coverage %+v limitations %v", doc.Coverage, doc.Limitations)
	}
	scan := rr.specs[len(rr.specs)-1]
	joined := strings.Join(scan.Args, " ")
	for _, want := range []string{"dir . ", "--redact=100", "--no-banner", "--log-level info", "--ignore-gitleaks-allow", "--report-format template", "--report-path -"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("scan argv lacks %q: %v", want, scan.Args)
		}
	}
	if slices.Contains(scan.Args, "-v") || slices.Contains(scan.Args, "--verbose") || scan.Dir != fx.Job.Snapshot.WorkspaceDir || len(scan.Env) != 2 {
		t.Fatalf("scan spec %+v", scan)
	}
	anypacktest.Capture(t, "findings", execScan, res, secret)
}

// A path scope scans each root (cwd = the workspace root, so gitleaks
// reports snapshot paths) and reads only the .gitleaksignore of a scan root.
func TestRealGitleaksScopedRoots(t *testing.T) {
	a, err := New(Settings{Executable: testGitleaks(t)})
	if err != nil {
		t.Fatal(err)
	}
	secret := anypacktest.Secret(t)
	fx := anypacktest.New(t, "", fixtureFiles(secret), domain.Scope{Kind: "paths", Paths: []string{"src", "config/"}})
	res := anypacktest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	anypacktest.AssertAbsent(t, secret, "envelope", raw)
	anypacktest.AssertTreeAbsent(t, secret, fx.Job.Scratch)
	anypacktest.AssertResultAbsent(t, secret, res)
	if res.Status != domain.TaskSucceeded || len(doc.Executions) != 3 || doc.Executions[1].Name != execName(0, 2) || doc.Executions[2].Name != execName(1, 2) {
		t.Fatalf("%s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Executions)
	}
	// The root .gitleaksignore is outside both scan roots: src/ignored.py is
	// reported here.
	if got, want := placesOf(t, doc), []string{"config/settings.yaml:2:aws-access-token", "src/allow.py:1:aws-access-token", "src/app.py:3:aws-access-token", "src/ignored.py:1:aws-access-token"}; !slices.Equal(got, want) {
		t.Fatalf("places %v", got)
	}
	if len(doc.RulePack.ConfigFiles) != 0 || slices.Contains(doc.Limitations, LimIgnoreFileHonored) {
		t.Fatalf("rule pack %+v", doc.RulePack)
	}
	anypacktest.Capture(t, "scoped-r01", execName(0, 2), res, secret)
	anypacktest.Capture(t, "scoped-r02", execName(1, 2), res, secret)
}

// No secret: exit 0 with "no leaks found" and the trailer of zero records.
func TestRealGitleaksClean(t *testing.T) {
	a, err := New(Settings{Executable: testGitleaks(t)})
	if err != nil {
		t.Fatal(err)
	}
	fx := anypacktest.New(t, "", cleanFiles(), domain.Scope{})
	res := anypacktest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, _ := anypacktest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || len(doc.Observations) != 0 || len(doc.Coverage.ProcessedPaths) != 3 {
		t.Fatalf("%s/%s %+v %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage, doc.ToolErrors)
	}
	anypacktest.Capture(t, "clean", execScan, res)
}
