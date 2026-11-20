package review

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestLiteralPythonAPIReachesReport(t *testing.T) {
	const scope = "packages/display"
	const page = scope + "/docs/display.md"
	const impl = scope + "/src/display/count.py"
	const testFile = scope + "/tests/test_count.py"
	const readme = scope + "/README.md"
	const stub = "# Counts\n\n::: display.count\n"
	body := "\"\"\"Count formatting.\"\"\"\n" + strings.Repeat("# Module setup.\n", 55) + `
def display_count(value: int, blocks: bool = False) -> str:
    """Render a quantity for display.

    Examples:
        >>> display_count(90)
        '9.0 pkt'
        >>> display_count(90, True)
        '7.5 blk'

    Args:
        blocks: Use 12 per block when True and 10 per packet by default.
    """
    scale = 12 if blocks else 10
    unit = "blk" if blocks else "pkt"
    return f"{value / scale:.1f} {unit}"
`
	testSource := "import pytest\nfrom display.count import display_count\n" +
		strings.Repeat("# Test setup.\n", 55) + `
@pytest.mark.parametrize(
    "args, expected",
    [
        ([90], "9.0 pkt"),
        ([120], "12.0 pkt"),
        ([90, True], "7.5 blk"),
    ],
)
def test_display_count(args, expected):
    assert display_count(*args) == expected
`
	j := treeJob(t, map[string]string{
		page: stub, impl: body, testFile: testSource,
		readme:                    "Display formatting provides " + "`display_count` for quantities.\n",
		scope + "/pyproject.toml": "[project]\nname='display'\n",
	})
	script := maxScript(t)
	var documented docInput
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		var readmeID string
		for _, e := range in.Documents {
			if e.DirectivePath == page {
				documented = e
			}
			if e.Path == readme {
				readmeID = e.EvidenceID
			}
		}
		if documented.Path != impl || documented.AppliesTo != scope || documented.Class != "python_docstring" ||
			documented.StartLine <= 50 || !strings.Contains(documented.Text, ">>> display_count(90, True)") ||
			!strings.Contains(documented.Text, "'7.5 blk'") || strings.Contains(documented.Text, "scale =") {
			t.Fatalf("missing bounded API documentation: %+v", in.Documents)
		}
		if documented.Text != strings.Join(strings.Split(body, "\n")[documented.StartLine-1:documented.EndLine], "\n") {
			t.Fatal("docstring input differs from the snapshot's physical lines")
		}
		p := scope
		if in.Repair == nil {
			p = "." // the declared package scope may not be widened
		} else if !slices.Contains(in.Repair.OutputErrors, "scope_generalized") {
			t.Fatalf("scope rejection absent: %+v", in.Repair)
		}
		return asJSON(docsOutput{
			Contracts: []record.Contract{
				{Statement: "화면 표시용 `display_count`를 제공한다.", AppliesTo: scope, EvidenceIDs: []string{readmeID}},
				{Statement: "`display_count`는 `blocks=True`일 때 12를 기준으로 나누고 blk를 붙인다.", AppliesTo: p, EvidenceIDs: []string{documented.EvidenceID}},
			},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		if !slices.ContainsFunc(in.Documents.Evidence, func(e docEvidence) bool { return e.DirectivePath == page && e.AppliesTo == scope }) {
			t.Fatalf("handoff lost API page provenance: %+v", in.Documents)
		}
		var codeID, testID string
		for _, e := range in.Scope.Excerpts {
			if e.Path == impl && e.Kind == "code" && e.Start > documented.EndLine &&
				strings.Contains(e.Text, "scale = 12 if blocks else 10") && strings.Contains(e.Text, "return f") {
				codeID = e.ID
			}
			if e.Path == testFile && e.Kind == "test" && strings.Contains(e.Text, "9.0 pkt") && strings.Contains(e.Text, "7.5 blk") &&
				strings.Contains(e.Text, "assert display_count(*args) == expected") {
				testID = e.ID
			}
		}
		if codeID == "" || testID == "" {
			t.Fatalf("missing executable declaration body or static test parameters: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 2, AppliesTo: scope, Status: "match",
			Reason:              "기본값은 90을 10으로 나누어 9.0 pkt, blocks=True이면 12로 나누어 7.5 blk를 반환한다.",
			DocumentEvidenceIDs: []string{documented.EvidenceID}, CodeEvidenceIDs: []string{codeID, testID},
		}}})
	}
	h := Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}}
	plan, results := runRoleWith(t, j, h, "1.27.4")
	for _, key := range []string{"review.documents", "review.investigate"} {
		if results[key].Status != domain.TaskSucceeded {
			t.Fatalf("%s: %+v", key, results[key])
		}
	}
	doc := readRecord(t, j, "documents")
	excerpt := catalogOf(doc.Evidence)[documented.EvidenceID]
	e := includedFiles(j.Snapshot.Manifest)[impl]
	if excerpt.DirectivePath != page || excerpt.Text != "" ||
		excerpt.ID != record.ExcerptID(j.Snapshot.Digest, impl, e.SHA256, documented.StartLine, documented.EndLine) {
		t.Fatalf("docstring record does not bind to the source file and lines: %+v", excerpt)
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 2}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	for _, includeCode := range []bool{true, false} {
		raw, err := report.Marshal(report.Project(r, includeCode))
		if err != nil {
			t.Fatal(err)
		}
		reread, err := report.Unmarshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(reread.Documents.Comparisons) != 2 || reread.Documents.Comparisons[1].Status != "match" ||
			!slices.ContainsFunc(reread.Documents.Evidence, func(ev report.SectionEvidence) bool {
				return ev.ID == excerpt.ID && ev.Kind == "document" && ev.Path == impl && ev.DirectivePath == page
			}) {
			t.Fatalf("report round trip lost its documented API comparison: %+v", reread.Documents)
		}
	}
}

func TestLiteralPythonAPIRejectionAndObjectResolution(t *testing.T) {
	const page = "docs/api.md"
	const impl = "src/panel/values.py"
	const doc = "\"\"\"State label.\"\"\""
	implementation := "...\n\nclass Value:\n    def label(self):\n        " + doc + "\n        return 'ready'\n"
	cases := []struct {
		name, markdown, source string
		extra                  map[string]string
		scope                  domain.Scope
		wantAPI                bool
	}{
		{name: "public method", markdown: "# API\n\n::: panel.values.Value.label\n", source: implementation, wantAPI: true},
		{name: "module file", markdown: "::: panel.values\n", source: implementation, wantAPI: false},
		{name: "ambiguous root and src", markdown: "::: panel.values.Value.label\n", source: implementation, extra: map[string]string{"panel/values.py": implementation}},
		{name: "fenced example", markdown: "```md\n::: panel.values.Value.label\n```\n", source: implementation},
		{name: "nonliteral target", markdown: "::: panel.values.Value.label()\n", source: implementation},
		{name: "with rendering options", markdown: "::: panel.values.Value.label\n    options:\n      members: []\n", source: implementation},
		{name: "source out of scope", markdown: "::: panel.values.Value.label\n", source: implementation, scope: domain.Scope{Kind: "paths", Paths: []string{"docs"}}},
		{name: "unsafe source", markdown: "::: panel.values.Value.label\n", source: implementation + "\npassword = \"placeholder-credential-12345\"\n"},
		{name: "not a docstring", markdown: "::: panel.values.Value.label\n", source: strings.Replace(implementation, doc, "f"+doc, 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{page: c.markdown, impl: c.source}
			for p, text := range c.extra {
				files[p] = text
			}
			j := treeJob(t, files)
			if c.scope.Kind != "" {
				j.Scope = c.scope
			}
			s := roleStage{j: j, ctx: context.Background()}
			e := s.entries()[page]
			n := strings.Count(c.markdown, "\n")
			in := []envdoc.Doc{{Path: page, Class: envdoc.ClassDesign, AppliesTo: ".", SHA256: &e.SHA256, LineCount: &n}}
			excerpts, _, err := s.documentExcerpts(in)
			if err != nil || len(excerpts) != 1 || (excerpts[0].DirectivePath == page) != c.wantAPI {
				t.Fatalf("incorrect document selection: %+v, err %v", excerpts, err)
			}
			if c.wantAPI && (excerpts[0].Path != impl || !strings.Contains(excerpts[0].Text, doc)) {
				t.Fatalf("incorrect resolved public object: %+v", excerpts[0])
			}
		})
	}
}
