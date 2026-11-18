package jscpd

import (
	"context"
	"encoding/json"
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
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without node or jscpd: the executions return the
// streams captured from jscpd 5.4.3 on node 24 in testdata/ (real_test.go)
// for the fixtures of helpers_test.go.

const capturedVersion = "jscpd 5.4.3\n"

type replayCase struct {
	version string
	run     anypacktest.Recorded
	mutate  func(spec executor.Spec, r *executor.Result)
	files   map[string][]byte
}

type replayed struct {
	res    analysis.TaskResult
	doc    obsdoc.Document
	r      *anypacktest.Replay
	fx     anypacktest.Fixture
	script string
	node   pack.Toolchain
}

func replay(t *testing.T, c replayCase) replayed {
	t.Helper()
	node := anypacktest.FakeExecutable(t, "node")
	script := anypacktest.FakeExecutable(t, "run-jscpd.js")
	tc := pack.Toolchain{Name: "node", Executable: node, Dir: filepath.Dir(node), Version: "24.18.0"}
	a, err := New(Config{Executable: script, Node: tc})
	if err != nil {
		t.Fatal(err)
	}
	if c.files == nil {
		c.files = fixtureFiles(anypacktest.Placeholder)
	}
	if c.version == "" {
		c.version = capturedVersion
	}
	fx := anypacktest.New(t, "", c.files, domain.Scope{})
	r := &anypacktest.Replay{Fake: node, Mutate: c.mutate, Answer: func(spec executor.Spec) anypacktest.Recorded {
		if slices.Equal(spec.Args, []string{script, "--version"}) {
			return anypacktest.Recorded{Stdout: []byte(c.version)}
		}
		return c.run
	}}
	res := anypacktest.RunTask(t, a, fx.Job, Name, r)
	doc, _ := anypacktest.ReadEnvelope(t, fx.Job, res)
	return replayed{res: res, doc: doc, r: r, fx: fx, script: script, node: tc}
}

func isRun(s executor.Spec) bool { return len(s.Args) == 3 && s.Args[1] == "--config" }

// TC-117 on captured bytes: one duplicated block is one metric observation
// with the other place as related location; jscpd is started as
// <node> <run-jscpd.js> --config <scratch file> in the workspace with PATH
// set to the node directory only, and the CODEYE configuration names
// exactly the files handed over.
func TestReplayClones(t *testing.T) {
	p := replay(t, replayCase{run: anypacktest.LoadCapture(t, "clones")})
	if p.res.Status != domain.TaskSucceeded || p.doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || p.doc.Active() != 0 {
		t.Fatalf("%s/%s %+v", p.res.Status, p.res.Reason, p.doc.Outcome)
	}
	if got := clonesOf(t, p.doc); !slices.Equal(got, []string{"lib/b.js:1-17~src/a.js:1-17=16"}) {
		t.Fatalf("clones %v", got)
	}
	o := p.doc.Observations[0]
	if o.RawMessage != "Found 16 lines (1-17) duplicated on file src/a.js (1-17)" || strings.Contains(o.RawMessage, analysis.RedactedMarker) {
		t.Fatalf("message %q", o.RawMessage)
	}
	if len(p.r.Specs) != 2 || !isRun(p.r.Specs[1]) {
		t.Fatalf("specs %+v", p.r.Specs)
	}
	run := p.r.Specs[1]
	cfgPath := filepath.Join(p.fx.Job.Scratch, "analyzers", Name, "jscpd.json")
	if run.Executable != p.node.Executable || !slices.Equal(run.Args, append([]string{p.script}, RunArgs(cfgPath)...)) || run.Env["PATH"] != p.node.Dir || run.Env["JSCPD_NO_TIPS"] != "1" || run.Dir != p.fx.Job.Snapshot.WorkspaceDir {
		t.Fatalf("run spec %+v", run)
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	var rel []string
	for _, f := range cfg.Path {
		r, err := filepath.Rel(p.fx.Job.Snapshot.WorkspaceDir, f)
		if err != nil || !filepath.IsAbs(f) {
			t.Fatalf("config path %q", f)
		}
		rel = append(rel, filepath.ToSlash(r))
	}
	if !slices.Equal(rel, []string{"lib/b.js", "src/a.js", "src/ignored.js", "src/types/a.d.ts", "tools/small.py"}) || cfg.MinLines != MinLines || cfg.MinTokens != MinTokens ||
		!slices.Equal(cfg.Reporters, []string{"xcode"}) || !strings.HasPrefix(cfg.Output, p.fx.Job.Scratch) || !cfg.NoGitignore {
		t.Fatalf("config %+v (%v)", cfg, rel)
	}
	if p.doc.Coverage.Units[UnitCompared] != 5 || p.doc.Coverage.Units[SkipTooLarge] != 1 || p.doc.Coverage.Units[SkipNotUTF8] != 1 || *p.doc.Coverage.FilesInScope != 7 ||
		p.doc.Coverage.Units[anypack.UnitExcludedDataDir] != 1 || !slices.Contains(p.doc.Limitations, anypack.LimDataDirsExcluded) {
		t.Fatalf("coverage %+v", p.doc.Coverage)
	}
}

func TestReplayNoClones(t *testing.T) {
	p := replay(t, replayCase{run: anypacktest.LoadCapture(t, "noclones"), files: noCloneFiles()})
	if p.res.Status != domain.TaskSucceeded || len(p.doc.Observations) != 0 || p.doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings {
		t.Fatalf("%s/%s %+v", p.res.Status, p.res.Reason, p.doc.Outcome)
	}
}

// Every failure is a failure, never "no duplication".
func TestReplayFailuresAreNotSuccess(t *testing.T) {
	good := anypacktest.LoadCapture(t, "clones")
	edit := func(old, new string) anypacktest.Recorded {
		r := good
		r.Stdout = []byte(strings.Replace(string(good.Stdout), old, new, 1))
		return r
	}
	onRun := func(f func(r *executor.Result)) func(executor.Spec, *executor.Result) {
		return func(s executor.Spec, r *executor.Result) {
			if isRun(s) {
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
		{"exit 1", replayCase{run: anypacktest.Recorded{Stdout: good.Stdout, Stderr: good.Stderr, Exit: 1}}, domain.TaskFailed, ReasonFailed},
		{"unknown stderr", replayCase{run: anypacktest.Recorded{Stdout: good.Stdout, Stderr: []byte("Error: ENOENT: no such file\n")}}, domain.TaskFailed, ReasonFailed},
		{"summary mismatch", replayCase{run: edit("Found 1 clones.", "Found 2 clones.")}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"no summary", replayCase{run: edit("Found 1 clones.\n", "")}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"unlisted path", replayCase{run: edit("[REDACTED]/src/a.js", "[REDACTED]/src/zzz.js")}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"unmasked path", replayCase{run: edit("[REDACTED]/lib/b.js", "/elsewhere/lib/b.js")}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"under threshold", replayCase{run: edit("Found 16 lines", "Found 3 lines")}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"unknown line", replayCase{run: edit("time:", "tips: try jscpd pro\ntime:")}, domain.TaskFailed, anypack.ReasonOutputUnparsed},
		{"version", replayCase{version: "5.4.3\n", run: good}, domain.TaskFailed, anypack.ReasonToolVersion},
		{"truncated", replayCase{run: good, mutate: onRun(func(r *executor.Result) { r.Stdout.Truncated = true })}, domain.TaskFailed, anypack.ReasonOutputTruncated},
		{"timeout", replayCase{run: good, mutate: onRun(func(r *executor.Result) { r.Status, r.ExitCode = executor.StatusTimedOut, nil })}, domain.TaskTimedOut, anypack.ReasonTimeout},
		{"cancelled", replayCase{run: good, mutate: onRun(func(r *executor.Result) { r.Status, r.ExitCode = executor.StatusCancelled, nil })}, domain.TaskCancelled, anypack.ReasonCancelled},
		{"cleanup uncertain", replayCase{run: good, mutate: onRun(func(r *executor.Result) { r.Cleanup = executor.CleanupUncertain })}, domain.TaskFailed, anypack.ReasonCleanupUncertain},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := replay(t, c.c)
			if p.res.Status != c.status || p.res.Reason != c.reason || len(p.doc.Coverage.ProcessedPaths) != 0 || p.doc.Parse.OK {
				t.Fatalf("%s/%s outcome %+v coverage %+v", p.res.Status, p.res.Reason, p.doc.Outcome, p.doc.Coverage)
			}
		})
	}
}

// When every eligible file is one jscpd would skip silently, nothing is
// started and the task is unsupported (no_analyzable_files), never clean.
func TestReplayNothingToCompare(t *testing.T) {
	q := replay(t, replayCase{files: map[string][]byte{"native/latin1.c": []byte("/* caf\xe9 */\nint x;\n")}})
	if q.res.Status != domain.TaskUnsupported || q.res.Reason != anypack.ReasonNoAnalyzableFiles || len(q.r.Specs) != 0 || q.doc.Outcome.ExitClass != obsdoc.ExitNotRun {
		t.Fatalf("%s/%s %+v specs %d", q.res.Status, q.res.Reason, q.doc.Outcome, len(q.r.Specs))
	}
}

func TestNewRejectsShims(t *testing.T) {
	dir := t.TempDir()
	node := anypacktest.FakeExecutable(t, "node")
	tc := pack.Toolchain{Name: "node", Executable: node, Dir: filepath.Dir(node), Version: "24.18.0"}
	shim := filepath.Join(dir, "jscpd")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Executable: shim, Node: tc}); err == nil {
		t.Fatal("shell shim accepted")
	}
	if _, err := New(Config{Executable: anypacktest.FakeExecutable(t, "run-jscpd.js")}); err == nil {
		t.Fatal("no node toolchain accepted")
	}
	if _, err := New(Config{Executable: "run-jscpd.js", Node: tc}); err == nil {
		t.Fatal("relative script accepted")
	}
}

func TestPlanAndVersion(t *testing.T) {
	fx := anypacktest.New(t, "", fixtureFiles(anypacktest.Placeholder), domain.Scope{})
	tasks, err := Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != Name || tasks[0].Spec.Required || *tasks[0].FilesInScope != 7 {
		t.Fatalf("plan %+v %v", tasks, err)
	}
	for in, want := range map[string]string{"jscpd 5.4.3\n": "5.4.3", "5.4.3\n": "", "jscpd v5.4.3\n": "", "jscpd 5.4.3\nx\n": ""} {
		if got, ok := ParseVersion([]byte(in)); got != want || ok != (want != "") {
			t.Fatalf("ParseVersion(%q) = %q %v", in, got, ok)
		}
	}
}
