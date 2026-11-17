package source

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	original := filepath.Join(base, "original")
	data := filepath.Join(base, "data")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	return original, data
}
func put(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, root, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func cleanupSnapshot(t *testing.T, s Snapshot) {
	t.Helper()
	t.Cleanup(func() {
		if err := Remove(s.Root); err != nil {
			t.Error(err)
		}
	})
}
func find(s Snapshot, path string) Entry {
	for _, e := range s.Manifest.Entries {
		if e.Path == path {
			return e
		}
	}
	return Entry{}
}
func TestLocalIndependentSnapshotsAndRetry(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "src/한 글.py", "value = 1\n")
	put(t, root, "README.md", "intro")
	put(t, root, "literal;$(echo injected).py", "literal")
	put(t, root, ".env", "private")
	put(t, root, ".env.example", "safe")
	put(t, root, ".git/config", "private remote")
	put(t, root, "node_modules/dependency.js", "ignored")
	if err := os.Mkdir(filepath.Join(root, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if s.Manifest.Consistency != "stable_copy_best_effort" || find(s, ".env").ExcludedReason != "secret_file_policy" || find(s, "node_modules").ExcludedReason == "" {
		t.Fatal(s.Manifest)
	}
	put(t, s.WorkspaceDir, "src/한 글.py", "changed workspace")
	if read(t, root, "src/한 글.py") != "value = 1\n" || read(t, s.SnapshotDir, "src/한 글.py") != "value = 1\n" {
		t.Fatal("shared source bytes")
	}
	if read(t, s.WorkspaceDir, "literal;$(echo injected).py") != "literal" {
		t.Fatal("shell metacharacters lost")
	}
	if err := CloneWorkspace(context.Background(), s, filepath.Join(root, "forbidden-work")); !errors.Is(err, ErrInvalid) {
		t.Fatal("retry wrote outside managed data root", err)
	}
	put(t, root, "src/한 글.py", "new original")
	loaded, err := Load(context.Background(), s.Root, s.Digest)
	if err != nil {
		t.Fatal(err)
	}
	retry := filepath.Join(data, "retry")
	if err = CloneWorkspace(context.Background(), loaded, retry); err != nil {
		t.Fatal(err)
	}
	if read(t, retry, "src/한 글.py") != "value = 1\n" {
		t.Fatal("retry reacquired source")
	}
	if _, err = os.Stat(filepath.Join(retry, "empty")); err != nil {
		t.Fatal("empty directory lost", err)
	}
	if _, err = os.Stat(filepath.Join(retry, ".env")); !os.IsNotExist(err) {
		t.Fatal("secret file copied")
	}
}
func TestLocalChangesAreRetriedAndUnstableFails(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "one")
	s, err := acquireLocal(context.Background(), root, ".", data, DefaultLimits(), func(attempt int) {
		if attempt == 0 {
			put(t, root, "new.txt", "two")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if read(t, s.SnapshotDir, "new.txt") != "two" {
		t.Fatal("retry missed added file")
	}
	_, err = acquireLocal(context.Background(), root, ".", data, DefaultLimits(), func(attempt int) { put(t, root, "a.txt", strings.Repeat("x", attempt+4)) })
	if !errors.Is(err, ErrUnstable) {
		t.Fatalf("got %v", err)
	}
}
func TestLocalLinksBoundariesAndDataPath(t *testing.T) {
	root, data := fixture(t)
	outside := t.TempDir()
	put(t, outside, "private.txt", "outside")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlink creation unavailable")
	}
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if find(s, "link").ExcludedReason != "link_not_followed" {
		t.Fatal(s.Manifest)
	}
	if _, err = os.Stat(filepath.Join(s.SnapshotDir, "link")); !os.IsNotExist(err) {
		t.Fatal("followed link")
	}
	_, err = AcquireLocal(context.Background(), root, "link", data, DefaultLimits())
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	_, err = AcquireLocal(context.Background(), root, "..", data, DefaultLimits())
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(data), "data-alias")
	if err = os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	_, err = AcquireLocal(context.Background(), root, ".", filepath.Join(alias, "must-not-create"), DefaultLimits())
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "must-not-create")); !os.IsNotExist(err) {
		t.Fatal("wrote inside original before validation")
	}
}
func TestLocalLimitsCancellationAndIntegrity(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "12345")
	limit := DefaultLimits()
	limit.MaxFileBytes = 4
	_, err := AcquireLocal(context.Background(), root, ".", data, limit)
	if !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = AcquireLocal(ctx, root, ".", data, DefaultLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if err = os.Chmod(filepath.Join(s.SnapshotDir, "a.txt"), 0600); err != nil {
		t.Fatal(err)
	}
	put(t, s.SnapshotDir, "a.txt", "wrong")
	err = CloneWorkspace(context.Background(), s, filepath.Join(data, "tampered"))
	if !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	_, err = Load(context.Background(), s.Root, strings.Repeat("0", 64))
	if !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
}
func TestPortablePaths(t *testing.T) {
	for _, path := range []string{"../a", "/a", "C:/a", "a\\b", "a//b", "CON", "com1.txt", "trailing.", "trailing ", "bad\x00"} {
		if portablePath(path) {
			t.Errorf("accepted %q", path)
		}
	}
}
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	base := []string{"-c", "user.name=CODEYE Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	cmd := exec.Command(bin, append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "USERPROFILE=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0"}
	for _, k := range []string{"SystemRoot", "WINDIR"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %s: %s", args[0], out)
	}
	return strings.TrimSpace(string(out))
}
func fileURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
func TestGitFixesAcquireHEADWithoutFiltersOrCredentials(t *testing.T) {
	root, data := fixture(t)
	git(t, root, "init", "-b", "main")
	put(t, root, "a.txt", "first")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "first")
	first := git(t, root, "rev-parse", "HEAD")
	// Queue-time HEAD differs from acquire-time HEAD. No export-ignore/filter is honored.
	put(t, root, "a.txt", "second")
	put(t, root, "hidden.txt", "tracked")
	put(t, root, ".gitattributes", "hidden.txt export-ignore\n*.txt filter=forbidden\n")
	put(t, root, "large.bin", "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 99\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "second")
	second := git(t, root, "rev-parse", "HEAD")
	if first == second {
		t.Fatal("fixture did not advance")
	}
	bin, _ := exec.LookPath("git")
	bin, _ = filepath.Abs(bin)
	options := GitOptions{Executable: bin, AllowedLocalRoot: root, Timeout: 10 * time.Second}
	s, err := AcquireGit(context.Background(), fileURL(root), "main", data, DefaultLimits(), options)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	if s.Manifest.SourceSHA != second || read(t, s.WorkspaceDir, "a.txt") != "second" || read(t, s.WorkspaceDir, "hidden.txt") != "tracked" {
		t.Fatal("incorrect Git snapshot")
	}
	if find(s, "large.bin").Kind != "lfs_pointer" || !strings.HasPrefix(read(t, s.WorkspaceDir, "large.bin"), "version https:") {
		t.Fatal("LFS gap lost")
	}
	put(t, root, "a.txt", "third")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "third")
	if read(t, s.SnapshotDir, "a.txt") != "second" {
		t.Fatal("branch movement changed snapshot")
	}
	if _, err = os.Stat(filepath.Join(s.WorkspaceDir, ".git")); !os.IsNotExist(err) {
		t.Fatal("git metadata leaked")
	}
	if _, err = os.Stat(filepath.Join(s.Root, "git")); !os.IsNotExist(err) {
		t.Fatal("fetch metadata retained")
	}
}
func TestGitTransportPolicyAndMalformedBatch(t *testing.T) {
	for _, repository := range []string{"https://user:secret@example.invalid/repo", "https://example.invalid/repo?token=secret", "https://evil.invalid/repo", "ssh://example.invalid/repo", "file:///etc"} {
		if _, _, err := gitLocation(repository, GitOptions{AllowedHost: "example.invalid"}); err == nil {
			t.Fatal("accepted unapproved transport")
		}
	}
	if _, _, err := gitLocation("https://example.invalid/repo", GitOptions{AllowedHost: "example.invalid"}); err != nil {
		t.Fatal(err)
	}
	_, data := fixture(t)
	s, err := newSnapshot(data, "git", "git_commit")
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	err = extractObjects(context.Background(), &s, []Entry{{Path: "a.txt", Kind: "file"}}, []byte("bad blob -1\n"), []string{strings.Repeat("a", 40)}, DefaultLimits())
	if err == nil {
		t.Fatal("malformed object accepted")
	}
}
