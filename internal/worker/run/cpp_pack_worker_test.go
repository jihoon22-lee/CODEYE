//go:build linux

package run

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

// The C/C++ pack (docs/ANALYZERS.md) through the production composition and
// the real worker:
// discovery → the analyzer composite with the C/C++ pack → review roles,
// artifacts uploaded, report built by the default registry.

func cppFixture(t *testing.T, add map[string]string) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "fixtures", "cpp", "input")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range add {
		files[k] = v
	}
	return files
}

func toolRows(r report.Report) map[string]report.ToolCoverage {
	out := map[string]report.ToolCoverage{}
	for _, row := range r.Coverage.Tools {
		out[row.Analyzer] = row
	}
	return out
}

// TC-105 in CI conditions (neither tool): both C/C++ tasks are planned and
// visible, cppcheck is the required baseline of the ecosystem, no
// pack-gap.cpp task exists, and the report carries a not-run row for each.
func TestCppPackWithoutTools(t *testing.T) {
	r := runPackFiles(t, cppFixture(t, nil), nil)
	rows := taskRows(r.manifest)
	if rows["cppcheck"] != "unsupported/tool_not_configured" || rows["clang-tidy"] != "unsupported/tool_not_configured" {
		t.Fatalf("tasks %v", rows)
	}
	if _, ok := rows["pack-gap.cpp"]; ok {
		t.Fatalf("gap task planned: %v", rows)
	}
	for _, task := range r.manifest.Tasks {
		if (task.Key == "cppcheck") != task.Required && (task.Key == "cppcheck" || task.Key == "clang-tidy") {
			t.Fatalf("required %+v", task)
		}
	}
	for key, id := range map[string]string{"cppcheck": "cpp.cppcheck", "clang-tidy": "cpp.clang_tidy"} {
		d, ok := r.docs[key]
		if !ok || d.Analyzer.ID != id || d.Analyzer.Required != (key == "cppcheck") || d.Outcome.ExitClass != obsdoc.ExitNotRun {
			t.Fatalf("%s envelope %+v", key, d)
		}
	}
	tools := toolRows(r.report)
	if tools["cpp.cppcheck"].ExitClass != "not_run" || tools["cpp.clang_tidy"].ExitClass != "not_run" {
		t.Fatalf("tool rows %+v", r.report.Coverage.Tools)
	}
	if names := cppToolStates(r.envs); !slices.Equal(names, []string{"clang-tidy=" + envdoc.ObsNotProbed, "cppcheck=" + envdoc.ObsNotProbed}) {
		t.Fatalf("analyzer tools %v", names)
	}
}

// cppToolStates are the discovery records of the two C/C++ tools; tools of
// other packs (and the always-recorded "any" ecosystem) are not compared.
func cppToolStates(envs []envdoc.Document) []string {
	var out []string
	for _, at := range analyzerTools(envs) {
		if at.Tool == "cppcheck" || at.Tool == "clang-tidy" {
			out = append(out, at.Tool+"="+at.Status)
		}
	}
	return out
}

func cppTools(t *testing.T) map[string]string {
	t.Helper()
	cc, ct := os.Getenv("CODEYE_TEST_CPPCHECK"), os.Getenv("CODEYE_TEST_CLANG_TIDY")
	if cc == "" || ct == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK and CODEYE_TEST_CLANG_TIDY to the cppcheck and clang-tidy binaries")
	}
	return map[string]string{"cppcheck": cc, "clang-tidy": ct}
}

// TC-105..TC-107 end to end with the real tools: both tasks succeed on the
// fixture with its generated header, the worker records the executions, the
// report decoder accepts both envelopes (hyphenated tool name, Required
// consistent) and the tool rows carry the rule context and processed paths.
func TestCppPackWithRealTools(t *testing.T) {
	tools := cppTools(t)
	r := runPackFiles(t, cppFixture(t, map[string]string{"_build/gen/config.h": "#define RINGBUF_VERSION \"1.0\"\n"}), tools)
	rows := taskRows(r.manifest)
	if rows["cppcheck"] != "succeeded/" || rows["clang-tidy"] != "succeeded/" {
		t.Fatalf("tasks %v", rows)
	}
	want := map[string][]string{"cppcheck": {"cppcheck.version", "cppcheck.check"}, "clang-tidy": {"clang-tidy.version", "clang-tidy.check.1"}}
	for _, task := range r.manifest.Tasks {
		if w, ok := want[task.Key]; ok {
			var names []string
			for _, e := range task.Executions {
				names = append(names, e.Name)
				if e.StdoutArtifactID == nil || e.Command.Dir != "[REDACTED]" {
					t.Fatalf("%s execution %+v", task.Key, e)
				}
			}
			if !slices.Equal(names, w) || task.Required != (task.Key == "cppcheck") {
				t.Fatalf("%s executions %v required %v", task.Key, names, task.Required)
			}
		}
	}
	tr := toolRows(r.report)
	cc, ct := tr["cpp.cppcheck"], tr["cpp.clang_tidy"]
	if cc.ExitClass != obsdoc.ExitSuccessWithFindings || ct.ExitClass != obsdoc.ExitSuccessWithFindings ||
		!slices.Equal(cc.ProcessedPaths, []string{"src/parse.cpp", "src/ring.c", "src/util.cpp"}) || !slices.Equal(ct.ProcessedPaths, cc.ProcessedPaths) {
		t.Fatalf("tool rows cppcheck %+v clang-tidy %+v", cc, ct)
	}
	if cc.RuleContext == nil || cc.RuleContext.RepositoryConfig != "honored" || cc.RuleContext.SourceSuppressions != "disabled" ||
		ct.RuleContext == nil || ct.RuleContext.RepositoryConfig != "honored" || ct.RuleContext.SourceSuppressions != "honored" {
		t.Fatalf("rule context %+v %+v", cc.RuleContext, ct.RuleContext)
	}
	var obs []string
	for _, o := range r.report.Observations {
		if o.Tool == "cpp.cppcheck" || o.Tool == "cpp.clang_tidy" {
			obs = append(obs, o.Tool+" "+o.RuleID)
		}
	}
	if !slices.Contains(obs, "cpp.cppcheck nullPointer") || !slices.Contains(obs, "cpp.clang_tidy clang-analyzer-core.NullDereference") {
		t.Fatalf("report observations %v", obs)
	}
	if at := cppToolStates(r.envs); !slices.Equal(at, []string{"clang-tidy=" + envdoc.ObsObserved, "cppcheck=" + envdoc.ObsObserved}) {
		t.Fatalf("analyzer tools %v", at)
	}
}

// Security end to end: a repository source that includes a host file by
// absolute path. The tools read it; no byte of it reaches any uploaded
// artifact (streams, envelopes, manifest) or the report.
func TestCppPackHostFileNotStored(t *testing.T) {
	tools := cppTools(t)
	host := t.TempDir()
	const canary = "CPPCANARY_E2E_5be03d"
	if err := os.WriteFile(filepath.Join(host, "secret.h"), []byte(canary+" token\nstatic int leak(void) { int *p = 0; return *p; } /* "+canary+" */\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inc := "#include \"" + filepath.ToSlash(filepath.Join(host, "secret.h")) + "\"\n"
	db, _ := json.Marshal([]map[string]any{{"directory": "/build/leak", "file": "src/leak.c", "arguments": []string{"cc", "-c", "src/leak.c"}}})
	files := map[string]string{"src/leak.c": inc + "int use(void) { return leak(); }\n", "compile_commands.json": string(db)}
	r := runPackFiles(t, files, tools)
	rows := taskRows(r.manifest)
	if rows["cppcheck"] == "" || !strings.HasPrefix(rows["clang-tidy"], "failed/") {
		t.Fatalf("tasks %v", rows)
	}
	rep, _ := json.Marshal(r.report)
	if strings.Contains(string(rep), canary) {
		t.Fatal("host file content in the report")
	}
	if len(r.blobs) == 0 {
		t.Fatal("no uploaded artifacts")
	}
	for _, b := range r.blobs {
		if strings.Contains(string(b), canary) {
			t.Fatalf("host file content in an uploaded artifact:\n%s", b)
		}
	}
	for _, key := range []string{"cppcheck", "clang-tidy"} {
		if d := r.docs[key]; !slices.Contains(d.Limitations, "outside_workspace_diagnostics_dropped") {
			t.Fatalf("%s limitations %v", key, d.Limitations)
		}
	}
}
