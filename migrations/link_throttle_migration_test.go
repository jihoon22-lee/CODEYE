package migrations

import (
	"errors"
	"testing"
)

// TestLinkThrottleMigration applies 0001-0010 with throttle counters, then
// 0011: the old counters are gone and the new table takes the three login
// scopes with a required last failure time and no stored lock end. A 0010
// binary refuses the result.
// TC-153
func TestLinkThrottleMigration(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 10), "m"); err != nil {
		t.Fatalf("apply 0001-0010: %v", err)
	}
	mustExec(t, ctx, c, "INSERT INTO auth_throttle(scope,key_hmac,failures,last_failure_at,locked_until) VALUES('account',$1,9,now(),now()+interval '1 minute')", digest('a'))

	if err := applyFS(ctx, c, embeddedPrefix(t, 11), "m"); err != nil {
		t.Fatalf("apply 0011 on populated schema: %v", err)
	}
	var left int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM auth_throttle").Scan(&left); err != nil || left != 0 {
		t.Fatalf("old counters kept: %d %v", left, err)
	}
	var cols int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='auth_throttle' AND column_name='locked_until'", schema).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("stored lock end remains: %d %v", cols, err)
	}
	insert := "INSERT INTO auth_throttle(scope,key_hmac,failures,last_failure_at) VALUES($1,$2,$3,now())"
	for _, scope := range []string{"pair", "account", "client"} {
		mustExec(t, ctx, c, insert, scope, digest('b'), 1)
	}
	execState(t, ctx, c, "23514", insert, "user", digest('c'), 1)
	execState(t, ctx, c, "23514", insert, "pair", digest('c'), -1)
	execState(t, ctx, c, "23502", "INSERT INTO auth_throttle(scope,key_hmac,failures) VALUES('pair',$1,1)", digest('d'))
	if !relationExists(t, ctx, c, schema, "auth_throttle_last_failure_idx") {
		t.Fatal("reap index missing")
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 11), "m"); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if h := historyRows(t, ctx, c); len(h) != 11 {
		t.Fatalf("history %v", h)
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 10), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("0010 binary on 0011 schema: %v", err)
	}
}
