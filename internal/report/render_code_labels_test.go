package report

import (
	"os"
	"strings"
	"testing"
)

func readFlatExample(t *testing.T) Report {
	t.Helper()
	b, err := os.ReadFile("../../examples/report-go-vet.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.hasSections() {
		t.Fatal("the flat example has sections")
	}
	return r
}

// The flat layout words the reason, limitation and counter codes of a tool
// row, the task table and the AI review, and keeps the raw code beside the
// words. TC-329
func TestFlatRenderWordsCodesAndKeepsTheRawCode(t *testing.T) {
	r := readFlatExample(t)
	r.Coverage.Tools[0].Packages = map[string]int{"analyzed_with_findings": 2, "failed_typecheck": 1, "no_such_counter": 3}
	r.Coverage.Tools[0].Limitations = []string{"mypy_failed", "cgo_disabled", "no_such_limit"}
	r.Tasks[0].Reason = strPtr("tsconfig_invalid")
	r.Coverage.AIReview.RequiredTasks = []AIReviewTask{{Key: "review.verify", TaskStatus: strPtr("failed"), Result: "task_failed", Reason: strPtr("model_timeout")}}
	r.Coverage.AIReview.Reason = strPtr("required_task_not_succeeded")
	md := string(RenderMarkdown(r, strings.Repeat("0", 64), Korean))
	html, err := RenderHTML(r, strings.Repeat("0", 64), Korean)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| 단위 상태 | 한계 |",
		"분석 완료, 진단 있음\\: 2 `analyzed_with_findings`",
		"타입 검사 오류로 분석하지 못한 패키지 \\(실패\\)\\: 1 `failed_typecheck`",
		"`no_such_counter`",
		"mypy가 구문 오류가 아닌 오류\\(exit 2\\)로 끝나 결과를 만들지 못함 `mypy_failed`",
		"`no_such_limit`",
		"tsconfig를 불러오지 못했거나 옵션 오류가 있어 검사 결과를 쓰지 않음 \\(설정 오류\\) `tsconfig_invalid`",
		"모델 호출이 제한시간 안에 끝나지 않음",
		"`model_timeout`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	for _, want := range []string{
		"<th>단위 상태</th><th>한계</th>",
		`<span title="analyzed_with_findings">분석 완료, 진단 있음: 2</span>`,
		`<span title="mypy_failed">mypy가 구문 오류가 아닌 오류(exit 2)로 끝나 결과를 만들지 못함</span>`,
		`<code>no_such_limit</code>`,
		`<span title="tsconfig_invalid">`,
		`<span title="model_timeout">`,
		`<span title="required_task_not_succeeded">`,
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("html lacks %q", want)
		}
	}
	for _, raw := range []string{"analyzed_with_findings=2", "failed_typecheck=1"} {
		if strings.Contains(md, raw) || strings.Contains(string(html), raw) {
			t.Errorf("the raw key=count form %q is back", raw)
		}
	}
}

// The sectioned layout folds each tool row's limitations and counters in words
// under the row, and does not repeat the row's own task reason there. TC-329
func TestSectionedRenderFoldsToolCoverageNotes(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	md := string(RenderMarkdownAt(r, strings.Repeat("0", 64), Korean, DetailFull))
	html, err := RenderHTMLAt(r, strings.Repeat("0", 64), Korean, DetailFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<summary>범위 단위와 한계", "분석 완료, 진단 없음\\: 1 `analyzed_clean`"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	if !strings.Contains(string(html), "범위 단위와 한계") || !strings.Contains(string(html), "analyzed_clean") {
		t.Error("html lacks the coverage notes")
	}
	// A failed row says its reason once, in the outcome line.
	const reason = "노드의 toolchain이 없거나 모듈 요구보다 낮아 실행하지 못했습니다"
	for _, line := range strings.Split(md, "\n") {
		if strings.Contains(line, "go-vet.m02") && strings.Contains(line, reason) && strings.Count(line, reason) != 1 {
			t.Errorf("reason repeated in one line: %q", line)
		}
	}
	idx := strings.Index(md, "go-vet.m02")
	if idx < 0 {
		t.Fatal("no go-vet.m02 row")
	}
	row := md[idx:min(len(md), idx+700)]
	if end := strings.Index(row, "</details>"); end >= 0 {
		row = row[:end]
	}
	if strings.Count(row, reason) != 0 {
		t.Errorf("the failed row's reason is repeated in its notes fold: %q", row)
	}
}

// Every reason an unfinished run's task table, AI review lines and static rows
// show is words with the code beside them: a stored report with no Korean text
// for a reason code would show only the code.
func TestSectionedAppendixWordsTaskReasons(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	html, err := RenderHTMLAt(r, strings.Repeat("0", 64), Korean, DetailFull)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `<span title="toolchain_unavailable">`) {
		t.Error("the task table does not word toolchain_unavailable")
	}
}
