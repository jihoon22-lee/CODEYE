package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent/procid"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// workerProc is one supervised worker process.
type workerProc struct {
	id     string
	dir    string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	conn   *workerproto.Conn
	ident  procid.Identity
	exited chan struct{}
	stderr *ringBuffer
	// release is the idempotent platform hook (Windows: close the worker Job).
	release func()

	// guarded by Agent.mu
	state      string
	handlers   []workerproto.HandlerInfo
	registered bool // seen in a successful node heartbeat
	draining   bool
	att        *activeAttempt
	claimSeq   int64
	claimKey   string // reused until a definitive claim answer

	uplink chan any // event/artifact/complete, served in order off the reader
}

// activeAttempt is the attempt a worker currently executes.
type activeAttempt struct {
	lease nodeproto.Lease
	job   nodeproto.Job

	mu          sync.Mutex
	artifactIDs map[string]string // client artifact id → server artifact id
	lastRenew   time.Time         // monotonic, last successful renewal (or claim send)
	cancelSent  bool
	stale       bool
	completed   bool
	stopWatch   context.CancelFunc
}

func (a *activeAttempt) isStale() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stale
}

// ringBuffer keeps the last n bytes of worker stderr (never parsed).
type ringBuffer struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.n {
		r.buf = append([]byte(nil), r.buf[len(r.buf)-r.n:]...)
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// workerEnv is the complete worker environment (contract §6.1): nothing is
// inherited, so no node secret, DSN or token can reach the worker.
func (ag *Agent) workerEnv(dir string) []string {
	tmp := filepath.Join(dir, "tmp")
	env := []string{"HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "LANG=C.UTF-8", "PATH=" + ag.cfg.ToolPath}
	return platformEnv(env)
}

// startWorker spawns and handshakes one worker. It journals worker_started
// (identity read after spawn) before sending hello.
func (ag *Agent) startWorker(ctx context.Context, id string) (*workerProc, error) {
	dir := filepath.Join(ag.cfg.StateDir, "workers", id)
	for _, d := range []string{dir, filepath.Join(dir, "home"), filepath.Join(dir, "tmp"), filepath.Join(dir, "scratch")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	argv := ag.cfg.WorkerCommand
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = ag.workerEnv(dir) // non-nil: never inherit
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	w := &workerProc{id: id, dir: dir, cmd: cmd, stdin: stdin, exited: make(chan struct{}), stderr: &ringBuffer{n: 64 << 10}, state: nodeproto.WorkerStarting, uplink: make(chan any, 64)}
	cmd.Stderr = w.stderr
	release, err := ag.spawner.start(cmd)
	if err != nil {
		release()
		return nil, err
	}
	w.release = release
	pid := cmd.Process.Pid
	w.ident = procid.Identity{PID: pid, PGID: pid}
	if id, err := procid.Read(pid); err == nil {
		w.ident = id
	}
	journalErr := ag.journal.Append(Record{Kind: RecWorkerStarted, WorkerID: id, PID: pid, StartTime: w.ident.StartTime, PGID: w.ident.PGID})
	// Exactly one Wait goroutine per worker; it releases platform resources.
	go func() {
		defer w.release()
		err := cmd.Wait()
		exit := "exited"
		if err != nil {
			exit = err.Error()
		}
		_ = ag.journal.Append(Record{Kind: RecWorkerExited, WorkerID: id, PID: pid, StartTime: w.ident.StartTime, Exit: exit})
		close(w.exited)
	}()
	if journalErr != nil {
		// Never hand a job to an unjournaled worker.
		ag.killWorker(w)
		return nil, journalErr
	}
	w.conn = workerproto.NewConn(stdout, stdin)
	w.conn.Transcript = ag.cfg.Transcript
	w.conn.Start()
	hello := &workerproto.Hello{ModelExecutable: ag.cfg.ModelExecutable, ReviewPack: ag.cfg.ReviewPack, ReviewMaxCalls: ag.cfg.ReviewMaxCalls, ModelName: ag.cfg.ModelName, ModelRelayFile: ag.cfg.ModelRelayFile, Protocol: workerproto.Protocol, WorkerID: id, DataRoot: ag.dataRoot(), ScratchRoot: filepath.Join(dir, "scratch"), Limits: ag.cfg.Limits, GitExecutable: ag.cfg.GitExecutable, GoExecutable: ag.cfg.GoExecutable, GoModCache: ag.cfg.GoModCache, AnalyzerTools: ag.cfg.AnalyzerTools, ToolPath: ag.cfg.ToolPath, LeaseMarginSeconds: int(ag.leaseMargin() / time.Second)}
	hello.AdvertiseWithoutBaseline = ag.cfg.AdvertiseWithoutBaseline
	if err := w.conn.Send(hello); err != nil {
		ag.killWorker(w)
		return nil, err
	}
	timeout := time.After(60 * time.Second) // toolchain probes run before ready
handshake:
	for {
		select {
		case m, ok := <-w.conn.Incoming():
			if l, isLog := m.(*workerproto.Log); ok && isLog {
				ag.log("warn", "worker_log", "worker_id", id, "worker_code", l.Code, "detail", l.Message)
				continue
			}
			r, isReady := m.(*workerproto.Ready)
			if !ok || !isReady || r.Protocol != workerproto.Protocol {
				ag.killWorker(w)
				return nil, errors.New("worker handshake failed")
			}
			w.handlers = r.Handlers
			break handshake
		case <-timeout:
			ag.killWorker(w)
			return nil, errors.New("worker handshake timeout")
		case <-ctx.Done():
			ag.killWorker(w)
			return nil, ctx.Err()
		}
	}
	w.state = nodeproto.WorkerIdle
	go ag.serveUplink(w)
	go ag.readWorker(w)
	return w, nil
}

// killWorker terminates a worker's process group after identity check.
func (ag *Agent) killWorker(w *workerProc) {
	_ = w.stdin.Close()
	select {
	case <-w.exited:
		return
	case <-time.After(3 * time.Second):
	}
	w.release()
	_, _ = procid.TerminateVerifiedGroup(w.ident.PGID, w.ident.StartTime, 2*time.Second, 5*time.Second)
	select {
	case <-w.exited:
	case <-time.After(5 * time.Second):
	}
}

// readWorker handles worker messages. Journal-type messages are answered
// inline (fast, fsync only) so executor OnStarted never waits behind HTTP;
// event/artifact/complete go to the per-worker uplink queue.
func (ag *Agent) readWorker(w *workerProc) {
	defer close(w.uplink)
	for m := range w.conn.Incoming() {
		switch v := m.(type) {
		case *workerproto.Intent:
			ag.replyJournal(w, v.ID, v.AttemptID, v.Epoch, Record{Kind: RecIntent, AttemptID: v.AttemptID, Epoch: v.Epoch, Intent: v.Kind, TaskKey: v.TaskKey, Ordinal: v.Ordinal, Result: v.Result})
		case *workerproto.ProcessStarted:
			ag.replyJournal(w, v.ID, v.AttemptID, v.Epoch, Record{Kind: RecProcessStarted, AttemptID: v.AttemptID, Epoch: v.Epoch, TaskKey: v.TaskKey, Ordinal: v.Ordinal, PID: v.PID, StartTime: v.StartTime, PGID: v.PGID, Containment: v.Containment})
		case *workerproto.ProcessEnded:
			ag.replyJournal(w, v.ID, v.AttemptID, v.Epoch, Record{Kind: RecProcessEnded, AttemptID: v.AttemptID, Epoch: v.Epoch, TaskKey: v.TaskKey, Ordinal: v.Ordinal, PID: v.PID, StartTime: v.StartTime, Cleanup: v.Cleanup})
		case *workerproto.Log:
			ag.log("info", "worker_log", "worker_id", w.id, "worker_code", v.Code)
		case *workerproto.Idle:
			ag.mu.Lock()
			if w.att == nil && w.state == nodeproto.WorkerBusy {
				w.state = nodeproto.WorkerIdle
			}
			// A drained worker exits once its attempt is done (drain).
			drained := w.draining && w.att == nil && !ag.shutting
			ag.mu.Unlock()
			if drained {
				go ag.stopWorker(w)
			}
			// The controller leases again only after a heartbeat reports idle.
			ag.heartbeatSoon()
		case *workerproto.Event, *workerproto.Artifact, *workerproto.Complete:
			w.uplink <- m
		default:
			ag.log("warn", "worker_protocol_error", "worker_id", w.id)
			_ = w.stdin.Close()
		}
	}
	if err := w.conn.Err(); err != nil {
		ag.log("warn", "worker_protocol_error", "worker_id", w.id)
		_ = w.stdin.Close()
	}
}

// replyJournal fsyncs a record for the worker's current attempt and replies.
func (ag *Agent) replyJournal(w *workerProc, msgID, attemptID string, epoch int64, rec Record) {
	att := ag.currentAttempt(w)
	if att == nil || att.lease.AttemptID != attemptID || att.lease.LeaseEpoch != epoch {
		_ = w.conn.ReplyError(msgID, workerproto.ErrInvalid)
		return
	}
	if err := ag.journal.Append(rec); err != nil {
		_ = w.conn.ReplyError(msgID, workerproto.ErrUnavailable)
		return
	}
	_ = w.conn.ReplyOK(msgID, nil)
}

func (ag *Agent) currentAttempt(w *workerProc) *activeAttempt {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	return w.att
}

func (ag *Agent) serveUplink(w *workerProc) {
	for m := range w.uplink {
		switch v := m.(type) {
		case *workerproto.Event:
			ag.forwardEvent(w, v)
		case *workerproto.Artifact:
			ag.forwardArtifact(w, v)
		case *workerproto.Complete:
			ag.forwardComplete(w, v)
		}
	}
}

func replyCode(err error) string {
	var ae *APIError
	switch {
	case IsStale(err):
		return workerproto.ErrStaleAttempt
	case errors.As(err, &ae) && ae.Code == nodeproto.CodeIntegrityMismatch:
		return workerproto.ErrIntegrityMismatch
	case errors.As(err, &ae) && ae.Status == 409:
		return workerproto.ErrConflict
	case errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500:
		return workerproto.ErrInvalid
	}
	return workerproto.ErrUnavailable
}

func (ag *Agent) checkAttempt(w *workerProc, attemptID string, epoch int64) (*activeAttempt, string) {
	att := ag.currentAttempt(w)
	if att == nil || att.lease.AttemptID != attemptID || att.lease.LeaseEpoch != epoch {
		return nil, workerproto.ErrInvalid
	}
	if att.isStale() {
		return nil, workerproto.ErrStaleAttempt
	}
	return att, ""
}

func (ag *Agent) forwardEvent(w *workerProc, v *workerproto.Event) {
	att, code := ag.checkAttempt(w, v.AttemptID, v.Epoch)
	if att == nil {
		_ = w.conn.ReplyError(v.ID, code)
		return
	}
	var payload any = v.Payload
	req := nodeproto.EventRequest{WorkerID: w.id, LeaseEpoch: v.Epoch, ClientEventID: v.ClientEventID, Kind: v.Kind, Payload: payload}
	var raw json.RawMessage
	err := retry(ag.ctx, 6, func() error {
		var e error
		raw, e = ag.client.Event(ag.ctx, v.AttemptID, req)
		return e
	})
	if err != nil {
		ag.noteStale(att, err)
		_ = w.conn.ReplyError(v.ID, replyCode(err))
		return
	}
	if v.Kind == nodeproto.EventSnapshotFixed && v.SnapshotRoot != "" {
		var p nodeproto.SnapshotFixedPayload
		if json.Unmarshal(v.Payload, &p) == nil && withinResolved(ag.dataRoot(), v.SnapshotRoot) {
			if err := ag.cache.put(p.ManifestDigest, v.SnapshotRoot); err != nil {
				ag.log("warn", "snapshot_cache_write_failed", "attempt_id", v.AttemptID)
			}
		}
	}
	_ = w.conn.Send(&workerproto.Reply{Header: workerproto.Header{ReplyTo: v.ID}, OK: true, Result: raw})
}

func (ag *Agent) noteStale(att *activeAttempt, err error) {
	if IsStale(err) {
		ag.leaseLost(att, "stale_attempt")
	}
}

// readArtifact opens path without following a final symlink, requires a
// regular file inside the worker scratch or the snapshot data root, and
// returns its bytes; the hash is computed from these same bytes.
func (ag *Agent) readArtifact(w *workerProc, path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("relative path")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	resolved := filepath.Join(parent, filepath.Base(path))
	ok := false
	for _, root := range []string{filepath.Join(w.dir, "scratch"), ag.dataRoot()} {
		if r, err := filepath.EvalSymlinks(root); err == nil && within(r, resolved) {
			ok = true
		}
	}
	if !ok {
		return nil, errors.New("artifact outside allowed roots")
	}
	f, err := openNoFollow(resolved)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > nodeproto.MaxArtifactBytes {
		return nil, errors.New("artifact too large")
	}
	b, err := io.ReadAll(io.LimitReader(f, nodeproto.MaxArtifactBytes+1))
	if err != nil || int64(len(b)) > nodeproto.MaxArtifactBytes {
		return nil, errors.New("artifact too large")
	}
	return b, nil
}

// withinResolved compares after resolving symlinks on both sides (snapshot acquisition
// returns resolved snapshot roots; the state dir may sit behind a symlink).
func withinResolved(root, p string) bool {
	r, err1 := filepath.EvalSymlinks(root)
	q, err2 := filepath.EvalSymlinks(p)
	return err1 == nil && err2 == nil && within(r, q)
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (ag *Agent) forwardArtifact(w *workerProc, v *workerproto.Artifact) {
	att, code := ag.checkAttempt(w, v.AttemptID, v.Epoch)
	if att == nil {
		_ = w.conn.ReplyError(v.ID, code)
		return
	}
	if !nodeproto.ValidArtifactRole(v.Role) || v.MediaType == "" {
		_ = w.conn.ReplyError(v.ID, workerproto.ErrInvalid)
		return
	}
	data, err := ag.readArtifact(w, v.Path)
	if err != nil {
		ag.log("warn", "artifact_refused", "attempt_id", v.AttemptID)
		_ = w.conn.ReplyError(v.ID, workerproto.ErrInvalid)
		return
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if sha != v.SHA256 || int64(len(data)) != v.SizeBytes {
		_ = w.conn.ReplyError(v.ID, workerproto.ErrIntegrityMismatch)
		return
	}
	var resp nodeproto.ArtifactResponse
	err = retry(ag.ctx, 6, func() error {
		var e error
		resp, e = ag.client.PutArtifact(ag.ctx, v.AttemptID, v.ClientArtifactID, w.id, v.Epoch, v.Role, v.MediaType, sha, data)
		return e
	})
	if err != nil {
		ag.noteStale(att, err)
		_ = w.conn.ReplyError(v.ID, replyCode(err))
		return
	}
	att.mu.Lock()
	att.artifactIDs[v.ClientArtifactID] = resp.ArtifactID
	att.mu.Unlock()
	_ = w.conn.ReplyOK(v.ID, map[string]string{"artifact_id": resp.ArtifactID})
}

// forwardComplete posts complete and records the real cleanup result. If the
// controller rejects it (lease lost) the cleanup is reported separately.
func (ag *Agent) forwardComplete(w *workerProc, v *workerproto.Complete) {
	att := ag.currentAttempt(w)
	if att == nil || att.lease.AttemptID != v.AttemptID || att.lease.LeaseEpoch != v.Epoch {
		_ = w.conn.ReplyError(v.ID, workerproto.ErrInvalid)
		return
	}
	att.stopWatch()
	cleanup := v.CleanupState
	if cleanup != nodeproto.CleanupConfirmed {
		cleanup = nodeproto.CleanupUncertain
	}
	// The journal is the second opinion: an open intent or an uncertain
	// process end overrides a worker claim of confirmed.
	if cleanup == nodeproto.CleanupConfirmed && ag.journalUncertain(v.AttemptID) {
		cleanup = nodeproto.CleanupUncertain
	}
	req := nodeproto.CompleteRequest{WorkerID: w.id, LeaseEpoch: v.Epoch, Outcome: v.Outcome, CleanupState: cleanup, Reason: v.Reason}
	if v.ResultManifestClientArtifact != nil {
		att.mu.Lock()
		id := att.artifactIDs[*v.ResultManifestClientArtifact]
		att.mu.Unlock()
		if id != "" {
			req.ResultManifestID = &id
		}
	}
	// Always try: the local lease-margin watchdog may fire while the server
	// lease is still valid; the controller decides staleness (409).
	err := retry(ag.ctx, 6, func() error {
		_, e := ag.client.Complete(ag.ctx, v.AttemptID, req)
		return e
	})
	outcome := v.Outcome
	if err != nil {
		outcome = "rejected"
		ag.log("warn", "complete_rejected", "attempt_id", v.AttemptID, "reply_code", replyCode(err))
		// Report the real cleanup through the heartbeat (stale lease path).
		method := v.CleanupMethod
		if method == "" {
			method = nodeproto.MethodProcessGroupEmpty
		}
		_ = ag.journal.Append(Record{Kind: RecCleanupPending, AttemptID: v.AttemptID, WorkerID: w.id, Epoch: v.Epoch, CleanupState: cleanup, Method: method, VerifiedAt: time.Now().UTC()})
	}
	_ = ag.journal.Append(Record{Kind: RecAttemptDone, AttemptID: v.AttemptID, Outcome: outcome, CleanupState: cleanup})
	att.mu.Lock()
	att.completed = true
	att.mu.Unlock()
	ag.mu.Lock()
	w.att = nil // state stays busy until the worker's own idle message
	ag.mu.Unlock()
	ag.log("info", "attempt_completed", "attempt_id", v.AttemptID, "outcome", outcome, "cleanup_state", cleanup)
	if err != nil {
		_ = w.conn.ReplyError(v.ID, replyCode(err))
	} else {
		_ = w.conn.ReplyOK(v.ID, nil)
	}
	// The controller leases again only after a heartbeat reports it idle.
	ag.heartbeatSoon()
}

func (ag *Agent) journalUncertain(attemptID string) bool {
	uncertain := false
	ag.journal.With(func(j *Journal) {
		a := j.Attempts[attemptID]
		if a == nil {
			return
		}
		if a.SourceOpen || a.Uncertain || j.Torn {
			uncertain = true
			return
		}
		for _, p := range a.Procs {
			if !p.Ended {
				uncertain = true
			}
		}
	})
	return uncertain
}
