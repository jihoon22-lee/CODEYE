package report

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestProjectOpeningSelectsLocatedProductionReview(t *testing.T) {
	makeFinding := func(title, file, state, severity string) Finding {
		loc := Location{Path: file, StartLine: 12, EndLine: 27}
		return Finding{ID: title, Title: title, Validation: state, Severity: severity, Disposition: "open",
			Claims:       []Claim{{Assertion: "입력과 결과 사이의 근거", Kind: "inferred", EvidenceIDs: []string{"code"}}},
			Evidence:     []Evidence{{ID: "code", Kind: "code", Location: &loc}},
			Verification: []string{"호출자가 응답을 재사용하는지 확인한다."},
		}
	}
	f := []Finding{
		makeFinding("근거 있는 조치", "service.go", ValidationSupported, "high"),
		makeFinding("재현된 조치", "store.py", ValidationReproduced, "high"),
		makeFinding("보조 자료", "fixtures/compat.go", ValidationReproduced, "critical"),
		makeFinding("테스트만 확인", "service_test.go", ValidationSupported, "critical"),
		makeFinding("부차 구현 검토", "worker.go", ValidationSupported, "medium"),
		makeFinding("한도 밖 조치", "server.go", ValidationSupported, "low"),
		makeFinding("호출 경로 확인", "client.ts", ValidationInconclusive, "medium"),
		makeFinding("추가 확인 미기록", "client.ts", ValidationInconclusive, "high"),
		makeFinding("사람이 숨긴 항목", "client.ts", ValidationSupported, "critical"),
		makeFinding("반대 근거만 확인", "client.ts", ValidationSupported, "critical"),
	}
	f[7].Verification = nil
	f[8].Disposition = "false_positive"
	f[9].CounterevidenceIDs = []string{"code"}
	c := &CodeReviewSection{
		Strengths: []record.Strength{
			{Title: "예제의 강점", EvidenceIDs: []string{"sample"}},
			{Title: "구현의 강점", Benefit: "오류를 호출자에게 전달한다.", Scope: ".", EvidenceIDs: []string{"implementation"}},
		},
		Evidence: []SectionEvidence{
			{ID: "sample", Kind: "code", Path: "examples/gateway.go", StartLine: 1, EndLine: 8},
			{ID: "implementation", Kind: "code", Path: "gateway.go", StartLine: 4, EndLine: 8},
		},
	}
	for _, finding := range f {
		c.FindingIDs = append(c.FindingIDs, finding.ID)
	}
	comparison := func(status, statement, document string) DocComparison {
		return DocComparison{Status: status, Statement: statement, AppliesTo: ".", Reason: "호출 제한의 근거를 코드에서 확인했다.",
			DocumentEvidenceIDs: []string{document}, CodeEvidenceIDs: []string{"implementation"}}
	}
	d := &DocumentsSection{
		Comparisons: []DocComparison{
			comparison("unknown", "대조 미확인", "readme"), comparison("match", "근거 있는 일치", "readme"),
			comparison("mismatch", "예제 문서의 계약", "sample_doc"), comparison("partial", "부분 일치 계약", "readme"),
			comparison("mismatch", "불일치 계약", "readme"),
		},
		Evidence: append(c.Evidence,
			SectionEvidence{ID: "readme", Kind: "document", Path: "README.md", StartLine: 2, EndLine: 7},
			SectionEvidence{ID: "sample_doc", Kind: "document", Path: "samples/README.md", StartLine: 2, EndLine: 7}),
	}
	v := secView{view: view{L: Korean, R: Report{Export: Export{IncludeCode: true}, CodeReview: c, Documents: d, Findings: f}}}
	decision := v.projectDecision()
	if decision.Title != "불일치 계약" || len(decision.Sources) != 2 || decision.Sources[0].Location != "README.md:2-7" || decision.Action == nil {
		t.Fatalf("the documented behavior and independent high priority action should both appear: %+v", decision)
	}
	if decision.Action.Title != "재현된 조치" || decision.Action.State != "실행으로 재현" || decision.Action.Source.Location != "store.py:12-27" {
		t.Fatalf("the overview should identify evidence for the strongest independent action: %+v", decision.Action)
	}
	v.R.Findings = f[4:6]
	if decision = v.projectDecision(); decision.Title != "불일치 계약" || decision.Action != nil {
		t.Fatalf("lower priority findings should stay in their section: %+v", decision)
	}
	v.R.Documents = &DocumentsSection{}
	v.R.Findings = f[:1]
	if state := v.projectDecision().State; state != "코드 검토로 확인" {
		t.Fatalf("the overview should identify code review evidence: %q", state)
	}
}

func TestSummaryExportsLabelExampleAndTestFindings(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	var keep Finding
	for _, f := range r.Findings {
		if f.Validation == ValidationSupported {
			keep = f
			break
		}
	}
	if keep.ID == "" {
		t.Fatal("expected a supported finding in the report example")
	}
	for _, row := range []struct{ id, title, file, validation string }{
		{"fixture-only", "예제의 인자 검증", "fixtures/data.go", ValidationSupported},
		{"test-only", "시험의 예상값", "service_test.go", ValidationInconclusive},
	} {
		f := keep
		f.ID, f.Title, f.Validation = row.id, row.title, row.validation
		loc := Location{Path: row.file, StartLine: 8, EndLine: 9}
		f.Locations = []Location{loc}
		f.Evidence = []Evidence{{ID: "source", Kind: "code", Location: &loc}}
		r.Findings = append(r.Findings, f)
		r.CodeReview.FindingIDs = append(r.CodeReview.FindingIDs, f.ID)
	}
	h, err := RenderHTMLAt(r, "0", Korean, DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{string(h), string(RenderMarkdownAt(r, "0", Korean, DetailSummary))} {
		before, exampleSection, found := strings.Cut(out, Korean.Label("summary.findings_secondary"))
		if !found || !strings.Contains(before, keep.Title) || strings.Contains(before, "예제의 인자 검증") || strings.Contains(before, "시험의 예상값") ||
			!strings.Contains(exampleSection, "예제의 인자 검증") || !strings.Contains(exampleSection, "시험의 예상값") {
			t.Fatal("summary does not preserve findings under their source context")
		}
	}
}

func TestProjectDecisionShowsOneGroundedPriority(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs = nil
	c := r.Documents.Comparisons[0]
	c.Status = "mismatch"
	c.Statement = "재시도 범위가 문서의 약속과 다르다."
	c.Reason = "재시도 대상에는 일시 실패 이외의 오류도 포함된다."
	r.Documents.Comparisons = []DocComparison{c}
	for _, include := range []bool{true, false} {
		p := Project(r, include)
		md := string(RenderMarkdownAt(p, "0", Korean, DetailSummary))
		h, err := RenderHTMLAt(p, "0", Korean, DetailSummary)
		if err != nil {
			t.Fatal(err)
		}
		checkInPageLinks(t, h)
		for _, out := range []string{md, string(h)} {
			opening, _, ok := strings.Cut(out, Korean.Label("opening.coverage_details"))
			if !ok || !strings.Contains(opening, r.Project.Source) {
				t.Fatal("the registered source or coverage control is missing")
			}
			for _, want := range []string{"gateway.go:1-16", "README.md:1-9", Korean.Label("decision.comparison.mismatch")} {
				if !strings.Contains(opening, want) {
					t.Fatalf("opening lacks %q", want)
				}
			}
			// The mismatch card and the project decision each say that the
			// document reading is the model's; neither repeats it.
			if strings.Count(opening, Korean.Label("insight.interpretation")) != 2 {
				t.Fatal("opening should explain the source of the review once per presentation")
			}
			for _, s := range []string{c.Statement, c.Reason, "Package gateway forwards billing requests"} {
				if present := strings.Contains(opening, s) || strings.Contains(opening, mdText(s)); present != include {
					t.Fatalf("include_code=%t: opening has incorrect visibility for %q", include, s)
				}
			}
			omitted := Korean.Label("decision.omitted")
			if shown := strings.Contains(opening, omitted) || strings.Contains(opening, mdText(omitted)); shown == include {
				t.Fatal("projection did not explain an omitted decision accurately")
			}
		}
	}
}

func TestToolOnlyDecisionKeepsInterpretationAndCoverageSeparate(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths, r.Documents.Comparisons = nil, nil, nil
	r.Coverage.AIReview.Status = AIReviewNotPerformed
	v := newSummaryView(r, "0", Korean)
	d := v.Decision
	if d.State != Korean.Label("opening.tool") || d.Review || d.Next != "" || len(d.Sources) != 1 || d.Sources[0].Location == "" {
		t.Fatalf("tool-only decision claims too much or lost its location: %+v", d)
	}
	if !strings.Contains(strings.Join(v.OverviewDetail[0].Paras, " "), Korean.Label("opening.model.not_performed")) {
		t.Fatal("tool-only limitation missing from the analysis coverage")
	}
}

func TestDecisionGlossaryUsesOnlyProjectedPresentation(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs = nil
	c := r.Documents.Comparisons[0]
	c.Statement = "대기 중인 promise를 반환합니다."
	c.Reason = "잘못된 JSON 요청에 파싱 오류를 포함한 500 응답을 반환합니다. JSON 파싱 실패를 400으로 처리합니다."
	r.Documents.Comparisons = []DocComparison{c}
	for _, include := range []bool{true, false} {
		page, err := RenderHTMLAt(Project(r, include), "0", Korean, DetailSummary)
		if err != nil {
			t.Fatal(err)
		}
		opening, _, _ := strings.Cut(string(page), Korean.Label("opening.coverage_details"))
		for _, term := range []string{"Promise", "JSON 파싱", "HTTP 400", "HTTP 500"} {
			if got := strings.Contains(opening, "<dt>"+term+"</dt>"); got != include {
				t.Fatalf("include code=%t, unexpectedly has %q=%t", include, term, got)
			}
		}
		if strings.Count(opening, `<details class="report-glossary">`) != 1 || !strings.Contains(opening, "<dt>파일과 줄 번호</dt>") {
			t.Fatal("glossary should be a single closed disclosure for the retained location")
		}
	}
	plain := projectDecision{Title: "작업 400개와 500바이트, PromiseQueue와 JSON 파일", Next: "요청 수를 400으로 처리한다."}
	if terms := decisionGlossary(plain); len(terms) != 0 {
		t.Fatalf("unrelated counts, identifiers and file formats do not match: %+v", terms)
	}
	if terms := decisionGlossary(projectDecision{Next: "HTTP 400/500을 비교한다."}); len(terms) != 2 {
		t.Fatalf("both explicit status codes should match: %+v", terms)
	}
}

func TestEvidenceMissingDecisionStatesKnownCause(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths, r.Documents.Comparisons, r.Observations = nil, nil, nil, nil
	reason := "model_source_not_approved"
	r.Coverage.AIReview = AIReview{Status: AIReviewNotPerformed, RequiredTasks: []AIReviewTask{{Reason: &reason}}}
	d := newSummaryView(r, "0", Korean).Decision
	if d.Title != Korean.Label("opening.no_highlights") || d.Reason != reasonText(Korean, reason) || len(d.Glossary) != 0 {
		t.Fatalf("absent evidence did not show its recorded cause: %+v", d)
	}
}

func TestUnknownComparisonKeepsItsEvidenceAndStatus(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	c := r.Documents.Comparisons[0]
	c.Status, c.Reason = "unknown", "호출자가 제한을 적용하는지 코드에서 확인하지 못했다."
	missing := c
	missing.CodeEvidenceIDs = nil
	missing.Statement = "구현 인용이 없는 약속"
	additional := c
	additional.Statement = "다른 미확인 계약"
	r.Documents.Comparisons = []DocComparison{missing, c, additional}
	r.CodeReview.FindingIDs, r.CodeReview.Strengths = nil, nil
	d := newSummaryView(r, "0", Korean).Decision
	rows := d.ComparisonRows
	if d.Title != Korean.Label("decision.document_comparison") || d.State != Korean.Label("decision.code_evidence_missing") ||
		len(rows) != 2 || rows[0].Text != c.Statement || rows[1].Text != c.Reason || len(rows[0].Sources) != 1 ||
		len(rows[1].Sources) != len(c.CodeEvidenceIDs) || len(d.Sources) != 0 || d.Next != "" {
		t.Fatalf("the decision upgraded, duplicated or lost its unresolved comparison: %+v", d)
	}
	if len(d.ComparisonCounts) != 1 || d.ComparisonCounts[0].Name != "unknown" || d.ComparisonCounts[0].Value != "3개" {
		t.Fatal("document comparisons lost their overall unknown count")
	}
}

func TestDocumentQuestionBreaksOnlyAnUnambiguousUnknownTie(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths = nil, nil
	readme := SectionEvidence{ID: "readme", Path: "README.md", Kind: "document", StartLine: 2, EndLine: 12}
	guide := SectionEvidence{ID: "guide", Path: "docs/guide.md", Kind: "document", StartLine: 2, EndLine: 12}
	r.Documents.Evidence = []SectionEvidence{readme, guide}
	const queue = "대기열 보존 조건을 확인한다."
	const restore = "복원 기록을 확인한다."
	row := func(statement string) DocComparison {
		return DocComparison{Statement: statement, AppliesTo: ".", Status: "unknown", Reason: "공개 진입점은 확인하지 못했다.",
			DocumentEvidenceIDs: []string{readme.ID}}
	}
	r.Documents.Comparisons = []DocComparison{row("지원 범위와 실행 환경을 설명한다."), row("대기열 항목은 연결을 끊어도 보존된다."), row("복원 기록을 읽을 수 있다.")}
	question := func(prose, scope, id string) DocQuestion {
		return DocQuestion{Question: prose, AppliesTo: scope, EvidenceIDs: []string{id}}
	}
	for _, tc := range []struct {
		name      string
		questions []DocQuestion
		want      int
	}{
		{name: "earliest distinct subject", questions: []DocQuestion{question(queue, ".", readme.ID), question(restore, ".", readme.ID)}, want: 1},
		{name: "citation differs", questions: []DocQuestion{question(queue, ".", guide.ID)}},
		{name: "scope differs", questions: []DocQuestion{question(queue, "module", readme.ID)}},
		{name: "ambiguous", questions: []DocQuestion{question("대기열 또는 복원 동작을 확인한다.", ".", readme.ID)}},
		{name: "all generic", questions: []DocQuestion{question("지원 범위 확인", ".", readme.ID)}},
		{name: "older document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.Documents.Questions = tc.questions
			selected := newSummaryView(r, "0", Korean).Decision
			want := r.Documents.Comparisons[tc.want].Statement
			if len(selected.ComparisonRows) != 2 || selected.ComparisonRows[0].Text != want || selected.ComparisonAnchor != comparisonAnchor(tc.want) {
				t.Fatalf("selected %+v, want %q", selected, want)
			}
		})
	}
	r.Documents.Questions = []DocQuestion{question(queue, ".", readme.ID)}
	for _, include := range []bool{true, false} {
		published := Project(r, include)
		selected := newSummaryView(published, "0", Korean).Decision
		wantAnchor := comparisonAnchor(0)
		wantStatus := Korean.Label("decision.comparison.unknown")
		if include {
			wantAnchor = comparisonAnchor(1)
			wantStatus = Korean.Label("decision.code_evidence_missing")
		}
		if selected.ComparisonAnchor != wantAnchor || selected.State != wantStatus {
			t.Fatalf("unexpected decision in include_code=%t: %+v", include, selected)
		}
		html, err := RenderHTMLAt(published, "0", Korean, DetailSummary)
		if err != nil {
			t.Fatal(err)
		}
		contents := string(html)
		wantTitle := openingText(published.Documents.Comparisons[0].Statement, tf(Korean, "docs.withheld_behavior", 1))
		if include {
			wantTitle = published.Documents.Comparisons[1].Statement
		}
		if !strings.Contains(contents, `href="#`+wantAnchor+`"`) || !strings.Contains(contents, `id="`+wantAnchor+`" tabindex="-1"><strong>`+wantTitle+`</strong>`) ||
			!strings.Contains(contents, `class="badge report-reading-status">`+Korean.Label("decision.comparison.unknown")) {
			t.Fatal("the overview is missing the exact comparison route or matching status")
		}
	}
}

func TestDocumentDetailCollapsesOnlyAnEquivalentContract(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	c := r.Documents.Comparisons[0]
	c.Statement = "사용자의 같은 동작 설명"
	r.Documents.Comparisons = []DocComparison{c}
	r.Documents.Contracts = []DocContract{
		{Statement: c.Statement, AppliesTo: c.AppliesTo, EvidenceIDs: c.DocumentEvidenceIDs},
		{Statement: "아직 비교하지 않은 별도 동작", AppliesTo: c.AppliesTo, EvidenceIDs: c.DocumentEvidenceIDs},
	}
	h, err := RenderHTMLAt(r, "0", Korean, DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(string(h), `<section id="documents"`)
	details, _, _ := strings.Cut(body, `<section id="static_analysis"`)
	if strings.Count(details, c.Statement) != 1 || strings.Count(details, r.Documents.Contracts[1].Statement) != 1 {
		t.Fatal("the detailed document section lost an independent contract or repeated an equivalent comparison")
	}
}

func TestMatchedComparisonAddsOptionFromSameOperationAndCodeRange(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs = nil
	first := r.Documents.Comparisons[0]
	first.Statement, first.Status = "limit.clearQueue()는 대기 중인 작업을 버린다.", "match"
	second := first
	second.Statement = "rejectOnClear가 활성화된 경우 clearQueue()는 대기 중인 promise를 AbortError로 거부한다."
	otherOperation := second
	otherOperation.Statement = "rejectOnClear가 활성화된 경우 dispose()는 대기 작업을 버린다."
	otherRange := second
	otherRange.Statement = "rejectOnClear가 활성화된 경우 clearQueue()는 다른 동작을 한다."
	e := *bestProductEvidence(first.CodeEvidenceIDs, r.Documents.Evidence, "code")
	e.ID, e.EndLine = "another-range", e.EndLine+1
	otherRange.CodeEvidenceIDs = []string{e.ID}
	optionSource := SectionEvidence{ID: "option-doc", Kind: "document", Path: "docs/options.md", StartLine: 5, EndLine: 8}
	second.DocumentEvidenceIDs = []string{optionSource.ID}
	r.Documents.Evidence = append(r.Documents.Evidence, e, optionSource)
	r.Documents.Comparisons = []DocComparison{first, otherOperation, otherRange, second}
	d := newSummaryView(r, "0", Korean).Decision
	if d.Title != first.Statement || d.Condition != second.Statement || d.Reason != first.Reason || len(d.Sources) != 3 || d.Sources[2].Location != "docs/options.md:5-8" {
		t.Fatalf("expected one matched comparison with the grounded option: %+v", d)
	}
	r.Documents.Comparisons = r.Documents.Comparisons[:3]
	if d := newSummaryView(r, "0", Korean).Decision; d.Condition != "" {
		t.Fatalf("paired a different operation or a different range: %+v", d)
	}
}

func TestDocumentOnlyDecisionRemainsUnverifiedInBothExports(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths = nil, nil
	c := r.Documents.Comparisons[0]
	c.Status, c.CodeEvidenceIDs = "unknown", nil
	c.Statement = "설계 문서에만 적힌 동작 예시"
	r.Documents.Comparisons = []DocComparison{c}
	for _, include := range []bool{true, false} {
		p := Project(r, include)
		d := newSummaryView(p, "0", Korean).Decision
		if include {
			rows := d.ComparisonRows
			if d.Title != Korean.Label("decision.document_comparison") || d.State != Korean.Label("decision.code_evidence_missing") ||
				len(rows) != 2 || rows[0].Text != c.Statement || len(rows[0].Sources) != 1 ||
				rows[0].Sources[0].Location != "README.md:1-9" || len(rows[1].Sources) != 0 {
				t.Fatalf("invalid document-only rows: %+v", d)
			}
		} else if d.Title != Korean.Label("decision.omitted") || len(d.ComparisonRows) != 0 || d.State != Korean.Label("decision.comparison.unknown") ||
			len(d.Sources) != 1 || d.Sources[0].Location != "README.md:1-9" || d.Next != "" {
			t.Fatalf("include_code=%t: invalid document-only decision: %+v", include, d)
		}
	}
}

func TestProjectOpeningLeadsWithProductEvidenceWhenComparisonsAreUnknown(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	docs, review := r.Documents, r.CodeReview
	row := docs.Comparisons[0]
	row.Status, row.CodeEvidenceIDs = "unknown", nil
	row.Statement = "사용자는 이전의 알림을 다시 열 수 있다."
	row.Reason = "알림을 다시 여는 진입점은 확인하지 못했다."
	other := row
	other.Statement = "지원 도구는 요청 전 환경을 확인한다."
	fixture := row
	fixture.AppliesTo = "fixtures"
	fixture.Statement = "시험에서만 사용하는 동작"
	docs.Comparisons = []DocComparison{row, other, fixture}
	review.FindingIDs = nil
	review.Strengths[0].Scope = "gateway"
	proof := review.Strengths[0]
	review.Evidence = append(review.Evidence, SectionEvidence{
		ID: "qa", Kind: "code", Path: "scripts/qa-reports.sh", StartLine: 1, EndLine: 4,
	})
	review.Strengths = append([]record.Strength{{
		Title: "시험 준비", Benefit: "시험용 보고서를 생성한다.", Scope: ".", EvidenceIDs: []string{"qa"},
	}}, review.Strengths...)
	overview := overviewOf(r)
	r.Overview = &overview

	for _, include := range []bool{true, false} {
		projection := Project(r, include)
		d := newSummaryView(projection, "0", Korean).Decision
		if d.State != Korean.Label("decision.preserve") || d.ComparisonAnchor != comparisonAnchor(0) ||
			d.Scope != proof.Scope || len(d.ComparisonCounts) != 1 || d.ComparisonCounts[0].Value != "3개" ||
			len(d.Sources) != 1 || len(d.ComparisonRows) != 0 {
			t.Fatalf("include_code=%t: unexpected opening %+v", include, d)
		}
		if include && (d.Title != proof.Title || d.Reason != proof.Benefit || d.ComparisonExample != row.Statement) ||
			!include && (d.Title != Korean.Label("decision.omitted") || d.Reason != "" || d.ComparisonExample != "") {
			t.Fatalf("include_code=%t: unexpected visible prose %+v", include, d)
		}
		for _, detail := range []string{DetailSummary, DetailFull} {
			h, err := RenderHTMLAt(projection, "0", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			html := string(h)
			md := string(RenderMarkdownAt(projection, "0", Korean, detail))
			if !strings.Contains(html, `data-comparison-status="unknown">3개`) ||
				!strings.Contains(html, `href="#document-comparison-1"`) ||
				!strings.Contains(html, `id="document-comparison-1"`) ||
				!strings.Contains(md, "**문서 동작 대조 3개** · 구현 대조 미확인 3개") ||
				!strings.Contains(md, "[문서 분석에서 보기](#document-comparison-1)") ||
				!strings.Contains(md, `<a id="document-comparison-1"></a>`) {
				t.Fatalf("%s export lost status counts or the detail destination", detail)
			}
			for _, out := range []string{html, md} {
				opening, _, _ := strings.Cut(out, Korean.Label("opening.coverage_details"))
				contains := func(s string) bool { return strings.Contains(opening, s) || strings.Contains(opening, mdText(s)) }
				if contains(proof.Title) != include || contains(proof.Benefit) != include || contains(row.Statement) != include {
					t.Fatalf("%s export exposed or hid the wrong prose (include_code=%t)", detail, include)
				}
			}
		}
	}
}

func TestProjectDecisionShowsRecordedLimitationBeforeAction(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.Documents.Comparisons = nil
	finding := &r.Findings[slices.IndexFunc(r.Findings, func(f Finding) bool { return f.Validation == ValidationSupported })]
	finding.Conditions[0].Statement = "운영체제에서 파일 알림이 동시에 전달되면 큐가 재시작된다."
	finding.Verification = []string{"운영체제의 파일 알림 전달 순서를 확인한다."}
	finding.Recommendation.Summary = "파일 알림 큐를 교체한다."
	const limitation = "운영체제의 파일 알림이 동시에 전달되는지는 확인되지 않았다."
	finding.Limitations = []string{"verification_call_failed", limitation, "추가 입력의 종류는 확인하지 않았다."}
	d := newSummaryView(r, "0", Korean).Decision
	if d.State != Korean.Label("decision.supported") || d.Tone != "action" || d.Reason != finding.Impact ||
		d.Limitation != limitation || d.Next != finding.Recommendation.Summary || len(d.Sources) != 1 {
		t.Fatalf("the published finding lost its state, caveat or recommendation: %+v", d)
	}
	h, err := RenderHTMLAt(r, "0", Korean, DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{string(h), string(RenderMarkdownAt(r, "0", Korean, DetailSummary))} {
		opening, _, _ := strings.Cut(out, Korean.Label("opening.coverage_details"))
		caveat, action := strings.Index(opening, Korean.Label("decision.finding_limitation")), strings.Index(opening, Korean.Label("decision.conditional_action"))
		if caveat < 0 || action <= caveat || !strings.Contains(opening, "파일 알림 큐를 교체한다") ||
			strings.Contains(opening, "추가 입력의 종류는 확인하지 않았다") || strings.Contains(opening, Korean.Label("decision.next")) {
			t.Fatal("opening did not provide the recorded limitation before the conditional recommendation")
		}
	}
	if finding.Validation != ValidationSupported {
		t.Fatal("rendering altered the published assessment")
	}
	finding.Validation = ValidationInconclusive
	d = newSummaryView(r, "0", Korean).Decision
	if d.Limitation != limitation || d.Tone != "pending" || d.Next != finding.Verification[0] {
		t.Fatalf("an inconclusive finding lost the caveat or next check: %+v", d)
	}
	exported := newSummaryView(Project(r, false), "0", Korean).Decision
	if exported.Limitation != "" {
		t.Fatalf("code-free overview exposed a caveat: %+v", exported)
	}
}

func TestStrengthOpeningKeepsSharedEvidenceWithoutRepeatingItsTitle(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.Documents.Comparisons, r.CodeReview.FindingIDs = nil, nil
	proof := &r.CodeReview.Strengths[0]
	proof.Title, proof.Benefit = "재시도 중단", "재시도  중단."
	e := *bestProductEvidence(proof.EvidenceIDs, r.CodeReview.Evidence, "code")
	wider := e
	wider.ID, wider.EndLine = "wider", e.EndLine+2
	r.CodeReview.Evidence = append(r.CodeReview.Evidence, wider)
	r.CodeReview.Strengths = append(r.CodeReview.Strengths, record.Strength{
		Title: "재시도 제한", Benefit: "오류가 이어지면 요청 횟수를 제한한다.", EvidenceIDs: []string{wider.ID},
	})
	d := newSummaryView(r, "0", Korean).Decision
	if d.Title != tf(Korean, "decision.related", proof.Title, r.CodeReview.Strengths[1].Title) ||
		d.Reason != r.CodeReview.Strengths[1].Benefit || len(d.Sources) != 1 ||
		d.Sources[0].Location != wider.Path+":"+itoa(wider.StartLine)+"-"+itoa(wider.EndLine) {
		t.Fatalf("related strength lost its context, removed the useful effect or repeated its title: %+v", d)
	}
}

func TestUnknownComparisonRowsEscapeTextAndKeepInlineCode(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths = nil, nil
	c := r.Documents.Comparisons[0]
	c.Status = "unknown"
	c.Statement = "설명서는 `Read(path)` 호출을 설명한다. <img src=x>"
	c.Reason = "코드에서 `<script>probe()</script>`와 ``예시``를 확인했다. 짝 없는 `"
	r.Documents.Comparisons = []DocComparison{c}
	for _, detail := range []string{DetailSummary, DetailFull} {
		raw, err := RenderHTMLAt(r, "0", Korean, detail)
		if err != nil {
			t.Fatal(err)
		}
		_, start, _ := strings.Cut(string(raw), `<dl class="report-comparison-rows">`)
		rows, _, _ := strings.Cut(start, "</dl>")
		for _, expected := range []string{"<dt>설명서</dt>", "<dt>코드</dt>", "<code>Read(path)</code>",
			"&lt;img src=x&gt;", "<code>&lt;script&gt;probe()&lt;/script&gt;</code>", "``예시``", "짝 없는 `"} {
			if !strings.Contains(rows, expected) {
				t.Errorf("HTML %s rows missing %q", detail, expected)
			}
		}
		if strings.Contains(rows, "<script>") || strings.Contains(rows, "<img") {
			t.Errorf("HTML %s created tags from report text", detail)
		}
		output := string(RenderMarkdownAt(r, "0", Korean, detail))
		for _, expected := range []string{"**설명서** 설명서는 `Read(path)`", "**코드** 코드에서 `<script>probe()</script>`",
			"&lt;img src", "짝 없는 \\`", "[문서 분석에서 보기](#document-comparison-1)", `<a id="document-comparison-1"></a>`} {
			if !strings.Contains(output, expected) {
				t.Errorf("Markdown %s missing %q", detail, expected)
			}
		}
	}
}

func TestRelatedConfirmedControlAndPendingBehaviorHaveSeparateRanges(t *testing.T) {
	loc := Location{Path: "backup.py", StartLine: 53, EndLine: 73}
	finding := Finding{ID: "pending", Title: "백업 실패 시 일부 파일이 남을 수 있음", Validation: ValidationInconclusive,
		Evidence:     []Evidence{{ID: "rest", Kind: "code", Location: &loc}},
		Claims:       []Claim{{EvidenceIDs: []string{"rest"}}},
		Verification: []string{"백업 호출자의 실패 처리와 보존 계약을 확인한다.", "백업 목록과 복원 경로가 부분 파일을 어떻게 취급하는지 확인한다."},
		Impact:       "저장소 손상 확정"}
	r := Report{Export: Export{IncludeCode: true}, Findings: []Finding{finding}, Documents: &DocumentsSection{}, CodeReview: &CodeReviewSection{
		FindingIDs: []string{finding.ID},
		Strengths: []record.Strength{
			{Title: "요청 오류 처리", Benefit: "실패를 로그에 남긴다.", EvidenceIDs: []string{"same-file"}},
			{Title: "백업 실패 처리", Benefit: "값을 가린다.", EvidenceIDs: []string{"different-file"}},
			{Title: "백업 무결성 확인", Benefit: "압축 전 SQLite 백업의 손상을 검사한다.", EvidenceIDs: []string{"backup"}},
		},
		Evidence: []SectionEvidence{
			{ID: "same-file", Kind: "code", Path: "backup.py", StartLine: 4, EndLine: 8},
			{ID: "different-file", Kind: "code", Path: "client.py", StartLine: 4, EndLine: 8},
			{ID: "backup", Kind: "code", Path: "backup.py", StartLine: 1, EndLine: 50},
		},
	}}
	v := secView{view: view{R: r, L: Korean}}
	d := v.projectDecision()
	if d.Title != r.CodeReview.Strengths[2].Title || d.Boundary != finding.Title || d.Reason != r.CodeReview.Strengths[2].Benefit ||
		d.Next != finding.Verification[1] || len(d.Sources) != 2 ||
		d.Sources[0].Location != "backup.py:1-50" || d.Sources[1].Location != "backup.py:53-73" {
		t.Fatalf("the established behavior and pending boundary were conflated: %+v", d)
	}
	r.CodeReview.Strengths = r.CodeReview.Strengths[:2]
	d = v.projectDecision()
	if d.Title != finding.Title || d.Boundary != "" || d.Reason != "" || len(d.Sources) != 1 || d.Next != finding.Verification[1] {
		t.Fatalf("an unrelated strength overrode the only stored next check: %+v", d)
	}
}

func TestReportCoverUsesOnlyRegisteredSourceAndRecordedTime(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.Inputs.Description = "scratch/workspace/provisional-copy"
	r.Status = "partial"
	r.Project.StartedAt = "2026-11-20T21:00:20+09:00"
	r.Project.FinishedAt = "2026-11-20T21:01:52+09:00"
	for _, source := range []string{
		"https://github.com/owner/" + strings.Repeat("long-", 35) + "project.git",
		"/nfs/team folder/" + strings.Repeat("nested/", 35) + "분석 대상",
	} {
		r.Project.Source = source
		for _, detail := range []string{DetailSummary, DetailFull} {
			html, err := RenderHTMLAt(r, "0", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			_, rest, ok := strings.Cut(string(html), "<header class=\"report-hero\">")
			if !ok {
				t.Fatal("missing report cover")
			}
			cover, _, _ := strings.Cut(rest, "</header>")
			for _, want := range []string{r.Project.Name, source, "일부 검사 미완료로 종료",
				`시작 시각</dt><dd><time datetime="2026-11-20T21:00:20&#43;09:00">2026-11-20 12:00:20 UTC`,
				`종료 시각</dt><dd><time datetime="2026-11-20T21:01:52&#43;09:00">2026-11-20 12:01:52 UTC`,
				"소요 시간</dt><dd>1분 32초"} {
				if !strings.Contains(cover, want) {
					t.Fatalf("cover lacks %q", want)
				}
			}
			for _, unwanted := range []string{r.Inputs.Description, Korean.Label("verdict.inconclusive"), "분석 종료", "href="} {
				if strings.Contains(cover, unwanted) {
					t.Fatalf("cover exposes %q", unwanted)
				}
			}
			if strings.Contains(string(html), "<strong>구현 근거</strong>") || strings.Contains(string(html), ">상세 근거 보기<") ||
				strings.Contains(string(html), "적용 범위 · .") || strings.Contains(string(html), "[.] ") {
				t.Fatal("rendered report has a duplicate evidence label or a scope placeholder")
			}
		}
	}
}

func TestReportCoverKeepsCommitAndPublicationFallback(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	commit := strings.Repeat("ab", 20)
	r.Inputs.CommitSHA = &commit
	r.Project.StartedAt, r.Project.FinishedAt, r.CreatedAt = "2026-11-20T12:00:00Z", "", "2026-11-20T12:01:00Z"
	html, err := RenderHTMLAt(r, "0", Korean, DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(html), `<header class="report-hero">`)
	cover, _, _ := strings.Cut(rest, `</header>`)
	if !strings.Contains(cover, `title="`+commit+`">`+commit[:12]) || !strings.Contains(cover, "시작 시각") ||
		!strings.Contains(cover, `보고서 작성 시각</dt><dd><time datetime="2026-11-20T12:01:00Z">`) ||
		strings.Contains(cover, "종료 시각") || strings.Contains(cover, "소요 시간") {
		t.Fatalf("bad cover: %s", cover)
	}
}

func TestSnapshotBrowserURL(t *testing.T) {
	sha := strings.Repeat("a", 40)
	valid := Report{Export: Export{IncludeCode: true}, Project: &ProjectInfo{Source: "https://GHE.example:8443/team/tool.git/"},
		Inputs: Inputs{SourceKind: "git", CommitSHA: &sha}}
	file := "src/조건 (v1)#.go"
	got, err := url.Parse(snapshotBrowserURL(valid, file, 282, 304))
	if err != nil || got.Scheme != "https" || got.Host != "ghe.example:8443" || got.Fragment != "L282-L304" ||
		got.Path != "/team/tool/blob/"+sha+"/"+file || !strings.Contains(got.EscapedPath(), "%20%28v1%29%23.go") {
		t.Fatalf("invalid pinned browser URL: %v (%v)", got, err)
	}
	for _, raw := range []string{"github.com/team/tool", "http://github.com/team/tool", "https://user@github.com/team/tool", "https://github.com/team/tool?branch=main", "https://github.com/team/tool#main", "https://github.com/team/tool/tree/main"} {
		r := valid
		r.Project = &ProjectInfo{Source: raw}
		if got := snapshotBrowserURL(r, file, 282, 304); got != "" {
			t.Errorf("accepted noncanonical repository %q: %q", raw, got)
		}
	}
	for _, path := range []string{"/etc/passwd", "foo/../bar", "C:\\path.go"} {
		if got := snapshotBrowserURL(valid, path, 1, 1); got != "" {
			t.Errorf("accepted invalid source path %q", path)
		}
	}
	for _, r := range []Report{
		{Export: Export{IncludeCode: false}, Project: valid.Project, Inputs: valid.Inputs},
		{Export: valid.Export, Project: valid.Project, Inputs: Inputs{SourceKind: "local", CommitSHA: &sha}},
		{Export: valid.Export, Project: valid.Project, Inputs: Inputs{SourceKind: "git"}},
		{Export: valid.Export, Project: valid.Project, Inputs: valid.Inputs, PullRequest: &PullRequestSection{HeadRepository: "fork/tool"}},
	} {
		if got := snapshotBrowserURL(r, file, 1, 1); got != "" {
			t.Errorf("linked ineligible commit evidence: %q", got)
		}
	}
}

func TestSnapshotBrowserLinksRespectExportProjection(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.Project.Source = "https://github.com/owner/project.git"
	sha := strings.Repeat("b", 40)
	r.Inputs.SourceKind, r.Inputs.CommitSHA = "git", &sha
	want := "https://github.com/owner/project/blob/" + sha + "/gateway.go#L1-L16"
	for _, include := range []bool{true, false} {
		p := Project(r, include)
		md := string(RenderMarkdownAt(p, "0", Korean, DetailSummary))
		html, err := RenderHTMLAt(p, "0", Korean, DetailSummary)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(md, want) != include || strings.Contains(string(html), want) != include {
			t.Fatalf("include_code=%t, pinned link presence is incorrect", include)
		}
		if include && (!strings.Contains(string(html), `rel="noopener noreferrer"`) || !strings.Contains(md, mdText(Korean.Label("opening.open_source"))) ||
			!strings.Contains(string(html), "Package gateway forwards billing requests")) {
			t.Fatal("missing pinned link, its label or the original excerpt")
		}
		_, contents, _ := strings.Cut(string(html), `class="card report-group report-decision"`)
		overview, _, _ := strings.Cut(contents, `<details class="card report-disclosure"`)
		if strings.Contains(overview, `class="report-excerpt"`) != include || strings.Contains(overview, want) != include {
			t.Fatalf("overview include_code=%t did not preserve the saved excerpt and fixed link", include)
		}
	}
	for i := range r.Documents.Evidence {
		r.Documents.Evidence[i].Excerpt = nil
	}
	html, err := RenderHTMLAt(r, "0", Korean, DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	_, contents, _ := strings.Cut(string(html), `class="card report-group report-decision"`)
	overview, _, _ := strings.Cut(contents, `<details class="card report-disclosure"`)
	if !strings.Contains(overview, `<a href="`+want+`"`) || strings.Contains(overview, `class="report-excerpt"`) {
		t.Fatal("overview did not fall back to the verified link when the excerpt was absent")
	}
}

func TestSecretOverviewSeparatesDetectionsFromScreeningState(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	detection := r.Secrets.Detections[0]
	cases := []struct {
		state, status, tone, value string
		detected                   bool
	}{
		{"succeeded", "탐지 있음", "attention", "검사에서 보고한 탐지 1건", true},
		{"partial", "탐지 있음", "attention", "검사에서 보고한 탐지 1건 · 일부 수행", true},
		{"partial", "일부 수행", "partial", "탐지 결과 미확정", false},
		{"failed", "실패", "failed", "탐지 결과 미확정", false},
		{"skipped", "제외", "neutral", "탐지 결과 미확정", false},
		{"succeeded", "탐지 없음", "ready", "검사에서 보고한 탐지 0건", false},
	}
	for _, version := range []string{"16", BuilderVersion} {
		r.Builder.Version = version
		for _, tc := range cases {
			r.Secrets.Stage.State = tc.state
			r.Secrets.Detections = nil
			if tc.detected {
				r.Secrets.Detections = []SecretDetection{detection}
			}
			v := secView{view: view{L: Korean, R: r}}
			rows := v.ReadingOverview()
			i := slices.IndexFunc(rows, func(row overviewReadingRow) bool { return row.Section == SectionSecrets })
			if i < 0 {
				t.Fatal("missing secrets row")
			}
			row := rows[i]
			if row.Status != tc.status || row.Tone != tc.tone || row.Value != tc.value+" · Git 이력 미검사" {
				t.Fatalf("builder %s, %s, detected=%t: %+v", version, tc.state, tc.detected, row)
			}
		}
	}
	r = storedExample(t, "testdata/report-unscreened.json")
	v := secView{view: view{L: Korean, R: r}}
	if slices.ContainsFunc(v.ReadingOverview(), func(row overviewReadingRow) bool { return row.Section == SectionSecrets }) {
		t.Fatal("reports with no detection task should not display a scan result")
	}
}
