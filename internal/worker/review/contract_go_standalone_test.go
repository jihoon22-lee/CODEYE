package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestDocumentedStandaloneGoCommandOffersWholeMainAndUniqueCallee(t *testing.T) {
	const doc = "cmd/orchard/README.md"
	const cli = "cmd/orchard/main.go"
	const main = "func main() {\nif len(os.Args) == 1 {\nread(os.Stdin)\nreturn\n}\nfor _, arg := range os.Args[1:] { read(strings.NewReader(arg)) }\n}"
	const handler = "func read(r io.Reader) {\ntext, err := io.ReadAll(r)\nif err != nil { log.Fatal(err) }\nif len(text) != 0 { fmt.Println(string(text)) }\n}"
	files := map[string]string{
		"go.mod": "module example.org/orchard\n",
		doc:      "# Orchard reader\n\n    $ orchard props.data\n    $ orchard --details props.data\n",
		cli: "package main\nimport (\"os\"; \"strings\"; \"io\"; \"log\"; \"fmt\")\n" +
			strings.Repeat("// Additional CLI settings\n", 55) + main + "\n\n" + handler + "\n",
	}
	for _, statement := range []string{"`orchard` reads the provided input.", "`orchard --details props.data` reads the provided input."} {
		code, links := documentedCommandCorpus(t, files, doc, statement, nil)
		if len(links.directCalls) != 1 {
			t.Fatalf("repeated calls should link one handler for %q: %+v", statement, links.directCalls)
		}
		link := links.directCalls[0]
		catalog := catalogOf(code)
		from, to := catalog[link.CallerEvidenceID], catalog[link.CalleeEvidenceID]
		if link.Symbol != "read" || from.Path != cli || to.Path != cli || from.Text != main || to.Text != handler {
			t.Fatalf("standalone CLI omitted executable source: %+v %+v", from, to)
		}
	}
}

func TestDocumentedStandaloneGoCommandRequiresTheLocalGuideAndDirectCallee(t *testing.T) {
	const local = "cmd/orchard/README.md"
	const cli = "cmd/orchard/main.go"
	for _, tc := range []struct {
		name, doc, statement, suffix string
		mainOnly                     bool
	}{
		{name: "outside command directory", doc: "README.md", statement: "`orchard` reads a file.", suffix: "func main() { read() }\nfunc read() { println(1) }\n"},
		{name: "near command spelling", doc: local, statement: "`orchard-other` reads a file.", suffix: "func main() { read() }\nfunc read() { println(1) }\n"},
		{name: "ambiguous callees", doc: local, statement: "`orchard` reads a file.", suffix: "func main() { read(); inspect() }\nfunc read() { println(1) }\nfunc inspect() { println(2) }\n", mainOnly: true},
		{name: "function value", doc: local, statement: "`orchard` reads a file.", suffix: "func main() { run := read; run() }\nfunc read() { println(1) }\n", mainOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"go.mod": "module example.org/orchard\n",
				tc.doc:   "$ orchard --details props.data\n",
				cli:      "package main\n" + strings.Repeat("// setup\n", 60) + tc.suffix,
			}
			code, links := documentedCommandCorpus(t, files, tc.doc, tc.statement, nil)
			if len(links.directCalls) != 0 {
				t.Fatalf("unexpected direct call: %+v", links.directCalls)
			}
			mainSelected := slices.ContainsFunc(code, func(e record.Excerpt) bool { return e.Path == cli && strings.HasPrefix(e.Text, "func main()") })
			if mainSelected != tc.mainOnly {
				t.Fatalf("selected main=%t, wanted %t: %+v", mainSelected, tc.mainOnly, code)
			}
		})
	}
}
