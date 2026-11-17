package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CODEYE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL URL not configured")
	}
	cfg, err := connectionConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal("test DB connection failed")
	}
	schema := "test_" + strings.ReplaceAll(domain.NewID(), "-", "")
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	if _, err = conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = conn.Close(ctx)
	})
	return &Store{pool: pool}
}
func seed(t *testing.T, s *Store) (context.Context, string, Project, Run) {
	t.Helper()
	ctx := context.Background()
	actor, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, actor, "sample", json.RawMessage(`{"source":{"kind":"local"}}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Enqueue(ctx, actor, p.ID, "first", json.RawMessage(`{"profile":"deep-review"}`))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, actor, p, r
}
func claim(t *testing.T, s *Store, r Run) Ownership {
	t.Helper()
	a, err := s.BeginAttempt(context.Background(), r.ID, domain.NewID(), domain.NewID(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return Ownership{a.ID, a.NodeID, a.WorkerID, a.Epoch}
}
func snapshot(t *testing.T, s *Store, o Ownership) string {
	t.Helper()
	id, err := s.FixSnapshot(context.Background(), o, domain.Digest([]byte("snapshot")), "stable_copy_best_effort")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func expect(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error=%v want=%v", err, want)
	}
}
func sqlExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionPolicy(t *testing.T) {
	clearPostgresEnvironment(t)
	for _, name := range []string{"PGSERVICE", "PGPASSWORD", "PGHOST"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "private")
			_, err := connectionConfig("postgres://user:secret@127.0.0.1/test?sslmode=disable")
			expect(t, err, domain.ErrInvalid)
		})
	}
	for _, dsn := range []string{"", "postgres://user:secret@remote.invalid/test?sslmode=disable", "postgres://user:secret@localhost/test?sslmode=prefer", "postgres://user:secret@localhost/test?service=hidden"} {
		_, err := connectionConfig(dsn)
		expect(t, err, domain.ErrInvalid)
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("credential leak")
		}
	}
	cfg, err := connectionConfig("postgres://user:secret@127.0.0.1/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Password != "secret" || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("unexpected connection policy")
	}
}
func TestProjectACLRevisionAndEnqueue(t *testing.T) {
	s := testStore(t)
	ctx, owner, p, r := seed(t, s)
	other, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetProject(ctx, other, p.ID)
	expect(t, err, domain.ErrForbidden)
	if err = s.SetMember(ctx, owner, p.ID, other, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetRun(ctx, other, r.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Enqueue(ctx, other, p.ID, "denied", json.RawMessage(`{}`))
	expect(t, err, domain.ErrForbidden)
	if err = s.SetMember(ctx, owner, p.ID, other, domain.RoleRunner); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(ctx, other, p.ID, "allowed", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.SetMember(ctx, owner, p.ID, other, ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetRun(ctx, other, r.ID)
	expect(t, err, domain.ErrForbidden)
	_, err = s.Events(ctx, other, r.ID, 0, 100)
	expect(t, err, domain.ErrForbidden)
	rev, err := s.ReviseProject(ctx, owner, p.ID, 1, json.RawMessage(`{"source":{"kind":"git"}}`))
	if err != nil || rev != 2 {
		t.Fatal(rev, err)
	}
	old, err := s.GetRun(ctx, owner, r.ID)
	if err != nil || old.ProjectRevision != 1 {
		t.Fatal(old, err)
	}
	_, err = s.ReviseProject(ctx, owner, p.ID, 1, json.RawMessage(`{}`))
	expect(t, err, domain.ErrConflict)
	same, err := s.Enqueue(ctx, owner, p.ID, "first", json.RawMessage(`{ "profile": "deep-review" }`))
	if err != nil || same.ID != r.ID || same.ProjectRevision != 1 {
		t.Fatal(same, err)
	}
	_, err = s.Enqueue(ctx, owner, p.ID, "first", json.RawMessage(`{"profile":"focused"}`))
	expect(t, err, domain.ErrConflict)
	_, err = s.pool.Exec(ctx, "UPDATE project_revisions SET config='{}' WHERE project_id=$1", p.ID)
	if err == nil {
		t.Fatal("mutable project revision")
	}
}
func TestConcurrentEnqueueAndClaim(t *testing.T) {
	s := testStore(t)
	ctx, actor, p, r := seed(t, s)
	start := make(chan struct{})
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			x, err := s.Enqueue(ctx, actor, p.ID, "concurrent", json.RawMessage(`{}`))
			ids <- x.ID
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatal("duplicate enqueue")
		}
	}
	start = make(chan struct{})
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.BeginAttempt(ctx, r.ID, domain.NewID(), domain.NewID(), time.Minute)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			expect(t, err, domain.ErrConflict)
		}
	}
	if wins != 1 {
		t.Fatalf("claim winners=%d", wins)
	}
}
func TestEventFailureRollsBack(t *testing.T) {
	s := testStore(t)
	ctx, actor, p, r := seed(t, s)
	sqlExec(t, s, `CREATE FUNCTION reject_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test rejection'; END $$`)
	sqlExec(t, s, `CREATE TRIGGER reject_event BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION reject_event()`)
	_, err := s.Enqueue(ctx, actor, p.ID, "rollback", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("event failure accepted")
	}
	var count int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM runs WHERE idempotency_key='rollback'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	_, err = s.Cancel(ctx, actor, r.ID)
	if err == nil {
		t.Fatal("cancel event failure accepted")
	}
	got, err := s.GetRun(ctx, actor, r.ID)
	if err != nil || got.Status != domain.RunQueued || got.Version != 1 || got.EventSequence != 1 {
		t.Fatal(got, err)
	}
}
func TestLeaseExpiryCleanupRetryAndReplay(t *testing.T) {
	s := testStore(t)
	ctx, actor, _, r := seed(t, s)
	o := claim(t, s, r)
	snap := snapshot(t, s, o)
	bad := o
	bad.NodeID = domain.NewID()
	_, err := s.Heartbeat(ctx, bad, time.Minute)
	expect(t, err, domain.ErrStaleAttempt)
	sqlExec(t, s, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", o.AttemptID)
	_, err = s.FixSnapshot(ctx, o, domain.Digest([]byte("snapshot")), "stable_copy_best_effort")
	expect(t, err, domain.ErrStaleAttempt)
	_, err = s.FinishAttempt(ctx, o, "failed", "confirmed")
	expect(t, err, domain.ErrStaleAttempt)
	recovery, err := s.ExpireAttempt(ctx, o.AttemptID)
	if err != nil || recovery.Status != domain.RunRecoveryRequired {
		t.Fatal(recovery, err)
	}
	_, err = s.Retry(ctx, actor, r.ID, recovery.Version)
	expect(t, err, domain.ErrConflict)
	failed, err := s.ConfirmCleanup(ctx, o)
	if err != nil || failed.Status != domain.RunFailed {
		t.Fatal(failed, err)
	}
	retry, err := s.Retry(ctx, actor, r.ID, failed.Version)
	if err != nil || retry.Status != domain.RunQueued || retry.SnapshotID == nil || *retry.SnapshotID != snap {
		t.Fatal(retry, err)
	}
	newer := claim(t, s, retry)
	if newer.Epoch <= o.Epoch {
		t.Fatal("nonincreasing epoch")
	}
	_, err = s.FinishAttempt(ctx, o, "failed", "confirmed")
	expect(t, err, domain.ErrStaleAttempt)
	done, err := s.FinishAttempt(ctx, newer, "failed", "confirmed")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.FinishAttempt(ctx, newer, "failed", "confirmed")
	if err != nil || replay.Version != done.Version || replay.EventSequence != done.EventSequence {
		t.Fatal(replay, err)
	}
	_, err = s.FinishAttempt(ctx, newer, "failed", "uncertain")
	expect(t, err, domain.ErrConflict)
	events, err := s.Events(ctx, actor, r.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Sequence != int64(i+1) {
			t.Fatal("event gap")
		}
	}
}
func TestCancellationAndUncertainCleanup(t *testing.T) {
	s := testStore(t)
	ctx, actor, p, r := seed(t, s)
	cancelled, err := s.Cancel(ctx, actor, r.ID)
	if err != nil || cancelled.Status != domain.RunCancelled {
		t.Fatal(cancelled, err)
	}
	_, err = s.Retry(ctx, actor, r.ID, cancelled.Version)
	expect(t, err, domain.ErrConflict)
	r, err = s.Enqueue(ctx, actor, p.ID, "active", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	o := claim(t, s, r)
	snapshot(t, s, o)
	cancelled, err = s.Cancel(ctx, actor, r.ID)
	if err != nil || cancelled.Status != domain.RunCancelRequested {
		t.Fatal(cancelled, err)
	}
	uncertain, err := s.FinishAttempt(ctx, o, "cancelled", "uncertain")
	if err != nil || uncertain.Status != domain.RunRecoveryRequired {
		t.Fatal(uncertain, err)
	}
	_, err = s.BeginAttempt(ctx, r.ID, domain.NewID(), domain.NewID(), time.Minute)
	expect(t, err, domain.ErrConflict)
	if _, err = s.ConfirmCleanup(ctx, o); err != nil {
		t.Fatal(err)
	}
}
func TestTasksEvidenceScopeAndDurability(t *testing.T) {
	s := testStore(t)
	ctx, actor, p, r := seed(t, s)
	o := claim(t, s, r)
	snap := snapshot(t, s, o)
	_, err := s.FixSnapshot(ctx, o, domain.Digest([]byte("different")), "stable_copy_best_effort")
	expect(t, err, domain.ErrConflict)
	task, err := s.CreateTask(ctx, o, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetTaskStatus(ctx, o, task, 1, domain.TaskSucceeded)
	expect(t, err, domain.ErrInvalid)
	v, err := s.SetTaskStatus(ctx, o, task, 1, domain.TaskRunning)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTaskStatus(ctx, o, task, v, domain.TaskFailed); err != nil {
		t.Fatal(err)
	}
	path := "src/main.go"
	line := int64(1)
	hash := domain.Digest([]byte("code"))
	input := EvidenceInput{TaskID: task, Kind: "code", SnapshotID: &snap, Path: &path, StartLine: &line, EndLine: &line, BlobSHA256: &hash}
	if _, err = s.RecordEvidence(ctx, o, input); err != nil {
		t.Fatal(err)
	}
	other, err := s.Enqueue(ctx, actor, p.ID, "other", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	otherOwner := claim(t, s, other)
	otherSnap := snapshot(t, s, otherOwner)
	input.SnapshotID = &otherSnap
	_, err = s.RecordEvidence(ctx, o, input)
	expect(t, err, domain.ErrInvalid)
	input.SnapshotID = &snap
	otherTask, err := s.CreateTask(ctx, otherOwner, false)
	if err != nil {
		t.Fatal(err)
	}
	input.TaskID = otherTask
	_, err = s.RecordEvidence(ctx, o, input)
	expect(t, err, domain.ErrInvalid)
	artifact := domain.NewID()
	sqlExec(t, s, "INSERT INTO artifacts(id,project_id,run_id,sha256,size_bytes,finalized) VALUES($1,$2,$3,$4,0,false)", artifact, p.ID, r.ID, hash)
	output := EvidenceInput{TaskID: task, Kind: "tool_output", ArtifactID: &artifact}
	_, err = s.RecordEvidence(ctx, o, output)
	expect(t, err, domain.ErrInvalid)
	sqlExec(t, s, "UPDATE artifacts SET finalized=true WHERE id=$1", artifact)
	if _, err = s.RecordEvidence(ctx, o, output); err != nil {
		t.Fatal(err)
	}
	// Evidence-only event changes sequence without inventing a new run entity version.
	events, err := s.Events(ctx, actor, r.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].EntityVersion != events[len(events)-2].EntityVersion {
		t.Fatal("evidence changed run version")
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
	var current domain.TaskStatus
	var version int64
	if err = s.pool.QueryRow(ctx, "SELECT status,version FROM tasks WHERE id=$1", task).Scan(&current, &version); err != nil || current != domain.TaskQueued {
		t.Fatal(current, err)
	}
	if _, err = s.SetTaskStatus(ctx, next, task, version, domain.TaskRunning); err != nil {
		t.Fatal(err)
	}
	var previous string
	if err = s.pool.QueryRow(ctx, "SELECT status FROM task_executions WHERE attempt_id=$1 AND task_id=$2", o.AttemptID, task).Scan(&previous); err != nil || previous != "failed" {
		t.Fatal(previous, err)
	}
	cfg := s.pool.Config()
	newPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer newPool.Close()
	reopened := &Store{pool: newPool}
	again, err := reopened.GetRun(ctx, actor, r.ID)
	if err != nil || again.SnapshotID == nil || *again.SnapshotID != snap {
		t.Fatal(again, err)
	}
}
func TestFailedProjectCreationAtomic(t *testing.T) {
	s := testStore(t)
	_, err := s.CreateProject(context.Background(), domain.NewID(), "orphan", json.RawMessage(`{}`))
	expect(t, err, domain.ErrInvalid)
	var count int
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM projects").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestSQLStateErrorsDoNotLeak(t *testing.T) {
	err := classify(fmt.Errorf("secret DSN"))
	expect(t, err, domain.ErrUnavailable)
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("leak")
	}
}

func TestPostgresInstallPathsDoNotOverrideConnection(t *testing.T) {
	clearPostgresEnvironment(t)
	t.Setenv("PGROOT", "C:\\Program Files\\PostgreSQL\\17")
	t.Setenv("PGBIN", "C:\\Program Files\\PostgreSQL\\17\\bin")
	config, err := connectionConfig("postgres://user:secret@127.0.0.1/test?sslmode=disable")
	if err != nil || config.ConnConfig.Host != "127.0.0.1" {
		t.Fatal("unrelated install paths blocked explicit connection", err)
	}
}

// Hosted runners may provision PGUSER/PGPASSWORD. Configuration unit tests must
// isolate that ambient setup while production continues to reject inheritance.
func clearPostgresEnvironment(t *testing.T) {
	t.Helper()
	var names []string
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "PG") {
			names = append(names, key)
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Setenv(key, value) })
		}
	}
	if len(names) > 0 {
		t.Logf("isolated PostgreSQL environment variable names: %v", names)
	}
}
