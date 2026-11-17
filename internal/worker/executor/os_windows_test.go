//go:build windows

package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func platformHelper(mode string, args []string) bool {
	switch mode {
	case "breakaway":
		// A child asking to leave the job must be refused (no BREAKAWAY_OK).
		self, _ := os.Executable()
		c := exec.Command(self, helperArg, "sleep")
		c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
		if err := c.Start(); err != nil {
			fmt.Print("breakaway_denied")
			return true
		}
		_ = c.Process.Kill()
		fmt.Print("breakaway_allowed")
	default:
		return false
	}
	return true
}

// processAlive reports whether pid is a running instance of this test
// binary; Windows reuses PIDs quickly, so the image path is compared too.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	if err != nil || ev != uint32(windows.WAIT_TIMEOUT) {
		return false
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return true // running but unidentifiable: treat as alive (conservative)
	}
	self, err := os.Executable()
	if err != nil {
		return true
	}
	a, errA := filepath.EvalSymlinks(windows.UTF16ToString(buf[:n]))
	b, errB := filepath.EvalSymlinks(self)
	if errA != nil || errB != nil {
		return true
	}
	return strings.EqualFold(a, b)
}

func TestJobBreakawayDenied(t *testing.T) {
	res := run(t, helperSpec(t, newWorkspace(t), "breakaway"))
	requireClean(t, res, StatusExited)
	if string(res.Stdout.Data) != "breakaway_denied" {
		t.Fatalf("stdout = %q", res.Stdout.Data)
	}
	if res.Resources.CPUScope != "job" || res.Resources.PeakMemoryScope != "job" {
		t.Fatalf("job accounting = %+v", res.Resources)
	}
	t.Logf("job peak memory bytes = %d", res.Resources.PeakMemoryBytes)
}

func TestBatchScriptRejected(t *testing.T) {
	ws := newWorkspace(t)
	f := filepath.Join(ws, "tool.CMD")
	if err := os.WriteFile(f, []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := helperSpec(t, ws, "cwd")
	spec.Executable = f
	if res := run(t, spec); res.Reason != "batch_script_unsupported" {
		t.Fatalf("reason = %s", res.Reason)
	}
}

func TestWindowsTimeoutHasNoGracefulPhase(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "sleep")
	spec.Timeout = 10 * time.Second
	res := run(t, spec)
	requireClean(t, res, StatusTimedOut)
	if res.Resources.GracefulStopUsed || res.ExitCode == nil || *res.ExitCode != 1 {
		t.Fatalf("graceful=%v exit=%v", res.Resources.GracefulStopUsed, derefInt(res.ExitCode))
	}
	assertDead(t, readPidFile(t, filepath.Join(ws, "sleep")))
}
