//go:build linux

package run

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

// pythonFiles is a small Python project: a pyproject that declares the
// version, a ruff finding (F401), a bandit finding (B602) and a mypy/pyright
// type error.
var pythonFiles = map[string]string{
	"pyproject.toml":  "[project]\nname = \"app\"\nrequires-python = \">=3.11\"\n",
	"app/__init__.py": "",
	"app/main.py": "import os\nimport subprocess\n\n\ndef run(cmd: str) -> int:\n    return subprocess.call(cmd, shell=True)\n\n\n" +
		"def size(x: int) -> str:\n    return x\n",
	"README.md": "# app\n",
}

// TC-108 (docs/ANALYZERS.md#python-팩): the Python pack replaces the
// pack-gap.python gap. On a node with --go only, a Python snapshot plans the
// pack's tasks: the ruff baseline is required and unsupported
// (tool_not_configured), so the report is partial with a required task that
// did not succeed; mypy, pyright and bandit are visible not-required
// unsupported tasks; no gap task is planned. The any-ecosystem analyzers plan their
// not-required tool_not_configured tasks after them.
func TestPythonSnapshotWithoutRuff(t *testing.T) {
	r := runPackFiles(t, pythonFiles, nil)
	rows := taskRows(r.manifest)
	var static []string
	for _, task := range r.manifest.Tasks {
		if r.stages[task.Key] != domain.StageStaticAnalysis {
			continue
		}
		static = append(static, task.Key)
		if task.Required != (task.Key == "ruff") {
			t.Fatalf("required flag %+v", task)
		}
	}
	// The any-ecosystem analyzers (docs/ANALYZERS.md) also plan, not required.
	pyKeys := []string{"ruff", "mypy", "pyright", "bandit"}
	if !slices.Equal(static, append(slices.Clone(pyKeys), anyEcosystemTasks()...)) {
		t.Fatalf("static tasks %v (all %v)", static, rows)
	}
	for _, k := range pyKeys {
		if rows[k] != "unsupported/tool_not_configured" {
			t.Fatalf("%s: %s", k, rows[k])
		}
		d := r.docs[k]
		if d.Outcome.ExitClass != obsdoc.ExitNotRun || len(d.Executions) != 0 || len(d.Coverage.ProcessedPaths) != 0 || d.Module.Dir != "." {
			t.Fatalf("%s envelope %+v", k, d)
		}
	}
	rep := r.report
	if rep.Status != report.StatusPartial || rep.QualityVerdict != report.VerdictInconclusive || slices.Contains(rep.Limitations, "static_analysis_gap:python") ||
		!slices.Contains(rep.Limitations, "required_task_not_succeeded") {
		t.Fatalf("report %s/%s limitations %v", rep.Status, rep.QualityVerdict, rep.Limitations)
	}
	required := false
	for _, task := range rep.Tasks {
		if task.Key == "ruff" && task.RequiredForVerdict && task.Status == "unsupported" {
			required = true
		}
	}
	if !required {
		t.Fatalf("ruff report task %+v", rep.Tasks)
	}
	at := analyzerTools(r.envs)
	var tools []string
	for _, a := range at {
		tools = append(tools, a.Tool+"="+string(a.Status))
	}
	if !slices.Equal(tools, []string{"bandit=not_probed", "gitleaks=not_probed", "import-linter=not_probed", "jscpd=not_probed", "lizard=not_probed", "mypy=not_probed", "opengrep=not_probed", "pyright=not_probed", "ruff=not_probed"}) {
		t.Fatalf("analyzer tools %v", tools)
	}
}

// The real e2e (TC-108..TC-110 with CODEYE_TEST_RUFF and optionally
// CODEYE_TEST_MYPY, CODEYE_TEST_BANDIT, CODEYE_TEST_PYRIGHT with
// CODEYE_TEST_NODE): the production composition runs the configured Python
// tools on the attempt workspace; each task succeeds with findings, binds
// its executions and lists the processed files; the version comes from
// requires-python.
func TestPythonPackWithRealTools(t *testing.T) {
	ruff := os.Getenv("CODEYE_TEST_RUFF")
	if ruff == "" {
		t.Skip("set CODEYE_TEST_RUFF to the ruff binary")
	}
	tools := map[string]string{"ruff": ruff}
	want := map[string]string{"ruff": "F401"}
	if p := os.Getenv("CODEYE_TEST_MYPY"); p != "" {
		tools["mypy"], want["mypy"] = p, "return-value"
	}
	if p := os.Getenv("CODEYE_TEST_BANDIT"); p != "" {
		tools["bandit"], want["bandit"] = p, "B602"
	}
	if p, node := os.Getenv("CODEYE_TEST_PYRIGHT"), os.Getenv("CODEYE_TEST_NODE"); p != "" && node != "" {
		tools["pyright"], tools["node"], want["pyright"] = p, node, "reportReturnType"
	}
	r := runPackFiles(t, pythonFiles, tools)
	rows := taskRows(r.manifest)
	for key, rule := range want {
		if rows[key] != "succeeded/" {
			t.Fatalf("%s: %s (all %v)", key, rows[key], rows)
		}
		d := r.docs[key]
		rules := map[string]bool{}
		for _, o := range d.Observations {
			rules[o.RuleID] = true
		}
		// bandit parses with its own interpreter: the declared version is
		// recorded, not applied.
		version := map[bool]string{true: "3.11", false: ""}[key != "bandit"]
		if !rules[rule] || d.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !slices.Equal(d.Coverage.ProcessedPaths, []string{"app/__init__.py", "app/main.py"}) ||
			d.Target.Properties["python_version"] != version || d.Target.Properties["python_version_source"] != "requires_python" ||
			d.Target.Properties["python_version_declared"] != ">=3.11" {
			t.Fatalf("%s envelope rules %v outcome %+v coverage %+v target %+v", key, rules, d.Outcome, d.Coverage, d.Target)
		}
		var names []string
		for _, task := range r.manifest.Tasks {
			if task.Key != key {
				continue
			}
			for _, e := range task.Executions {
				names = append(names, e.Name)
				if e.StdoutArtifactID == nil {
					t.Fatalf("%s execution without stdout %+v", key, e)
				}
			}
		}
		if len(names) < 2 || names[0] != key+".version" {
			t.Fatalf("%s executions %v", key, names)
		}
		// The both-checkers selection is recorded exactly when both run.
		_, both := want["pyright"]
		if _, m := want["mypy"]; (key == "mypy" || key == "pyright") && slices.Contains(d.Limitations, "python_type_checkers_both_selected") != (both && m) {
			t.Fatalf("%s limitations %v", key, d.Limitations)
		}
		t.Logf("%s: %d observations, executions %v, limitations %v", key, len(d.Observations), names, d.Limitations)
	}
	for _, a := range analyzerTools(r.envs) {
		if _, ok := tools[a.Tool]; ok && (a.Status != envdoc.ObsObserved || a.Version == nil) {
			t.Fatalf("analyzer tool %+v", a)
		}
	}
	for _, f := range r.report.Findings {
		if strings.Contains(f.Title, r.complete.AttemptID) {
			t.Fatalf("finding leaks the attempt path: %+v", f)
		}
	}
}
