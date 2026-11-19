package retention

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func makeDir(t *testing.T, dir string, files map[string]string, at time.Time) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chtimes(p, at, at)
		}
		return nil
	})
}

// TC-229 (node part): old snapshot copies and workspaces are listed with
// their size; a recent one, a workspace of an attempt still open in the
// journal, other names and links are not; retired agent directories next to
// the agent directory are listed; removal works on read-only copies.
func TestScanAndRemoveOldNodeData(t *testing.T) {
	state := filepath.Join(t.TempDir(), "agent")
	data := filepath.Join(state, "data")
	old, recent := time.Now().Add(-40*24*time.Hour), time.Now().Add(-time.Hour)
	makeDir(t, filepath.Join(data, "acquire-old"), map[string]string{"snapshot/a.go": "package a\n", "manifest.json": "{}"}, old)
	makeDir(t, filepath.Join(data, "acquire-new"), map[string]string{"snapshot/a.go": "package a\n"}, recent)
	makeDir(t, filepath.Join(data, "ws-11111111-1111-4111-8111-111111111111"), map[string]string{"a.go": "x"}, old)
	makeDir(t, filepath.Join(data, "ws-22222222-2222-4222-8222-222222222222"), map[string]string{"a.go": "x"}, old)
	makeDir(t, filepath.Join(data, "other"), map[string]string{"keep": "x"}, old)
	makeDir(t, filepath.Join(state, "journal-dir"), map[string]string{"journal.jsonl": ""}, old)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(data, "acquire-old"), filepath.Join(data, "acquire-link")); err != nil {
			t.Fatal(err)
		}
	}
	makeDir(t, state+".retired-123", map[string]string{"journal.jsonl": "{}"}, old)
	makeDir(t, state+".retired-456", map[string]string{"journal.jsonl": "{}"}, recent)
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	active := map[string]bool{"22222222-2222-4222-8222-222222222222": true}
	entries, err := ScanAgent(state, cutoff, active)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := ScanRetired(state, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, retired...)
	Sort(entries)
	var got []string
	for _, e := range entries {
		got = append(got, e.Kind+":"+filepath.Base(e.Path))
		if e.Bytes == 0 || e.Incomplete {
			t.Errorf("%s measured %d incomplete=%v", e.Path, e.Bytes, e.Incomplete)
		}
	}
	want := []string{"retired_agent:agent.retired-123", "snapshot:acquire-old", "workspace:ws-11111111-1111-4111-8111-111111111111"}
	if len(got) != len(want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries %v, want %v", got, want)
		}
	}
	// A read-only copy of the source is still removed.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(data, "acquire-old", "snapshot"), 0o500); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range entries {
		if err := Remove(e); err != nil {
			t.Fatalf("remove %s: %v", e.Path, err)
		}
		if _, err := os.Lstat(e.Path); !os.IsNotExist(err) {
			t.Fatalf("%s still there", e.Path)
		}
	}
	for _, kept := range []string{"acquire-new", "other", "ws-22222222-2222-4222-8222-222222222222"} {
		if _, err := os.Stat(filepath.Join(data, kept)); err != nil {
			t.Fatalf("%s removed: %v", kept, err)
		}
	}
	if _, err := os.Stat(state + ".retired-456"); err != nil {
		t.Fatal("recent retired directory removed")
	}
	// Nothing to scan is not an error.
	if e, err := ScanAgent(filepath.Join(t.TempDir(), "none"), cutoff, nil); err != nil || len(e) != 0 {
		t.Fatal(e, err)
	}
}
