//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TC-125 node part (W25, docs/OPERATIONS.md#start-stop-status): a drain request lets the running
// attempt finish (succeeded, cleanup confirmed), takes no further claim
// although work is queued, and the agent then exits cleanly (nil error,
// exit 0) with every worker journaled as exited.
func TestDrainFinishesRunningWorkThenExits(t *testing.T) {
	h := newHarness(t, "brief")
	h.fc.addJob(h.job())
	h.fc.addJob(h.job())
	drain := make(chan struct{})
	cfg := testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker")
	cfg.Drain = drain
	_, _, done := startAgent(t, cfg)
	waitStartedProc(t, h.state)
	claims := h.fc.claimCount()
	close(drain)
	c := waitFor(t, h.fc.completeCh, 30*time.Second, "complete")
	if c.Outcome != "succeeded" || c.CleanupState != "confirmed" {
		t.Fatalf("drained attempt must finish normally: %+v", c)
	}
	err := waitFor(t, done, 30*time.Second, "agent exit after drain")
	if err != nil {
		t.Fatalf("drained agent exit: %v", err)
	}
	done <- nil
	if h.fc.claimCount() != claims {
		t.Fatalf("claimed after the drain request (%d -> %d)", claims, h.fc.claimCount())
	}
	h.fc.mu.Lock()
	queued := len(h.fc.jobs)
	h.fc.mu.Unlock()
	if queued != 1 {
		t.Fatalf("queued jobs %d", queued)
	}
	j, err := ReadJournal(filepath.Join(h.state, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range j.Workers {
		if !w.Exited {
			t.Fatalf("worker %s not journaled as exited", w.ID)
		}
	}
	for _, a := range j.Attempts {
		if !a.Done {
			t.Fatalf("attempt %s not done", a.ID)
		}
	}
}

// TC-124 node part: areas at their limit are reported in the heartbeat;
// the field is absent without a quota (heartbeat bytes unchanged).
func TestStorageQuotaReportedInHeartbeat(t *testing.T) {
	h := newHarness(t, "succeed")
	logs := t.TempDir()
	if err := os.WriteFile(filepath.Join(logs, "agent.log"), []byte(strings.Repeat("x", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker-none")
	cfg.StorageQuota = map[string]int64{"logs": 10, "workspace": 1 << 40}
	cfg.StorageLogDir = logs
	startAgent(t, cfg)
	deadline := time.Now().Add(10 * time.Second)
	for {
		hb, n := h.fc.heartbeatState()
		if n > 0 && strings.Join(hb.StorageQuotaExceeded, ",") == "logs" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat areas %v", hb.StorageQuotaExceeded)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := New(Config{ControllerURL: "http://127.0.0.1:1", StateDir: h.state, StorageQuota: map[string]int64{"artifacts": 1}}); err == nil {
		t.Fatal("artifacts is not a node area")
	}
	if _, err := New(Config{ControllerURL: "http://127.0.0.1:1", StateDir: h.state, StorageQuota: map[string]int64{"logs": 1}}); err == nil {
		t.Fatal("a logs quota needs --storage-log-dir")
	}
}

func TestHeartbeatOmitsQuotaWithoutLimits(t *testing.T) {
	h := newHarness(t, "succeed")
	startAgent(t, testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker-none"))
	deadline := time.Now().Add(10 * time.Second)
	for {
		hb, n := h.fc.heartbeatState()
		if n > 0 {
			if hb.StorageQuotaExceeded != nil {
				t.Fatalf("field present without a quota: %v", hb.StorageQuotaExceeded)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
