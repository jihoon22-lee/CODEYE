package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gitleaks"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The review slice report example (examples/report-slices.json and .md,
// docs/REPORT.md#모듈별-검토-장부) is the builder's output for
// fixtures/modules/slices/input under the smallest call ceiling (15): the
// real discovery and review handlers run in process with a scripted
// model caller that answers every task with one call (no model is
// contacted), a clean synthetic secrets screen and a go vet stand-in without
// diagnostics. Some ranges are left when the call ceiling is reached.
// CODEYE_UPDATE_EXAMPLES=1 rewrites both files.
const (
	sliceExampleJSON = "../../../examples/report-slices.json"
	sliceExampleMD   = "../../../examples/report-slices.md"
	sliceExampleCap  = 15
)

const sliceExampleBanner = "> **합성 예시.** 아래는 `fixtures/modules/slices/input`에 대해 CODEYE의 탐색·검토 역할과 모듈 단위 검토 분할을 호출 상한 15로 실제로 실행하고 보고서 builder가 만든 출력이다. 모델 응답은 미리 정한 스크립트(작업마다 호출 1회)이며 모델을 부르지 않았다. 비밀값 탐지는 탐지 없는 합성 결과이고 정적 분석 도구는 실행하지 않았다. 사용자 프로젝트의 결함 발견 결과가 아니다. 기계 판독 원본은 [report-slices.json](report-slices.json)이다.\n"

// TC-169 (docs/REPORT.md#모듈별-검토-장부).
func TestReportSlicesExample(t *testing.T) {
	j := coverageJob(t)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000601", "0195f3a0-0000-7000-8000-000000000602"
	pinExampleSnapshot(t, &j)
	ids := &exampleIDs{n: 0x102}
	caller := &scriptedCaller{script: coverageScript(t)}
	h, err := discovery.Wrap(Handler{Static: sliceExampleStatic(t), Caller: caller, MaxCalls: sliceExampleCap, Budget: smallBudget}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]analysis.TaskResult{}
	for _, task := range plan {
		results[task.Key] = h.Run(t.Context(), j, task, nil)
	}
	in := exampleInput(t, j, h, plan, results, ids)
	r, err := report.Build(in, report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	checkSliceExample(t, r, len(caller.requests))
	// Every slot investigation was offered leads too.
	requireLeadsRecords(t, r, "review.investigate", "review.investigate.s001", "review.investigate.s002")
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte(`"demo":false`)) != 1 {
		t.Fatal("demo flag not found once")
	}
	raw = bytes.Replace(raw, []byte(`"demo":false`), []byte(`"demo":true`), 1)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	md := string(report.RenderMarkdownAt(r, report.Digest(raw), report.Korean, report.DetailSummary))
	title, rest, _ := strings.Cut(md, "\n")
	files := map[string][]byte{sliceExampleJSON: pretty.Bytes(), sliceExampleMD: []byte(title + "\n\n" + sliceExampleBanner + rest)}
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		for p, b := range files {
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for p, b := range files {
		compareStoredExample(t, p, b)
	}
}

// sliceReport runs the review with the slices on j through the report
// builder.
func sliceReport(t *testing.T, j analysis.Job, static analysis.Handler, maxCalls int, ids *exampleIDs) report.Report {
	t.Helper()
	return sliceReportWithCaller(t, j, static, maxCalls, ids, &scriptedCaller{script: coverageScript(t)})
}

// sliceReportWithCaller runs the review at the smallest request budget,
// where the slice fixtures exceed the overview.
func sliceReportWithCaller(t *testing.T, j analysis.Job, static analysis.Handler, maxCalls int, ids *exampleIDs, caller *scriptedCaller) report.Report {
	t.Helper()
	pinExampleSnapshot(t, &j)
	h, err := discovery.Wrap(Handler{Static: static, Caller: caller, MaxCalls: maxCalls, Budget: smallBudget}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]analysis.TaskResult{}
	for _, task := range plan {
		results[task.Key] = h.Run(t.Context(), j, task, nil)
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, ids), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TC-169: without a secrets scan the slices still run, as the whole-run
// roles do. The scope artifact is accepted, every module has its row, the
// slices' findings are in the report, and the report says the model input
// was not screened (secrets.model_input not_screened and the gap
// model_input_not_screened). The report is stored as
// internal/report/testdata/report-unscreened.json, which the schema check
// (validate_bundle.py) and the web parser read too.
func TestReportSlicesUnscreened(t *testing.T) {
	j := coverageJob(t)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000701", "0195f3a0-0000-7000-8000-000000000702"
	static, err := pack.NewComposite(exampleMember{d: govet.Descriptor(), plan: govet.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult { return exampleVet(t, j, task) }})
	if err != nil {
		t.Fatal(err)
	}
	r := sliceReport(t, j, static, record.DefaultMaxCalls, &exampleIDs{n: 0x202})
	l := r.CodeReview.Ledger
	if l == nil || l.Scope.Result != "accepted" || len(l.Modules) == 0 || l.Status != report.LedgerComplete ||
		l.MaxCalls == nil || *l.MaxCalls != record.DefaultMaxCalls || r.Secrets.ModelInput != report.ScreenNotScreened {
		t.Fatalf("ledger %+v model input %s", l, r.Secrets.ModelInput)
	}
	gaps := map[string]bool{}
	for _, g := range r.Gaps {
		gaps[g.Code] = true
	}
	if !gaps["model_input_not_screened"] || gaps["review_scope_skipped"] {
		t.Fatalf("gaps %+v", r.Gaps)
	}
	sliceFindings := 0
	for _, f := range r.Findings {
		if strings.HasPrefix(f.ID, "review_s") {
			sliceFindings++
		}
	}
	if sliceFindings == 0 {
		t.Fatal("no slice finding without a screen")
	}
	writeOrCompare(t, r, "../../report/testdata/report-unscreened.json")
}

// TC-190: a secrets task that ran but whose result is unusable (here a
// failed scan, as after workspace drift) is no screen. The whole-run roles,
// the scope and the slices run and are accepted, no review task is skipped
// for the screen, the records say model_input_not_secret_screened, the
// report's secrets section is not_screened with the gap
// model_input_not_screened, and its sentence names the secrets task with its
// state and reason instead of saying the scan did not run. The report is
// stored as internal/report/testdata/report-screen-unusable.json, which the
// schema check (validate_bundle.py) and the web parser read too.
func TestReportSlicesSecretScreenUnusable(t *testing.T) {
	j := coverageJob(t)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000901", "0195f3a0-0000-7000-8000-000000000902"
	gl := gitleaks.Descriptor()
	static, err := pack.NewComposite(
		exampleMember{d: govet.Descriptor(), plan: govet.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult { return exampleVet(t, j, task) }},
		exampleMember{d: gl, plan: gitleaks.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			return exampleFailure(t, j, gl, task, "8.30.1", "workspace_drift", "")
		}})
	if err != nil {
		t.Fatal(err)
	}
	r := sliceReport(t, j, static, record.DefaultMaxCalls, &exampleIDs{n: 0x402})
	if r.Secrets == nil || r.Secrets.ModelInput != report.ScreenNotScreened || r.Coverage.AIReview.Status != report.ModelPerformed {
		t.Fatalf("secrets %+v ai review %+v", r.Secrets, r.Coverage.AIReview)
	}
	reviewed := 0
	for _, task := range r.Tasks {
		if !strings.HasPrefix(task.Key, "review.") {
			continue
		}
		if task.Status == string(domain.TaskSkippedPolicy) && deref(task.Reason) == "secret_screen_failed" {
			t.Fatalf("%s skipped: %s", task.Key, deref(task.Reason))
		}
		if task.Status == string(domain.TaskSucceeded) {
			reviewed++
		}
	}
	if l := r.CodeReview.Ledger; reviewed < 3 || l == nil || l.Scope.Result != "accepted" {
		t.Fatalf("reviewed %d ledger %+v", reviewed, r.CodeReview.Ledger)
	}
	gaps := map[string]bool{}
	for _, g := range r.Gaps {
		gaps[g.Code] = true
	}
	if !gaps["model_input_not_screened"] {
		t.Fatalf("gaps %+v", r.Gaps)
	}
	md := string(report.RenderMarkdown(r, strings.Repeat("0", 64), report.Korean))
	if !strings.Contains(md, "탐지 작업 gitl"+"eaks의 결과를 쓸 수 없어") || !strings.Contains(md, "기본 읽기 필터") || strings.Contains(md, "탐지 작업이 실행되지 않아") {
		t.Fatal("the secrets section does not name the unusable scan")
	}
	for _, name := range []string{"investigate", "verify", "investigate.s001"} {
		rec, err := os.ReadFile(recordPath(j, name))
		if err != nil || !strings.Contains(string(rec), limNotSecretScreened) {
			t.Fatalf("%s record: %v", name, err)
		}
	}
	writeOrCompare(t, r, "../../report/testdata/report-screen-unusable.json")
}

// TC-169: beyond the 96 slots the files are counted per module as not
// planned, shown as a gap, and the report validates.
func TestReportSlicesNotPlanned(t *testing.T) {
	t.Parallel()
	tree := map[string]string{"go.mod": "module sample.test/big\n\ngo 1.23\n", "README.md": "# Big\n"}
	for i := range 600 {
		tree[fmt.Sprintf("p/f%03d.go", i)] = "package p\n\n" + strings.Repeat("// filler line for the slice bound\n", 80)
	}
	j := treeJob(t, tree)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000801", "0195f3a0-0000-7000-8000-000000000802"
	r := sliceReport(t, j, sliceExampleStatic(t), 15, &exampleIDs{n: 0x302})
	l := r.CodeReview.Ledger
	if l == nil || len(l.Modules) != 1 || l.Modules[0].FilesNotPlanned == 0 || l.Modules[0].FilesBudgetExhausted == 0 || l.Status != report.LedgerPartial {
		t.Fatalf("ledger %+v", l)
	}
	found := false
	for _, g := range r.Gaps {
		found = found || (g.Code == "review_scope_not_planned" && deref(g.Subject) == l.Modules[0].ModuleID)
	}
	if !found {
		t.Fatalf("gaps %+v", r.Gaps)
	}
	if _, err := report.Marshal(r); err != nil {
		t.Fatal(err)
	}
}

// The line ledger follows the investigation records of a complete synthetic
// worker run. A file already represented in the overview still gets its tail.
func TestReportLineCoverageAcrossModules(t *testing.T) {
	tree := map[string]string{"README.md": "# Review catalogue\n\nEach module publishes a numbered entry.\n"}
	for i := range 67 {
		module := fmt.Sprintf("module%02d", i%12)
		tree[module+"/go.mod"] = "module sample.test/" + module + "\n\ngo 1.23\n"
		tree[fmt.Sprintf("%s/f%03d.go", module, i)] = fmt.Sprintf("package catalogue\n\nfunc Entry%03d() int { return %d }\n", i, i)
	}
	const longFile = "module00/f000.go"
	const lineLimitFile = "module01/f001.go"
	tree[longFile] = "package catalogue\n" + strings.Repeat("// module catalogue entry\n", 108) + "func FinalEntry() int { return 108 }\n"
	tree[lineLimitFile] = "package catalogue\n//" + strings.Repeat("x", 2500) + "\nfunc AfterLongLine() int { return 3 }\n"
	j := treeJob(t, tree)
	const tailCandidate = "마지막 항목이 다른 값을 반환"
	script := coverageScript(t)
	base := script["investigate"]
	script["investigate"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if e.Path == longFile && strings.Contains(e.Text, "func FinalEntry()") {
				return asJSON(map[string]any{"candidates": []record.CandidateText{{
					Title: tailCandidate, Condition: "마지막 항목을 읽는다.", Mechanism: "반환값이 마지막 번호로 고정된다.", Impact: "항목 번호가 달라진다.",
					Improvement: "마지막 항목의 반환값을 확인한다.", Counterpoint: "별도의 허용 번호가 지정될 수 있다.", Evidence: []string{e.ID},
				}}})
			}
		}
		return base(n, q)
	}
	r := sliceReportWithCaller(t, j, secretsStub{}, 22, &exampleIDs{n: 0x402}, &scriptedCaller{script: script})
	l := r.CodeReview.Ledger
	if l == nil || len(l.Modules) != 12 || len(l.Files) != 67 || l.CallsUsed == nil || *l.CallsUsed > 22 {
		t.Fatalf("coverage ledger missing or over budget: %+v", l)
	}
	complete, exposed := 0, 0
	for _, f := range l.Files {
		if f.SentLines > 0 {
			exposed++
		}
		if f.ReviewedLines == f.TotalLines {
			complete++
		}
		if f.Path == longFile && (f.TotalLines != 110 || f.ReviewedLines != 110) {
			t.Fatalf("long file tail not reviewed: %+v", f)
		}
		if f.Path == lineLimitFile && (f.SentLines != 2 || f.ReviewedLines != 2 || !slices.Equal(f.Remaining, []report.LedgerRange{{StartLine: 2, EndLine: 2, Reason: record.ScopeReasonLineLimit}})) {
			t.Fatalf("long single line hid following source: %+v", f)
		}
	}
	t.Logf("synthetic corpus: %d/%d files represented, %d fully covered, %d/%d calls", exposed, len(l.Files), complete, *l.CallsUsed, *l.MaxCalls)
	if exposed != 67 || complete != 66 || l.Status != report.LedgerPartial {
		t.Fatalf("coverage %d represented, %d complete, ledger %s", exposed, complete, l.Status)
	}
	if !slices.ContainsFunc(r.Findings, func(f report.Finding) bool {
		return f.Title == tailCandidate && slices.ContainsFunc(f.Evidence, func(e report.Evidence) bool {
			return e.Location != nil && e.Location.Path == longFile && e.Location.EndLine == 110
		})
	}) {
		t.Fatal("report lost the scripted candidate and its source range at the file tail")
	}
	if _, err := report.Marshal(r); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageVisitsSixtySevenModulesWithoutInventingCandidateVerification(t *testing.T) {
	t.Parallel()
	const modules = 67
	tree := map[string]string{}
	for i := range modules {
		module := fmt.Sprintf("module%02d", i)
		dir := module
		if i >= 60 {
			dir = "examples/" + module
		}
		tree[dir+"/go.mod"] = "module sample.test/" + module + "\n\ngo 1.23\n"
		tree[dir+"/entry.go"] = fmt.Sprintf("package catalogue\n\nfunc Hold%02d() int { return %d }\n", i, i)
	}
	for i := range 110 {
		tree[fmt.Sprintf("module00/extra%03d.go", i)] = "package catalogue\n" + strings.Repeat("// implementation context for the first module\n", 100)
	}
	j := treeJob(t, tree)
	script := coverageScript(t)
	sent := map[string]bool{}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if strings.Contains(e.Text, "func Hold") {
				sent[e.Path] = true
			}
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	caller := &scriptedCaller{script: script}
	r := sliceReportWithCaller(t, j, secretsStub{}, record.DefaultMaxCalls, &exampleIDs{n: 0x502}, caller)
	l := r.CodeReview.Ledger
	if l == nil || len(l.Modules) != modules || l.CallsUsed == nil || *l.CallsUsed > record.DefaultMaxCalls || len(sent) != modules || caller.calls("verify") != 0 {
		t.Fatalf("ledger missing or over budget, real representative snippets %d/%d, verify calls %d", len(sent), modules, caller.calls("verify"))
	}
	for _, m := range l.Modules {
		if m.FilesRequested == nil || *m.FilesRequested == 0 || m.FilesAccepted == nil || *m.FilesAccepted == 0 || m.FilesWithCompletedReview != 0 {
			t.Fatalf("module has no accepted source or overstates candidate verification: %+v", m)
		}
	}
	t.Logf("native worker and report builder recorded real source snippets for %d/%d modules in %d/%d calls; zero candidate verification calls", len(sent), modules, *l.CallsUsed, *l.MaxCalls)
}

func TestCoverageRecordsRejectedRequestAndReschedulesItsLines(t *testing.T) {
	j := treeJob(t, map[string]string{
		"go.mod":  "module sample.test/catalogue\n\ngo 1.23\n",
		"main.go": "package main\n\nfunc Allow() bool { return true }\n",
	})
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		return opencode.Reply{Reason: "model_unavailable"}
	}
	r := sliceReportWithCaller(t, j, secretsStub{}, record.DefaultMaxCalls, &exampleIDs{n: 0x602}, &scriptedCaller{script: script})
	l := r.CodeReview.Ledger
	if l == nil || len(l.Modules) != 1 || len(l.Files) != 1 || *l.Modules[0].FilesRequested != 1 || *l.Modules[0].FilesAccepted != 0 {
		t.Fatalf("request vs accepted coverage: %+v", l)
	}
	f := l.Files[0]
	if f.Investigation != "requested_without_accepted_response" || f.SentLines != 3 || f.ReviewedLines != 0 ||
		!slices.Equal(f.Remaining, []report.LedgerRange{{StartLine: 1, EndLine: 3, Reason: "requested_without_accepted_response"}}) {
		t.Fatalf("rejected investigation treated as accepted or counted twice: %+v", f)
	}
	if l.CallsUsed == nil || !slices.ContainsFunc(r.Coverage.ReviewCalls, func(c report.ReviewCall) bool { return c.TaskKey == "review.investigate.s001" }) {
		t.Fatalf("failed overview input was not retried in a slice (ledger %+v)", l)
	}
}

func TestInvestigationKeepsCandidateWhenStrengthCitesOnlyImportAndComment(t *testing.T) {
	j := treeJob(t, map[string]string{
		"go.mod":     "module sample.test/catalogue\n\ngo 1.23\n",
		"main.go":    "package main\n\nfunc Allow() bool { return true }\n",
		"imports.go": "package main\n\nimport \"fmt\"\n\n// return success for every route\n",
	})
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		main := excerptID(in.Scope.Excerpts, "main.go")
		imports := excerptID(in.Scope.Excerpts, "imports.go")
		return asJSON(map[string]any{
			"candidates": []record.CandidateText{{Title: "조건 없이 성공하는 반환", Condition: "Allow를 직접 호출한다.", Mechanism: "Allow가 입력과 무관하게 참을 돌려준다.",
				Impact: "부정 입력을 별도로 확인해야 한다.", Improvement: "호출자가 전달한 상태를 확인한다.",
				Counterpoint: "모든 호출자가 이미 상태를 확인할 수 있다.", Evidence: []string{main}}},
			"strengths": []record.Strength{{Title: "라우트 응답 검사", Benefit: "응답 누락을 방지한다.", Scope: ".", EvidenceIDs: []string{imports}}},
		})
	}
	caller := &scriptedCaller{script: script}
	pinExampleSnapshot(t, &j)
	r := sliceReportWithCaller(t, j, secretsStub{}, record.DefaultMaxCalls, &exampleIDs{n: 0x702}, caller)
	rec := readRecord(t, j, record.StageInvestigate)
	if !rec.Accepted() || len(rec.Calls) != 1 || len(rec.Candidates) != 1 || len(rec.Strengths) != 0 ||
		!slices.Contains(rec.Limitations, "review_strength_omitted") || len(r.CodeReview.FindingIDs) != 1 {
		t.Fatalf("candidate or optional evidence status differs: %+v, finding IDs %v", rec.Candidates, r.CodeReview.FindingIDs)
	}
}

func TestExtendedCoverageReadsPastOrdinarySlotLimitOnRetry(t *testing.T) {
	t.Parallel()
	const tail = "late.go"
	const marker = "return catalogueSize + 1"
	tree := map[string]string{"go.mod": "module sample.test/catalogue\n\ngo 1.23\n"}
	for i := range 800 {
		// Each short file is one unit: more files than the ordinary slots
		// hold at the smallest budget's eight units per slot.
		tree[fmt.Sprintf("file%03d.go", i)] = fmt.Sprintf("package catalogue\nfunc Entry%03d() int {\nreturn %d\n}\n", i, i)
	}
	tree[tail] = "package catalogue\nfunc Last(catalogueSize int) int {\n" + marker + "\n}\n"
	j := treeJob(t, tree)
	j.Scope = domain.Scope{Kind: "full", ReviewCoverage: domain.ReviewCoverageExtended}
	pinExampleSnapshot(t, &j)

	normal := j
	normal.Scope.ReviewCoverage = ""
	normal.Snapshot.SnapshotDir = "" // Standard planning only requires the manifest.
	if got := (Handler{MaxCalls: 600}).maxCalls(normal.Scope); got != record.LegacyMaxCalls {
		t.Fatalf("ordinary review inherited the extended operator limit: %d", got)
	}
	noModel, err := Handler{Static: secretsStub{}}.Plan(t.Context(), normal)
	if err != nil || len(sliceKeys(noModel)) > record.LegacyMaxSlices {
		t.Fatalf("ordinary plan with no model allocated %d pairs: %v", len(sliceKeys(noModel)), err)
	}

	script := coverageScript(t)
	script["investigate"] = func(_ int, _ opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"candidates": []any{}})
	}
	originalCaller := &scriptedCaller{script: script}
	original := sliceReportWithCaller(t, j, secretsStub{}, record.LegacyMaxCalls, &exampleIDs{n: 0x802}, originalCaller)
	j.AttemptID, j.Scratch = domain.NewID(), t.TempDir()
	retryCaller := &scriptedCaller{script: script}
	retried := sliceReportWithCaller(t, j, secretsStub{}, 600, &exampleIDs{n: 0x902}, retryCaller)
	first, refreshed := original.CodeReview.Ledger, retried.CodeReview.Ledger
	if first == nil || refreshed == nil || !first.ExtendedCoverage || !refreshed.ExtendedCoverage ||
		first.Status == report.LedgerComplete || refreshed.Status != report.LedgerComplete ||
		refreshed.CallsUsed == nil || *refreshed.CallsUsed > 600 || retryCaller.calls(record.StageInvestigate) <= record.LegacyMaxSlices {
		t.Fatalf("unexpected coverage status: ordinary calls %d, extended calls %d", len(originalCaller.requests), len(retryCaller.requests))
	}
	find := func(files []report.LedgerFile) report.LedgerFile {
		return files[slices.IndexFunc(files, func(f report.LedgerFile) bool { return f.Path == tail })]
	}
	before, after := find(first.Files), find(refreshed.Files)
	if !slices.ContainsFunc(before.Remaining, func(r report.LedgerRange) bool { return r.Reason == record.ScopeReasonSlotLimit }) ||
		len(after.Remaining) != 0 || after.ReviewedLines != after.TotalLines ||
		!slices.ContainsFunc(retryCaller.requests, func(q opencode.Request) bool { return strings.Contains(string(q.Input), marker) }) {
		t.Fatalf("extended review did not reach the late source: before %+v, after %+v", before, after)
	}
	keys := func(r report.Report) []string {
		var keys []string
		for _, task := range r.Tasks {
			if _, _, ok := record.SliceTask(task.Key); ok {
				keys = append(keys, task.Key)
			}
		}
		return keys
	}
	if !slices.Equal(keys(original), keys(retried)) || original.Inputs.ManifestSHA256 != retried.Inputs.ManifestSHA256 {
		t.Fatal("retry changed the pinned plan keys or snapshot")
	}
	t.Logf("pinned snapshot: %d ordinary vs %d extended investigation requests; file tail advanced from %d/%d to %d/%d accepted source lines",
		originalCaller.calls(record.StageInvestigate), retryCaller.calls(record.StageInvestigate), before.ReviewedLines, before.TotalLines, after.ReviewedLines, after.TotalLines)
}

// writeOrCompare stores r (demo) at p, or compares with the stored file.
func writeOrCompare(t *testing.T, r report.Report, p string) {
	t.Helper()
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"demo":false`), []byte(`"demo":true`), 1)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	compareStoredExample(t, p, pretty.Bytes())
}

// sliceExampleStatic is go vet per Go module (clean) and a secrets scan
// without detections, so the slices run after a completed screen. The
// Python and JavaScript modules have no member: their pack gaps stay
// visible and the report is partial.
func sliceExampleStatic(t *testing.T) *pack.Composite {
	t.Helper()
	gl := gitleaks.Descriptor()
	c, err := pack.NewComposite(
		exampleMember{d: govet.Descriptor(), plan: govet.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult { return exampleVet(t, j, task) }},
		exampleMember{d: gl, plan: gitleaks.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			rp := obsdoc.RulePack{ID: "codeye-secrets-gitl" + "eaks", Version: "1", Selection: []string{"config=extend.useDefault"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
				SourceSuppressions: obsdoc.SuppressDisabled, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
			var processed []string
			for _, e := range j.Snapshot.Manifest.Entries {
				if e.Kind == "file" && e.ExcludedReason == "" {
					processed = append(processed, e.Path)
				}
			}
			return exampleEnvelope(t, j, gl, task, "8.30.1", rp, processed, []string{"history_not_scanned", "secret_columns_not_recorded", "secret_values_withheld"}, nil)
		}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// checkSliceExample fails unless the report read every slice artifact and
// its ledger tells the admitted, budget-exhausted and completed scope with
// numbers that follow from the run.
func checkSliceExample(t *testing.T, r report.Report, calls int) {
	t.Helper()
	for _, l := range r.Limitations {
		if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, "artifact_rejected") ||
			strings.HasPrefix(l, "unrecognized_artifact") || strings.HasPrefix(l, "result_manifest") {
			t.Fatalf("limitation %s in %v", l, r.Limitations)
		}
	}
	if r.Coverage.AIReview.Status != report.AIReviewPerformed {
		t.Fatalf("status %s ai review %+v", r.Status, r.Coverage.AIReview)
	}
	cr := r.CodeReview
	if cr == nil || cr.Ledger == nil {
		t.Fatal("no ledger")
	}
	l := cr.Ledger
	if l.Status != report.LedgerPartial || l.Scope.Result != "accepted" || l.MaxCalls == nil || *l.MaxCalls != sliceExampleCap ||
		l.CallsUsed == nil || *l.CallsUsed != calls || calls > sliceExampleCap || l.RequestBytesSent == nil || *l.RequestBytesSent != *cr.RequestBytes {
		t.Fatalf("ledger header %+v (calls %d)", l, calls)
	}
	completed, sent := 0, 0
	for _, m := range l.Modules {
		completed += m.FilesWithCompletedReview
		sent += m.FilesSentToModel
	}
	if l.PrimarySlotsBudgetExhausted == 0 || completed == 0 || sent == 0 || *r.Coverage.LLMExposedFiles <= 8 ||
		!slices.ContainsFunc(l.Files, func(f report.LedgerFile) bool {
			return slices.ContainsFunc(f.Remaining, func(r report.LedgerRange) bool { return r.Reason == reasonBudgetExhausted })
		}) {
		t.Fatalf("ledger rows %+v exposed %d", l.Modules, *r.Coverage.LLMExposedFiles)
	}
	slice := 0
	for _, f := range r.Findings {
		if strings.HasPrefix(f.ID, "review_s001_") {
			slice++
		}
	}
	keys := map[string]bool{}
	for _, c := range r.Coverage.ReviewCalls {
		keys[c.TaskKey] = true
	}
	if slice == 0 || !keys["review.investigate.s001"] || !keys["review.verify.s001"] {
		t.Fatalf("slice findings %d, call tasks %v", slice, keys)
	}
	for _, task := range r.Tasks {
		if stage, _, ok := record.SliceTask(task.Key); ok && stage == record.StageInvestigate && task.Status == string(domain.TaskBudgetExhausted) && deref(task.Reason) != "review_budget_exhausted" {
			t.Fatalf("slice %s reason %v", task.Key, task.Reason)
		}
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
