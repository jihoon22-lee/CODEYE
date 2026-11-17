//go:build linux || windows

package executor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The test binary re-executes itself as the subprocess under test:
// argv = [test-binary, "codeye-helper", mode, args...]. Mode is passed by
// argv, not env, because the executor deliberately isolates env.
const helperArg = "codeye-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperArg {
		os.Exit(runHelper(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

func runHelper(mode string, args []string) int {
	switch mode {
	case "args":
		_ = json.NewEncoder(os.Stdout).Encode(args)
	case "env":
		_ = json.NewEncoder(os.Stdout).Encode(os.Environ())
	case "cwd":
		wd, _ := os.Getwd()
		_ = os.WriteFile("started.txt", []byte("started"), 0o600)
		fmt.Print(wd)
	case "out":
		n, _ := strconv.Atoi(args[0])
		e, _ := strconv.Atoi(args[1])
		writeN(os.Stdout, 'o', n)
		writeN(os.Stderr, 'e', e)
	case "exit":
		code, _ := strconv.Atoi(args[0])
		fmt.Fprint(os.Stderr, "failing on purpose")
		return code
	case "argv0":
		fmt.Print(os.Args[0])
	case "stdin":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "sleep":
		writePid("sleep")
		time.Sleep(120 * time.Second)
	case "tree":
		// tree <depth> <wait|exit>: each level spawns the next and writes
		// pid-<depth>; the leaf (depth 0) sleeps. All share stdout/stderr.
		depth, _ := strconv.Atoi(args[0])
		if depth > 0 {
			self, _ := os.Executable()
			c := exec.Command(self, helperArg, "tree", strconv.Itoa(depth-1), "wait")
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			if err := c.Start(); err != nil {
				fmt.Fprintln(os.Stderr, "spawn failed:", err)
				return 3
			}
		}
		writePid(fmt.Sprintf("pid-%d", depth))
		if args[1] == "exit" {
			waitForFile("pid-0", 60*time.Second)
			fmt.Print("main exiting")
			return 0
		}
		time.Sleep(120 * time.Second)
	case "write":
		// Modify every file in cwd and add a new one.
		entries, _ := os.ReadDir(".")
		for _, e := range entries {
			if !e.IsDir() {
				_ = os.WriteFile(e.Name(), []byte("modified by tool"), 0o600)
			}
		}
		_ = os.WriteFile("generated.txt", []byte("new"), 0o600)
	default:
		if !platformHelper(mode, args) {
			fmt.Fprintln(os.Stderr, "unknown helper mode", mode)
			return 2
		}
	}
	return 0
}

func writeN(w io.Writer, b byte, n int) {
	buf := make([]byte, 4096)
	for i := range buf {
		buf[i] = b
	}
	for n > 0 {
		k := min(n, len(buf))
		_, _ = w.Write(buf[:k])
		n -= k
	}
}

func writePid(name string) {
	tmp := name + ".tmp"
	_ = os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0o600)
	_ = os.Rename(tmp, name)
}

func waitForFile(path string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// ---- test-side helpers ----

func helperExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// newWorkspace returns an approved workspace whose path contains a space
// and non-ASCII characters.
func newWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "work space 한글")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	return ws
}

func helperSpec(t *testing.T, ws string, mode string, args ...string) Spec {
	t.Helper()
	return Spec{
		TaskID:        "task-1",
		AttemptEpoch:  1,
		Executable:    helperExe(t),
		Args:          append([]string{helperArg, mode}, args...),
		WorkspaceRoot: ws,
		Dir:           ws,
		TempParent:    t.TempDir(),
		Timeout:       60 * time.Second,
	}
}

func readPidFile(t *testing.T, path string) int {
	t.Helper()
	if !waitForFile(path, 60*time.Second) {
		t.Fatalf("pid file %s not written", filepath.Base(path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// assertDead checks with the OS (not the executor's own claim) that pid is
// gone.
func assertDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still alive after cleanup", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
