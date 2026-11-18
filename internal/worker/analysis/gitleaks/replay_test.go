package gitleaks

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without gitleaks: the scans return the streams
// captured from gitleaks 8.30.1 in testdata/ (real_test.go) for the
// fixtures of helpers_test.go with the placeholder in place of the secret.

const capturedVersion = "v8.30.1\n"

type replayCase struct {
	version string
	scans   map[string]anypacktest.Recorded // by scan root
	mutate  func(spec executor.Spec, r *executor.Result)
	files   map[string][]byte
	scope   domain.Scope
	before  func(fx anypacktest.Fixture)
}

type replayed struct {
	res analysis.TaskResult
	doc obsdoc.Document
	r   *anypacktest.Replay
	fx  anypacktest.Fixture
}

func isScan(s executor.Spec) bool { return len(s.Args) > 1 && s.Args[0] == "dir" }

func replay(t *testing.T, c replayCase) replayed {
	t.Helper()
	fake := anypacktest.FakeExecutable(t, Name)
	a, err := New(Settings{Executable: fake})
	if err != nil {
		t.Fatal(err)
	}
	if c.files == nil {
		c.files = fixtureFiles(anypacktest.Placeholder)
	}
	if c.version == "" {
		c.version = capturedVersion
	}
	fx := anypacktest.New(t, "", c.files, c.scope)
	if c.before != nil {
		c.before(fx)
	}
	r := &anypacktest.Replay{Fake: fake, Mutate: c.mutate, Answer: func(spec executor.Spec) anypacktest.Recorded {
		if slices.Equal(spec.Args, []string{"version"}) {
			return anypacktest.Recorded{Stdout: []byte(c.version)}
		}
		return c.scans[ScanRoot(spec.Args)]
	}}
	res := anypacktest.RunTask(t, a, fx.Job, Name, r)
	doc, _ := anypacktest.ReadEnvelope(t, fx.Job, res)
	return replayed{res: res, doc: doc, r: r, fx: fx}
}

func findings(t *testing.T) map[string]anypacktest.Recorded {
	return map[string]anypacktest.Recorded{".": anypacktest.LoadCapture(t, "findings")}
}

// TC-118 on captured bytes: exit 3 with as many records as "leaks found"
// is success with findings; each observation carries the rule, the place
// and the fixed message, never the value or its entropy; the scan runs
// with the CODEYE configuration, template and an empty ignore directory
// from task scratch.
func TestReplayFindings(t *testing.T) {
	p := replay(t, replayCase{scans: findings(t)})
	if p.res.Status != domain.TaskSucceeded || p.doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || p.doc.Active() != 4 {
		t.Fatalf("%s/%s %+v", p.res.Status, p.res.Reason, p.doc.Outcome)
	}
	if got := placesOf(t, p.doc); !slices.Equal(got, []string{"assets/blob.dat:1:aws-access-token", "config/settings.yaml:2:aws-access-token", "src/allow.py:1:aws-access-token", "src/app.py:3:aws-access-token"}) {
		t.Fatalf("places %v", got)
	}
	for _, o := range p.doc.Observations {
		if strings.Contains(o.RawMessage, "3.68") || o.Location.StartCol == 0 || o.Location.EndCol <= o.Location.StartCol {
			t.Fatalf("observation %+v", o)
		}
	}
	dir := filepath.Join(p.fx.Job.Scratch, "analyzers", Name)
	scan := p.r.Specs[1]
	if !slices.Equal(scan.Args, ScanArgs(".", filepath.Join(dir, "codeye-gitleaks.toml"), filepath.Join(dir, "ignore"), filepath.Join(dir, "report.tmpl"))) || scan.Dir != p.fx.Job.Snapshot.WorkspaceDir {
		t.Fatalf("scan %+v", scan)
	}
	for name, want := range map[string]string{"codeye-gitleaks.toml": Config, "report.tmpl": Template} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", name, b, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "ignore")); err != nil || len(entries) != 0 {
		t.Fatalf("ignore dir %v %v", entries, err)
	}
	for _, field := range []string{".Secret", ".Match", ".Line", ".Fingerprint", ".Commit", ".Author", ".Email"} {
		if strings.Contains(Template, field) {
			t.Fatalf("template prints %s", field)
		}
	}
}

func TestReplayClean(t *testing.T) {
	p := replay(t, replayCase{files: cleanFiles(), scans: map[string]anypacktest.Recorded{".": anypacktest.LoadCapture(t, "clean")}})
	if p.res.Status != domain.TaskSucceeded || p.doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || len(p.doc.Observations) != 0 {
		t.Fatalf("%s/%s %+v", p.res.Status, p.res.Reason, p.doc.Outcome)
	}
}

func TestReplayScopedRoots(t *testing.T) {
	p := replay(t, replayCase{scope: domain.Scope{Kind: "paths", Paths: []string{"src", "config/"}},
		scans: map[string]anypacktest.Recorded{"config": anypacktest.LoadCapture(t, "scoped-r01"), "src": anypacktest.LoadCapture(t, "scoped-r02")}})
	if p.res.Status != domain.TaskSucceeded || len(p.r.Specs) != 3 || ScanRoot(p.r.Specs[1].Args) != "config" || ScanRoot(p.r.Specs[2].Args) != "src" ||
		!slices.Equal(p.r.Specs[1].Args[len(p.r.Specs[1].Args)-2:], []string{"--", "./config"}) {
		t.Fatalf("%s/%s specs %d", p.res.Status, p.res.Reason, len(p.r.Specs))
	}
	if got := placesOf(t, p.doc); len(got) != 4 {
		t.Fatalf("places %v", got)
	}
}

// Every failure is a failure, never "no secret": a fatal error or skipped
// path (exit 1 or a warning), another exit code, records and logs that
// disagree, a cut stream, a record with a field the template never prints
// (a changed template could print the value), the version, timeout,
// cancellation, uncertain cleanup and a snapshot file that no longer
// matches its manifest.
func TestReplayFailuresAreNotSuccess(t *testing.T) {
	good := anypacktest.LoadCapture(t, "findings")
	clean := anypacktest.LoadCapture(t, "clean")
	with := func(r anypacktest.Recorded, f func(*anypacktest.Recorded)) map[string]anypacktest.Recorded {
		f(&r)
		return map[string]anypacktest.Recorded{".": r}
	}
	onScan := func(f func(r *executor.Result)) func(executor.Spec, *executor.Result) {
		return func(s executor.Spec, r *executor.Result) {
			if isScan(s) {
				f(r)
			}
		}
	}
	for _, c := range []struct {
		name   string
		c      replayCase
		status domain.TaskStatus
		reason string
	}{
		{"exit 1", replayCase{scans: with(clean, func(r *anypacktest.Recorded) { r.Exit = 1 })}, domain.TaskFailed, ReasonScanIncomplete},
		{"skipped path", replayCase{scans: with(clean, func(r *anypacktest.Recorded) {
			r.Stderr = append([]byte("9:53PM WRN skipping file: permission denied\n"), r.Stderr...)
		})}, domain.TaskFailed, ReasonScanIncomplete},
		{"exit 2", replayCase{scans: with(good, func(r *anypacktest.Recorded) { r.Exit = 2 })}, domain.TaskFailed, ReasonFailed},
		{"exit 0 with records", replayCase{scans: with(good, func(r *anypacktest.Recorded) { r.Exit = 0 })}, domain.TaskFailed, ReasonExitMismatch},
		{"leak count", replayCase{scans: with(good, func(r *anypacktest.Recorded) {
			r.Stderr = []byte(strings.Replace(string(r.Stderr), "leaks found: 4", "leaks found: 5", 1))
		})}, domain.TaskFailed, ReasonExitMismatch},
		{"exit 3 without records", replayCase{scans: with(clean, func(r *anypacktest.Recorded) { r.Exit = ExitCodeLeaks })}, domain.TaskFailed, ReasonExitMismatch},
		{"cut", replayCase{scans: with(good, func(r *anypacktest.Recorded) { r.Stdout = r.Stdout[:len(r.Stdout)-40] })}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"value field", replayCase{scans: with(good, func(r *anypacktest.Recorded) {
			r.Stdout = []byte(strings.Replace(string(r.Stdout), `"symlink":""`, `"symlink":"","secret":"REDACTED"`, 1))
		})}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"trailer", replayCase{scans: with(good, func(r *anypacktest.Recorded) {
			r.Stdout = []byte(strings.Replace(string(r.Stdout), `"findings":4`, `"findings":3`, 1))
		})}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"version", replayCase{version: "version is set by build process\n", scans: findings(t)}, domain.TaskFailed, anypack.ReasonToolVersion},
		{"truncated", replayCase{scans: findings(t), mutate: onScan(func(r *executor.Result) { r.Stdout.Truncated = true })}, domain.TaskFailed, anypack.ReasonOutputTruncated},
		{"timeout", replayCase{scans: findings(t), mutate: onScan(func(r *executor.Result) { r.Status, r.ExitCode = executor.StatusTimedOut, nil })}, domain.TaskTimedOut, anypack.ReasonTimeout},
		{"cancelled", replayCase{scans: findings(t), mutate: onScan(func(r *executor.Result) { r.Status, r.ExitCode = executor.StatusCancelled, nil })}, domain.TaskCancelled, anypack.ReasonCancelled},
		{"cleanup uncertain", replayCase{scans: findings(t), mutate: onScan(func(r *executor.Result) { r.Cleanup = executor.CleanupUncertain })}, domain.TaskFailed, anypack.ReasonCleanupUncertain},
		{"snapshot integrity", replayCase{scans: findings(t), before: func(fx anypacktest.Fixture) {
			p := filepath.Join(fx.Job.Snapshot.SnapshotDir, "README.md")
			_ = os.Chmod(p, 0o600)
			if err := os.WriteFile(p, []byte("changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}}, domain.TaskFailed, anypack.ReasonSnapshotIntegrity},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := replay(t, c.c)
			if p.res.Status != c.status || p.res.Reason != c.reason || len(p.doc.Coverage.ProcessedPaths) != 0 || p.doc.Parse.OK {
				t.Fatalf("%s/%s outcome %+v coverage %+v errors %+v", p.res.Status, p.res.Reason, p.doc.Outcome, p.doc.Coverage, p.doc.ToolErrors)
			}
		})
	}
}

// The allowlist replica matches the gitleaks 8.30.1 default path patterns
// a file or one of its directories hits.
func TestAllowlisted(t *testing.T) {
	for p, want := range map[string]bool{
		"go.sum": true, "sub/go.mod": true, "package-lock.json": true, "web/yarn.lock": true, "vendor/modules.txt": true, ".gitleaks.toml": true,
		"img/logo.PNG": true, "lib/jquery-3.7.1.min.js": true, "venv/lib/python3.12/site.py": true, "pkg/x-1.0.dist-info/METADATA": true,
		"src/app.py": false, "config/settings.yaml": false, "README.md": false, "vendor/other/x.go": false, ".gitleaksignore": false,
	} {
		if Allowlisted(p) != want {
			t.Fatalf("Allowlisted(%q) = %v", p, !want)
		}
	}
}

func TestPlanAndVersion(t *testing.T) {
	fx := anypacktest.New(t, "", fixtureFiles(anypacktest.Placeholder), domain.Scope{})
	tasks, err := Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != Name || tasks[0].Spec.Required || *tasks[0].FilesInScope != 13 {
		t.Fatalf("plan %+v %v", tasks, err)
	}
	docs := anypacktest.New(t, "", anypacktest.Text(map[string]string{"README.md": "# x\n", "config.yaml": "a: 1\n"}), domain.Scope{})
	if tasks, _ := Plan(context.Background(), docs.Job); len(tasks) != 0 {
		t.Fatalf("no-language plan %+v", tasks)
	}
	for in, want := range map[string]string{"v8.30.1\n": "8.30.1", "8.30.1\n": "", "version is set by build process\n": "", "v8.30.1\nx\n": ""} {
		if got, ok := ParseVersion([]byte(in)); got != want || ok != (want != "") {
			t.Fatalf("ParseVersion(%q) = %q %v", in, got, ok)
		}
	}
}
