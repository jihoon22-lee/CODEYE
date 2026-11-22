package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const jsOptionImplementationPath = "src/notification.js"
const jsOptionTestPath = "test/notification.test.js"
const jsOptionCode = `export async function schedule(topic, {waitForDelivery = true} = {}) {
    if (waitForDelivery) {
        return await confirm(topic);
    }
    return submit(topic);
}
async function confirm(topic) { return "confirmed:" + topic; }
function submit(topic) { return "submitted:" + topic; }
`
const jsOptionTests = `import {test} from "node:test";
import assert from "node:assert/strict";
import {schedule as notify} from "../src/notification.js";

test("dispatch waits for delivery by default", async () => {
    assert.equal(await notify("alert"), "confirmed:alert");
});
test("dispatch can return before delivery", async () => {
    assert.equal(await notify("alert", {waitForDelivery: false}), "submitted:alert");
});
`

func TestJavaScriptOptionWitnessesRequireBoundDecisionsAndAssertions(t *testing.T) {
	for _, tc := range []struct {
		name, query        string
		change             func(map[string]string)
		wantCode, wantPair bool
	}{
		{name: "exported API and option", wantCode: true, wantPair: true},
		{name: "explicit option without API", query: "option", wantCode: true, wantPair: true},
		{name: "wrong symbol despite an equal named option", query: "wrong"},
		{name: "case-sensitive export", query: "case"},
		{name: "wrong option on a valid API", query: "wrong option"},
		{name: "default export bound by relative import", wantCode: true, wantPair: true, change: func(files map[string]string) {
			files[jsOptionImplementationPath] = strings.Replace(jsOptionCode, "export async", "export default async", 1)
			files[jsOptionTestPath] = strings.Replace(jsOptionTests, "{schedule as notify}", "notify", 1)
		}},
		{name: "exported arrow function async", wantCode: true, wantPair: true, change: func(files map[string]string) {
			files[jsOptionImplementationPath] = strings.Replace(jsOptionCode, "async function schedule(", "const schedule = async (", 1)
			files[jsOptionImplementationPath] = strings.Replace(files[jsOptionImplementationPath], "= {}) {", "= {}) => {", 1)
		}},
		{name: "TypeScript parameters", wantCode: true, wantPair: true, change: func(files map[string]string) {
			files["src/notification.ts"] = strings.Replace(files[jsOptionImplementationPath],
				"(topic, {waitForDelivery = true} = {})", "(topic: string, {waitForDelivery = true}: Settings = {}): Promise<string>", 1)
			delete(files, jsOptionImplementationPath)
		}},
		{name: "async resolved assertion", wantCode: true, wantPair: true, change: func(files map[string]string) {
			files[jsOptionTestPath] = strings.Replace(files[jsOptionTestPath],
				`assert.equal(await notify("alert", {waitForDelivery: false}), "submitted:alert")`,
				`await expect(notify("alert", {waitForDelivery: false})).resolves.toBe("submitted:alert")`, 1)
		}},
		{name: "test callback makes no assertion on result", wantCode: true, change: func(files map[string]string) {
			files[jsOptionTestPath] = strings.Replace(files[jsOptionTestPath],
				`assert.equal(await notify("alert", {waitForDelivery: false}), "submitted:alert")`,
				`expect(() => notify("alert", {waitForDelivery: false})).toBeDefined()`, 1)
		}},
		{name: "test declared in unrelated callback", wantCode: true, change: func(files map[string]string) {
			files[jsOptionTestPath] = strings.Replace(jsOptionTests, "test(\"dispatch can return", "function uncalled() { test(\"dispatch can return", 1) + "}"
		}},
		{name: "untested async result", wantCode: true, change: func(files map[string]string) {
			files[jsOptionTestPath] = strings.ReplaceAll(files[jsOptionTestPath], "await notify", "notify")
		}},
		{name: "different ordinary input", wantCode: true, change: func(files map[string]string) {
			files[jsOptionTestPath] = strings.Replace(files[jsOptionTestPath], `notify("alert", {`, `notify("reminder", {`, 1)
		}},
		{name: "wrong imported module", wantCode: true, change: func(files map[string]string) {
			files[jsOptionTestPath] = strings.Replace(files[jsOptionTestPath], "/notification.js", "/unrelated.js", 1)
			files["src/unrelated.js"] = `export async function schedule() { return "submitted:alert"; }`
		}},
		{name: "module extension ambiguity", wantCode: true, change: func(files map[string]string) {
			files["src/notification.ts"] = `export async function schedule() { return "submitted:alert"; }`
		}},
		{name: "competing public implementations", change: func(files map[string]string) {
			files["src/competing.js"] = jsOptionCode
		}},
		{name: "branch in a callback", change: func(files map[string]string) {
			files[jsOptionImplementationPath] = strings.Replace(jsOptionCode, "    if (", "    const neverInvoked = () => {\n    if (", 1)
			files[jsOptionImplementationPath] = strings.Replace(files[jsOptionImplementationPath], "    return submit", "    };\n    return submit", 1)
		}},
		{name: "option modified before decision", change: func(files map[string]string) {
			files[jsOptionImplementationPath] = strings.Replace(jsOptionCode, "    if (", "    waitForDelivery = true;\n    if (", 1)
		}},
		{name: "outside contract scope", query: "scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{jsOptionImplementationPath: jsOptionCode, jsOptionTestPath: jsOptionTests}
			if tc.change != nil {
				tc.change(files)
			}
			entries := map[string]source.Entry{}
			for p := range files {
				entries[p] = source.Entry{Path: p}
			}
			q := witnessQuery{scope: ".", navigationNames: []string{"schedule", "waitForDelivery"}, jsAPINames: []string{"schedule"}, contractIndex: 2}
			switch tc.query {
			case "option":
				q.jsAPINames, q.navigationNames = nil, []string{"waitForDelivery"}
			case "wrong":
				q.jsAPINames, q.navigationNames = []string{"reschedule"}, []string{"reschedule", "waitForDelivery"}
			case "case":
				q.jsAPINames, q.navigationNames = []string{"Schedule"}, []string{"Schedule"}
			case "wrong option":
				q.jsOptionNames = jsDocumentedOptions("`schedule()` accepts the `waitForResponse` option.", []string{"schedule", "waitForResponse"})
			case "scope":
				q.scope = "src"
				tc.wantCode = true
			}
			got := jsOptionTraces([]witnessQuery{q}, files, entries, nil)
			if (len(got) == 1) != tc.wantCode {
				t.Fatalf("trace = %+v, want source = %v", got, tc.wantCode)
			}
			if len(got) == 0 {
				return
			}
			trace := got[0]
			if trace.index != 2 || trace.implementation.line != 1 || trace.implementation.end != 5 ||
				(trace.defaultCheck.line > 0 && trace.override.line > 0) != tc.wantPair {
				t.Fatalf("trace = %+v, want pair = %v", trace, tc.wantPair)
			}
			if tc.wantPair && (trace.defaultCheck.line != 5 || trace.defaultCheck.end != 6 || trace.override.line != 8 || trace.override.end != 9) {
				t.Fatalf("assertion ranges = %+v and %+v", trace.defaultCheck, trace.override)
			}
		})
	}
}

func TestOverviewPrioritizesDocumentedJavaScriptDefaultAndOverride(t *testing.T) {
	const statement = "`schedule()` uses the `waitForDelivery` option to wait for delivery by default."
	sourcePrelude := strings.Repeat("// Another interface is described here.\n", 85)
	testPrelude := strings.Repeat("// Earlier exercises cover another task.\n", 78)
	files := map[string]string{
		"README.md": "# Notification guide\n\n" + strings.Repeat("There are several independent ways to configure a topic.\n", 135) +
			"\n## Delivery options\n\n" + statement + "\nSet it to false to return after submission.\n",
		"package.json":             `{"type":"module"}`,
		jsOptionImplementationPath: sourcePrelude + jsOptionCode,
		jsOptionTestPath:           testPrelude + jsOptionTests,
	}
	for i := range 14 {
		files[fmt.Sprintf("src/aux%02d.js", i)] = "export const anotherState = 0;\n"
	}
	j := treeJob(t, files)
	script := coverageScript(t)
	handled := false
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		for _, d := range in.Documents {
			if strings.Contains(d.Text, statement) {
				return asJSON(docsOutput{Contracts: []record.Contract{{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{d.EvidenceID}}},
					Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
			}
		}
		t.Fatal("the late README section was not sent for review")
		return opencode.Reply{}
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		handled = true
		var code, tests record.Excerpt
		for _, x := range in.Scope.Excerpts {
			switch x.Path {
			case jsOptionImplementationPath:
				code = x
			case jsOptionTestPath:
				tests = x
			}
		}
		if code.Start != 86 || code.End != 90 || tests.Start != 83 || tests.End != 87 ||
			!strings.Contains(code.Text, "waitForDelivery = true") || !strings.Contains(code.Text, "return await confirm(topic)") ||
			!strings.Contains(code.Text, "return submit(topic)") || !strings.Contains(tests.Text, `await notify("alert")`) ||
			!strings.Contains(tests.Text, `{waitForDelivery: false}`) {
			t.Fatalf("priority code %+v and tests %+v", code, tests)
		}
		if !slices.Contains(in.Scope.DirectCalls, directCall{CallerEvidenceID: tests.ID, CalleeEvidenceID: code.ID, Symbol: "schedule"}) {
			t.Fatalf("missing relative import binding from test to API: %+v", in.Scope.DirectCalls)
		}
		if len(in.Scope.Excerpts) > roleMaxCodeFiles || len(opencode.Prompt(q)) > PromptBudget {
			t.Fatal("JavaScript option witnesses exceeded the existing input budget")
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "기본 분기의 확인 결과와 반대 선택의 제출 결과를 비교했다.",
			DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{code.ID, tests.ID},
		}}})
	}
	runRole(t, j, langStub{eco: "javascript", manifest: "package.json", path: jsOptionImplementationPath, line: 87},
		&scriptedCaller{script: script}, "1.27.4")
	r := readRecord(t, j, "investigate")
	if !handled || !slices.ContainsFunc(r.ContractComparisons, func(c record.ContractComparison) bool {
		return c.ContractIndex == 1 && c.Status == "match" && len(c.CodeEvidenceIDs) == 2
	}) {
		t.Fatalf("the checked excerpt IDs were not accepted in the ordinary comparison: %+v", r.ContractComparisons)
	}
}

func TestJavaScriptOptionUsesTwoWindowsForSeparateDeclarationAndDecision(t *testing.T) {
	files := map[string]string{
		jsOptionImplementationPath: strings.Replace(jsOptionCode, "    if", strings.Repeat("\n", 30)+"    if", 1),
		jsOptionTestPath:           jsOptionTests,
	}
	entries := map[string]source.Entry{}
	for p := range files {
		entries[p] = source.Entry{Path: p}
	}
	q := witnessQuery{scope: ".", navigationNames: []string{"schedule"}, jsAPINames: []string{"schedule"}, contractIndex: 1}
	got := jsOptionTraces([]witnessQuery{q}, files, entries, nil)
	if len(got) != 1 || got[0].implementation.line != 1 || got[0].implementation.end != 1 ||
		got[0].branch.line != 32 || got[0].branch.end != 35 || got[0].override.line != 8 {
		t.Fatalf("separate default, branch and tests were not retained: %+v", got)
	}
}
