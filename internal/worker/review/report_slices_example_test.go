package review

import (
	"bytes"
	"encoding/json"
	"os"
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
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The review slice report example (examples/report-slices.json and .md,
// docs/REPORT.md#모듈별-검토-장부) is the builder's output for
// fixtures/modules/coverage/input under the smallest call ceiling (15): the
// real discovery and role-pack handlers run in process with a scripted
// model caller that answers every task with one call (no model is
// contacted), a clean synthetic secrets screen and a go vet stand-in without
// diagnostics. Three slices are admitted and the other four end
// budget_exhausted, so the ledger shows sent, budget-exhausted and completed
// files per module. CODEYE_UPDATE_EXAMPLES=1 rewrites both files.
const (
	sliceExampleJSON = "../../../examples/report-slices.json"
	sliceExampleMD   = "../../../examples/report-slices.md"
	sliceExampleCap  = 15
)

const sliceExampleBanner = "> **합성 예시.** 아래는 `fixtures/modules/coverage/input`에 대해 CODEYE의 탐색·검토 역할과 모듈 단위 검토 분할을 호출 상한 15로 실제로 실행하고 보고서 builder가 만든 출력이다. 모델 응답은 미리 정한 스크립트(작업마다 호출 1회)이며 모델을 부르지 않았다. 비밀값 탐지는 탐지 없는 합성 결과이고 정적 분석 도구는 실행하지 않았다. 사용자 프로젝트의 결함 발견 결과가 아니다. 기계 판독 원본은 [report-slices.json](report-slices.json)이다.\n"

// TC-169 (docs/REPORT.md#모듈별-검토-장부).
func TestReportSlicesExample(t *testing.T) {
	j := coverageJob(t)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000601", "0195f3a0-0000-7000-8000-000000000602"
	pinExampleSnapshot(t, &j)
	ids := &exampleIDs{n: 0x102}
	caller := &roleCaller{script: coverageScript(t)}
	h, err := discovery.Wrap(RoleHandler{Static: sliceExampleStatic(t), Caller: caller, MaxCalls: sliceExampleCap}, discovery.Config{Observer: exampleObserver{t: t}})
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
	md := string(report.RenderMarkdown(r, report.Digest(raw), report.Korean))
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
		have, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(have, b) {
			t.Fatalf("%s differs from the regenerated example; rerun with CODEYE_UPDATE_EXAMPLES=1 and review the diff", p)
		}
	}
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
		if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, report.LimReviewPackMismatch) || strings.HasPrefix(l, "artifact_rejected") ||
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
	exhausted, completed, sent, eligible := 0, 0, 0, 0
	for _, m := range l.Modules {
		exhausted += m.FilesBudgetExhausted
		completed += m.FilesWithCompletedReview
		sent += m.FilesSentToModel
		eligible += m.FilesEligible
	}
	if exhausted == 0 || completed == 0 || sent == 0 || sent >= eligible || *r.Coverage.LLMExposedFiles <= 8 {
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
	if slice == 0 || !keys["review.investigate.s001"] || !keys["review.verify.s003"] || keys["review.investigate.s004"] {
		t.Fatalf("slice findings %d, call tasks %v", slice, keys)
	}
	gaps := map[string]bool{}
	for _, g := range r.Gaps {
		gaps[g.Code] = true
	}
	if !gaps["review_scope_budget_exhausted"] {
		t.Fatalf("gaps %+v", r.Gaps)
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
