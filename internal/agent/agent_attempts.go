package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// leaseLost: stop new tasks and cancel; the worker's complete will be
// rejected as stale and its real cleanup is reported via the heartbeat.
func (ag *Agent) leaseLost(att *activeAttempt, why string) {
	att.mu.Lock()
	if att.stale {
		att.mu.Unlock()
		return
	}
	att.stale = true
	att.mu.Unlock()
	ag.log("warn", "lease_lost", "attempt_id", att.lease.AttemptID, "why", why)
	ag.mu.Lock()
	defer ag.mu.Unlock()
	for _, w := range ag.workers {
		if w.att == att {
			go func() { _ = w.conn.Send(&workerproto.StopNewTasks{Reason: "lease_margin"}) }()
			ag.sendCancelLocked(w, att, workerproto.CancelLeaseLost)
		}
	}
}

// watch renews the lease; a monotonic deadline of lease−margin since the
// last successful renewal triggers lease loss even if the network hangs.
func (ag *Agent) watch(ctx context.Context, w *workerProc, att *activeAttempt) {
	interval := ag.heartbeatInterval()
	limit := ag.leaseDuration() - ag.leaseMargin()
	t := time.NewTicker(interval / 2)
	defer t.Stop()
	nextRenew := time.Now().Add(interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		att.mu.Lock()
		elapsed := time.Since(att.lastRenew)
		att.mu.Unlock()
		if elapsed >= limit {
			ag.leaseLost(att, "lease_margin")
			return
		}
		if time.Now().Before(nextRenew) {
			continue
		}
		nextRenew = time.Now().Add(interval)
		sent := time.Now()
		timeout := interval
		if left := limit - elapsed; left < timeout {
			timeout = left // never let a hung renewal overshoot lease − margin
		}
		rctx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := ag.client.AttemptHeartbeat(rctx, att.lease.AttemptID, nodeproto.AttemptHeartbeatRequest{WorkerID: w.id, LeaseEpoch: att.lease.LeaseEpoch})
		cancel()
		if ctx.Err() != nil {
			return
		}
		switch {
		case err == nil:
			att.mu.Lock()
			att.lastRenew = sent
			att.mu.Unlock()
			if resp.CancelRequested {
				ag.mu.Lock()
				ag.sendCancelLocked(w, att, workerproto.CancelRunRequested)
				ag.mu.Unlock()
			}
		case IsStale(err):
			ag.leaseLost(att, "stale_attempt")
			return
		}
	}
}

// claimIdle starts a claim for each idle, controller-registered worker that
// advertised at least one handler. A worker without handlers never claims.
func (ag *Agent) claimIdle() {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if ag.shutting {
		return
	}
	for _, w := range ag.workers {
		if w.state != nodeproto.WorkerIdle || w.att != nil || w.draining || !w.registered || len(w.handlers) == 0 {
			continue
		}
		w.registered = false // one claim per idle report; re-armed by the next heartbeat
		ag.claiming++
		if w.claimKey == "" {
			// A key without a definitive answer is reused, so a claim that
			// committed but whose response was lost is replayed, not leaked.
			w.claimSeq++
			w.claimKey = fmt.Sprintf("%s:%s-%d", w.id, ag.boot, w.claimSeq)
		}
		go ag.claimFor(w, w.claimKey)
	}
}

func (ag *Agent) claimFor(w *workerProc, key string) {
	defer func() {
		ag.mu.Lock()
		ag.claiming--
		ag.mu.Unlock()
		ag.kick()
	}()
	sent := time.Now()
	var resp *nodeproto.ClaimResponse
	err := retry(ag.ctx, 5, func() error {
		var e error
		resp, e = ag.client.Claim(ag.ctx, w.id, key)
		return e
	})
	var ae *APIError
	if err == nil || errors.As(err, &ae) {
		// Definitive HTTP answer (200/204/4xx/5xx): the key is consumed.
		ag.mu.Lock()
		w.claimKey = ""
		ag.mu.Unlock()
	}
	if err != nil || resp == nil {
		if err != nil {
			ag.log("warn", "claim_failed", "worker_id", w.id, "reply_code", replyCode(err))
		}
		// Re-armed by the next heartbeat that reports the worker idle.
		return
	}
	l := resp.Lease
	if l.WorkerID != w.id || !domain.ValidID(l.AttemptID) {
		ag.log("error", "claim_mismatch", "worker_id", w.id)
		return
	}
	att := &activeAttempt{lease: l, job: resp.Job, artifactIDs: map[string]string{}, lastRenew: sent}
	if err := ag.journal.Append(Record{Kind: RecAttemptAssigned, AttemptID: l.AttemptID, WorkerID: w.id, RunID: l.RunID, Epoch: l.LeaseEpoch}); err != nil {
		// Cannot journal: do not dispatch. Nothing ran, cleanup is confirmed.
		reason := "journal_unavailable"
		_, _ = ag.client.Complete(ag.ctx, l.AttemptID, nodeproto.CompleteRequest{WorkerID: w.id, LeaseEpoch: l.LeaseEpoch, Outcome: nodeproto.OutcomeFailed, CleanupState: nodeproto.CleanupConfirmed, Reason: &reason})
		return
	}
	wctx, stop := context.WithCancel(ag.ctx)
	att.stopWatch = stop
	ag.mu.Lock()
	w.att = att
	w.state = nodeproto.WorkerBusy
	ag.mu.Unlock()
	ag.log("info", "attempt_claimed", "attempt_id", l.AttemptID, "worker_id", w.id, "run_id", l.RunID)
	go ag.watch(wctx, w, att)
	if err := w.conn.Send(ag.buildJob(att)); err != nil {
		ag.log("warn", "job_dispatch_failed", "attempt_id", l.AttemptID)
		_ = w.stdin.Close() // worker exit handling reconciles and completes
	}
}

// buildJob resolves the job source from the agent's private configuration.
func (ag *Agent) buildJob(att *activeAttempt) *workerproto.Job {
	l, j := att.lease, att.job
	scope, _ := json.Marshal(j.Scope)
	res, _ := json.Marshal(j.Resources)
	job := &workerproto.Job{Attempt: workerproto.AttemptRef{ID: l.AttemptID, RunID: l.RunID, Epoch: l.LeaseEpoch}, Profile: j.Profile, Scope: scope, Resources: res, RunVersion: l.RunVersion, Source: workerproto.JobSource{Kind: j.Source.Kind}}
	switch j.Source.Kind {
	case "local":
		job.AllowModelReview = ag.cfg.ModelSourceRoots[j.Source.SourceRootID]
		job.AllowRepositoryCode = ag.cfg.RepositoryCodeSourceRoots[j.Source.SourceRootID]
		job.Source.ApprovedRoot = ag.cfg.SourceRoots[j.Source.SourceRootID]
		job.Source.RelativePath = j.Source.RelativePath
	case "git":
		job.Source.RepositoryURL, job.Source.Branch = j.Source.RepositoryURL, j.Source.Branch
		for _, h := range ag.cfg.GitHosts {
			if strings.EqualFold(h, j.Source.Host) {
				job.Source.AllowedHost = strings.ToLower(h)
			}
		}
	}
	if j.Snapshot != nil {
		job.Snapshot = &workerproto.JobSnapshot{ID: j.Snapshot.ID, ManifestDigest: j.Snapshot.ManifestDigest, Root: ag.cache.get(j.Snapshot.ManifestDigest)}
	}
	return job
}
