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
