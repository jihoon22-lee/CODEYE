package agent

import (
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent/procid"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const (
	recoverGrace = 2 * time.Second
	recoverWait  = 5 * time.Second
)

// reconcileStartup runs before the first heartbeat after a restart. Each
// journaled worker still marked running is terminated only after identity
// verification; each unfinished attempt becomes a cleanup report and is
// never re-run.
func (ag *Agent) reconcileStartup() {
	var workers []JWorker
	var attempts []string
	ag.journal.With(func(j *Journal) {
		for _, w := range j.Workers {
			if !w.Exited {
				workers = append(workers, *w)
			}
		}
		for id, a := range j.Attempts {
			if !a.Done {
				attempts = append(attempts, id)
			}
		}
	})
	workerOK := map[string]bool{}
	for _, w := range workers {
		empty, err := procid.TerminateVerifiedGroup(w.PGID, w.StartTime, recoverGrace, recoverWait)
		workerOK[w.ID] = err == nil && empty
		if workerOK[w.ID] {
			_ = ag.journal.Append(Record{Kind: RecWorkerExited, WorkerID: w.ID, PID: w.PID, StartTime: w.StartTime, Exit: "reconciled"})
		}
		ag.log("info", "recovery_worker", "worker_id", w.ID, "confirmed", boolStr(workerOK[w.ID]))
	}
	for _, id := range attempts {
		var a JAttempt
		ag.journal.With(func(j *Journal) { a = *j.Attempts[id] })
		state, method := ag.reconcileAttempt(id, workerOK)
		_ = ag.journal.Append(Record{Kind: RecCleanupPending, AttemptID: id, WorkerID: a.WorkerID, Epoch: a.Epoch, CleanupState: state, Method: method, VerifiedAt: time.Now().UTC()})
		_ = ag.journal.Append(Record{Kind: RecAttemptDone, AttemptID: id, Outcome: "abandoned", CleanupState: state})
		ag.log("warn", "recovery_cleanup", "attempt_id", id, "cleanup_state", state)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// reconcileAttempt verifies every journaled process of an attempt. It is
// confirmed only when the worker is verified gone (workerOK), every task
// process group is observed empty, and no intent is open.
func (ag *Agent) reconcileAttempt(id string, workerOK map[string]bool) (string, string) {
	var a JAttempt
	torn, workerExited := false, false
	procs := map[int]JProc{}
	ag.journal.With(func(j *Journal) {
		if p := j.Attempts[id]; p != nil {
			a = *p
			for k, v := range p.Procs {
				procs[k] = *v
			}
		}
		torn = j.Torn
		if w := j.Workers[a.WorkerID]; w != nil && w.Exited && w.PID > 0 {
			workerExited = true
		}
	})
	confirmed := !torn && !a.SourceOpen && !a.Uncertain
	// Positive evidence only: the attempt's worker must be journaled and
	// either recorded as exited or verified gone/killed by this recovery.
	// A missing worker record or identity never confirms.
	if !workerOK[a.WorkerID] && !workerExited {
		confirmed = false
	}
	anyProc := false
	for ord, p := range procs {
		if p.Started {
			anyProc = true
		}
		switch {
		case p.Ended:
			if p.Cleanup == string(executor.CleanupUncertain) {
				confirmed = false
			}
		case !p.Started:
			// task_exec_started without identity: an unidentified child may exist.
			confirmed = false
		case p.Containment != string(executor.ContainmentProcessGroup) || p.PGID <= 1:
			confirmed = false
		default:
			empty, err := procid.TerminateVerifiedGroup(p.PGID, p.StartTime, recoverGrace, recoverWait)
			if err != nil || !empty {
				confirmed = false
				continue
			}
			_ = ag.journal.Append(Record{Kind: RecProcessEnded, AttemptID: id, TaskKey: p.TaskKey, Ordinal: ord, PID: p.PID, StartTime: p.StartTime, Cleanup: string(executor.CleanupConfirmed)})
		}
	}
	method := nodeproto.MethodNoProcessStarted
	if anyProc {
		method = nodeproto.MethodProcessGroupEmpty
	}
	if !confirmed {
		return nodeproto.CleanupUncertain, method
	}
	return nodeproto.CleanupConfirmed, method
}
