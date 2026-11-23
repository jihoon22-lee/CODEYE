package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	cliDocs     = "Run `orchard serve --config PATH` to open the browser.\n"
	cliMain     = "package main\nimport stdos \"os\"\nfunc main() { stdos.Exit(run(stdos.Args[1:])) }\n"
	cliRun      = "package main\nfunc run(args []string) int {\nif len(args) == 0 { return 2 }\nif result, ok := dispatch(args); ok { return result }\nreturn 1\n}\n"
	cliDispatch = "package main\nfunc dispatch(args []string) (int, bool) {\nswitch args[0] {\ncase \"serve\":\nreturn activate(args[1:]), true\n}\nreturn 0, false\n}\n"
	cliHandler  = "package main\nfunc activate(args []string) int {\nif len(args) != 2 { return 2 }\nreturn 7\n}\n"
)

func commandTree() map[string]string {
	return map[string]string{
		"go.mod":                   "module example.org/orchard\n\ngo 1.23\n",
		"README.md":                cliDocs,
		"cmd/orchard/main.go":      cliMain,
		"cmd/orchard/run.go":       cliRun,
		"cmd/orchard/dispatch.go":  cliDispatch,
		"cmd/orchard/activate.go":  cliHandler,
		"scripts/browser_smoke.py": strings.Repeat("# setup\n", 55) + "def browser():\n    return 'orchard serve --config PATH open browser'\n",
		"fixtures/cmd/orchard/main.go": "package main\nimport \"os\"\n" +
			"func main() { if os.Args[1] == \"serve\" { fake() } }\nfunc fake() { println(\"fixture\") }\n",
		"cmd/orchard/dispatch_test.go": "package main\nimport \"testing\"\n" + strings.Repeat("// test setup\n", 60) +
			"func TestServe(t *testing.T) {\nif run([]string{\"serve\", \"--config\", \"PATH\"}) != 7 { t.Fatal(\"serve browser\") }\n}\n",
	}
}

func commandCorpus(t *testing.T, files map[string]string, statement string, configure func(*roleStage)) ([]record.Excerpt, corpusLinks) {
	t.Helper()
	return documentedCommandCorpus(t, files, "README.md", statement, configure)
}

func documentedCommandCorpus(t *testing.T, files map[string]string, doc, statement string, configure func(*roleStage)) ([]record.Excerpt, corpusLinks) {
	t.Helper()
	j := treeJob(t, files)
	s := &roleStage{h: Handler{Budget: smallBudget}, ctx: t.Context(), j: j, role: record.StageInvestigate}
	if configure != nil {
		configure(s)
	}
	e := s.entries()[doc]
	end := len(strings.Split(strings.TrimSuffix(files[doc], "\n"), "\n"))
	id := record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, end)
	x := record.Excerpt{ID: id, Path: e.Path, SHA256: e.SHA256, Start: 1, End: end, Kind: "document"}
	d := record.Record{
		Evidence:  []record.Excerpt{x},
		Contracts: []record.Contract{{Statement: statement, AppliesTo: x.DocumentScope(), EvidenceIDs: []string{id}}},
	}
	code, links, _, err := s.codeCorpus(d)
	if err != nil {
		t.Fatal(err)
	}
	return code, links
}

func TestDocumentedGoCommandPrefersRuntimeRoute(t *testing.T) {
	code, links := commandCorpus(t, commandTree(), "With orchard serve --config PATH, the browser opens.", nil)
	catalog := catalogOf(code)
	want := []struct{ symbol, from, to string }{
		{"run", "stdos.Args[1:]", "dispatch(args)"},
		{"dispatch", "dispatch(args)", "case \"serve\":"},
		{"activate", "return activate(args[1:]), true", "return 7"},
	}
	if len(links.directCalls) != len(want) {
		t.Fatalf("wanted three verified static call sites, got %+v in %+v", links.directCalls, code)
	}
	for i, link := range links.directCalls {
		a, b := catalog[link.CallerEvidenceID], catalog[link.CalleeEvidenceID]
		if link.Symbol != want[i].symbol || !strings.HasPrefix(a.Path, "cmd/orchard/") || !strings.HasPrefix(b.Path, "cmd/orchard/") ||
			!strings.Contains(a.Text, want[i].from) || !strings.Contains(b.Text, want[i].to) {
			t.Fatalf("unexpected dispatch link %+v from %+v to %+v", link, a, b)
		}
	}
	if !slices.ContainsFunc(code, func(e record.Excerpt) bool {
		return e.Kind == "test" && e.Path == "cmd/orchard/dispatch_test.go" && strings.Contains(e.Text, "run([]string")
	}) {
		t.Fatalf("supporting product test was not sent: %+v", code)
	}
	// The QA runner may travel as an ordinary unit (the whole file), never
	// as a targeted witness window.
	if slices.ContainsFunc(code, func(e record.Excerpt) bool {
		return e.Path == "scripts/browser_smoke.py" && strings.Contains(e.Text, "def browser") && e.Start > 1
	}) {
		t.Fatalf("QA runner received the runtime witness slot: %+v", code)
	}
}

func TestDocumentedGoCommandRejectsUnverifiedRoutes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(map[string]string)
		config func(*roleStage)
	}{
		{name: "near word in citation", edit: func(files map[string]string) { files["README.md"] = strings.ReplaceAll(cliDocs, "serve", "serve-all") }},
		{name: "unrelated argv", edit: func(files map[string]string) {
			files["cmd/orchard/main.go"] = strings.ReplaceAll(cliMain, "stdos.Args[1:]", "[]string{\"other\"}")
		}},
		{name: "wrong argv slot", edit: func(files map[string]string) {
			files["cmd/orchard/dispatch.go"] = strings.ReplaceAll(cliDispatch, "args[0]", "args[1]")
		}},
		{name: "argv overwritten", edit: func(files map[string]string) {
			files["cmd/orchard/dispatch.go"] = strings.ReplaceAll(cliDispatch, "switch args[0]", "args[0] = \"shadow value\"\nswitch args[0]")
		}},
		{name: "duplicate runtime routes", edit: func(files map[string]string) {
			files["cmd/orchard/dispatch.go"] = strings.ReplaceAll(cliDispatch, "switch args[0]", "if args[0] == \"serve\" { return 0, false }\nswitch args[0]")
		}},
		{name: "another package can produce the binary", edit: func(files map[string]string) { files["nested/cmd/orchard/main.go"] = cliMain }},
		{name: "generated dispatcher", edit: func(files map[string]string) {
			files["cmd/orchard/dispatch.go"] = "// Code generated by arg parser. DO NOT EDIT.\n" + cliDispatch
		}},
		{name: "withheld target", config: func(s *roleStage) { s.screen.withheld = map[string]bool{"cmd/orchard/activate.go": true} }},
		{name: "scoped target", config: func(s *roleStage) { s.j.Scope = domain.Scope{Kind: "paths", Paths: []string{"cmd/orchard/main.go"}} }},
		{name: "pull request", config: func(s *roleStage) { s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"cmd/orchard/main.go"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := commandTree()
			if tc.edit != nil {
				tc.edit(files)
			}
			_, links := commandCorpus(t, files, "orchard serve는 브라우저를 연다.", tc.config)
			if len(links.directCalls) > 0 {
				t.Fatalf("ambiguous or unavailable CLI produced a direct link: %+v", links.directCalls)
			}
		})
	}
}
