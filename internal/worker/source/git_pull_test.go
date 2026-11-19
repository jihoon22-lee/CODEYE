package source

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// pullRepo is a repository whose refs/pull/7/head points at a commit that no
// branch contains (as on GitHub).
func pullRepo(t *testing.T) (root, data, head string) {
	t.Helper()
	root, data = fixture(t)
	git(t, root, "init", "-b", "main")
	put(t, root, "a.txt", "base")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")
	git(t, root, "checkout", "-b", "feature")
	put(t, root, "b.txt", "change")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "change")
	head = git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/pull/7/head", head)
	git(t, root, "checkout", "main")
	git(t, root, "branch", "-D", "feature")
	return root, data, head
}

func TestGitFetchesThePullRequestHead(t *testing.T) {
	root, data, head := pullRepo(t)
	s, err := AcquireGit(context.Background(), fileURL(root), "", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second, PullRequest: 7, ExpectSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if s.Manifest.SourceSHA != head || s.Manifest.SourceBranch != "" || s.Manifest.SourceRef != "refs/pull/7/head" || find(s, "b.txt").SHA256 == "" {
		t.Fatalf("sha %s branch %q ref %q entries %v", s.Manifest.SourceSHA, s.Manifest.SourceBranch, s.Manifest.SourceRef, s.Manifest.Entries)
	}
	// The stored manifest is one the report builder accepts.
	raw, err := os.ReadFile(s.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(raw); err != nil {
		t.Fatalf("pull-request manifest refused: %v", err)
	}
	for _, bad := range []Manifest{{SourceBranch: "main", SourceRef: "refs/pull/7/head"}, {}, {SourceRef: "refs/heads/main"}, {SourceRef: "refs/pull/0/head"}} {
		m := s.Manifest
		m.SourceBranch, m.SourceRef = bad.SourceBranch, bad.SourceRef
		if validateManifest(m) == nil {
			t.Errorf("manifest with branch %q ref %q accepted", bad.SourceBranch, bad.SourceRef)
		}
	}
}

// A push after the pin moves refs/pull/7/head: the pinned commit itself is
// fetched when the host serves it, else the acquisition is unstable.
func TestGitPullRequestThatMovedFetchesThePinnedCommit(t *testing.T) {
	root, data, head := pullRepo(t)
	git(t, root, "checkout", "-q", head)
	put(t, root, "c.txt", "newer push")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "newer")
	newer := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/pull/7/head", newer)
	git(t, root, "checkout", "-q", "main")
	// git serves unadvertised reachable commits over protocol v2.
	s, err := AcquireGit(context.Background(), fileURL(root), "", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second, PullRequest: 7, ExpectSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if s.Manifest.SourceSHA != head || find(s, "c.txt").SHA256 != "" {
		t.Fatalf("sha %s, want the pinned %s", s.Manifest.SourceSHA, head)
	}
	gone := strings.Repeat("ab", 20)
	_, err = AcquireGit(context.Background(), fileURL(root), "", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second, PullRequest: 7, ExpectSHA: gone})
	if !errors.Is(err, ErrUnstable) {
		t.Fatalf("unreachable pinned commit: %v", err)
	}
}

func TestGitPullRequestNeedsAPinnedHeadAndNoBranch(t *testing.T) {
	_, data := fixture(t)
	for _, o := range []GitOptions{{PullRequest: 7}, {PullRequest: 7, ExpectSHA: "short"}, {PullRequest: -1}, {Token: "a\nb"}} {
		o.Executable, o.AllowedHost, o.Timeout = "/usr/bin/git", "github.com", time.Second
		if _, err := AcquireGit(context.Background(), "https://github.com/o/r", "", data, DefaultLimits(), o); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", o, err)
		}
	}
	o := GitOptions{Executable: "/usr/bin/git", AllowedHost: "github.com", Timeout: time.Second, PullRequest: 7, ExpectSHA: strings.Repeat("a", 40)}
	if _, err := AcquireGit(context.Background(), "https://github.com/o/r", "main", data, DefaultLimits(), o); !errors.Is(err, ErrInvalid) {
		t.Errorf("branch with a pull request: %v", err)
	}
}

// The token reaches git as a host-scoped Authorization header in the
// environment only: not in argv, not in the remote URL, not in a file of the
// acquisition. A fake git records what it was given.
func TestGitTokenTravelsInTheEnvironmentOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake git")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "git")
	script := "#!/bin/sh\n{ printf 'ARGV'; for a in \"$@\"; do printf ' %s' \"$a\"; done; printf '\\n'; env | sort; printf 'END\\n'; } >> " + record + "\n" +
		"case \"$*\" in *ls-remote*) printf 'ref: refs/heads/main\\tHEAD\\n';; esac\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, data := fixture(t)
	const token = "ghp_secretTOKEN1234567890"
	_, err := AcquireGit(context.Background(), "https://github.com/o/r", "", data, DefaultLimits(), GitOptions{Executable: fake, AllowedHost: "github.com", Timeout: 10 * time.Second, Token: token})
	if err == nil {
		t.Fatal("the fake git cannot succeed")
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	header := "GIT_CONFIG_VALUE_0=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	calls := strings.Split(string(raw), "END\n")
	seen := 0
	for _, c := range calls {
		if strings.TrimSpace(c) == "" {
			continue
		}
		seen++
		argv, _, _ := strings.Cut(c, "\n")
		if strings.Contains(argv, token) || strings.Contains(argv, "x-access-token") || strings.Contains(argv, "Authorization") || strings.Contains(argv, "@github.com") {
			t.Fatalf("credential in argv: %s", argv)
		}
		if !strings.Contains(c, "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader\n") || !strings.Contains(c, header+"\n") || !strings.Contains(c, "GIT_CONFIG_COUNT=1\n") {
			t.Fatalf("no scoped header in the environment of %s", argv)
		}
	}
	if seen == 0 {
		t.Fatal("git was not run")
	}
	// Nothing of the failed acquisition stays, so no file holds the token.
	_ = filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), token) {
				t.Errorf("token in %s", p)
			}
		}
		return nil
	})
}

func TestReadHostToken(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "github.json")
	if ReadHostToken(p, "github.com") != "" || ReadHostToken("", "github.com") != "" {
		t.Fatal("token without a file")
	}
	if err := os.WriteFile(p, []byte(`{"schema_version":"0.1","host":"github.com","token":"tok12345678","source":"gh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ReadHostToken(p, "GitHub.com") != "tok12345678" || ReadHostToken(p, "ghe.example") != "" {
		t.Fatal("host binding")
	}
}
