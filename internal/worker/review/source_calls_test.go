package review

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type callProject struct {
	caller, callee string
	call, result   string
	tree           map[string]string
}

func testCallProjects() map[string]callProject {
	padding := strings.Repeat("// unrelated commentary\n", 65)
	return map[string]callProject{
		"Go": {
			caller: "app/command.go", callee: "pricing/rules.go", call: "amount := rates.Quote(5)", result: "return count * 7",
			tree: map[string]string{
				"go.mod": "module example.org/billing\n\ngo 1.23\n",
				"app/command.go": "package app\nimport rates \"example.org/billing/pricing\"\n" + padding + `
func Command() int {
	amount := rates.Quote(5) // quote-order
	return amount
}
`,
				"pricing/rules.go": "package pricing\n" + padding + "func Quote(count int) int {\n\treturn count * 7\n}\n",
			},
		},
		"Java": {
			caller: "src/main/java/app/Command.java", callee: "src/main/java/pricing/Rules.java", call: "return Rules.quote(5)", result: "return count * 7",
			tree: map[string]string{
				"pom.xml": "<project/>\n",
				"src/main/java/app/Command.java": "package app;\nimport pricing.Rules;\n" + padding + `
public class Command {
  public static int run(String command) {
    if (command.equals("quote-order")) return Rules.quote(5);
    return 0;
  }
}
`,
				"src/main/java/pricing/Rules.java": "package pricing;\npublic final class Rules {\n" + padding + `
  public static int quote(String count) { return -1; }
  public static int quote(int count) {
    return count * 7;
  }
}
`,
			},
		},
		"Python": {
			caller: "app/command.py", callee: "app/rules.py", call: "return calculate(5)", result: "return count * 7",
			tree: map[string]string{
				"app/command.py":    "from .public_api import estimate as calculate\n" + strings.ReplaceAll(padding, "//", "#") + "def run(command):\n    if command == 'quote-order':\n        return calculate(5)\n",
				"app/public_api.py": "from .rules import quote as estimate\n",
				"app/rules.py":      strings.ReplaceAll(padding, "//", "#") + "def quote(count):\n    return count * 7\n",
			},
		},
		"TypeScript": {
			caller: "app/command.ts", callee: "app/rules.ts", call: "return calculate(5)", result: "return count * 7",
			tree: map[string]string{
				"app/command.ts":    "import { estimate as calculate } from './public-api.js';\n" + padding + "export function run(command: string) {\n  if (command === 'quote-order') return calculate(5);\n  return 0;\n}\n",
				"app/public-api.ts": "export { quote as estimate } from './rules.js';\n",
				"app/rules.ts":      padding + "export function quote(count: number) {\n  return count * 7;\n}\n",
			},
		},
		"C++": {
			caller: "native/command.cpp", callee: "native/rules.hpp", call: "return quote(5)", result: "return count * 7",
			tree: map[string]string{
				"native/command.cpp": "#include \"rules.hpp\"\n" + padding + "int command(bool quote_order) {\n  if (quote_order) return quote(5);\n  return 0;\n}\n",
				"native/rules.hpp":   "#pragma once\n" + padding + "static inline int quote(int count) {\n  return count * 7;\n}\n",
			},
		},
		"Rust": {
			caller: "src/lib.rs", callee: "src/rules.rs", call: "return calculate(5)", result: "count * 7",
			tree: map[string]string{
				"Cargo.toml":   "[package]\nname = 'billing'\nversion = '0.1.0'\n",
				"src/lib.rs":   "mod rules;\nuse crate::rules::quote as calculate;\n" + padding + "pub fn command(quote_order: bool) -> i32 {\n    if quote_order { return calculate(5); }\n    0\n}\n",
				"src/rules.rs": padding + "pub(crate) fn quote(count: i32) -> i32 {\n    count * 7\n}\n",
			},
		},
	}
}

func evidenceLine(t *testing.T, text, content string) int {
	t.Helper()
	at := strings.Index(text, content)
	if at < 0 {
		t.Fatalf("missing %q in test input", content)
	}
	return strings.Count(text[:at], "\n") + 1
}

func TestDocumentedCallsReachScopedImplementations(t *testing.T) {
	projects := testCallProjects()
	c := projects["C++"]
	projects["C"] = callProject{caller: "native/command.c", callee: "native/rules.h", call: c.call, result: c.result, tree: map[string]string{
		"native/command.c": strings.NewReplacer("rules.hpp", "rules.h", "bool quote_order", "int quote_order").Replace(c.tree[c.caller]),
		"native/rules.h":   c.tree[c.callee],
	}}
	java := projects["Java"]
	java.tree = maps.Clone(java.tree)
	java.tree[java.caller] = strings.NewReplacer("import pricing.Rules;", "import static pricing.Rules.quote;", "Rules.quote(5)", "quote(5)").Replace(java.tree[java.caller])
	java.call = "return quote(5)"
	projects["Java static import"] = java
	goGeneric := projects["Go"]
	goGeneric.tree = maps.Clone(goGeneric.tree)
	goGeneric.tree[goGeneric.caller] = strings.NewReplacer(`import rates "example.org/billing/pricing"`, `import "example.org/billing/pricing"`,
		"rates.Quote(5)", "policy.Quote[int](5)").Replace(goGeneric.tree[goGeneric.caller])
	goGeneric.tree[goGeneric.callee] = strings.NewReplacer("package pricing", "package policy", "Quote(count int) int", "Quote[T ~int](count T) T").Replace(goGeneric.tree[goGeneric.callee])
	goGeneric.call = "amount := policy.Quote[int](5)"
	projects["Go imported generic"] = goGeneric
	for lang, project := range projects {
		t.Run(lang, func(t *testing.T) {
			project.tree["README.md"] = "# Commands\n\nUse `quote-order` to calculate the order price.\n"
			j := treeJob(t, project.tree)
			s := &roleStage{ctx: t.Context(), j: j, role: record.StageInvestigate}
			entry := s.entries()["README.md"]
			id := record.ExcerptID(j.Snapshot.Digest, entry.Path, entry.SHA256, 1, 3)
			d := record.Record{
				Evidence:  []record.Excerpt{{ID: id, Path: entry.Path, SHA256: entry.SHA256, Start: 1, End: 3, Kind: "document"}},
				Contracts: []record.Contract{{Statement: "`quote-order` calculates the order price in `" + project.caller + "`.", AppliesTo: ".", EvidenceIDs: []string{id}}},
			}
			code, links, _, err := s.codeCorpus(d)
			if err != nil || len(links.directCalls) != 1 {
				t.Fatalf("expected the actual call in the overview request: %+v, %v", links.directCalls, err)
			}
			byID := catalogOf(code)
			link := links.directCalls[0]
			from, to := byID[link.CallerEvidenceID], byID[link.CalleeEvidenceID]
			for _, expected := range []struct {
				e          record.Excerpt
				file, line string
			}{
				{from, project.caller, project.call}, {to, project.callee, project.result},
			} {
				line := evidenceLine(t, project.tree[expected.file], expected.line)
				e := expected.e
				if e.Path != expected.file || line < e.Start || line > e.End || !strings.Contains(e.Text, expected.line) ||
					e.ID != record.ExcerptID(j.Snapshot.Digest, e.Path, s.entries()[e.Path].SHA256, e.Start, e.End) {
					t.Fatalf("wrong snapshot range for %q: %+v", expected.line, e)
				}
			}
			if strings.HasPrefix(lang, "Java") && strings.Contains(to.Text, "return -1") {
				t.Fatalf("another overload was selected: %+v", to)
			}
		})
	}
}

func TestSourceCallContextRejectsAmbiguousOrUnavailableBindings(t *testing.T) {
	projects := testCallProjects()
	for _, test := range []struct {
		name, lang string
		change     func(tree map[string]string, p callProject)
		restrict   func(*roleStage, callProject)
	}{
		{name: "Go shadow", lang: "Go", change: func(tree map[string]string, p callProject) {
			tree[p.caller] = strings.Replace(tree[p.caller], "func Command()", "func Command(rates interface{ Quote(int) int })", 1)
		}},
		{name: "Go conditional duplicate", lang: "Go", change: func(tree map[string]string, p callProject) {
			tree["pricing/rules_linux.go"] = "package pricing\nfunc Quote(count int) int { return 0 }\n"
		}},
		{name: "Java overloaded unknown argument", lang: "Java", change: func(tree map[string]string, p callProject) {
			tree[p.caller] = strings.Replace(tree[p.caller], "Rules.quote(5)", "Rules.quote(command)", 1)
		}},
		{name: "Java inherited field could shadow class", lang: "Java", change: func(tree map[string]string, p callProject) {
			tree[p.caller] = strings.Replace(tree[p.caller], "class Command", "class Command extends Other", 1)
		}},
		{name: "Java duplicate class in another source set", lang: "Java", change: func(tree map[string]string, p callProject) {
			tree["other/src/main/java/pricing/Rules.java"] = tree[p.callee]
		}},
		{name: "Python reexport overwritten", lang: "Python", change: func(tree map[string]string, p callProject) { tree["app/public_api.py"] += "estimate = fallback\n" }},
		{name: "TypeScript ambiguous extension", lang: "TypeScript", change: func(tree map[string]string, p callProject) {
			tree["app/rules.js"] = "export function quote(count) { return 0; }\n"
		}},
		{name: "C preprocessor branch", lang: "C++", change: func(tree map[string]string, p callProject) {
			tree[p.callee] = "#ifdef USE_DISCOUNT\n" + tree[p.callee] + "#endif\n"
		}},
		{name: "Rust feature conditional module", lang: "Rust", change: func(tree map[string]string, p callProject) {
			tree[p.caller] = "#[cfg(feature = \"quote\")]\n" + tree[p.caller]
		}},
		{name: "Rust Cargo source root override", lang: "Rust", change: func(tree map[string]string, _ callProject) {
			tree["Cargo.toml"] += "[lib]\npath = 'other.rs'\n"
		}},
		{name: "out of selected paths", lang: "Go", restrict: func(s *roleStage, p callProject) { s.j.Scope = domain.Scope{Kind: "paths", Paths: []string{p.caller}} }},
		{name: "unchanged in pull request", lang: "Java", restrict: func(s *roleStage, p callProject) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{p.caller}}
		}},
		{name: "reexport withheld", lang: "Python", restrict: func(s *roleStage, p callProject) { s.screen.withheld = map[string]bool{"app/public_api.py": true} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := projects[test.lang]
			p.tree = maps.Clone(p.tree)
			if test.change != nil {
				test.change(p.tree, p)
			}
			j := treeJob(t, p.tree)
			s := &roleStage{ctx: t.Context(), j: j}
			if test.restrict != nil {
				test.restrict(s, p)
			}
			e := s.entries()[p.caller]
			anchor := record.Excerpt{Path: e.Path, SHA256: e.SHA256, Kind: "code", Start: 1, End: strings.Count(p.tree[p.caller], "\n")}
			out, err := s.sourceCallContext(".", []record.Excerpt{anchor}, nil, 2, 4000)
			if err != nil || len(out) != 0 {
				t.Fatalf("ambiguous call yielded evidence: %+v, %v", out, err)
			}
		})
	}
}

func TestVerifyFollowsImportedGoCallFromCandidate(t *testing.T) {
	p := testCallProjects()["Go"]
	j := treeJob(t, p.tree)
	script := coverageScript(t)
	const title = "수량에 적용된 정책"
	script[record.StageInvestigate] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if e.Path == p.callee && strings.Contains(e.Text, p.result) {
				return asJSON(map[string]any{"candidates": []record.CandidateText{{
					Title: title, Condition: "Quote를 호출한다.", Mechanism: "호출 결과의 단위가 바뀐다.", Impact: "가격이 변한다.", Improvement: "단위를 문서에 적는다.",
					Counterpoint: "호출자에서 별도로 단위를 맞출 수 있다.", Evidence: []string{e.ID},
				}}})
			}
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	verify := script[record.StageVerify]
	seen := false
	script[record.StageVerify] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if slices.ContainsFunc(in.Candidates, func(c record.Candidate) bool { return c.Title == title }) {
			seen = true
			line := evidenceLine(t, p.tree[p.caller], p.call)
			if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.Path == p.caller && e.Symbol == "Quote" && e.Origin == "caller" && e.Start <= line && line <= e.End && strings.Contains(e.Text, p.call)
			}) {
				t.Fatalf("missing cross-package call in actual verifier request: %+v", in.Scope.Excerpts)
			}
		}
		return verify(n, q)
	}
	runRoleWith(t, j, Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
	if !seen {
		t.Fatal("no verification request for the cited implementation")
	}
}

func TestContractFollowupFollowsImportedGoImplementation(t *testing.T) {
	const callerPath, calleePath = "svc/b/quote.go", "svc/b/policy/rules.go"
	const caller = `package b
import "example.test/modules/svc/b/policy"
func Quote(amount int) int { return policy.Apply(amount) }
`
	const callee = `package policy
func Apply(amount int) int {
	if amount < 0 { return 0 }
	return amount * 7
}
`
	j := contractExampleJob(t, func(root string) {
		writeFile(t, root, callerPath, caller)
		writeFile(t, root, calleePath, callee)
	})
	contractExampleInput(t, j)
	doc, inv := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate)
	doc.Conflicts = nil
	if _, err := persistRecord(j, doc); err != nil {
		t.Fatal(err)
	}
	for i, up := range inv.Upstream {
		if up.Source == "review.documents" {
			inv.Upstream[i].SHA256 = digestOf(mustRead(t, recordPath(j, record.StageDocuments)))
		}
	}
	files := (&roleStage{j: j}).entries()
	e := files[callerPath]
	inv.ContractProbe.Candidates = []record.ContractLocation{{ID: record.ContractLocationID(j.Snapshot.Digest, e.Path, e.SHA256, 3, 3),
		Path: e.Path, SHA256: e.SHA256, Start: 3, End: 3, Kind: "code", Symbol: "Quote"}}
	inv.ContractProbe.Literals = nil
	if _, err := persistRecord(j, inv); err != nil {
		t.Fatal(err)
	}
	var found string
	f := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
		record.StageContract: func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[contractReviewInput](t, q)
			for _, x := range in.Scope.Excerpts {
				if x.Path == calleePath && x.Start == 2 && x.End == 5 && strings.Contains(x.Text, "return amount * 7") {
					found = x.ID
				}
			}
			if found == "" {
				t.Fatalf("cross-package definition not present in actual contract request: %+v", in.Scope.Excerpts)
			}
			comparison := in.Prior
			comparison.CodeEvidenceIDs = []string{found}
			comparison.Reason = "가져온 구현의 양수 계산을 읽었다. 문서의 조건은 미확인이다."
			return asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{comparison}})
		},
	}}
	h := Handler{Static: vetStub{}, Caller: f}
	res := h.Run(t.Context(), j, analysis.TaskSpec{Key: record.TaskContract}, nil)
	if res.Status != domain.TaskSucceeded || found == "" {
		t.Fatalf("cross-package contract evidence was not accepted: %+v", res)
	}
	final := readRecord(t, j, record.StageContract)
	if len(final.ContractComparisons) != 1 || !slices.Equal(final.ContractComparisons[0].CodeEvidenceIDs, []string{found}) {
		t.Fatalf("cross-package citation was not retained: %+v", final.ContractComparisons)
	}
}
