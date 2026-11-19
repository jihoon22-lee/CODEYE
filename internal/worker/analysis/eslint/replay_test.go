package eslint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/tsc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without Node: the executions return the redacted
// streams captured from eslint 10.15.0 (typescript-eslint 8.73.0,
// @eslint/js 10.0.1, TypeScript 6.0.3) and tsc 6.0.3 in testdata/ (see the
// capture command in real_test.go). The fixture sources are the ones the
// captures were taken from; changing them requires a new capture.

const typedMain = "export async function later(): Promise<number> {\n  return 1;\n}\n"

func replayFiles(t *testing.T, name string) map[string]string {
	switch {
	case strings.HasPrefix(name, "monorepo"):
		return jststest.MonorepoFiles(t, 4)
	case name == "clean":
		return caseFiles(map[string]string{"index.js": fixClean})
	case name == "parse-error":
		return caseFiles(map[string]string{"index.js": fixBroken, "ok.js": "const unused = 1;\n"})
	case name == "suppressions":
		return caseFiles(map[string]string{"index.js": fixSup, "eslint-suppressions.json": fixSupFile})
	case name == "typed-gap":
		return caseFiles(map[string]string{"tsconfig.json": fixGapTSConfig, "src/main.ts": fixGapMain})
	case name == "typed-deprecated":
		return caseFiles(map[string]string{"tsconfig.json": "{\"compilerOptions\":{\"target\":\"ES2022\",\"module\":\"commonjs\",\"moduleResolution\":\"node\",\"baseUrl\":\".\",\"lib\":[\"ES2022\"],\"strict\":true},\"include\":[\"src\"]}\n",
			"src/main.ts": typedMain})
	case name == "typed-package-extends":
		return caseFiles(map[string]string{"tsconfig.json": "{\"extends\":\"@tsconfig/strictest/tsconfig.json\",\"compilerOptions\":{\"lib\":[\"ES2022\"]},\"include\":[\"src\"]}\n",
			"src/main.ts": typedMain})
	case strings.HasPrefix(name, "overlap"):
		return caseFiles(map[string]string{"tsconfig.json": fixOverlapConfig, "src/o.ts": fixOverlapTS, "lib/o.js": fixOverlapJS})
	}
	t.Fatalf("no fixture for %s", name)
	return nil
}

func replayRun(t *testing.T, name string, typed bool, key string, mutate func(jststest.Recorded, *executor.Result)) (*Analyzer, packtest.Fixture, *jststest.Replay, obsdoc.Document) {
	t.Helper()
	a := fakeAnalyzer(t, typed)
	fx := packtest.NewFixture(t, "", replayFiles(t, name), domain.Scope{})
	r := &jststest.Replay{T: t, Steps: jststest.LoadCapture(t, name), Mutate: mutate, Match: func(step jststest.Recorded, s executor.Spec) bool {
		return (step.Name == execVersion) == slices.Equal(s.Args[1:], []string{"--version"})
	}}
	res := packtest.RunTask(t, a, fx.Job, key, r)
	doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
	packtest.AssertNoNodePaths(t, raw, nodePaths(a, fx)...)
	return a, fx, r, doc
}

func TestReplayESLintCaptures(t *testing.T) {
	for _, c := range []struct {
		name, key string
		typed     bool
		status    domain.TaskStatus
		reason    string
		rules     map[string]int
		processed int
	}{
		{"monorepo-root", "eslint", false, domain.TaskSucceeded, "", map[string]int{"no-unused-vars": 1, "no-empty": 1}, 1},
		{"monorepo-app", "eslint.m01", false, domain.TaskSucceeded, "", map[string]int{"@typescript-eslint/no-explicit-any": 2, "@typescript-eslint/no-unused-vars": 1, "prefer-const": 1}, 4},
		{"monorepo-lib", "eslint.m02", false, domain.TaskSucceeded, "", map[string]int{"no-unused-vars": 2, "no-cond-assign": 1, "no-constant-condition": 1, "no-dupe-keys": 1}, 2},
		{"monorepo-typed", "eslint-typed.m01", true, domain.TaskSucceeded, "", map[string]int{"@typescript-eslint/no-floating-promises": 1, "@typescript-eslint/no-misused-promises": 1,
			"@typescript-eslint/no-unsafe-assignment": 1, "@typescript-eslint/no-unsafe-member-access": 1, "@typescript-eslint/no-unsafe-return": 1, "@typescript-eslint/require-await": 1}, 3},
		{"clean", "eslint", false, domain.TaskSucceeded, "", map[string]int{}, 1},
		{"parse-error", "eslint", false, domain.TaskSucceeded, "", map[string]int{"no-unused-vars": 1, ParseErrorRule: 1}, 1},
		{"suppressions", "eslint", false, domain.TaskSucceeded, "", map[string]int{"no-empty": 1, "no-unused-vars": 1, "no-debugger": 1}, 1},
		{"typed-gap", "eslint-typed", true, domain.TaskFailed, ReasonDependency, map[string]int{}, -1},
		{"typed-deprecated", "eslint-typed", true, domain.TaskSucceeded, "", map[string]int{"@typescript-eslint/require-await": 1}, 1},
		{"typed-package-extends", "eslint-typed", true, domain.TaskFailed, ReasonConfig, map[string]int{}, -1},
		{"overlap", "eslint", false, domain.TaskSucceeded, "", map[string]int{"@typescript-eslint/no-unused-vars": 2, "no-unused-vars": 2, "no-fallthrough": 2, "no-dupe-class-members": 1}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, fx, r, doc := replayRun(t, c.name, c.typed, c.key, nil)
			got := packtest.Rules(doc)
			if doc.Outcome.TaskStatus != c.status || (c.reason != "" && *doc.Outcome.Reason != c.reason) || !mapsEqual(got, c.rules) {
				t.Fatalf("%s: %+v rules %v errors %+v", c.name, doc.Outcome, got, doc.ToolErrors)
			}
			if (c.processed < 0) != (doc.Coverage.ProcessedPaths == nil) || (c.processed >= 0 && len(doc.Coverage.ProcessedPaths) != c.processed) {
				t.Fatalf("%s: processed %v", c.name, doc.Coverage.ProcessedPaths)
			}
			checkSpecs(t, a, fx, r.Specs)
		})
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// checkSpecs: node runs the operator script (never PATH, never #!), with
// PATH = the node directory and LANG only, in the module directory of the
// workspace copy, the managed configuration in task scratch loading only
// absolute tool-directory modules, and "--" before the positionals.
func checkSpecs(t *testing.T, a *Analyzer, fx packtest.Fixture, specs []executor.Spec) {
	t.Helper()
	for _, s := range specs {
		if s.Executable != a.script.Node.Executable || s.Args[0] != a.script.Path || s.Env["PATH"] != a.script.Node.Dir || !jststest.EnvAllowlisted(s.Env) || s.Env["LANG"] != "C.UTF-8" ||
			!strings.HasPrefix(s.Dir, fx.Job.Snapshot.WorkspaceDir) || s.Timeout <= 0 || !slices.Contains(s.ProtectedRoots, fx.Job.Snapshot.SnapshotDir) {
			t.Fatalf("spec %+v", s)
		}
		i := slices.Index(s.Args, "-c")
		if i < 0 {
			continue
		}
		cfg := s.Args[i+1]
		if !strings.HasPrefix(cfg, fx.Job.Scratch) || !slices.Contains(s.Args, "--no-inline-config") || !slices.Contains(s.Args, "--") {
			t.Fatalf("check argv %v", s.Args)
		}
		body, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if j := strings.Index(line, "require("); j >= 0 && !strings.Contains(line[j:], "require("+lit(a.plugins.dir)[:len(lit(a.plugins.dir))-1]) {
				t.Fatalf("config requires a module outside the tool dir: %s", line)
			}
		}
		if strings.Contains(string(body), fx.Job.Snapshot.WorkspaceDir+string(filepath.Separator)+"node_modules") {
			t.Fatal("config names workspace node_modules")
		}
	}
}

// The tsc half of the overlap capture: the TS constructs the baseline
// leaves to tsc are reported by tsc, its lint-like checks are off.
func TestReplayOverlapTsc(t *testing.T) {
	_, script, node := jststest.FakeTools(t)
	tc, err := tsc.New(tsc.Config{Script: script, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	fx := packtest.NewFixture(t, "", replayFiles(t, "overlap-tsc"), domain.Scope{})
	res := packtest.RunTask(t, tc, fx.Job, "tsc", &jststest.Replay{T: t, Steps: jststest.LoadCapture(t, "overlap-tsc")})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if got := packtest.Rules(doc); !mapsEqual(got, map[string]int{"TS2304": 1, "TS2393": 2}) {
		t.Fatalf("tsc rules %v", got)
	}
}

// Damaged replays and version handling (TC-08 on captured bytes).
func TestReplayESLintDamagedAndVersions(t *testing.T) {
	isCheck := func(s jststest.Recorded) bool { return s.Name == execCheck }
	for _, c := range []struct {
		name, reason string
		mutate       func(jststest.Recorded, *executor.Result)
	}{
		{"truncated", ReasonOutputTruncated, func(s jststest.Recorded, r *executor.Result) {
			if isCheck(s) {
				r.Stdout.Data, r.Stdout.Truncated = r.Stdout.Data[:len(r.Stdout.Data)/2], true
			}
		}},
		{"crash", ReasonFailed, func(s jststest.Recorded, r *executor.Result) {
			if isCheck(s) {
				two := 2
				r.ExitCode, r.Stdout.Data, r.Stdout.TotalBytes = &two, nil, 0
			}
		}},
		{"exit-zero", ReasonExitMismatch, func(s jststest.Recorded, r *executor.Result) {
			if isCheck(s) {
				zero := 0
				r.ExitCode = &zero
			}
		}},
		{"version-garbage", ReasonToolVersion, func(s jststest.Recorded, r *executor.Result) {
			if !isCheck(s) {
				r.Stdout.Data = []byte("eslint dev\n")
				r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, r, doc := replayRun(t, "monorepo-lib", false, "eslint.m02", c.mutate)
			if doc.Outcome.TaskStatus != domain.TaskFailed || *doc.Outcome.Reason != c.reason || doc.Coverage.ProcessedPaths != nil && len(doc.Coverage.ProcessedPaths) > 0 {
				t.Fatalf("%s: %+v %+v", c.name, doc.Outcome, doc.Coverage)
			}
			if c.name == "version-garbage" && len(r.Specs) != 1 {
				t.Fatalf("an unidentified eslint ran the check: %d", len(r.Specs))
			}
		})
	}
	// A newer patch series runs, marked untested.
	_, _, _, doc := replayRun(t, "monorepo-lib", false, "eslint.m02", func(s jststest.Recorded, r *executor.Result) {
		if s.Name == execVersion {
			r.Stdout.Data = []byte("v10.16.0\n")
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}
	})
	if doc.Outcome.TaskStatus != domain.TaskSucceeded || doc.Tool.VersionTested || !slices.Contains(doc.Limitations, "tool_version_untested") {
		t.Fatalf("untested version %+v %+v %v", doc.Outcome, doc.Tool, doc.Limitations)
	}
}

// docs/ANALYZERS.md#저장소-코드와-tsconfig-검사: a result for a file outside the snapshot is dropped without
// echoing its path or messages, and recorded as a limitation.
func TestReplayESLintHostResultDropped(t *testing.T) {
	host := `{"filePath":"/etc/host-secret.js","messages":[{"ruleId":"no-unused-vars","severity":2,"message":"MARKER-host-text","line":1,"column":1}],` +
		`"suppressedMessages":[],"errorCount":1,"fatalErrorCount":0,"warningCount":0,"fixableErrorCount":0,"fixableWarningCount":0,"usedDeprecatedRules":[]},`
	_, _, _, doc := replayRun(t, "monorepo-lib", false, "eslint.m02", func(s jststest.Recorded, r *executor.Result) {
		if s.Name == execCheck {
			r.Stdout.Data = append([]byte("["+host), r.Stdout.Data[1:]...)
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}
	})
	for _, o := range doc.Observations {
		if strings.Contains(o.RawMessage, "MARKER") || strings.Contains(*o.Path, "host-secret") {
			t.Fatalf("host result kept: %+v", o)
		}
	}
	for _, te := range doc.ToolErrors {
		if strings.Contains(te.Message, "MARKER") || strings.Contains(te.Message, "host-secret") {
			t.Fatalf("host result echoed: %+v", te)
		}
	}
	if !slices.Contains(doc.Limitations, "results_outside_snapshot") || len(doc.Observations) != 5 {
		t.Fatalf("%+v %v %d", doc.Outcome, doc.Limitations, len(doc.Observations))
	}
}
