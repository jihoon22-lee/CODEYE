//go:build linux

package run

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Security (W17, docs/42 §9) through the production composition:
// repository configuration that points every path option outside the
// workspace (an outside directory holding a unique canary) is never read.
// With the configured real tools the canary appears in no uploaded artifact
// (streams, envelopes, manifests) and not in the report, and nothing is
// written to the outside directory.
func TestPythonPackOutsideConfigCanary(t *testing.T) {
	ruff := os.Getenv("CODEYE_TEST_RUFF")
	if ruff == "" {
		t.Skip("set CODEYE_TEST_RUFF (and optionally the other Python tools)")
	}
	tools := map[string]string{"ruff": ruff}
	for tool, env := range map[string]string{"mypy": "CODEYE_TEST_MYPY", "bandit": "CODEYE_TEST_BANDIT"} {
		if p := os.Getenv(env); p != "" {
			tools[tool] = p
		}
	}
	if p, node := os.Getenv("CODEYE_TEST_PYRIGHT"), os.Getenv("CODEYE_TEST_NODE"); p != "" && node != "" {
		tools["pyright"], tools["node"] = p, node
	}
	const canary = "W17CANARY_run_5d2e"
	outside := t.TempDir()
	for name, body := range map[string]string{
		"canary_mod.py": "\"\"\"" + canary + "\"\"\"\n\n\ndef fetch() -> int:\n    return \"" + canary + "\"\n",
		"ruff.toml":     "# " + canary + "\n[lint]\nselect = [\"ALL\"]\n" + canary + " = 1\n",
		"plugin.py":     "import pathlib\n\npathlib.Path(__file__).with_name(\"PLUGIN_RAN\").write_text(\"" + canary + "\")\n",
	} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	o := filepath.ToSlash(outside)
	files := map[string]string{
		"pyproject.toml": "[project]\nname = \"canary\"\nrequires-python = \">=3.11\"\n\n[tool.ruff]\nextend = \"" + o + "/ruff.toml\"\ncache-dir = \"" + o + "/rc\"\n\n" +
			"[tool.mypy]\nmypy_path = \"" + o + "\"\nfiles = [\"" + o + "/canary_mod.py\"]\ncache_dir = \"" + o + "/mc\"\nplugins = [\"" + o + "/plugin.py\"]\n\n" +
			"[tool.pyright]\nextraPaths = [\"" + o + "\"]\ninclude = [\"" + o + "\"]\nvenvPath = \"" + o + "\"\nvenv = \"v\"\n",
		".bandit": "[bandit]\ntargets = " + o + "\n",
		"app.py":  "import os\nimport subprocess\n\nimport canary_mod\n\n\ndef run(cmd: str) -> int:\n    canary_mod.fetch()\n    return subprocess.call(cmd, shell=True)\n",
	}
	before, _ := os.ReadDir(outside)
	r := runPackFiles(t, files, tools)
	rows := taskRows(r.manifest)
	for tool := range tools {
		if tool != "node" && rows[tool] != "succeeded/" {
			t.Fatalf("%s: %s (all %v)", tool, rows[tool], rows)
		}
	}
	for i, body := range r.artifacts {
		if bytes.Contains(body, []byte(canary)) || bytes.Contains(body, []byte(o)) {
			t.Fatalf("artifact %d holds the canary or the outside path", i)
		}
	}
	rep, err := json.Marshal(r.report)
	if err != nil || bytes.Contains(rep, []byte(canary)) {
		t.Fatalf("report holds the canary (%v)", err)
	}
	after, _ := os.ReadDir(outside)
	if len(after) != len(before) {
		t.Fatalf("written outside: %v", after)
	}
}
