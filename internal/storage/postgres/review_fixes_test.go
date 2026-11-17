package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

func TestClassifyDataExceptionsAreInvalid(t *testing.T) {
	cases := map[string]error{
		"22003": domain.ErrInvalid,     // numeric overflow
		"22P05": domain.ErrInvalid,     // untranslatable character (jsonb \u0000)
		"22021": domain.ErrInvalid,     // bad encoding
		"23503": domain.ErrInvalid,     // foreign key
		"23505": domain.ErrConflict,    // unique
		"CY004": domain.ErrConflict,    // guard trigger
		"08006": domain.ErrUnavailable, // connection failure
		"57014": domain.ErrUnavailable, // statement timeout
		"55P03": domain.ErrUnavailable, // lock timeout
	}
	for code, want := range cases {
		got := classify(&pgconn.PgError{Code: code, Message: "secret detail"})
		if got != want {
			t.Errorf("%s -> %v, want %v", code, got, want)
		}
	}
}

func TestSessionTimeouts(t *testing.T) {
	s := testStore(t)
	for param, want := range map[string]string{"lock_timeout": "5s", "statement_timeout": "15s"} {
		var got string
		if err := s.pool.QueryRow(context.Background(), "SHOW "+param).Scan(&got); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", param, got, err, want)
		}
	}
}

func TestSnapshotDigestPolicy(t *testing.T) {
	s := testStore(t)
	ctx, _, _, r := seed(t, s)
	o := claim(t, s, r)
	sha1 := strings.Repeat("a", 40)
	sha256 := strings.Repeat("b", 64)
	for _, c := range []struct{ digest, kind string }{
		{sha1, "stable_copy_best_effort"}, // a manifest digest is always SHA-256
		{sha256[:39], "git_commit"},
		{strings.ToUpper(sha1), "git_commit"},
		{sha1 + "0", "git_commit"},
		{sha1, "unknown"},
	} {
		_, err := s.FixSnapshot(ctx, o, c.digest, c.kind)
		expect(t, err, domain.ErrInvalid)
	}
	id, err := s.FixSnapshot(ctx, o, sha1, "git_commit")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.FixSnapshot(ctx, o, sha1, "git_commit")
	if err != nil || again != id {
		t.Fatal(again, err)
	}
	_, err = s.FixSnapshot(ctx, o, sha256, "git_commit")
	expect(t, err, domain.ErrConflict)

	_, _, _, r2 := seed(t, s)
	o2 := claim(t, s, r2)
	if _, err = s.FixSnapshot(ctx, o2, sha256, "git_commit"); err != nil {
		t.Fatal(err)
	}
}

func TestConfigNormalizationRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	actor, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, actor, "p", json.RawMessage(`{"zz":1e2,"a":{"y":1.0,"x":1.5E+2},"long_key":[true,null]}`))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"a":{"x":150,"y":1.0},"long_key":[true,null],"zz":100}`
	check := func(label string, cfg json.RawMessage, digest string) {
		t.Helper()
		if string(cfg) != want {
			t.Errorf("%s config = %s, want %s", label, cfg, want)
		}
		if domain.Digest(cfg) != digest {
			t.Errorf("%s digest does not describe config", label)
		}
	}
	check("create", p.Config, p.ConfigDigest)
	got, err := s.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("get", got.Config, got.ConfigDigest)
	if got.ConfigDigest != p.ConfigDigest {
		t.Error("digest changed between create and get")
	}
	var column string
	if err = s.pool.QueryRow(ctx, "SELECT config_digest FROM project_revisions WHERE project_id=$1 AND revision=1", p.ID).Scan(&column); err != nil || column != got.ConfigDigest {
		t.Fatal(column, err)
	}

	// A revision with equal values spelled differently stores the same normalized text.
	if _, err = s.ReviseProject(ctx, actor, p.ID, 1, json.RawMessage(`{"long_key":[true,null],"zz":100,"a":{"x":150,"y":1.0}}`)); err != nil {
		t.Fatal(err)
	}
	rev, err := s.GetProject(ctx, actor, p.ID)
	if err != nil || rev.Revision != 2 {
		t.Fatal(rev, err)
	}
	check("revise", rev.Config, rev.ConfigDigest)
	if rev.ConfigDigest != p.ConfigDigest {
		t.Error("equivalent revisions must hash equally")
	}
	// 1 and 1.0 stay distinct.
	one, err := s.CreateProject(ctx, actor, "one", json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	onePointZero, err := s.CreateProject(ctx, actor, "one0", json.RawMessage(`{"n":1.0}`))
	if err != nil {
		t.Fatal(err)
	}
	if one.ConfigDigest == onePointZero.ConfigDigest {
		t.Error("1 and 1.0 collapsed")
	}
}

func TestUnstorableConfigIsInvalidNotUnavailable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	actor, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, actor, "p", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"nul escape":       `{"a":"\u0000"}`,
		"numeric overflow": `{"a":1e999999999}`,
		"not an object":    `[1]`,
	} {
		_, err := s.CreateProject(ctx, actor, "bad", json.RawMessage(body))
		if !errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrUnavailable) {
			t.Errorf("%s: CreateProject err=%v", name, err)
		}
		_, err = s.ReviseProject(ctx, actor, p.ID, 1, json.RawMessage(body))
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: ReviseProject err=%v", name, err)
		}
	}
	var projects, revisions int
	if err = s.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM projects), (SELECT count(*) FROM project_revisions)").Scan(&projects, &revisions); err != nil || projects != 1 || revisions != 1 {
		t.Fatal("rejected config left rows", projects, revisions, err)
	}
	// The revision counter did not move, so the expected version still works.
	if _, err = s.ReviseProject(ctx, actor, p.ID, 1, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
}

// Readers take only FOR SHARE on the project and never lock the run row, so they
// proceed while another transaction holds those locks, and heartbeats do too.
func TestReadsAndHeartbeatDoNotBlockOnShareLocks(t *testing.T) {
	s := testStore(t)
	ctx, actor, p, r := seed(t, s)
	o := claim(t, s, r)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var ignored string
	if err = tx.QueryRow(ctx, "SELECT id::text FROM projects WHERE id=$1 FOR SHARE", p.ID).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, "SELECT id::text FROM runs WHERE id=$1 FOR UPDATE", r.ID).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err = s.GetProject(short, actor, p.ID); err != nil {
		t.Fatal("GetProject blocked:", err)
	}
	if _, err = s.GetRun(short, actor, r.ID); err != nil {
		t.Fatal("GetRun blocked by run lock:", err)
	}
	if _, err = s.Events(short, actor, r.ID, 0, 10); err != nil {
		t.Fatal("Events blocked by run lock:", err)
	}
	// A heartbeat needs the run row, so it must wait for the run lock, not the project share lock.
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, "SELECT id::text FROM projects WHERE id=$1 FOR SHARE", p.ID).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Heartbeat(short, o, time.Minute); err != nil {
		t.Fatal("Heartbeat blocked by project share lock:", err)
	}
}

func TestRecordEvidenceRequiresExecutionInCurrentAttempt(t *testing.T) {
	s := testStore(t)
	ctx, actor, _, r := seed(t, s)
	o := claim(t, s, r)
	snap := snapshot(t, s, o)
	path, line, hash := "src/a.go", int64(1), domain.Digest([]byte("x"))
	evidence := func(owner Ownership, task string) error {
		_, err := s.RecordEvidence(ctx, owner, EvidenceInput{TaskID: task, Kind: "code", SnapshotID: &snap, Path: &path, StartLine: &line, EndLine: &line, BlobSHA256: &hash})
		return err
	}
	task, err := s.CreateTask(ctx, o, true)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, evidence(o, task), domain.ErrInvalid) // never ran
	v, err := s.SetTaskStatus(ctx, o, task, 1, domain.TaskBlocked)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, evidence(o, task), domain.ErrInvalid) // blocked is not execution
	if v, err = s.SetTaskStatus(ctx, o, task, v, domain.TaskQueued); err != nil {
		t.Fatal(err)
	}
	expect(t, evidence(o, task), domain.ErrInvalid)
	if v, err = s.SetTaskStatus(ctx, o, task, v, domain.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err = evidence(o, task); err != nil {
		t.Fatal(err)
	}
	if v, err = s.SetTaskStatus(ctx, o, task, v, domain.TaskBudgetExhausted); err != nil {
		t.Fatal(err)
	}
	expect(t, evidence(o, task), domain.ErrInvalid) // budget_exhausted is not recorded as executed
	_ = v

	// A task that ran only in an earlier attempt cannot be cited by the next one.
	ran, err := s.CreateTask(ctx, o, false)
	if err != nil {
		t.Fatal(err)
	}
	rv, err := s.SetTaskStatus(ctx, o, ran, 1, domain.TaskRunning)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTaskStatus(ctx, o, ran, rv, domain.TaskSucceeded); err != nil {
		t.Fatal(err)
	}
	if err = evidence(o, ran); err != nil {
		t.Fatal(err)
	}
	failed, err := s.FinishAttempt(ctx, o, "failed", "confirmed")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Retry(ctx, actor, r.ID, failed.Version)
	if err != nil {
		t.Fatal(err)
	}
	next := claim(t, s, retry)
	expect(t, evidence(next, ran), domain.ErrInvalid)
	var version int64
	if err = s.pool.QueryRow(ctx, "SELECT version FROM tasks WHERE id=$1", ran).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTaskStatus(ctx, next, ran, version, domain.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err = evidence(next, ran); err != nil {
		t.Fatal(err)
	}
}

func taskState(t *testing.T, s *Store, id string) domain.TaskStatus {
	t.Helper()
	var st domain.TaskStatus
	if err := s.pool.QueryRow(context.Background(), "SELECT status FROM tasks WHERE id=$1", id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func executionState(t *testing.T, s *Store, attempt, task string) (domain.TaskStatus, bool) {
	t.Helper()
	var st domain.TaskStatus
	err := s.pool.QueryRow(context.Background(), "SELECT status FROM task_executions WHERE attempt_id=$1 AND task_id=$2", attempt, task).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return st, true
}

// openTasks builds one task per open state plus one already-succeeded task.
func openTasks(t *testing.T, s *Store, o Ownership) (running, queued, blocked, done string) {
	t.Helper()
	ctx := context.Background()
	mk := func(required bool) string {
		id, err := s.CreateTask(ctx, o, required)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	step := func(id string, version int64, to domain.TaskStatus) int64 {
		v, err := s.SetTaskStatus(ctx, o, id, version, to)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	running, queued, blocked, done = mk(true), mk(true), mk(false), mk(false)
	step(running, 1, domain.TaskRunning)
	step(blocked, 1, domain.TaskBlocked)
	v := step(done, 1, domain.TaskRunning)
	step(done, v, domain.TaskSucceeded)
	return
}

func eventTypes(t *testing.T, s *Store, actor, run string) ([]Event, []string) {
	t.Helper()
	events, err := s.Events(context.Background(), actor, run, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return events, types
}

// Every status carried by an event must be a move the domain table allows.
func assertEventsFollowDomain(t *testing.T, events []Event) {
	t.Helper()
	current := domain.RunQueued
	for _, e := range events {
		switch e.Type {
		case "attempt.leased": // BeginAttempt moves queued -> preparing without a status field
			current = domain.RunPreparing
			continue
		case "run.retried": // service-guarded retry of a failed/cancelled run
			current = domain.RunQueued
			continue
		}
		var p struct {
			Status domain.RunStatus `json:"status"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil || !p.Status.Valid() || p.Status == current || strings.HasPrefix(e.Type, "task.") {
			continue
		}
		if !domain.CanTransitionRun(current, p.Status) {
			t.Errorf("event %d (%s): %s -> %s is not allowed by the domain table", e.Sequence, e.Type, current, p.Status)
		}
		current = p.Status
	}
}

func TestConfirmedFailureClosesOpenTasksPreservingHistory(t *testing.T) {
	s := testStore(t)
	ctx, actor, _, r := seed(t, s)
	o := claim(t, s, r)
	snapshot(t, s, o)
	running, queued, blocked, done := openTasks(t, s, o)

	// Uncertain cleanup leaves everything as it was.
	rec, err := s.FinishAttempt(ctx, o, "failed", "uncertain")
	if err != nil || rec.Status != domain.RunRecoveryRequired {
		t.Fatal(rec, err)
	}
	for id, want := range map[string]domain.TaskStatus{running: domain.TaskRunning, queued: domain.TaskQueued, blocked: domain.TaskBlocked, done: domain.TaskSucceeded} {
		if got := taskState(t, s, id); got != want {
			t.Errorf("uncertain cleanup changed task to %s, want %s", got, want)
		}
	}
	failed, err := s.ConfirmCleanup(ctx, o)
	if err != nil || failed.Status != domain.RunFailed {
		t.Fatal(failed, err)
	}
	expectTasks := func(label string, m map[string]domain.TaskStatus) {
		t.Helper()
		for id, want := range m {
			if got := taskState(t, s, id); got != want {
				t.Errorf("%s: task %s = %s, want %s", label, id, got, want)
			}
		}
	}
	expectTasks("confirm", map[string]domain.TaskStatus{running: domain.TaskFailed, queued: domain.TaskSkippedPolicy, blocked: domain.TaskSkippedPolicy, done: domain.TaskSucceeded})
	for id, want := range map[string]domain.TaskStatus{running: domain.TaskFailed, blocked: domain.TaskSkippedPolicy, done: domain.TaskSucceeded} {
		if got, ok := executionState(t, s, o.AttemptID, id); !ok || got != want {
			t.Errorf("execution of %s = %s (%v), want %s", id, got, ok, want)
		}
	}
	if _, ok := executionState(t, s, o.AttemptID, queued); ok {
		t.Error("a task that never started must not get an execution row")
	}
	events, _ := eventTypes(t, s, actor, r.ID)
	last := events[len(events)-1]
	var payload map[string]any
	if err = json.Unmarshal(last.Payload, &payload); err != nil || last.Type != "attempt.cleanup_confirmed" || payload["closed_tasks"] != float64(3) {
		t.Fatal(last.Type, string(last.Payload), err)
	}
	assertEventsFollowDomain(t, events)

	// A conservative retry reruns every task but keeps the previous attempt's history.
	retry, err := s.Retry(ctx, actor, r.ID, failed.Version)
	if err != nil {
		t.Fatal(err)
	}
	expectTasks("retry", map[string]domain.TaskStatus{running: domain.TaskQueued, queued: domain.TaskQueued, blocked: domain.TaskQueued, done: domain.TaskQueued})
	if got, _ := executionState(t, s, o.AttemptID, running); got != domain.TaskFailed {
		t.Errorf("history rewritten: %s", got)
	}
	if got, _ := executionState(t, s, o.AttemptID, done); got != domain.TaskSucceeded {
		t.Errorf("history rewritten: %s", got)
	}
	_ = claim(t, s, retry)

	// Confirmed failure reported directly by the worker closes tasks the same way.
	_, _, _, r2 := seed(t, s)
	o2 := claim(t, s, r2)
	snapshot(t, s, o2)
	run2, queued2, blocked2, done2 := openTasks(t, s, o2)
	f2, err := s.FinishAttempt(ctx, o2, "failed", "confirmed")
	if err != nil || f2.Status != domain.RunFailed {
		t.Fatal(f2, err)
	}
	for id, want := range map[string]domain.TaskStatus{run2: domain.TaskFailed, queued2: domain.TaskSkippedPolicy, blocked2: domain.TaskSkippedPolicy, done2: domain.TaskSucceeded} {
		if got := taskState(t, s, id); got != want {
			t.Errorf("finish: task %s = %s, want %s", id, got, want)
		}
	}
}

func TestCancellationReceiptRoutesThroughCancelling(t *testing.T) {
	s := testStore(t)
	ctx, actor, _, r := seed(t, s)
	o := claim(t, s, r)
	snapshot(t, s, o)
	running, queued, blocked, done := openTasks(t, s, o)
	if got, err := s.Cancel(ctx, actor, r.ID); err != nil || got.Status != domain.RunCancelRequested {
		t.Fatal(got, err)
	}
	cancelled, err := s.FinishAttempt(ctx, o, "cancelled", "confirmed")
	if err != nil || cancelled.Status != domain.RunCancelled {
		t.Fatal(cancelled, err)
	}
	events, types := eventTypes(t, s, actor, r.ID)
	n := len(types)
	if n < 3 || types[n-3] != "run.cancel_requested" || types[n-2] != "run.status" || types[n-1] != "attempt.finished" {
		t.Fatal("unexpected cancellation events", types)
	}
	var mid, end struct{ Status domain.RunStatus }
	if json.Unmarshal(events[n-2].Payload, &mid) != nil || json.Unmarshal(events[n-1].Payload, &end) != nil || mid.Status != domain.RunCancelling || end.Status != domain.RunCancelled {
		t.Fatal(string(events[n-2].Payload), string(events[n-1].Payload))
	}
	assertEventsFollowDomain(t, events)
	for id, want := range map[string]domain.TaskStatus{running: domain.TaskCancelled, queued: domain.TaskCancelled, blocked: domain.TaskCancelled, done: domain.TaskSucceeded} {
		if got := taskState(t, s, id); got != want {
			t.Errorf("task %s = %s, want %s", id, got, want)
		}
	}
	// The receipt replays; a different outcome does not.
	replay, err := s.FinishAttempt(ctx, o, "cancelled", "confirmed")
	if err != nil || replay.Version != cancelled.Version || replay.EventSequence != cancelled.EventSequence {
		t.Fatal(replay, err)
	}
	_, err = s.FinishAttempt(ctx, o, "failed", "confirmed")
	expect(t, err, domain.ErrConflict)

	// From cancelling the receipt goes straight to cancelled with no extra hop.
	_, actor2, _, r2 := seed(t, s)
	o2 := claim(t, s, r2)
	snapshot(t, s, o2)
	if _, err = s.Cancel(ctx, actor2, r2.ID); err != nil {
		t.Fatal(err)
	}
	cur, err := s.GetRun(ctx, actor2, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(ctx, o2, cur.Version, domain.RunCancelling); err != nil {
		t.Fatal(err)
	}
	if done, err := s.FinishAttempt(ctx, o2, "cancelled", "confirmed"); err != nil || done.Status != domain.RunCancelled {
		t.Fatal(done, err)
	}
	ev2, _ := eventTypes(t, s, actor2, r2.ID)
	assertEventsFollowDomain(t, ev2)
}

// finalizingReceipt drives a fresh run to an accepted finalizing receipt.
func finalizingReceipt(t *testing.T, s *Store) (string, Ownership, Run) {
	t.Helper()
	ctx, actor, _, r := seed(t, s)
	o := claim(t, s, r)
	snapshot(t, s, o)
	cur, err := s.GetRun(ctx, actor, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(ctx, o, cur.Version, domain.RunRunning); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, o, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SetTaskStatus(ctx, o, task, 1, domain.TaskRunning)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTaskStatus(ctx, o, task, v, domain.TaskSucceeded); err != nil {
		t.Fatal(err)
	}
	if cur, err = s.GetRun(ctx, actor, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(ctx, o, cur.Version, domain.RunFinalizing); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.FinishAttempt(ctx, o, "succeeded", "confirmed")
	if err != nil || receipt.Status != domain.RunFinalizing {
		t.Fatal(receipt, err)
	}
	return actor, o, receipt
}

func TestFinalizingReceiptReplaysAfterPublication(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, published := range []domain.RunStatus{domain.RunFinalizing, domain.RunCompleted, domain.RunPartial} {
		actor, o, receipt := finalizingReceipt(t, s)
		if published != domain.RunFinalizing {
			sqlExec(t, s, "UPDATE runs SET status=$2 WHERE id=(SELECT run_id FROM attempts WHERE id=$1)", o.AttemptID, published)
		}
		replay, err := s.FinishAttempt(ctx, o, "succeeded", "confirmed")
		if err != nil || replay.Status != published || replay.Version != receipt.Version || replay.EventSequence != receipt.EventSequence {
			t.Fatalf("replay with run %s: %+v, %v", published, replay, err)
		}
		_, err = s.FinishAttempt(ctx, o, "partial", "confirmed")
		expect(t, err, domain.ErrConflict)
		_, err = s.FinishAttempt(ctx, o, "succeeded", "uncertain")
		expect(t, err, domain.ErrConflict)
		stale := o
		stale.Epoch++
		_, err = s.FinishAttempt(ctx, stale, "succeeded", "confirmed")
		expect(t, err, domain.ErrStaleAttempt)
		other := o
		other.NodeID = domain.NewID()
		_, err = s.FinishAttempt(ctx, other, "succeeded", "confirmed")
		expect(t, err, domain.ErrStaleAttempt)
		// Replay writes nothing and cannot revive the released lease.
		if _, err = s.Heartbeat(ctx, o, time.Minute); !errors.Is(err, domain.ErrStaleAttempt) {
			t.Fatal("heartbeat after finish:", err)
		}
		if again, err := s.GetRun(ctx, actor, receipt.ID); err != nil || again.EventSequence != receipt.EventSequence {
			t.Fatal("replay appended events", again, err)
		}
	}
}

func TestFinalizingReceiptDoesNotReplayAfterRecoveryOrNewEpoch(t *testing.T) {
	s := testStore(t)
	ctx, actor, _, r := seed(t, s)
	o := claim(t, s, r)
	snapshot(t, s, o)
	failed, err := s.FinishAttempt(ctx, o, "failed", "confirmed")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Retry(ctx, actor, r.ID, failed.Version)
	if err != nil {
		t.Fatal(err)
	}
	newer := claim(t, s, retry)
	// The old receipt is for an older epoch and the run is queued/preparing again.
	_, err = s.FinishAttempt(ctx, o, "failed", "confirmed")
	expect(t, err, domain.ErrStaleAttempt)
	_ = newer
}
