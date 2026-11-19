package migrations

import (
	"errors"
	"testing"
)

// TestScheduleMigrationKeepsLegacyRows applies 0001-0006, writes a run and tasks
// the way workers that declared stages but no weight did, then applies 0007: the run stays a manual request
// (no schedule origin), the tasks keep NULL weight and creation sequence (no
// backfill), the new columns follow their first-record rules, occurrences are
// unique per (schedule_id, scheduled_for) and decided occurrences are
// immutable. A 0006 binary refuses the result.
func TestScheduleMigrationKeepsLegacyRows(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 6), "m"); err != nil {
		t.Fatalf("apply 0001-0006: %v", err)
	}
	user, project, run, task := newID(), newID(), newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", user)
	mustExec(t, ctx, c, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, user, digest('a'))
	mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status) VALUES($1,$2,$3,'k',$4,1,'running')", run, project, user, digest('b'))
	mustExec(t, ctx, c, "INSERT INTO tasks(id,run_id,status,required_for_verdict,key,stage,version) VALUES($1,$2,'succeeded',true,'go-vet.root','static_analysis',3)", task, run)

	if err := applyFS(ctx, c, embeddedPrefix(t, 7), "m"); err != nil {
		t.Fatalf("apply 0007 on populated schema: %v", err)
	}
	var legacy int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM runs r JOIN tasks t ON t.run_id=r.id WHERE r.id=$1 AND r.schedule_id IS NULL AND r.scheduled_for IS NULL AND t.weight IS NULL AND t.created_sequence IS NULL AND t.version=3", run).Scan(&legacy); err != nil || legacy != 1 {
		t.Fatalf("legacy rows changed: %d %v", legacy, err)
	}
	// Weight: checked range, recordable once on a legacy row, then immutable.
	for _, bad := range []int{0, 1001} {
		execState(t, ctx, c, "23514", "UPDATE tasks SET weight=$2 WHERE id=$1", task, bad)
	}
	mustExec(t, ctx, c, "UPDATE tasks SET weight=2 WHERE id=$1", task)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE tasks SET weight=3 WHERE id=$1", task)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE tasks SET created_sequence=5 WHERE id=$1", task)

	schedule := newID()
	mustExec(t, ctx, c, `INSERT INTO schedules(id,project_id,created_by,actor_id,idempotency_key,request_digest,cron,timezone,run_request,overlap,misfire,enabled,next_at)
		VALUES($1,$2,$3,$3,'k',$4,'0 9 * * *','Asia/Seoul','{}','coalesce_latest','latest_once',true,'2026-01-01T00:00:00Z')`, schedule, project, user, digest('c'))
	execState(t, ctx, c, "23514", "UPDATE schedules SET overlap='catch_up' WHERE id=$1", schedule)
	execState(t, ctx, c, "23514", "UPDATE schedules SET enabled=false WHERE id=$1", schedule) // next_at requires enabled
	execState(t, ctx, c, SQLStateImmutable, "UPDATE schedules SET created_by=$2 WHERE id=$1", schedule, newID())
	mustExec(t, ctx, c, "INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state) VALUES($1,'2026-01-01T00:00:00Z','pending')", schedule)
	execState(t, ctx, c, "23505", "INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state,decided_at) VALUES($1,'2026-01-01T00:00:00Z','enqueued',now())", schedule)
	execState(t, ctx, c, "23505", "INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state) VALUES($1,'2026-01-02T00:00:00Z','pending')", schedule) // one pending
	execState(t, ctx, c, "23514", "INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state,decided_at) VALUES($1,'2026-01-03T00:00:00Z','skipped',now())", schedule)
	mustExec(t, ctx, c, "UPDATE schedule_occurrences SET state='enqueued',decided_at=now() WHERE schedule_id=$1", schedule)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE schedule_occurrences SET state='skipped',reason='misfire' WHERE schedule_id=$1", schedule)
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM schedule_occurrences WHERE schedule_id=$1", schedule)
	// A scheduled run references its occurrence; the origin is fixed.
	scheduled := newID()
	execState(t, ctx, c, "23503", "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status,schedule_id,scheduled_for) VALUES($1,$2,$3,'s2',$4,1,'queued',$5,'2026-01-05T00:00:00Z')", scheduled, project, user, digest('d'), schedule)
	mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status,schedule_id,scheduled_for) VALUES($1,$2,$3,'s1',$4,1,'queued',$5,'2026-01-01T00:00:00Z')", scheduled, project, user, digest('d'), schedule)
	execState(t, ctx, c, "23505", "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status,schedule_id,scheduled_for) VALUES($1,$2,$3,'s3',$4,1,'queued',$5,'2026-01-01T00:00:00Z')", newID(), project, user, digest('d'), schedule)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE runs SET schedule_id=NULL,scheduled_for=NULL WHERE id=$1", scheduled)
	execState(t, ctx, c, "23514", "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status,schedule_id) VALUES($1,$2,$3,'s4',$4,1,'queued',$5)", newID(), project, user, digest('d'), schedule)
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM schedules WHERE id=$1", schedule)
	if !relationExists(t, ctx, c, schema, "schedules_due_idx") || !relationExists(t, ctx, c, schema, "runs_schedule_occurrence_idx") {
		t.Fatal("schedule indexes missing")
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 7), "m"); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if h := historyRows(t, ctx, c); len(h) != 7 {
		t.Fatalf("history %v", h)
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 6), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("0006 binary on 0007 schema: %v", err)
	}
}
