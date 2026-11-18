package tsc

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without Node: the executions return the redacted
// streams captured from tsc 6.0.3 in testdata/ (see the capture command in
// real_test.go). The fixture sources are the ones the captures were taken
// from; changing them requires a new capture.

func replayFiles(t *testing.T, name string) map[string]string {
	cases := map[string]map[string]string{
		"clean":      {"tsconfig.json": fixCleanConfig, "src/a.ts": fixClean},
		"gap":        {"tsconfig.json": fixConfig, "src/main.ts": fixGap},
		"syntax":     {"tsconfig.json": fixConfig, "src/bad.ts": fixSyntax, "src/ok.ts": fixTypeErr},
		"solution":   {"tsconfig.json": fixSolution, "tsconfig.app.json": fixSolutionApp, "tsconfig.node.json": fixSolutionNod, "src/main.ts": fixTypeErr, "vite.config.ts": fixClean},
		"extends":    {"tsconfig.json": fixPackageExt, "src/a.ts": fixClean},
		"no-inputs":  {"tsconfig.json": fixNoInputs, "src/a.ts": fixClean},
		"deprecated": {"tsconfig.json": fixDeprecated, "src/a.ts": fixTypeErr},
	}
	if name == "monorepo" {
		return jststest.MonorepoFiles(t, 4)
	}
	files, ok := cases[name]
	if !ok {
		t.Fatalf("no fixture for %s", name)
	}
	return caseFiles(files)
}

func replayRun(t *testing.T, name, key string, mutate func(jststest.Recorded, *executor.Result)) (*jststest.Replay, obsdoc.Document) {
	t.Helper()
	a := fakeAnalyzer(t)
	fx := jststest.NewFixture(t, "", replayFiles(t, name), domain.Scope{})
	r := &jststest.Replay{T: t, Steps: jststest.LoadCapture(t, name), Mutate: mutate, Match: func(step jststest.Recorded, s executor.Spec) bool {
		switch {
		case step.Name == execVersion:
			return slices.Equal(s.Args[1:], []string{"--version"})
		case strings.HasPrefix(step.Name, execConfig):
			return slices.Contains(s.Args, "--showConfig")
		}
		return !slices.Contains(s.Args, "--showConfig") && slices.Contains(s.Args, "--noEmit")
	}}
	res := jststest.RunTask(t, a, fx.Job, key, r)
	doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
	jststest.AssertNoNodePaths(t, raw, nodePaths(a, fx)...)
	for _, s := range r.Specs {
		p := s.Args[slices.Index(s.Args, "-p")+1:]
		if s.Args[1] != "--version" && (len(p) == 0 || !strings.HasPrefix(p[0], "./")) || s.Env["PATH"] != a.script.Node.Dir || !jststest.EnvAllowlisted(s.Env) ||
			!strings.HasPrefix(s.Dir, fx.Job.Snapshot.WorkspaceDir) || slices.Contains(s.Args, "--build") || slices.Contains(s.Args, "-b") {
			t.Fatalf("spec %+v", s)
		}
	}
	return r, doc
}

func TestReplayTscCaptures(t *testing.T) {
	for _, c := range []struct {
		name, key string
		status    domain.TaskStatus
		reason    string
		rules     []string
		processed int
	}{
		{"monorepo", "tsc.m01", domain.TaskSucceeded, "", []string{"TS2322"}, 3},
		{"clean", "tsc", domain.TaskSucceeded, "", nil, 1},
		{"gap", "tsc", domain.TaskFailed, ReasonDependency, nil, -1},
		{"syntax", "tsc", domain.TaskFailed, ReasonSyntax, nil, -1},
		{"solution", "tsc", domain.TaskSucceeded, "", []string{"TS2322"}, 2},
		{"extends", "tsc", domain.TaskFailed, ReasonExtendsUnresolved, nil, -1},
		{"no-inputs", "tsc", domain.TaskUnsupported, ReasonNoInputs, nil, 0},
		{"deprecated", "tsc", domain.TaskSucceeded, "", []string{"TS2322"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, doc := replayRun(t, c.name, c.key, nil)
			var rules []string
			for _, o := range doc.Observations {
				rules = append(rules, o.RuleID)
			}
			if doc.Outcome.TaskStatus != c.status || (c.reason != "" && *doc.Outcome.Reason != c.reason) || !slices.Equal(rules, c.rules) {
				t.Fatalf("%s: %+v rules %v errors %+v", c.name, doc.Outcome, rules, doc.ToolErrors)
			}
			if (c.processed < 0) != (doc.Coverage.ProcessedPaths == nil) || (c.processed >= 0 && len(doc.Coverage.ProcessedPaths) != c.processed) {
				t.Fatalf("%s: processed %v", c.name, doc.Coverage.ProcessedPaths)
			}
			if c.name == "gap" {
				kinds := map[string]int{}
				for _, te := range doc.ToolErrors {
					kinds[te.Kind]++
				}
				// TS2307 is the gap; TS7006 (follows from it) and TS2322 are withheld.
				if kinds["dependency"] != 1 || kinds["withheld"] != 2 {
					t.Fatalf("gap tool errors %+v", doc.ToolErrors)
				}
			}
		})
	}
}

func TestReplayTscDamaged(t *testing.T) {
	isCheck := func(s jststest.Recorded) bool { return s.Name == execCheck }
	for _, c := range []struct {
		name, reason string
		mutate       func(jststest.Recorded, *executor.Result)
	}{
		{"truncated", ReasonOutputTruncated, func(s jststest.Recorded, r *executor.Result) {
			if isCheck(s) {
				r.Stdout.Data, r.Stdout.Truncated = r.Stdout.Data[:len(r.Stdout.Data)/2], true
			}
		}},
		{"garbled", ReasonOutputUnparsed, func(s jststest.Recorded, r *executor.Result) {
			if isCheck(s) {
				r.Stdout.Data = append(r.Stdout.Data, []byte("garbage\n")...)
				r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
			}
		}},
		{"exit-zero", ReasonExitMismatch, func(s jststest.Recorded, r *executor.Result) {
			if isCheck(s) {
				zero := 0
				r.ExitCode = &zero
			}
		}},
		{"version-garbage", ReasonToolVersion, func(s jststest.Recorded, r *executor.Result) {
			if s.Name == execVersion {
				r.Stdout.Data = []byte("Version 6.1.0-dev.20260101\n")
				r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, doc := replayRun(t, "monorepo", "tsc.m01", c.mutate)
			if doc.Outcome.TaskStatus != domain.TaskFailed || *doc.Outcome.Reason != c.reason || doc.Coverage.ProcessedPaths != nil && len(doc.Coverage.ProcessedPaths) > 0 {
				t.Fatalf("%s: %+v %+v", c.name, doc.Outcome, doc.Coverage)
			}
			if c.name == "version-garbage" && len(r.Specs) != 1 {
				t.Fatalf("an unidentified tsc ran: %d", len(r.Specs))
			}
		})
	}
}

// docs/43 §3.2: a diagnostic about a file or a configuration file outside
// the workspace is never echoed, and a quoted host path in a global
// diagnostic is withheld; the run is refused as reading outside files.
func TestReplayTscHostDiagnosticsWithheld(t *testing.T) {
	inject := "/etc/host-secret.ts(1,1): error TS2322: MARKER-host-text\n" +
		"/etc/host-secret.json(1,1): error TS5024: MARKER-config-text\n" +
		"error TS5083: Cannot read file '/etc/host-secret/tsconfig.json'.\n"
	_, doc := replayRun(t, "monorepo", "tsc.m01", func(s jststest.Recorded, r *executor.Result) {
		if s.Name == execCheck {
			r.Stdout.Data = append([]byte(inject), r.Stdout.Data...)
			r.Stdout.TotalBytes = int64(len(r.Stdout.Data))
		}
	})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"MARKER-host-text", "MARKER-config-text", "/etc/host-secret"} {
		if strings.Contains(string(raw), marker) {
			t.Fatalf("host diagnostic echoed (%s): %s", marker, raw)
		}
	}
	if doc.Outcome.TaskStatus != domain.TaskSkippedPolicy || *doc.Outcome.Reason != ReasonFilesOutside || !slices.Contains(doc.Limitations, "program_files_outside_snapshot") {
		t.Fatalf("%+v %v", doc.Outcome, doc.Limitations)
	}
}
