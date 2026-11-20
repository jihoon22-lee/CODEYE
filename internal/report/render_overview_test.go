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
	v := secView{view: view{L: Korean, R: Report{CodeReview: c, Documents: d, Findings: f}}}
	parts := v.projectOpening()
	want := [][]string{{"재현된 조치", "근거 있는 조치", "부차 구현 검토"}, {"불일치 계약", "부분 일치 계약", "근거 있는 일치"}, {"구현의 강점"}, {"호출 경로 확인"}}
	if len(parts) != len(want) {
		t.Fatalf("opening groups: got %d, want %d", len(parts), len(want))
	}
	for i, group := range want {
		if len(parts[i].Cards) != len(group) {
			t.Fatalf("%s: got %d cards, want %d", parts[i].Heading, len(parts[i].Cards), len(group))
		}
		for j, title := range group {
			if got := parts[i].Cards[j].Title; got != title {
				t.Errorf("%s: got %q, want %q", parts[i].Heading, got, title)
			}
		}
	}
}

func TestProjectOpeningExportPolicyAndDetails(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	for _, include := range []bool{true, false} {
		p := Project(r, include)
		md := string(RenderMarkdownAt(p, "0", Korean, DetailSummary))
		h, err := RenderHTMLAt(p, "0", Korean, DetailSummary)
		if err != nil {
			t.Fatal(err)
		}
		checkInPageLinks(t, h)
		for _, out := range []string{md, string(h)} {
			opening, rest, ok := strings.Cut(out, Korean.Label("opening.coverage_details"))
			if !ok || !strings.Contains(rest, "fixtures/modules/input") || strings.Contains(opening, "fixtures/modules/input") {
				t.Fatal("source identity is missing from details or appears in the opening")
			}
			if !strings.Contains(opening, "gateway.go:1-16") || !strings.Contains(opening, "README.md:1-9") || !strings.Contains(opening, Korean.Label("opening.document_evidence")) {
				t.Fatal("located document and code evidence is missing from the opening")
			}
			if strings.Count(opening, Korean.Label("insight.interpretation")) != 1 {
				t.Fatal("opening should explain the source of the review once")
			}
			if strings.Contains(opening, "결제 호출은 요청마다 정확히 한 번만 한다") {
				t.Fatal("unverified contract in opening")
			}
			if hasExcerpt := strings.Contains(out, "Package gateway forwards billing requests"); hasExcerpt != include {
				t.Fatalf("include_code=%t, excerpt present=%t", include, hasExcerpt)
			}
			if hasModelText := strings.Contains(opening, "성공하면 즉시 종료하는 제한된 재시도"); hasModelText != include {
				t.Fatalf("include_code=%t, model text present=%t", include, hasModelText)
			}
		}
	}
}

func TestProjectOpeningToolOnly(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	r.CodeReview.FindingIDs, r.CodeReview.Strengths, r.Documents.Comparisons = nil, nil, nil
	r.Coverage.AIReview.Status = AIReviewNotPerformed
	v := newSummaryView(r, "0", Korean)
	var reasons, tool bool
	for _, part := range v.Sections[0].Parts {
		switch part.Heading {
		case Korean.Label("opening.limits"):
			reasons = strings.Contains(strings.Join(part.Paras, " "), Korean.Label("opening.model.not_performed"))
		case Korean.Label("opening.tool"):
			tool = len(part.List) == 1 && len(part.List[0].Codes) == 1 && strings.Contains(part.List[0].Codes[0], "-") &&
				part.List[0].Link == SectionStaticAnalysis
		default:
			t.Fatalf("unexpected review section %q", part.Heading)
		}
	}
	if !reasons || !tool {
		t.Fatalf("tool-only summary: model limitation=%t, located diagnostic=%t", reasons, tool)
	}
}

func TestProjectOpeningShowsOneUnknownComparisonWithBothReferences(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	c := r.Documents.Comparisons[0]
	c.Status, c.Reason = "unknown", "재시도 횟수는 코드에 있지만, 호출자가 제한을 적용하는지는 확인하지 못했다."
	missing := r.Documents.Comparisons[1]
	additional := c
	additional.Statement = "다른 미확인 계약"
	strengths := r.CodeReview.Strengths
	r.Documents.Comparisons = []DocComparison{missing, c, additional}
	r.CodeReview.FindingIDs, r.CodeReview.Strengths = nil, nil
	v := newSummaryView(r, "0", Korean)
	part := v.Sections[0].Parts[0]
	if part.Heading != Korean.Label("opening.document_follow_up") || len(part.Cards) != 1 {
		t.Fatalf("opening comparison: %+v", part)
	}
	card := part.Cards[0]
	if card.Title != c.Statement || card.State != Korean.Label("insight.unknown") || card.Link != SectionDocuments {
		t.Fatalf("unresolved comparison lost its status, statement or detail link: %+v", card)
	}
	md := string(RenderMarkdownAt(r, "0", Korean, DetailSummary))
	opening, _, _ := strings.Cut(md, Korean.Label("opening.coverage_details"))
	for _, want := range []string{c.Reason, "README.md:1-9", "gateway.go:1-16"} {
		if !strings.Contains(opening, want) && !strings.Contains(opening, mdText(want)) {
			t.Errorf("opening lacks %q", want)
		}
	}
	if strings.Contains(opening, missing.Statement) || strings.Contains(opening, additional.Statement) || strings.Contains(opening, Korean.Label("opening.tool")) {
		t.Fatal("opening contains more than one unresolved comparison or unrelated tool diagnostic")
	}
	r.CodeReview.Strengths = strengths
	parts := newSummaryView(r, "0", Korean).Sections[0].Parts
	if parts[0].Heading != Korean.Label("opening.strengths") || parts[1].Heading != Korean.Label("opening.document_follow_up") {
		t.Fatal("document follow-up should follow strengths with implementation evidence")
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
