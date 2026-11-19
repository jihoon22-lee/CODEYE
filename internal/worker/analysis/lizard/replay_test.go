package lizard

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
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without lizard: the lizard executions return the
// streams captured from lizard 1.26.1 in testdata/ (real_test.go) for the
// fixture of helpers_test.go.

const capturedVersion = "1.26.1\n"

type replayCase struct {
	version string
	run     anypacktest.Recorded
	mutate  func(spec executor.Spec, r *executor.Result)
	files   map[string][]byte
	scope   domain.Scope
	before  func(anypacktest.Fixture)
}

func replay(t *testing.T, c replayCase) (analysis.TaskResult, obsdoc.Document, *anypacktest.Replay, anypacktest.Fixture) {
	t.Helper()
	fake := packtest.FakeExecutable(t, "lizard")
	a, err := New(Config{Executable: fake})
	if err != nil {
		t.Fatal(err)
	}
	if c.files == nil {
		c.files = fixtureFiles(anypacktest.Placeholder, false)
	}
	if c.version == "" {
		c.version = capturedVersion
	}
	fx := anypacktest.New(t, "", c.files, c.scope)
	if c.before != nil {
		c.before(fx)
	}
	r := &anypacktest.Replay{Fake: fake, Mutate: c.mutate, Answer: func(spec executor.Spec) anypacktest.Recorded {
		if slices.Equal(spec.Args, []string{"--version"}) {
			return anypacktest.Recorded{Stdout: []byte(c.version)}
		}
		return c.run
	}}
	res := packtest.RunTask(t, a, fx.Job, Name, r)
	doc, _ := anypacktest.ReadEnvelope(t, fx.Job, res)
	return res, doc, r, fx
}

// TC-117 on captured bytes: the complexity metrics are observations with
// metric values, never findings; lizard is started with argv, the attempt
// workspace as cwd, a CODEYE file list in scratch and an explicit
// environment.
func TestReplayMetrics(t *testing.T) {
	res, doc, r, fx := replay(t, replayCase{run: anypacktest.LoadCapture(t, "metrics")})
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || doc.Active() != 0 {
		t.Fatalf("outcome %+v", doc.Outcome)
	}
	if got, want := metricsOf(t, doc), []string{"src/complex.py:4=17", "src/route/route.go:3=17"}; !slices.Equal(got, want) {
		t.Fatalf("metrics %v", got)
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/complex.py", "src/route/route.go", "web/add.js"}) || doc.Coverage.Units[UnitAnalyzed] != 3 ||
		doc.Coverage.Units[anypack.UnitExcludedDataDir] != 2 || !slices.Contains(doc.Limitations, anypack.LimDataDirsExcluded) || doc.Target.Properties["excluded_directories"] != anypack.DataDirectories {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	if len(r.Specs) != 2 || !slices.Equal(r.Specs[0].Args, []string{"--version"}) {
		t.Fatalf("specs %+v", r.Specs)
	}
	run := r.Specs[1]
	list := filepath.Join(fx.Job.Scratch, "analyzers", Name, "files.txt")
	if !slices.Equal(run.Args, RunArgs(list)) || run.Dir != fx.Job.Snapshot.WorkspaceDir || !slices.Contains(run.ProtectedRoots, fx.Job.Snapshot.SnapshotDir) {
		t.Fatalf("run spec %+v", run)
	}
	b, err := os.ReadFile(list)
	if err != nil || string(b) != "src/complex.py\nsrc/route/route.go\nweb/add.js\n" {
		t.Fatalf("file list %q %v", b, err)
	}
	for _, a := range run.Args {
		if strings.Contains(a, "duplicate") || strings.HasPrefix(a, "-E") {
			t.Fatal("lizard extensions are never enabled (duplication is jscpd's)")
		}
	}
	if !slices.Equal(doc.RulePack.Selection, Selection) || doc.Target.Properties["threshold"] != "16" {
		t.Fatalf("rule pack %+v target %+v", doc.RulePack, doc.Target)
	}
}

// Every failure is a failure, never "no complex function": a non-zero exit,
// a cut or malformed XML, unknown stderr, a file lizard measured that CODEYE
// did not list, a version it cannot read, cancellation, timeout, uncertain
// cleanup and a workspace changed during the run.
func TestReplayFailuresAreNotSuccess(t *testing.T) {
	good := anypacktest.LoadCapture(t, "metrics")
	cut := good
	cut.Stdout = good.Stdout[:len(good.Stdout)/2]
	extra := good
	extra.Stdout = []byte(strings.Replace(string(good.Stdout), `<item name="web/add.js">`, `<item name="web/other.js">`, 1))
	for _, c := range []struct {
		name   string
		c      replayCase
		status domain.TaskStatus
		reason string
	}{
		{"exit 1", replayCase{run: anypacktest.Recorded{Stdout: good.Stdout, Exit: 1}}, domain.TaskFailed, ReasonFailed},
		{"exit 2 usage", replayCase{run: anypacktest.Recorded{Stderr: []byte("usage: lizard [options]\n"), Exit: 2}}, domain.TaskFailed, ReasonFailed},
		{"cut xml", replayCase{run: cut}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"empty", replayCase{run: anypacktest.Recorded{}}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"unlisted file", replayCase{run: extra}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"stderr", replayCase{run: anypacktest.Recorded{Stdout: good.Stdout, Stderr: []byte("Traceback (most recent call last):\n")}}, domain.TaskFailed, ReasonFailed},
		{"version", replayCase{version: "lizard 1.26.1\n", run: good}, domain.TaskFailed, anypack.ReasonToolVersion},
		{"truncated", replayCase{run: good, mutate: func(s executor.Spec, r *executor.Result) {
			if !slices.Equal(s.Args, []string{"--version"}) {
				r.Stdout.Truncated = true
			}
		}}, domain.TaskFailed, anypack.ReasonOutputTruncated},
		{"timeout", replayCase{run: good, mutate: func(s executor.Spec, r *executor.Result) {
			if !slices.Equal(s.Args, []string{"--version"}) {
				r.Status, r.ExitCode = executor.StatusTimedOut, nil
			}
		}}, domain.TaskTimedOut, anypack.ReasonTimeout},
		{"cancelled", replayCase{run: good, mutate: func(s executor.Spec, r *executor.Result) {
			if !slices.Equal(s.Args, []string{"--version"}) {
				r.Status, r.ExitCode = executor.StatusCancelled, nil
			}
		}}, domain.TaskCancelled, anypack.ReasonCancelled},
		{"cleanup uncertain", replayCase{run: good, mutate: func(s executor.Spec, r *executor.Result) {
			r.Cleanup = executor.CleanupUncertain
		}}, domain.TaskFailed, anypack.ReasonCleanupUncertain},
		{"drift", replayCase{run: good, mutate: func(s executor.Spec, r *executor.Result) {
			if !slices.Equal(s.Args, []string{"--version"}) {
				_ = os.WriteFile(filepath.Join(s.Dir, "web", "add.js"), []byte("changed\n"), 0o644)
			}
		}}, domain.TaskFailed, anypack.ReasonWorkspaceDrift},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, doc, _, _ := replay(t, c.c)
			if res.Status != c.status || res.Reason != c.reason || doc.Coverage.ProcessedPaths != nil && len(doc.Coverage.ProcessedPaths) != 0 || doc.Parse.OK {
				t.Fatalf("%s/%s outcome %+v coverage %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage)
			}
		})
	}
}

// An untested lizard release runs and says so; the XML is still parsed
// strictly.
func TestReplayUntestedVersion(t *testing.T) {
	res, doc, _, _ := replay(t, replayCase{version: "1.27.0\n", run: anypacktest.LoadCapture(t, "metrics")})
	if res.Status != domain.TaskSucceeded || doc.Tool.VersionTested || !slices.Contains(doc.Limitations, "tool_version_untested") {
		t.Fatalf("%s %+v %v", res.Status, doc.Tool, doc.Limitations)
	}
}

// A file lizard reports on stderr as unreadable is skipped: its metrics are
// not recorded, it is not processed and the limitation says so.
func TestReplaySkippedFile(t *testing.T) {
	run := anypacktest.LoadCapture(t, "metrics")
	run.Stderr = []byte("Error: doesn't support none utf encoding 'src/complex.py'\n")
	res, doc, _, _ := replay(t, replayCase{run: run})
	if res.Status != domain.TaskSucceeded || slices.Contains(doc.Coverage.ProcessedPaths, "src/complex.py") || doc.Coverage.FilesSkipped != 1 || !slices.Contains(doc.Limitations, LimFilesNotAnalyzed) {
		t.Fatalf("%s %+v %v", res.Status, doc.Coverage, doc.Limitations)
	}
	if got := metricsOf(t, doc); !slices.Equal(got, []string{"src/route/route.go:3=17"}) {
		t.Fatalf("metrics %v", got)
	}
}

// Plans: one task over the supported source outside data directories; a
// snapshot without supported language source plans nothing (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백);
// a path scope keeps its roots.
func TestPlan(t *testing.T) {
	fx := anypacktest.New(t, "", fixtureFiles(anypacktest.Placeholder, false), domain.Scope{})
	tasks, err := Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != Name || tasks[0].Spec.Required || tasks[0].Spec.Stage != domain.StageStaticAnalysis || *tasks[0].FilesInScope != 3 || !slices.Equal(tasks[0].Patterns, []string{"."}) {
		t.Fatalf("plan %+v %v", tasks, err)
	}
	docs := anypacktest.New(t, "", anypacktest.Text(map[string]string{"README.md": "# x\n", "Makefile": "all:\n"}), domain.Scope{})
	if tasks, _ := Plan(context.Background(), docs.Job); len(tasks) != 0 {
		t.Fatalf("docs-only plan %+v", tasks)
	}
	scoped := anypacktest.New(t, "", fixtureFiles(anypacktest.Placeholder, false), domain.Scope{Kind: "paths", Paths: []string{"src/route", "src"}})
	tasks, _ = Plan(context.Background(), scoped.Job)
	if len(tasks) != 1 || *tasks[0].FilesInScope != 2 || !slices.Equal(tasks[0].Patterns, []string{"src"}) {
		t.Fatalf("scoped plan %+v", tasks)
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{"1.26.1\n": "1.26.1", "1.26.1": "1.26.1", "lizard 1.26.1\n": "", "1.26\n": "", "1.26.1\n1.26.1\n": "", "": ""} {
		if got, ok := ParseVersion([]byte(in)); got != want || ok != (want != "") {
			t.Fatalf("ParseVersion(%q) = %q %v", in, got, ok)
		}
	}
}

// The parser is strict about the cppncss shape and splits names against
// the measured files (longest match).
func TestParseXMLShape(t *testing.T) {
	good := anypacktest.LoadCapture(t, "metrics").Stdout
	if p := parseXML(good); p.DecodeError != "" || p.ShapeErrors != 0 || len(p.Functions) != 4 || len(p.Files) != 3 {
		t.Fatalf("parse %+v", p)
	}
	for name, b := range map[string]string{
		"labels":   strings.Replace(string(good), "<label>CCN</label>", "<label>CCX</label>", 1),
		"value":    strings.Replace(string(good), "<value>17</value>", "<value>x</value>", 1),
		"element":  strings.Replace(string(good), "<measure type=\"File\">", "<measure type=\"File\"><extra/>", 1),
		"measures": strings.Replace(string(good), "type=\"File\"", "type=\"Files\"", 1),
	} {
		if p := parseXML([]byte(b)); p.DecodeError == "" && p.ShapeErrors == 0 {
			t.Fatalf("%s accepted", name)
		}
	}
	files := map[string]bool{"a at b.py": true, "b.py": true}
	if fn, file, line, ok := functionAt("f(...) at a at b.py:7", files); !ok || fn != "f(...)" || file != "a at b.py" || line != 7 {
		t.Fatalf("functionAt %q %q %d %v", fn, file, line, ok)
	}
}
