package migrations

import "testing"

// TC-266: The migration keeps old account IDs, sessions and ownership while
// the database enforces canonical and unique emails for new accounts.
func TestEmailAccountsMigrationKeepsLegacyOwnership(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx, conn := testCtx(t), open()
	if err := applyFS(ctx, conn, embeddedPrefix(t, 14), "m"); err != nil {
		t.Fatal(err)
	}
	owner, linkUser, project, session := newID(), newID(), newID(), newID()
	mustExec(t, ctx, conn, `INSERT INTO users(id,username,is_admin,password_kdf,password_iterations,password_salt,password_hash,password_set_at)
		VALUES($1,'legacy',true,'pbkdf2-sha256',1000,$2,$3,now())`, owner, make([]byte, 16), make([]byte, 32))
	mustExec(t, ctx, conn, "INSERT INTO users(id) VALUES($1)", linkUser)
	mustExec(t, ctx, conn, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, owner, digest('a'))
	mustExec(t, ctx, conn, "INSERT INTO auth_sessions(id,user_id,token_sha256,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", session, linkUser, digest('b'))
	if err := applyFS(ctx, conn, embeddedPrefix(t, 15), "m"); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN projects p ON p.owner_id=u.id WHERE p.id=$1 AND u.id=$2 AND
		u.email IS NULL AND u.username='legacy' AND u.is_admin AND u.password_hash=$3) AND
		EXISTS(SELECT 1 FROM users u JOIN auth_sessions s ON s.user_id=u.id WHERE s.id=$4 AND u.id=$5 AND
		u.email IS NULL AND u.username IS NULL AND s.revoked_at IS NULL)`, project, owner, make([]byte, 32), session, linkUser).Scan(&preserved)
	if err != nil || !preserved {
		t.Fatalf("legacy ownership or credentials changed: %t %v", preserved, err)
	}
	const insert = "INSERT INTO users(id,email) VALUES($1,$2)"
	for _, invalid := range []string{"UPPER@example.test", " edge@example.test ", "no-domain", "invalid..email@example.test"} {
		execState(t, ctx, conn, "23514", insert, newID(), invalid)
	}
	id := newID()
	mustExec(t, ctx, conn, insert, id, "valid+test@example.test")
	execState(t, ctx, conn, "23505", insert, newID(), "valid+test@example.test")
	execState(t, ctx, conn, SQLStateImmutable, "UPDATE users SET email='changed@example.test' WHERE id=$1", id)
}
