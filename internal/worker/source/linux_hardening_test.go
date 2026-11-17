//go:build linux

package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A regular file replaced by a FIFO between the scan and the open must not hang the
// acquisition (open(2) of a FIFO blocks until a writer appears without O_NONBLOCK).
func TestFIFOSwappedInAfterScanDoesNotHang(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "regular")
	put(t, root, "b.txt", "other")
	fifo := filepath.Join(root, "a.txt")
	hooks := localHooks{beforeOpen: func(attempt int, p string) {
		if attempt == 0 && p == "a.txt" {
			if err := os.Remove(fifo); err != nil {
				t.Error(err)
			}
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Error(err)
			}
		}
	}}
	type result struct {
		s   Snapshot
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := acquireLocalHooks(context.Background(), root, ".", data, DefaultLimits(), hooks)
		done <- result{s, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("expected the retry to classify the FIFO as a special file: %v", r.err)
		}
		cleanupSnapshot(t, r.s)
		if e := find(r.s, "a.txt"); e.Kind != "special" || e.ExcludedReason != "special_file_not_copied" {
			t.Fatalf("FIFO not recorded as an excluded special file: %+v", e)
		}
		if read(t, r.s.SnapshotDir, "b.txt") != "other" {
			t.Fatal("regular file lost")
		}
		if _, err := os.Stat(filepath.Join(r.s.SnapshotDir, "a.txt")); !os.IsNotExist(err) {
			t.Fatal("FIFO content copied")
		}
	case <-time.After(5 * time.Second):
		// Release the blocked open so the goroutine can finish, then fail.
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("acquisition blocked on a FIFO swapped in after the scan")
	}
}

// With a FIFO that stays in place on every attempt the result is unstable, not a hang.
func TestPersistentFIFOSwapEndsAsUnstable(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "regular")
	hooks := localHooks{beforeOpen: func(attempt int, p string) {
		if p != "a.txt" {
			return
		}
		_ = os.Remove(filepath.Join(root, "a.txt"))
		_ = syscall.Mkfifo(filepath.Join(root, "a.txt"), 0600)
		// Put a regular file back before the next scan so every attempt races again.
		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = os.Remove(filepath.Join(root, "a.txt"))
			_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("regular"), 0600)
		}()
	}}
	done := make(chan error, 1)
	go func() {
		_, err := acquireLocalHooks(context.Background(), root, ".", data, DefaultLimits(), hooks)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, ErrUnstable) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocked")
	}
	cleanupAll(t, data)
}

func fakeGit(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-git")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// The timeout covers the whole acquisition: each command is quick, together they are not.
func TestGitTimeoutIsAWholeAcquisitionDeadline(t *testing.T) {
	_, data := fixture(t)
	exe := fakeGit(t, "sleep 0.35\ncase \"$*\" in *rev-parse*) echo 1111111111111111111111111111111111111111;; esac\n")
	start := time.Now()
	s, err := AcquireGit(context.Background(), "https://example.invalid/repo", "main", data, DefaultLimits(), GitOptions{Executable: exe, AllowedHost: "example.invalid", Timeout: time.Second})
	if !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("a series of commands outlasting the deadline must end as recovery_required: %v", err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("deadline not enforced: %v", took)
	}
	if s.Root == "" {
		t.Fatal("quarantine directory not returned")
	}
	cleanupSnapshot(t, s)
	if _, err = os.Stat(filepath.Join(s.Root, "recovery-required")); err != nil {
		t.Fatal("missing recovery marker")
	}
}

// A runaway fetch is stopped by the soft size monitor; the result is both a limit failure
// and recovery_required, and the directory is quarantined rather than deleted.
func TestGitFetchSizeMonitorStopsRunawayFetchAndQuarantines(t *testing.T) {
	_, data := fixture(t)
	exe := fakeGit(t, `case "$*" in
*fetch*)
  i=0
  while [ $i -lt 300 ]; do
    head -c 1048576 /dev/zero >> tmp_pack_runaway
    sleep 0.1
    i=$((i+1))
  done;;
esac
`)
	limits := DefaultLimits()
	limits.MaxFileBytes, limits.MaxTotalBytes = 1<<20, 1<<20
	start := time.Now()
	s, err := AcquireGit(context.Background(), "https://example.invalid/repo", "main", data, limits, GitOptions{Executable: exe, AllowedHost: "example.invalid", Timeout: 60 * time.Second})
	if !errors.Is(err, ErrLimit) || !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("want ErrLimit and ErrRecoveryRequired, got %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("monitor too slow: %v", took)
	}
	if s.Root == "" {
		t.Fatal("no quarantine directory")
	}
	cleanupSnapshot(t, s)
	if _, err = os.Stat(filepath.Join(s.Root, "recovery-required")); err != nil {
		t.Fatal("missing recovery marker")
	}
	info, err := os.Stat(filepath.Join(s.Root, "git", "tmp_pack_runaway"))
	if err != nil {
		t.Fatal(err)
	}
	// Soft limit: overshoot of a few polls is expected, running to the end (300 MiB) is not.
	if info.Size() > 64<<20 {
		t.Fatalf("runaway fetch not stopped in time: %d MiB", info.Size()>>20)
	}
}

// Within the limit a slow fetch is not interrupted by the monitor.
func TestGitFetchSizeMonitorLeavesSmallFetchesAlone(t *testing.T) {
	_, data := fixture(t)
	exe := fakeGit(t, "case \"$*\" in\n*fetch*) head -c 524288 /dev/zero > tmp_pack_small; sleep 0.5;; *rev-parse*) echo 1111111111111111111111111111111111111111;; esac\n")
	limits := DefaultLimits()
	limits.MaxFileBytes, limits.MaxTotalBytes = 1<<20, 1<<20
	_, err := AcquireGit(context.Background(), "https://example.invalid/repo", "main", data, limits, GitOptions{Executable: exe, AllowedHost: "example.invalid", Timeout: 30 * time.Second})
	if errors.Is(err, ErrLimit) || errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("monitor interrupted a fetch within the limit: %v", err)
	}
	cleanupAll(t, data)
}
