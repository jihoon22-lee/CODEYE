package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestOverviewIncludesCompleteDocumentedShortRuntime(t *testing.T) {
	const docsPath = "README.md"
	const codePath = "src/pricing.js"
	const testsPath = "src/pricing.test.js"
	src := "function roundUnits(value) {\n  if (value < 0) return 0;\n  return Math.ceil(value);\n}\n" +
		strings.Repeat("// Helper for invoice display.\n", 29) +
		"export function formatInvoice(units) {\n  return roundUnits(units) + ' units';\n}\n"
	const statement = "`formatInvoice`는 음수 수량을 0으로 표시한다."
	files := map[string]string{
		docsPath:       "# Invoices\n\n`formatInvoice` displays negative quantities as zero.\n",
		"package.json": "{}",
		codePath:       src,
		testsPath: strings.Repeat("// Other pricing checks.\n", 60) +
			"test('formatInvoice handles negative units', () => {\n  expect(formatInvoice(-2)).toBe('0 units');\n});\n",
	}
	for i := range 10 {
		files[fmt.Sprintf("src/aux%02d.js", i)] = "export const state = 0;\n"
	}
	j := treeJob(t, files)
	var handled bool
	script := maxScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		return asJSON(docsOutput{
			Contracts: []record.Contract{{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{in.Documents[0].EvidenceID}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		handled = true
		var x record.Excerpt
		var total int
		for _, e := range in.Scope.Excerpts {
			total += record.JSONLen(e.Text)
			if e.Path == codePath {
				x = e
			}
		}
		if x.Start != 1 || x.End != strings.Count(src, "\n") || x.Text != strings.TrimSuffix(src, "\n") ||
			x.ID != record.ExcerptID(j.Snapshot.Digest, x.Path, x.SHA256, x.Start, x.End) {
			t.Fatalf("short file did not keep the helper and API: %+v", x)
		}
		if len(in.Scope.Excerpts) != roleMaxCodeFiles || total > roleMaxCodeBytes ||
			!slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == testsPath }) {
			t.Fatalf("existing breadth or contract test was lost: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "입력 수량이 음수면 반올림 함수가 0을 반환한다.",
			DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{x.ID},
		}}})
	}
	runRole(t, j, langStub{eco: "javascript", manifest: "package.json", path: codePath, line: 34}, &scriptedCaller{script: script}, "1.27.4")
	if !handled || len(readRecord(t, j, "investigate").ContractComparisons) != 1 {
		t.Fatalf("the complete file's evidence ID was not accepted")
	}
}

func TestOverviewKeepsExactWitnessesWhenWideningWouldDisplaceInput(t *testing.T) {
	anchor := record.Excerpt{ID: "anchor", Path: "product.js", Kind: "code", Start: 20, End: 21, Text: "return value;"}
	full := record.Excerpt{ID: "full", Path: anchor.Path, Kind: "code", Start: 1, End: 21, Text: "function helper() {}\nreturn value;"}
	other := record.Excerpt{ID: "other", Path: "another.js", Kind: "code", Start: 1, End: 1, Text: "export const answer = 42;"}
	s := &roleStage{role: record.StageInvestigate}
	for _, tc := range []struct {
		name   string
		change func([]record.Excerpt, *corpusLinks)
		build  func([]record.Excerpt) ([]byte, error)
	}{
		{name: "request budget", build: func(in []record.Excerpt) ([]byte, error) {
			if in[0].ID == full.ID {
				return make([]byte, PromptBudget), nil
			}
			return marshal(in)
		}},
		{name: "corpus budget", change: func(in []record.Excerpt, _ *corpusLinks) {
			in[1].Text = strings.Repeat("x", roleMaxCodeBytes-record.JSONLen(anchor.Text)-2)
		}},
		{name: "direct call link", change: func(_ []record.Excerpt, links *corpusLinks) {
			links.directCalls = []directCall{{CallerEvidenceID: anchor.ID, CalleeEvidenceID: other.ID, Symbol: "helper"}}
		}},
		{name: "second same-file window", change: func(in []record.Excerpt, _ *corpusLinks) { in[1].Path = anchor.Path }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := []record.Excerpt{anchor, other}
			links := corpusLinks{wholeWitness: map[string]record.Excerpt{anchor.ID: full}}
			build := tc.build
			if build == nil {
				build = func(in []record.Excerpt) ([]byte, error) { return marshal(in) }
			}
			if tc.change != nil {
				tc.change(initial, &links)
			}
			got, changed := s.widenOverviewWitnesses(initial, links, investigateContract, build)
			if changed || !slices.Equal(got, initial) {
				t.Fatalf("widening displaced or changed an existing window: %+v", got)
			}
		})
	}
}
