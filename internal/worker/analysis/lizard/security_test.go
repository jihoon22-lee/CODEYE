package lizard

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The repository is hostile (docs/ANALYZERS.md security): file names that look like
// options and links out of the tree.
func hostileFiles() map[string][]byte {
	return map[string][]byte{
		"--help/a.py":      []byte(pyComplex(anypacktest.Placeholder)),
		"-x.py":            []byte("def f():\n    return 1\n"),
		"src/--version.go": []byte(goComplex()),
	}
}

// Option-like names are paths (they reach lizard only inside the CODEYE
// file list, never as argv), and a link to a file outside the tree that
// holds a canary is never read: the snapshot does not copy it and nothing
// of the canary reaches a stream, scratch or the envelope. The streams
// hold no source text (lizard XML names functions only).
func TestRealLizardHostileNamesAndLinks(t *testing.T) {
	a, err := New(Config{Executable: testLizard(t)})
	if err != nil {
		t.Fatal(err)
	}
	canary := anypacktest.Secret(t)
	target := anypacktest.Canary(t, canary)
	fx := anypacktest.NewLinked(t, hostileFiles(), map[string]string{"src/leak.py": target, "linked": filepath.Dir(target)}, domain.Scope{})
	res := anypacktest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"--help/a.py", "-x.py", "src/--version.go"}) {
		t.Fatalf("%s/%s %+v %+v", res.Status, res.Reason, doc.Coverage, doc.ToolErrors)
	}
	if got := metricsOf(t, doc); !slices.Equal(got, []string{"--help/a.py:4=17", "src/--version.go:3=17"}) {
		t.Fatalf("metrics %v", got)
	}
	for _, e := range res.Executions {
		for _, arg := range e.Result.Command.Argv[1:] {
			if arg == "--help" || arg == "-x.py" || arg == "--version.go" {
				t.Fatalf("%s: repository name as argv %q", e.Name, arg)
			}
		}
	}
	anypacktest.AssertAbsent(t, canary, "envelope", raw)
	anypacktest.AssertTreeAbsent(t, canary, fx.Job.Scratch)
	anypacktest.AssertResultAbsent(t, canary, res)
	anypacktest.AssertNoSnippet(t, res, "return len(token)", "if value == 1:", "return a + b")
}

// A link planted in the attempt workspace refuses the task before lizard
// starts.
func TestReplayRefusesWorkspaceLink(t *testing.T) {
	target := anypacktest.Canary(t, anypacktest.Placeholder)
	res, doc, r, _ := replay(t, replayCase{run: anypacktest.LoadCapture(t, "metrics"), before: func(fx anypacktest.Fixture) {
		anypacktest.LinkInWorkspace(t, fx, "web/add.js", target)
	}})
	if res.Status != domain.TaskFailed || res.Reason != anypack.ReasonWorkspaceLink || len(r.Specs) != 0 || doc.Outcome.ExitClass != obsdoc.ExitNotRun {
		t.Fatalf("%s/%s specs %d %+v", res.Status, res.Reason, len(r.Specs), doc.Outcome)
	}
}
