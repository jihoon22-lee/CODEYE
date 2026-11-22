package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// errAborted: the attempt cannot continue (lease lost, cancel, agent gone).
var errAborted = errors.New("attempt_aborted")

type attempt struct {
	job       *workerproto.Job
	ctx       context.Context
	cancelCtx context.CancelFunc

	reasonMu sync.Mutex
	reason   string // cancel reason, "" while not cancelled

	stopNew   atomic.Bool
	stale     atomic.Bool
	ordinal   atomic.Int64
	uncertain atomic.Bool
	anyProc   atomic.Bool

	snapshot   source.Snapshot
	snapshotID string
	scratch    string
	workspace  string // attempt workspace to remove after the attempt
	// guard restores the workspace to the snapshot after every task; nil
	// when it could not record the clone (tasks then check drift alone).
	guard *source.WorkspaceGuard

	recMu    sync.Mutex
	recorded map[string][]executor.Result // task key → runner results

	manifestClientID string
}

func newAttempt(job *workerproto.Job) *attempt {
	ctx, cancel := context.WithCancel(context.Background())
	return &attempt{job: job, ctx: ctx, cancelCtx: cancel, recorded: map[string][]executor.Result{}}
}

func (a *attempt) cancel(reason string) {
	a.reasonMu.Lock()
	if a.reason == "" {
		a.reason = reason
	}
	a.reasonMu.Unlock()
	a.cancelCtx()
}

func (a *attempt) cancelReason() string {
	a.reasonMu.Lock()
	defer a.reasonMu.Unlock()
	return a.reason
}

func (a *attempt) ref() (string, int64) { return a.job.Attempt.ID, a.job.Attempt.Epoch }

// request sends a message for the current attempt and waits for its reply.
// A stale_attempt reply cancels the attempt (lease lost).
func (w *worker) request(a *attempt, m any, timeout time.Duration, result any) error {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	r, err := w.conn.Request(ctx, m)
	if err != nil {
		return err
	}
	err = r.Check(result)
	var re *workerproto.ReplyErr
	if errors.As(err, &re) && re.Code == workerproto.ErrStaleAttempt {
		a.stale.Store(true)
		a.cancel(workerproto.CancelLeaseLost)
	}
	return err
}

func (w *worker) intent(a *attempt, kind, task string, ordinal int, result string) error {
	id, epoch := a.ref()
	return w.request(a, &workerproto.Intent{AttemptID: id, Epoch: epoch, Kind: kind, TaskKey: task, Ordinal: ordinal, Result: result}, w.cfg.ReplyTimeout, nil)
}

// event posts one node event through the agent. Events are not sent after a
// cancel (§10) — only complete is.
func (w *worker) event(a *attempt, kind string, payload any, snapshotRoot string) (nodeproto.EventResponse, error) {
	var resp nodeproto.EventResponse
	if a.ctx.Err() != nil {
		return resp, errAborted
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return resp, err
	}
	id, epoch := a.ref()
	err = w.request(a, &workerproto.Event{AttemptID: id, Epoch: epoch, ClientEventID: domain.NewID(), Kind: kind, Payload: b, SnapshotRoot: snapshotRoot}, 0, &resp)
	return resp, err
}

// upload hashes a file and asks the agent to upload it. The agent re-hashes
// from the bytes it sends; this hash is the integrity expectation.
func (w *worker) upload(a *attempt, path, role, media string) (string, error) {
	return w.uploadID(a, domain.NewID(), path, role, media)
}

func (w *worker) uploadID(a *attempt, clientID, path, role, media string) (string, error) {
	if a.ctx.Err() != nil && role != nodeproto.RoleResultManifest {
		return "", errAborted
	}
	if !w.allowedPath(a, path) {
		return "", fmt.Errorf("artifact path outside attempt roots")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return "", err
	}
	id, epoch := a.ref()
	var res struct {
		ArtifactID string `json:"artifact_id"`
	}
	err = w.request(a, &workerproto.Artifact{AttemptID: id, Epoch: epoch, ClientArtifactID: clientID, Path: path, Role: role, MediaType: media, SHA256: hex.EncodeToString(h.Sum(nil)), SizeBytes: n}, 0, &res)
	if err == nil && res.ArtifactID == "" {
		err = errors.New("artifact reply without id")
	}
	return res.ArtifactID, err
}

// allowedPath: a regular, non-symlink file inside the attempt scratch or the
// snapshot root.
func (w *worker) allowedPath(a *attempt, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for _, root := range []string{a.scratch, a.snapshot.Root} {
		if root == "" {
			continue
		}
		r, err := filepath.EvalSymlinks(root)
		if err == nil && within(r, resolved) {
			return true
		}
	}
	return false
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// sourceReason maps source acquisition errors to reason codes 1:1 by their text.
func sourceReason(err error) string {
	for _, e := range []error{source.ErrHoldsProtected, source.ErrAuthRequired, source.ErrUnreachable, source.ErrUnavailable, source.ErrUnstable, source.ErrLimit, source.ErrUnsupported, source.ErrIntegrity} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return nodeproto.ReasonSourceUnavailable
}

type taskRun struct {
	spec     analysis.TaskSpec
	taskID   string
	version  int64
	status   domain.TaskStatus
	reason   string
	result   analysis.TaskResult
	manifest analysis.ManifestTask
	started  bool
}

// runJob executes one attempt end to end and always sends complete.
func (w *worker) runJob(parent context.Context, a *attempt) {
	stopParent := context.AfterFunc(parent, func() { a.cancel(workerproto.CancelShutdown) })
	defer stopParent()
	defer a.cancelCtx()
	outcome, reason, manifestID := w.execute(a)
	cleanup, method := w.cleanupState(a)
	// Only a user cancel ends as cancelled; lease loss and agent shutdown are
	// failures with their own reason (lease_lost / agent_shutdown).
	switch r := a.cancelReason(); r {
	case "":
	case workerproto.CancelRunRequested:
		outcome, reason, manifestID = nodeproto.OutcomeCancelled, nodeproto.ReasonCancelled, nil
	default:
		outcome, reason, manifestID = nodeproto.OutcomeFailed, r, nil
	}
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	if cleanup == nodeproto.CleanupConfirmed {
		// All uploads are done. Keep the snapshot (retries Load it); drop the
		// attempt-private copies. Uncertain cleanup keeps them for inspection.
		if a.workspace != "" {
			_ = source.Remove(a.workspace)
		}
		if a.scratch != "" {
			_ = source.Remove(a.scratch)
		}
	}
	id, epoch := a.ref()
	_ = w.request(a, &workerproto.Complete{AttemptID: id, Epoch: epoch, Outcome: outcome, CleanupState: cleanup, ResultManifestClientArtifact: manifestID, Reason: reasonPtr, CleanupMethod: method}, 0, nil)
}

func (w *worker) cleanupState(a *attempt) (string, string) {
	if a.uncertain.Load() {
		return nodeproto.CleanupUncertain, ""
	}
	a.recMu.Lock()
	defer a.recMu.Unlock()
	for _, rs := range a.recorded {
		for _, r := range rs {
			if r.Cleanup == executor.CleanupUncertain {
				return nodeproto.CleanupUncertain, ""
			}
		}
	}
	if !a.anyProc.Load() {
		return nodeproto.CleanupConfirmed, nodeproto.MethodNoProcessStarted
	}
	if runtime.GOOS == "windows" {
		return nodeproto.CleanupConfirmed, nodeproto.MethodJobObjectClosed
	}
	return nodeproto.CleanupConfirmed, nodeproto.MethodProcessGroupEmpty
}

// execute returns outcome, reason and the result-manifest client artifact id.
func (w *worker) execute(a *attempt) (string, string, *string) {
	job := a.job
	id := job.Attempt.ID
	if !domain.ValidID(id) {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}
	a.scratch = filepath.Join(w.hello.ScratchRoot, id)
	if err := os.MkdirAll(w.hello.ScratchRoot, 0700); err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}
	if err := os.Mkdir(a.scratch, 0700); err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}
	_ = os.Mkdir(filepath.Join(a.scratch, "tmp"), 0700)
	if w.cfg.RefreshHandlers != nil {
		registry, err := w.cfg.RefreshHandlers(a.ctx, w.runner)
		if err != nil {
			w.log("warn", "runtime_settings_unavailable", "")
			return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
		}
		if registry != nil {
			w.registry = registry
		}
	}
	if w.cfg.PrepareAttempt != nil {
		job.Source.ProtectedDirs = append(job.Source.ProtectedDirs, w.cfg.PrepareAttempt(a.scratch)...)
	}

	if reason := w.acquire(a); reason != "" {
		return nodeproto.OutcomeFailed, reason, nil
	}
	manifestArtifact, err := w.upload(a, a.snapshot.ManifestPath, nodeproto.RoleSourceManifest, "application/vnd.codeye.source-manifest+json")
	if err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonArtifactUploadFailed, nil
	}
	m := a.snapshot.Manifest
	var sha *string
	if m.SourceSHA != "" {
		s := m.SourceSHA
		sha = &s
	}
	resp, err := w.event(a, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: a.snapshot.Digest, Kind: m.Kind, Consistency: m.Consistency, SourceSHA: sha, ManifestArtifactID: manifestArtifact}, a.snapshot.Root)
	if err != nil || resp.SnapshotID == nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonSnapshotUnavailable, nil
	}
	a.snapshotID = *resp.SnapshotID
	if g, err := source.NewWorkspaceGuard(a.snapshot, a.snapshot.WorkspaceDir, pack.DataDir); err == nil {
		a.guard = g
	}

	handler, ok := w.registry.Lookup(nodeproto.AnalysisCapability(job.Profile))
	if !ok {
		return nodeproto.OutcomeFailed, nodeproto.ReasonAnalysisUnsupported, nil
	}
	var scope domain.Scope
	if len(job.Scope) > 0 {
		_ = json.Unmarshal(job.Scope, &scope)
	}
	ajob := analysis.Job{AllowModelReview: job.AllowModelReview, AllowRepositoryCode: job.AllowRepositoryCode, ModelReviewWithheld: job.ModelReviewWithheld, RepositoryCodeWithheld: job.RepositoryCodeWithheld, AllowDependencyFetch: job.AllowDependencyFetch, RunID: job.Attempt.RunID, AttemptID: id, Epoch: job.Attempt.Epoch, Profile: job.Profile, Scope: scope, Snapshot: a.snapshot, Scratch: a.scratch, PullRequest: job.PullRequest}
	if job.Source.Kind == "local" && job.Source.ApprovedRoot != "" {
		ajob.SourceDir = filepath.Join(job.Source.ApprovedRoot, filepath.FromSlash(job.Source.RelativePath))
	}
	taskContext := review.WithAttemptSourceIndex(a.ctx, ajob)
	specs, err := safePlan(taskContext, handler, ajob)
	if err != nil || len(specs) == 0 {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}
	seen := map[string]bool{}
	for _, s := range specs {
		// An unknown stage is a handler bug; "" is sent as no stage (not recorded).
		if !nodeproto.ValidTaskKey(s.Key) || seen[s.Key] || (s.Stage != "" && !s.Stage.Valid()) || s.Weight < 0 || s.Weight > 1000 {
			return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
		}
		seen[s.Key] = true
	}
	tasks := make([]*taskRun, 0, len(specs))
	for _, s := range specs {
		payload := nodeproto.TaskCreatedPayload{Key: s.Key, Required: s.Required}
		if s.Stage != "" {
			stage := string(s.Stage)
			payload.Stage = &stage
		}
		if s.Weight > 0 {
			weight := s.Weight
			payload.Weight = &weight
		}
		resp, err := w.event(a, nodeproto.EventTaskCreated, payload, "")
		if err != nil || resp.TaskID == nil {
			return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
		}
		v := int64(1)
		if resp.Version != nil {
			v = *resp.Version
		}
		tasks = append(tasks, &taskRun{spec: s, taskID: *resp.TaskID, version: v, status: domain.TaskQueued})
	}
	if _, err := w.event(a, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: string(domain.RunRunning)}, ""); err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}

	for _, t := range tasks {
		if a.ctx.Err() != nil || a.stopNew.Load() {
			break
		}
		w.runTask(taskContext, a, handler, ajob, t)
		collectTestEvidence(&ajob, t)
	}
	if a.ctx.Err() != nil {
		return nodeproto.OutcomeCancelled, nodeproto.ReasonCancelled, nil
	}
	w.saveSourceExcerpts(a, tasks)
	outcome, reason := decideOutcome(tasks)
	if outcome == nodeproto.OutcomeFailed {
		// Tasks ran but none succeeded: still upload the result manifest so the
		// controller can report the real task states, raw streams and gaps.
		// The run is not advanced to finalizing; an upload failure only drops
		// the manifest, never changes the failed outcome.
		cleanup, _ := w.cleanupState(a)
		path, err := w.writeManifest(a, handler, tasks, cleanup)
		if err != nil {
			return outcome, reason, nil
		}
		if _, err := w.uploadManifest(a, path); err != nil {
			return outcome, reason, nil
		}
		cid := a.manifestClientID
		return outcome, reason, &cid
	}
	if _, err := w.event(a, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: string(domain.RunFinalizing)}, ""); err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}
	cleanup, _ := w.cleanupState(a)
	path, err := w.writeManifest(a, handler, tasks, cleanup)
	if err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonHandlerError, nil
	}
	if _, err := w.uploadManifest(a, path); err != nil {
		return nodeproto.OutcomeFailed, nodeproto.ReasonArtifactUploadFailed, nil
	}
	cid := a.manifestClientID
	return outcome, reason, &cid
}

// decideOutcome derives the attempt outcome from structured task states.
func decideOutcome(tasks []*taskRun) (string, string) {
	allRequired, anySucceeded := true, false
	reason := ""
	for _, t := range tasks {
		if t.status == domain.TaskSucceeded {
			anySucceeded = true
			continue
		}
		if t.spec.Required {
			allRequired = false
			if reason == "" {
				reason = t.reason
				if reason == "" {
					reason = nodeproto.ReasonHandlerError
				}
			}
		}
	}
	switch {
	case allRequired && anySucceeded:
		return nodeproto.OutcomeSucceeded, ""
	case anySucceeded:
		return nodeproto.OutcomePartial, reason
	}
	if reason == "" {
		reason = nodeproto.ReasonHandlerError
	}
	return nodeproto.OutcomeFailed, reason
}

func safePlan(ctx context.Context, h analysis.Handler, job analysis.Job) (specs []analysis.TaskSpec, err error) {
	defer func() {
		if recover() != nil {
			specs, err = nil, errors.New("handler_panic")
		}
	}()
	return h.Plan(ctx, job)
}

func safeRun(ctx context.Context, h analysis.Handler, job analysis.Job, t analysis.TaskSpec, r executor.Runner) (res analysis.TaskResult) {
	defer func() {
		if recover() != nil {
			res = analysis.TaskResult{Status: domain.TaskFailed, Reason: nodeproto.ReasonHandlerError}
		}
	}()
	return h.Run(ctx, job, t, r)
}

func (w *worker) setTask(a *attempt, t *taskRun, status domain.TaskStatus, reason string) error {
	var rp *string
	if reason != "" {
		rp = &reason
	}
	resp, err := w.event(a, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: t.taskID, ExpectedVersion: t.version, Status: string(status), Reason: rp}, "")
	if err != nil {
		return err
	}
	if resp.Version != nil {
		t.version = *resp.Version
	} else {
		t.version++
	}
	t.status, t.reason = status, reason
	return nil
}

func (w *worker) runTask(ctx context.Context, a *attempt, h analysis.Handler, job analysis.Job, t *taskRun) {
	if err := w.setTask(a, t, domain.TaskRunning, ""); err != nil {
		return
	}
	t.started = true
	runner := &journalRunner{w: w, a: a, task: t.spec.Key, taskID: t.taskID}
	res := safeRun(ctx, h, job, t.spec, runner)
	res.Limitations = append(res.Limitations, w.restoreWorkspace(a)...)
	t.result = res
	status, reason := w.verifyTaskResult(a, t.spec.Key, res)
	t.manifest, status, reason = w.manifestForTask(a, t, status, reason, res)
	if a.ctx.Err() != nil {
		t.status, t.reason = domain.TaskCancelled, nodeproto.ReasonCancelled
		return
	}
	_ = w.setTask(a, t, status, reason)
}

// Limitations of the task after which the workspace was restored: it left
// changed or missing manifest files, or unlisted files outside data
// directories, or the restore itself failed.
const (
	LimWorkspaceRestored     = "workspace_restored_after_task"
	LimWorkspaceLeftovers    = "workspace_leftovers_removed"
	LimWorkspaceRestoreError = "workspace_restore_failed"
)

// restoreWorkspace puts the attempt workspace back to the snapshot after a
// task, so what one task's tools or build scripts changed or left behind
// never reaches the next task (source.WorkspaceGuard). It returns the
// limitations of the task that ran. A failed restore leaves the copy as it
// is; every later task's own drift check still sees the difference.
func (w *worker) restoreWorkspace(a *attempt) []string {
	if a.guard == nil || a.ctx.Err() != nil {
		return nil
	}
	res, err := a.guard.Restore(a.ctx)
	var lims []string
	if len(res.Restored) > 0 {
		lims = append(lims, LimWorkspaceRestored)
	}
	if len(res.Removed) > 0 {
		lims = append(lims, LimWorkspaceLeftovers)
	}
	if err != nil && a.ctx.Err() == nil {
		lims = append(lims, LimWorkspaceRestoreError)
	}
	return lims
}

// verifyTaskResult never lets a handler claim success the recorded
// executions do not support.
func (w *worker) verifyTaskResult(a *attempt, key string, res analysis.TaskResult) (domain.TaskStatus, string) {
	status, reason := res.Status, res.Reason
	if !status.Terminal() || status == domain.TaskQueued || status == domain.TaskBlocked {
		return domain.TaskFailed, nodeproto.ReasonHandlerError
	}
	if reason != "" && !nodeproto.ValidReason(reason) {
		reason = nodeproto.ReasonHandlerError
	}
	if status != domain.TaskSucceeded {
		if reason == "" {
			reason = nodeproto.ReasonHandlerError
		}
		return status, reason
	}
	a.recMu.Lock()
	defer a.recMu.Unlock()
	for _, r := range a.recorded[key] {
		if r.Status != executor.StatusExited || r.ExitCode == nil || r.Cleanup == executor.CleanupUncertain || r.Stdout.Incomplete || r.Stdout.Truncated {
			return domain.TaskFailed, nodeproto.ReasonHandlerError
		}
	}
	return domain.TaskSucceeded, ""
}

// journalRunner wraps the executor: it journals intent and process identity
// through the agent, adds attempt protections, and records every result.
type journalRunner struct {
	w      *worker
	a      *attempt
	task   string
	taskID string
}

func (r *journalRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	a, w := r.a, r.w
	ordinal := int(a.ordinal.Add(1))
	refused := func(reason string) executor.Result {
		res := executor.Result{Status: executor.StatusRejected, Reason: reason, Cleanup: executor.CleanupNotStarted}
		r.record(res)
		return res
	}
	if a.ctx.Err() != nil || a.stopNew.Load() {
		return refused("attempt_stopping")
	}
	if err := w.intent(a, workerproto.IntentTaskExecStarted, r.task, ordinal, ""); err != nil {
		return refused("journal_unavailable")
	}
	spec.ProtectedRoots = append(spec.ProtectedRoots, a.snapshot.SnapshotDir)
	if a.job.Source.Kind == "local" && a.job.Source.ApprovedRoot != "" {
		// The user's own project folder: no tool runs in it or from it. The
		// rest of the approved root is not the analyzed source (a $HOME root
		// also holds the installed tools and CODEYE's state).
		spec.ProtectedRoots = append(spec.ProtectedRoots, filepath.Join(a.job.Source.ApprovedRoot, filepath.FromSlash(a.job.Source.RelativePath)))
	}
	spec.TempParent = filepath.Join(a.scratch, "tmp")
	spec.TaskID = r.taskID
	spec.AttemptEpoch = a.job.Attempt.Epoch
	id, epoch := a.ref()
	spec.OnStarted = func(ctx context.Context, p executor.ProcessIdentity) error {
		a.anyProc.Store(true)
		msg := &workerproto.ProcessStarted{AttemptID: id, Epoch: epoch, TaskKey: r.task, Ordinal: ordinal, PID: p.PID, StartTime: p.StartTime}
		if runtime.GOOS == "linux" {
			msg.PGID, msg.Containment = p.PID, string(executor.ContainmentProcessGroup)
		} else if runtime.GOOS == "windows" {
			msg.Containment = string(executor.ContainmentJobObject)
		}
		rctx, cancel := context.WithTimeout(ctx, w.cfg.ReplyTimeout)
		defer cancel()
		reply, err := w.conn.Request(rctx, msg)
		if err != nil {
			return err
		}
		return reply.Check(nil)
	}
	// Cancelled by either the attempt (cancel/lease loss) or the handler.
	runCtx, cancelRun := context.WithCancel(ctx)
	stop := context.AfterFunc(a.ctx, cancelRun)
	res := w.runner.Run(runCtx, spec)
	stop()
	cancelRun()
	if res.Process.PID != 0 {
		a.anyProc.Store(true)
	}
	r.record(res)
	end := &workerproto.ProcessEnded{AttemptID: id, Epoch: epoch, TaskKey: r.task, Ordinal: ordinal, PID: res.Process.PID, StartTime: res.Process.StartTime, Cleanup: string(res.Cleanup), LeftoverKilled: res.LeftoverKilled}
	if err := w.request(a, end, w.cfg.ReplyTimeout, nil); err != nil {
		// The intent stays open in the agent journal: report uncertain.
		a.uncertain.Store(true)
	}
	return res
}

func (r *journalRunner) record(res executor.Result) {
	r.a.recMu.Lock()
	r.a.recorded[r.task] = append(r.a.recorded[r.task], res)
	r.a.recMu.Unlock()
}

func fileDigest(path string) (string, int64) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	h := sha256.New()
	n, _ := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n
}
