package jscpd

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

// Option-like names are paths (absolute, inside the CODEYE configuration,
// never argv); a link out of the tree to a canary is never read; the xcode
// streams hold no source text.
func TestRealJscpdHostileNamesAndLinks(t *testing.T) {
	script, node := testTools(t)
	a, err := New(Config{Executable: script, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	canary := anypacktest.Secret(t)
	target := anypacktest.Canary(t, canary)
	files := map[string][]byte{"--help/a.js": []byte(block("alpha", canary)), "-x/b.js": []byte(block("alpha", canary))}
	fx := anypacktest.NewLinked(t, files, map[string]string{"src/leak.js": target, "linked": filepath.Dir(target)}, domain.Scope{})
	res := anypacktest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"--help/a.js", "-x/b.js"}) {
		t.Fatalf("%s/%s %+v %+v", res.Status, res.Reason, doc.Coverage, doc.ToolErrors)
	}
	if got := clonesOf(t, doc); len(got) != 1 {
		t.Fatalf("clones %v", got)
	}
	anypacktest.AssertAbsent(t, canary, "envelope", raw)
	anypacktest.AssertTreeAbsent(t, canary, fx.Job.Scratch)
	anypacktest.AssertResultAbsent(t, canary, res)
	anypacktest.AssertNoSnippet(t, res, "total += item.value * 2", "console.log(label, token)")
}

func TestReplayRefusesWorkspaceLink(t *testing.T) {
	target := anypacktest.Canary(t, anypacktest.Placeholder)
	p := replay(t, replayCase{run: anypacktest.LoadCapture(t, "clones"), before: func(fx anypacktest.Fixture) {
		anypacktest.LinkInWorkspace(t, fx, "src/a.js", target)
	}})
	if p.res.Status != domain.TaskFailed || p.res.Reason != anypack.ReasonWorkspaceLink || len(p.r.Specs) != 0 || p.doc.Outcome.ExitClass != obsdoc.ExitNotRun {
		t.Fatalf("%s/%s specs %d %+v", p.res.Status, p.res.Reason, len(p.r.Specs), p.doc.Outcome)
	}
}
