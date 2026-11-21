package report

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestProjectBehaviorsRankStoredComparisonsBeforeTakingFour(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.Findings = nil
	r.CodeReview.FindingIDs = nil
	cite := func(id, kind, path, text string) SectionEvidence {
		end := strings.Count(text, "\n") + 1
		return SectionEvidence{ID: id, Kind: kind, Path: path, StartLine: 1, EndLine: end,
			Excerpt: &sourceexcerpt.Excerpt{StartLine: 1, EndLine: end, Text: text}}
	}
	r.Documents.Evidence = []SectionEvidence{
		cite("guide", "document", "README.md", "# Delivery\n\n## Features\n\nDeliver and calculate totals."),
		cite("notes", "document", "README.md", "## Development history\n\nIn this release, the total reflects all items."),
		cite("imports", "code", "src/overview.ts", "import { StatusReady } from './state';\nimport type { Options } from './options';"),
		cite("execution", "code", "src/totals.py", "    return total * multiplier"),
	}
	row := func(status, statement string, evidence ...string) DocComparison {
		return DocComparison{Status: status, Statement: statement, AppliesTo: ".", Reason: "저장한 대조 이유입니다.",
			DocumentEvidenceIDs: []string{"guide"}, CodeEvidenceIDs: evidence}
	}
	r.Documents.Comparisons = []DocComparison{
		row("unknown", "첫 약속은 가져오기 줄만 인용합니다.", "imports"),
		row("unknown", "두 번째 약속입니다."),
		row("unknown", "세 번째 약속입니다."),
		row("unknown", "네 번째 약속입니다."),
		row("unknown", "다섯 번째 동작은 합계를 반환합니다.", "execution"),
		row("unknown", "같은 수준의 후속 동작도 값을 반환합니다.", "execution"),
	}
	for i := 4; i <= 5; i++ {
		r.Documents.Comparisons[i].DocumentEvidenceIDs = []string{"notes"}
	}
	anchors := func(rows []projectBehavior) []string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.Anchor)
		}
		return out
	}
	if got := anchors(newSummaryView(r, "x", Korean).Behaviors.Rows); !slices.Equal(got, []string{
		"document-comparison-5", "document-comparison-6", "document-comparison-1", "document-comparison-2",
	}) {
		t.Fatalf("ordinary rows: %v", got)
	}
	r.Documents.Comparisons = append(r.Documents.Comparisons,
		row("match", "저장된 일치입니다.", "execution"),
		row("partial", "저장된 부분 일치입니다.", "execution"),
		row("mismatch", "저장된 불일치입니다.", "execution"),
		row("unknown", "릴리스 안내가 아직 확인되지 않았습니다.", "imports"),
		row("unknown", "배포 명령은 별도 옵션을 받습니다.", "execution"),
	)
	for i := 9; i <= 10; i++ {
		r.Documents.Comparisons[i].DocumentEvidenceIDs = []string{"notes"}
	}
	stored := slices.Clone(r.Documents.Comparisons)
	view := newSummaryView(r, "x", Korean)
	if view.Decision.ComparisonAnchor != "document-comparison-9" ||
		!slices.Equal(anchors(view.Behaviors.Rows), []string{
			"document-comparison-8", "document-comparison-7", "document-comparison-5", "document-comparison-6",
		}) ||
		!slices.EqualFunc(view.Behaviors.Rows, []string{"partial", "match", "unknown", "unknown"}, func(row projectBehavior, status string) bool {
			return row.Status == status
		}) {
		t.Fatalf("priority and behavior shelf: %s, %+v", view.Decision.ComparisonAnchor, view.Behaviors.Rows)
	}
	if next := newSummaryView(r, "x", Korean); !reflect.DeepEqual(view.Behaviors, next.Behaviors) ||
		!reflect.DeepEqual(r.Documents.Comparisons, stored) {
		t.Fatal("rendering changed the stored or rerendered order")
	}
	if rows := view.projectBehaviors().Rows; len(rows) != 9 {
		t.Fatalf("heading fallback admitted imports or operational guidance: %v", anchors(rows))
	}
	codeFree := Project(r, false)
	if view := newSummaryView(codeFree, "x", Korean); len(view.Behaviors.Rows) != 0 ||
		len(codeFree.Documents.Comparisons) != len(stored) ||
		!slices.EqualFunc(codeFree.Documents.Comparisons, stored, func(a, b DocComparison) bool {
			return a.Status == b.Status && slices.Equal(a.CodeEvidenceIDs, b.CodeEvidenceIDs)
		}) {
		t.Fatal("code-free projection lost recorded statuses or locations")
	}
}

func TestProjectBehaviorsRetainDocumentAndConditionalResponse(t *testing.T) {
	report := storedExample(t, "../../examples/report.json")
	cite := func(id, kind, path string, start int, text string) SectionEvidence {
		end := start + strings.Count(text, "\n")
		return SectionEvidence{ID: id, Kind: kind, Path: path, StartLine: start, EndLine: end,
			Excerpt: &sourceexcerpt.Excerpt{StartLine: start, EndLine: end, Text: text}}
	}
	usage := cite("readme-usage", "document", "README.md", 1, "# Parcel\n\nParcel delivers receipts to customers.\n\n## Features\n\nOpen a receipt.\n\n## Configuration\nThe receipt source may be changed.")
	module := cite("module-usage", "document", "receipt/README.md", 1, "# Receipts\n\nThe module saves a receipt copy.")
	params := cite("readme-params", "document", "README.md", 18, "## Parameters\n\nDatabase path defaults to /data.")
	branch := cite("lookup-branch", "code", "serve/receipts.go", 20, `return func(w http.ResponseWriter, r *http.Request) {
  receipt, err := service.store(r).Receipt(id)
  if _, ok := err.(store.UnknownReceipt); ok {
    http.Error(w, "receipt was not found", http.StatusNotFound)
    return
  } else if err != nil {
    return
  }
  print(receipt)
}`)
	config := cite("config", "code", "main.go", 10, `path := flag.String("db", "store", "storage location")`)
	reason := "링크를 만드는 경로는 확인하지 못했습니다."
	compared := func(state, claim, document, code string) DocComparison {
		return DocComparison{Status: state, Statement: claim, Reason: reason,
			AppliesTo: ".", DocumentEvidenceIDs: []string{document}, CodeEvidenceIDs: []string{code}}
	}
	report.Documents.Evidence = []SectionEvidence{usage, params, module, branch, config}
	report.Documents.Comparisons = []DocComparison{
		compared("match", "DB 경로를 생략할 수 있습니다.", params.ID, config.ID),
		compared("unknown", "영수증 링크를 열 수 있습니다.", usage.ID, branch.ID),
		compared("unknown", "영수증 사본을 저장할 수 있습니다.", module.ID, branch.ID),
	}
	additional := "요청한 항목이 없을 때의 응답 분기가 인용돼 있습니다."
	report.Documents.Comparisons[1].AdditionalObservation = &AdditionalCodeObservation{
		Reason: additional, CodeEvidenceIDs: []string{branch.ID},
	}
	report.Documents.Comparisons[2].AppliesTo = "receipt"
	for _, detail := range []string{DetailSummary, DetailFull} {
		markdown := string(RenderMarkdownAt(report, "x", Korean, detail))
		for _, anchor := range []string{`<a id="document-comparison-2"></a>`, `<a id="document-comparison-3"></a>`} {
			if !strings.Contains(markdown, anchor) {
				t.Fatalf("missing Markdown jump target %s", anchor)
			}
		}
		html, err := RenderHTMLAt(report, "x", Korean, detail)
		if err != nil {
			t.Fatal(err)
		}
		checkInPageLinks(t, html)
		_, header, _ := strings.Cut(string(html), `<header class="report-hero">`)
		hero, _, _ := strings.Cut(header, "</header>")
		if !strings.Contains(hero, `class="report-purpose"`) || !strings.Contains(hero, "Parcel delivers receipts to customers.") ||
			!strings.Contains(hero, "README.md:3") {
			t.Fatal("the project purpose is missing from the report header")
		}
		_, behaviorHTML, _ := strings.Cut(string(html), "프로젝트 사용 흐름")
		behaviorHTML, _, _ = strings.Cut(behaviorHTML, "</section>")
		if !strings.Contains(behaviorHTML, "인용 코드 전체 보기") || !strings.Contains(behaviorHTML, "print(receipt)") {
			t.Fatal("the full stored citation is missing from the behavior card")
		}
		for _, output := range []string{markdown, string(html)} {
			start := strings.Index(output, "프로젝트 사용 흐름")
			if start < 0 {
				t.Fatal("missing project behaviors")
			}
			view := output[start:]
			for _, want := range []string{
				"영수증 링크를 열 수 있습니다.", "영수증 사본을 저장할 수 있습니다.", "구현 미확인",
				"Receipt", "store.UnknownReceipt", "http.StatusNotFound", "serve/receipts.go:21-24",
				"#document-comparison-2", "#document-comparison-3", reason, additional,
			} {
				if !strings.Contains(view, want) && !strings.Contains(view, mdText(want)) {
					t.Fatalf("missing %q from the reader view", want)
				}
			}
			if strings.Index(view, "영수증 링크를 열 수 있습니다") > strings.Index(view, "DB 경로를 생략할 수 있습니다") {
				t.Fatal("operational config was shown before the user-facing flow")
			}
		}
		noCode := Project(report, false)
		html, err = RenderHTMLAt(noCode, "x", Korean, detail)
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range []string{string(RenderMarkdownAt(noCode, "x", Korean, detail)), string(html)} {
			if strings.Contains(output, "Parcel delivers receipts to customers.") || strings.Contains(output, "store.UnknownReceipt") ||
				strings.Contains(output, "프로젝트 사용 흐름") || !strings.Contains(output, "#document-comparison-1") {
				t.Fatal("code-free export did not retain only allowed comparison information")
			}
		}
	}
	if slices.ContainsFunc(newSummaryView(report, "x", Korean).Behaviors.Rows, func(row projectBehavior) bool {
		return row.Status != "unknown" || row.Claim == report.Documents.Comparisons[0].Statement
	}) {
		t.Fatal("highlighting a branch upgraded the documented claim or admitted config")
	}
	preview, _ := behaviorPreview(branch)
	if preview.StartLine != 21 || preview.EndLine != 24 || preview.Excerpt.Text != strings.Join(strings.Split(branch.Excerpt.Text, "\n")[1:5], "\n") {
		t.Fatalf("preview does not preserve the exact cited lines: %+v", preview)
	}
	for i := range report.Documents.Evidence {
		if report.Documents.Evidence[i].ID == branch.ID {
			report.Documents.Evidence[i] = preview
		}
	}
	for _, row := range newSummaryView(report, "x", Korean).Behaviors.Rows {
		if row.Preview == nil || row.CodeSource.Location != "" {
			t.Fatalf("identical preview and source was duplicated: %+v", row)
		}
	}
}
