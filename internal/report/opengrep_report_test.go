package report

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/opengrep"
)

// Every rule of the embedded codeye-taint pack has a Korean description
// whose first sentence names the rule's CWE (the title uses that sentence),
// and no description is left for a rule the pack does not have.
func TestOpengrepRulesHaveKoreanDescriptions(t *testing.T) {
	cwe := map[string]string{}
	for _, l := range opengrep.Languages() {
		for _, block := range regexp.MustCompile(`(?m)^  - id: `).Split(string(l.File), -1)[1:] {
			id := strings.Fields(block)[0]
			m := regexp.MustCompile(`cwe: \["(CWE-\d+)"\]`).FindStringSubmatch(block)
			if m == nil {
				t.Fatalf("%s has no CWE", id)
			}
			cwe[id] = m[1]
		}
	}
	if len(cwe) != len(opengrep.RuleIDs()) {
		t.Fatalf("rules %d, ids %d", len(cwe), len(opengrep.RuleIDs()))
	}
	for _, id := range opengrep.RuleIDs() {
		text := Korean.RuleDescriptions[opengrep.AnalyzerID+":"+id]
		if first := firstSentence(text); !strings.Contains(first, "("+cwe[id]+")") || len([]rune(first)) > summaryMessageRunes {
			t.Fatalf("%s: description %q", id, text)
		}
	}
	for key := range Korean.RuleDescriptions {
		if rule, ok := strings.CutPrefix(key, opengrep.AnalyzerID+":"); ok && cwe[rule] == "" {
			t.Fatalf("description for an unknown rule %s", key)
		}
	}
}

// The taint flow steps of the Opengrep pack (examples/observations-opengrep.json,
// a real envelope) stay related locations of the observation in the full and
// the code-free report; suppressed results stay observations, not findings.
func TestOpengrepFlowStepsSurviveReportProjection(t *testing.T) {
	f := analyzerFixture(t, nil, taskSpec{key: "opengrep", status: "succeeded", doc: readExample(t, "opengrep")})
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if len(r.Observations) != 9 || len(r.Findings) != 7 {
		t.Fatalf("observations %d findings %d", len(r.Observations), len(r.Findings))
	}
	var flow *Observation
	suppressed := 0
	for i, o := range r.Observations {
		if o.Tool != "any.opengrep" {
			t.Fatalf("tool %q", o.Tool)
		}
		if o.Suppressed {
			suppressed++
		}
		if o.RuleID == "codeye.python.command-injection" && o.Location != nil && o.Location.StartLine == 16 {
			flow = &r.Observations[i]
		}
	}
	if suppressed != 2 || flow == nil || len(flow.Related) != 6 {
		t.Fatalf("suppressed %d flow %+v", suppressed, flow)
	}
	source, callee := flow.Related[0], flow.Related[3]
	if source.Kind != "tool_flow_step" || source.Location == nil || source.Location.StartLine != 15 || source.RawMessage == nil ||
		*source.RawMessage != "taint flow step 1 of 6: source" || source.RawRef == nil || source.RawRef.ArtifactID != flow.RawRef.ArtifactID {
		t.Fatalf("flow source %+v", source)
	}
	if callee.Location == nil || callee.Location.StartLine != 9 || *callee.RawMessage != "taint flow step 4 of 6: callee parameter" {
		t.Fatalf("callee step %+v", callee)
	}
	full, err := RenderHTML(r, "test", Korean)
	if err != nil || !strings.Contains(string(full), "taint flow step 4 of 6: callee parameter") {
		t.Fatalf("render lost the flow steps: %v", err)
	}
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	for _, o := range free.Observations {
		for _, rel := range o.Related {
			if rel.RawMessage != nil || rel.Excerpt != nil || rel.Location == nil {
				t.Fatalf("code-free flow step %+v", rel)
			}
		}
	}
}
