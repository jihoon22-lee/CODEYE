package report

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// actionScenario is the report example with the stored findings and
// comparisons a reader acts on: an excerpt for the finding to act on, a
// secondary-path finding, a candidate to check, and documents that disagree
// with the implementation.
func actionScenario(t *testing.T) Report {
	t.Helper()
	r := storedExample(t, "../../examples/report.json")
	for i := range r.Findings {
		f := &r.Findings[i]
		if f.ID != "review_interpret_1" {
			continue
		}
		f.Severity = "high"
		f.Conditions = []Condition{
			{Statement: "send가 두 번 이상 실패하는 경우", Status: "verified", EvidenceIDs: []string{f.Evidence[0].ID}},
			{Statement: "호출자가 반환 오류만 기록하는 경우", Status: "assumed", EvidenceIDs: nil},
		}
		f.Evidence[0].Excerpt = &sourceexcerpt.Excerpt{StartLine: 3, EndLine: 9, Text: "func Forward() error {\n\tvar err error\n\tfor i := 0; i < MaxRetries; i++ {\n\t\terr = send()\n\t}\n\treturn err\n}"}
		f.Limitations = []string{"호출자가 오류를 따로 기록하는지는 확인하지 못했다."}
		f.CounterevidenceIDs = []string{"ev_counter"}
		loc := f.Locations[0]
		f.Evidence = append(f.Evidence, Evidence{ID: "ev_counter", Kind: "code", Description: "호출자가 오류를 기록한다", Location: &loc})
		f.ObservationIDs = nil
		first := *f
		first.ID, first.Title, first.Severity, first.Validation = "review_second", "빈 응답을 성공으로 취급한다", "medium", ValidationReproduced
		low := *f
		low.ID, low.Title, low.Severity = "review_third", "로그에 요청 본문이 남는다", "low"
		secondary := *f
		secondary.ID, secondary.Title = "review_fixture", "예제 입력의 검증이 없다"
		secondaryLoc := Location{SnapshotID: loc.SnapshotID, Path: "fixtures/data.go", StartLine: 4, EndLine: 6}
		secondary.Locations = []Location{secondaryLoc}
		secondary.Evidence = []Evidence{{ID: "ev_fixture", Kind: "code", Location: &secondaryLoc}}
		secondary.Conditions, secondary.Claims, secondary.CounterevidenceIDs = nil, nil, nil
		pending := *f
		pending.ID, pending.Title, pending.Validation = "review_pending", "취소 신호를 확인하지 않을 수 있다", ValidationInconclusive
		pending.Verification = []string{"취소된 컨텍스트로 Forward를 호출해 반환 시간을 확인한다."}
		r.Findings = append(r.Findings, first, low, secondary, pending)
		r.CodeReview.FindingIDs = append(r.CodeReview.FindingIDs, first.ID, low.ID, secondary.ID, pending.ID)
		break
	}
	r.Documents.Comparisons = append(r.Documents.Comparisons,
		DocComparison{Status: "partial", Statement: "재시도 사이에 지연을 둔다.", AppliesTo: ".", Reason: "지연 없이 곧바로 다시 호출한다.",
			DocumentEvidenceIDs: r.Documents.Comparisons[0].DocumentEvidenceIDs, CodeEvidenceIDs: r.Documents.Comparisons[0].CodeEvidenceIDs},
		DocComparison{Status: "mismatch", Statement: "마지막 오류를 호출자에게 돌려준다.", AppliesTo: "svc/a", Reason: "`Forward`는 마지막이 아닌 첫 오류를 돌려준다.",
			DocumentEvidenceIDs: r.Documents.Comparisons[0].DocumentEvidenceIDs, CodeEvidenceIDs: r.Documents.Comparisons[0].CodeEvidenceIDs})
	return r
}

// exampleFile reads a stored example whether or not it is marked demo.
func exampleFile(t *testing.T, path string) Report {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func renderBoth(t *testing.T, r Report, detail string) (md, html string) {
	t.Helper()
	digest := strings.Repeat("0", 64)
	h, err := RenderHTMLAt(r, digest, Korean, detail)
	if err != nil {
		t.Fatal(err)
	}
	checkInPageLinks(t, h)
	md = string(RenderMarkdownAt(r, digest, Korean, detail))
	checkMarkdownLinks(t, md)
	if strings.Contains(md, "%!") || strings.Contains(string(h), "%!") {
		t.Fatal("format error")
	}
	return md, string(h)
}

var markdownAnchor = regexp.MustCompile(`<a id="([^"]+)"></a>`)
var markdownLink = regexp.MustCompile(`\]\(#([^)]+)\)`)

// checkMarkdownLinks fails for an in-page link without an anchor.
func checkMarkdownLinks(t *testing.T, md string) {
	t.Helper()
	ids := map[string]bool{}
	for _, m := range markdownAnchor.FindAllStringSubmatch(md, -1) {
		ids[m[1]] = true
	}
	for _, m := range markdownLink.FindAllStringSubmatch(md, -1) {
		if !ids[m[1]] {
			t.Fatalf("markdown link to #%s without an anchor", m[1])
		}
	}
}

// The action cards come first, most severe first, and carry what a reader
// needs to fix and check: when, impact, code, fix, check, state and where
// the full evidence is.
func TestActionCardsLeadAndCarryTheFixAndTheCheck(t *testing.T) {
	r := actionScenario(t)
	for _, detail := range []string{DetailSummary, DetailFull} {
		md, html := renderBoth(t, r, detail)
		for name, out := range map[string]string{"markdown": md, "html": html} {
			at := func(s string) int {
				i := strings.Index(out, s)
				if i < 0 {
					t.Fatalf("%s %s lacks %q", detail, name, s)
				}
				return i
			}
			cards := []string{"재시도 반복에서 앞선 시도의 오류를 덮어씀", "빈 응답을 성공으로 취급한다", "로그에 요청 본문이 남는다"}
			// high, then reproduced medium, then low: the stored order differs.
			if !(at(cards[0]) < at(cards[1]) && at(cards[1]) < at(cards[2])) {
				t.Fatalf("%s %s: cards are not most severe first", detail, name)
			}
			environment := `<a id="environment"></a>`
			if name == "html" {
				environment = `id="environment"`
			}
			table := Korean.Label("reading.outcome")
			for _, later := range []string{table, Korean.Label("opening.coverage_details"), environment} {
				if later == Korean.Label("opening.coverage_details") && detail == DetailFull && name == "markdown" {
					continue // the full layout has no such block
				}
				if at(cards[2]) > at(later) {
					t.Fatalf("%s %s: %q comes before the action cards", detail, name, later)
				}
			}
			for _, want := range []string{
				Korean.Label("opening.condition.verified"), "send가 두 번 이상 실패하는 경우", Korean.Label("opening.condition.assumed"),
				Korean.Label("opening.impact"), "func Forward() error", Korean.Label("opening.recommendation"), "errors", Korean.Label("opening.verification"),
				"근거 있음", "실행으로 재현", "호출자가 오류를 따로 기록하는지는 확인하지 못했다",
				"근거와 반대 근거 보기", "반대 근거 1건",
			} {
				if !strings.Contains(out, want) && !strings.Contains(out, mdText(want)) {
					t.Fatalf("%s %s: the card lacks %q", detail, name, want)
				}
			}
			// The secondary-path finding and the candidate are not action cards.
			if strings.Contains(out[:at(Korean.Label("reading.outcome"))], "예제 입력의 검증이 없다") &&
				strings.Index(out, "예제 입력의 검증이 없다") < strings.Index(out, Korean.Label("actions.secondary")[:len("예제·시험 입력 등에서 발견한 항목")]) {
				t.Fatalf("%s %s: a secondary-path finding is shown as an action", detail, name)
			}
		}
	}
}

// The number of cards is the number the overview row already prints.
func TestActionCardCountMatchesTheOverviewRow(t *testing.T) {
	for _, r := range []Report{storedExample(t, "../../examples/report.json"), actionScenario(t)} {
		v := withInsights(newSecView(r, strings.Repeat("0", 64), Korean))
		want := ""
		for _, row := range v.ReadingOverview() {
			if row.Section == SectionCodeReview && strings.Contains(row.Value, "조치") {
				want = row.Count
			}
		}
		if got := strconv.Itoa(len(v.Actions.Cards)); got != want {
			t.Fatalf("cards %s, overview count %q", got, want)
		}
	}
}

// What the reader sees when there is nothing to act on depends on whether a
// review was accepted: missing, failed or partial work is never an empty list.
func TestActionOpeningNeverReadsMissingWorkAsNothingToDo(t *testing.T) {
	accepted := exampleFile(t, "../../examples/report-slices.json")
	pull := exampleFile(t, "../../examples/report-pull-request.json")
	failed := accepted
	failed.Status = StatusFailed
	for name, row := range map[string]struct {
		r          Report
		want, deny []string
	}{
		"accepted review without a finding to act on": {accepted,
			[]string{Korean.Label("review.reading_none"), Korean.Label("actions.none_not_a_proof"), "모델에 보냈다"}, []string{Korean.Label("actions.not_assessed")}},
		"review not performed": {pull,
			[]string{Korean.Label("actions.not_assessed"), Korean.Label("opening.model.not_performed")}, []string{Korean.Label("review.reading_none")}},
		"failed run": {failed,
			[]string{Korean.Label("actions.not_assessed")}, []string{Korean.Label("review.reading_none")}},
	} {
		for _, detail := range []string{DetailSummary, DetailFull} {
			md, html := renderBoth(t, row.r, detail)
			for format, out := range map[string]string{"markdown": md, "html": html} {
				opening := out[:strings.Index(out, Korean.Label("reading.outcome"))]
				for _, want := range row.want {
					if !strings.Contains(opening, want) && !strings.Contains(opening, mdText(want)) {
						t.Errorf("%s %s %s: the opening lacks %q", name, detail, format, want)
					}
				}
				for _, deny := range row.deny {
					if strings.Contains(opening, deny) || strings.Contains(opening, mdText(deny)) {
						t.Errorf("%s %s %s: the opening says %q", name, detail, format, deny)
					}
				}
			}
		}
	}
}

// Missing, failed or unsupported work stays in a line under the verdict that
// links to the status table, which comes after the action items.
func TestCoverageLineLinksToTheStatusTable(t *testing.T) {
	r := actionScenario(t)
	md, html := renderBoth(t, r, DetailSummary)
	for name, out := range map[string]string{"markdown": md, "html": html} {
		line := strings.Index(out, Korean.Label("actions.coverage_lead"))
		cards := strings.Index(out, "재시도 반복에서 앞선 시도의 오류를 덮어씀")
		table := strings.Index(out, Korean.Label("reading.outcome"))
		if line < 0 || line > cards || cards > table {
			t.Fatalf("%s: coverage line %d, cards %d, status table %d", name, line, cards, table)
		}
	}
	if !strings.Contains(html, `href="#coverage"`) || !strings.Contains(html, `id="coverage"`) {
		t.Fatal("the coverage line does not link to the status table")
	}
	complete := r
	complete.Overview = &Overview{Sections: r.Overview.Sections}
	complete.Coverage.AIReview.Status = AIReviewPerformed
	if _, html := renderBoth(t, complete, DetailSummary); strings.Contains(html, `<p class="report-coverage-note"`) {
		t.Fatal("a report with nothing unanalysed shows a coverage line")
	}
}

// Documents that disagree with the implementation come after the action
// cards, mismatches first, with the promise, the observation and both places.
func TestDocumentMismatchCards(t *testing.T) {
	r := actionScenario(t)
	md, html := renderBoth(t, r, DetailSummary)
	for name, out := range map[string]string{"markdown": md, "html": html} {
		mismatch := strings.Index(out, "마지막 오류를 호출자에게 돌려준다")
		partial := strings.Index(out, "재시도 사이에 지연을 둔다")
		cards := strings.Index(out, "로그에 요청 본문이 남는다")
		if !(cards < mismatch && mismatch < partial) {
			t.Fatalf("%s: mismatch %d, partial %d, cards %d", name, mismatch, partial, cards)
		}
		for _, want := range []string{Korean.Label("decision.comparison.mismatch"), Korean.Label("decision.comparison.partial"),
			Korean.Label("actions.observed"), "README.md:1-9", "gateway.go:1-16", Korean.Label("insight.interpretation"), "document-comparison-"} {
			if !strings.Contains(out, want) && !strings.Contains(out, mdText(want)) {
				t.Fatalf("%s lacks %q", name, want)
			}
		}
	}
	// Past the visible limit the cards fold; none is dropped.
	for len(r.Documents.Comparisons) < 12 {
		r.Documents.Comparisons = append(r.Documents.Comparisons, r.Documents.Comparisons[len(r.Documents.Comparisons)-1])
	}
	opening := withInsights(newSecView(r, strings.Repeat("0", 64), Korean)).Actions
	total := 0
	for _, c := range r.Documents.Comparisons {
		if c.Status == "mismatch" || c.Status == "partial" {
			total++
		}
	}
	if len(opening.Mismatches) != mismatchCardLimit || len(opening.Mismatches)+len(opening.MismatchMore) != total {
		t.Fatalf("shown %d, folded %d, stored %d", len(opening.Mismatches), len(opening.MismatchMore), total)
	}
}

// Candidates and tool diagnostics are collapsed, the diagnostics grouped by
// analyzer and rule with the count, the description and the places.
func TestCandidatesAndDiagnosticsAreCollapsedAndGrouped(t *testing.T) {
	r := actionScenario(t)
	md, html := renderBoth(t, r, DetailSummary)
	for _, want := range []string{`<details class="card report-disclosure" id="candidates">`, `<details class="card report-disclosure" id="tool-diagnostics">`,
		`<details class="card report-disclosure" id="secondary-findings">`} {
		if !strings.Contains(html, want) {
			t.Fatalf("html lacks the collapsed block %s", want)
		}
	}
	for _, want := range []string{"취소 신호를 확인하지 않을 수 있다", "취소된 컨텍스트로 Forward를 호출해 반환 시간을 확인한다", "go.staticcheck", "SA4006", "gateway.go:13-13",
		"this value of err is overwritten"} {
		if !strings.Contains(html, want) || !strings.Contains(md, mdText(want)) && !strings.Contains(md, want) {
			t.Fatalf("a collapsed block lacks %q", want)
		}
	}
	// The candidate is in the candidate block and not an action card.
	if opening := html[:strings.Index(html, `id="candidates"`)]; strings.Contains(opening, "취소 신호를 확인하지 않을 수 있다") {
		t.Fatal("a candidate is shown before the collapsed candidate block")
	}
	// The flat list of generic one-liners is replaced by one group per rule.
	if n := strings.Count(html, `class="report-diag-group"`); n != 2 {
		t.Fatalf("%d diagnostic groups, want 2", n)
	}
}

// A code-free export keeps locations and states and says once that the text
// is omitted; it neither shows code nor repeats the omission on every card.
func TestCodeFreeActionCards(t *testing.T) {
	r := Project(actionScenario(t), false)
	md, html := renderBoth(t, r, DetailSummary)
	for name, out := range map[string]string{"markdown": md, "html": html} {
		for _, hidden := range []string{"func Forward() error", "send가 두 번 이상 실패하는 경우", "시도마다 오류를 기록하거나"} {
			if strings.Contains(out, hidden) || strings.Contains(out, mdText(hidden)) {
				t.Fatalf("%s: a code-free export shows %q", name, hidden)
			}
		}
		for _, want := range []string{"gateway.go:3-16", "근거 있음", Korean.Label("actions.lead_code_free")} {
			if !strings.Contains(out, want) && !strings.Contains(out, mdText(want)) {
				t.Fatalf("%s: a code-free export lacks %q", name, want)
			}
		}
		opening := out[:strings.Index(out, Korean.Label("reading.outcome"))]
		if strings.Contains(opening, Korean.Label("actions.observed")) {
			t.Fatalf("%s: a code-free mismatch card keeps an empty observation row", name)
		}
		if n := strings.Count(opening, textOmitted); n > 0 {
			t.Fatalf("%s: %d unreplaced omission marker(s) in the opening", name, n)
		}
	}
}

// A partial review that found nothing to act on says so inside the block, and
// a fold summary shows no backslash escapes in Markdown (it is raw HTML).
func TestPartialReviewWithoutCardsAndFoldSummaries(t *testing.T) {
	r := exampleFile(t, "../../examples/report-slices.json")
	r.Coverage.AIReview.Status = AIReviewPartial
	md, html := renderBoth(t, r, DetailSummary)
	for name, out := range map[string]string{"markdown": md, "html": html} {
		if !strings.Contains(out, Korean.Label("actions.none_partial")) && !strings.Contains(out, mdText(Korean.Label("actions.none_partial"))) {
			t.Fatalf("%s: a partial review without a finding does not say so", name)
		}
	}
	md, _ = renderBoth(t, actionScenario(t), DetailSummary)
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "<details><summary>도구 진단") || strings.HasPrefix(line, "<details><summary>go.staticcheck") {
			if strings.Contains(line, "\\") {
				t.Fatalf("a raw HTML summary line shows backslash escapes: %s", line)
			}
		}
	}
	if !strings.Contains(md, "<details><summary>도구 진단 2건 중 0건 판정&#40;문제·무해&#41;, 1건 판단 보류 &#40;규칙 2종, 도구 1종&#41;</summary>") {
		t.Fatal("the diagnostic fold summary is not entity-encoded")
	}
}
