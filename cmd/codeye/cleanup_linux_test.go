//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TC-229: while an agent holds its state lock, a running attempt may work
// inside an old snapshot copy, so cleanup keeps every snapshot copy (and
// says so) but still removes a finished workspace.
func TestCleanupKeepsSnapshotsWhileTheAgentRuns(t *testing.T) {
	state := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"acquire-1", "ws-done"} {
		dir := filepath.Join(state, "data", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := os.OpenFile(filepath.Join(state, "agent.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"cleanup", "--agent-state-dir", state, "--older-than", "1d", "--apply", "--format", "json"}, &out, &errb); code != 0 {
		t.Fatalf("apply %d %s", code, errb.String())
	}
	var rep cleanupReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.NodeData.Entries) != 1 || rep.NodeData.Entries[0].Kind != "workspace" || len(rep.Notes) == 0 {
		t.Fatalf("report %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(state, "data", "acquire-1")); err != nil {
		t.Fatal("a snapshot copy was removed while the agent runs")
	}
	if _, err := os.Stat(filepath.Join(state, "data", "ws-done")); !os.IsNotExist(err) {
		t.Fatal("the finished workspace was kept")
	}
}
