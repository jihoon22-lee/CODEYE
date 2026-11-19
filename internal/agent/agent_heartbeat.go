package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

func (ag *Agent) loadOrEnroll(ctx context.Context) error {
	id, err := loadIdentity(ag.cfg.StateDir)
	if err == nil {
		if normalizeURL(id.Controller) != normalizeURL(ag.cfg.ControllerURL) {
			// Never present a node secret to a different controller.
			return &ExitError{ExitUsage, "node.json belongs to a different --controller"}
		}
		ag.ident = id
		ag.client.SetCredentials(id.NodeID, id.NodeSecret)
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return &ExitError{ExitRuntime, err.Error()}
	}
	if ag.cfg.EnrollTokenFile == "" {
		return &ExitError{ExitUsage, "not enrolled: --enroll-token-file is required"}
	}
	raw, err := os.ReadFile(ag.cfg.EnrollTokenFile)
	if err != nil {
		return &ExitError{ExitUsage, "cannot read enrollment token file"}
	}
	token := strings.TrimSpace(string(raw))
	var resp nodeproto.EnrollResponse
	err = retry(ctx, 5, func() error {
		var e error
		resp, e = ag.client.Enroll(ctx, nodeproto.EnrollRequest{EnrollmentToken: token, Name: ag.cfg.Name, ProtocolVersion: nodeproto.ProtocolVersion, OS: runtime.GOOS, Arch: runtime.GOARCH})
		return e
	})
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			return &ExitError{ExitEnroll, "enrollment rejected: " + ae.Code}
		}
		return &ExitError{ExitRuntime, "enrollment failed"}
	}
	ag.ident = &Identity{NodeID: resp.NodeID, NodeSecret: resp.NodeSecret, PoolID: resp.PoolID, HeartbeatSeconds: resp.HeartbeatSeconds, LeaseSeconds: resp.LeaseSeconds, Controller: normalizeURL(ag.cfg.ControllerURL)}
	if err := saveIdentity(ag.cfg.StateDir, ag.ident); err != nil {
		return &ExitError{ExitRuntime, "cannot store node identity"}
	}
	ag.client.SetCredentials(resp.NodeID, resp.NodeSecret)
	ag.log("info", "enrolled", "node_id", resp.NodeID)
	return nil
}

// capabilities: analysis:* only from handlers that ready workers advertised;
// source capabilities only for locally mapped roots / approved Git hosts.
// A draining agent takes no new work, so it advertises no analysis:*: the
// controller then neither counts it as a place for queued runs nor plans
// worker starts on it.
func (ag *Agent) capabilities() []string {
	set := map[string]bool{}
	ag.mu.Lock()
	draining := ag.draining
	for _, h := range ag.probeHandlers {
		if draining {
			break
		}
		if nodeproto.ValidCapability(h.Capability) && strings.HasPrefix(h.Capability, "analysis:") {
			set[h.Capability] = true
		}
	}
	for _, w := range ag.workers {
		if w.draining || draining {
			continue
		}
		for _, h := range w.handlers {
			if nodeproto.ValidCapability(h.Capability) && strings.HasPrefix(h.Capability, "analysis:") {
				set[h.Capability] = true
			}
		}
	}
	ag.mu.Unlock()
	for id := range ag.cfg.SourceRoots {
		set[nodeproto.LocalSourceCapability(id)] = true
	}
	if ag.cfg.ControllerSources {
		set[nodeproto.ControllerSourcesCapability] = true
	}
	if ag.cfg.GitExecutable != "" {
		for _, h := range ag.cfg.GitHosts {
			set[nodeproto.GitSourceCapability(h)] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	if len(out) > nodeproto.MaxCapabilities {
		out = out[:nodeproto.MaxCapabilities]
	}
	return out
}

func (ag *Agent) heartbeat(ctx context.Context) error {
	ag.mu.Lock()
	observed := make([]nodeproto.ObservedWorker, 0, len(ag.workers))
	reportedIdle := map[string]bool{}
	for _, w := range ag.workers {
		state := w.state
		if w.draining && state != nodeproto.WorkerBusy {
			state = nodeproto.WorkerDraining
		}
		ow := nodeproto.ObservedWorker{ID: w.id, State: state}
		if w.att != nil {
			id := w.att.lease.AttemptID
			ow.AttemptID = &id
			ow.State = nodeproto.WorkerBusy
		}
		if ow.State == nodeproto.WorkerIdle {
			reportedIdle[w.id] = true
		}
		observed = append(observed, ow)
	}
	ag.mu.Unlock()
	sort.Slice(observed, func(a, b int) bool { return observed[a].ID < observed[b].ID })
	var reports []nodeproto.CleanupReport
	var last int64
	ag.journal.With(func(j *Journal) {
		last = j.LastSequence
		for _, p := range j.Pending {
			reports = append(reports, nodeproto.CleanupReport{AttemptID: p.AttemptID, WorkerID: p.WorkerID, LeaseEpoch: p.Epoch, CleanupState: p.CleanupState, Method: p.Method, VerifiedAt: p.VerifiedAt})
		}
	})
	sort.Slice(reports, func(a, b int) bool { return reports[a].AttemptID < reports[b].AttemptID })
	mem, disk := hostCapacity(ag.cfg.StateDir)
	req := nodeproto.NodeHeartbeatRequest{ProtocolVersion: nodeproto.ProtocolVersion, ObservedWorkers: observed, Capacity: nodeproto.Capacity{CPUCores: float64(runtime.NumCPU()), MemoryAvailableBytes: mem, DiskAvailableBytes: disk}, Capabilities: ag.capabilities(), LastCommandSequence: last, CleanupReports: reports}
	if req.CleanupReports == nil {
		req.CleanupReports = []nodeproto.CleanupReport{}
	}
	ag.mu.Lock()
	draining := ag.draining
	ag.mu.Unlock()
	if ag.cfg.Autoscale && !draining {
		// A draining agent ignores start_worker; reporting static scaling takes
		// it out of the controller's scaling plan (no starts, and no pending
		// starts holding the pool's budget for StartTimeout).
		req.WorkerScaling, req.MaxWorkers = nodeproto.ScalingController, ag.cfg.MaxWorkers
	}
	req.StorageQuotaExceeded = ag.storageQuotaExceeded()
	hctx, cancel := context.WithTimeout(ctx, ag.heartbeatInterval()*2)
	defer cancel()
	resp, err := ag.client.Heartbeat(hctx, req)
	if err != nil {
		ag.log("warn", "heartbeat_failed", "reply_code", replyCode(err))
		return err
	}
	ag.mu.Lock()
	for _, w := range ag.workers {
		if reportedIdle[w.id] && w.state == nodeproto.WorkerIdle && w.att == nil {
			// The controller now knows this worker idle and may lease to it.
			w.registered = true
		}
	}
	ag.mu.Unlock()
	for _, r := range resp.CleanupResults {
		code := ""
		if r.Code != nil {
			code = *r.Code
		}
		ag.log("info", "cleanup_reported", "attempt_id", r.AttemptID, "accepted", fmt.Sprint(r.Accepted), "reply_code", code)
		if ag.reportSettled(r, code) {
			_ = ag.journal.Append(Record{Kind: RecCleanupAcked, AttemptID: r.AttemptID})
		}
	}
	for _, c := range resp.Commands {
		if err := ag.applyCommand(c); err != nil {
			return err // Leave this command and the rest of the batch unacknowledged.
		}
	}
	ag.kick()
	return nil
}

// reportResendWindow bounds how long a confirmed report refused with a
// retryable code is resent (the attempt is normally still leased until the
// controller's sweep moves it to recovery_required).
const reportResendWindow = 15 * time.Minute

// reportSettled decides whether a cleanup report is finished. Accepted and
// terminal refusals settle it; "conflict" (attempt still leased) and
// "unavailable" are resent on later heartbeats within reportResendWindow.
func (ag *Agent) reportSettled(r nodeproto.CleanupResult, code string) bool {
	if r.Accepted {
		return true
	}
	switch code {
	case "conflict", "unavailable", "":
		var verified time.Time
		ag.journal.With(func(j *Journal) {
			if p := j.Pending[r.AttemptID]; p != nil {
				verified = p.VerifiedAt
			}
		})
		if !verified.IsZero() && time.Since(verified) > reportResendWindow {
			ag.log("error", "cleanup_report_abandoned", "attempt_id", r.AttemptID, "reply_code", code)
			return true
		}
		return false
	}
	// not_found, stale_attempt, invalid, uncertain_not_applied: final.
	return true
}

func (ag *Agent) applyCommand(c nodeproto.Command) error {
	applied := false
	ag.journal.With(func(j *Journal) {
		_, applied = j.Commands[c.CommandID]
		applied = applied || c.Sequence <= j.LastSequence
	})
	if applied {
		return nil
	}
	// The command cursor must be durable before its worker can start.
	// A crash before spawn is recovered by the controller's bounded start timeout.
	if err := ag.journal.Append(Record{Kind: RecCommandApplied, CommandID: c.CommandID, Sequence: c.Sequence}); err != nil {
		ag.log("error", "command_journal_failed", "command_id", c.CommandID, "kind", c.Kind)
		return err
	}
	ag.mu.Lock()
	switch c.Kind {
	case nodeproto.CommandCancelAttempt:
		for _, w := range ag.workers {
			if w.att != nil && c.AttemptID != nil && w.att.lease.AttemptID == *c.AttemptID {
				ag.sendCancelLocked(w, w.att, workerproto.CancelRunRequested)
			}
		}
	case nodeproto.CommandDrainWorker, nodeproto.CommandStopWorker:
		if c.WorkerID != nil {
			if w := ag.workers[*c.WorkerID]; w != nil {
				w.draining = true
				// A delayed stop may reach a worker that is already busy. Only an
				// explicit attempt cancellation is allowed to interrupt that work.
				if w.att == nil {
					go ag.stopWorker(w)
				}
			}
		}
	case nodeproto.CommandStartWorker:
		// Applied once per command ID (journal) and once per worker ID.
		if c.WorkerID != nil && domain.ValidID(*c.WorkerID) && ag.workers[*c.WorkerID] == nil {
			ag.scaler.Requested(*c.WorkerID)
		}
	}
	ag.mu.Unlock()
	ag.log("info", "command_applied", "command_id", c.CommandID, "kind", c.Kind)
	return nil
}

func (ag *Agent) sendCancelLocked(w *workerProc, att *activeAttempt, reason string) {
	att.mu.Lock()
	already := att.cancelSent
	att.cancelSent = true
	att.mu.Unlock()
	if already {
		return
	}
	go func() { _ = w.conn.Send(&workerproto.Cancel{Reason: reason}) }()
}
