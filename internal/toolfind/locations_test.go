package toolfind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The module cache is taken from the go found, kept only when it is an
// existing absolute directory, and an explicit one wins.
func TestDetectGoModCache(t *testing.T) {
	tr := newTree(t)
	goroot := filepath.Join(tr.home, "goroot")
	if err := os.MkdirAll(filepath.Join(goroot, "src", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	script(t, filepath.Join(goroot, "bin", "go"), versionOutputs["go"])
	tr.env.Path = []string{filepath.Join(goroot, "bin")}
	cache := filepath.Join(tr.home, "mod")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(answer string, ex Explicit) Result {
		return Detect(context.Background(), Options{Env: tr.env, Explicit: ex, ProbeDir: t.TempDir(),
			GoEnv: func(_ context.Context, exe string) string {
				if exe != filepath.Join(goroot, "bin", "go") {
					t.Errorf("asked %s", exe)
				}
				return answer
			}})
	}
	if res := run(cache, Explicit{}); res.GoModCache != cache {
		t.Fatalf("%q", res.GoModCache)
	}
	for _, bad := range []string{"", "relative/mod", filepath.Join(tr.home, "absent"), filepath.Join(goroot, "bin", "go")} {
		if res := run(bad, Explicit{}); res.GoModCache != "" {
			t.Fatalf("%q accepted as %q", bad, res.GoModCache)
		}
	}
	if res := run(cache, Explicit{GoModCache: "/mine/mod"}); res.GoModCache != "/mine/mod" {
		t.Fatalf("explicit lost: %q", res.GoModCache)
	}
}

// npm's global bin directory holds shims that point at the entry script; the
// entry script (not the shim) is the tool path.
func TestDetectFindsNpmShimOnPath(t *testing.T) {
	tr := newTree(t)
	nodeBin := filepath.Join(tr.home, "weird", "bin")
	fakeNode(t, filepath.Join(nodeBin, "node"))
	nm := filepath.Join(tr.home, "elsewhere", "node_modules")
	entry := npmTool(t, nm, "jscpd/run-jscpd.js", versionOutputs["jscpd"])
	if err := os.Symlink(entry, filepath.Join(nodeBin, "jscpd")); err != nil {
		t.Fatal(err)
	}
	tr.env.Path = []string{nodeBin}
	res := detect(t, tr, Explicit{})
	if j := mustTool(t, res, "jscpd"); j.Status != Found || j.Path != entry || j.Source != SourcePath {
		t.Fatalf("%+v", j)
	}
}

// Without node every tool that node runs says so; nothing else changes.
func TestDetectNodeToolsNeedNode(t *testing.T) {
	tr := newTree(t)
	nm := filepath.Join(tr.tools(), "node", "node_modules")
	npmTool(t, nm, "pyright/index.js", versionOutputs["pyright"])
	res := detect(t, tr, Explicit{})
	p := mustTool(t, res, "pyright")
	if p.Status != Unusable || !strings.Contains(p.Reason, "needs node") {
		t.Fatalf("%+v", p)
	}
}
