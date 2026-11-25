package opengrep

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without Opengrep: the scan returns the streams and
// JSON copy captured from Opengrep 2.0.0 in testdata/ (real_test.go) for the
// fixtures of helpers_test.go.

type replayCase struct {
	version string
	stdout  func([]byte) []byte
	copy    func([]byte) []byte
	exit    int
	files   map[string][]byte
	noCopy  bool
}

type replayed struct {
	res analysis.TaskResult
	doc obsdoc.Document
	r   *anypacktest.Replay
}

func isScan(s executor.Spec) bool { return len(s.Args) > 0 && s.Args[0] == "scan" }

func replay(t *testing.T, c replayCase) replayed {
	t.Helper()
	fake := packtest.FakeExecutable(t, Name)
	a, err := New(Settings{Executable: fake})
	if err != nil {
		t.Fatal(err)
	}
	if c.files == nil {
		c.files = fixtureFiles(t)
	}
	if c.version == "" {
		c.version = "2.0.0\n"
	}
	rec := anypacktest.LoadCapture(t, "fixtures")
	copyData, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.stdout != nil {
		rec.Stdout = c.stdout(rec.Stdout)
	}
	if c.copy != nil {
		copyData = c.copy(copyData)
	}
	if c.exit != 0 {
		rec.Exit = c.exit
	}
	fx := anypacktest.New(t, "", c.files, domain.Scope{})
	r := &anypacktest.Replay{Fake: fake,
		Answer: func(spec executor.Spec) anypacktest.Recorded {
			if isScan(spec) {
				return rec
			}
			return anypacktest.Recorded{Stdout: []byte(c.version)}
		},
		Mutate: func(spec executor.Spec, _ *executor.Result) {
			if !isScan(spec) || c.noCopy {
				return
			}
			i := slices.Index(spec.Args, "--json-output")
			if err := os.WriteFile(spec.Args[i+1], copyData, 0o600); err != nil {
				t.Fatal(err)
			}
		}}
	res := packtest.RunTask(t, a, fx.Job, Name, r)
	doc, _ := anypacktest.ReadEnvelope(t, fx.Job, res)
	return replayed{res: res, doc: doc, r: r}
}

// TC-290 (replay): the captured scan of the rule pack fixtures gives every
// expected finding with its tool flow and the suppressed ones marked.
func TestReplayOpengrepFixtures(t *testing.T) {
	got := replay(t, replayCase{})
	if got.res.Status != domain.TaskSucceeded || got.doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings {
		t.Fatalf("%s/%s %+v %+v", got.res.Status, got.res.Reason, got.doc.Outcome, got.doc.Parse)
	}
	assertFixtureResult(t, fixtureFiles(t), got.doc)
	scan := got.r.Specs[len(got.r.Specs)-1]
	for _, want := range []string{"--taint-intrafile", "--dataflow-traces", "--no-rewrite-rule-ids", "--sarif", "--quiet"} {
		if !slices.Contains(scan.Args, want) {
			t.Fatalf("scan argv lacks %s: %v", want, scan.Args)
		}
	}
	// No rule source other than CODEYE's files, no interfile analysis and an
	// explicit environment: nothing that downloads rules or reports usage.
	for _, a := range scan.Args {
		if a == "--enable-interfile" || strings.HasPrefix(a, "p/") || a == "auto" || strings.Contains(a, "://") || strings.HasPrefix(a, "git+") {
			t.Fatalf("scan argv names a remote or interfile option: %v", scan.Args)
		}
	}
	for k := range scan.Env {
		if k != "PATH" && k != "LANG" && k != "SystemRoot" {
			t.Fatalf("unexpected environment variable %s", k)
		}
	}
	if i := slices.Index(scan.Args, "--"); i < 0 || !slices.ContainsFunc(scan.Args[i+1:], func(a string) bool { return strings.HasPrefix(a, "./") }) {
		t.Fatalf("targets not after -- as ./paths: %v", scan.Args)
	}
	for _, o := range got.doc.Observations {
		for _, r := range o.Related {
			if strings.Contains(r.RawMessage, "'") || strings.Contains(r.RawMessage, "@") {
				t.Fatalf("flow step quotes source text: %q", r.RawMessage)
			}
		}
	}
}

func TestReplayOpengrepFailures(t *testing.T) {
	cases := map[string]struct {
		c      replayCase
		status domain.TaskStatus
		reason string
	}{
		"rule file rejected": {replayCase{exit: 4}, domain.TaskFailed, ReasonRulePackLoad},
		"fatal error":        {replayCase{exit: 2}, domain.TaskFailed, ReasonFailed},
		"log not sarif":      {replayCase{stdout: func([]byte) []byte { return []byte("{}") }}, domain.TaskFailed, "output_unparsed"},
		"cut log":            {replayCase{stdout: func(b []byte) []byte { return b[:len(b)/2] }}, domain.TaskFailed, "output_unparsed"},
		"unknown sarif member": {replayCase{stdout: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"ruleId":`), []byte(`"baselineState":"new","ruleId":`), 1)
		}}, domain.TaskFailed, "output_unparsed"},
		"rule missing": {replayCase{stdout: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"id":"codeye.python.ssrf"`), []byte(`"id":"codeye.python.other"`), 1)
		}}, domain.TaskFailed, ReasonRulePackLoad},
		"result outside the copy": {replayCase{stdout: func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"uri":"python/ssrf.py"`), []byte(`"uri":"../ssrf.py"`))
		},
			copy: func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte(`"path":"python/ssrf.py"`), []byte(`"path":"../ssrf.py"`))
			}}, domain.TaskFailed, ReasonOutsideSnapshot},
		"json copy missing": {replayCase{noCopy: true}, domain.TaskFailed, "output_unparsed"},
		"json copy disagrees": {replayCase{copy: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"check_id":"codeye.python.ssrf"`), []byte(`"check_id":"codeye.python.sql-injection"`), 1)
		}}, domain.TaskFailed, "output_unparsed"},
		"skipped rule": {replayCase{copy: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"skipped_rules":[]`), []byte(`"skipped_rules":[{"rule_id":"x"}]`), 1)
		}}, domain.TaskFailed, ReasonRulePackLoad},
		"version not recognized": {replayCase{version: "dev build\n"}, domain.TaskFailed, "tool_version_unrecognized"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := replay(t, tc.c)
			if got.res.Status != tc.status || got.res.Reason != tc.reason {
				t.Fatalf("%s/%s, want %s/%s (%+v)", got.res.Status, got.res.Reason, tc.status, tc.reason, got.doc.Parse)
			}
			if got.doc.Coverage.ProcessedPaths != nil && got.doc.Outcome.ExitClass != obsdoc.ExitNotRun {
				t.Fatalf("failed task claims processed files")
			}
		})
	}
}

// A file Opengrep reports an error about (parsed in part, not parsed at all,
// a rule timed out on it; any level, any shape of the error type) keeps its
// findings but is not claimed processed, and the other files keep theirs:
// one bad file does not fail the optional task. The first entries are the
// error objects Opengrep 2.0.0 wrote (testdata/errors.json, paths moved onto
// fixture files); the rest are shapes the adapter must not read as clean.
func TestReplayOpengrepFileErrors(t *testing.T) {
	var captured []struct {
		Error json.RawMessage `json:"error"`
	}
	b, err := os.ReadFile("testdata/errors.json")
	if err != nil || json.Unmarshal(b, &captured) != nil || len(captured) != 3 {
		t.Fatalf("errors.json: %v", err)
	}
	cases := map[string]string{}
	for i, c := range captured {
		cases["captured "+strconv.Itoa(i)] = string(c.Error)
	}
	for name, typ := range map[string]string{"null type": `null`, "empty type": `""`, "empty list": `[]`, "object": `{"PartialParsing":[]}`,
		"nested list": `[["PartialParsing"]]`, "number": `3`} {
		cases[name] = `{"code":3,"level":"warn","type":` + typ + `,"message":"x","path":"python/ssrf.py"}`
	}
	cases["error level"] = `{"code":3,"level":"error","type":"Fatal error","message":"x","path":"python/ssrf.py"}`
	cases["rule timeout"] = `{"code":3,"level":"warn","type":"Timeout","message":"x","path":"python/ssrf.py","rule_id":"codeye.python.ssrf"}`
	note := []byte(`"toolExecutionNotifications":[{"descriptor":{"id":"Syntax error"},"level":"warning","message":{"text":"Syntax error at line \u000c 1"}}]`)
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			var e struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(entry), &e); err != nil {
				t.Fatal(err)
			}
			got := replay(t, replayCase{
				stdout: func(b []byte) []byte { return bytes.Replace(b, []byte(`"toolExecutionNotifications":[]`), note, 1) },
				copy: func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"errors":[]`), []byte(`"errors":[`+entry+`]`), 1)
				}})
			if got.res.Status != domain.TaskSucceeded || slices.Contains(got.doc.Coverage.ProcessedPaths, e.Path) ||
				got.doc.Target.Properties["incomplete_files"] != e.Path || !slices.Contains(got.doc.Limitations, LimIncomplete) {
				t.Fatalf("%s/%s processed %v props %v lims %v", got.res.Status, got.res.Reason, got.doc.Coverage.ProcessedPaths, got.doc.Target.Properties, got.doc.Limitations)
			}
			if got.doc.Coverage.Units[UnitIncomplete] != 1 || len(got.doc.Coverage.ProcessedPaths) == 0 || len(got.doc.Observations) == 0 {
				t.Fatalf("units %v observations %d", got.doc.Coverage.Units, len(got.doc.Observations))
			}
		})
	}
	// An error of the run that names no file and no rule fails the task.
	got := replay(t, replayCase{
		stdout: func(b []byte) []byte { return bytes.Replace(b, []byte(`"toolExecutionNotifications":[]`), note, 1) },
		copy: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"errors":[]`), []byte(`"errors":[{"code":2,"level":"error","type":"Fatal error","message":"x"}]`), 1)
		}})
	if got.res.Status != domain.TaskFailed || got.res.Reason != ReasonFailed {
		t.Fatalf("run error: %s/%s", got.res.Status, got.res.Reason)
	}
}

// What the review found the adapter took for success or for the work copy:
// a run that says it did not execute successfully, a columnKind other than
// the measured byte columns, a base id given a URI, and an absolute path
// written after "./".
func TestReplayOpengrepRejectedLogs(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"execution not successful": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"executionSuccessful":true`), []byte(`"executionSuccessful":false`), 1)
		},
		"utf-16 columns": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"results":[`), []byte(`"columnKind":"utf16CodeUnits","results":[`), 1)
		},
		"base given a uri": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"results":[`), []byte(`"originalUriBaseIds":{"%SRCROOT%":{"uri":"file:///elsewhere/"}},"results":[`), 1)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if got := replay(t, replayCase{stdout: mutate}); got.res.Status != domain.TaskFailed {
				t.Fatalf("%s/%s", got.res.Status, got.res.Reason)
			}
		})
	}
	got := replay(t, replayCase{stdout: func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(`"uri":"python/ssrf.py"`), []byte(`"uri":".//etc/passwd"`))
	}, copy: func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(`"path":"python/ssrf.py","start"`), []byte(`"path":".//etc/passwd","start"`))
	}})
	if got.res.Status != domain.TaskFailed || got.res.Reason != ReasonOutsideSnapshot {
		t.Fatalf("absolute path after ./: %s/%s", got.res.Status, got.res.Reason)
	}
}

// The rule context names the rule files the run loaded: a Python-only
// snapshot records the Python file and no other language's.
func TestOpengrepSelectionIsLoadedFiles(t *testing.T) {
	files := map[string][]byte{}
	for p, b := range fixtureFiles(t) {
		if strings.HasPrefix(p, "python/") {
			files[p] = b
		}
	}
	got := replay(t, replayCase{files: files, copy: func(b []byte) []byte {
		return bytes.Replace(b, []byte(`"errors":[]`), []byte(`"errors":[]`), 1)
	}})
	sel := got.doc.RulePack.Selection
	if !slices.ContainsFunc(sel, func(s string) bool { return strings.HasPrefix(s, "rules/python.yaml@") }) ||
		slices.ContainsFunc(sel, func(s string) bool {
			return strings.HasPrefix(s, "rules/") && !strings.HasPrefix(s, "rules/python.yaml@")
		}) {
		t.Fatalf("selection %v", sel)
	}
	if got.doc.Target.Properties["rule_languages"] != "python" {
		t.Fatalf("rule languages %q", got.doc.Target.Properties["rule_languages"])
	}
}

// The path arguments of one scan stay under the Windows command line limit
// there and under 96 KiB elsewhere.
func TestMaxArgBytes(t *testing.T) {
	if maxArgBytes("windows") != 24<<10 || maxArgBytes("linux") != 96<<10 || maxArgBytes("darwin") != 96<<10 {
		t.Fatal("argument bound")
	}
	if 32767-maxArgBytes("windows") < 4096 {
		t.Fatal("no room for the rest of the argv on Windows")
	}
}

// Without source of a rule language nothing is planned; a snapshot of only
// unsupported files plans no task.
func TestPlanOpengrep(t *testing.T) {
	fx := anypacktest.New(t, "", map[string][]byte{"README.md": []byte("x\n"), "lib/a.rb": []byte("x\n")}, domain.Scope{})
	tasks, err := Plan(t.Context(), fx.Job)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("tasks %v err %v", tasks, err)
	}
	fx = anypacktest.New(t, "", map[string][]byte{"app.py": []byte("x = 1\n"), "vendor/v.py": []byte("x = 1\n"), "third_party/t.py": []byte("x = 1\n")}, domain.Scope{})
	tasks, err = Plan(t.Context(), fx.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Key != Name || *tasks[0].FilesInScope != 1 || tasks[0].Spec.Required {
		t.Fatalf("tasks %+v err %v", tasks, err)
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{"2.0.0\n": "2.0.0", "2.0.0": "2.0.0", "v2.0.0\n": "", "2.0\n": "", "2.0.0\n1\n2\n": "", "dev\n": ""} {
		if got, _ := ParseVersion([]byte(in)); got != want {
			t.Errorf("ParseVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if !VersionTested("2.0.0") || VersionTested("1.9.0") {
		t.Fatal("tested series")
	}
}

// The rule pack is one file per language, each rule a taint rule with a CWE.
func TestRulePackShape(t *testing.T) {
	for _, l := range Languages() {
		text := string(l.File)
		if len(l.Rules) == 0 {
			t.Fatalf("%s: no rules", l.Name)
		}
		if strings.Count(text, "\n  - id: ") != len(l.Rules) || strings.Count(text, "mode: taint") != len(l.Rules) ||
			strings.Count(text, "taint_intrafile: true") != len(l.Rules) || strings.Count(text, "cwe: [\"CWE-") != len(l.Rules) {
			t.Fatalf("%s: every rule must be an intra-file taint rule with a CWE", l.Name)
		}
		if strings.Contains(text, "&") && strings.Contains(text, ": *") || strings.Contains(text, "interfile") {
			t.Fatalf("%s: YAML aliases or interfile options", l.Name)
		}
	}
}
