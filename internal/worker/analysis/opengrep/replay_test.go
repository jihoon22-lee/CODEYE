package opengrep

import (
	"bytes"
	"os"
	"slices"
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

// A partially parsed file keeps its findings but is not claimed processed.
func TestReplayOpengrepPartialParse(t *testing.T) {
	got := replay(t, replayCase{
		stdout: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"toolExecutionNotifications":[]`), []byte(`"toolExecutionNotifications":[{"descriptor":{"id":"Syntax error"},"level":"warning","message":{"text":"Syntax error at line python/ssrf.py:1"}}]`), 1)
		},
		copy: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"errors":[]`), []byte(`"errors":[{"code":3,"level":"warn","type":["PartialParsing",[]],"message":"Syntax error","path":"python/ssrf.py"}]`), 1)
		}})
	if got.res.Status != domain.TaskSucceeded || slices.Contains(got.doc.Coverage.ProcessedPaths, "python/ssrf.py") || got.doc.Target.Properties["incomplete_files"] != "python/ssrf.py" ||
		!slices.Contains(got.doc.Limitations, LimIncomplete) {
		t.Fatalf("%s/%s processed %v props %v lims %v", got.res.Status, got.res.Reason, got.doc.Coverage.ProcessedPaths, got.doc.Target.Properties, got.doc.Limitations)
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
