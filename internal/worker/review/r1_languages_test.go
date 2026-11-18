package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	reviewpackr1 "github.com/jihoon22-lee/CODEYE/prompts/review-r1"
)

// guideTitles are the first lines of the review-r1 language guides.
var guideTitles = map[string]string{
	"cpp":        "# C/C++ 검토 팩\n",
	"go":         "# Go 검토 팩\n",
	"javascript": "# JavaScript/TypeScript 검토 팩\n",
	"python":     "# Python 검토 팩\n",
	"rust":       "# Rust 검토 팩\n",
}

func guideFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f, "languages/") {
			out = append(out, f)
		}
	}
	return out
}

func guidePaths(ecosystems []string) []string {
	var out []string
	for _, eco := range ecosystems {
		out = append(out, "languages/"+eco+".md")
	}
	return out
}

// checkGuides fails unless text carries exactly the guides of want.
func checkGuides(t *testing.T, label, text string, want []string) {
	t.Helper()
	for eco, title := range guideTitles {
		if strings.Contains(text, title) != slices.Contains(want, eco) {
			t.Fatalf("%s: guide %s present=%v, want ecosystems %v", label, eco, !slices.Contains(want, eco), want)
		}
	}
}

// treeJob acquires a snapshot of the given files.
func treeJob(t *testing.T, tree map[string]string) analysis.Job {
	t.Helper()
	root := t.TempDir()
	for name, body := range tree {
		writeFile(t, root, name, body)
	}
	s, err := source.AcquireLocal(t.Context(), root, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Profile: "deep-review", Snapshot: s, Scratch: t.TempDir(), Scope: domain.Scope{Kind: "full"}, AllowModelReview: true}
}

// W24 rc2: every R1 role gets the language guides of the snapshot's
// in-scope source ecosystems, by the analyzer packs' rule (pack.InScope,
// docs/39 §2.8: source extensions, outside data and vendored directories,
// within the scope; build files alone do not count), in sorted order, and
// none when no supported ecosystem is in scope.
func TestR1LanguageGuidesFollowInScopeEcosystems(t *testing.T) {
	polyglot := func(root string) {
		writeFile(t, root, "tools/gen.py", "print('x')\n")
		writeFile(t, root, "native/src/lib.rs", "pub fn f() {}\n")
		writeFile(t, root, "web/app.tsx", "export const x = 1;\n")
		writeFile(t, root, "ext/codec.cc", "int f() { return 0; }\n")
	}
	cases := []struct {
		name   string
		change func(root string)
		scope  domain.Scope
		want   []string
	}{
		{"go only", nil, domain.Scope{Kind: "full"}, []string{"go"}},
		{"empty scope kind is full", nil, domain.Scope{}, []string{"go"}},
		{"polyglot", polyglot, domain.Scope{Kind: "full"}, []string{"cpp", "go", "javascript", "python", "rust"}},
		{"path scope", polyglot, domain.Scope{Kind: "paths", Paths: []string{"tools", "native"}}, []string{"python", "rust"}},
		{"data and vendored directories", func(root string) {
			writeFile(t, root, "vendor/x/y.c", "int y;\n")
			writeFile(t, root, "node_modules/a/index.js", "module.exports = 1;\n")
			writeFile(t, root, "testdata/t.py", "x = 1\n")
			writeFile(t, root, ".tools/z.rs", "fn z() {}\n")
		}, domain.Scope{Kind: "full"}, []string{"go"}},
		{"build files alone", func(root string) {
			writeFile(t, root, "Makefile", "all:\n\ttrue\n")
			writeFile(t, root, "web/package.json", "{\"name\": \"web\"}\n")
			writeFile(t, root, "py/pyproject.toml", "[project]\nname = \"py\"\n")
		}, domain.Scope{Kind: "full"}, []string{"go"}},
		{"no supported ecosystem in scope", nil, domain.Scope{Kind: "paths", Paths: []string{"docs"}}, nil},
	}
	for _, c := range cases {
		j := w15Job(t, c.change)
		j.Scope = c.scope
		for _, role := range reviewpackr1.Roles {
			s := &r1Stage{h: R1Handler{Caller: &r1Caller{}}, ctx: t.Context(), j: j, role: role}
			if r := s.begin(nil); r != nil {
				t.Fatalf("%s %s: %+v", c.name, role, r)
			}
			want, err := reviewpackr1.For(role, c.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := guideFiles(s.pack.Files); !slices.Equal(got, guidePaths(c.want)) || s.pack.Digest != want.Digest || s.pack.Text != want.Text {
				t.Fatalf("%s %s: guides %v, want %v", c.name, role, got, c.want)
			}
			checkGuides(t, c.name+" "+role, s.pack.Text, c.want)
		}
	}
}

// Every source ecosystem the analyzer packs know (obsdoc.Ecosystems; "any"
// is language independent) has a guide, so the selection never drops one.
func TestR1EveryLanguageEcosystemHasAGuide(t *testing.T) {
	var langs []string
	for _, eco := range obsdoc.Ecosystems {
		if eco != "any" {
			langs = append(langs, eco)
		}
	}
	if !slices.Equal(reviewpackr1.Languages(), langs) {
		t.Fatalf("guides %v, analyzer ecosystems %v", reviewpackr1.Languages(), langs)
	}
}

// The review record shows the language files each call used (pack_files)
// and the request carries exactly those guides: a Go, Python and Rust
// snapshot through all five roles.
func TestR1RecordsShowLanguageGuides(t *testing.T) {
	j := w15Job(t, func(root string) {
		writeFile(t, root, "tools/gen.py", "print('x')\n")
		writeFile(t, root, "native/src/lib.rs", "pub fn f() {}\n")
	})
	f := &r1Caller{script: validScript(t)}
	plan, res := runR1(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	for _, p := range plan {
		if strings.HasPrefix(p.Key, "review.") && res[p.Key].Status != domain.TaskSucceeded {
			t.Fatalf("%s %+v", p.Key, res[p.Key])
		}
	}
	want := []string{"go", "python", "rust"}
	calls := 0
	for _, stage := range record.R1Stages {
		for _, c := range readR1(t, j, stage).Calls {
			calls++
			if got := guideFiles(c.PackFiles); !slices.Equal(got, guidePaths(want)) {
				t.Fatalf("%s pack_files %v", stage, c.PackFiles)
			}
		}
	}
	if calls != len(record.R1Stages) || len(f.requests) != calls {
		t.Fatalf("calls %d, requests %d", calls, len(f.requests))
	}
	for _, q := range f.requests {
		checkGuides(t, q.Role, q.Instructions, want)
	}
	t.Logf("largest request per role with three guides (bytes): %v", maxPrompt(f))
}

// A Python-only snapshot gets the Python guide alone and no Go guide, and
// since W24 phase 2c every code role reaches the model with Python code:
// investigate and verify run (they failed with review_input_unavailable and
// were skipped while code excerpts were Go only).
func TestR1PythonOnlySnapshotGetsThePythonGuide(t *testing.T) {
	j := treeJob(t, map[string]string{
		"README.md":         "# Tool\n\nRun the tool with a configuration file.\n",
		"pyproject.toml":    "[project]\nname = \"tool\"\n",
		"tool/__init__.py":  "",
		"tool/main.py":      "def main():\n    return 0\n",
		"tests/test_app.py": "def test_main():\n    assert True\n",
	})
	if got := languagesInScope(j); !slices.Equal(got, []string{"python"}) {
		t.Fatalf("ecosystems %v", got)
	}
	f := &r1Caller{script: maxScript(t)}
	_, res := runR1(t, j, vetStub{}, f, "1.27.4")
	for _, key := range []string{"review.documents", "review.investigate", "review.verify"} {
		if res[key].Status != domain.TaskSucceeded {
			t.Fatalf("%s %+v", key, res[key])
		}
	}
	for _, stage := range []string{"documents", "investigate", "verify"} {
		calls := readR1(t, j, stage).Calls
		if len(calls) != 1 || !slices.Equal(guideFiles(calls[0].PackFiles), []string{"languages/python.md"}) {
			t.Fatalf("%s calls %+v", stage, calls)
		}
	}
	var paths []string
	for _, e := range readR1(t, j, "investigate").Evidence {
		if e.Kind != "document" {
			paths = append(paths, e.Path+":"+e.Kind)
		}
	}
	if !slices.Equal(paths, []string{"tool/main.py:code", "tests/test_app.py:test"}) {
		t.Fatalf("investigate evidence %v", paths)
	}
	if len(f.requests) != 3 {
		t.Fatalf("requests %d", len(f.requests))
	}
	for _, q := range f.requests {
		checkGuides(t, q.Role, q.Instructions, []string{"python"})
	}
}
