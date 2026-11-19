package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// reconcileWorkers keeps the scaler's desired worker count.
func (ag *Agent) reconcileWorkers(ctx context.Context) {
	ag.mu.Lock()
	if ag.shutting || ag.draining || ag.starting > 0 {
		ag.mu.Unlock()
		return
	}
	// Backoff first: Plan consumes controller start requests, which must not
	// be dropped while a failed start is backing off.
	if time.Since(ag.lastStart) < ag.backoff() {
		ag.mu.Unlock()
		return
	}
	alive := 0
	for _, w := range ag.workers {
		if !w.draining {
			alive++
		}
	}
	ids := ag.scaler.Plan(alive, len(ag.workers))
	if cs, ok := ag.scaler.(*ControllerScaler); ok {
		for _, id := range cs.Refused() {
			ag.log("warn", "worker_start_refused", "worker_id", id, "reason", "max_workers")
		}
	}
	if len(ids) == 0 {
		ag.mu.Unlock()
		return
	}
	ag.starting = len(ids)
	ag.lastStart = time.Now()
	ag.mu.Unlock()
	go func() {
		for _, id := range ids {
			w, err := ag.startWorker(ctx, id)
			ag.mu.Lock()
			ag.starting--
			if err != nil {
				ag.failures++
				ag.mu.Unlock()
				ag.log("error", "worker_start_failed", "worker_id", id)
				continue
			}
			ag.failures = 0
			ag.workers[id] = w
			if ag.cfg.Autoscale {
				ag.probeHandlers, ag.probed = w.handlers, true // latest observation
			}
			shutting := ag.shutting || ag.draining
			if ag.draining {
				w.draining = true
			}
			ag.mu.Unlock()
			go ag.awaitExit(w)
			if shutting {
				go ag.stopWorker(w)
				continue
			}
			ag.log("info", "worker_ready", "worker_id", id, "pid", fmt.Sprint(w.ident.PID), "handlers", fmt.Sprint(len(w.handlers)))
			if len(w.handlers) == 0 {
				ag.log("warn", "no_analysis_handler", "worker_id", id)
			}
		}
		ag.heartbeatSoon()
	}()
}

func (ag *Agent) backoff() time.Duration {
	if ag.failures == 0 {
		return 0
	}
	d := time.Second << min(ag.failures, 6)
	return d
}

// probe starts one worker only to read its ready handlers (validated in
// capabilities()), then stops it. The probe worker is journaled like any
// worker but is never reported to the controller and never claims. If the
// probe fails nothing is advertised and it is retried after probeRetry.
func (ag *Agent) probe(ctx context.Context) {
	ag.mu.Lock()
	if ag.probing || ag.shutting {
		ag.mu.Unlock()
		return
	}
	ag.probing, ag.lastProbe = true, time.Now()
	ag.mu.Unlock()
	defer func() {
		ag.mu.Lock()
		ag.probing = false
		ag.mu.Unlock()
	}()
	w, err := ag.startWorker(ctx, domain.NewID())
	if err != nil {
		ag.log("warn", "capability_probe_failed")
		return
	}
	ag.stopWorker(w)
	select {
	case <-w.exited:
		_ = os.RemoveAll(w.dir)
	case <-time.After(10 * time.Second):
		ag.log("warn", "capability_probe_worker_not_exited", "worker_id", w.id)
	}
	ag.mu.Lock()
	ag.probeHandlers, ag.probed = w.handlers, true
	ag.mu.Unlock()
	ag.log("info", "capability_probe", "worker_id", w.id, "handlers", fmt.Sprint(len(w.handlers)))
	if len(w.handlers) == 0 {
		ag.log("warn", "no_analysis_handler", "worker_id", w.id)
	}
}

// retryProbe re-runs a failed startup probe in the background (autoscale).
func (ag *Agent) retryProbe(ctx context.Context) {
	if !ag.cfg.Autoscale {
		return
	}
	ag.mu.Lock()
	due := !ag.probed && !ag.probing && !ag.shutting && !ag.draining && time.Since(ag.lastProbe) >= probeRetry
	ag.mu.Unlock()
	if due {
		go ag.probe(ctx)
	}
}

// stopWorker asks an idle worker to exit.
func (ag *Agent) stopWorker(w *workerProc) {
	_ = w.conn.Send(&workerproto.Shutdown{})
	ag.killWorker(w)
}

// awaitExit handles a worker process exit, reconciling an unfinished attempt.
func (ag *Agent) awaitExit(w *workerProc) {
	<-w.exited
	ag.mu.Lock()
	delete(ag.workers, w.id)
	att := w.att
	w.att = nil
	shutting := ag.shutting
	ag.mu.Unlock()
	if att != nil {
		att.mu.Lock()
		done := att.completed
		att.mu.Unlock()
		if !done {
			ag.workerLost(w, att)
		}
	}
	uncertain := false
	ag.journal.With(func(j *Journal) {
		for _, p := range j.Pending {
			if p.WorkerID == w.id && p.CleanupState != nodeproto.CleanupConfirmed {
				uncertain = true
			}
		}
	})
	if !uncertain {
		_ = os.RemoveAll(w.dir)
	}
	if !shutting {
		ag.log("warn", "worker_exited", "worker_id", w.id)
	}
	ag.kick()
}

// workerLost: the worker died during an attempt. Verify the journaled
// processes, then complete the attempt with the real cleanup state.
func (ag *Agent) workerLost(w *workerProc, att *activeAttempt) {
	att.stopWatch()
	state, method := ag.reconcileAttempt(att.lease.AttemptID, nil)
	reason := "worker_lost"
	req := nodeproto.CompleteRequest{WorkerID: w.id, LeaseEpoch: att.lease.LeaseEpoch, Outcome: nodeproto.OutcomeFailed, CleanupState: state, Reason: &reason}
	// Always try: the local watchdog margin may fire while the server lease is
	// still valid; the controller decides staleness.
	err := retry(context.Background(), 4, func() error {
		_, e := ag.client.Complete(context.Background(), att.lease.AttemptID, req)
		return e
	})
	outcome := nodeproto.OutcomeFailed
	if err != nil {
		outcome = "rejected"
		_ = ag.journal.Append(Record{Kind: RecCleanupPending, AttemptID: att.lease.AttemptID, WorkerID: w.id, Epoch: att.lease.LeaseEpoch, CleanupState: state, Method: method, VerifiedAt: time.Now().UTC()})
	}
	_ = ag.journal.Append(Record{Kind: RecAttemptDone, AttemptID: att.lease.AttemptID, Outcome: outcome, CleanupState: state})
	ag.log("warn", "attempt_worker_lost", "attempt_id", att.lease.AttemptID, "cleanup_state", state)
}
