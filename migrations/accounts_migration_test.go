package migrations

import (
	"errors"
	"testing"
	"testing/fstest"
)

// TestAccountsMigrationKeepsLoginLinkUsers applies 0001-0009 with users (one
// named, one disabled), a project and a live session, then 0010: existing
// users become login-link users (no username, no password, not
// administrators, no forced change) and keep their sessions; the account
// columns follow their all-or-none and form rules; usernames are unique and
// set once; the audit table is append-only with a closed vocabulary; the
// throttle key exists once and is immutable. A 0009 binary refuses the
// result.
// TC-153
func TestAccountsMigrationKeepsLoginLinkUsers(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 9), "m"); err != nil {
		t.Fatalf("apply 0001-0009: %v", err)
	}
	named, disabled, project, session := newID(), newID(), newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id,display_name) VALUES($1,'admin')", named)
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", disabled)
	mustExec(t, ctx, c, "UPDATE users SET disabled_at=now() WHERE id=$1", disabled)
	mustExec(t, ctx, c, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, named, digest('a'))
	mustExec(t, ctx, c, "INSERT INTO auth_sessions(id,user_id,token_sha256,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", session, named, digest('b'))

	if err := applyFS(ctx, c, embeddedPrefix(t, 10), "m"); err != nil {
		t.Fatalf("apply 0010 on populated schema: %v", err)
	}
	var legacy int
	if err := c.QueryRow(ctx, `SELECT count(*) FROM users WHERE username IS NULL AND NOT is_admin AND NOT must_change_password
		AND password_kdf IS NULL AND password_hash IS NULL AND id IN ($1,$2)`, named, disabled).Scan(&legacy); err != nil || legacy != 2 {
		t.Fatalf("legacy users changed: %d %v", legacy, err)
	}
	var live int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM auth_sessions WHERE id=$1 AND revoked_at IS NULL", session).Scan(&live); err != nil || live != 1 {
		t.Fatalf("session changed: %d %v", live, err)
	}

	// Account columns.
	salt, hash := "\\x"+digest('1')[:32], "\\x"+digest('2')
	execState(t, ctx, c, "23514", "UPDATE users SET username='Upper' WHERE id=$1", named)             // stored lower-case
	execState(t, ctx, c, "23514", "UPDATE users SET username='a' WHERE id=$1", named)                 // 2..64
	execState(t, ctx, c, "23514", "UPDATE users SET must_change_password=true WHERE id=$1", named)    // needs a username
	execState(t, ctx, c, "23514", "UPDATE users SET password_kdf='pbkdf2-sha256' WHERE id=$1", named) // all or none
	mustExec(t, ctx, c, "UPDATE users SET username='alice' WHERE id=$1", named)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE users SET username='alice2' WHERE id=$1", named) // set once
	execState(t, ctx, c, "23505", "UPDATE users SET username='alice' WHERE id=$1", disabled)         // unique
	execState(t, ctx, c, "23514", "UPDATE users SET password_kdf='md5',password_iterations=1000,password_salt=$2,password_hash=$3,password_set_at=now() WHERE id=$1", named, salt, hash)
	execState(t, ctx, c, "23514", "UPDATE users SET password_kdf='pbkdf2-sha256',password_iterations=999,password_salt=$2,password_hash=$3,password_set_at=now() WHERE id=$1", named, salt, hash)
	mustExec(t, ctx, c, "UPDATE users SET password_kdf='pbkdf2-sha256',password_iterations=600000,password_salt=$2,password_hash=$3,password_set_at=now(),must_change_password=true,is_admin=true WHERE id=$1", named, salt, hash)

	// Audit rows: closed vocabulary, actor shape, append-only.
	insert := "INSERT INTO audit_events(actor_kind,actor_user_id,action,target_type,target_id,project_id,request_id,detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8)"
	execState(t, ctx, c, "23514", insert, "user", named, "user.deleted", nil, nil, nil, nil, "{}")
	execState(t, ctx, c, "23514", insert, "user", nil, "auth.logout", nil, nil, nil, nil, "{}")            // user actor needs an id
	execState(t, ctx, c, "23514", insert, "operator_cli", named, "user.created", nil, nil, nil, nil, "{}") // operator has none
	execState(t, ctx, c, "23514", insert, "node", nil, "node.enrolled", nil, nil, nil, nil, "{}")          // node needs a node
	execState(t, ctx, c, "23514", insert, "user", named, "auth.logout", "session", nil, nil, nil, "{}")    // target pair
	execState(t, ctx, c, "23514", insert, "user", named, "auth.logout", nil, nil, nil, "req_x", "{}")      // request id form
	execState(t, ctx, c, "23514", insert, "user", named, "auth.logout", nil, nil, nil, nil, "[]")          // detail object
	execState(t, ctx, c, "23503", insert, "user", named, "run.requested", "run", "r", newID(), nil, "{}")  // project exists
	mustExec(t, ctx, c, insert, "user", named, "run.requested", "run", "r1", project, "req_0123456789abcdef01234567", `{"profile":"deep-review"}`)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE audit_events SET action='auth.logout'")
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM audit_events")
	execState(t, ctx, c, SQLStateImmutable, "TRUNCATE audit_events")

	var keys int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM auth_secrets WHERE octet_length(throttle_key)=32").Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("throttle key: %d %v", keys, err)
	}
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_secrets SET created_at=now()")
	execState(t, ctx, c, "23505", "INSERT INTO auth_secrets(id,throttle_key) SELECT 1,throttle_key FROM auth_secrets")
	execState(t, ctx, c, "23514", "INSERT INTO auth_throttle(scope,key_hmac) VALUES('user',$1)", digest('c'))
	for _, idx := range []string{"users_username_idx", "audit_events_time_idx", "audit_events_project_idx"} {
		if !relationExists(t, ctx, c, schema, idx) {
			t.Fatalf("index %s missing", idx)
		}
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 10), "m"); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if h := historyRows(t, ctx, c); len(h) != 10 {
		t.Fatalf("history %v", h)
	}
	if err := applyFS(ctx, c, embeddedPrefix(t, 9), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("0009 binary on 0010 schema: %v", err)
	}
}

// A failing statement inside 0010 rolls back the whole application: the
// users table keeps its 0009 shape and no audit table exists.
// TC-153
func TestAccountsMigrationFailureRollsBack(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 9), "m"); err != nil {
		t.Fatal(err)
	}
	user := newID()
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", user)
	broken := embeddedPrefix(t, 10)
	for name, f := range broken {
		if name[2:6] == "0010" {
			broken[name] = &fstest.MapFile{Data: append(append([]byte{}, f.Data...), []byte("\nSELECT 1/0;\n")...)}
		}
	}
	expectState(t, applyFS(ctx, c, broken, "m"), "22012")
	if relationExists(t, ctx, c, schema, "audit_events") || relationExists(t, ctx, c, schema, "auth_throttle") {
		t.Fatal("partial 0010 committed")
	}
	var cols int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='users' AND column_name='username'", schema).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("users altered by a failed migration: %d %v", cols, err)
	}
	if h := historyRows(t, ctx, c); len(h) != 9 {
		t.Fatalf("history %v", h)
	}
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("apply after failure: %v", err)
	}
	var n int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM users WHERE id=$1 AND username IS NULL", user).Scan(&n); err != nil || n != 1 {
		t.Fatalf("user after retry: %d %v", n, err)
	}
}
