package report

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// W16 (docs/41): the real cppcheck 2.22.0 and clang-tidy 23.1.5 envelopes
// (examples/observations-{cppcheck,clang-tidy}.json, the fixture with its
// generated header) through the default registry in CI: cppcheck is the
// required baseline, clang-tidy extra depth; observations become unreviewed
// low tool findings and two engines on one line stay two findings (TC-102);
// the processed files are the union of translation units; the compile DB is
// the recorded repository configuration of both rows (TC-104).
func TestAnalyzerDecoderCppPack(t *testing.T) {
	read := func(name string) map[string]any {
		b, err := os.ReadFile("../../examples/observations-" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	f := analyzerFixture(t, nil,
		taskSpec{key: "cppcheck", status: "succeeded", required: true, doc: read("cppcheck")},
		taskSpec{key: "clang-tidy", status: "succeeded", required: false, doc: read("clang-tidy")})
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if r.Summary.RawObservations != 4+6 || r.Summary.TotalFindings != 4+6 || r.Summary.ActionableFindings != 0 {
		t.Fatalf("summary %+v", r.Summary)
	}
	units := []string{"src/parse.cpp", "src/ring.c", "src/util.cpp"}
	for key, want := range map[string]struct {
		analyzer, version, suppressions string
	}{"cppcheck": {"cpp.cppcheck", "2.22.0", "disabled"}, "clang-tidy": {"cpp.clang_tidy", "23.1.5", "honored"}} {
		row := toolRow(r, key)
		if row == nil || row.Analyzer != want.analyzer || row.ToolVersion == nil || *row.ToolVersion != want.version || row.ExitClass != "success_with_findings" ||
			!slices.Equal(row.ProcessedPaths, units) || row.ProcessedFiles == nil || *row.ProcessedFiles != 3 {
			t.Fatalf("%s row %+v", key, row)
		}
		rc := row.RuleContext
		if rc == nil || rc.RepositoryConfig != "honored" || rc.Config != "recorded" || !slices.Equal(rc.ConfigPaths, []string{"compile_commands.json"}) || rc.ConfigDigest == "" ||
			rc.SourceSuppressions != want.suppressions {
			t.Fatalf("%s rule context %+v", key, rc)
		}
	}
	// Both engines report ring.c:12 (memleak, clang-analyzer-unix.Malloc):
	// two findings, never merged across tools.
	ring12 := 0
	for _, x := range r.Findings {
		if x.Validation != "unreviewed" || x.Severity != "low" || x.FingerprintBasis == nil {
			t.Fatalf("tool finding %+v", x)
		}
		if x.Locations[0].Path == "src/ring.c" && x.Locations[0].StartLine == 12 {
			ring12++
		}
	}
	if ring12 != 2 {
		t.Fatalf("ring.c:12 findings %d", ring12)
	}
	if r.Coverage.ToolProcessedFiles == nil || *r.Coverage.ToolProcessedFiles != 3 {
		t.Fatalf("processed union %v", r.Coverage.ToolProcessedFiles)
	}
	// Code-free export: no tool message and no configuration path or digest.
	b, err := Marshal(Project(r, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Observations {
		if strings.Contains(string(b), *o.RawMessage) {
			t.Fatalf("code-free export carries %q", *o.RawMessage)
		}
	}
	// (The cppcheck argv names CODEYE's own scratch DB, "--project=[REDACTED]/
	// compile_commands.json", which is not the repository path.)
	if digest := toolRow(r, "cppcheck").RuleContext.ConfigDigest; strings.Contains(string(b), `"config_paths"`) || strings.Contains(string(b), digest) {
		t.Fatal("code-free export carries the configuration paths or digest")
	}
}
