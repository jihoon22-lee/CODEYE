package deps

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An interpreter started on a prepared tree (mypy asks one for its search
// paths) runs the import lines of *.pth files and the sitecustomize module
// it finds there. The canary shows that it does, and that after the fetch
// removed the hooks it no longer does. Skipped without python3 or on
// Windows (the virtual environment layout differs).
func TestStartupHooksRemovedFromPreparedTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix virtual environment layout")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	out, err := exec.Command(py, "-I", "-S", "-c", pyVersionScript).Output()
	if err != nil {
		t.Skip("python3 does not run")
	}
	minor, ok := pythonMinor(strings.TrimSpace(string(out)))
	if !ok {
		t.Skip("python3 too old")
	}
	venv := t.TempDir()
	site := filepath.Join(venv, "lib", "python"+minor, "site-packages")
	marks := t.TempDir()
	pth := filepath.Join(marks, "pth-ran")
	custom := filepath.Join(marks, "sitecustomize-ran")
	writeTree(t, site, map[string]string{
		"canary.pth":       "import pathlib; pathlib.Path(" + quote(pth) + ").write_text('x')\n",
		"sitecustomize.py": "import pathlib\npathlib.Path(" + quote(custom) + ").write_text('x')\n",
		"pkg/__init__.py":  "",
	})
	writeTree(t, venv, map[string]string{"pyvenv.cfg": "home = " + filepath.Dir(py) + "\ninclude-system-site-packages = false\n"})
	if err := os.MkdirAll(filepath.Join(venv, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(venv, "bin", "python")
	if err := os.Symlink(py, shim); err != nil {
		t.Fatal(err)
	}
	start := func() {
		cmd := exec.Command(shim, "-c", "import sys")
		cmd.Env = []string{"PATH=" + filepath.Dir(py), "PYTHONNOUSERSITE=1", "PYTHONSAFEPATH=1", "PYTHONDONTWRITEBYTECODE=1"}
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v %s", err, b)
		}
	}
	start()
	if _, err := os.Stat(pth); err != nil {
		t.Fatal("the .pth canary did not run: the test does not show the risk")
	}
	// A system sitecustomize earlier on sys.path (Debian has one) shadows the
	// tree's; elsewhere the tree's runs.
	_ = os.Remove(pth)
	_ = os.Remove(custom)
	n, err := removeStartupHooks(site)
	if err != nil || n != 2 {
		t.Fatalf("removed %d, %v", n, err)
	}
	start()
	for _, m := range []string{pth, custom} {
		if _, err := os.Stat(m); err == nil {
			t.Fatalf("%s ran after the hooks were removed", filepath.Base(m))
		}
	}
	if _, err := os.Stat(filepath.Join(site, "pkg", "__init__.py")); err != nil {
		t.Fatalf("package removed: %v", err)
	}
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `\'`) + "'" }
