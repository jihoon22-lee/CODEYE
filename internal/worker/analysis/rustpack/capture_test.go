package rustpack

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
)

// capture writes the main execution of a real run (stdout, stderr, exit
// code) and the dep-info files of its target directory to
// $CODEYE_RUST_CAPTURE/<name>.*, with every node path replaced by a
// placeholder, so CI replays the exact bytes of the pinned toolchain
// (docs/ANALYZERS.md#실제-도구-시험과-출력-캡처). Version outputs are captured once as versions.*.
func capture(t *testing.T, name string, job analysis.Job, key string, res analysis.TaskResult, a *Analyzer) {
	t.Helper()
	dir := os.Getenv("CODEYE_RUST_CAPTURE")
	if dir == "" {
		return
	}
	taskDir := filepath.Join(job.Scratch, "analyzers", key)
	home, _ := os.UserHomeDir()
	clean := func(b []byte) []byte {
		for _, r := range []struct{ from, to string }{{taskDir, "$SCRATCH"}, {a.tcDir, "$TOOLCHAIN"}, {job.Scratch, "$SCRATCH"}, {job.Snapshot.WorkspaceDir, "$WORKSPACE"}, {home, "$HOME"}} {
			if r.from != "" {
				b = bytes.ReplaceAll(b, []byte(r.from), []byte(r.to))
			}
		}
		if bytes.Contains(b, []byte("/home/")) || bytes.Contains(b, []byte(`\Users\`)) {
			t.Fatalf("capture %s holds a node path:\n%s", name, b)
		}
		return b
	}
	write := func(file string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, file), clean(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range res.Executions {
		code := "none"
		if e.Result.ExitCode != nil {
			code = strconv.Itoa(*e.Result.ExitCode)
		}
		switch e.Name {
		case a.kind.main():
			write(name+".stdout", e.Result.Stdout.Data)
			write(name+".stderr", e.Result.Stderr.Data)
			write(name+".exit", []byte(code+"\n"))
		case a.kind.retryName():
			write(name+".retry.stdout", e.Result.Stdout.Data)
			write(name+".retry.stderr", e.Result.Stderr.Data)
			write(name+".retry.exit", []byte(code+"\n"))
		case execRustc:
			write("versions.rustc", e.Result.Stdout.Data)
		case execCargo:
			write("versions.cargo", e.Result.Stdout.Data)
		case execClippyV:
			write("versions.clippy", e.Result.Stdout.Data)
		}
	}
	for target, deps := range map[string]string{"target": name + ".deps", "target-retry": name + ".retry.deps"} {
		depDir := filepath.Join(dir, deps)
		_ = os.RemoveAll(depDir)
		n := 0
		_ = filepath.WalkDir(filepath.Join(taskDir, target), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".d") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(depDir, 0o755); err != nil {
				t.Fatal(err)
			}
			n++
			if err := os.WriteFile(filepath.Join(depDir, strconv.Itoa(n)+".d"), clean(b), 0o644); err != nil {
				t.Fatal(err)
			}
			return nil
		})
	}
}
