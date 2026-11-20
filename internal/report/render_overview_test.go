package report

import (
	"net/url"
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
	if decision.Title != "재현된 조치" || decision.Tone != "action" || len(decision.Sources) != 1 || decision.Sources[0].Location != "store.py:12-27" {
		t.Fatalf("one strongest grounded decision required: %+v", decision)
	}
	if decision.State != "실행으로 재현" {
		t.Fatalf("the overview should identify execution evidence: %q", decision.State)
	}
	v.R.Findings = f[:1]
	if state := v.projectDecision().State; state != "코드 검토로 확인" {
		t.Fatalf("the overview should identify code review evidence: %q", state)
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
			if strings.Count(opening, Korean.Label("insight.interpretation")) != 1 {
				t.Fatal("opening should explain the source of the review once")
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
	if d.Title != Korean.Label("decision.document_claim") || d.DocumentClaim != c.Statement || d.Reason != c.Reason ||
		d.ReasonLabel != Korean.Label("decision.unconfirmed_reason") || d.State != Korean.Label("decision.comparison.unknown") || len(d.Sources) != 2 || d.Next != "" {
		t.Fatalf("the decision upgraded, duplicated or lost its unresolved comparison: %+v", d)
	}
	if d.Coverage != tf(Korean, "decision.comparison_all_unknown", 3) {
		t.Fatal("document comparisons lost their overall unknown count")
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
	r.Documents.Evidence = append(r.Documents.Evidence, e)
	r.Documents.Comparisons = []DocComparison{first, otherOperation, otherRange, second}
	d := newSummaryView(r, "0", Korean).Decision
	if d.Title != first.Statement || d.Condition != second.Statement || d.Reason != "" || len(d.Sources) != 2 {
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
		want, claim := Korean.Label("decision.omitted"), ""
		if include {
			want, claim = Korean.Label("decision.document_claim"), c.Statement
		}
		if d.Title != want || d.DocumentClaim != claim || d.State != Korean.Label("opening.implementation_unknown") || len(d.Sources) != 1 ||
			d.Sources[0].Location != "README.md:1-9" || d.Next != "" {
			t.Fatalf("include_code=%t: invalid document-only decision: %+v", include, d)
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
	if d.Title != r.CodeReview.Strengths[2].Benefit || d.Boundary != finding.Title || d.Reason != "" ||
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
			for _, want := range []string{r.Project.Name, source, Korean.Label("report.status." + r.Status), "2026-11-20 12:01 UTC"} {
				if !strings.Contains(cover, want) {
					t.Fatalf("cover lacks %q", want)
				}
			}
			for _, unwanted := range []string{r.Inputs.Description, Korean.Label("verdict.inconclusive"), "href="} {
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
	}
}
