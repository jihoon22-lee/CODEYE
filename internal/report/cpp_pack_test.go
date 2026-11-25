package report

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// The real cppcheck 2.22.0 and clang-tidy 23.1.5 envelopes
// (docs/ANALYZERS.md; examples/observations-{cppcheck,clang-tidy}.json, the fixture with its
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
	if r.Summary.RawObservations != 4+5 || r.Summary.TotalFindings != 4+5 || r.Summary.ActionableFindings != 0 {
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
		if x.Validation != "unreviewed" || !defaultSeverityOK(x) || x.FingerprintBasis == nil {
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

func TestClangNotesStayWithOneDiagnosticInAllProjections(t *testing.T) {
	f := analyzerFixture(t, nil, taskSpec{key: "clang-tidy", status: "succeeded", doc: readExample(t, "clang-tidy")})
	r := build(t, f.in, DefaultRegistry())
	var main *Observation
	for i := range r.Observations {
		if r.Observations[i].RuleID == "clang-analyzer-unix.Malloc" {
			main = &r.Observations[i]
			break
		}
	}
	if main == nil || main.Location == nil || main.Location.StartLine != 12 || len(main.Related) != 6 {
		t.Fatalf("ordered tool notes lost: %+v", main)
	}
	for i, want := range []int{7, 8, 8, 11, 11, 12} {
		note := main.Related[i]
		if note.Location == nil || note.Location.StartLine != want || note.RawRef == nil || note.RawRef.ArtifactID != main.RawRef.ArtifactID {
			t.Fatalf("wrong note %d: %+v", i, note)
		}
	}
	if main.Related[1].Kind != "condition_assumption" || main.Related[2].Kind != "assumed_branch" {
		t.Fatal("clang-analyzer assumptions not classified")
	}
	full, err := RenderHTML(r, "test", Korean)
	if err != nil || !strings.Contains(string(full), "분석기가 가정한 조건") ||
		!strings.Contains(string(full), "Memory is allocated") {
		t.Fatalf("render lost the tool notes: %v", err)
	}
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	for _, o := range free.Observations {
		for _, note := range o.Related {
			if note.RawMessage != nil || note.Excerpt != nil {
				t.Fatal("code-free projection leaked a note")
			}
		}
	}
	if main.Related[0].RawMessage == nil {
		t.Fatal("projection modified the stored report")
	}
}

func TestDuplicateClangRuleRetainsSeparateDiagnosticNotesInExports(t *testing.T) {
	d := readExample(t, "clang-tidy")
	observations := map[string]string{}
	for _, raw := range d["observations"].([]any) {
		o := raw.(map[string]any)
		switch o["rule_id"] {
		case "clang-analyzer-unix.Malloc":
			observations[o["id"].(string)] = "src/ring.c:7-7"
		case "bugprone-use-after-move":
			observations[o["id"].(string)] = "src/util.cpp:8-8"
			o["rule_id"] = "clang-analyzer-unix.Malloc"
		}
	}
	if len(observations) != 2 {
		t.Fatal("need two observations in different files for the same rule")
	}
	f := analyzerFixture(t, nil, taskSpec{key: "clang-tidy", status: "succeeded", doc: d})
	r := build(t, f.in, DefaultRegistry())
	for _, include := range []bool{true, false} {
		projected := Project(r, include)
		if err := ValidateReport(projected); err != nil {
			t.Fatal(err)
		}
		for _, level := range []string{DetailSummary, DetailFull} {
			full, err := RenderHTMLAt(projected, "test", Korean, level)
			if err != nil {
				t.Fatal(err)
			}
			body := string(full)
			_, folded, found := strings.Cut(body, `<details class="report-related-diagnostic" id="static-detail-`)
			if !found || strings.Count(body, `class="report-related-diagnostic"`) != 1 {
				t.Fatalf("%s/%t: missing individual disclosure for the second diagnostic", level, include)
			}
			id, _, _ := strings.Cut(folded, `"`)
			folded, _, _ = strings.Cut(folded, "</details>")
			if expected := observations[id]; expected == "" || !strings.Contains(folded, `title="`+expected+`"`) {
				t.Fatalf("%s/%t: the second diagnostic lost its own related position", level, include)
			}
			for id, position := range observations {
				if !strings.Contains(body, `title="`+position+`"`) || !strings.Contains(string(RenderMarkdownAt(projected, "test", Korean, level)), position) {
					t.Fatalf("%s/%t: lost related position %s of %s", level, include, position, id)
				}
			}
			for _, note := range []string{"is not equal to null", "move occurred here"} {
				if strings.Contains(body, note) != include {
					t.Fatalf("%s/%t: incorrect tool note projection %q", level, include, note)
				}
			}
		}
	}
}
