package python

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Security rules of the pack (docs/ANALYZERS.md#python-입력-보호): repository configuration is
// hostile data, repository-derived argv elements never read as options,
// captured streams carry no source lines, and diagnostics of files outside
// the attempt workspace are dropped.

// positionalsSafe checks that every argument after "--" is "." or starts
// with "./", and that an @file list holds only "./" lines.
func positionalsSafe(t *testing.T, s executor.Spec) {
	t.Helper()
	after := false
	for _, a := range s.Args {
		switch {
		case a == "--":
			after = true
		case after && a != "." && !strings.HasPrefix(a, "./"):
			t.Fatalf("positional %q in %v", a, s.Args)
		case strings.HasPrefix(a, "@"):
			b, err := os.ReadFile(a[1:])
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if !strings.HasPrefix(l, "./") {
					t.Fatalf("file list line %q", l)
				}
			}
		}
	}
	if len(s.Stdin) > 0 {
		for _, l := range strings.Split(strings.TrimSpace(string(s.Stdin)), "\n") {
			if !strings.HasPrefix(l, "./") {
				t.Fatalf("stdin file list line %q", l)
			}
		}
	}
}

func TestArgvPositionalsNeverReadAsOptions(t *testing.T) {
	for _, args := range [][]string{
		RuffArgs(11, []string{"sub"}, []string{".", "-x.py", "--help.py", "pkg/-y.py"}),
		BanditArgs("/scratch/b.ini", "csv", nil, []string{"-x.py", "pkg"}),
	} {
		positionalsSafe(t, executor.Spec{Args: args})
		if i := slices.Index(args, "--"); i < 0 || !slices.Contains(args[i:], "./-x.py") {
			t.Fatalf("argv %v", args)
		}
	}
	if safeNested([]string{"a,b"}) || safeNested([]string{"x*"}) || !safeNested([]string{"-a", "b c"}) {
		t.Fatal("safeNested")
	}
}

// Files named like options (--help.py, -x.py, pkg/-y.py), in a full scope
// and as explicit scope paths, are analyzed as files by every tool.
func TestRealOptionLikeFileNames(t *testing.T) {
	type tc struct {
		tool string
		a    func(*testing.T) planner
		rule string
		file string
	}
	cases := []tc{
		{RuffTool, func(t *testing.T) planner { return newRuffT(t) }, "F401", "--help.py"},
		{MypyTool, func(t *testing.T) planner { return newMypyT(t) }, "return-value", "pkg/-y.py"},
		{PyrightTool, func(t *testing.T) planner { return newPyrightT(t) }, "reportReturnType", "pkg/-y.py"},
		{BanditTool, func(t *testing.T) planner { return newBanditT(t) }, "B602", "pkg/-y.py"},
	}
	for _, c := range cases {
		for _, scope := range []domain.Scope{{Kind: "full"}, {Kind: "paths", Paths: []string{"--help.py", "-x.py", "pkg"}}} {
			t.Run(c.tool+"/"+scope.Kind, func(t *testing.T) {
				if os.Getenv("CODEYE_TEST_"+strings.ToUpper(c.tool)) == "" {
					t.Skipf("set CODEYE_TEST_%s", strings.ToUpper(c.tool))
				}
				a := c.a(t)
				fx := newFixture(t, loadFixture(t, "dashnames"), scope)
				rr := &recordingRunner{inner: &executor.Executor{}}
				res := packtest.RunTask(t, a, fx.Job, c.tool, rr)
				doc, _ := readEnvelope(t, fx.Job, res)
				want := []string{"--help.py", "-x.py", "pkg/-y.py", "pkg/__init__.py"}
				slices.Sort(want)
				if res.Status != domain.TaskSucceeded || !slices.Equal(doc.Coverage.ProcessedPaths, want) {
					t.Fatalf("%s/%s processed %v", res.Status, res.Reason, doc.Coverage.ProcessedPaths)
				}
				found := false
				for _, o := range doc.Observations {
					found = found || (o.RuleID == c.rule && *o.Path == c.file)
				}
				if !found {
					t.Fatalf("no %s on %s: %+v", c.rule, c.file, doc.Observations)
				}
				for _, s := range rr.specs {
					positionalsSafe(t, s)
				}
			})
		}
	}
}

// canary is a unique string only the outside files hold.
const canary = "CANARY_8f3a1c"

// canaryFiles is a project whose repository configuration points every
// path option the tools know outside the workspace (absolute paths into
// outside) and asks for writes there.
func canaryFiles(outside string) map[string]string {
	o := filepath.ToSlash(outside)
	return map[string]string{
		"pyproject.toml": "[project]\nname = \"canary-project\"\nrequires-python = \">=3.11\"\n\n" +
			"[tool.ruff]\nextend = \"" + o + "/ruff.toml\"\nsrc = [\"" + o + "\"]\ncache-dir = \"" + o + "/ruff-cache\"\n\n" +
			"[tool.mypy]\nmypy_path = \"" + o + "\"\nfiles = [\"" + o + "/canary_mod.py\"]\ncache_dir = \"" + o + "/mypy-cache\"\nplugins = [\"" + o + "/plugin.py\"]\nhtml_report = \"" + o + "/html\"\n\n" +
			"[tool.pyright]\nextraPaths = [\"" + o + "\"]\nstubPath = \"" + o + "/stubs\"\ntypeshedPath = \"" + o + "/typeshed\"\nvenvPath = \"" + o + "\"\nvenv = \"venv\"\ninclude = [\"" + o + "\"]\n",
		"pyrightconfig.json": "{\"extraPaths\": [\"" + o + "\"], \"stubPath\": \"" + o + "/stubs\", \"venvPath\": \"" + o + "\", \"venv\": \"venv\", \"include\": [\"" + o + "\"]}\n",
		"mypy.ini":           "[mypy]\nmypy_path = " + o + "\nfiles = " + o + "/canary_mod.py\ncache_dir = " + o + "/mypy-cache\nplugins = " + o + "/plugin.py\nhtml_report = " + o + "/html\n",
		".bandit":            "[bandit]\ntargets = " + o + "\noutput = " + o + "/bandit-out.json\nformat = txt\n",
		"app.py":             "import os\nimport subprocess\n\nimport canary_mod\n\n\ndef size(x: int) -> str:\n    return x\n\n\ndef run(cmd: str) -> int:\n    canary_mod.fetch()\n    return subprocess.call(cmd, shell=True)\n",
	}
}

func writeOutside(t *testing.T) string {
	t.Helper()
	outside := t.TempDir()
	packtest.WriteTree(t, outside, map[string]string{
		"canary_mod.py":        "\"\"\"" + canary + "\"\"\"\n\nCANARY = \"" + canary + "\"\n\n\ndef fetch() -> int:\n    return \"" + canary + "\"\n",
		"ruff.toml":            "# " + canary + "\n[lint]\nselect = [\"ALL\"]\n" + canary + " = 1\n",
		"plugin.py":            "import pathlib\n\npathlib.Path(__file__).with_name(\"PLUGIN_RAN\").write_text(\"" + canary + "\")\n\n\ndef plugin(version):\n    raise SystemExit(\"" + canary + "\")\n",
		"stubs/canary_mod.pyi": "CANARY: str\n\ndef fetch() -> int: ...\n# " + canary + "\n",
		"venv/pyvenv.cfg":      "home = /nonexistent\n",
	})
	return outside
}

// Repository configuration that points outside the workspace is never
// read: every tool runs with the CODEYE configuration only (overridden), the
// canary of the outside files appears in no stream and no envelope, and
// nothing is written outside the task scratch.
func TestRealOutsideConfigCanary(t *testing.T) {
	cases := []struct {
		tool string
		a    func(*testing.T) planner
	}{
		{RuffTool, func(t *testing.T) planner { return newRuffT(t) }},
		{MypyTool, func(t *testing.T) planner { return newMypyT(t) }},
		{PyrightTool, func(t *testing.T) planner { return newPyrightT(t) }},
		{BanditTool, func(t *testing.T) planner { return newBanditT(t) }},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			if os.Getenv("CODEYE_TEST_"+strings.ToUpper(c.tool)) == "" {
				t.Skipf("set CODEYE_TEST_%s", strings.ToUpper(c.tool))
			}
			a := c.a(t)
			outside := writeOutside(t)
			before := packtest.TreeHash(t, outside)
			fx := newFixture(t, canaryFiles(outside), domain.Scope{})
			src := packtest.TreeHash(t, fx.SourceRoot)
			rr := &recordingRunner{inner: &executor.Executor{}}
			res := packtest.RunTask(t, a, fx.Job, c.tool, rr)
			doc, raw := readEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || doc.RulePack.RepositoryConfig != obsdoc.RepoConfigOverridden {
				t.Fatalf("%s/%s %+v %+v", res.Status, res.Reason, doc.Outcome, doc.ToolErrors)
			}
			if bytes.Contains(raw, []byte(canary)) {
				t.Fatal("envelope holds the canary")
			}
			for i, r := range rr.results {
				if bytes.Contains(r.Stdout.Data, []byte(canary)) || bytes.Contains(r.Stderr.Data, []byte(canary)) {
					t.Fatalf("execution %d streams hold the canary", i)
				}
				for _, arg := range rr.specs[i].Args {
					if strings.Contains(arg, outside) {
						t.Fatalf("argv names the outside directory: %v", rr.specs[i].Args)
					}
				}
			}
			if packtest.TreeHash(t, outside) != before || packtest.TreeHash(t, fx.SourceRoot) != src {
				t.Fatal("a tool wrote outside the task scratch")
			}
			for _, p := range doc.Coverage.ProcessedPaths {
				if p != "app.py" {
					t.Fatalf("processed %v", doc.Coverage.ProcessedPaths)
				}
			}
		})
	}
}

// Captured streams carry no source line: ruff runs with --unfixable ALL
// (fix edits copy source text), bandit's JSON (with its "code" lines) goes
// to a scratch file and stdout is the code-free CSV.
func TestRealStreamsHoldNoSourceLines(t *testing.T) {
	files := loadFixture(t, "findings")
	var lines []string
	for rel, body := range files {
		if strings.HasSuffix(rel, ".py") {
			for _, l := range strings.Split(body, "\n") {
				if l = strings.TrimSpace(l); len(l) >= 12 && !strings.HasPrefix(l, "\"\"\"") && !strings.HasPrefix(l, "#") {
					lines = append(lines, l)
				}
			}
		}
	}
	for _, c := range []struct {
		tool string
		a    func(*testing.T) planner
	}{
		{RuffTool, func(t *testing.T) planner { return newRuffT(t) }},
		{MypyTool, func(t *testing.T) planner { return newMypyT(t) }},
		{PyrightTool, func(t *testing.T) planner { return newPyrightT(t) }},
		{BanditTool, func(t *testing.T) planner { return newBanditT(t) }},
	} {
		t.Run(c.tool, func(t *testing.T) {
			if os.Getenv("CODEYE_TEST_"+strings.ToUpper(c.tool)) == "" {
				t.Skipf("set CODEYE_TEST_%s", strings.ToUpper(c.tool))
			}
			fx := newFixture(t, files, domain.Scope{})
			rr := &recordingRunner{inner: &executor.Executor{}}
			res := packtest.RunTask(t, c.a(t), fx.Job, c.tool, rr)
			if res.Status != domain.TaskSucceeded {
				t.Fatalf("%s/%s", res.Status, res.Reason)
			}
			for _, r := range rr.results {
				for _, l := range lines {
					if bytes.Contains(r.Stdout.Data, []byte(l)) || bytes.Contains(r.Stderr.Data, []byte(l)) {
						t.Fatalf("captured stream holds the source line %q", l)
					}
				}
				if bytes.Contains(r.Stdout.Data, []byte(`"content":`)) {
					t.Fatal("ruff fix edits in the stream")
				}
			}
		})
	}
}

// A diagnostic or tool error of a file outside the attempt workspace is
// dropped with its text and only counted (replayed: ruff reports a file
// outside the workspace).
func TestReplayOutsideDiagnosticsDropped(t *testing.T) {
	outsideLine := []byte(`{"cell":null,"code":"F401","end_location":{"column":10,"row":1},"filename":"/opt/elsewhere/` + canary + `.py","fix":null,"location":{"column":8,"row":1},"message":"` + canary + `","name":"unused-import","noqa_row":1,"severity":"error","url":null}` + "\n")
	r := runReplay(t, RuffTool, "findings", func(name string, _ executor.Spec, res *executor.Result) {
		if name == "ruff.check" || name == "ruff.check-ignore-noqa" {
			res.Stdout.Data = append(append([]byte{}, res.Stdout.Data...), outsideLine...)
		}
	})
	if r.res.Status != domain.TaskSucceeded || !hasLimit(r.doc, LimOutsideDropped) || bytes.Contains(r.raw, []byte(canary)) {
		t.Fatalf("%s/%s limitations %v", r.res.Status, r.res.Reason, r.doc.Limitations)
	}
	if got := rulesOf(r.doc, false)["F401"]; got != 1 {
		t.Fatalf("F401 %d", got)
	}
}
