package anypack

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The node_modules the dependency task installed may hold links (pnpm's
// layout); the walk skips exactly those real directories and still refuses
// a link anywhere else.
func TestLinkFreeTreeExceptSkipsInstalledTrees(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory links need privileges on Windows")
	}
	ws := t.TempDir()
	for _, d := range []string{"src", "node_modules/.pnpm/a", "web/node_modules"} {
		if err := os.MkdirAll(filepath.Join(ws, filepath.FromSlash(d)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "a.js"), []byte("1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "node_modules", ".pnpm", "a"), filepath.Join(ws, "node_modules", "a")); err != nil {
		t.Fatal(err)
	}
	roots := []string{"."}
	if LinkFreeTree(ws, roots) {
		t.Fatal("a link in node_modules passes without the skip list")
	}
	if !LinkFreeTreeExcept(ws, roots, []string{"node_modules"}) {
		t.Fatal("the installed tree is not skipped")
	}
	if err := os.Symlink(filepath.Join(ws, "src"), filepath.Join(ws, "src", "loop")); err != nil {
		t.Fatal(err)
	}
	if LinkFreeTreeExcept(ws, roots, []string{"node_modules"}) {
		t.Fatal("a link outside the installed tree passes")
	}
	if err := os.Remove(filepath.Join(ws, "src", "loop")); err != nil {
		t.Fatal(err)
	}
	// A listed name that is a link is not skipped.
	if err := os.RemoveAll(filepath.Join(ws, "web", "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(ws, "web", "node_modules")); err != nil {
		t.Fatal(err)
	}
	if LinkFreeTreeExcept(ws, roots, []string{"node_modules", "web/node_modules"}) {
		t.Fatal("a linked tree passes")
	}
}
