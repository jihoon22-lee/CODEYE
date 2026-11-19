package source

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An empty branch fetches the remote's default branch and records its name.
func TestGitEmptyBranchUsesTheDefaultBranch(t *testing.T) {
	root, data := fixture(t)
	git(t, root, "init", "-b", "trunk")
	put(t, root, "a.txt", "on trunk")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "x")
	git(t, root, "branch", "other")
	s, err := AcquireGit(context.Background(), fileURL(root), "", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if s.Manifest.SourceBranch != "trunk" || find(s, "a.txt").SHA256 == "" {
		t.Fatalf("branch %q, entries %v", s.Manifest.SourceBranch, s.Manifest.Entries)
	}
}

func TestGitFailureClassification(t *testing.T) {
	for msg, want := range map[string]error{
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":   ErrAuthRequired,
		"remote: Repository not found.\nfatal: repository 'https://github.com/o/r/' not found": ErrAuthRequired,
		"fatal: unable to access 'https://x.example/o/r/': Could not resolve host: x.example":  ErrUnreachable,
		"fatal: unable to access 'https://x.example/o/r/': Failed to connect to x.example":     ErrUnreachable,
	} {
		err := gitFailure([]byte(msg))
		if !errors.Is(err, want) || !errors.Is(err, ErrUnavailable) {
			t.Errorf("%q: %v", msg, err)
		}
	}
	if err := gitFailure([]byte("fatal: bad object")); err != ErrUnavailable {
		t.Errorf("other failure: %v", err)
	}
}
