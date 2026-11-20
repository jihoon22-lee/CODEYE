package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestDocumentedMemberEffectsReachOverview(t *testing.T) {
	const implementation = "index.js"
	const tests = "test.js"
	for _, filler := range []int{0, 12} {
		t.Run(fmt.Sprintf("%d additional files", filler), func(t *testing.T) {
			padding := strings.Repeat("// other setup\n", 61)
			files := map[string]string{
				"package.json": "{}",
				"README.md": "The checkout API provides cart status and lookups.\n" +
					"Use `cart.discardDrafts()` to remove pending drafts. Active checkout continues.\n",
				implementation: padding + "export default function checkout(options) {\n" +
					"  const cart = {};\n  Object.defineProperties(cart, {\n" +
					"    pendingCount: {get: () => pending.length},\n" +
					"    lookup: {value() { return pending[0]; }},\n" +
					"    discardDrafts: {\n      value() {\n        if (options.rejectDrafts) {\n" +
					"          for (const draft of pending) draft.reject('discarded');\n        }\n" +
					"        pending.length = 0;\n      },\n    },\n  });\n  return cart;\n}\n",
				"index.d.ts": padding + "export declare function discardDrafts(): void;\n",
				"index.test-d.ts": padding + "declare const cart: {discardDrafts(): void};\n" +
					"expectType<void>(cart.discardDrafts());\n",
				"benchmark.js": padding + "const cart = createFakeCart();\ncart.discardDrafts();\n",
				tests: "import checkout, {version} from './index.js';\n" + padding +
					"test('count', t => {\n  const cart = checkout({});\n  t.is(cart.pendingCount, 0);\n});\n" +
					"test('lookups', t => {\n  const cart = checkout({});\n" +
					"  t.deepEqual(cart.lookup(), []);\n  t.deepEqual(cart.lookup(), []);\n});\n" +
					"test('discard', async t => {\n  const cart = checkout({rejectDrafts: true});\n" +
					"  const earlier = queueDraft(`draft ${version}`);\n" +
					"  t.is(cart.pendingCount, 2);\n  cart.discardDrafts();\n  t.is(cart.pendingCount, 0);\n" +
					"  await t.throwsAsync(earlier, {message: 'discarded'});\n});\n",
			}
			for i := range filler {
				files[fmt.Sprintf("extras/support%02d.js", i)] = "export const helper = 'ready';\n"
			}
			j := treeJob(t, files)
			script := maxScript(t)
			script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, q)
				id := in.Documents[0].EvidenceID
				var contracts []record.Contract
				for _, statement := range []string{
					"`cart.pendingCount`는 대기 초안 수를 반환한다.",
					"`cart.lookup()`으로 대기 항목을 조회한다.",
					"`cart.lookup()`의 결과를 순서대로 읽는다.",
					"옵션이 없으면 기본값을 사용한다.",
					"`discardDrafts()`는 대기 초안을 제거한다.",
					"거절 옵션이 켜졌을 때 `discardDrafts()`는 대기 초안을 거절한다.",
				} {
					contracts = append(contracts, record.Contract{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{id}})
				}
				return asJSON(docsOutput{
					Contracts: contracts, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
				})
			}
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				if !in.Scope.WholeRun {
					return asJSON(map[string]any{"candidates": []any{}})
				}
				if len(in.Scope.Excerpts) > roleMaxCodeFiles || len(in.Scope.Excerpts) < 2 {
					t.Fatalf("unexpected excerpt count %d", len(in.Scope.Excerpts))
				}
				source, assertion := in.Scope.Excerpts[0], in.Scope.Excerpts[1]
				if source.Path != implementation || source.Start <= 50 || !strings.Contains(source.Text, "draft.reject") ||
					!strings.Contains(source.Text, "pending.length = 0") ||
					assertion.Path != tests || assertion.Start <= 50 || !strings.Contains(assertion.Text, "cart.discardDrafts()") ||
					!strings.Contains(assertion.Text, "cart.pendingCount, 0") || !strings.Contains(assertion.Text, "t.throwsAsync(earlier") {
					t.Fatalf("implementation and checked behavior lost their reservation: %+v / %+v", source, assertion)
				}
				total := 0
				byPathLines, byPathBytes := map[string]int{}, map[string]int{}
				for _, e := range in.Scope.Excerpts {
					n := record.JSONLen(e.Text)
					total += n
					byPathLines[e.Path] += e.End - e.Start + 1
					byPathBytes[e.Path] += n
					if byPathLines[e.Path] > roleMaxCodeLines || byPathBytes[e.Path] > roleMaxCodeFile {
						t.Fatalf("per-file budget exceeded by %s", e.Path)
					}
				}
				if total > roleMaxCodeBytes {
					t.Fatalf("overview exceeds byte budget: %d", total)
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
					ContractIndex: 6, AppliesTo: ".", Status: "match", Reason: "거절 옵션이 켜졌을 때 대기 초안을 거절한 뒤 비운다.",
					DocumentEvidenceIDs: in.Documents.Contracts[5].EvidenceIDs, CodeEvidenceIDs: []string{source.ID, assertion.ID},
				}}})
			}
			_, results := runRole(t, j, langStub{eco: "javascript", manifest: "package.json", path: implementation, line: 69}, &scriptedCaller{script: script}, "1.27.4")
			if results["review.investigate"].Status != domain.TaskSucceeded {
				t.Fatalf("%+v", results["review.investigate"])
			}
			rec := readRecord(t, j, "investigate")
			if len(rec.ContractComparisons) != 1 || !slices.ContainsFunc(rec.CitedEvidence(), func(e record.Excerpt) bool { return e.Path == tests && e.Start > 50 }) {
				t.Fatalf("checked behavior lost from the record: %+v", rec.ContractComparisons)
			}
		})
	}
}

func TestMemberAssertionRequiresReceiverFromTheReadModule(t *testing.T) {
	const source = "export default function create() { return { reset() { return true; } }; }"
	for _, tc := range []struct {
		name, body string
	}{
		{"foreign factory", "import make from 'external';\ntest('reset', t => {const item = make(); t.is(item.reset(), true);});\n"},
		{"unrelated assertion", "import make from './index.js';\ntest('reset', t => {const item = make(); item.reset(); t.is(1, 1);});\n"},
		{"receiver rebound", "import make from './index.js';\ntest('reset', t => {let item = make(); item = mock(); t.is(item.reset(), true);});\n"},
		{"receiver rebound before checking", "import make from './index.js';\ntest('reset', t => {let item = make(); item.reset(); item = mock(); t.is(item.status, true);});\n"},
		{"factory shadowed", "import make from './index.js';\ntest('reset', t => {const make = mock; const item = make(); t.is(item.reset(), true);});\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := treeJob(t, map[string]string{"index.js": source, "test.js": tc.body})
			files := includedFiles(j.Snapshot.Manifest)
			queries := []witnessQuery{{scope: ".", members: []string{"reset"}}}
			m := memberWitnesses{sources: map[memberKey][]contractWitness{}, tests: map[string][]memberTest{}}
			m.observe(files["index.js"], source, queries, false)
			m.observe(files["test.js"], tc.body, queries, true)
			if _, ok := m.choose(queries, files); ok {
				t.Fatal("unbound test promoted as a checked member")
			}
		})
	}
}
