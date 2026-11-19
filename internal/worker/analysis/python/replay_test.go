package python

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without the Python tools: the tool executions
// return the streams captured from ruff 0.16.3, mypy 2.4.1, pyright 1.1.414
// and bandit 1.9.4 on the fixtures/python projects (testdata/<tool>/).
// The captures are taken by the real-tool tests with CODEYE_PYTHON_CAPTURE set;
// the module directory is stored as @MODULE@ and the home directory as
// @HOME@. Changing a fixture requires a new capture.

const (
	moduleMarker = "@MODULE@"
	homeMarker   = "@HOME@"
)

// execNameOf names a tool execution from its arguments (for pyright, the
// arguments after the entry script).
func execNameOf(tool string, args []string) string {
	switch {
	case slices.Contains(args, "--version"):
		return tool + ".version"
	case tool == RuffTool && slices.Contains(args, "--show-files"):
		return "ruff.files"
	case tool == RuffTool && slices.Contains(args, "--ignore-noqa"):
		return "ruff.check-ignore-noqa"
	case tool == BanditTool && slices.Contains(args, "--ignore-nosec"):
		return "bandit.check-ignore-nosec"
	}
	if tool == MypyTool {
		// A rerun without unparsable files reads files.rNN.txt.
		for _, a := range args {
			if base := filepath.Base(a); strings.HasPrefix(a, "@") && strings.HasPrefix(base, "files.r") {
				return "mypy.check." + strings.TrimSuffix(strings.TrimPrefix(base, "files."), ".txt")
			}
		}
	}
	return tool + ".check"
}

func toolArgs(tool string, args []string) []string {
	if tool == PyrightTool && len(args) > 0 {
		return args[1:]
	}
	return args
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// capture writes the streams of a real root-module run under
// $CODEYE_PYTHON_CAPTURE/<tool>/<fixture>.<execution>.*.
func capture(t *testing.T, fixtureName, key string, r realRun) {
	t.Helper()
	dir := os.Getenv("CODEYE_PYTHON_CAPTURE")
	if dir == "" || strings.Contains(key, ".") {
		return
	}
	tool := key
	home := os.Getenv("HOME")
	for i, spec := range r.rr.specs {
		args := toolArgs(tool, spec.Args)
		name := execNameOf(tool, args)
		res := r.rr.results[i]
		sub := func(b []byte) []byte {
			for _, d := range []string{spec.Dir, evalOr(spec.Dir)} {
				b = bytes.ReplaceAll(b, []byte(d), []byte(moduleMarker))
			}
			if home != "" {
				b = bytes.ReplaceAll(b, []byte(home), []byte(homeMarker))
			}
			return b
		}
		base := filepath.Join(dir, tool, fixtureName+"."+name)
		if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
			t.Fatal(err)
		}
		code := -1
		if res.ExitCode != nil {
			code = *res.ExitCode
		}
		write := func(suffix string, b []byte) {
			if err := os.WriteFile(base+suffix, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write(".stdout", sub(res.Stdout.Data))
		write(".stderr", sub(res.Stderr.Data))
		write(".exit", []byte(strconv.Itoa(code)+"\n"))
		if tool == MypyTool && strings.HasPrefix(name, "mypy.check") {
			if b, err := os.ReadFile(filepath.Join(argAfter(args, "--lineprecision-report"), "lineprecision.txt")); err == nil {
				write(".lineprecision", b)
			}
		}
		if tool == BanditTool && name == "bandit.check" {
			if b, err := os.ReadFile(argAfter(args, "-o")); err == nil {
				write(".report", sub(b))
			}
		}
	}
}

func evalOr(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// replayRunner answers the fake tool's executions from a capture and
// rejects anything else (the pack runs no other process).
type replayRunner struct {
	t        *testing.T
	tool     string
	fake     string
	fixture  string
	mutate   func(name string, spec executor.Spec, r *executor.Result)
	override map[string]string // execution name -> capture fixture
}

func (rr *replayRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	if spec.Executable != rr.fake {
		rr.t.Errorf("unexpected process %s", spec.Executable)
		return executor.Result{Status: executor.StatusRejected, Cleanup: executor.CleanupNotStarted}
	}
	args := toolArgs(rr.tool, spec.Args)
	name := execNameOf(rr.tool, args)
	fixture := rr.fixture
	if f, ok := rr.override[name]; ok {
		fixture = f
	}
	base := filepath.Join("testdata", rr.tool, fixture+"."+name)
	read := func(suffix string) []byte {
		b, err := os.ReadFile(base + suffix)
		if err != nil {
			rr.t.Fatalf("capture %s%s: %v", base, suffix, err)
		}
		b = bytes.ReplaceAll(b, []byte(moduleMarker), []byte(spec.Dir))
		return bytes.ReplaceAll(b, []byte(homeMarker), []byte("/home/user"))
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(read(".exit"))))
	if err != nil {
		rr.t.Fatal(err)
	}
	if rr.tool == BanditTool && name == "bandit.check" {
		if rep, err := os.ReadFile(base + ".report"); err == nil {
			if err := os.WriteFile(argAfter(args, "-o"), rep, 0o600); err != nil {
				rr.t.Fatal(err)
			}
		}
	}
	if rr.tool == MypyTool && strings.HasPrefix(name, "mypy.check") {
		if lp, err := os.ReadFile(base + ".lineprecision"); err == nil {
			if err := os.WriteFile(filepath.Join(argAfter(args, "--lineprecision-report"), "lineprecision.txt"), lp, 0o600); err != nil {
				rr.t.Fatal(err)
			}
		}
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out, errb := read(".stdout"), read(".stderr")
	// The executor masks the Redact values in the recorded argv (and in the
	// streams, which the captures already are).
	secrets := append([]string{}, spec.Redact...)
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	argv := []string{spec.Executable}
	for _, a := range spec.Args {
		for _, s := range secrets {
			if s != "" {
				a = strings.ReplaceAll(a, s, analysis.RedactedMarker)
			}
		}
		argv = append(argv, a)
	}
	res := executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone, ExitCode: &code,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: argv, Dir: analysis.RedactedMarker, EnvKeys: keys},
		Stdout:  executor.Stream{Data: out, TotalBytes: int64(len(out))}, Stderr: executor.Stream{Data: errb, TotalBytes: int64(len(errb))}}
	if rr.mutate != nil {
		rr.mutate(name, spec, &res)
		// A mutated stream keeps the executor invariant total >= captured.
		for _, st := range []*executor.Stream{&res.Stdout, &res.Stderr} {
			if !st.Truncated {
				st.TotalBytes = int64(len(st.Data))
			}
		}
	}
	return res
}

// replayAnalyzer configures tool with fake files that are never executed.
func replayAnalyzer(t *testing.T, tool string) (planner, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var (
		a   planner
		err error
	)
	fake := filepath.Join(dir, tool)
	switch tool {
	case PyrightTool:
		fake = filepath.Join(dir, "node")
		write(fake, "not executed: replayed\n")
		js := filepath.Join(dir, "pyright", "index.js")
		write(js, "// not executed: replayed\n")
		a, err = NewPyright(Config{Executable: js, Node: &pack.Toolchain{Name: "node", Executable: fake, Dir: dir}})
	case RuffTool:
		write(fake, "not executed: replayed\n")
		a, err = NewRuff(Config{Executable: fake})
	case MypyTool:
		write(fake, "#!/bin/sh\nexit 3\n")
		a, err = NewMypy(Config{Executable: fake})
	case BanditTool:
		write(fake, "#!/bin/sh\nexit 3\n")
		a, err = NewBandit(Config{Executable: fake})
	}
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func runReplay(t *testing.T, tool, fixtureName string, mutate func(string, executor.Spec, *executor.Result)) realRun {
	t.Helper()
	a, fake := replayAnalyzer(t, tool)
	return runWith(t, a, fixtureName, tool, &replayRunner{t: t, tool: tool, fake: fake, fixture: fixtureName, mutate: mutate}, filepath.Dir(fake))
}

// ---- captured outcomes (the same checks as the real-tool tests) ----

func TestReplayRuffFindings(t *testing.T) {
	checkRuffFindings(t, runReplay(t, RuffTool, "findings", nil))
}
func TestReplayRuffClean(t *testing.T) { checkRuffClean(t, runReplay(t, RuffTool, "clean", nil)) }
func TestReplayRuffSuppressions(t *testing.T) {
	checkRuffSuppressionsReported(t, runReplay(t, RuffTool, "suppressions", nil))
}
func TestReplayRuffSyntaxError(t *testing.T) {
	checkRuffSyntaxError(t, runReplay(t, RuffTool, "syntax", nil))
}
func TestReplayRuffOverlap(t *testing.T) { checkRuffOverlap(t, runReplay(t, RuffTool, "overlap", nil)) }
func TestReplayMypyFindings(t *testing.T) {
	checkMypyFindings(t, runReplay(t, MypyTool, "findings", nil))
}
func TestReplayMypyImports(t *testing.T) {
	checkMypyImportsAreCoverageGaps(t, runReplay(t, MypyTool, "imports", nil))
}
func TestReplayMypySuppressions(t *testing.T) {
	checkMypySuppressionsHonored(t, runReplay(t, MypyTool, "suppressions", nil))
}
func TestReplayMypySyntaxError(t *testing.T) {
	checkMypySyntaxError(t, runReplay(t, MypyTool, "syntax", nil))
}
func TestReplayMypyOverlap(t *testing.T) {
	checkMypyOverlapDisabled(t, runReplay(t, MypyTool, "overlap", nil))
}
func TestReplayBanditFindings(t *testing.T) {
	checkBanditFindings(t, runReplay(t, BanditTool, "findings", nil))
}
func TestReplayBanditSuppressions(t *testing.T) {
	checkBanditSuppressionsReported(t, runReplay(t, BanditTool, "suppressions", nil))
}
func TestReplayBanditSyntaxError(t *testing.T) {
	checkBanditSyntaxError(t, runReplay(t, BanditTool, "syntax", nil))
}

func replayPyright(t *testing.T, fixtureName string, check func(*testing.T, *Pyright, realRun)) {
	t.Helper()
	a, fake := replayAnalyzer(t, PyrightTool)
	r := runWith(t, a, fixtureName, PyrightTool, &replayRunner{t: t, tool: PyrightTool, fake: fake, fixture: fixtureName}, filepath.Dir(fake))
	check(t, a.(*Pyright), r)
}

func TestReplayPyrightFindings(t *testing.T) { replayPyright(t, "findings", checkPyrightFindings) }
func TestReplayPyrightImports(t *testing.T) {
	replayPyright(t, "imports", checkPyrightImportsAreCoverageGaps)
}
func TestReplayPyrightSuppressions(t *testing.T) {
	replayPyright(t, "suppressions", checkPyrightSuppressionsHonored)
}
func TestReplayPyrightSyntaxError(t *testing.T) { replayPyright(t, "syntax", checkPyrightSyntaxError) }
func TestReplayPyrightUnparsableFiles(t *testing.T) {
	replayPyright(t, "syntaxmix", checkPyrightUnparsable)
}
func TestReplayRuffUnparsableFiles(t *testing.T) {
	checkRuffUnparsable(t, runReplay(t, RuffTool, "syntaxmix", nil))
}
func TestReplayMypyUnparsableFiles(t *testing.T) {
	checkMypyUnparsable(t, runReplay(t, MypyTool, "syntaxmix", nil))
}

// mypy's reruns without the unparsable files share the task's one time
// limit: a rerun gets only what is left, and when nothing is left the task
// times out with the syntax errors found so far (TC-201).
func TestReplayMypyRerunsShareTimeLimit(t *testing.T) {
	const limit = 2 * time.Second
	dir := t.TempDir()
	fake := filepath.Join(dir, MypyTool)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		delay time.Duration
	}{{"time left", limit / 4}, {"time spent", limit + limit/4}} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewMypy(Config{Executable: fake, Timeout: limit})
			if err != nil {
				t.Fatal(err)
			}
			slow := func(name string, _ executor.Spec, _ *executor.Result) {
				if name == "mypy.check" {
					time.Sleep(tc.delay)
				}
			}
			r := runWith(t, a, "syntaxmix", MypyTool, &replayRunner{t: t, tool: MypyTool, fake: fake, fixture: "syntaxmix", mutate: slow}, dir)
			var reruns []time.Duration
			for _, s := range r.rr.specs {
				if s.Timeout > limit {
					t.Fatalf("execution timeout %s over the limit %s", s.Timeout, limit)
				}
				if slices.ContainsFunc(s.Args, func(a string) bool { return strings.Contains(a, "files.r") }) {
					reruns = append(reruns, s.Timeout)
				}
			}
			if tc.delay < limit {
				checkMypyUnparsable(t, r)
				if len(reruns) != 2 || reruns[0] > limit-tc.delay {
					t.Fatalf("rerun timeouts %v: want at most %s", reruns, limit-tc.delay)
				}
				return
			}
			wantStatus(t, r, domain.TaskTimedOut, obsdoc.ExitTimeout)
			if len(reruns) != 0 || r.res.Reason != ReasonTimeout || !slices.Contains(r.doc.Limitations, LimMypyRerunsTimedOut) {
				t.Fatalf("reruns %v reason %s limitations %v", reruns, r.res.Reason, r.doc.Limitations)
			}
			wantExecutions(t, r.doc, "mypy.version", "mypy.check")
			if got := findObs(r.doc, "syntax"); len(got) != 1 || len(r.doc.Coverage.ProcessedPaths) != 0 {
				t.Fatalf("syntax observations %+v processed %v", got, r.doc.Coverage.ProcessedPaths)
			}
		})
	}
}

func TestReplayBanditUnparsableFiles(t *testing.T) {
	checkBanditUnparsable(t, runReplay(t, BanditTool, "syntaxmix", nil))
}
func TestReplayPyrightOverlap(t *testing.T) { replayPyright(t, "overlap", checkPyrightOverlapDisabled) }

// ---- failure modes: never success ----

func checkName(tool string) string {
	if tool == BanditTool {
		return "bandit.check-ignore-nosec"
	}
	if tool == RuffTool {
		return "ruff.check-ignore-noqa"
	}
	return tool + ".check"
}

// Every tool: a timeout, a cancellation, uncertain cleanup, truncated or
// unparseable output and an exit status that contradicts the output each
// fail the task and claim no processed file (TC-108).
func TestReplayFailuresAreNeverSuccess(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*executor.Result)
		status domain.TaskStatus
		reason string
	}{
		{"timeout", func(r *executor.Result) { r.Status, r.ExitCode = executor.StatusTimedOut, nil }, domain.TaskTimedOut, ReasonTimeout},
		{"cancelled", func(r *executor.Result) { r.Status, r.ExitCode = executor.StatusCancelled, nil }, domain.TaskCancelled, ReasonCancelled},
		{"cleanup uncertain", func(r *executor.Result) { r.Cleanup = executor.CleanupUncertain }, domain.TaskFailed, ReasonCleanupUncertain},
		{"truncated", func(r *executor.Result) { r.Stdout.Truncated = true }, domain.TaskFailed, ReasonOutputTruncated},
		{"unparsed", func(r *executor.Result) {
			r.Stdout.Data = append(append([]byte{}, r.Stdout.Data...), "\n<<garbage>>"...)
		}, domain.TaskFailed, ReasonOutputUnparsed},
		{"exit mismatch", func(r *executor.Result) { zero := 0; r.ExitCode = &zero }, domain.TaskFailed, ReasonExitMismatch},
	}
	for _, tool := range []string{RuffTool, MypyTool, PyrightTool, BanditTool} {
		for _, c := range cases {
			t.Run(tool+"/"+c.name, func(t *testing.T) {
				r := runReplay(t, tool, "findings", func(name string, _ executor.Spec, res *executor.Result) {
					if name == checkName(tool) {
						c.mutate(res)
					}
				})
				if r.res.Status != c.status || r.res.Reason != c.reason || r.doc.Coverage.ProcessedPaths != nil || r.doc.Parse.OK {
					t.Fatalf("%s/%s coverage %+v parse %+v", r.res.Status, r.res.Reason, r.doc.Coverage, r.doc.Parse)
				}
			})
		}
	}
}

// An unrecognized version never runs the tool on the code; an untested one
// runs and is marked.
func TestReplayToolVersion(t *testing.T) {
	for _, tool := range []string{RuffTool, MypyTool, PyrightTool, BanditTool} {
		r := runReplay(t, tool, "findings", func(name string, _ executor.Spec, res *executor.Result) {
			if name == tool+".version" {
				res.Stdout.Data = []byte("something else 1\n")
			}
		})
		if r.res.Status != domain.TaskFailed || r.res.Reason != ReasonToolVersion || len(r.doc.Executions) != 1 {
			t.Fatalf("%s unrecognized: %s/%s %d executions", tool, r.res.Status, r.res.Reason, len(r.doc.Executions))
		}
	}
	untested := map[string]string{RuffTool: "ruff 0.99.0\n", MypyTool: "mypy 9.0.0 (compiled: yes)\n", PyrightTool: "pyright 1.1.999\n", BanditTool: "bandit 9.0.0\n"}
	for tool, v := range untested {
		r := runReplay(t, tool, "findings", func(name string, _ executor.Spec, res *executor.Result) {
			if name == tool+".version" {
				res.Stdout.Data = []byte(v)
			}
		})
		if r.res.Status != domain.TaskSucceeded || r.doc.Tool.VersionTested || !hasLimit(r.doc, LimToolVersionUntested) {
			t.Fatalf("%s untested: %s/%s %+v %v", tool, r.res.Status, r.res.Reason, r.doc.Tool, r.doc.Limitations)
		}
	}
}

// The two-run suppression diff is checked: an active finding missing from
// the all-diagnostics run is an inconsistency, never a guess.
func TestReplaySuppressionDiffInconsistent(t *testing.T) {
	r := runReplay(t, RuffTool, "findings", func(name string, _ executor.Spec, res *executor.Result) {
		if name == "ruff.check-ignore-noqa" {
			_, rest, _ := bytes.Cut(res.Stdout.Data, []byte("\n"))
			res.Stdout.Data = rest
		}
	})
	if r.res.Status != domain.TaskFailed || r.res.Reason != ReasonSuppressionDiff || r.doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("ruff inconsistent diff: %s/%s", r.res.Status, r.res.Reason)
	}
	a, fake := replayAnalyzer(t, BanditTool)
	rr := &replayRunner{t: t, tool: BanditTool, fake: fake, fixture: "findings", override: map[string]string{"bandit.check-ignore-nosec": "suppressions"}}
	r = runWith(t, a, "findings", BanditTool, rr, filepath.Dir(fake))
	if r.res.Status != domain.TaskFailed || r.doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("bandit inconsistent diff: %s/%s", r.res.Status, r.res.Reason)
	}
}

// Processed files are verified against the tool's own record: a planned
// file the tool did not list is not claimed and is counted not analyzed.
func TestReplayProcessedFilesVerified(t *testing.T) {
	r := runReplay(t, RuffTool, "findings", func(name string, spec executor.Spec, res *executor.Result) {
		if name == "ruff.files" {
			var keep []string
			for _, l := range strings.Split(string(res.Stdout.Data), "\n") {
				if !strings.HasSuffix(l, "stubs.pyi") {
					keep = append(keep, l)
				}
			}
			res.Stdout.Data = []byte(strings.Join(keep, "\n"))
		}
	})
	if r.res.Status != domain.TaskSucceeded || slices.Contains(r.doc.Coverage.ProcessedPaths, "app/stubs.pyi") || r.doc.Coverage.FilesUnlisted != 1 ||
		r.doc.Coverage.Units[UnitNotAnalyzed] != 1 || !hasLimit(r.doc, LimFilesNotProcessed) {
		t.Fatalf("ruff unlisted %s/%s %+v %v", r.res.Status, r.res.Reason, r.doc.Coverage, r.doc.Limitations)
	}
	r = runReplay(t, PyrightTool, "findings", func(name string, _ executor.Spec, res *executor.Result) {
		if name == "pyright.check" {
			res.Stdout.Data = bytes.Replace(res.Stdout.Data, []byte(`"filesAnalyzed": 3`), []byte(`"filesAnalyzed": 2`), 1)
		}
	})
	if r.res.Status != domain.TaskFailed || r.doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("pyright filesAnalyzed mismatch %s/%s", r.res.Status, r.res.Reason)
	}
	r = runReplay(t, MypyTool, "findings", func(name string, spec executor.Spec, res *executor.Result) {
		if name == "mypy.check" {
			_ = os.Remove(filepath.Join(argAfter(spec.Args, "--lineprecision-report"), "lineprecision.txt"))
		}
	})
	if r.res.Status != domain.TaskFailed || r.res.Reason != ReasonProcessedMismatch {
		t.Fatalf("mypy without line precision %s/%s", r.res.Status, r.res.Reason)
	}
}

// A tool that writes into the workspace inputs makes the run fail with
// workspace_drift; the snapshot is never written.
func TestReplayWorkspaceDrift(t *testing.T) {
	r := runReplay(t, RuffTool, "findings", func(name string, spec executor.Spec, _ *executor.Result) {
		if name == "ruff.check" {
			if err := os.WriteFile(filepath.Join(spec.Dir, "app", "core.py"), []byte("changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	if r.res.Status != domain.TaskFailed || r.res.Reason != ReasonWorkspaceDrift || !slices.Equal(r.doc.WorkspaceDrift, []string{"app/core.py"}) {
		t.Fatalf("drift %s/%s %v", r.res.Status, r.res.Reason, r.doc.WorkspaceDrift)
	}
}

// A cancelled job runs nothing and reports cancelled.
func TestReplayCancelledBeforeStart(t *testing.T) {
	a, fake := replayAnalyzer(t, RuffTool)
	fx := newFixture(t, loadFixture(t, "findings"), domain.Scope{})
	tasks, err := a.Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("plan %v %+v", err, tasks)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rr := &recordingRunner{inner: &replayRunner{t: t, tool: RuffTool, fake: fake, fixture: "findings"}}
	res := a.Run(ctx, fx.Job, tasks[0].Spec, rr)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskCancelled || len(rr.specs) != 0 || doc.Outcome.ExitClass != obsdoc.ExitCancelled {
		t.Fatalf("cancelled %s/%s %d specs %+v", res.Status, res.Reason, len(rr.specs), doc.Outcome)
	}
}
