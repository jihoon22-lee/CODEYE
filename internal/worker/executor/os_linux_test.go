//go:build linux

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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
	case "emfile-run":
		emfileRun(args[0], args[1])
	default:
		return false
	}
	return true
}

// emfileRun is an isolated executor parent: once the tree is running it
// exhausts its own file descriptors (so /proc cannot be opened) and cancels.
// Its fd table dies with it; the outer test's process is unaffected.
func emfileRun(ws, tempParent string) {
	self, _ := os.Executable()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var held []*os.File
	go func() {
		waitForFile(filepath.Join(ws, "pid-0"), 60*time.Second)
		var lim syscall.Rlimit
		_ = syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim)
		lim.Cur = 256
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
		for {
			f, err := os.Open(os.DevNull)
			if err != nil {
				break // EMFILE
			}
			held = append(held, f)
		}
		cancel()
	}()
	e := Executor{KillWait: time.Second}
	res := e.Run(ctx, Spec{
		Executable: self, Args: []string{helperArg, "tree", "2", "wait"},
		WorkspaceRoot: ws, Dir: ws, TempParent: tempParent,
		Timeout: 60 * time.Second, Grace: 200 * time.Millisecond,
	})
	for _, f := range held {
		_ = f.Close()
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
		"status": string(res.Status), "cleanup": string(res.Cleanup), "note": res.CleanupNote,
	})
}

func TestProcScanFailureStillKillsAndStaysUncertain(t *testing.T) {
	ws := newWorkspace(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, helperExe(t), helperArg, "emfile-run", ws, t.TempDir())
	out, err := cmd.Output()
	pids := []int{}
	for _, name := range []string{"pid-2", "pid-1", "pid-0"} {
		pid := readPidFile(t, filepath.Join(ws, name))
		pids = append(pids, pid)
		defer syscall.Kill(pid, syscall.SIGKILL) // hygiene if the assertion fails
	}
	if err != nil {
		t.Fatalf("inner executor failed: %v %s", err, out)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	t.Logf("inner result %v", got)
	if got["status"] != string(StatusCancelled) || got["cleanup"] != string(CleanupUncertain) || !strings.Contains(got["note"], "proc_scan_failed") {
		t.Fatalf("scan failure must not confirm cleanup: %v", got)
	}
	for _, pid := range pids {
		assertDead(t, pid) // signals reached the pinned group without /proc
	}
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
