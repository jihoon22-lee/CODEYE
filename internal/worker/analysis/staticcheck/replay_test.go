package staticcheck

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without staticcheck: the go executions are real
// (the toolchain that built the test), the staticcheck executions return the
// redacted streams captured from staticcheck 2026.2.2 in testdata/ (see
// real_test.go capture). The fixture sources in helpers_test.go are the ones
// the captures were taken from; changing them requires a new capture.

const capturedVersion = "staticcheck 2026.2.2 (0.8.2)\n"

type recorded struct {
	stdout, stderr []byte
	exit           int
}

func loadCapture(t *testing.T, name string) recorded {
	t.Helper()
	read := func(suffix string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name+suffix))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(read(".exit"))))
	if err != nil {
		t.Fatal(err)
	}
	return recorded{stdout: read(".stdout"), stderr: read(".stderr"), exit: code}
}

// replayRunner runs go for real and answers the fake staticcheck from a
// capture. It records every staticcheck spec so tests can check argv, cwd,
// environment and timeout.
type replayRunner struct {
	real    executor.Runner
	fake    string
	version string
	check   recorded
	mutate  func(r *executor.Result)
	specs   []executor.Spec
	all     []executor.Spec
}

func (r *replayRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	r.all = append(r.all, spec)
	if spec.Executable != r.fake {
		return r.real.Run(ctx, spec)
	}
	r.specs = append(r.specs, spec)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: append([]string{spec.Executable}, spec.Args...), Dir: analysis.RedactedMarker, EnvKeys: keys}}
	out, errb, code := []byte(r.version), []byte(nil), 0
	isCheck := !(len(spec.Args) == 1 && spec.Args[0] == "-version")
	if isCheck {
		out, errb, code = r.check.stdout, r.check.stderr, r.check.exit
	}
	res.ExitCode = &code
	res.Stdout = executor.Stream{Data: append([]byte{}, out...), TotalBytes: int64(len(out))}
	res.Stderr = executor.Stream{Data: append([]byte{}, errb...), TotalBytes: int64(len(errb))}
	if isCheck && r.mutate != nil {
		r.mutate(&res)
	}
	return res
}

func replayAnalyzer(t *testing.T) (*Analyzer, string) {
	t.Helper()
	fake := filepath.Join(t.TempDir(), "staticcheck")
	if err := os.WriteFile(fake, []byte("not executed: replayed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goh, err := govet.New(govet.Config{GoExecutable: packtest.GoExecutable(t)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Executable: fake, Go: goh})
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func replay(t *testing.T, files map[string]string, capture string, mutate func(*executor.Result)) (analysis.TaskResult, obsdoc.Document, *replayRunner, fixture) {
	t.Helper()
	a, fake := replayAnalyzer(t)
	fx := newFixture(t, "", files, domain.Scope{})
	r := &replayRunner{real: &executor.Executor{}, fake: fake, version: capturedVersion, mutate: mutate}
	if capture != "" {
		r.check = loadCapture(t, capture)
	}
	res := packtest.RunTask(t, a, fx.Job, "staticcheck", r)
	doc, _ := readEnvelope(t, fx.Job, res)
	return res, doc, r, fx
}

// TC-07 on captured bytes: exit 1 with only check records and a clean
// stderr is success_with_findings; the staticcheck process is started with
// argv, the attempt workspace as cwd, an explicit offline environment and a
// timeout.
func TestReplayFindingsExitOne(t *testing.T) {
	res, doc, r, fx := replay(t, findingFiles(), "findings", nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.Outcome.PartialParse || !doc.Parse.OK {
		t.Fatalf("status %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	want := map[string]int{"SA1000": 1, "SA1012": 1, "SA4006": 2, "SA4023": 1, "U1000": 1, "SA2001": 1}
	if got := ruleCount(doc); len(got) != len(want) {
		t.Fatalf("rules %v", got)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("rules %v, want %v", got, want)
			}
		}
	}
	cats := map[string]string{}
	related := 0
	for _, o := range doc.Observations {
		cats[o.RuleID] = o.Category
		if o.LocationState != obsdoc.LocationResolved || o.Location.ColumnUnit != "byte" || o.RawRef.Execution != execCheck {
			t.Fatalf("observation %+v", o)
		}
		for _, rel := range o.Related {
			if rel.LocationState != obsdoc.LocationResolved || rel.Location.Path != "find/find.go" || rel.Location.StartLine != 27 {
				t.Fatalf("related %+v", rel)
			}
			related++
		}
	}
	if related != 1 || cats["SA2001"] != "concurrency" || cats["U1000"] != "maintainability" || cats["SA1000"] != "correctness" {
		t.Fatalf("related %d categories %v", related, cats)
	}
	if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"clean/c.go", "find/find.go", "find/find_test.go"}) || doc.Coverage.Units[PackageAnalyzedWithFindings] != 1 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	if len(r.specs) != 2 {
		t.Fatalf("staticcheck executions %d", len(r.specs))
	}
	check := r.specs[1]
	if !slices.Equal(check.Args, CheckArgs([]string{"./..."})) || check.Timeout != DefaultTimeout || check.Dir != fx.Job.Snapshot.WorkspaceDir || check.WorkspaceRoot != fx.Job.Snapshot.WorkspaceDir {
		t.Fatalf("check spec %+v", check)
	}
	for k, v := range map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOENV": "off", "GOWORK": "off", "GOFLAGS": "", "CGO_ENABLED": "0", "GOSUMDB": "off", "GOVCS": "*:off"} {
		if got, ok := check.Env[k]; !ok || got != v {
			t.Fatalf("env %s=%q (%v), want %q", k, got, ok, v)
		}
	}
	taskDir := filepath.Join(fx.Job.Scratch, "analyzers", "staticcheck")
	for _, k := range []string{"STATICCHECK_CACHE", "GOCACHE", "GOPATH", "GOMODCACHE"} {
		if !strings.HasPrefix(check.Env[k], taskDir+string(filepath.Separator)) {
			t.Fatalf("%s=%q is not in task scratch", k, check.Env[k])
		}
	}
	if _, ok := check.Env["HOME"]; ok {
		t.Fatal("HOME is passed to staticcheck")
	}
	if !slices.Contains(check.ProtectedRoots, fx.Job.Snapshot.SnapshotDir) {
		t.Fatalf("snapshot not protected: %v", check.ProtectedRoots)
	}
}

// Exit 0 with no record and no stderr is the only clean result.
func TestReplayClean(t *testing.T) {
	res, doc, _, _ := replay(t, map[string]string{"go.mod": fixGoMod, "clean/c.go": fixClean}, "clean", nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || len(doc.Observations) != 0 || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"clean/c.go"}) {
		t.Fatalf("clean %s %+v %+v", res.Status, doc.Outcome, doc.Coverage)
	}
}

// TC-104 on captured bytes (-show-ignored): problems //lint:ignore and
// //lint:file-ignore hide arrive with severity "ignored" and become
// suppressed observations; only the active one makes the class
// success_with_findings, and all suppressed with exit 1 is
// success_without_findings with the package analyzed clean.
func TestReplaySuppressionsReported(t *testing.T) {
	for _, tc := range []struct {
		capture        string
		all            bool
		active, hidden int
		class, unit    string
	}{
		{"suppressed", false, 2, 2, obsdoc.ExitSuccessWithFindings, PackageAnalyzedWithFindings},
		{"suppressed-only", true, 0, 3, obsdoc.ExitSuccessWithoutFindings, PackageAnalyzedClean},
	} {
		res, doc, _, _ := replay(t, suppressionFiles(tc.all), tc.capture, nil)
		if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != tc.class || doc.Coverage.Units[tc.unit] != 1 {
			t.Fatalf("%s: %s/%s %+v units %v", tc.capture, res.Status, res.Reason, doc.Outcome, doc.Coverage.Units)
		}
		active, hidden := 0, 0
		for _, o := range doc.Observations {
			switch {
			case o.Suppressed && *o.RawSeverity == "ignored":
				hidden++
			case !o.Suppressed && *o.RawSeverity == "error":
				active++
			default:
				t.Fatalf("%s: observation %+v", tc.capture, o)
			}
		}
		if active != tc.active || hidden != tc.hidden || doc.Active() != tc.active || doc.RulePack.SourceSuppressions != obsdoc.SuppressReported ||
			!slices.Equal(doc.RulePack.UnreportedSuppressionRules, []string{"U1000"}) {
			t.Fatalf("%s: active %d hidden %d %+v", tc.capture, active, hidden, doc.RulePack)
		}
	}
	// The same bytes without -show-ignored semantics would be a contract
	// break: a suppressed record under honored suppressions is invalid.
	_, doc, _, _ := replay(t, suppressionFiles(false), "suppressed", nil)
	doc.RulePack.SourceSuppressions, doc.RulePack.UnreportedSuppressionRules = obsdoc.SuppressHonored, []string{}
	if err := obsdoc.Validate(doc, obsdoc.Expect{}); err == nil {
		t.Fatal("suppressed observations accepted under honored suppressions")
	}
}

// TC-08: a compile record fails the task, keeps the other package's
// observations and leaves processed files unknown.
func TestReplayTypeErrorPartial(t *testing.T) {
	files := findingFiles()
	files["typeerr/t.go"] = fixType
	res, doc, _, _ := replay(t, files, "typeerr", nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonPackageErrors || doc.Outcome.ExitClass != obsdoc.ExitExecutionError || !doc.Outcome.PartialParse {
		t.Fatalf("type error %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	if len(doc.Observations) != 7 || len(doc.ToolErrors) != 1 || doc.ToolErrors[0].Kind != "typecheck" || doc.ToolErrors[0].Path == nil || *doc.ToolErrors[0].Path != "typeerr/t.go" {
		t.Fatalf("observations %d tool errors %+v", len(doc.Observations), doc.ToolErrors)
	}
	if doc.Coverage.ProcessedPaths != nil || doc.Coverage.Units[PackageFailedTypecheck] != 1 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
}

// TC-68 on captured bytes: offline dependency and toolchain gaps are
// configuration errors, not findings and not success.
func TestReplayOfflineConfigurationErrors(t *testing.T) {
	res, doc, _, _ := replay(t, map[string]string{"go.mod": "module example.test/dep\n\ngo 1.22\n\nrequire golang.org/x/text v0.3.0\n", "dep.go": fixDep}, "dependency", nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonDependency || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || !slices.Contains(doc.Limitations, "no_offline_module_cache") {
		t.Fatalf("dependency %s/%s %+v %v", res.Status, res.Reason, doc.Outcome, doc.Limitations)
	}
	res, doc, _, _ = replay(t, map[string]string{"go.mod": "module example.test/newer\n\ngo 1.40\n", "n.go": "package newer\n"}, "toolchain", nil)
	if res.Status != domain.TaskFailed || res.Reason != ReasonToolchain || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError {
		t.Fatalf("toolchain %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
}

// "matched no packages" with exit 0 is unsupported, never a clean result.
func TestReplayNothingAnalyzable(t *testing.T) {
	res, doc, _, _ := replay(t, map[string]string{"go.mod": fixGoMod, "tagged/t.go": fixTag}, "no-packages", nil)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonNoPackages || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError || len(doc.Coverage.ProcessedPaths) != 0 {
		t.Fatalf("no packages %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage)
	}
}

func TestReplayOverlapFixture(t *testing.T) {
	res, doc, _, _ := replay(t, map[string]string{"go.mod": fixGoMod, "overlap/o.go": fixOverlap, "overlap/o_test.go": fixOverlapT}, "overlap", nil)
	if res.Status != domain.TaskSucceeded || len(doc.Observations) != 1 || doc.Observations[0].RuleID != "SA4000" || doc.Observations[0].Location.StartLine != 37 {
		t.Fatalf("overlap %s %+v", res.Status, doc.Observations)
	}
}

// TC-08 exit mapping: damaged or contradictory bytes are failures that keep
// what was parsed; none reads as zero findings.
func TestReplayDamagedOutputAndExitMapping(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(r *executor.Result)
		partial      bool
	}{
		{"truncated", ReasonOutputTruncated, func(r *executor.Result) {
			r.Stdout.Data = r.Stdout.Data[:len(r.Stdout.Data)/2]
			r.Stdout.Truncated = true
		}, true},
		{"garbled", ReasonOutputUnparsed, func(r *executor.Result) {
			r.Stdout.Data = append(r.Stdout.Data, []byte("{\"code\":")...)
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}, true},
		{"unknown-field", ReasonOutputUnparsed, func(r *executor.Result) {
			r.Stdout.Data = append(r.Stdout.Data, []byte("{\"code\":\"SA9999\",\"severity\":\"error\",\"location\":{\"file\":\"\",\"line\":0,\"column\":0},\"end\":{\"file\":\"\",\"line\":0,\"column\":0},\"message\":\"m\",\"fix\":1}\n")...)
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}, true},
		{"crash-exit-2", ReasonFailed, func(r *executor.Result) {
			two := 2
			r.ExitCode = &two
			r.Stdout.Data, r.Stdout.TotalBytes = nil, 0
			r.Stderr.Data, r.Stderr.TotalBytes = []byte("panic: boom\n"), 12
		}, false},
		{"exit-1-fatal-no-records", ReasonFailed, func(r *executor.Result) {
			r.Stdout.Data, r.Stdout.TotalBytes = nil, 0
			r.Stderr.Data, r.Stderr.TotalBytes = []byte("err: internal error\n"), 20
		}, false},
		{"exit-zero-with-records", ReasonExitMismatch, func(r *executor.Result) {
			zero := 0
			r.ExitCode = &zero
		}, true},
		{"signal", ReasonFailed, func(r *executor.Result) {
			r.ExitCode = nil
			r.Signal = "killed"
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, doc, _, _ := replay(t, findingFiles(), "findings", c.mutate)
			if res.Status != domain.TaskFailed || res.Reason != c.reason || doc.Outcome.PartialParse != c.partial || doc.Parse.OK || doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("%s: %s/%s %+v parse %+v", c.name, res.Status, res.Reason, doc.Outcome, doc.Parse)
			}
		})
	}
}

// The version contract: an unrecognized -version line stops before the
// check runs; a parsable release outside 2026.2 runs with tool_version_untested.
func TestReplayVersionContract(t *testing.T) {
	a, fake := replayAnalyzer(t)
	fx := newFixture(t, "", findingFiles(), domain.Scope{})
	r := &replayRunner{real: &executor.Executor{}, fake: fake, version: "staticcheck (devel, v0.0.0-00010101000000-000000000000)\n", check: loadCapture(t, "findings")}
	res := packtest.RunTask(t, a, fx.Job, "staticcheck", r)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonToolVersion || len(r.specs) != 1 || doc.Coverage.ProcessedPaths != nil {
		t.Fatalf("devel %s/%s staticcheck runs %d %+v", res.Status, res.Reason, len(r.specs), doc.Coverage)
	}
	fx2 := newFixture(t, "", findingFiles(), domain.Scope{})
	r2 := &replayRunner{real: &executor.Executor{}, fake: fake, version: "staticcheck 2025.1.1 (0.6.1)\n", check: loadCapture(t, "findings")}
	res = packtest.RunTask(t, a, fx2.Job, "staticcheck", r2)
	doc, _ = readEnvelope(t, fx2.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Tool.VersionTested || !slices.Contains(doc.Limitations, "tool_version_untested") {
		t.Fatalf("untested %s/%s tested=%v %v", res.Status, res.Reason, doc.Tool.VersionTested, doc.Limitations)
	}
}
