package quota

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The areas map to the agent and personal layout (docs/47 §4): the
// workspace of an acquisition is not counted as snapshot, pinned retry
// copies are workspace, worker homes and the Go module cache are cache.
func TestMeasureAreas(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "agent")
	write(t, filepath.Join(state, "data", "acquire-1", "snapshot", "a.go"), 100)
	write(t, filepath.Join(state, "data", "acquire-1", "manifest.json"), 10)
	write(t, filepath.Join(state, "data", "acquire-1", "workspace", "a.go"), 100)
	write(t, filepath.Join(state, "data", "ws-attempt", "a.go"), 7)
	write(t, filepath.Join(state, "workers", "w1", "scratch", "cache.bin"), 30)
	write(t, filepath.Join(base, "gomod", "m.zip"), 5)
	write(t, filepath.Join(base, "logs", "agent.log"), 3)
	write(t, filepath.Join(base, "artifacts", "sha256", "ab", "abcd"), 11)
	l := Layout{AgentStateDir: state, GoModCache: filepath.Join(base, "gomod"), LogDir: filepath.Join(base, "logs"), ArtifactDir: filepath.Join(base, "artifacts")}
	want := map[string]int64{Snapshot: 110, Workspace: 107, Cache: 35, Logs: 3, Artifacts: 11}
	for area, bytes := range want {
		u, err := Measure(l, area)
		if err != nil || u.Bytes != bytes || u.Incomplete {
			t.Errorf("%s: %+v %v (want %d)", area, u, err, bytes)
		}
	}
	if runtime.GOOS != "windows" {
		// A symlink is counted by its own size and never followed.
		if err := os.Symlink(filepath.Join(base, "artifacts"), filepath.Join(base, "logs", "link")); err != nil {
			t.Fatal(err)
		}
		if u, _ := Measure(l, Logs); u.Files != 2 || u.Bytes != int64(3+len(filepath.Join(base, "artifacts"))) {
			t.Fatalf("symlink followed: %+v", u)
		}
	}
	exceeded, usage := Exceeded(l, map[string]int64{Snapshot: 110, Workspace: 108, Logs: 0})
	if strings.Join(exceeded, ",") != Snapshot || usage[Workspace].Bytes != 107 {
		t.Fatalf("exceeded %v %v", exceeded, usage)
	}
	if _, err := Measure(Layout{}, Logs); err != ErrNotMeasured {
		t.Fatal("unmeasured area")
	}
	if u, err := Measure(Layout{AgentStateDir: filepath.Join(base, "missing")}, Snapshot); err != nil || u.Bytes != 0 {
		t.Fatal("missing dirs are empty")
	}
}

func TestParseLimit(t *testing.T) {
	if a, n, err := ParseLimit("workspace=1073741824"); err != nil || a != Workspace || n != 1<<30 {
		t.Fatal(a, n, err)
	}
	for _, bad := range []string{"artifacts=1", "workspace=0", "workspace=-1", "workspace=1e9", "workspace", "=5", "logs=99999999999999999999"} {
		if _, _, err := ParseLimit(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
