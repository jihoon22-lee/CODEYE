package toolfind

import (
	"path/filepath"
	"strings"
	"testing"
)

// The Python interpreter of the dependency fetch needs pip unless uv is
// found; one with pip is found without uv. Its version decides whether it
// is of the tested series (tomllib from 3.11).
func TestDetectPythonPackageManagers(t *testing.T) {
	tr := newTree(t)
	usrbin := tr.pathDir(t, "usr/bin")
	py := write(t, filepath.Join(usrbin, "python3"), "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'Python 3.10.12'; exit 0; fi\nexit 1\n", 0o755)
	res := detect(t, tr, Explicit{})
	if got := mustTool(t, res, "python"); got.Status != Unusable || !strings.Contains(got.Reason, "pip") || got.Hint == "" {
		t.Fatalf("python without pip and uv: %+v", got)
	}
	if got := mustTool(t, res, "uv"); got.Status != NotFound || !strings.Contains(got.Hint, "agent.tools.uv") {
		t.Fatalf("uv: %+v", got)
	}
	// With pip: found, version 3.10 is outside the tested series.
	write(t, py, "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'Python 3.10.12'; exit 0; fi\necho 'pip 22.0.2 from /usr/lib/python3/dist-packages/pip (python 3.10)'\n", 0o755)
	res = detect(t, tr, Explicit{})
	if got := mustTool(t, res, "python"); got.Status != Found || got.Version != "3.10.12" || got.VersionTested || got.TestedSeries != pythonTestedSeries {
		t.Fatalf("python with pip: %+v", got)
	}
	// An explicit path wins and is not replaced.
	res = detect(t, tr, Explicit{Tools: map[string]string{"python": filepath.Join(tr.home, "missing", "python3")}})
	if got := mustTool(t, res, "python"); got.Status != Unusable || got.Source != SourceConfig {
		t.Fatalf("explicit python: %+v", got)
	}
	r := Resolve(detect(t, tr, Explicit{}), Explicit{})
	if r.Tools["python"] != py {
		t.Fatalf("resolved %v", r.Tools)
	}
}

// A JS package manager is validated as the deps.javascript task runs it:
// node first, then the entry script inside its own npm package (no shim),
// then `<node> <script> --version`.
func TestValidateJSPackageManagers(t *testing.T) {
	tr := newTree(t)
	nm := filepath.Join(tr.home, "nm")
	good := npmTool(t, nm, "pnpm/bin/pnpm.cjs", "11.28.13")
	other := npmTool(t, nm, "other/bin/pnpm.cjs", "11.28.13")
	shim := write(t, filepath.Join(nm, "shim", "pnpm.cmd"), "@echo off\n", 0o755)
	odd := npmTool(t, filepath.Join(tr.home, "nm2"), "pnpm/bin/pnpm.cjs", "Update available")
	ex := func(p string) Explicit { return Explicit{Tools: map[string]string{"pnpm": p}} }

	if got := mustTool(t, detect(t, tr, ex(good)), "pnpm"); got.Status != Unusable || !strings.Contains(got.Reason, "node") {
		t.Fatalf("pnpm without node: %+v", got)
	}
	fakeNode(t, filepath.Join(tr.pathDir(t, "nodebin"), "node"))
	if got := mustTool(t, detect(t, tr, ex(good)), "pnpm"); got.Status != Found || got.Version != "11.28.13" || !got.VersionTested {
		t.Fatalf("pnpm: %+v", got)
	}
	for name, p := range map[string]string{"other package": other, "command shim": shim, "unknown version": odd} {
		if got := mustTool(t, detect(t, tr, ex(p)), "pnpm"); got.Status != Unusable || got.Reason == "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	for in, want := range map[string]string{"11.16.0": "11.16.0", "v1.22.22": "1.22.22", "4.0.0-rc.1": "4.0.0", "pnpm 11": ""} {
		m := jsManagerVersion.FindStringSubmatch(in)
		if (m == nil) != (want == "") || (m != nil && m[1] != want) {
			t.Errorf("version %q = %v", in, m)
		}
	}
}
