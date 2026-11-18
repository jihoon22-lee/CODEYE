package cpp

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
)

// fixtures/w16-cpp/expected.json: the expected outcome of each analyzer on
// variants of fixtures/w16-cpp/input. The real-tool tests compare the tools'
// results with it; the replay tests compare the parsers' results on the
// captured streams (testdata/) with the same file, in CI.

type expectedTool struct {
	TaskStatus   string            `json:"task_status"`
	Reason       string            `json:"reason"`
	ExitClass    string            `json:"exit_class"`
	Units        map[string]int    `json:"units"`
	Processed    *[]string         `json:"processed_paths"`
	Observations []string          `json:"observations"` // "rule path:line"
	ToolErrors   []string          `json:"tool_errors"`  // "kind path:line" or "kind -"
	Limitations  []string          `json:"limitations"`
	Executions   []string          `json:"executions"`
	Properties   map[string]string `json:"properties"`
}

type expectedCase struct {
	Description string            `json:"description"`
	Add         map[string]string `json:"add"`
	Remove      []string          `json:"remove"`
	Scope       []string          `json:"scope"`
	BatchSize   int               `json:"batch_size"`
	// RealOnly cases pin a tool behavior (a repository file the tool must
	// not read); replaying captured streams would not test it.
	RealOnly  bool          `json:"real_only"`
	Cppcheck  *expectedTool `json:"cppcheck"`
	ClangTidy *expectedTool `json:"clang_tidy"`
}

type expectedFixture struct {
	Description string `json:"description"`
	CompileDB   struct {
		Entries         int      `json:"entries"`
		Units           []string `json:"units"`
		EntriesExcluded string   `json:"entries_excluded"`
		FlagsDropped    string   `json:"flags_dropped"`
		Limitations     []string `json:"limitations"`
	} `json:"compile_db"`
	Cases map[string]expectedCase `json:"cases"`
}

func loadExpected(t *testing.T) expectedFixture {
	t.Helper()
	b, err := os.ReadFile(fixtureExpected)
	if err != nil {
		t.Fatal(err)
	}
	var e expectedFixture
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("expected.json: %v", err)
	}
	return e
}

func caseNames(e expectedFixture) []string {
	var out []string
	for k := range e.Cases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// caseFiles is the fixture input with the case's changes.
func caseFiles(t *testing.T, c expectedCase) map[string]string {
	t.Helper()
	files := fixtureFiles(t)
	for _, p := range c.Remove {
		if _, ok := files[p]; !ok {
			t.Fatalf("remove %s: not in the fixture", p)
		}
		delete(files, p)
	}
	for p, body := range c.Add {
		files[p] = body
	}
	return files
}

func caseScope(c expectedCase) domain.Scope {
	if len(c.Scope) == 0 {
		return domain.Scope{Kind: "full"}
	}
	return domain.Scope{Kind: "paths", Paths: c.Scope}
}

// summarize renders the comparable facts of an envelope.
func summarize(d obsdoc.Document) expectedTool {
	s := expectedTool{TaskStatus: string(d.Outcome.TaskStatus), ExitClass: d.Outcome.ExitClass, Units: d.Coverage.Units, Limitations: d.Limitations,
		Observations: []string{}, ToolErrors: []string{}, Executions: []string{}, Properties: map[string]string{}}
	if d.Outcome.Reason != nil {
		s.Reason = *d.Outcome.Reason
	}
	if d.Coverage.ProcessedPaths != nil {
		p := append([]string{}, d.Coverage.ProcessedPaths...)
		s.Processed = &p
	}
	for _, o := range d.Observations {
		pos := "-"
		if o.Location != nil {
			pos = o.Location.Path + ":" + strconv.Itoa(o.Location.StartLine)
		}
		s.Observations = append(s.Observations, o.RuleID+" "+pos)
	}
	sort.Strings(s.Observations)
	for _, te := range d.ToolErrors {
		pos := "-"
		if te.Path != nil {
			pos = *te.Path + ":" + strconv.Itoa(te.Line)
		}
		s.ToolErrors = append(s.ToolErrors, te.Kind+" "+pos)
	}
	sort.Strings(s.ToolErrors)
	for _, e := range d.Executions {
		s.Executions = append(s.Executions, e.Name)
	}
	for k, v := range d.Target.Properties {
		s.Properties[k] = v
	}
	return s
}

// compareTool checks an envelope against the expectation. Properties are
// compared for the keys the expectation lists.
func compareTool(t *testing.T, label string, d obsdoc.Document, want expectedTool) {
	t.Helper()
	got := summarize(d)
	if out := os.Getenv("CODEYE_W16_PRINT"); out != "" {
		// Review aid: record the actual outcome instead of comparing.
		b, _ := json.Marshal(map[string]any{"label": label, "actual": got})
		f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, _ = f.Write(append(b, '\n'))
		return
	}
	var diffs []string
	diff := func(field string, g, w any) {
		if fmt.Sprint(g) != fmt.Sprint(w) {
			diffs = append(diffs, fmt.Sprintf("%s: got %v want %v", field, g, w))
		}
	}
	diff("task_status", got.TaskStatus, want.TaskStatus)
	diff("reason", got.Reason, want.Reason)
	diff("exit_class", got.ExitClass, want.ExitClass)
	diff("units", got.Units, want.Units)
	switch {
	case (got.Processed == nil) != (want.Processed == nil):
		diffs = append(diffs, fmt.Sprintf("processed_paths: got %v want %v", got.Processed, want.Processed))
	case got.Processed != nil && !slices.Equal(*got.Processed, *want.Processed):
		diffs = append(diffs, fmt.Sprintf("processed_paths: got %v want %v", *got.Processed, *want.Processed))
	}
	diff("observations", got.Observations, want.Observations)
	diff("tool_errors", got.ToolErrors, want.ToolErrors)
	diff("limitations", got.Limitations, want.Limitations)
	diff("executions", got.Executions, want.Executions)
	for k, v := range want.Properties {
		diff("property "+k, got.Properties[k], v)
	}
	if len(diffs) > 0 {
		b, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("%s differs from expected.json:\n  %s\nactual:\n%s", label, strings.Join(diffs, "\n  "), b)
	}
}

// The expectation file itself: every case names both analyzers' outcome or
// says why one is absent, and cases are documented.
func TestExpectedFixtureIsComplete(t *testing.T) {
	e := loadExpected(t)
	if len(e.Cases) < 6 {
		t.Fatalf("cases %v", caseNames(e))
	}
	for name, c := range e.Cases {
		if c.Description == "" || (c.Cppcheck == nil && c.ClangTidy == nil) {
			t.Fatalf("case %s incomplete", name)
		}
		for _, tool := range []*expectedTool{c.Cppcheck, c.ClangTidy} {
			if tool != nil && (tool.TaskStatus == "" || tool.ExitClass == "" || (tool.TaskStatus == "succeeded") != (tool.Reason == "")) {
				t.Fatalf("case %s tool expectation %+v", name, tool)
			}
		}
	}
}
