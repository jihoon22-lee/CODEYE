package migrations

import (
	"errors"
	"testing"
)

// TestLinkThrottleMigration applies 0001-0010 with throttle counters, an
// unused login link and a live session, then 0011: the link is retired (its
// issuer is unknown), the session stays unattributed and ungated; link
// issuer and purpose, a session's issuer, and a session gaining a password
// requirement are immutable; the reset record keeps its form; the new audit
// action is accepted. The old counters are gone and the new table takes the
// three login scopes with a required last failure time and no stored lock
// end. A 0010 binary refuses the result.
// TC-153
func TestLinkThrottleMigration(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 10), "m"); err != nil {
		t.Fatalf("apply 0001-0010: %v", err)
	}
	mustExec(t, ctx, c, "INSERT INTO auth_throttle(scope,key_hmac,failures,last_failure_at,locked_until) VALUES('account',$1,9,now(),now()+interval '1 minute')", digest('a'))
	user, admin, session := newID(), newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id,username) VALUES($1,'erin'),($2,'root')", user, admin)
	mustExec(t, ctx, c, "INSERT INTO auth_launch_tokens(token_sha256,user_id,expires_at) VALUES($1,$2,now()+interval '10 minutes')", digest('e'), user)
	mustExec(t, ctx, c, "INSERT INTO auth_sessions(id,user_id,token_sha256,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", session, user, digest('f'))

	if err := applyFS(ctx, c, embeddedPrefix(t, 11), "m"); err != nil {
		t.Fatalf("apply 0011 on populated schema: %v", err)
	}
	// A link issued before issuers were recorded no longer signs in; the
	// session stays, unattributed and ungated.
	var retired bool
	var purpose string
	if err := c.QueryRow(ctx, "SELECT used_at IS NOT NULL,purpose FROM auth_launch_tokens WHERE token_sha256=$1", digest('e')).Scan(&retired, &purpose); err != nil || !retired || purpose != "login" {
		t.Fatalf("pre-0011 link: retired %v purpose %q %v", retired, purpose, err)
	}
	var live, gated bool
	if err := c.QueryRow(ctx, "SELECT revoked_at IS NULL,password_required OR link_issued_by IS NOT NULL FROM auth_sessions WHERE id=$1", session).Scan(&live, &gated); err != nil || !live || gated {
		t.Fatalf("pre-0011 session: live %v gated %v %v", live, gated, err)
	}
	mustExec(t, ctx, c, "INSERT INTO auth_launch_tokens(token_sha256,user_id,expires_at,issued_by,purpose) VALUES($1,$2,now()+interval '10 minutes',$3,'password_reset')", digest('7'), user, admin)
	execState(t, ctx, c, "23514", "INSERT INTO auth_launch_tokens(token_sha256,user_id,expires_at,purpose) VALUES($1,$2,now()+interval '1 minute','invite')", digest('8'), user)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_launch_tokens SET issued_by=NULL WHERE token_sha256=$1", digest('7'))
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_launch_tokens SET purpose='login' WHERE token_sha256=$1", digest('7'))
	mustExec(t, ctx, c, "UPDATE auth_launch_tokens SET used_at=now() WHERE token_sha256=$1", digest('7'))
	gatedSession := newID()
	mustExec(t, ctx, c, "INSERT INTO auth_sessions(id,user_id,token_sha256,expires_at,link_issued_by,password_required) VALUES($1,$2,$3,now()+interval '1 hour',$4,true)", gatedSession, user, digest('9'), admin)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_sessions SET link_issued_by=NULL WHERE id=$1", gatedSession)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_sessions SET password_required=true WHERE id=$1", session)
	mustExec(t, ctx, c, "UPDATE auth_sessions SET password_required=false WHERE id=$1", gatedSession)
	execState(t, ctx, c, "23514", "UPDATE users SET password_reset_by=$2 WHERE id=$1", user, admin)
	execState(t, ctx, c, "23514", "UPDATE users SET password_reset_acknowledged_at=now() WHERE id=$1", user)
	mustExec(t, ctx, c, "UPDATE users SET password_reset_at=now(),password_reset_by=$2,password_reset_acknowledged_at=now() WHERE id=$1", user, admin)
	insert := "INSERT INTO audit_events(actor_kind,actor_user_id,action) VALUES('user',$1,$2)"
	mustExec(t, ctx, c, insert, user, "auth.password_reset_acknowledged")
	execState(t, ctx, c, "23514", insert, user, "user.deleted")
	var left int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM auth_throttle").Scan(&left); err != nil || left != 0 {
		t.Fatalf("old counters kept: %d %v", left, err)
	}
	var cols int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='auth_throttle' AND column_name='locked_until'", schema).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("stored lock end remains: %d %v", cols, err)
	}
	insert = "INSERT INTO auth_throttle(scope,key_hmac,failures,last_failure_at) VALUES($1,$2,$3,now())"
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
