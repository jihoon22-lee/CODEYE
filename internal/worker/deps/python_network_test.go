package deps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The real install from PyPI with uv and with pip, wheels only, into the
// shared tree; a package without a wheel (mkautodoc 0.2.0 has only an
// sdist) is left out. It needs the network, so it runs only with
// CODEYE_TEST_DEPS_NETWORK=1 and a python3 (uv from PATH when present).
func TestRealPythonFetch(t *testing.T) {
	if os.Getenv("CODEYE_TEST_DEPS_NETWORK") != "1" {
		t.Skip("set CODEYE_TEST_DEPS_NETWORK=1 to install from PyPI")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	managers := map[string]string{"pip": ""}
	if uv, err := exec.LookPath("uv"); err == nil {
		managers["uv"] = uv
	}
	for name, uv := range managers {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"requirements.txt": "idna==3.10\nmkautodoc==0.2.0\n", "a.py": "import idna\n"}
			job, src := jobOf(t, files)
			cache := filepath.Join(t.TempDir(), "cache", "deps")
			h, err := Wrap(innerHandler{}, Config{CacheRoot: cache, Python: py, UV: uv, PythonConsumers: true}, PythonFetcher{})
			if err != nil {
				t.Fatal(err)
			}
			res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &executor.Executor{})
			rec := readRecord(t, job, "deps.python")
			if res.Status != domain.TaskFailed || rec.State != depdoc.StatePartial || rec.Counts.Prepared != 1 || rec.Counts.Failed != 1 || rec.FailedItems[0].Name != "mkautodoc" {
				t.Fatalf("result %+v record %+v", res, rec)
			}
			if rec.Manager.Name != name || rec.Cache.Kind != depdoc.CacheShared {
				t.Fatalf("record %+v", rec)
			}
			site := mustGlob(t, filepath.Join(cache, filepath.FromSlash(*rec.Cache.Key), "lib", "python3.*", "site-packages"))
			if _, err := os.Stat(filepath.Join(site, "idna", "py.typed")); err != nil {
				t.Fatalf("idna not installed: %v", err)
			}
			for rel, body := range files {
				if b, err := os.ReadFile(filepath.Join(src, rel)); err != nil || string(b) != body {
					t.Fatalf("source %s changed", rel)
				}
			}
			if err := os.RemoveAll(cache); err != nil {
				t.Fatalf("cache not removable: %v", err)
			}
		})
	}
}

func mustGlob(t *testing.T, pattern string) string {
	t.Helper()
	m, _ := filepath.Glob(pattern)
	if len(m) != 1 {
		t.Fatalf("%s: %v", pattern, m)
	}
	return m[0]
}
