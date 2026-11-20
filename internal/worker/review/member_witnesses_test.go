package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
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
					if in.Scope.MemberTest != nil {
						t.Fatal("member test hint escaped to a slice")
					}
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
				wantHint := memberTestHint{ImplementationEvidenceID: source.ID, TestEvidenceID: assertion.ID, Member: "discardDrafts"}
				if in.Scope.MemberTest == nil || *in.Scope.MemberTest != wantHint {
					t.Fatalf("request did not link the exact emitted windows: %+v", in.Scope.MemberTest)
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
				comparison := record.ContractComparison{
					ContractIndex: 6, AppliesTo: ".", Status: "match", Reason: "거절 옵션이 켜졌을 때 대기 초안을 거절한 뒤 비운다.",
					DocumentEvidenceIDs: in.Documents.Contracts[5].EvidenceIDs, CodeEvidenceIDs: []string{source.ID, assertion.ID},
				}
				if filler == 0 {
					comparison.Status = "unknown"
					comparison.Reason = "호출자의 사용 조건을 확인할 근거가 부족하다."
					comparison.CodeEvidenceIDs = []string{}
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{comparison}})
			}
			_, results := runRole(t, j, langStub{eco: "javascript", manifest: "package.json", path: implementation, line: 69}, &scriptedCaller{script: script}, "1.27.4")
			if results["review.investigate"].Status != domain.TaskSucceeded {
				t.Fatalf("%+v", results["review.investigate"])
			}
			rec := readRecord(t, j, "investigate")
			if len(rec.ContractComparisons) != 1 {
				t.Fatalf("contract comparison lost from the accepted record: %+v", rec.ContractComparisons)
			}
			if filler == 0 {
				if comparison := rec.ContractComparisons[0]; comparison.Status != "unknown" || len(comparison.CodeEvidenceIDs) != 0 {
					t.Fatalf("input hint changed the model's unanswered comparison: %+v", comparison)
				}
			} else if !slices.ContainsFunc(rec.CitedEvidence(), func(e record.Excerpt) bool { return e.Path == tests && e.Start > 50 }) {
				t.Fatalf("checked behavior lost from the record: %+v", rec.ContractComparisons)
			}
		})
	}
}

func TestMemberTestHintFollowsRequestTrimming(t *testing.T) {
	excerpts := []record.Excerpt{
		{ID: "implementation", Kind: "code", Path: "index.js", SHA256: "blob-code", Start: 9, End: 11, Text: "object.reset();"},
		{ID: "assertion", Kind: "test", Path: "test.js", SHA256: "blob-test", Start: 24, End: 26, Text: "assert.equal(result, true);"},
	}
	makeWitness := func(e record.Excerpt) contractWitness {
		return contractWitness{entry: source.Entry{Path: e.Path, SHA256: e.SHA256}, line: e.Start, end: e.End}
	}
	base := investigateScope{WholeRun: true}
	links := corpusLinks{member: memberPair{source: makeWitness(excerpts[0]), test: makeWitness(excerpts[1]), name: "reset"}}
	build := func(list []record.Excerpt) ([]byte, error) {
		return marshal(base.withExcerpts(list, links))
	}
	full, err := build(excerpts)
	if err != nil || !strings.Contains(string(full), `"member_test":`) {
		t.Fatalf("full request lacks the optional member test link: %s, %v", full, err)
	}
	one, err := build(excerpts[:1])
	if err != nil {
		t.Fatal(err)
	}
	s := &roleStage{role: "investigate"}
	size, _ := s.fits("", one, true)
	instructions := strings.Repeat("x", PromptBudget-size)
	sent, trimmed, err := s.fitExcerpts(instructions, 1, excerpts, build)
	if err != nil || !trimmed || len(sent) != 1 || base.withExcerpts(sent, links).MemberTest != nil {
		t.Fatalf("request trimming left a dangling member test link: %v, %t, %v", sent, trimmed, err)
	}
	cut := slices.Clone(excerpts)
	cut[1].End--
	if base.withExcerpts(cut, links).MemberTest != nil {
		t.Fatal("same file and a shortened range was accepted as the exact test window")
	}
	noPair, err := marshal(base.withExcerpts(excerpts, corpusLinks{}))
	if err != nil || strings.Contains(string(noPair), `"member_test"`) {
		t.Fatalf("legacy requests gained a member test field: %s, %v", noPair, err)
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

func TestMemberTestHintSelectionRespectsContractScope(t *testing.T) {
	const implementation = "app/index.js"
	const test = "outside/test.js"
	files := map[string]string{
		implementation: "export default function make() { return {reset() { return true; }}; }\n",
		test:           "import make from '../app/index.js';\ntest('reset', t => {const item = make(); t.is(item.reset(), true);});\n",
	}
	j := treeJob(t, files)
	entries := includedFiles(j.Snapshot.Manifest)
	whole := []witnessQuery{{scope: ".", members: []string{"reset"}}}
	m := memberWitnesses{sources: map[memberKey][]contractWitness{}, tests: map[string][]memberTest{}}
	m.observe(entries[implementation], files[implementation], whole, false)
	m.observe(entries[test], files[test], whole, true)
	if _, ok := m.choose(whole, entries); !ok {
		t.Fatal("fixture did not qualify under the broader contract")
	}
	narrow := []witnessQuery{{scope: "app", members: []string{"reset"}}}
	if _, ok := m.choose(narrow, entries); ok {
		t.Fatal("linked a test outside the contract's scope")
	}
}
