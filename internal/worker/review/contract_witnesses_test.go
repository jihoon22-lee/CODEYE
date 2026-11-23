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
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
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
	_, results := runRoleSmall(t, j, stub, f, "1.27.4")
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
			if file.Path == hidden {
				t.Fatalf("withheld file in a slice: %+v", file)
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

func TestContractDirectCallsReachUnmentionedDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name, ext, eco, manifest, caller, callee, branch, outcome string
	}{
		{
			name: "Python absolute local import with alias", ext: ".py", eco: "python", manifest: "pyproject.toml",
			caller: "from app.calculation import compute as calculate\n\n" + strings.Repeat("# other commands\n", 63) +
				"def dispatch(command, count):\n    if command == 'estimate-invoice':\n        return calculate(count)\n",
			callee: strings.Repeat("# other rules\n", 58) + "def compute(count):\n    if count <= 0:\n        raise ValueError('quantity required')\n    return count * 9\n",
			branch: "command == 'estimate-invoice'", outcome: "raise ValueError('quantity required')",
		},
		{
			name: "TypeScript relative named import with alias", ext: ".ts", eco: "javascript", manifest: "package.json",
			caller: "import { compute as calculate } from './calculation';\n\n" + strings.Repeat("// other commands\n", 63) +
				"export function dispatch(command: string, count: number) {\n  if (command === 'estimate-invoice') {\n    return calculate(count);\n  }\n}\n",
			callee: strings.Repeat("// other rules\n", 58) + "export function compute(count: number): number {\n  if (count <= 0) {\n    throw new Error('quantity required');\n  }\n  return count * 9;\n}\n",
			branch: "command === 'estimate-invoice'", outcome: "throw new Error('quantity required')",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, callee := "app/dispatch"+tc.ext, "app/calculation"+tc.ext
			files := map[string]string{
				"README.md":       "# Commands\n\nUse `estimate-invoice` to see the invoice subtotal before confirming.\n",
				tc.manifest:       "{}\n",
				caller:            tc.caller,
				callee:            tc.callee,
				"app/__init__.py": "",
			}
			// Ordinary breadth is already full when the implementation is
			// found, and the definition does not repeat the command name.
			for i := range 12 {
				files[fmt.Sprintf("aux%02d%s", i, tc.ext)] = strings.Repeat("# unrelated line\n", 20)
			}
			j := treeJob(t, files)
			script := maxScript(t)
			script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, q)
				return asJSON(docsOutput{
					Contracts: []record.Contract{{Statement: "`estimate-invoice` 명령으로 확정 전에 합계를 확인한다.", AppliesTo: ".", EvidenceIDs: []string{in.Documents[0].EvidenceID}}},
					Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
				})
			}
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				if !in.Scope.WholeRun {
					if len(in.Scope.DirectCalls) > 0 {
						t.Fatal("overview direct call leaked into a slice")
					}
					return asJSON(map[string]any{"candidates": []any{}})
				}
				var total int
				byID := make(map[string]record.Excerpt)
				for _, e := range in.Scope.Excerpts {
					byID[e.ID] = e
					total += record.JSONLen(e.Text)
				}
				if len(in.Scope.Excerpts) > roleMaxCodeFiles || total > roleMaxCodeBytes {
					t.Fatalf("overview over budget: %d files, %d bytes", len(in.Scope.Excerpts), total)
				}
				if len(in.Scope.DirectCalls) != 1 {
					t.Fatalf("expected one direct call, got %+v, excerpts %+v", in.Scope.DirectCalls, in.Scope.Excerpts)
				}
				link := in.Scope.DirectCalls[0]
				from, to := byID[link.CallerEvidenceID], byID[link.CalleeEvidenceID]
				if link.Symbol != "compute" || from.Path != caller || to.Path != callee ||
					from.Start <= 50 || to.Start <= 50 || !strings.Contains(from.Text, tc.branch) ||
					!strings.Contains(from.Text, "calculate(count)") || !strings.Contains(to.Text, tc.outcome) ||
					strings.Contains(to.Text, "estimate-invoice") {
					t.Fatalf("call hint does not reach verified implementation: %+v / %+v / %+v", link, from, to)
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
					ContractIndex: 1, AppliesTo: ".", Status: "partial", Reason: "호출이 수량을 전달하고 구현에는 양수 수량 조건이 있다.",
					DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{from.ID, to.ID},
				}}})
			}
			_, results := runRoleSmall(t, j, langStub{eco: tc.eco, path: caller, line: 68, manifest: tc.manifest}, &scriptedCaller{script: script}, "1.27.4")
			if result := results["review.investigate"]; result.Status != domain.TaskSucceeded {
				t.Fatalf("investigate result %+v", result)
			}
			r := readRecord(t, j, "investigate")
			if len(r.ContractComparisons) != 1 || len(r.ContractComparisons[0].CodeEvidenceIDs) != 2 {
				t.Fatalf("direct call was not citable: %+v", r.ContractComparisons)
			}
			b, err := os.ReadFile(scopePath(j))
			if err != nil {
				t.Fatal(err)
			}
			scope, err := record.DecodeScope(b, scopeExpect(j))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{caller, callee} {
				if !slices.Contains(scope.Overview.Offered, p) {
					t.Fatalf("%s missing from scope ledger", p)
				}
			}
		})
	}
}

func TestContractDirectCallFallback(t *testing.T) {
	const caller = "app/command.ts"
	const callee = "app/rules.ts"
	const docsPath = "app/README.md"
	const lexicalAnchor = "quote-order"
	const binding = "import { calculate } from './rules';\n"
	callerBody := strings.Repeat("// additional commands\n", 66) +
		"export function run(command: string, amount: number) {\n" +
		"  if (command === 'quote-order') return calculate(amount);\n}\n"
	calleeBody := strings.Repeat("// other calculations\n", 61) +
		"export function calculate(amount: number) {\n  return amount * 2;\n}\n"
	for _, tc := range []struct {
		name, caller, callee string
		alternative          bool
		withheld             bool
		pullRequest          bool
	}{
		{name: "external", caller: strings.Replace(binding, "./rules", "external-rules", 1)},
		{name: "multiple module candidates", caller: binding, callee: calleeBody, alternative: true},
		{name: "screened", caller: binding, withheld: true},
		{name: "generated", caller: binding, callee: "// @generated\n" + calleeBody},
		{name: "unchanged target in PR", caller: binding, pullRequest: true},
		{name: "no visible outcome", caller: binding, callee: strings.Replace(calleeBody, "  return", strings.Repeat("  // details\n", 25)+"  return", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				docsPath: "# Commands\n\nUse `quote-order` to see the price.\n",
				caller:   tc.caller + callerBody,
				callee:   calleeBody,
			}
			if tc.callee != "" {
				files[callee] = tc.callee
			}
			if tc.alternative {
				files["app/rules.js"] = "export function calculate(amount) { return amount; }\n"
			}
			j := treeJob(t, files)
			s := &roleStage{h: Handler{Budget: smallBudget}, ctx: t.Context(), j: j, role: "investigate"}
			if tc.withheld {
				s.screen.withheld = map[string]bool{callee: true}
			}
			if tc.pullRequest {
				s.j.PullRequest = &domain.PullRequestJob{Changed: []string{caller}}
			}
			e := includedFiles(j.Snapshot.Manifest)[docsPath]
			id := record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, 3)
			d := record.Record{
				Evidence:  []record.Excerpt{{ID: id, Path: e.Path, SHA256: e.SHA256, Start: 1, End: 3, Kind: "document"}},
				Contracts: []record.Contract{{Statement: "`quote-order`로 가격을 확인한다.", AppliesTo: "app", EvidenceIDs: []string{id}}},
			}
			code, links, _, err := s.codeCorpus(d)
			if err != nil {
				t.Fatal(err)
			}
			if len(links.directCalls) != 0 || code[0].Path != caller || !strings.Contains(code[0].Text, lexicalAnchor) {
				t.Fatalf("fallback did not retain the relevant caller: %+v, %+v", links, code)
			}
			if tc.withheld && (!slices.Contains(s.rec.Limitations, limSecretWithheld) ||
				slices.ContainsFunc(code, func(e record.Excerpt) bool { return e.Path == callee })) {
				t.Fatalf("withheld source exposed: %+v", code)
			}
		})
	}
}

func TestContractDirectCallsRejectUncertainBindings(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		python       bool
	}{
		{"Python parameter shadows import", "from .module import calculate\ndef run(calculate):\n    return calculate(1)\n", true},
		{"Python wildcard overwrites import", "from .module import calculate\nfrom .other import *\ncalculate(1)\n", true},
		{"Python dynamic name", "from .module import calculate\nglobals()['calculate'] = another\ncalculate(1)\n", true},
		{"JavaScript parameter shadows import", "import { calculate } from './module';\nfunction run(calculate) { return calculate(1); }", false},
		{"JavaScript string and comment", "import { calculate } from './module';\nconst example = \"calculate(1)\";\n// calculate(1);", false},
		{"JavaScript object method", "import { calculate } from './module';\nconst example = { calculate(x) { return 1; } };", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if calls := importedCalls(callTokens(tc.source, tc.python), tc.python); len(calls) != 0 {
				t.Fatalf("ambiguous binding treated as a call: %+v", calls)
			}
		})
	}
}

func TestContractWitnessPriorityWithProductAndMaintenanceSources(t *testing.T) {
	const maintainer = "scripts/publish_release.py"
	const maintainerTest = "tests/test_publish_release.py"
	const product = "apps/desk/src/quotes.py"
	relevant := "def estimate_order():\n    if quote_order: return 'current price'\n"
	for _, tc := range []struct {
		name, statement string
		product         bool
		preferred       string
	}{
		{"product behavior", "`estimate-order` 명령은 현재 가격을 표시한다.", true, product},
		{"explicit release operation", "`estimate-order` 릴리스 과정은 현재 가격을 확인한다.", true, maintainer},
		{"explicit tool path", "`scripts/publish_release.py`의 `estimate-order` 명령을 사용한다.", true, maintainer},
		{"command line tool without product source", "`estimate-order` 명령은 현재 가격을 표시한다.", false, maintainer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const padding = 70
			code := strings.Repeat("# preceding work\n", padding) + relevant
			files := map[string]string{
				"README.md": "# Commands\n\n" + tc.statement + "\n\nRelease notes are available.\n",
				maintainer: strings.Repeat("# release procedures\n", padding) +
					"def estimate_order():\n    if quote_order and current_price: return 'estimate-order'\n",
				maintainerTest: strings.Repeat("# release procedures\n", padding) +
					"def test_estimate_order():\n    assert estimate_order() == 'current price'\n",
			}
			if tc.product {
				files[product] = code
			}
			j := treeJob(t, files)
			e := includedFiles(j.Snapshot.Manifest)["README.md"]
			id := record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, 5)
			s := &roleStage{h: Handler{Budget: smallBudget}, ctx: t.Context(), j: j, role: "investigate"}
			selected, err := s.contractWitnesses(planCodeGroups(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary), record.Record{
				Evidence:  []record.Excerpt{{ID: id, Path: e.Path, SHA256: e.SHA256, Start: 1, End: 5, Kind: "document"}},
				Contracts: []record.Contract{{Statement: tc.statement, AppliesTo: ".", EvidenceIDs: []string{id}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			witnesses := selected.witnesses
			if len(witnesses) == 0 || witnesses[0].entry.Path != tc.preferred || witnesses[0].line <= 50 {
				t.Fatalf("first witness should come from %s: %+v", tc.preferred, witnesses)
			}
			if tc.preferred == product && slices.ContainsFunc(witnesses, func(w contractWitness) bool { return w.entry.Path == maintainerTest }) {
				t.Fatalf("maintenance test was promoted for a product contract: %+v", witnesses)
			}
		})
	}
}

func TestContractDirectCallsBoundTheNumberOfSites(t *testing.T) {
	const binding = "from .module import calculate\n"
	const call = "calculate(1)\n"
	atLimit := binding + strings.Repeat(call, callMaxSites)
	if got := importedCalls(callTokens(atLimit, true), true); len(got) != callMaxSites {
		t.Fatalf("got %d calls at limit %d", len(got), callMaxSites)
	}
	if got := importedCalls(callTokens(atLimit+call, true), true); len(got) != 0 {
		t.Fatalf("expected lexical fallback above the call-site limit, got %d", len(got))
	}
}

func TestSpareOverviewWindowsPreserveFileAndLineCoverage(t *testing.T) {
	const implementation = "main.js"
	body := strings.Repeat("// leading context\n", 65) +
		"export function refresh(action) {\n  if (action === 'refresh-record') return 'refreshed';\n}\n" +
		strings.Repeat("// alternate behavior\n", 44) +
		"export function archive(action) {\n  if (action === 'archive-record') return 'archived';\n}\n"
	files := map[string]string{
		"README.md":    "Run `refresh-record` to refresh one record.\nRun `archive-record` to archive one record.\n",
		"package.json": "{}", implementation: body,
	}
	// The worst-case prefix cannot include all six files, but in the real
	// request each of these five excerpts costs a single short line.
	for i := range 5 {
		files[fmt.Sprintf("helpers/part%d.js", i)] = "export const p = 1;\n// " + strings.Repeat("long comment ", 370) + "\n"
	}
	j := treeJob(t, files)
	script := maxScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		id := in.Documents[0].EvidenceID
		return asJSON(docsOutput{
			Contracts: []record.Contract{
				{Statement: "`refresh-record`는 레코드를 갱신한다.", AppliesTo: ".", EvidenceIDs: []string{id}},
				{Statement: "`archive-record`는 레코드를 보관한다.", AppliesTo: ".", EvidenceIDs: []string{id}},
			},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	var lines, used int
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		// The implementation travels as its units (whole declarations)
		// beside the selector windows; a window inside a unit is a
		// reference and adds no bytes.
		seen := map[string]bool{}
		var windows []record.Excerpt
		total := 0
		for _, e := range in.Scope.Excerpts {
			seen[e.Path] = true
			total += record.JSONLen(e.Text)
			if e.Path == implementation {
				windows = append(windows, e)
			}
		}
		covered := map[int]bool{}
		for k, w := range windows {
			for line := w.Start; line <= w.End; line++ {
				covered[line] = true
			}
			if !slices.ContainsFunc(windows[:k], func(x record.Excerpt) bool { return x.Start <= w.Start && w.End <= x.End }) &&
				!slices.ContainsFunc(windows[k+1:], func(x record.Excerpt) bool {
					return x.Start <= w.Start && w.End <= x.End && (x.Start != w.Start || x.End != w.End)
				}) {
				used += record.JSONLen(w.Text)
			}
		}
		lines = len(covered)
		has := func(text string) bool {
			return slices.ContainsFunc(windows, func(e record.Excerpt) bool { return strings.Contains(e.Text, text) })
		}
		if len(seen) != 6 || total > smallBudget.limits().source || !has("'refreshed'") || !has("'archived'") ||
			lines != strings.Count(body, "\n") {
			var brief []string
			for _, e := range in.Scope.Excerpts {
				brief = append(brief, fmt.Sprintf("%s:%d-%d/%s/%d", e.Path, e.Start, e.End, e.Origin, len(e.Text)))
			}
			t.Fatalf("unbounded or incomplete implementation: %v total %d lines %d used %d", brief, total, lines, used)
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	_, results := runRoleSmall(t, j, langStub{eco: "javascript", manifest: "package.json", path: implementation, line: 67}, &scriptedCaller{script: script}, "1.27.4")
	if result := results[record.TaskScope]; result.Status != domain.TaskSucceeded {
		t.Fatalf("scope task: %+v", result)
	}
	b, err := os.ReadFile(scopePath(j))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := record.DecodeScope(b, scopeExpect(j))
	if err != nil {
		t.Fatal(err)
	}
	// The overview holds every readable line; only the helpers' lines
	// longer than a unit stay unsent, and they are recorded.
	if len(scope.Overview.Offered) != 6 || len(scope.Overview.Files) != 6 || len(scope.Slices) != 0 || len(scope.Omitted) != 5 ||
		slices.ContainsFunc(scope.Omitted, func(o record.ScopeOmission) bool { return o.Reason != record.ScopeReasonLineLimit }) {
		t.Fatalf("scope counted a window as another file: %+v", scope)
	}
	if !slices.ContainsFunc(scope.Overview.Files, func(f record.ScopeFile) bool {
		return f.Path == implementation && f.State == record.FileCoveredByOverview &&
			f.PlannedLines == lines && f.PlannedBytes == used && f.TotalLines == strings.Count(body, "\n")
	}) {
		t.Fatalf("scope did not sum the two implementation windows: %+v", scope.Overview.Files)
	}
}

func TestRuntimeContractsPreferCodeToBenchmarksAndDeclarations(t *testing.T) {
	const implementation = "index.js"
	const declaration = "index.d.ts"
	const benchmark = "benchmark.js"
	for _, tc := range []struct {
		name, statement, want string
		runtime               bool
	}{
		{"runtime", "`queueLimit`는 입력 수를 제한한다.", implementation, true},
		{"types-only scope", "`queueLimit`는 입력 수를 제한한다.", declaration, false},
		{"explicit type contract", "`index.d.ts`에서 `queueLimit` 타입을 제공한다.", declaration, true},
		{"explicit performance contract", "`benchmark.js`는 `queueLimit` 처리량을 측정한다.", benchmark, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pad := strings.Repeat("// definitions\n", 69)
			files := map[string]string{
				"README.md":       tc.statement + "\n",
				declaration:       pad + "export declare function queueLimit(queueLimit: number): number;\n",
				"index.test-d.ts": "type queueLimit = number;\n",
				"test.js":         "test('an example', () => {});\n",
			}
			if tc.runtime {
				files[implementation] = pad + "export function queueLimit(input) { return input; }\n"
				files[benchmark] = pad + "export function benchmark(queueLimit) { return queueLimit(); }\n"
			}
			j := treeJob(t, files)
			e := includedFiles(j.Snapshot.Manifest)["README.md"]
			id := record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, 1)
			s := &roleStage{h: Handler{Budget: smallBudget}, ctx: t.Context(), j: j, role: "investigate"}
			selected, err := s.contractWitnesses(planCodeGroups(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary), record.Record{
				Evidence:  []record.Excerpt{{ID: id, Path: e.Path, SHA256: e.SHA256, Start: 1, End: 1, Kind: "document"}},
				Contracts: []record.Contract{{Statement: tc.statement, AppliesTo: ".", EvidenceIDs: []string{id}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(selected.witnesses) == 0 || selected.witnesses[0].entry.Path != tc.want {
				t.Fatalf("wanted first %s, got %+v", tc.want, selected.witnesses)
			}
		})
	}
}

func TestCompileConditionWitnessRequiresExecutableBranch(t *testing.T) {
	const name = "RETAIN_DRAFTS"
	terms := map[string]int{}
	addWitnessWords(terms, "`"+name+"`", 4)
	query := []witnessQuery{{scope: ".", terms: terms, statement: terms}}
	for _, tc := range []struct {
		name, path, source string
		line               int
	}{
		{"active C branch", "cache.c", "\n#if RETAIN_DRAFTS\nreturn keepDrafts();\n#endif", 2},
		{"alternate C++ branch", "cache.cpp", "#if STORE_ALL\nreturn storeAll();\n#elif RETAIN_DRAFTS\nkeepDrafts();\n#endif", 3},
		{"define alone", "cache.c", "#define RETAIN_DRAFTS 1\nreturn keepDrafts();", 0},
		{"include alone", "cache.c", "#include <RETAIN_DRAFTS>\nreturn keepDrafts();", 0},
		{"header-only branch", "cache.h", "#ifndef RETAIN_DRAFTS\nextern int keepDrafts(void);\n#define KEEP 1\n#endif", 0},
		{"opposite branch", "cache.c", "#ifdef RETAIN_DRAFTS\n#define KEEP 1\n#else\nreturn keepDrafts();\n#endif", 0},
		{"commented statement", "cache.cpp", "#ifdef RETAIN_DRAFTS\n/*\nreturn keepDrafts();\n*/\n#endif", 0},
		{"macro continuation", "cache.c", "#ifdef RETAIN_DRAFTS\n#define OUT \\\nreturn keepDrafts();\n#endif", 0},
		{"condition in a raw string", "cache.cpp", "const char* example = R\"file(\n#if RETAIN_DRAFTS\nkeepDrafts();\n#endif\n)file\";", 0},
		{"branch outside excerpt", "cache.c", "#ifdef RETAIN_DRAFTS\n" + strings.Repeat("// details\n", witnessMaxLines) + "return keepDrafts();\n#endif", 0},
		{"comment syntax in Python", "cache.py", "#if RETAIN_DRAFTS\nreturn keepDrafts()", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := matchWitnessLines(tc.source, source.Entry{Path: tc.path}, query)[0]
			if got.line != tc.line || (tc.line > 0 && !got.direct) {
				t.Fatalf("compile guard witness %+v, want anchored line %d", got, tc.line)
			}
		})
	}
}

func TestOverviewKeepsOneDocumentedCompileBranch(t *testing.T) {
	const readme = "README.md"
	const implementation = "cache.c"
	file := strings.Repeat("// declarations elsewhere\n", 58) +
		"int loadDrafts(void) { return loadAll(); }\n" +
		strings.Repeat("// other behavior\n", 27) +
		"int reopen(void) {\n#if RETAIN_DRAFTS\nreturn keepDrafts();\n#else\nreturn resetDrafts();\n#endif\n}\n"
	files := map[string]string{
		readme:         "`loadDrafts` loads the current drafts.\n`RETAIN_DRAFTS` retains them when reopening.\n",
		implementation: file,
	}
	for i := range 9 {
		files[fmt.Sprintf("support/part%d.c", i)] = "int support(void) { return 1; }\n"
	}
	j := treeJob(t, files)
	e := includedFiles(j.Snapshot.Manifest)[readme]
	id := record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, 2)
	docs := record.Record{
		Evidence: []record.Excerpt{{ID: id, Path: e.Path, SHA256: e.SHA256, Start: 1, End: 2, Kind: "document"}},
		Contracts: []record.Contract{
			{Statement: "`loadDrafts`로 초안을 읽는다.", AppliesTo: ".", EvidenceIDs: []string{id}},
			{Statement: "`RETAIN_DRAFTS` 설정은 초안을 유지한다.", AppliesTo: ".", EvidenceIDs: []string{id}},
		},
	}
	s := &roleStage{ctx: t.Context(), j: j}
	excerpts, _, _, err := s.codeCorpus(docs)
	if err != nil {
		t.Fatal(err)
	}
	first := excerpts[0]
	if first.Path != implementation || !strings.HasPrefix(first.Text, "#if RETAIN_DRAFTS\nreturn keepDrafts();") {
		t.Fatalf("compile-time behavior lost to a previous contract in the same file: %+v", first)
	}
}
