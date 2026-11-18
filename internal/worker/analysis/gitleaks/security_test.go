package gitleaks

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Scope roots named like options are scanned as paths ("-- ./<root>"); a
// link out of the tree to a file holding a canary credential is never
// followed (the snapshot does not copy links and --follow-symlinks is never
// passed): only the planted secret inside the tree is reported, and neither
// value reaches a stream, scratch or the envelope.
func TestRealGitleaksHostileNamesAndLinks(t *testing.T) {
	a, err := New(Settings{Executable: testGitleaks(t)})
	if err != nil {
		t.Fatal(err)
	}
	secret, canary := anypacktest.Secret(t), anypacktest.Secret(t)
	target := anypacktest.Canary(t, canary)
	files := map[string][]byte{"main.go": []byte("package main\n"), "--help/app.py": []byte("AWS_KEY = \"" + secret + "\"\n"), "-x/clean.py": []byte("x = 1\n")}
	links := map[string]string{"--help/leak.py": target, "-x/linked": filepath.Dir(target)}
	for _, scope := range []domain.Scope{{Kind: "full"}, {Kind: "paths", Paths: []string{"--help", "-x"}}} {
		fx := anypacktest.NewLinked(t, files, links, scope)
		res := anypacktest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
		doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
		for _, v := range []string{secret, canary} {
			anypacktest.AssertAbsent(t, v, "envelope", raw)
			anypacktest.AssertTreeAbsent(t, v, fx.Job.Scratch)
			anypacktest.AssertResultAbsent(t, v, res)
		}
		anypacktest.AssertNoSnippet(t, res, "AWS_KEY", "TOKEN =")
		if res.Status != domain.TaskSucceeded || !slices.Equal(placesOf(t, doc), []string{"--help/app.py:1:aws-access-token"}) {
			t.Fatalf("%v: %s/%s %+v %+v", scope, res.Status, res.Reason, doc.Observations, doc.ToolErrors)
		}
		for _, e := range res.Executions {
			argv := e.Result.Command.Argv
			if i := slices.Index(argv, "--"); e.Name != execVersion && (i < 0 || i != len(argv)-2 || !(argv[i+1] == "." || strings.HasPrefix(argv[i+1], "./"))) {
				t.Fatalf("%s: root not after -- as ./path: %v", e.Name, argv)
			}
		}
	}
}

// A .gitleaksignore entry that names a path outside the copy refuses the
// task before gitleaks starts; the entry is not recorded.
func TestReplayRefusesHostileIgnoreFile(t *testing.T) {
	for _, entry := range []string{"../../etc/x.py:aws-access-token:1", "/etc/x.py:aws-access-token:1", "c0ffee:..\\\\x.py:aws-access-token:1"} {
		files := fixtureFiles(anypacktest.Placeholder)
		files[".gitleaksignore"] = []byte("src/ignored.py:aws-access-token:1\n" + entry + "\n")
		p := replay(t, replayCase{files: files, scans: findings(t)})
		if p.res.Status != domain.TaskFailed || p.res.Reason != ReasonConfigRejected || len(p.r.Specs) != 0 || !slices.Contains(p.doc.Limitations, LimIgnoreFileRejected) {
			t.Fatalf("%q: %s/%s specs %d %+v", entry, p.res.Status, p.res.Reason, len(p.r.Specs), p.doc.ToolErrors)
		}
		b, _ := marshalDoc(t, p)
		if strings.Contains(string(b), "etc/x.py") || strings.Contains(string(b), "x.py:aws") {
			t.Fatal("rejected entry recorded")
		}
	}
}

// A record for a file that is not a selected snapshot file (outside the
// copy) is dropped without its path and fails the task.
func TestReplayDropsResultsOutsideSnapshot(t *testing.T) {
	good := anypacktest.LoadCapture(t, "findings")
	good.Stdout = []byte(strings.Replace(string(good.Stdout), `"file":"src/app.py"`, `"file":"../outside/stolen.py"`, 1))
	p := replay(t, replayCase{scans: map[string]anypacktest.Recorded{".": good}})
	if p.res.Status != domain.TaskFailed || p.res.Reason != ReasonOutsideSnapshot || len(p.doc.Coverage.ProcessedPaths) != 0 {
		t.Fatalf("%s/%s %+v", p.res.Status, p.res.Reason, p.doc.Outcome)
	}
	b, _ := marshalDoc(t, p)
	if strings.Contains(string(b), "stolen") || strings.Contains(string(b), "outside/") {
		t.Fatal("outside path recorded")
	}
}

func TestReplayRefusesWorkspaceLink(t *testing.T) {
	target := anypacktest.Canary(t, anypacktest.Placeholder)
	p := replay(t, replayCase{scans: findings(t), before: func(fx anypacktest.Fixture) {
		anypacktest.LinkInWorkspace(t, fx, "src/extra.py", target)
	}})
	if p.res.Status != domain.TaskFailed || p.res.Reason != anypack.ReasonWorkspaceLink || len(p.r.Specs) != 0 || p.doc.Outcome.ExitClass != obsdoc.ExitNotRun {
		t.Fatalf("%s/%s specs %d %+v", p.res.Status, p.res.Reason, len(p.r.Specs), p.doc.Outcome)
	}
}

func marshalDoc(t *testing.T, p replayed) ([]byte, error) {
	t.Helper()
	_, raw := anypacktest.ReadEnvelope(t, p.fx.Job, p.res)
	return raw, nil
}

func TestIgnoreFileEntries(t *testing.T) {
	if !strings.HasPrefix(RootArg("-x"), "./") || RootArg(".") != "." || ScanRoot(ScanArgs("--help", "c", "i", "t")) != "--help" {
		t.Fatal("root argv form")
	}
}
