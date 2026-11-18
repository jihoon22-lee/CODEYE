package migrations

import (
	"errors"
	"testing"
	"testing/fstest"
)

// TestW07MigrationFromPopulatedW06Schema applies 0001-0003, fills pool, node,
// worker and command rows the way W06 wrote them, then applies 0004 and checks
// the defaults, the new guards and that an 0003 binary refuses the result.
func TestW07MigrationFromPopulatedW06Schema(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 3), "m"); err != nil {
		t.Fatalf("apply 0001-0003: %v", err)
	}
	pool, token, node, worker := newID(), newID(), newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO worker_pools(id,name) VALUES($1,'default')", pool)
	mustExec(t, ctx, c, "INSERT INTO node_enrollment_tokens(id,token_sha256,pool_id,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", token, digest('a'), pool)
	mustExec(t, ctx, c, "INSERT INTO nodes(id,pool_id,enrollment_token_id,name,secret_sha256,os,arch,protocol_version) VALUES($1,$2,$3,'n',$4,'linux','amd64','0.1')", node, pool, token, digest('b'))
	mustExec(t, ctx, c, "INSERT INTO workers(id,node_id,state) VALUES($1,$2,'idle')", worker, node)
	mustExec(t, ctx, c, "UPDATE scheduler_limits SET global_max_running=3")

	if err := Apply(ctx, c); err != nil {
		t.Fatalf("apply 0004 on populated schema: %v", err)
	}
	var minW, maxW, idle, up, down, rev int
	if err := c.QueryRow(ctx, "SELECT revision,min_workers,max_workers,idle_timeout_seconds,scale_up_cooldown_seconds,scale_down_cooldown_seconds FROM pool_policies WHERE pool_id=$1", pool).
		Scan(&rev, &minW, &maxW, &idle, &up, &down); err != nil || rev != 1 || minW != 0 || maxW != 2 || idle != 180 || up != 10 || down != 60 {
		t.Fatalf("default policy %d %d %d %d %d %d %v", rev, minW, maxW, idle, up, down, err)
	}
	var global, project, states int
	if err := c.QueryRow(ctx, "SELECT global_max_running,project_max_running,(SELECT count(*) FROM pool_scaler_state WHERE pool_id=$1) FROM scheduler_limits", pool).Scan(&global, &project, &states); err != nil || global != 3 || project != 1 || states != 1 {
		t.Fatalf("limits %d %d state rows %d %v", global, project, states, err)
	}
	var scaling string
	var maxNode *int
	if err := c.QueryRow(ctx, "SELECT worker_scaling,max_workers FROM nodes WHERE id=$1", node).Scan(&scaling, &maxNode); err != nil || scaling != "static" || maxNode != nil {
		t.Fatalf("existing node %s %v %v", scaling, maxNode, err)
	}
	var hasSince bool
	if err := c.QueryRow(ctx, "SELECT state_since IS NOT NULL FROM workers WHERE id=$1", worker).Scan(&hasSince); err != nil || !hasSince {
		t.Fatalf("worker state_since %v", err)
	}
	// Policy revisions are append-only and min <= max.
	execState(t, ctx, c, SQLStateImmutable, "UPDATE pool_policies SET max_workers=5 WHERE pool_id=$1", pool)
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM pool_policies WHERE pool_id=$1", pool)
	execState(t, ctx, c, "23514", "INSERT INTO pool_policies(pool_id,revision,min_workers,max_workers,idle_timeout_seconds,scale_up_cooldown_seconds,scale_down_cooldown_seconds) VALUES($1,2,3,2,1,0,0)", pool)
	// Autoscaler epoch and limits revision never decrease; state rows stay.
	mustExec(t, ctx, c, "UPDATE pool_scaler_state SET epoch=5 WHERE pool_id=$1", pool)
	execState(t, ctx, c, SQLStateCounterDecrease, "UPDATE pool_scaler_state SET epoch=4 WHERE pool_id=$1", pool)
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM pool_scaler_state WHERE pool_id=$1", pool)
	execState(t, ctx, c, SQLStateCounterDecrease, "UPDATE scheduler_limits SET revision=0")
	// A controller-scaled node must declare its local cap; worker commands need a worker.
	execState(t, ctx, c, "23514", "UPDATE nodes SET worker_scaling='controller' WHERE id=$1", node)
	mustExec(t, ctx, c, "UPDATE nodes SET worker_scaling='controller',max_workers=2 WHERE id=$1", node)
	execState(t, ctx, c, "23514", "INSERT INTO node_commands(id,node_id,sequence,kind) VALUES($1,$2,1,'start_worker')", newID(), node)
	// Reapply is a no-op; an 0001-0003 binary refuses the 0004 schema unchanged.
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if h := historyRows(t, ctx, c); len(h) != 4 {
		t.Fatalf("history %v", h)
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 3), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("0003 binary on 0004 schema: %v", err)
	}
}

// A failing statement inside 0004 rolls back the whole application.
func TestW07MigrationFailureRollsBack(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 3), "m"); err != nil {
		t.Fatal(err)
	}
	broken := embeddedPrefix(t, 4)
	for name, f := range broken {
		if name[2:6] == "0004" {
			broken[name] = &fstest.MapFile{Data: append(append([]byte{}, f.Data...), []byte("\nSELECT 1/0;\n")...)}
		}
	}
	expectState(t, applyFS(ctx, c, broken, "m"), "22012")
	if relationExists(t, ctx, c, schema, "pool_policies") {
		t.Fatal("partial 0004 committed")
	}
	if h := historyRows(t, ctx, c); len(h) != 3 {
		t.Fatalf("history %v", h)
	}
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("apply after failure: %v", err)
	}
}
