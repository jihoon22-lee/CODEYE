package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const routeSignature = `def predict_route(
    event: str,
    attempts: int = 3,
    *,
    queue_policy: str = "direct",
) -> str:`

func TestDocumentedPythonCallSuppliesCompleteScopedSignature(t *testing.T) {
	const scope = "packages/routing"
	const guide = scope + "/docs/usage.md"
	const impl = scope + "/routing/arrival.py"
	const test = scope + "/tests/test_arrival.py"
	const example = "`predict_route('clear')` returns direct_route_response for the event and attempts when queue_policy is 'direct'."
	source := "from .legacy import predict_route as _previous_route\n" + strings.Repeat("# Module metadata.\n", 50) + routeSignature + "\n" +
		"    \"\"\"Implementation notes.\n" + strings.Repeat("    A license governs this library.\n", 40) + "    \"\"\"\n" +
		"    if queue_policy == 'direct':\n        return direct_route_response(event, attempts)\n    return 'reserve:' + event\n"
	j := treeJob(t, map[string]string{
		guide: example + "\n", impl: source,
		test:                      strings.Repeat("# Test fixtures.\n", 60) + "def test_predict_route():\n    assert predict_route('clear') == 'direct:clear'\n",
		scope + "/pyproject.toml": "[project]\nname = 'arrival'\n",
		"unrelated/arrival.py":    strings.ReplaceAll(source, "'reserve:'", "'unrelated:'"),
	})
	script := maxScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		if len(in.Documents) != 1 || in.Documents[0].Path != guide || in.Documents[0].AppliesTo != scope {
			t.Fatalf("unexpected document scope: %+v", in.Documents)
		}
		return asJSON(docsOutput{
			Contracts: []record.Contract{{Statement: "predict_route('clear')는 queue_policy 기본값에서 direct_route_response를 반환한다.", AppliesTo: scope,
				EvidenceIDs: []string{in.Documents[0].EvidenceID}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	var header, runtime string
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		e := includedFiles(j.Snapshot.Manifest)[impl]
		var lines, bytes int
		for _, x := range in.Scope.Excerpts {
			if x.Path != impl {
				continue
			}
			lines += x.End - x.Start + 1
			bytes += record.JSONLen(x.Text)
			if x.Text == routeSignature && x.Start == 52 && x.End == 57 {
				header = x.ID
			}
			if strings.Contains(x.Text, "return direct_route_response(event, attempts)") {
				runtime = x.ID
			}
			if x.ID != record.ExcerptID(j.Snapshot.Digest, impl, e.SHA256, x.Start, x.End) {
				t.Fatalf("wrong evidence identity: %+v", x)
			}
		}
		if header == "" || runtime == "" || header == runtime || lines > roleMaxCodeLines || bytes > roleMaxCodeFile ||
			len(in.Scope.Excerpts) > roleMaxCodeFiles || len(opencode.Prompt(q)) > MinRequestBytes {
			t.Fatalf("missing complete header or bounded runtime excerpt: %+v", in.Scope.Excerpts)
		}
		if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
			return e.Path == test && e.Kind == "test" && strings.Contains(e.Text, "assert predict_route")
		}) {
			t.Fatalf("existing test witness displaced: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	_, results := runRoleSmall(t, j, vetStub{}, &scriptedCaller{script: script}, "1.27.4")
	if results["review.investigate"].Status != domain.TaskSucceeded {
		t.Fatalf("investigate: %+v", results["review.investigate"])
	}
	r := readRecord(t, j, record.StageInvestigate)
	if catalogOf(r.Evidence)[header].Kind != "code" || catalogOf(r.Evidence)[runtime].Kind != "code" || len(r.ContractComparisons) != 0 {
		t.Fatalf("static navigation changed the accepted assessment: %+v", r)
	}
}

func TestPythonSignatureRejectsAmbiguousOrNonRuntimeText(t *testing.T) {
	for _, tc := range []struct{ name, before, body, after, selection string }{
		{"unaliased import", "from .legacy import predict_route\n", "    return 'chosen'", "", "return 'chosen'"},
		{"duplicate declaration", "", "    return 'chosen'", "\ndef predict_route(event):\n    return 'other'", "return 'chosen'"},
		{"shadowed declaration", "", "    return 'chosen'", "\npredict_route = handler", "return 'chosen'"},
		{"decorated function", "@route_handler\n", "    return 'chosen'", "", "return 'chosen'"},
		{"dynamic binding", "", "    return 'chosen'", "\nexec(replacement)", "return 'chosen'"},
		{"nested function", "", "    def handler():\n        return 'chosen'\n    return handler()", "", "return 'chosen'"},
		{"docstring", "", "    \"\"\"Examples.\n        return 'chosen'\n    \"\"\"\n    return None", "", "return 'chosen'"},
		{"outside body", "", "    return None", "\ndef handler():\n    return 'chosen'", "return 'chosen'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.before + routeSignature + "\n" + tc.body + tc.after
			line := 1 + strings.Count(source[:strings.Index(source, tc.selection)], "\n")
			if start, end := pythonPublicSignature(source, []string{"predict_route"}, record.Excerpt{Start: line, End: line}); start != 0 || end != 0 {
				t.Fatalf("ambiguous source produced signature %d–%d", start, end)
			}
		})
	}
	if calls := pythonFreeCalls("Use client.predict_route('clear'), def predict_route(x), and incomplete predict_route("); len(calls) != 0 {
		t.Fatalf("accepted an unsupported call spelling: %v", calls)
	}
}

func TestPythonSignatureKeepsReviewScopeAndCitation(t *testing.T) {
	const statement = "predict_route('clear')는 기본 queue_policy로 직접 라우팅한다."
	const document = "Call predict_route('clear') to use the direct queue_policy.\n"
	const impl = "routing/arrival.py"
	for _, tc := range []struct {
		name, doc, path string
		configure       func(*roleStage)
	}{
		{name: "citation only names the function", doc: "The predict_route function has a default queue_policy.\n", path: impl},
		{name: "scope excludes file", doc: document, path: impl, configure: func(s *roleStage) {
			s.j.Scope = domain.Scope{Kind: "paths", Paths: []string{"README.md"}}
		}},
		{name: "screen withholds file", doc: document, path: impl, configure: func(s *roleStage) {
			s.screen.withheld = map[string]bool{impl: true}
		}},
		{name: "file absent from PR changes", doc: document, path: impl, configure: func(s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"README.md"}}
		}},
		{name: "test helper", doc: document, path: "routing/tests/test_arrival.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := commandCorpus(t, map[string]string{
				"README.md": tc.doc, tc.path: strings.Repeat("# Module support.\n", 60) + routeSignature + "\n" +
					strings.Repeat("    # Configuration details.\n", 28) + "    if queue_policy == 'direct':\n        return 'direct'\n    return 'reserve'\n",
			}, statement, tc.configure)
			if slices.ContainsFunc(code, func(x record.Excerpt) bool { return x.Text == routeSignature }) {
				t.Fatalf("found a declaration outside verified runtime or source scope: %+v", code)
			}
		})
	}
}
