package migrations

import (
	"errors"
	"testing"
)

// TestFindingReviewMigrationAddsReviewsOnly applies 0001-0007 with a project and run,
// then 0008: existing rows are untouched (0008 only adds finding_reviews), the
// review constraints hold before any foreign key is consulted, a review must
// name a published report, versions start at 1, and a 0007 binary refuses the
// result. Immutability and the full write path are in the storage tests
// (TestFindingReviews).
func TestFindingReviewMigrationAddsReviewsOnly(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 7), "m"); err != nil {
		t.Fatalf("apply 0001-0007: %v", err)
	}
	user, project, run := newID(), newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", user)
	mustExec(t, ctx, c, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, user, digest('a'))
	mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status) VALUES($1,$2,$3,'k',$4,1,'completed')", run, project, user, digest('b'))

	if err := applyFS(ctx, c, embeddedPrefix(t, 8), "m"); err != nil {
		t.Fatalf("apply 0008 on populated schema: %v", err)
	}
	var legacy int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM runs r JOIN projects p ON p.id=r.project_id WHERE r.id=$1 AND r.status='completed' AND p.current_revision=1", run).Scan(&legacy); err != nil || legacy != 1 {
		t.Fatalf("legacy rows changed: %d %v", legacy, err)
	}
	fp := "fp2:" + digest('c')
	insert := `INSERT INTO finding_reviews(id,project_id,fingerprint,version,disposition,reason,path_prefix,expires_at,actor_id,run_id,report_revision,finding_id,idempotency_key,request_digest)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,'fnd_1',$11,$12)`
	review := func(state, fingerprint string, version int, disposition, reason string, prefix, expires any) {
		t.Helper()
		execState(t, ctx, c, state, insert, newID(), project, fingerprint, version, disposition, reason, prefix, expires, user, run, newID(), digest('d'))
	}
	review("23514", "go-vet-fp-v1:abc", 1, "suppressed", "이유", nil, nil)    // fp2 only
	review("23514", fp, 1, "suppressed", "   ", nil, nil)                   // reason required
	review("23514", fp, 1, "ignored", "이유", nil, nil)                       // disposition enum
	review("23514", fp, 1, "open", "이유", "src/", nil)                       // open has no scope
	review("23514", fp, 1, "open", "이유", nil, "2099-01-01T00:00:00Z")       // nor expiry
	review("23514", fp, 1, "suppressed", "이유", "../x", nil)                 // snapshot-relative path
	review("23514", fp, 1, "suppressed", "이유", nil, "2000-01-01T00:00:00Z") // expiry after creation
	review(SQLStateCounterDecrease, fp, 2, "suppressed", "이유", nil, nil)    // versions start at 1
	review("23503", fp, 1, "suppressed", "이유", nil, nil)                    // a published report is required
	if !relationExists(t, ctx, c, schema, "finding_reviews_run_idx") || !relationExists(t, ctx, c, schema, "finding_reviews_actor_idx") {
		t.Fatal("finding review indexes missing")
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 8), "m"); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if h := historyRows(t, ctx, c); len(h) != 8 {
		t.Fatalf("history %v", h)
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 7), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("0007 binary on 0008 schema: %v", err)
	}
}
