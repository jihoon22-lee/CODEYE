//go:build linux

package executor

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func platformHelper(mode string, args []string) bool {
	switch mode {
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		writePid("ignore")
		time.Sleep(120 * time.Second)
	case "escape":
		// The child leaves the session/process group with setsid and keeps
		// the stdout pipe, like a daemonizing build server.
		self, _ := os.Executable()
		c := exec.Command(self, helperArg, "sleep")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			return false
		}
		waitForFile("sleep", 60*time.Second)
		fmt.Print("before-escape")
		writePid("escape")
		time.Sleep(120 * time.Second)
	default:
		return false
	}
	return true
}

func processAlive(pid int) bool {
	st, err := readStat(pid)
	return err == nil && st.live()
}

func TestGracefulTerminateBeforeKill(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "sleep")
	spec.Timeout = 2 * time.Second
	spec.Grace = 5 * time.Second
	res := run(t, spec)
	requireClean(t, res, StatusTimedOut)
	if res.Signal != "SIGTERM" || !res.Resources.GracefulStopUsed {
		t.Fatalf("signal=%s graceful=%v", res.Signal, res.Resources.GracefulStopUsed)
	}
	assertDead(t, readPidFile(t, filepath.Join(ws, "sleep")))
}

func TestKillAfterGraceWhenTermIgnored(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "ignore-term")
	spec.Timeout = 4 * time.Second // SIGTERM must be ignored before the timeout
	spec.Grace = 500 * time.Millisecond
	begin := time.Now()
	res := run(t, spec)
	requireClean(t, res, StatusTimedOut)
	if res.Signal != "SIGKILL" || time.Since(begin) < spec.Timeout+spec.Grace {
		t.Fatalf("signal=%s elapsed=%s", res.Signal, time.Since(begin))
	}
	assertDead(t, readPidFile(t, filepath.Join(ws, "ignore")))
}

func TestEscapedDescendantIsUncertain(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "escape")
	spec.Timeout = 3 * time.Second
	spec.Grace = 200 * time.Millisecond
	res := run(t, spec)
	escaped := readPidFile(t, filepath.Join(ws, "sleep"))
	defer syscall.Kill(escaped, syscall.SIGKILL)
	if res.Status != StatusTimedOut {
		t.Fatalf("status = %s", res.Status)
	}
	if res.Cleanup != CleanupUncertain || !res.RecoveryRequired() {
		t.Fatalf("escaped child must leave cleanup uncertain, got %s (%s)", res.Cleanup, res.CleanupNote)
	}
	assertDead(t, readPidFile(t, filepath.Join(ws, "escape")))
	if !processAlive(escaped) {
		t.Fatal("test setup: escaped process should still be alive")
	}
	// The held pipe keeps output incomplete, but what was captured is kept.
	if !res.Stdout.Incomplete || string(res.Stdout.Data) != "before-escape" || !res.Command.TempRetained {
		t.Fatalf("stdout=%q incomplete=%v tempRetained=%v", res.Stdout.Data, res.Stdout.Incomplete, res.Command.TempRetained)
	}
	if _, err := os.Stat(res.Command.TempDir); err != nil {
		t.Fatal("temp dir must be kept while a process may still use it")
	}
}

func TestSymlinkCwdEscapeRejected(t *testing.T) {
	ws := newWorkspace(t)
	outside := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	spec := helperSpec(t, ws, "cwd")
	spec.Dir = link
	if res := run(t, spec); res.Reason != "cwd_outside_workspace" {
		t.Fatalf("reason = %s", res.Reason)
	}
	// A protected root reached through a symlink inside the workspace.
	spec = helperSpec(t, ws, "cwd")
	spec.ProtectedRoots = []string{outside}
	spec.Dir = link
	if res := run(t, spec); res.Status != StatusRejected {
		t.Fatalf("status = %s", res.Status)
	}
}

func TestNonExecutableRejected(t *testing.T) {
	ws := newWorkspace(t)
	f := filepath.Join(ws, "tool")
	if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := helperSpec(t, ws, "cwd")
	spec.Executable = f
	if res := run(t, spec); res.Reason != "executable_not_executable" {
		t.Fatalf("reason = %s", res.Reason)
	}
}

func TestParseStatCommWithParens(t *testing.T) {
	line := "4242 (a) (b c)) S 1 4242 4242 0 -1 4194560 1 0 0 0 0 0 0 0 20 0 1 0 987654 1 1"
	st, err := parseStat(4242, line)
	if err != nil {
		t.Fatal(err)
	}
	if st.ppid != 1 || st.pgrp != 4242 || st.sid != 4242 || st.state != 'S' || st.start != 987654 {
		t.Fatalf("parsed %+v", st)
	}
}
