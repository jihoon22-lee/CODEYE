package migrations

import (
	"errors"
	"testing"
)

// TestW15MigrationLeavesLegacyTasksUnstaged applies 0001-0005, writes task rows
// the way R0 workers did (with and without a key), then applies 0006: the rows
// keep NULL stage and times and their versions (no backfill, no inference from
// the key). The new stage is checked and immutable once set; an 0005 binary
// refuses the result.
func TestW15MigrationLeavesLegacyTasksUnstaged(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 5), "m"); err != nil {
		t.Fatalf("apply 0001-0005: %v", err)
	}
	user, project, run := newID(), newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", user)
	mustExec(t, ctx, c, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, user, digest('a'))
	mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status) VALUES($1,$2,$3,'k',$4,1,'running')", run, project, user, digest('b'))
	vet, plain := newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO tasks(id,run_id,status,required_for_verdict,key,version) VALUES($1,$2,'succeeded',true,'go-vet.root',3)", vet, run)
	mustExec(t, ctx, c, "INSERT INTO tasks(id,run_id,status,required_for_verdict,version) VALUES($1,$2,'running',false,2)", plain, run)

	if err := applyFS(ctx, c, embeddedPrefix(t, 6), "m"); err != nil {
		t.Fatalf("apply 0006 on populated schema: %v", err)
	}
	var unstaged, versions int
	if err := c.QueryRow(ctx, "SELECT count(*) FILTER (WHERE stage IS NULL AND started_at IS NULL AND finished_at IS NULL),sum(version) FROM tasks WHERE run_id=$1", run).Scan(&unstaged, &versions); err != nil || unstaged != 2 || versions != 5 {
		t.Fatalf("legacy rows changed: unstaged %d versions %d %v", unstaged, versions, err)
	}
	// Only the six task stages; source/report are server-derived, never stored.
	for _, bad := range []string{"source", "report", "lint", "Static_analysis", ""} {
		execState(t, ctx, c, "23514", "UPDATE tasks SET stage=$2 WHERE id=$1", plain, bad)
	}
	// A legacy row may be given its stage once; then it is immutable.
	mustExec(t, ctx, c, "UPDATE tasks SET stage='static_analysis' WHERE id=$1", vet)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE tasks SET stage='code_review' WHERE id=$1", vet)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE tasks SET stage=NULL WHERE id=$1", vet)
	// Times are ordinary nullable columns (Retry clears them).
	mustExec(t, ctx, c, "UPDATE tasks SET started_at=clock_timestamp(),finished_at=clock_timestamp() WHERE id=$1", plain)
	mustExec(t, ctx, c, "UPDATE tasks SET started_at=NULL,finished_at=NULL,version=version+1 WHERE id=$1", plain)
	if !relationExists(t, ctx, c, schema, "events_lifecycle_idx") {
		t.Fatal("lifecycle event index missing")
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 6), "m"); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if h := historyRows(t, ctx, c); len(h) != 6 {
		t.Fatalf("history %v", h)
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 5), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("0005 binary on 0006 schema: %v", err)
	}
}
