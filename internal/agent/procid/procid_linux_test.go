//go:build linux

package procid

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func startGroup(t *testing.T) *exec.Cmd {
	t.Helper()
	// Leader plus a background grandchild in the same new process group.
	cmd := exec.Command("/bin/sh", "-c", "sleep 300 & sleep 300; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		m, _ := GroupMembers(cmd.Process.Pid)
		if len(m) >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cmd
}

func TestVerifiedGroupTermination(t *testing.T) {
	cmd := startGroup(t)
	pid := cmd.Process.Pid
	id, err := Read(pid)
	if err != nil || id.PGID != pid {
		t.Fatalf("read %+v %v", id, err)
	}
	if m, _ := GroupMembers(pid); len(m) < 2 {
		t.Fatalf("expected grandchild in group, got %v", m)
	}
	go func() { _ = cmd.Wait() }() // reap the leader so it does not linger as zombie
	empty, err := TerminateVerifiedGroup(pid, id.StartTime, time.Second, 5*time.Second)
	if err != nil || !empty {
		t.Fatalf("empty=%v err=%v", empty, err)
	}
}

func TestReusedPIDIsNeverSignalled(t *testing.T) {
	cmd := startGroup(t)
	pid := cmd.Process.Pid
	id, _ := Read(pid)
	// A recorded start time that differs from the live process models PID reuse.
	st, err := Check(pid, id.StartTime+1)
	if err != nil || st != Reused {
		t.Fatalf("status %v err %v", st, err)
	}
	empty, err := TerminateVerifiedGroup(pid, id.StartTime+1, 100*time.Millisecond, 100*time.Millisecond)
	if err != nil || empty {
		t.Fatalf("reused pid must not be reported empty: %v %v", empty, err)
	}
	time.Sleep(200 * time.Millisecond)
	if st, _ := Check(pid, id.StartTime); st != Same {
		t.Fatal("innocent process was signalled")
	}
}

func TestGoneLeaderWithSurvivorIsNotEmpty(t *testing.T) {
	cmd := startGroup(t)
	pid := cmd.Process.Pid
	id, _ := Read(pid)
	// Kill only the leader; the grandchild keeps the pgid alive.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = cmd.Wait()
	empty, err := TerminateVerifiedGroup(pid, id.StartTime, 100*time.Millisecond, 100*time.Millisecond)
	if err != nil || empty {
		t.Fatalf("leaderless group with members must be uncertain: %v %v", empty, err)
	}
	if m, _ := GroupMembers(pid); len(m) == 0 {
		t.Fatal("survivor must not have been blindly signalled")
	}
}

// codeye stop signals one recorded process only while its identity
// still matches; a reused PID is never signalled and a gone one is reported.
func TestSignalVerifiedSingleProcess(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	pid := cmd.Process.Pid
	id, err := Read(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignalVerified(pid, id.StartTime+1, syscall.SIGTERM); err != ErrReused {
		t.Fatalf("reused identity: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if st, _ := Check(pid, id.StartTime); st != Same {
		t.Fatal("a process with another identity was signalled")
	}
	if err := SignalVerified(pid, id.StartTime, syscall.SIGTERM); err != nil {
		if err == ErrPidfdUnsupported {
			t.Skip("kernel without pidfd: stop reports unsupported")
		}
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if err := SignalVerified(pid, id.StartTime, syscall.SIGTERM); err != ErrGone {
		t.Fatalf("gone process: %v", err)
	}
}
