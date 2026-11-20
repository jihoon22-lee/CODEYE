package review

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestContractWitnessesReachCitedSnapshotLines(t *testing.T) {
	const implementation = "app/src/dispatch.ts"
	const tests = "app/src/dispatch.test.ts"
	const hidden = "app/src/rotation.ts"
	const other = "elsewhere/dispatch.ts"
	const prompts = "app/prompts.py"
	padding := strings.Repeat("// setup for the remaining routes\n", 80)
	files := map[string]string{
		"app/README.md":     "# 명령\n\n보관 키를 갱신하려면 `rotate-vault`를 실행한다. 이미 갱신했으면 '기존 키 유지' 오류를 보고한다.\n",
		"app/docs/usage.md": "# 사용자 명령 안내\n\n`rotate-vault` 명령에는 별도 설정이 필요 없다.\n",
		"app/package.json":  "{}\n",
		implementation:      padding + "export function dispatch(command: string) {\n  switch (command) {\n    case \"rotate-vault\":\n      return renewKey(\"기존 키 유지\");\n  }\n}\n",
		tests:               padding + "test(\"rotate-vault\", () => {\n  expect(dispatch(\"rotate-vault\")).toBe(\"기존 키 유지\");\n});\n",
		hidden:              padding + "export function rotateVault() { return dispatch(\"rotate-vault\"); }\n",
		other:               padding + "export function rotateVault() { return dispatch(\"rotate-vault\"); }\n",
		prompts:             strings.Repeat("# unrelated modules\n", 52) + "prompt = \"\"\"\nrotate-vault를 실행하면 기존 키 유지 오류를 보고한다.\n\"\"\"\n",
	}
	for i := range 10 {
		dir := fmt.Sprintf("app/a%02d", i)
		files[dir+"/AGENTS.md"] = "Conventions for contributors.\n"
		files[dir+"/entry.ts"] = "export const endpoint = 3;\n"
	}
	j := treeJob(t, files)
	stub := langStub{eco: "javascript", path: implementation, line: 83, manifest: "app/package.json", secrets: "hit", secretPath: hidden}
	script := maxScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		if len(in.Documents) != roleMaxDocs || in.Documents[0].Path != "app/README.md" || in.Documents[1].Path != "app/docs/usage.md" {
			t.Fatalf("user docs did not lead under the document cap: %+v", in.Documents)
		}
		return asJSON(docsOutput{
			Contracts: []record.Contract{{Statement: "`rotate-vault`를 실행하면 보관 키를 갱신하고, 이미 갱신했으면 기존 키 유지 오류를 보고한다.", AppliesTo: "app", EvidenceIDs: []string{in.Documents[0].EvidenceID}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		got := map[string]record.Excerpt{}
		for _, e := range in.Scope.Excerpts {
			got[e.Path] = e
		}
		if got[implementation].Start <= 50 || got[tests].Start <= 50 ||
			!strings.Contains(got[implementation].Text, "기존 키 유지") || !strings.Contains(got[tests].Text, "expect(dispatch") ||
			got[hidden].ID != "" || got[other].Start > 1 || got[prompts].Start > 1 {
			t.Fatalf("implementation and test not selected within the contract scope: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 1, AppliesTo: "app", Status: "match", Reason: "명령 분기와 테스트가 결과를 확인한다.",
			DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{got[implementation].ID, got[tests].ID},
		}}})
	}
	f := &scriptedCaller{script: script}
	_, results := runRole(t, j, stub, f, "1.27.4")
	for _, task := range []string{"review.documents", "review.investigate", record.TaskScope} {
		if r := results[task]; r.Status != domain.TaskSucceeded {
			t.Fatalf("%s %+v", task, r)
		}
	}
	r := readRecord(t, j, "investigate")
	if len(r.ContractComparisons) != 1 || !slices.Contains(r.Limitations, limSecretWithheld) || !slices.Contains(r.Limitations, "excerpt_only") {
		t.Fatalf("comparison or withheld file missing: %+v", r)
	}
	root, err := os.OpenRoot(j.Snapshot.SnapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entries := includedFiles(j.Snapshot.Manifest)
	count := 0
	for _, e := range r.CitedEvidence() {
		if e.Path == implementation || e.Path == tests {
			excerpt, ok := sourceexcerpt.Read(root, entries[e.Path], e.Start)
			if !ok || !strings.Contains(excerpt.Text, "rotate-vault") {
				t.Fatalf("the report anchor on %s did not reach the relevant lines: %+v", e.Path, excerpt)
			}
			count++
		}
	}
	if count != 2 {
		t.Fatalf("got %d cited code/test excerpts", count)
	}

	b, err := os.ReadFile(scopePath(j))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := record.DecodeScope(b, scopeExpect(j))
	if err != nil {
		t.Fatal(err)
	}
	sliced := map[string]bool{}
	for _, slot := range scope.Slices {
		for _, file := range slot.Files {
			if file.Path == implementation || file.Path == tests || file.Path == hidden {
				t.Fatalf("already offered or withheld file in a slice: %+v", file)
			}
			sliced[file.Path] = true
		}
	}
	displaced := 0
	for _, p := range planSlices(j.Snapshot.Manifest, j.Scope, Handler{}.secondary()).Overview {
		if !slices.Contains(scope.Overview.Offered, p.Path) {
			displaced++
			if !sliced[p.Path] {
				t.Fatalf("file displaced by a contract witness was not given a slice: %s", p.Path)
			}
		}
	}
	if displaced == 0 {
		t.Fatal("fixture did not displace an ordinary overview file")
	}
}
