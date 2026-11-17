// Package agent is the node agent: it enrolls with the controller, keeps
// node heartbeats, supervises worker processes, claims work, renews leases
// with a monotonic watchdog, forwards worker events/artifacts, journals every
// process it may own, and reconciles that journal after a restart. It never
// touches the database and is the only holder of the node secret.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// Exit codes of `codeye agent`.
const (
	ExitOK        = 0
	ExitRuntime   = 1
	ExitUsage     = 2
	ExitEnroll    = 3
	ExitRevoked   = 4
	maxWorkers    = 16
	defaultMargin = 10 * time.Second
)

// ExitError carries a process exit code.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Config of one agent.
type Config struct {
	ControllerURL   string
	StateDir        string
	Name            string
	EnrollTokenFile string
	// SourceRoots is the private mapping source_root_id → absolute path.
	SourceRoots map[string]string
	// Workers is the static worker count (W06). A W07 scaler replaces it.
	Workers       int
	ToolPath      string
	GitExecutable string
	GitHosts      []string
	GoExecutable  string
	GoModCache    string
	Limits        workerproto.Limits
	// WorkerCommand is the worker argv (default: this executable + "worker").
	WorkerCommand []string
	Log           io.Writer
	// Transcript, if set, records all worker IPC (tests).
	Transcript io.Writer

	// Timing overrides (tests); zero uses the controller's values.
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	LeaseMargin       time.Duration
	ShutdownTimeout   time.Duration
}

// Agent is one running node agent.
type Agent struct {
	cfg     Config
	client  *Client
	journal *Journal
	cache   *snapshotCache
	spawner *spawner
	ident   *Identity
	scaler  Scaler
	boot    string

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	workers   map[string]*workerProc
	shutting  bool
	starting  int
	kickCh    chan struct{}
	hbNow     chan struct{}
	logMu     sync.Mutex
	failures  int
	lastStart time.Time
}

// New validates cfg.
func New(cfg Config) (*Agent, error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, &ExitError{ExitUsage, "--state-dir must be an absolute path"}
	}
	if cfg.Workers < 0 || cfg.Workers > maxWorkers {
		return nil, &ExitError{ExitUsage, "--workers must be 0..16"}
	}
	for id, p := range cfg.SourceRoots {
		if !domain.ValidID(id) || !filepath.IsAbs(p) {
			return nil, &ExitError{ExitUsage, "--source-root must be UUID=/absolute/path"}
		}
	}
	for _, h := range cfg.GitHosts {
		if domain.ValidateHost(strings.ToLower(h)) != nil {
			return nil, &ExitError{ExitUsage, "invalid --git-host"}
		}
	}
	for _, p := range []string{cfg.GitExecutable, cfg.GoExecutable, cfg.GoModCache} {
		if p != "" && !filepath.IsAbs(p) {
			return nil, &ExitError{ExitUsage, "tool paths must be absolute"}
		}
	}
	if cfg.ToolPath == "" {
		cfg.ToolPath = "/usr/bin:/bin"
	}
	if len(cfg.WorkerCommand) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cfg.WorkerCommand = []string{exe, "worker"}
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
	}
	client, err := NewClient(cfg.ControllerURL)
	if err != nil {
		return nil, &ExitError{ExitUsage, err.Error()}
	}
	return &Agent{cfg: cfg, client: client, workers: map[string]*workerProc{}, kickCh: make(chan struct{}, 1), hbNow: make(chan struct{}, 1), boot: domain.NewID()[:8], scaler: &StaticScaler{N: cfg.Workers}}, nil
}

func (ag *Agent) dataRoot() string { return filepath.Join(ag.cfg.StateDir, "data") }

func (ag *Agent) heartbeatInterval() time.Duration {
	if ag.cfg.HeartbeatInterval > 0 {
		return ag.cfg.HeartbeatInterval
	}
	if ag.ident != nil && ag.ident.HeartbeatSeconds > 0 {
		return time.Duration(ag.ident.HeartbeatSeconds) * time.Second
	}
	return nodeproto.HeartbeatSeconds * time.Second
}

func (ag *Agent) leaseDuration() time.Duration {
	if ag.cfg.LeaseDuration > 0 {
		return ag.cfg.LeaseDuration
	}
	if ag.ident != nil && ag.ident.LeaseSeconds > 0 {
		return time.Duration(ag.ident.LeaseSeconds) * time.Second
	}
	return nodeproto.LeaseSeconds * time.Second
}

func (ag *Agent) leaseMargin() time.Duration {
	if ag.cfg.LeaseMargin > 0 {
		return ag.cfg.LeaseMargin
	}
	return defaultMargin
}

// log writes one JSON line: code plus key/value pairs. Never pass secrets.
func (ag *Agent) log(level, code string, kv ...string) {
	m := map[string]string{"time": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "code": code}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	ag.logMu.Lock()
	defer ag.logMu.Unlock()
	_, _ = ag.cfg.Log.Write(append(b, '\n'))
}

func (ag *Agent) kick() {
	select {
	case ag.kickCh <- struct{}{}:
	default:
	}
}

func (ag *Agent) heartbeatSoon() {
	select {
	case ag.hbNow <- struct{}{}:
	default:
	}
}

// Run executes the agent until ctx ends or the node is revoked.
func (ag *Agent) Run(ctx context.Context) error {
	if err := os.MkdirAll(ag.cfg.StateDir, 0700); err != nil {
		return &ExitError{ExitRuntime, "cannot create state dir"}
	}
	_ = os.Chmod(ag.cfg.StateDir, 0700)
	for _, d := range []string{ag.dataRoot(), filepath.Join(ag.cfg.StateDir, "workers")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return &ExitError{ExitRuntime, "cannot create state dir"}
		}
	}
	// Exclusive ownership before reading or writing anything else in the
	// state dir: a second agent fails without touching journal or identity.
	unlock, err := lockStateDir(ag.cfg.StateDir)
	if err != nil {
		return &ExitError{ExitRuntime, "state dir is in use by another agent"}
	}
	defer unlock()
	if err := ag.loadOrEnroll(ctx); err != nil {
		return err
	}
	var j *Journal
	j, err = OpenJournal(filepath.Join(ag.cfg.StateDir, "journal.jsonl"))
	if err != nil {
		return &ExitError{ExitRuntime, "cannot open journal"}
	}
	ag.journal = j
	defer j.Close()
	if ag.cache, err = openSnapshotCache(filepath.Join(ag.cfg.StateDir, "snapshots.jsonl")); err != nil {
		return &ExitError{ExitRuntime, "cannot open snapshot cache"}
	}
	// Reconcile before the first heartbeat: unfinished attempts become
	// cleanup reports; they are never re-run.
	ag.reconcileStartup()
	if err := j.Compact(); err != nil {
		ag.log("warn", "journal_compact_failed")
	}
	ag.spawner = newSpawner()
	ag.ctx, ag.cancel = context.WithCancel(context.Background())
	defer ag.cancel()

	revoked := false
	if err := ag.heartbeat(ctx); IsUnauthenticated(err) {
		revoked = true
	}
	hb := time.NewTicker(ag.heartbeatInterval())
	defer hb.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	for !revoked {
		ag.reconcileWorkers(ctx)
		ag.claimIdle()
		select {
		case <-ctx.Done():
			return ag.shutdown()
		case <-hb.C:
			if err := ag.heartbeat(ctx); IsUnauthenticated(err) {
				revoked = true
			}
		case <-ag.hbNow:
			if err := ag.heartbeat(ctx); IsUnauthenticated(err) {
				revoked = true
			}
		case <-ag.kickCh:
		case <-poll.C:
		}
	}
	ag.log("error", "node_revoked")
	_ = ag.shutdown()
	return &ExitError{ExitRevoked, "node revoked"}
}

var errStateLocked = errors.New("state dir locked")

// normalizeURL is the identity binding key of a controller URL.
func normalizeURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

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
func (ag *Agent) capabilities() []string {
	set := map[string]bool{}
	ag.mu.Lock()
	for _, w := range ag.workers {
		if w.draining {
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
	hctx, cancel := context.WithTimeout(ctx, ag.heartbeatInterval()*2)
	defer cancel()
	resp, err := ag.client.Heartbeat(hctx, req)
	if err != nil {
		ag.log("warn", "heartbeat_failed", "code", replyCode(err))
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
		ag.log("info", "cleanup_reported", "attempt_id", r.AttemptID, "accepted", fmt.Sprint(r.Accepted), "code", code)
		_ = ag.journal.Append(Record{Kind: RecCleanupAcked, AttemptID: r.AttemptID})
	}
	for _, c := range resp.Commands {
		ag.applyCommand(c)
	}
	ag.kick()
	return nil
}

func (ag *Agent) applyCommand(c nodeproto.Command) {
	applied := false
	ag.journal.With(func(j *Journal) { _, applied = j.Commands[c.CommandID] })
	if applied {
		return
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
				if c.Kind == nodeproto.CommandStopWorker && w.att != nil {
					ag.sendCancelLocked(w, w.att, workerproto.CancelShutdown)
				}
				if w.att == nil {
					go ag.stopWorker(w)
				}
			}
		}
	case nodeproto.CommandStartWorker:
		if c.WorkerID != nil && domain.ValidID(*c.WorkerID) {
			ag.scaler.Requested(*c.WorkerID)
		}
	}
	ag.mu.Unlock()
	_ = ag.journal.Append(Record{Kind: RecCommandApplied, CommandID: c.CommandID, Sequence: c.Sequence})
	ag.log("info", "command_applied", "command_id", c.CommandID, "kind", c.Kind)
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
		rctx, cancel := context.WithTimeout(ctx, interval)
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
		w.claimSeq++
		go ag.claimFor(w, fmt.Sprintf("%s:%s-%d", w.id, ag.boot, w.claimSeq))
	}
}

func (ag *Agent) claimFor(w *workerProc, key string) {
	sent := time.Now()
	var resp *nodeproto.ClaimResponse
	err := retry(ag.ctx, 5, func() error {
		var e error
		resp, e = ag.client.Claim(ag.ctx, w.id, key)
		return e
	})
	if err != nil || resp == nil {
		if err != nil {
			ag.log("warn", "claim_failed", "worker_id", w.id, "code", replyCode(err))
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

// reconcileWorkers keeps the scaler's desired worker count.
func (ag *Agent) reconcileWorkers(ctx context.Context) {
	ag.mu.Lock()
	if ag.shutting || ag.starting > 0 {
		ag.mu.Unlock()
		return
	}
	alive := 0
	for _, w := range ag.workers {
		if !w.draining {
			alive++
		}
	}
	ids := ag.scaler.Plan(alive)
	if len(ids) == 0 || time.Since(ag.lastStart) < ag.backoff() {
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
			shutting := ag.shutting
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
	err := error(&APIError{Status: 409, Code: nodeproto.CodeStaleAttempt})
	if !att.isStale() {
		err = retry(context.Background(), 4, func() error {
			_, e := ag.client.Complete(context.Background(), att.lease.AttemptID, req)
			return e
		})
	}
	outcome := nodeproto.OutcomeFailed
	if err != nil {
		outcome = "rejected"
		_ = ag.journal.Append(Record{Kind: RecCleanupPending, AttemptID: att.lease.AttemptID, WorkerID: w.id, Epoch: att.lease.LeaseEpoch, CleanupState: state, Method: method, VerifiedAt: time.Now().UTC()})
	}
	_ = ag.journal.Append(Record{Kind: RecAttemptDone, AttemptID: att.lease.AttemptID, Outcome: outcome, CleanupState: state})
	ag.log("warn", "attempt_worker_lost", "attempt_id", att.lease.AttemptID, "cleanup_state", state)
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
