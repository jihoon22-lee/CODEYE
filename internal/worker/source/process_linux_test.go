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

func TestSpecialFileAndGitTimeout(t *testing.T) {
	root, data := fixture(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if find(s, "pipe").ExcludedReason != "special_file_not_copied" {
		t.Fatal("special file read")
	}
	fake := filepath.Join(t.TempDir(), "git-slow")
	if err = os.WriteFile(fake, []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pending, err := AcquireGit(context.Background(), "https://example.invalid/repo", "main", data, DefaultLimits(), GitOptions{Executable: fake, AllowedHost: "example.invalid", Timeout: 30 * time.Millisecond})
	if !errors.Is(err, ErrRecoveryRequired) || pending.Root == "" {
		t.Fatal("timeout lost recovery state", err)
	}
	cleanupSnapshot(t, pending)
	if _, err = os.Stat(filepath.Join(pending.Root, "recovery-required")); err != nil {
		t.Fatal("missing recovery marker")
	}
}
