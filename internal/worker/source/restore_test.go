package source

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func dataDirName(name string) bool { return strings.HasPrefix(name, ".") || name == "node_modules" }

func readWS(t *testing.T, s Snapshot, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.WorkspaceDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// What one task changes or leaves in the attempt workspace is undone before
// the next: rewritten, emptied and deleted manifest files get their manifest
// bytes back, a listed directory replaced by a file becomes a directory
// again, unlisted files and directories are removed and unlisted data
// directories (prepared dependencies, caches) stay untouched.
func TestWorkspaceGuardRestoresSnapshotContent(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "src/lib.rs", "pub fn original() {}\n")
	put(t, root, "src/keep.rs", "pub fn keep() {}\n")
	put(t, root, "docs/a.md", "# a\n")
	put(t, root, "pkg/inner/x.py", "x = 1\n")
	put(t, root, "Cargo.toml", "[package]\nname = \"a\"\n")
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewWorkspaceGuard(s, s.WorkspaceDir, dataDirName)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := g.Restore(context.Background()); err != nil || res.Changed() {
		t.Fatalf("fresh copy changed: %+v %v", res, err)
	}
	ws := s.WorkspaceDir
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("src/lib.rs", "pub fn rewritten() {}\n")
	if err := os.Remove(filepath.Join(ws, "docs", "a.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(ws, "pkg")); err != nil {
		t.Fatal(err)
	}
	write("pkg", "not a directory\n")
	write("Cargo.lock", "# generated\n")
	write("target/debug/out.txt", "build output\n")
	write("src/generated.rs", "pub fn generated() {}\n")
	write("node_modules/dep/index.js", "module.exports = 1;\n")
	write(".cache/state", "cache\n")
	// The same bytes written again (a new change time): not a change.
	write("src/keep.rs", "pub fn keep() {}\n")

	res, err := g.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docs/a.md", "pkg/inner/x.py", "src/lib.rs"}; !slices.Equal(res.Restored, want) {
		t.Fatalf("restored %v, want %v", res.Restored, want)
	}
	if want := []string{"Cargo.lock", "pkg", "src/generated.rs", "target"}; !slices.Equal(res.Removed, want) {
		t.Fatalf("removed %v, want %v", res.Removed, want)
	}
	for rel, body := range map[string]string{"src/lib.rs": "pub fn original() {}\n", "docs/a.md": "# a\n", "pkg/inner/x.py": "x = 1\n",
		"node_modules/dep/index.js": "module.exports = 1;\n", ".cache/state": "cache\n"} {
		if got := readWS(t, s, rel); got != body {
			t.Fatalf("%s = %q, want %q", rel, got, body)
		}
	}
	for _, rel := range []string{"Cargo.lock", "target", "src/generated.rs"} {
		if _, err := os.Lstat(filepath.Join(ws, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s still present: %v", rel, err)
		}
	}
	// The snapshot itself is never written.
	if b, _ := os.ReadFile(filepath.Join(s.SnapshotDir, "src", "lib.rs")); string(b) != "pub fn original() {}\n" {
		t.Fatalf("snapshot changed: %q", b)
	}
	if res, err := g.Restore(context.Background()); err != nil || res.Changed() {
		t.Fatalf("second restore changed: %+v %v", res, err)
	}
}

// A rewrite that keeps the size and puts the modification time back is
// still found (the content is compared whenever the stamp moved), and a
// symbolic link left where a manifest directory was is removed, not
// followed.
func TestWorkspaceGuardFindsHiddenRewritesAndLinks(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "src/a.go", "package a // original\n")
	put(t, root, "lib/b.go", "package b\n")
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewWorkspaceGuard(s, s.WorkspaceDir, dataDirName)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.WorkspaceDir, "src", "a.go")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package a // replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "b.go"), []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.RemoveAll(filepath.Join(s.WorkspaceDir, "lib")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(s.WorkspaceDir, "lib")); err != nil {
			t.Fatal(err)
		}
	}
	res, err := g.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Restored, "src/a.go") || readWS(t, s, "src/a.go") != "package a // original\n" {
		t.Fatalf("hidden rewrite kept: %+v", res)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Lstat(filepath.Join(s.WorkspaceDir, "lib")); err != nil || !fi.IsDir() {
			t.Fatalf("lib is not a directory again: %v", err)
		}
		if readWS(t, s, "lib/b.go") != "package b\n" {
			t.Fatal("lib/b.go not restored")
		}
		if b, _ := os.ReadFile(filepath.Join(outside, "b.go")); string(b) != "package outside\n" {
			t.Fatal("restore wrote through the link")
		}
	}
}

// A cancelled restore stops and says so.
func TestWorkspaceGuardHonorsCancellation(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "a\n")
	s, err := AcquireLocal(context.Background(), root, ".", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewWorkspaceGuard(s, s.WorkspaceDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if _, err := g.Restore(ctx); err == nil {
		t.Fatal("cancelled restore reported success")
	}
}
