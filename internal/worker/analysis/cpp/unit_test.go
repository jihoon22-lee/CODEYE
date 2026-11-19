package cpp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func manifestOf(paths ...string) source.Manifest {
	var m source.Manifest
	for _, p := range paths {
		m.Entries = append(m.Entries, source.Entry{Path: p, Kind: "file", SHA256: strings.Repeat("c", 64)})
	}
	return m
}

func planJob(scope domain.Scope, paths ...string) analysis.Job {
	return analysis.Job{Scope: scope, Snapshot: source.Snapshot{Manifest: manifestOf(paths...)}}
}

// Plan is deterministic and needs no tool: one visible task per analyzer
// when a C/C++ file is in scope (also headers only), none otherwise. The
// task never executes repository code.
func TestPlan(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		scope   domain.Scope
		paths   []string
		tasks   int
		sources int
	}{
		{"no C/C++", domain.Scope{Kind: "full"}, []string{"main.go", "README.md", "vendor/x.c"}, 0, 0},
		{"headers only", domain.Scope{Kind: "full"}, []string{"include/a.h", "README.md"}, 1, 0},
		{"sources", domain.Scope{Kind: "full"}, []string{"src/a.c", "src/b.cpp", "include/a.hpp", ".hidden/c.c", "testdata/d.c", "vendor/e.cc", "third/f.cxx"}, 1, 3},
		{"scoped", domain.Scope{Kind: "paths", Paths: []string{"src"}}, []string{"src/a.c", "lib/b.c", "compile_commands.json"}, 1, 1},
		{"out of scope", domain.Scope{Kind: "paths", Paths: []string{"docs"}}, []string{"src/a.c"}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range []struct {
				plan     func(context.Context, analysis.Job) ([]pack.Task, error)
				key      string
				required bool
			}{{PlanCppcheck, CppcheckName, true}, {PlanClangTidy, ClangTidyName, false}} {
				tasks, err := p.plan(ctx, planJob(tc.scope, tc.paths...))
				if err != nil || len(tasks) != tc.tasks {
					t.Fatalf("%s: %v %+v", p.key, err, tasks)
				}
				if tc.tasks == 0 {
					continue
				}
				task := tasks[0]
				if task.Spec.Key != p.key || task.Spec.Required != p.required || task.Spec.Stage != domain.StageStaticAnalysis || task.RepositoryCode ||
					task.Module != "." || task.FilesInScope == nil || *task.FilesInScope != tc.sources {
					t.Fatalf("%s task %+v", p.key, task)
				}
				if tc.scope.Kind == "paths" && !slices.Equal(task.Patterns, tc.scope.Paths) {
					t.Fatalf("patterns %v", task.Patterns)
				}
			}
		})
	}
	if _, err := PlanCppcheck(ctx, planJob(domain.Scope{Kind: "diff"}, "a.c")); err == nil {
		t.Fatal("unknown scope kind planned")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := PlanClangTidy(cancelled, planJob(domain.Scope{}, "a.c")); err == nil {
		t.Fatal("cancelled plan")
	}
}

// Descriptors (docs/ANALYZERS.md#기준-분석기와-추가-깊이): cppcheck is the one baseline analyzer of the
// ecosystem, clang-tidy is extra depth; the tool names are the agent flags.
func TestDescriptors(t *testing.T) {
	cc, ct := CppcheckDescriptor(), ClangTidyDescriptor()
	if cc.ID != "cpp.cppcheck" || !cc.Required || cc.Tool != "cppcheck" || cc.Ecosystem != "cpp" || !slices.Equal(cc.VersionArgs, []string{"--version"}) || len(cc.Toolchains) != 0 {
		t.Fatalf("cppcheck %+v", cc)
	}
	if ct.ID != "cpp.clang_tidy" || ct.Required || ct.Tool != "clang-tidy" || ct.Ecosystem != "cpp" || ct.Interpreter != "" {
		t.Fatalf("clang-tidy %+v", ct)
	}
	if _, err := NewCppcheck(Config{Executable: "cppcheck"}); err == nil {
		t.Fatal("relative executable accepted")
	}
	if _, err := NewClangTidy(Config{Executable: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing executable accepted")
	}
}

// The clang-tidy configuration is the rule pack: inline, complete, and the
// Clang Static Analyzer runs only as clang-analyzer-* checks.
func TestClangTidyRulePack(t *testing.T) {
	args := ClangTidyArgs("/scratch/work", []string{"/ws/a.c"})
	if args[0] != "-p" || args[1] != "/scratch/work" || !strings.HasPrefix(args[2], "--config={Checks: \"-*,clang-analyzer-*,") ||
		!slices.Contains(args, "--extra-arg-before=--no-default-config") || slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "--analyze") }) {
		t.Fatalf("args %q", args)
	}
	sel := ClangTidySelection()
	if sel[0] != "-*" || !slices.Contains(sel, "clang-analyzer-*") || !slices.Contains(sel, "HeaderFilterRegex: .*") || slices.ContainsFunc(sel, func(s string) bool { return strings.HasPrefix(s, "cert-") }) {
		t.Fatalf("selection %q", sel)
	}
	for check, want := range map[string]string{"clang-analyzer-unix.Malloc": "resource_lifecycle", "clang-analyzer-security.ArrayBound": "security", "performance-move-const-arg": "performance",
		"clang-analyzer-deadcode.DeadStores": "maintainability", "bugprone-use-after-move": "correctness", "bugprone-exception-escape": "error_handling",
		"clang-analyzer-unix.BlockInCriticalSection": "concurrency"} {
		if got := clangTidyCategory(check); got != want || !slices.Contains(obsdoc.Categories, got) {
			t.Fatalf("%s: %s", check, got)
		}
	}
	for id, want := range map[string]string{"memleak": "resource_lifecycle", "nullPointer": "correctness", "passedByValue": "performance", "exceptThrowInDestructor": "error_handling"} {
		sev := "error"
		if id == "passedByValue" {
			sev = "performance"
		}
		if got := cppcheckCategory(id, sev); got != want {
			t.Fatalf("%s: %s", id, got)
		}
	}
}

func TestVersions(t *testing.T) {
	for in, want := range map[string]string{"Cppcheck 2.22.0\n": "2.22.0", "Cppcheck 2.22\n": "2.22", "Cppcheck 2.23.1": "2.23.1"} {
		if v, ok := ParseCppcheckVersion([]byte(in)); !ok || v != want {
			t.Fatalf("%q: %q %v", in, v, ok)
		}
	}
	for _, bad := range []string{"Cppcheck Premium 25.1.0", "Cppcheck 2.23 dev", "Cppcheck 2.22.0\nextra", ""} {
		if _, ok := ParseCppcheckVersion([]byte(bad)); ok {
			t.Fatalf("%q recognized", bad)
		}
	}
	if !CppcheckVersionTested("2.22.0") || CppcheckVersionTested("2.23.0") || CppcheckVersionTested("2.220") {
		t.Fatal("cppcheck tested series")
	}
	good := "LLVM (http://llvm.org/):\n  LLVM version 23.1.5\n  Optimized build.\n"
	if v, ok := ParseClangTidyVersion([]byte(good)); !ok || v != "23.1.5" || !ClangTidyVersionTested(v) {
		t.Fatalf("%q %v", v, ok)
	}
	for _, bad := range []string{"LLVM version 23.1.5\n", "LLVM (http://llvm.org/):\n  LLVM version 23.1.5git\n", "LLVM (http://llvm.org/):\n  LLVM version 23.1.5\n  LLVM version 22.1.0\n"} {
		if _, ok := ParseClangTidyVersion([]byte(bad)); ok {
			t.Fatalf("%q recognized", bad)
		}
	}
	if ClangTidyVersionTested("22.1.8") || ClangTidyVersionTested("23.10.0") {
		t.Fatal("clang-tidy tested series")
	}
}

// Damaged tool output is never "no findings".
func TestParsersRejectDamagedOutput(t *testing.T) {
	ok := loadCapture(t, "with_generated_header.cppcheck").stderr
	if st := parseCppcheckXML(ok); !st.Complete || st.ShapeErrors != 0 || st.UnparsedBytes != 0 || st.DecodeError != "" || len(st.Records) != st.Values || st.Version != "2.22.0" {
		t.Fatalf("captured XML %+v", st)
	}
	s := string(ok)
	for name, damaged := range map[string]string{
		"truncated":       s[:len(s)/2],
		"trailing data":   s + "<error/>\n",
		"unknown element": strings.Replace(s, "<errors>", "<errors><warning/>", 1),
		"doctype":         strings.Replace(s, "<results", "<!DOCTYPE x [<!ENTITY e \"x\">]><results", 1),
		"results v1":      strings.Replace(s, `<results version="2">`, `<results version="1">`, 1),
		"bad line":        strings.Replace(s, `line="23"`, `line="x"`, 1),
		"unknown attr":    strings.Replace(s, `<error id="memleak"`, `<error new="1" id="memleak"`, 1),
		"comment":         strings.Replace(s, "<errors>", "<errors><!-- x -->", 1),
		"empty":           "",
	} {
		st := parseCppcheckXML([]byte(damaged))
		if st.Complete && st.ShapeErrors == 0 && st.UnparsedBytes == 0 && st.DecodeError == "" {
			t.Fatalf("%s parsed cleanly: %+v", name, st)
		}
	}
	out := loadCapture(t, "with_generated_header.clang-tidy.1").stdout
	if st := parseClangTidyStdout(out); st.ShapeErrors != 0 || st.UnparsedBytes != 0 || st.Values != len(st.Diags) || st.Values == 0 {
		t.Fatalf("captured stdout %+v", st)
	}
	for name, damaged := range map[string]string{
		"orphan note":     "/w/a.c:1:1: note: here\n",
		"no check":        "/w/a.c:1:1: warning: something\n",
		"unknown line":    string(out) + "Segmentation fault\n",
		"orphan snippet":  "    1 | int x;\n",
		"bad check token": "/w/a.c:1:1: warning: x [has space]\n",
	} {
		if st := parseClangTidyStdout([]byte(damaged)); st.ShapeErrors == 0 || st.UnparsedBytes == 0 {
			t.Fatalf("%s parsed cleanly: %+v", name, st)
		}
	}
	errs := parseClangTidyStderr([]byte("Error while trying to load a compilation database:\nCould not auto-detect compilation database\nRunning without flags.\nsomething new\n"))
	if len(errs.Fatal) != 3 || errs.Other != 1 {
		t.Fatalf("stderr %+v", errs)
	}
	multi := parseClangTidyStderr(loadCapture(t, "two_failures.clang-tidy.1").stderr)
	if len(multi.Units) != 3 || multi.Units[0].Errors != 1 || multi.Units[1].Errors != 1 || multi.Units[2].Errors != 3 || !multi.Units[1].ErrorLine {
		t.Fatalf("cumulative error counts %+v", multi.Units)
	}
}

// Sanitization (security): nothing located outside the workspace copy and
// no source line is stored; what was removed still counts.
func TestSanitizeStreams(t *testing.T) {
	inside := func(p string) bool { return strings.HasPrefix(p, "[REDACTED]/") }
	ct := "[REDACTED]/src/a.c:3:5: warning: kept [bugprone-x]\n    3 | int a = SECRET1;\n      |     ^\n" +
		"/etc/x.h:1:1: note: in a host file\n    1 | SECRET2\n" +
		"/home/op/k.h:2:1: error: unknown type name 'SECRET3' [clang-diagnostic-error]\n    2 | SECRET3\n[REDACTED]/src/a.c:1:1: note: included here\n" +
		"/usr/include/x.h:9:1: warning: system [bugprone-y]\n" +
		"<command line>:1:9: warning: macro [bugprone-z]\n"
	r := &executor.Result{Stdout: executor.Stream{Data: []byte(ct), TotalBytes: int64(len(ct)) + 10, Truncated: true}}
	d := sanitizeClangTidy(r, func(p string) bool { return insideOrPseudo(p, inside) })
	got := string(r.Stdout.Data)
	want := "[REDACTED]/src/a.c:3:5: warning: kept [bugprone-x]\n<command line>:1:9: warning: macro [bugprone-z]\n"
	if got != want || d != (ctDropped{Diagnostics: 2, Errors: 1, Notes: 2, Snippets: 4}) || r.Stdout.TotalBytes != int64(len(want))+10 {
		t.Fatalf("sanitized %q %+v total %d", got, d, r.Stdout.TotalBytes)
	}
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<results version="2">
    <cppcheck version="2.22.0"/>
    <errors>
        <error id="nullPointer" severity="error" msg="Null pointer dereference: SECRET4" verbose="x" file0="[REDACTED]/src/b.c">
            <location file="/home/op/ok.h" line="1" column="50" info="SECRET5"/>
            <symbol>SECRET6</symbol>
        </error>
        <error id="zerodiv" severity="error" msg="Division by zero." verbose="Division by zero." file0="[REDACTED]/src/c.c">
            <location file="[REDACTED]/src/c.c" line="1" column="30"/>
        </error>
    </errors>
</results>
`
	r = &executor.Result{Stderr: executor.Stream{Data: []byte(xml), TotalBytes: int64(len(xml))}}
	dropped := sanitizeCppcheck(r, inside)
	st := parseCppcheckXML(r.Stderr.Data)
	if len(dropped) != 1 || dropped[0].ID != "nullPointer" || dropped[0].File0 != "[REDACTED]/src/b.c" || dropped[0].Msg != "" || strings.Contains(string(r.Stderr.Data), "SECRET") ||
		!st.Complete || st.ShapeErrors != 0 || len(st.Records) != 1 || r.Stderr.TotalBytes != int64(len(r.Stderr.Data)) {
		t.Fatalf("sanitized %q %+v %+v", r.Stderr.Data, dropped, st)
	}
}

func mutateStream(s *executor.Stream, data string) {
	s.Data, s.TotalBytes = []byte(data), int64(len(data))
}

// Outcome rules on captured streams: failure, partial parse, timeout,
// truncation and an unidentified tool are never success.
func TestReplayFailuresAreNeverSuccess(t *testing.T) {
	e := loadExpected(t)
	c := e.Cases["with_generated_header"]
	files := caseFiles(t, c)
	okCC, okCT := captures(t, "with_generated_header", CppcheckTool), captures(t, "with_generated_header", ClangTidyTool)
	for _, tc := range []struct {
		name   string
		tool   string
		mutate func(int, *executor.Result)
		status domain.TaskStatus
		reason string
	}{
		{"cppcheck truncated", CppcheckTool, func(_ int, r *executor.Result) { r.Stderr.Truncated = true }, domain.TaskFailed, ReasonOutputTruncated},
		{"cppcheck usage error", CppcheckTool, func(_ int, r *executor.Result) {
			one := 1
			r.ExitCode = &one
			mutateStream(&r.Stdout, "cppcheck: error: unrecognized command line option\n")
			mutateStream(&r.Stderr, "")
		}, domain.TaskFailed, ReasonCppcheckFailed},
		{"cppcheck exit 0 with records", CppcheckTool, func(_ int, r *executor.Result) { zero := 0; r.ExitCode = &zero }, domain.TaskFailed, ReasonExitMismatch},
		{"cppcheck cut XML", CppcheckTool, func(_ int, r *executor.Result) { mutateStream(&r.Stderr, string(r.Stderr.Data[:len(r.Stderr.Data)/2])) }, domain.TaskFailed, ReasonOutputUnparsed},
		{"cppcheck timeout", CppcheckTool, func(_ int, r *executor.Result) { r.Status, r.ExitCode = executor.StatusTimedOut, nil }, domain.TaskTimedOut, ReasonTimeout},
		{"cppcheck cancelled", CppcheckTool, func(_ int, r *executor.Result) { r.Status, r.ExitCode = executor.StatusCancelled, nil }, domain.TaskCancelled, ReasonCancelled},
		{"cppcheck cleanup uncertain", CppcheckTool, func(_ int, r *executor.Result) { r.Cleanup = executor.CleanupUncertain }, domain.TaskFailed, ReasonCleanupUncertain},
		{"cppcheck unprocessed unit", CppcheckTool, func(_ int, r *executor.Result) {
			mutateStream(&r.Stdout, strings.Replace(string(r.Stdout.Data), "Checking [REDACTED]/src/util.cpp ...\n", "", 1))
		}, domain.TaskFailed, ReasonTUNotProcessed},
		{"clang-tidy ran without flags", ClangTidyTool, func(_ int, r *executor.Result) {
			mutateStream(&r.Stderr, "Error while trying to load a compilation database:\nRunning without flags.\n"+string(r.Stderr.Data))
		}, domain.TaskFailed, ReasonClangTidyFailed},
		{"clang-tidy crash", ClangTidyTool, func(_ int, r *executor.Result) { sig := 139; r.ExitCode = &sig }, domain.TaskFailed, ReasonClangTidyFailed},
		{"clang-tidy exit 1 without errors", ClangTidyTool, func(_ int, r *executor.Result) { one := 1; r.ExitCode = &one }, domain.TaskFailed, ReasonExitMismatch},
		{"clang-tidy unknown line", ClangTidyTool, func(_ int, r *executor.Result) { mutateStream(&r.Stdout, string(r.Stdout.Data)+"garbage\n") }, domain.TaskFailed, ReasonOutputUnparsed},
		{"clang-tidy missing unit block", ClangTidyTool, func(_ int, r *executor.Result) {
			mutateStream(&r.Stderr, strings.Replace(string(r.Stderr.Data), "[2/3] Processing file [REDACTED]/src/ring.c.\n", "", 1))
		}, domain.TaskFailed, ReasonOutputUnparsed},
		{"clang-tidy stdout truncated", ClangTidyTool, func(_ int, r *executor.Result) { r.Stdout.Truncated = true }, domain.TaskFailed, ReasonOutputTruncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc obsdoc.Document
			if tc.tool == CppcheckTool {
				_, doc, _ = replayCppcheck(t, files, domain.Scope{}, okCC, tc.mutate)
			} else {
				_, doc, _ = replayClangTidy(t, files, domain.Scope{}, 0, okCT, tc.mutate)
			}
			if doc.Outcome.TaskStatus != tc.status || doc.Outcome.Reason == nil || *doc.Outcome.Reason != tc.reason || doc.Coverage.ProcessedPaths != nil || doc.Parse.OK {
				t.Fatalf("outcome %+v reason %v processed %v", doc.Outcome, doc.Outcome.Reason, doc.Coverage.ProcessedPaths)
			}
		})
	}
}

// Version contract: another release runs with tool_version_untested; an
// unrecognized version output stops the task before the check.
func TestReplayVersionContract(t *testing.T) {
	files := caseFiles(t, loadExpected(t).Cases["with_generated_header"])
	okCC := captures(t, "with_generated_header", CppcheckTool)
	fake := packtest.FakeExecutable(t, "cppcheck")
	a, _ := NewCppcheck(Config{Executable: fake})
	for _, tc := range []struct {
		version string
		status  domain.TaskStatus
		checks  int
	}{{"Cppcheck 2.23.0\n", domain.TaskSucceeded, 1}, {"Cppcheck Premium 25.1\n", domain.TaskFailed, 0}} {
		// The XML names the version too; it must agree with --version.
		r := &replayRunner{fake: fake, version: recorded{stdout: []byte(tc.version)}, checks: okCC, mutate: func(_ int, r *executor.Result) {
			mutateStream(&r.Stderr, strings.Replace(string(r.Stderr.Data), `<cppcheck version="2.22.0"/>`, `<cppcheck version="2.23.0"/>`, 1))
		}}
		fx := newFixture(t, files, domain.Scope{})
		res := runTask(t, a, fx.Job, r)
		doc, _ := readEnvelope(t, fx.Job, res)
		if doc.Outcome.TaskStatus != tc.status || r.n != tc.checks {
			t.Fatalf("%q: %+v checks %d", tc.version, doc.Outcome, r.n)
		}
		if tc.status == domain.TaskSucceeded && (!slices.Contains(doc.Limitations, "tool_version_untested") || doc.Tool.VersionTested) {
			t.Fatalf("untested version: %v %+v", doc.Limitations, doc.Tool)
		}
		if tc.status == domain.TaskFailed && *doc.Outcome.Reason != ReasonToolVersion {
			t.Fatalf("unrecognized version reason %v", *doc.Outcome.Reason)
		}
	}
}

// A workspace input that changes during the run is drift, never success.
func TestReplayWorkspaceDrift(t *testing.T) {
	files := caseFiles(t, loadExpected(t).Cases["with_generated_header"])
	okCC := captures(t, "with_generated_header", CppcheckTool)
	var ws string
	_, doc, _ := replayCppcheck(t, files, domain.Scope{}, okCC, func(_ int, r *executor.Result) {
		for _, a := range r.Command.Argv {
			if strings.HasPrefix(a, "--project=") {
				b, _ := os.ReadFile(strings.TrimPrefix(a, "--project="))
				ws = strings.SplitN(strings.SplitN(string(b), `"directory": "`, 2)[1], `"`, 2)[0]
			}
		}
		_ = os.WriteFile(filepath.Join(ws, "src", "ring.c"), []byte("changed\n"), 0o600)
	})
	if doc.Outcome.TaskStatus != domain.TaskFailed || *doc.Outcome.Reason != ReasonWorkspaceDrift || !slices.Contains(doc.WorkspaceDrift, "src/ring.c") {
		t.Fatalf("drift %+v %v", doc.Outcome, doc.WorkspaceDrift)
	}
}

// Without a translation unit nothing runs: a headers-only snapshot is
// visible as unsupported, never as a clean pass.
func TestHeadersOnlyIsNotAPass(t *testing.T) {
	files := map[string]string{"include/a.h": "int f(void);\n"}
	res, doc, r := replayCppcheck(t, files, domain.Scope{}, nil, nil)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonNoTranslationUnits || len(r.specs) != 0 || doc.Outcome.ExitClass != obsdoc.ExitNotRun ||
		len(doc.Coverage.ProcessedPaths) != 0 || doc.Coverage.ProcessedPaths == nil {
		t.Fatalf("cppcheck headers only %+v %+v", res, doc.Outcome)
	}
	res, _, r = replayClangTidy(t, files, domain.Scope{}, 0, nil, nil)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonCompileDBMissing || len(r.specs) != 0 {
		t.Fatalf("clang-tidy headers only %+v", res)
	}
}
