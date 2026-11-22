package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestRuleDescriptionKeepsStoredFindingsAndGroupsInBothExports(t *testing.T) {
	const analyzer, rule = "java.spotbugs", "EI_EXPOSE_REP"
	const raw = "spotbugs reported " + rule
	d := readAnalyzerDoc(t, "../../examples/observations-spotbugs.json")
	for _, observation := range d["observations"].([]any) {
		o := observation.(map[string]any)
		o["rule_id"], o["raw_message"] = rule, raw
	}
	r := build(t, analyzerFixture(t, nil, taskSpec{key: "spotbugs.m01", status: "succeeded", doc: d}).in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	before, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const description = "반환한 가변 객체를 바꾸면 객체 내부 상태도 함께 바뀔 수 있습니다."
	if len(r.Observations) != 2 || r.Summary.ActionableFindings != 0 || r.Observations[0].RawMessage == nil ||
		*r.Observations[0].RawMessage != raw || !strings.HasPrefix(r.Findings[0].Title, analyzer+" "+rule+": ") {
		t.Fatalf("bad fixture: %+v", r.Observations)
	}
	for _, includeCode := range []bool{true, false} {
		projected := Project(r, includeCode)
		if err := ValidateReport(projected); err != nil {
			t.Fatal(err)
		}
		v := newSummaryView(projected, "", Korean)
		language := projected.StaticAnalysis.Languages[0]
		obs := map[string]Observation{}
		for _, o := range projected.Observations {
			obs[o.ID] = o
		}
		groups := v.staticResults(language, obs).Table
		if len(groups.Rows) != 1 || groups.Rows[0][2].Text != description || groups.Rows[0][3].Text != "2" ||
			strings.Contains(groups.Details[0].Text, raw) {
			t.Fatalf("single rule group did not explain its two diagnostics: %+v", groups)
		}
		for _, level := range []string{DetailSummary, DetailFull} {
			html, err := RenderHTMLAt(projected, "", Korean, level)
			if err != nil {
				t.Fatal(err)
			}
			markdown := RenderMarkdownAt(projected, "", Korean, level)
			for _, output := range []struct {
				content     []byte
				explanation string
				format      string
			}{{html, description, "HTML"}, {markdown, mdText(description), "Markdown"}} {
				if !bytes.Contains(output.content, []byte(analyzer)) || !bytes.Contains(output.content, []byte(rule)) ||
					!bytes.Contains(output.content, []byte(output.explanation)) || !includeCode && bytes.Contains(output.content, []byte(raw)) {
					t.Fatalf("missing source or explanation for %s %s/%v", output.format, level, includeCode)
				}
			}
		}
	}
	after, err := Marshal(r)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rendering changed the persisted report or its fingerprints", err)
	}
}

func TestCustomDependencyRuleUsesDeclaredIdentity(t *testing.T) {
	const analyzer = "javascript.dependency_cruiser"
	const rule = "project-local-name"
	const text = "프로젝트에 선언된 모듈 의존 규칙을 위반한 가져오기 경로입니다."
	if got := Korean.ruleDescription(analyzer, rule, "Dependency rule violated: "+rule); got != text {
		t.Fatalf("custom dependency rule explanation %q", got)
	}
	if got := Korean.ruleDescription(analyzer, "no-circular-dependencies", ""); !strings.Contains(got, "처음 모듈로 되돌아오는") {
		t.Fatalf("managed cycle rule explanation %q", got)
	}
	const unknown = "A meaningful diagnostic from the tool."
	if got := Korean.ruleDescription(analyzer, rule, unknown); got != unknown {
		t.Fatalf("unknown original tool message replaced with %q", got)
	}
}
