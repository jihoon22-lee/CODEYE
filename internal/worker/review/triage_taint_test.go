package review

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/opengrep"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// taintTree: a.go reads a file named by the request (source line 13, sink
// line 14); examples/b.go, an example the path rule lowers, removes one after an allow-list check (source 14, sink 19).
var taintTree = map[string]string{
	"go.mod":    "module example.test/taint\n\ngo 1.23\n",
	"README.md": "# Files\n\nThe service sends and removes files under /srv/files.\n",
	"a.go": `package taint

import (
	"net/http"
	"os"
	"path/filepath"
)

const root = "/srv/files"

// Download sends a file named by the request.
func Download(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Write(b)
}
`,
	"examples/b.go": "package taint\n\nimport (\n\t\"net/http\"\n\t\"os\"\n\t\"path/filepath\"\n\t\"regexp\"\n)\n\nvar validName = regexp.MustCompile(`^[a-z0-9]+\\.txt$`)\n\n" +
		`// Remove deletes a file named by the request after checking the name.
func Remove(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validName.MatchString(name) {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	os.Remove(filepath.Join(root, name))
}
`,
}

const taintRule = "codeye.go.path-traversal"

func taintStatic(t *testing.T) *pack.Composite {
	t.Helper()
	rp := obsdoc.RulePack{ID: opengrep.RulePackID, Version: opengrep.RulePackVersion, Selection: []string{"--taint-intrafile", "rules/go.yaml@000000000000"},
		RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	const msg = "Request input reaches a file path without a containment check."
	diags := []exampleDiag{
		{path: "a.go", line: 14, rule: taintRule, category: "security", message: msg, flow: []int{13, 14}},
		{path: "examples/b.go", line: 19, rule: taintRule, category: "security", message: msg, flow: []int{14, 19}},
	}
	c, err := pack.NewComposite(exampleMember{d: opengrep.Descriptor(), plan: opengrep.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
		return exampleEnvelope(t, j, opengrep.Descriptor(), task, "2.0.0", rp, []string{"a.go", "examples/b.go"}, []string{"intra_file_flows_only", "request_input_sources_only"}, diags)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TC-305: an Opengrep diagnostic reaches the triage request with its taint
// flow steps as related locations in its group's context and its default
// severity from the pack's table; the a.go group the triage calls a problem
// becomes a candidate the slot's verification supports, the examples/b.go group it
// calls harmless stays listed with its reason and keeps its tool finding,
// whose impact is the fixed rule description, not model text.
func TestOpengrepDiagnosticTriage(t *testing.T) {
	var mu sync.Mutex
	var sent []triageGroupInput
	s := goBuildFilesScript(t)
	s["interpret"] = func(int, opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []any{}, "limits": []any{}})
	}
	classify := triageScript(t, func(g triageGroupInput) string {
		mu.Lock()
		sent = append(sent, g)
		mu.Unlock()
		if g.Path == "a.go" {
			return record.TriageProblem
		}
		return record.TriageHarmless
	})
	s["triage"] = classify
	whole := s["verify"]
	s["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if len(in.Candidates) == 0 || !strings.HasPrefix(in.Candidates[0].ID, "triage_") {
			return whole(n, q)
		}
		var out []map[string]any
		for _, c := range in.Candidates {
			out = append(out, verdict(c.ID, "supported", func(m map[string]any) {
				m["category"], m["severity"], m["severity_reason"] = "security", "high", "요청이 정한 이름으로 서비스 밖 파일을 읽는다."
				m["impact"], m["valid_conditions"] = "요청이 ../를 담으면 루트 밖 파일이 응답으로 나간다.", []string{"name에 ../가 들어온다."}
				m["supporting_evidence_ids"] = []string{c.Evidence[0]}
			}))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	j := treeJob(t, taintTree)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000b01", "0195f3a0-0000-7000-8000-000000000b02"
	caller := &scriptedCaller{script: s}
	h, err := discovery.Wrap(Handler{Static: taintStatic(t), Caller: caller}, discovery.Config{Observer: exampleObserver{t: t}})
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
	for _, key := range []string{"review.triage.t001", "review.verify.t001"} {
		if res := results[key]; res.Status != "succeeded" {
			t.Fatalf("%s: %+v", key, res)
		}
	}
	// The request: one group per file; the example carries the flow steps
	// inside the group's own unit and the table's default severity.
	if len(sent) != 2 {
		t.Fatalf("%d groups sent", len(sent))
	}
	for _, g := range sent {
		want := map[string][]int{"a.go": {13, 14}, "examples/b.go": {14, 19}}[g.Path]
		// The model is told the severity the report gives the findings: the
		// table's medium cap for the shipped a.go, lowered for the example
		// file, which is ordered after it, as for any tool.
		wantSeverity, wantClass := severity.Medium, ""
		if g.Path == "examples/b.go" {
			wantSeverity, wantClass = "low", "example"
		}
		if g.DefaultSeverity != wantSeverity || g.PathClass != wantClass || !slices.Equal(g.Tools, []string{opengrep.AnalyzerID}) || len(g.Examples) != 1 {
			t.Fatalf("group %+v", g)
		}
		var lines []int
		for _, r := range g.Examples[0].Related {
			if r.Kind != obsdoc.RelatedFlowStep || r.EvidenceID != g.Context[0] {
				t.Fatalf("%s related %+v, context %v", g.Path, r, g.Context)
			}
			lines = append(lines, r.StartLine)
		}
		if !slices.Equal(lines, want) {
			t.Fatalf("%s flow lines %v", g.Path, lines)
		}
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 0xa00}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(report.Project(r, false)); err != nil {
		t.Fatalf("code-free export: %v", err)
	}
	rows := 0
	for _, row := range r.Coverage.Tools {
		if row.Analyzer == opengrep.AnalyzerID {
			rows++
			if row.SeverityTable != opengrep.RulePackID+"/1+"+severity.PathRule {
				t.Fatalf("tool row severity table %q", row.SeverityTable)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("%d opengrep tool rows", rows)
	}
	d := r.CodeReview.DiagnosticTriage
	if d == nil || d.Diagnostics != 2 || d.Triaged != 2 || len(d.Groups) != 2 {
		t.Fatalf("triage %+v", d)
	}
	findings := map[string]report.Finding{}
	for _, f := range r.Findings {
		findings[f.ID] = f
	}
	toolFinding := map[string]report.Finding{}
	for _, f := range r.Findings {
		if !strings.HasPrefix(f.ID, "review_") {
			for _, id := range f.ObservationIDs {
				toolFinding[id] = f
			}
		}
	}
	for _, g := range d.Groups {
		tf := toolFinding[g.ObservationIDs[0]]
		// The tool finding keeps the fixed Korean rule description and the
		// table's default (the medium cap; low in the example file by the
		// path rule, which its reason names), whatever the triage says.
		wantSeverity, wantReason := "medium", "측정이 없어"
		if tf.Locations[0].Path == "examples/b.go" {
			wantSeverity, wantReason = "low", severity.PathRule
		}
		if !strings.HasPrefix(tf.Impact, "요청 입력이") || tf.Severity != wantSeverity || !strings.Contains(tf.SeverityReason, wantReason) {
			t.Fatalf("tool finding %s: %q %s %q", tf.ID, tf.Impact, tf.Severity, tf.SeverityReason)
		}
		switch g.Classification {
		case record.TriageProblem:
			f, ok := findings[deref(g.FindingID)]
			if !ok || f.Validation != report.ValidationSupported || g.Bucket != report.TriageAttention || len(f.Locations) == 0 || f.Locations[0].Path != "a.go" {
				t.Fatalf("problem group %+v, finding %+v", g, f)
			}
			// The verified model finding has the severity verify gave it, so
			// the tool default's cap does not hide a real flow.
			if strings.HasPrefix(f.Impact, "요청 입력이") || f.Severity != "high" {
				t.Fatalf("candidate finding %q %s", f.Impact, f.Severity)
			}
		case record.TriageHarmless:
			if g.Reason == nil || g.FindingID != nil || g.Bucket != report.TriageHarmless || len(g.Locations) == 0 || g.Locations[0].Path != "examples/b.go" {
				t.Fatalf("harmless group %+v", g)
			}
		default:
			t.Fatalf("group %+v", g)
		}
	}
}

// TC-305: the related locations of a triage example are the ones in the
// group's own context, each naming its excerpt: a step in the unit names the
// unit, one in the file header sent with it names the header; a step in
// another unit, another file or another blob of the file is left out; past
// eight the first seven and the last stay. Leaving any out is reported.
func TestTriageRelatedInGroupContext(t *testing.T) {
	const sha = "sha-a"
	u := triageUnit{entry: source.Entry{Path: "a.go", SHA256: sha}, span: lineSpan{start: 20, end: 40}, header: 8}
	step := func(path, blob string, line int) obsdoc.Related {
		return obsdoc.Related{Kind: obsdoc.RelatedFlowStep, Location: &obsdoc.Location{Path: path, StartLine: line, EndLine: line, BlobSHA256: blob}}
	}
	o := toolObservation{Related: []obsdoc.Related{step("a.go", sha, 5), step("a.go", sha, 22), step("a.go", sha, 30)}}
	got, outside := triageRelated(o, u, "unit", "head")
	want := []obsRelatedInput{{Kind: obsdoc.RelatedFlowStep, EvidenceID: "head", StartLine: 5, EndLine: 5},
		{Kind: obsdoc.RelatedFlowStep, EvidenceID: "unit", StartLine: 22, EndLine: 22}, {Kind: obsdoc.RelatedFlowStep, EvidenceID: "unit", StartLine: 30, EndLine: 30}}
	if !slices.Equal(got, want) || outside {
		t.Fatalf("in context: %+v outside %v", got, outside)
	}
	// Without the header in the request, a header step is outside; so are
	// a step past the header, another file and another blob.
	for _, r := range []obsdoc.Related{step("a.go", sha, 5), step("a.go", sha, 12), step("a.go", sha, 41), step("b.go", sha, 22), step("a.go", "sha-old", 22)} {
		header := ""
		if r.Location.StartLine != 5 {
			header = "head"
		}
		got, outside := triageRelated(toolObservation{Related: []obsdoc.Related{r}}, u, "unit", header)
		if len(got) != 0 || !outside {
			t.Fatalf("%+v: %+v outside %v", r.Location, got, outside)
		}
	}
	// Twelve steps in the unit: the first seven and the sink.
	var long []obsdoc.Related
	for line := 21; line <= 32; line++ {
		long = append(long, step("a.go", sha, line))
	}
	got, outside = triageRelated(toolObservation{Related: long}, u, "unit", "")
	if len(got) != triageRelatedShown || got[6].StartLine != 27 || got[7].StartLine != 32 || !outside {
		t.Fatalf("long flow: %+v outside %v", got, outside)
	}
}

// TC-305: an Opengrep flow and another tool's report of the same construct
// on the same line are one triage group when both rules are of a mergeable
// family (the duplication measured on the fixtures: bandit's shell and SQL
// tests, SpotBugs' path traversal and servlet XSS, clang-tidy's command
// processor); Opengrep's SSRF is a security weakness and stays apart from
// bandit's urlopen; another line never merges.
func TestOpengrepMergesWithOtherToolsOnOneLine(t *testing.T) {
	unit := triageUnit{entry: source.Entry{Path: "app.py"}, span: lineSpan{start: 1, end: 40}}
	diag := func(tool, pack, version, rule string, line int) *triageGroup {
		o := toolObservation{ID: tool + rule, Tool: tool, RuleID: rule, Location: &toolLocation{Path: "app.py", StartLine: line, EndLine: line}}
		return &triageGroup{unit: unit, diags: []triageDiag{{obs: o, def: builtin.DefaultSeverity(tool, pack, version, rule, "", "")}}}
	}
	og := func(rule string, line int) *triageGroup {
		return diag(opengrep.AnalyzerID, opengrep.RulePackID, opengrep.RulePackVersion, rule, line)
	}
	bandit := func(rule string, line int) *triageGroup {
		return diag(python.BanditID, python.BanditRulePackID, python.BanditRulePackVer, rule, line)
	}
	for _, c := range []struct {
		name  string
		other *triageGroup
		rule  string
		line  int
		merge bool
	}{
		{"bandit shell", bandit("B602", 7), "codeye.python.command-injection", 7, true},
		{"bandit SQL", bandit("B608", 9), "codeye.python.sql-injection", 9, true},
		{"bandit markup", bandit("B704", 10), "codeye.python.html-injection", 10, true},
		{"spotbugs path", diag("java.spotbugs", "codeye-java-spotbugs", "1", "PT_RELATIVE_PATH_TRAVERSAL", 11), "codeye.java.path-traversal", 11, true},
		{"spotbugs xss", diag("java.spotbugs", "codeye-java-spotbugs", "1", "XSS_REQUEST_PARAMETER_TO_SERVLET_WRITER", 12), "codeye.java.html-injection", 12, true},
		{"spotbugs sql", diag("java.spotbugs", "codeye-java-spotbugs", "1", "SQL_NONCONSTANT_STRING_PASSED_TO_EXECUTE", 14), "codeye.java.sql-injection", 14, true},
		{"clang-tidy", diag(cpp.ClangTidyID, cpp.ClangTidyRulePackID, cpp.ClangTidyRulePackVersion, "bugprone-command-processor", 13), "codeye.c.command-injection", 13, true},
		{"bandit urlopen", bandit("B310", 15), "codeye.python.ssrf", 15, false},
		{"another line", bandit("B602", 20), "codeye.python.command-injection", 21, false},
	} {
		got := mergeCrossTool([]*triageGroup{og(c.rule, c.line), c.other})
		if merged := len(got) == 1 && got[0].crossTool; merged != c.merge {
			t.Errorf("%s: merged %v", c.name, merged)
		}
	}
}
