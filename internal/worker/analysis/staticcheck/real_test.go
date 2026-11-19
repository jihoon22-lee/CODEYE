package staticcheck

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real staticcheck tests run only with CODEYE_TEST_STATICCHECK (CI has no
// staticcheck). CODEYE_STATICCHECK_CAPTURE=DIR also writes the redacted
// staticcheck.check streams and exit code of each case to DIR, which is how
// testdata/*.stdout|stderr|exit were captured (docs/ANALYZERS.md#staticcheck).

func capture(t *testing.T, name string, res analysis.TaskResult) {
	t.Helper()
	dir := os.Getenv("CODEYE_STATICCHECK_CAPTURE")
	if dir == "" {
		return
	}
	for _, e := range res.Executions {
		if e.Name != execCheck {
			continue
		}
		code := "none"
		if e.Result.ExitCode != nil {
			code = strconv.Itoa(*e.Result.ExitCode)
		}
		for suffix, data := range map[string][]byte{".stdout": e.Result.Stdout.Data, ".stderr": e.Result.Stderr.Data, ".exit": []byte(code + "\n")} {
			if bytes.Contains(data, []byte("/home/")) || bytes.Contains(data, []byte(`\Users\`)) {
				t.Fatalf("capture %s%s holds a node path", name, suffix)
			}
			if err := os.WriteFile(filepath.Join(dir, name+suffix), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func ruleCount(doc obsdoc.Document) map[string]int {
	out := map[string]int{}
	for _, o := range doc.Observations {
		out[o.RuleID]++
	}
	return out
}

// TC-07: findings make staticcheck exit 1; with only check records and a
// clean stderr that is success_with_findings, not a crash.
func TestRealStaticcheckFindingsExitOne(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	base := filepath.Join(t.TempDir(), "공백 & (dir)")
	fx := newFixture(t, base, findingFiles(), domain.Scope{})
	before, snapBefore := treeHash(t, fx.SourceRoot), treeHash(t, fx.Job.Snapshot.SnapshotDir)
	a := newAnalyzer(t, "")
	res := runTask(t, a, fx.Job, "staticcheck", &executor.Executor{})
	capture(t, "findings", res)
	doc, raw := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.Outcome.PartialParse {
		t.Fatalf("status %s/%s outcome %+v errors %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	check := res.Executions[len(res.Executions)-1]
	if check.Name != execCheck || check.Result.ExitCode == nil || *check.Result.ExitCode != 1 {
		t.Fatalf("staticcheck must exit 1 with findings: %+v", check.Result)
	}
	names := []string{}
	for _, e := range doc.Executions {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{execGoVersion, execVersion, execModEdit, execList, execCheck}) {
		t.Fatalf("executions %v", names)
	}
	rules := ruleCount(doc)
	for _, r := range []string{"SA1000", "SA1012", "SA4006", "SA4023", "U1000", "SA2001"} {
		if rules[r] == 0 {
			t.Fatalf("rule %s missing: %v", r, rules)
		}
	}
	related := false
	for _, o := range doc.Observations {
		if o.Location == nil || o.LocationState != obsdoc.LocationResolved || !strings.HasPrefix(o.Location.Path, "find/") || o.RawSeverity == nil || *o.RawSeverity != "error" {
			t.Fatalf("observation %+v", o)
		}
		if o.Category == "" || o.Unit != "example.test/sc/find" {
			t.Fatalf("category/unit %+v", o)
		}
		related = related || len(o.Related) > 0 && o.Related[0].LocationState == obsdoc.LocationResolved
	}
	if !related {
		t.Fatal("SA4023 related location not resolved")
	}
	if doc.Coverage.Units[PackageAnalyzedWithFindings] != 1 || doc.Coverage.Units[PackageAnalyzedClean] != 1 || doc.Coverage.FilesSkipped != 1 ||
		!slices.Equal(doc.Coverage.ProcessedPaths, []string{"clean/c.go", "find/find.go", "find/find_test.go"}) || *doc.Coverage.FilesInScope != 4 {
		t.Fatalf("coverage %+v", doc.Coverage)
	}
	if doc.Tool.Version == nil || !doc.Tool.VersionTested || doc.Target.Properties["toolchain"] == "" || doc.Target.Platform == nil || doc.Module.Name != "example.test/sc" {
		t.Fatalf("tool/target %+v %+v", doc.Tool, doc.Target)
	}
	for _, l := range []string{"cgo_disabled", "default_build_context_only", "go_vet_overlap_checks_excluded", "build_constraint_ignored_files", "no_offline_module_cache"} {
		if !slices.Contains(doc.Limitations, l) {
			t.Fatalf("limitation %s missing: %v", l, doc.Limitations)
		}
	}
	assertNoNodePaths(t, raw, base, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.SnapshotDir, a.resolved)
	if treeHash(t, fx.SourceRoot) != before || treeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("source or snapshot changed")
	}
	// Same input, fresh attempt: IDs are content derived.
	fx2 := newFixture(t, "", findingFiles(), domain.Scope{})
	doc2, _ := readEnvelope(t, fx2.Job, runTask(t, a, fx2.Job, "staticcheck", &executor.Executor{}))
	for i := range doc.Observations {
		if doc.Observations[i].ID != doc2.Observations[i].ID {
			t.Fatalf("observation ids differ between runs: %s %s", doc.Observations[i].ID, doc2.Observations[i].ID)
		}
	}
}

// TC-104 (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제): with -show-ignored staticcheck reports the
// problems //lint:ignore and //lint:file-ignore hide, with severity
// "ignored". They are kept as suppressed observations (source_suppressions
// reported) and are not findings: a package whose every problem is
// suppressed still exits 1 and is success_without_findings. A suppressed
// U1000 leaves no record at all, so the rule pack lists U1000 as an
// unreported suppression rule.
func TestRealStaticcheckSuppressionsReported(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	a := newAnalyzer(t, "")
	for _, tc := range []struct {
		name           string
		all            bool
		active, hidden int
		class          string
	}{
		{"suppressed", false, 2, 2, obsdoc.ExitSuccessWithFindings},
		{"suppressed-only", true, 0, 3, obsdoc.ExitSuccessWithoutFindings},
	} {
		fx := newFixture(t, "", suppressionFiles(tc.all), domain.Scope{})
		res := runTask(t, a, fx.Job, "staticcheck", &executor.Executor{})
		capture(t, tc.name, res)
		doc, _ := readEnvelope(t, fx.Job, res)
		check := res.Executions[len(res.Executions)-1]
		if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != tc.class || check.Result.ExitCode == nil || *check.Result.ExitCode != 1 {
			t.Fatalf("%s: %s/%s %+v exit %v", tc.name, res.Status, res.Reason, doc.Outcome, check.Result.ExitCode)
		}
		active, hidden := 0, 0
		for _, o := range doc.Observations {
			// The only U1000 record is the visible function's.
			if o.LocationState != obsdoc.LocationResolved || (o.RuleID != "SA1000" && (o.RuleID != "U1000" || o.Suppressed || o.Location.StartLine != 3)) {
				t.Fatalf("%s: observation %+v", tc.name, o)
			}
			if o.Suppressed {
				hidden++
				if *o.RawSeverity != "ignored" {
					t.Fatalf("%s: suppressed severity %+v", tc.name, o)
				}
			} else {
				active++
			}
		}
		if active != tc.active || hidden != tc.hidden || doc.RulePack.SourceSuppressions != obsdoc.SuppressReported ||
			!slices.Contains(doc.RulePack.Selection, "-show-ignored") || !slices.Contains(doc.Limitations, "source_suppressed_observations") ||
			!slices.Equal(doc.RulePack.UnreportedSuppressionRules, []string{"U1000"}) {
			t.Fatalf("%s: active %d hidden %d rule pack %+v limitations %v", tc.name, active, hidden, doc.RulePack, doc.Limitations)
		}
		if !slices.Equal(doc.Coverage.ProcessedPaths, []string{"sup/file.go", "sup/sup.go", "sup/unused.go"}) {
			t.Fatalf("%s: coverage %+v", tc.name, doc.Coverage)
		}
	}
}

func TestRealStaticcheckClean(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	fx := newFixture(t, "", map[string]string{"go.mod": fixGoMod, "clean/c.go": fixClean}, domain.Scope{})
	res := runTask(t, newAnalyzer(t, ""), fx.Job, "staticcheck", &executor.Executor{})
	capture(t, "clean", res)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || len(doc.Observations) != 0 || len(doc.Coverage.ProcessedPaths) != 1 {
		t.Fatalf("clean %s %+v %+v", res.Status, doc.Outcome, doc.Coverage)
	}
}

// TC-08: a type error is a compile record (exit 1): the task fails with
// package_errors, the other package's observations are kept (partial parse)
// and processed files are unknown.
func TestRealStaticcheckTypeErrorPartial(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	files := findingFiles()
	files["typeerr/t.go"] = fixType
	fx := newFixture(t, "", files, domain.Scope{})
	res := runTask(t, newAnalyzer(t, ""), fx.Job, "staticcheck", &executor.Executor{})
	capture(t, "typeerr", res)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonPackageErrors || doc.Outcome.ExitClass != obsdoc.ExitExecutionError || !doc.Outcome.PartialParse {
		t.Fatalf("type error %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
	if len(doc.Observations) == 0 || len(doc.ToolErrors) == 0 || doc.ToolErrors[0].Kind != "typecheck" || doc.ToolErrors[0].Unit != "example.test/sc/typeerr" ||
		doc.ToolErrors[0].Path == nil || *doc.ToolErrors[0].Path != "typeerr/t.go" {
		t.Fatalf("observations kept / tool errors %+v", doc.ToolErrors)
	}
	if doc.Coverage.ProcessedPaths != nil || doc.Coverage.Units[PackageFailedTypecheck] != 1 || doc.Parse.OK {
		t.Fatalf("coverage %+v parse %+v", doc.Coverage, doc.Parse)
	}
}

// TC-68: offline dependency and toolchain gaps are configuration errors; no
// download fallback, the empty module cache gains no file.
func TestRealStaticcheckOfflineConfigurationErrors(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	a := newAnalyzer(t, "")
	dep := newFixture(t, "", map[string]string{"go.mod": "module example.test/dep\n\ngo 1.22\n\nrequire golang.org/x/text v0.3.0\n", "dep.go": fixDep}, domain.Scope{})
	res := runTask(t, a, dep.Job, "staticcheck", &executor.Executor{})
	capture(t, "dependency", res)
	doc, _ := readEnvelope(t, dep.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonDependency || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError {
		t.Fatalf("dependency %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
	var files []string
	_ = filepath.WalkDir(filepath.Join(dep.Job.Scratch, "analyzers", "staticcheck", "gopath"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 0 {
		t.Fatalf("module cache gained files offline: %v", files)
	}
	newer := newFixture(t, "", map[string]string{"go.mod": "module example.test/newer\n\ngo 1.40\n", "n.go": "package newer\n"}, domain.Scope{})
	res = runTask(t, a, newer.Job, "staticcheck", &executor.Executor{})
	capture(t, "toolchain", res)
	doc, _ = readEnvelope(t, newer.Job, res)
	if res.Status != domain.TaskFailed || res.Reason != ReasonToolchain || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError {
		t.Fatalf("toolchain %s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
	}
}

// Every package excluded by build constraints: staticcheck exits 0 with only
// a stderr warning, which is not a clean result.
func TestRealStaticcheckNothingAnalyzable(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	fx := newFixture(t, "", map[string]string{"go.mod": fixGoMod, "tagged/t.go": fixTag}, domain.Scope{})
	res := runTask(t, newAnalyzer(t, ""), fx.Job, "staticcheck", &executor.Executor{})
	capture(t, "no-packages", res)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonNoPackages || doc.Outcome.ExitClass != obsdoc.ExitConfigurationError {
		t.Fatalf("no packages %s/%s %+v", res.Status, res.Reason, doc.Outcome)
	}
}

// A filesystem replace leaving the snapshot is refused before go list and
// staticcheck read the host path.
func TestRealStaticcheckReplaceOutsideRefused(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	fx := newFixture(t, "", map[string]string{"go.mod": "module example.test/r\n\ngo 1.22\n\nreplace example.test/x => ../outside\n", "r.go": "package r\n"}, domain.Scope{})
	runner := &mutatingRunner{inner: &executor.Executor{}}
	res := runTask(t, newAnalyzer(t, ""), fx.Job, "staticcheck", runner)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonReplaceOutside || len(runner.specs) != 3 {
		t.Fatalf("replace %s/%s executions %d %+v", res.Status, res.Reason, len(runner.specs), doc.Outcome)
	}
}

// TC-08 on real bytes: truncated or garbled stdout and a crash exit are
// failures that keep what was parsed; none of them reads as zero findings.
func TestRealStaticcheckDamagedOutput(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	a := newAnalyzer(t, "")
	isCheck := func(s executor.Spec) bool { return len(s.Args) > 0 && s.Args[0] == "-f" }
	cases := []struct {
		name, reason string
		mutate       func(r *executor.Result)
		partial      bool
	}{
		{"truncated", ReasonOutputTruncated, func(r *executor.Result) {
			r.Stdout.Data = r.Stdout.Data[:len(r.Stdout.Data)/2]
			r.Stdout.Truncated = true
		}, true},
		{"garbled", ReasonOutputUnparsed, func(r *executor.Result) { r.Stdout.Data = append(r.Stdout.Data, []byte("{\"code\":")...) }, true},
		{"crash", ReasonFailed, func(r *executor.Result) {
			two := 2
			r.ExitCode = &two
			r.Stdout.Data, r.Stdout.TotalBytes = nil, 0
			r.Stderr.Data, r.Stderr.TotalBytes = []byte("panic: boom\n"), 12
		}, false},
		{"exit-zero-with-records", ReasonExitMismatch, func(r *executor.Result) { zero := 0; r.ExitCode = &zero }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t, "", findingFiles(), domain.Scope{})
			runner := &mutatingRunner{inner: &executor.Executor{}, mutate: func(s executor.Spec, r *executor.Result) {
				if isCheck(s) {
					c.mutate(r)
				}
			}}
			res := runTask(t, a, fx.Job, "staticcheck", runner)
			doc, _ := readEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskFailed || res.Reason != c.reason || doc.Outcome.PartialParse != c.partial || doc.Parse.OK || doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("%s: %s/%s %+v parse %+v", c.name, res.Status, res.Reason, doc.Outcome, doc.Parse)
			}
		})
	}
}

func TestRealStaticcheckCancelAndTimeout(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	a := newAnalyzer(t, "")
	fx := newFixture(t, "", findingFiles(), domain.Scope{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tasks, _ := a.Plan(context.Background(), fx.Job)
	res := a.Run(ctx, fx.Job, tasks[0].Spec, &executor.Executor{})
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskCancelled || doc.Outcome.ExitClass != obsdoc.ExitCancelled {
		t.Fatalf("cancelled %s %+v", res.Status, doc.Outcome)
	}
	short, err := New(Config{Executable: a.cfg.Executable, Go: a.cfg.Go, Timeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	fx2 := newFixture(t, "", findingFiles(), domain.Scope{})
	res = runTask(t, short, fx2.Job, "staticcheck", &executor.Executor{})
	doc, _ = readEnvelope(t, fx2.Job, res)
	if res.Status != domain.TaskTimedOut || doc.Outcome.ExitClass != obsdoc.ExitTimeout {
		t.Fatalf("timeout %s %+v", res.Status, doc.Outcome)
	}
}

// docs/ANALYZERS.md#탐색-probe: the excluded staticcheck checks are the ones go vet reports in
// the same run; SA4000 (identical operands) stays on as a broader check, so
// it and go vet's bools both report line 37 as separate observations.
func TestRealStaticcheckVetOverlapExcluded(t *testing.T) {
	testStaticcheck(t) // skip before building fixtures (CI has no staticcheck)
	fx := newFixture(t, "", map[string]string{"go.mod": fixGoMod, "overlap/o.go": fixOverlap, "overlap/o_test.go": fixOverlapT}, domain.Scope{})
	res := runTask(t, newAnalyzer(t, ""), fx.Job, "staticcheck", &executor.Executor{})
	capture(t, "overlap", res)
	doc, _ := readEnvelope(t, fx.Job, res)
	rules := ruleCount(doc)
	for code := range VetOverlap {
		if rules[code] != 0 {
			t.Fatalf("excluded check %s reported: %v", code, rules)
		}
	}
	if rules["SA4000"] != 1 || len(doc.Observations) != 1 {
		t.Fatalf("overlap fixture rules %v", rules)
	}
}
