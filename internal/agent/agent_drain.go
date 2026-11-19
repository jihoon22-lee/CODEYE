package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// beginDrain stops claims and worker starts; idle workers are stopped
// now, busy ones when their attempt ends (they are marked draining).
func (ag *Agent) beginDrain() {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if ag.draining {
		return
	}
	ag.draining = true
	for _, w := range ag.workers {
		w.draining = true
		if w.att == nil {
			go ag.stopWorker(w)
		}
	}
	ag.log("info", "agent_drain_requested")
}

// drained: a drain was requested and no attempt runs, no claim is in flight
// and no worker is starting.
func (ag *Agent) drained() bool {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if !ag.draining || ag.claiming > 0 || ag.starting > 0 || ag.probing {
		return false
	}
	for _, w := range ag.workers {
		if w.att != nil {
			return false
		}
	}
	return true
}

// storageQuotaExceeded returns the node storage areas at or over their limit
// (sorted), re-measuring at most every storageMeasureEvery. A change is logged.
func (ag *Agent) storageQuotaExceeded() []string {
	if len(ag.cfg.StorageQuota) == 0 {
		return nil
	}
	if !ag.quotaChecked.IsZero() && time.Since(ag.quotaChecked) < storageMeasureEvery {
		return ag.quotaExceeded
	}
	layout := quota.Layout{AgentStateDir: ag.cfg.StateDir, GoModCache: ag.cfg.GoModCache, DependencyCache: ag.cfg.DependencyCacheDir, LogDir: ag.cfg.StorageLogDir}
	exceeded, _ := quota.Exceeded(layout, ag.cfg.StorageQuota)
	if strings.Join(exceeded, ",") != strings.Join(ag.quotaExceeded, ",") {
		if len(exceeded) > 0 {
			ag.log("warn", "storage_quota_exceeded", "areas", strings.Join(exceeded, ","))
		} else {
			ag.log("info", "storage_quota_ok")
		}
	}
	ag.quotaExceeded, ag.quotaChecked = exceeded, time.Now()
	return exceeded
}

// shutdown cancels running attempts, waits for their real cleanup, stops
// workers and flushes cleanup reports.
func (ag *Agent) shutdown() error {
	ag.mu.Lock()
	ag.shutting = true
	for _, w := range ag.workers {
		w.draining = true
		if w.att != nil {
			ag.sendCancelLocked(w, w.att, workerproto.CancelShutdown)
		}
	}
	ag.mu.Unlock()
	deadline := time.Now().Add(ag.cfg.ShutdownTimeout)
	for time.Now().Before(deadline) {
		busy := false
		ag.mu.Lock()
		if ag.starting > 0 {
			busy = true // a start in flight registers its worker first
		}
		for _, w := range ag.workers {
			if w.att != nil {
				busy = true
			}
		}
		ag.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	ag.mu.Lock()
	ws := make([]*workerProc, 0, len(ag.workers))
	for _, w := range ag.workers {
		ws = append(ws, w)
	}
	ag.mu.Unlock()
	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func() { defer wg.Done(); ag.stopWorker(w) }()
	}
	wg.Wait()
	// Give exit handlers a moment to journal and reconcile.
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		ag.mu.Lock()
		n := len(ag.workers)
		ag.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	hctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = ag.heartbeat(hctx)
	cancel()
	ag.cancel()
	uncertain := false
	ag.journal.With(func(j *Journal) {
		for _, p := range j.Pending {
			if p.CleanupState != nodeproto.CleanupConfirmed {
				uncertain = true
			}
		}
		for _, w := range j.Workers {
			if !w.Exited {
				uncertain = true
			}
		}
	})
	ag.mu.Lock()
	remaining := len(ag.workers)
	ag.mu.Unlock()
	if remaining == 0 {
		// Safe only now: the locked thread's exit fires workers' PDEATHSIG.
		ag.spawner.close()
	}
	ag.log("info", "agent_stopped", "uncertain", fmt.Sprint(uncertain))
	if uncertain {
		return &ExitError{ExitRuntime, "shutdown with uncertain cleanup"}
	}
	return nil
}
