package python

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// TestDeclarationMatchesDiscovery pins the pack's copy of the Python
// version readers to env.discover's (discovery.PythonRequirement) on a
// generated corpus: same kind, same constraint, same malformed verdict.
func TestDeclarationMatchesDiscovery(t *testing.T) {
	type sample struct{ name, body string }
	var corpus []sample
	long := strings.Repeat("9", 300)
	values := []string{`">=3.10"`, `'>=3.9'`, `">=3.8" # comment`, `"""x"""`, `'''x'''`, `">=3.10`, `'x`, `>=3.11`, `"a\"b"`, `"\u0001"`,
		`"` + long + `"`, `"password = 'abcdefghijklmnop'"`, `"ghp_abcdefghijklmnopqrstuv"`, `""`, `">=3.12" trailing`, `"~=3.11.2"`, `["3.10"]`}
	headers := []string{"[project]", "[ project ]", `["project"]`, "[tool.x]", "[[project]]", "[project", "[project] # c", "[project] x", "[project.urls]"}
	keys := []string{"requires-python", `"requires-python"`, `'requires-python'`, "requires_python"}
	prefixes := []string{"", "deps = [\n  \"a\",\n  \"b\",\n]\n", "x = 1\n# c\n"}
	for _, h := range headers {
		for _, k := range keys {
			for _, v := range values {
				for _, pre := range prefixes {
					body := h + "\n" + pre + k + " = " + v + "\n"
					corpus = append(corpus, sample{"pyproject.toml", body}, sample{"pyproject.toml", strings.ReplaceAll(body, "\n", "\r\n")},
						sample{"pyproject.toml", body + k + " = " + v + "\n"})
				}
			}
		}
	}
	for _, sec := range []string{"[options]", "[ options ]", "[metadata]", "[options"} {
		for _, sep := range []string{"=", ":", " = ", " : "} {
			for _, v := range []string{">=3.8", "", ">=3.9 # c", "\u0001", long, "~=3.10"} {
				body := "; c\n" + sec + "\npython_requires" + sep + v + "\n"
				corpus = append(corpus, sample{"setup.cfg", body}, sample{"setup.cfg", body + "python_requires" + sep + v + "\n"})
			}
		}
	}
	for _, b := range []string{"3.11.4\n", "# c\n3.12\n", "3.10 3.11\n", "", "\n\n", "3.9 # x\n", "pypy3.10\n", long + "\n", "\u0001\n", "ghp_abcdefghijklmnopqrstuv\n"} {
		corpus = append(corpus, sample{".python-version", b}, sample{"sub/.python-version", b})
	}
	big := strings.Repeat("#", maxDeclarationFileBytes) + "\n[project]\nrequires-python = \">=3.10\"\n"
	corpus = append(corpus, sample{"pyproject.toml", big}, sample{"setup.cfg", big}, sample{".python-version", big}, sample{"README.md", "x"})
	for _, s := range corpus {
		k1, c1, e1 := readDeclaration(s.name, []byte(s.body))
		k2, c2, e2 := discovery.PythonRequirement(s.name, []byte(s.body))
		if k1 != k2 || c1 != c2 || (e1 != nil) != (e2 != nil) {
			t.Fatalf("%s %q: pack (%q %q %v) discovery (%q %q %v)", s.name, s.body, k1, c1, e1, k2, c2, e2)
		}
	}
	t.Logf("%d samples", len(corpus))
}

func TestMinimumMinorAndClamp(t *testing.T) {
	for _, c := range []struct {
		kind, constraint string
		want             int
		ok               bool
	}{
		{SourceRequiresPython, ">=3.10", 10, true},
		{SourceRequiresPython, ">=3.9, <4", 9, true},
		{SourceRequiresPython, ">=3.8,!=3.9.*,>=3.11", 11, true},
		{SourceRequiresPython, "~=3.12.1", 12, true},
		{SourceRequiresPython, "==3.13.*", 13, true},
		{SourceRequiresPython, ">3.10", 10, true},
		{SourceRequiresPython, "<3.12", 0, false},
		{SourceRequiresPython, ">=2.7", 0, false},
		{SourcePythonRequires, ">= 3.11", 11, true},
		{SourceVersionFile, "3.12.4", 12, true},
		{SourceVersionFile, "pypy3.10", 0, false},
		{SourceVersionFile, "3", 0, false},
	} {
		if n, ok := minimumMinor(c.kind, c.constraint); n != c.want || ok != c.ok {
			t.Errorf("%s %q: %d %v", c.kind, c.constraint, n, ok)
		}
	}
	if n, lim := clampMinor(8, 10, 15); n != 10 || lim != LimVersionBelowTool {
		t.Fatal(n, lim)
	}
	if n, lim := clampMinor(16, 10, 15); n != 15 || lim != LimVersionAboveTool {
		t.Fatal(n, lim)
	}
	if n, lim := clampMinor(12, 10, 15); n != 12 || lim != "" {
		t.Fatal(n, lim)
	}
}

// The declaration order is pyproject requires-python, setup.cfg
// python_requires, .python-version; an unknown version is the recorded
// default, never a guess.
func TestResolveTarget(t *testing.T) {
	for _, c := range []struct {
		files  map[string]string
		minor  int
		source string
		lims   []string
	}{
		{map[string]string{"a.py": "", "pyproject.toml": "[project]\nrequires-python = \">=3.11\"\n", "setup.cfg": "[options]\npython_requires = >=3.9\n"}, 11, SourceRequiresPython, nil},
		{map[string]string{"a.py": "", "pyproject.toml": "[project]\nname = \"x\"\n", "setup.cfg": "[options]\npython_requires = >=3.9\n"}, 9, SourcePythonRequires, nil},
		{map[string]string{"a.py": "", ".python-version": "3.12\n"}, 12, SourceVersionFile, nil},
		{map[string]string{"a.py": ""}, DefaultPythonMinor, SourceDefault, []string{LimVersionUnknown}},
		{map[string]string{"a.py": "", "pyproject.toml": "[project]\nrequires-python = \"<4\"\n"}, DefaultPythonMinor, SourceDefault, []string{LimVersionNotUnderstood, LimVersionUnknown}},
		{map[string]string{"a.py": "", "pyproject.toml": "[project]\nrequires-python = \"\"\"x\"\"\"\n", ".python-version": "3.10\n"}, 10, SourceVersionFile, []string{LimVersionUnreadable}},
	} {
		fx := newFixture(t, c.files, domain.Scope{})
		got := resolveTarget(fx.Job.Snapshot.SnapshotDir, includedFiles(fx.Job.Snapshot.Manifest), ".")
		if got.Minor != c.minor || got.Source != c.source || !slices.Equal(got.Limitations, c.lims) {
			t.Errorf("%v: %+v", c.files, got)
		}
	}
}

func manifestOf(paths ...string) source.Manifest {
	var m source.Manifest
	for _, p := range paths {
		size := int64(1)
		m.Entries = append(m.Entries, source.Entry{Path: p, Kind: "file", Size: &size, SHA256: strings.Repeat("0", 64)})
	}
	return m
}

func TestPlanModules(t *testing.T) {
	m := manifestOf("pyproject.toml", "a.py", "pkg/b.py", "pkg/b.pyi", "sub/setup.cfg", "sub/c.py", "sub/deep/d.py", "venv/lib/e.py", ".hidden/f.py",
		"pkg/.g.py", "node_modules/h.py", "pkg/__pycache__/i.py", "testdata/j.py", "README.md", "x.PY")
	mods, err := planModules(m, domain.Scope{Kind: "full"})
	if err != nil || len(mods) != 2 {
		t.Fatalf("%v %+v", err, mods)
	}
	if mods[0].Dir != "." || !slices.Equal(mods[0].Files, []string{"a.py", "pkg/b.py", "pkg/b.pyi"}) || !slices.Equal(mods[0].Nested, []string{"sub"}) || !slices.Equal(mods[0].Targets, []string{"."}) {
		t.Fatalf("root %+v", mods[0])
	}
	if mods[1].Dir != "sub" || !slices.Equal(mods[1].Files, []string{"sub/c.py", "sub/deep/d.py"}) || len(mods[1].Nested) != 0 {
		t.Fatalf("sub %+v", mods[1])
	}
	mods, _ = planModules(m, domain.Scope{Kind: "paths", Paths: []string{"pkg/b.py", "sub/deep"}})
	if len(mods) != 2 || !slices.Equal(mods[0].Targets, []string{"pkg/b.py"}) || !slices.Equal(mods[1].Targets, []string{"deep"}) {
		t.Fatalf("paths %+v", mods)
	}
	// Python source in scope but nothing analyzable: one placeholder task.
	mods, _ = planModules(manifestOf("venv/x.py", "README.md"), domain.Scope{Kind: "full"})
	if len(mods) != 1 || !mods[0].Placeholder {
		t.Fatalf("placeholder %+v", mods)
	}
	if mods, _ := planModules(manifestOf("main.go"), domain.Scope{Kind: "full"}); len(mods) != 0 {
		t.Fatalf("no python %+v", mods)
	}
	if _, err := planModules(m, domain.Scope{Kind: "diff"}); err == nil {
		t.Fatal("unsupported scope kind planned")
	}
}

func TestPlanKeysAndScope(t *testing.T) {
	job := analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: source.Snapshot{Manifest: manifestOf("a.py", "x/pyproject.toml", "x/m.py", "y/setup.cfg", "y/n.pyi")}}
	tasks, err := RuffPlan(context.Background(), job)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("%v %+v", err, tasks)
	}
	var keys []string
	for _, tk := range tasks {
		keys = append(keys, tk.Spec.Key)
		if !tk.Spec.Required || tk.RepositoryCode || tk.Spec.Stage != domain.StageStaticAnalysis {
			t.Fatalf("task %+v", tk)
		}
	}
	if !slices.Equal(keys, []string{"ruff", "ruff.m01", "ruff.m02"}) {
		t.Fatalf("keys %v", keys)
	}
	bt, _ := BanditPlan(context.Background(), job)
	if len(bt) != 3 || bt[0].Spec.Required || *bt[2].FilesInScope != 0 || *tasks[2].FilesInScope != 1 {
		t.Fatalf("bandit %+v", bt)
	}
}

func TestByteColumn(t *testing.T) {
	line := []byte("a\u00e9\U0001F600b")
	for _, c := range []struct {
		unit string
		col  int
		want int
		ok   bool
	}{
		{unitRune, 1, 1, true}, {unitRune, 2, 2, true}, {unitRune, 3, 4, true}, {unitRune, 4, 8, true}, {unitRune, 5, 9, true}, {unitRune, 6, 0, false},
		{unitUTF16, 3, 4, true}, {unitUTF16, 4, 0, false}, {unitUTF16, 5, 8, true}, {unitUTF16, 6, 9, true},
		{unitByte, 9, 9, true}, {unitByte, 10, 0, false}, {unitRune, 0, 0, false},
	} {
		got, ok := byteColumn(line, c.unit, c.col)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s %d: %d %v", c.unit, c.col, got, ok)
		}
	}
	if _, ok := byteColumn([]byte{0xff, 'a'}, unitRune, 2); ok {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestParseDocumentRanges(t *testing.T) {
	b := []byte(`{"a": 1, "results": [ {"x": 1}, {"y": [2, 3]} ], "z": "s"}`)
	d := parseDocument(b, "results")
	if d.DecodeError != "" || len(d.Arrays["results"]) != 2 {
		t.Fatalf("%+v", d)
	}
	for i, want := range []string{`{"x": 1}`, `{"y": [2, 3]}`} {
		el := d.Arrays["results"][i]
		if string(b[el.Start:el.End]) != want || el.Pointer != "/results/"+string(rune('0'+i)) || el.Index != i {
			t.Fatalf("element %d %+v", i, el)
		}
	}
	if d := parseDocument([]byte(`{"results": [`), "results"); d.DecodeError == "" {
		t.Fatal("truncated JSON accepted")
	}
	l := parseJSONLines([]byte("{\"a\":1}\n{\"b\":2}\nnot json\n"))
	if l.UnparsedBytes == 0 {
		t.Fatalf("lines %+v", l)
	}
	var v struct{ A int }
	if decodeStrict([]byte(`{"A":1,"B":2}`), &v) == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestVersionParsers(t *testing.T) {
	for _, c := range []struct {
		parse func([]byte) (string, bool)
		out   string
		want  string
	}{
		{ParseRuffVersion, "ruff 0.16.3\n", "0.16.3"},
		{ParseMypyVersion, "mypy 2.4.1 (compiled: yes)\n", "2.4.1"},
		{ParsePyrightVersion, "pyright 1.1.414\n", "1.1.414"},
		{ParseBanditVersion, "bandit 1.9.4\n  python version = 3.12.3 (main)\n", "1.9.4"},
	} {
		if v, ok := c.parse([]byte(c.out)); !ok || v != c.want {
			t.Errorf("%q: %q %v", c.out, v, ok)
		}
		if _, ok := c.parse([]byte("x " + c.out)); ok {
			t.Errorf("garbage accepted for %q", c.out)
		}
	}
	if !seriesTested(RuffTestedSeries)("0.16.9") || seriesTested(RuffTestedSeries)("0.17.0") {
		t.Fatal("series")
	}
	if banditParserPython([]byte("bandit 1.9.4\n  python version = 3.12.3 (main)\n")) != "3.12" {
		t.Fatal("bandit python")
	}
}

// The rule packs are the documented selections (docs/ANALYZERS.md#python-규칙-팩).
func TestRulePackSelections(t *testing.T) {
	if slices.ContainsFunc(strings.Split(RuffRules, ","), func(r string) bool { return strings.HasPrefix(r, "S") }) || !slices.Contains(RuffSelection, "--isolated") || !slices.Contains(RuffSelection, "--unfixable") {
		t.Fatalf("ruff %v", RuffSelection)
	}
	if !slices.Equal(MypyDisabledCodes, []string{"await-not-async", "name-defined", "no-redef", "str-format"}) || !slices.Contains(MypySelection, "--config-file=") || !slices.Contains(MypySelection, "--no-site-packages") {
		t.Fatalf("mypy %v", MypySelection)
	}
	if !slices.Contains(PyrightSelection, "config=codeye-managed") || len(PyrightRuleOverrides) != 5 {
		t.Fatalf("pyright %v", PyrightSelection)
	}
	if BanditSkips != "B101,B105,B106,B107,B613" {
		t.Fatal(BanditSkips)
	}
	cfg := string(PyrightConfig(11, "../../ws"))
	for _, want := range []string{`"pythonVersion":"3.11"`, `"venv":"codeye-empty"`, `"reportUndefinedVariable":"none"`} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("pyright config lacks %s:\n%s", want, cfg)
		}
	}
}
