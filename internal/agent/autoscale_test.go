//go:build linux

package agent

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Autoscale mode with real worker processes (the test binary re-executed
// as "worker"/"worker-none") against the strict fake controller.

func autoscaleConfig(h *harness, workerArg string, cap int) Config {
	cfg := testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, workerArg)
	cfg.Workers, cfg.Autoscale, cfg.MaxWorkers = 0, true, cap
	return cfg
}

// waitHB waits until a heartbeat satisfies ok.
func waitHB(t *testing.T, fc *fakeController, d time.Duration, what string, ok func(nodeproto.NodeHeartbeatRequest) bool) nodeproto.NodeHeartbeatRequest {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		hb, n := fc.heartbeatState()
		if n > 0 && ok(hb) {
			return hb
		}
		time.Sleep(20 * time.Millisecond)
	}
	hb, _ := fc.heartbeatState()
	t.Fatalf("timeout waiting for %s; last heartbeat %+v", what, hb)
	return hb
}

func observed(hb nodeproto.NodeHeartbeatRequest) map[string]string {
	out := map[string]string{}
	for _, w := range hb.ObservedWorkers {
		out[w.ID] = w.State
	}
	return out
}

func journalWorkerStarts(t *testing.T, state string) []string {
	t.Helper()
	j, err := OpenJournal(filepath.Join(state, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	var ids []string
	j.With(func(j *Journal) {
		for id := range j.Workers {
			ids = append(ids, id)
		}
	})
	return ids
}

// An autoscale agent starts with zero workers yet advertises the analysis
// capability its probe worker validated; with no handler it advertises none.
func TestAutoscaleZeroWorkersAdvertisesProbedCapabilities(t *testing.T) {
	for _, tc := range []struct {
		worker string
		want   bool
	}{{"worker", true}, {"worker-none", false}} {
		t.Run(tc.worker, func(t *testing.T) {
			h := newHarness(t, "succeed")
			logs := &lockedBuf{}
			cfg := autoscaleConfig(h, tc.worker, 2)
			cfg.Log = logs
			ag, _, _ := startAgent(t, cfg)
			hb := waitHB(t, h.fc, 30*time.Second, "first heartbeat", func(nodeproto.NodeHeartbeatRequest) bool { return true })
			if hb.WorkerScaling != nodeproto.ScalingController || hb.MaxWorkers != 2 || len(hb.ObservedWorkers) != 0 {
				t.Fatalf("heartbeat %+v", hb)
			}
			if got := slices.Contains(hb.Capabilities, "analysis:deep-review"); got != tc.want {
				t.Fatalf("analysis capability %v want %v: %v", got, tc.want, hb.Capabilities)
			}
			if !slices.Contains(hb.Capabilities, nodeproto.LocalSourceCapability(h.rootID)) {
				t.Fatalf("source capability missing: %v", hb.Capabilities)
			}
			if len(ag.workerPIDs()) != 0 {
				t.Fatal("autoscale agent kept a worker")
			}
			// The probe worker ran as a real, journaled, exited process.
			if !strings.Contains(logs.String(), `"code":"capability_probe"`) {
				t.Fatalf("no probe log: %s", logs.String())
			}
			time.Sleep(500 * time.Millisecond)
			if h.fc.claimCount() != 0 {
				t.Fatal("claimed without a worker")
			}
		})
	}
}

// Controller commands drive the worker set: start → idle → claim → drain
// (the attempt is not killed; the worker exits after it) → restart of the
// agent with commands resent below the cursor never restarts a worker twice;
// the local cap refuses extra starts.
func TestAutoscaleCommandsDrainAndRestartDedupe(t *testing.T) {
	h := newHarness(t, "sleep")
	h.fc.resendAll = true // the controller (fake) resends every command, always
	logs := &lockedBuf{}
	cfg := autoscaleConfig(h, "worker", 2)
	cfg.Log = logs
	_, cancel, done := startAgent(t, cfg)
	w1 := domain.NewID()
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w1)
	waitHB(t, h.fc, 30*time.Second, "w1 idle", func(hb nodeproto.NodeHeartbeatRequest) bool { return observed(hb)[w1] == "idle" })
	h.fc.addJob(h.job())
	waitTaskRunning(t, h.fc)
	h.fc.queueWorkerCommand(nodeproto.CommandDrainWorker, w1)
	// A stop planned from an earlier idle heartbeat must also wait for the current attempt.
	h.fc.queueWorkerCommand(nodeproto.CommandStopWorker, w1)
	// Drain does not cancel: the sleep task keeps running.
	time.Sleep(1500 * time.Millisecond)
	select {
	case c := <-h.fc.completeCh:
		t.Fatalf("drain ended the attempt: %+v", c)
	default:
	}
	if s := observed(waitHB(t, h.fc, 5*time.Second, "w1 still busy", func(nodeproto.NodeHeartbeatRequest) bool { return true }))[w1]; s != "busy" {
		t.Fatalf("drained busy worker state %q", s)
	}
	// The attempt ends (user cancel); the drained worker then exits by itself.
	h.fc.mu.Lock()
	var attemptID string
	for id := range h.fc.attempts {
		attemptID = id
	}
	h.fc.mu.Unlock()
	h.fc.queueCommand(nodeproto.CommandCancelAttempt, attemptID)
	if c := waitFor(t, h.fc.completeCh, 30*time.Second, "complete"); c.Outcome != "cancelled" || c.CleanupState != "confirmed" {
		t.Fatalf("complete %+v", c)
	}
	waitHB(t, h.fc, 30*time.Second, "drained worker gone", func(hb nodeproto.NodeHeartbeatRequest) bool { return len(hb.ObservedWorkers) == 0 })
	// Cap 2: three starts, two run.
	w2, w3, w4 := domain.NewID(), domain.NewID(), domain.NewID()
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w2)
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w3)
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w4)
	waitHB(t, h.fc, 30*time.Second, "two workers", func(hb nodeproto.NodeHeartbeatRequest) bool { return len(hb.ObservedWorkers) == 2 })
	time.Sleep(time.Second)
	if hb, _ := h.fc.heartbeatState(); len(hb.ObservedWorkers) != 2 || !strings.Contains(logs.String(), "worker_start_refused") {
		t.Fatalf("cap not enforced: %+v", hb.ObservedWorkers)
	}
	// Restart: every start (w1..w4) is resent; none is applied again.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	done <- nil
	before := len(journalWorkerStarts(t, h.state))
	logs2 := &lockedBuf{}
	cfg.Log = logs2
	cfg.EnrollTokenFile = ""
	startAgent(t, cfg)
	_, n := h.fc.heartbeatState()
	waitHB(t, h.fc, 30*time.Second, "heartbeats after restart", func(nodeproto.NodeHeartbeatRequest) bool { _, m := h.fc.heartbeatState(); return m >= n+5 })
	if hb, _ := h.fc.heartbeatState(); len(hb.ObservedWorkers) != 0 || strings.Contains(logs2.String(), `"code":"worker_ready"`) {
		t.Fatalf("restart re-applied start commands: %+v\n%s", hb.ObservedWorkers, logs2.String())
	}
	// Only the restart probe worker was added to the journal.
	if after := len(journalWorkerStarts(t, h.state)); after > before+1 {
		t.Fatalf("journal workers %d → %d", before, after)
	}
	// A new start is applied exactly once although it is resent every beat.
	w5 := domain.NewID()
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w5)
	waitHB(t, h.fc, 30*time.Second, "w5 idle", func(hb nodeproto.NodeHeartbeatRequest) bool { return observed(hb)[w5] == "idle" })
	time.Sleep(time.Second)
	if c := strings.Count(logs2.String(), `"worker_id":"`+w5+`"`); c == 0 || strings.Count(logs2.String(), `"code":"worker_ready"`) != 1 {
		t.Fatalf("w5 started more than once:\n%s", logs2.String())
	}
}

func TestStartNeedsDurableCommandCursor(t *testing.T) {
	scaler := &ControllerScaler{Cap: 1}
	ag := Agent{cfg: Config{Log: &lockedBuf{}}, journal: &Journal{}, scaler: scaler}
	worker := domain.NewID()
	command := nodeproto.Command{CommandID: domain.NewID(), Sequence: 1, Kind: nodeproto.CommandStartWorker, WorkerID: &worker}
	if err := ag.applyCommand(command); err == nil || len(scaler.Plan(0, 0)) != 0 {
		t.Fatal("a closed journal permitted a start")
	}
	// Compaction retains the sequence even after an old command ID has been dropped.
	ag.journal.LastSequence = command.Sequence
	if err := ag.applyCommand(command); err != nil || len(scaler.Plan(0, 0)) != 0 {
		t.Fatal("a command below the durable cursor was replayed")
	}
}

// A draining autoscale agent leaves the controller's scaling plan: its
// heartbeat reports static scaling and no analysis capability while the busy
// worker keeps its attempt; a start command that still arrives is not applied.
func TestDrainingAgentLeavesTheScalingPlan(t *testing.T) {
	h := newHarness(t, "sleep")
	logs := &lockedBuf{}
	cfg := autoscaleConfig(h, "worker", 2)
	cfg.Log = logs
	ag, _, _ := startAgent(t, cfg)
	w1 := domain.NewID()
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w1)
	waitHB(t, h.fc, 30*time.Second, "w1 idle", func(hb nodeproto.NodeHeartbeatRequest) bool { return observed(hb)[w1] == "idle" })
	h.fc.addJob(h.job())
	waitTaskRunning(t, h.fc)
	ag.beginDrain()
	hb := waitHB(t, h.fc, 30*time.Second, "draining heartbeat", func(hb nodeproto.NodeHeartbeatRequest) bool {
		return hb.WorkerScaling == "" && observed(hb)[w1] == "busy"
	})
	if hb.MaxWorkers != 0 || slices.Contains(hb.Capabilities, "analysis:deep-review") {
		t.Fatalf("draining heartbeat %+v", hb)
	}
	if !slices.Contains(hb.Capabilities, nodeproto.LocalSourceCapability(h.rootID)) {
		t.Fatalf("source capability dropped: %v", hb.Capabilities)
	}
	w2 := domain.NewID()
	h.fc.queueWorkerCommand(nodeproto.CommandStartWorker, w2)
	_, n := h.fc.heartbeatState()
	waitHB(t, h.fc, 30*time.Second, "beats after the start", func(nodeproto.NodeHeartbeatRequest) bool { _, m := h.fc.heartbeatState(); return m >= n+3 })
	if hb, _ := h.fc.heartbeatState(); observed(hb)[w2] != "" || observed(hb)[w1] != "busy" {
		t.Fatalf("draining agent started a worker or lost the attempt: %+v", hb.ObservedWorkers)
	}
	select {
	case c := <-h.fc.completeCh:
		t.Fatalf("drain ended the attempt: %+v", c)
	default:
	}
}
