package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"testing/fstest"
)

// embeddedPrefix returns an FS holding only the first n embedded migrations,
// i.e. what an older binary would apply.
func embeddedPrefix(t *testing.T, n int) fstest.MapFS {
	t.Helper()
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for _, m := range ms[:n] {
		name := "m/" + pad4(m.Version) + "_" + m.Name + ".sql"
		fsys[name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	return fsys
}

func pad4(v int) string {
	s := "000" + itoa(v)
	return s[len(s)-4:]
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestW06MigrationFromPopulatedW03Schema applies 0001-0002, fills rows the way
// W02/W03 wrote them, then applies 0003 and checks the legacy data handling.
func TestW06MigrationFromPopulatedW03Schema(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 2), "m"); err != nil {
		t.Fatalf("apply 0001-0002: %v", err)
	}
	user, project := newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", user)
	mustExec(t, ctx, c, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, user, digest('a'))
	// W03 stored only the canonical request digest. One reconstructible body,
	// one scope=paths body that cannot be reconstructed.
	known := `{"profile":"with-tests","scope":{"kind":"full"},"source_policy":"latest_at_acquire"}`
	unknown := `{"profile":"deep-review","scope":{"kind":"paths","paths":["a"]},"source_policy":"latest_at_acquire"}`
	knownRun, unknownRun := newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status) VALUES($1,$2,$3,'k1',$4,1,'queued')", knownRun, project, user, sha(known))
	mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status) VALUES($1,$2,$3,'k2',$4,1,'queued')", unknownRun, project, user, sha(unknown))
	// Legacy Git snapshot whose digest is a commit, and a W02 attempt with an
	// unregistered node ID.
	gitSnap, localSnap := newID(), newID()
	commit := randomHex(20)
	mustExec(t, ctx, c, "INSERT INTO snapshots(id,project_id,digest,consistency) VALUES($1,$2,$3,'git_commit')", gitSnap, project, commit)
	mustExec(t, ctx, c, "INSERT INTO snapshots(id,project_id,digest,consistency) VALUES($1,$2,$3,'stable_copy_best_effort')", localSnap, project, digest('b'))
	mustExec(t, ctx, c, "INSERT INTO attempts(id,run_id,node_id,worker_id,number,epoch,expires_at,status,cleanup_state) VALUES($1,$2,$3,$4,1,1,now(),'released','confirmed')", newID(), knownRun, newID(), newID())

	if err := Apply(ctx, c); err != nil {
		t.Fatalf("apply 0003 on populated schema: %v", err)
	}
	var restored string
	if err := c.QueryRow(ctx, "SELECT request::text FROM runs WHERE id=$1", knownRun).Scan(&restored); err != nil {
		t.Fatalf("known request not restored: %v", err)
	}
	if restored != `{"scope": {"kind": "full"}, "profile": "with-tests", "source_policy": "latest_at_acquire"}` {
		t.Fatalf("restored %s", restored)
	}
	var stillNull bool
	if err := c.QueryRow(ctx, "SELECT request IS NULL FROM runs WHERE id=$1", unknownRun).Scan(&stillNull); err != nil || !stillNull {
		t.Fatalf("unreconstructible request was guessed: %v", err)
	}
	var manifest, source *string
	if err := c.QueryRow(ctx, "SELECT manifest_digest,source_sha FROM snapshots WHERE id=$1", gitSnap).Scan(&manifest, &source); err != nil || manifest != nil || source != nil {
		t.Fatalf("legacy git snapshot rewritten: %v %v %v", manifest, source, err)
	}
	// Restored request is fixed once stored.
	execState(t, ctx, c, SQLStateImmutable, "UPDATE runs SET request='{}' WHERE id=$1", knownRun)
	// A new W06 git snapshot must carry the commit separately.
	execState(t, ctx, c, "23514", "INSERT INTO snapshots(id,project_id,digest,consistency,manifest_digest) VALUES($1,$2,$3,'git_commit',$3)", newID(), project, digest('c'))
	execState(t, ctx, c, "23514", "INSERT INTO snapshots(id,project_id,digest,consistency,manifest_digest) VALUES($1,$2,$3,'stable_copy_best_effort',$4)", newID(), project, digest('c'), digest('d'))
	mustExec(t, ctx, c, "INSERT INTO snapshots(id,project_id,digest,consistency,manifest_digest,source_sha) VALUES($1,$2,$3,'git_commit',$3,$4)", newID(), project, digest('e'), commit)
	// Reapply is a no-op; the seeded scheduler limit exists once.
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	var limits int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM scheduler_limits").Scan(&limits); err != nil || limits != 1 {
		t.Fatalf("scheduler_limits rows %d %v", limits, err)
	}
	if h := historyRows(t, ctx, c); len(h) != 3 {
		t.Fatalf("history %v", h)
	}
	// An older (0001-0002) binary refuses the migrated schema without changes.
	if err := applyFS(ctx, c, embeddedPrefix(t, 2), "m"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("old binary on new schema: %v", err)
	}
}

// The restore SQL must use exactly the canonical bytes W03 hashed.
func TestW06RequestBackfillMatchesCanonicalForms(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 2), "m"); err != nil {
		t.Fatal(err)
	}
	user, project := newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users(id) VALUES($1)", user)
	mustExec(t, ctx, c, "WITH p AS (INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,'p',1) RETURNING id) INSERT INTO project_revisions(project_id,revision,config,config_digest) SELECT id,1,'{}',$3 FROM p", project, user, digest('a'))
	for i, profile := range []string{"deep-review", "with-tests", "focused"} {
		body := `{"profile":"` + profile + `","scope":{"kind":"full"},"source_policy":"latest_at_acquire"}`
		mustExec(t, ctx, c, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status) VALUES($1,$2,$3,$4,$5,1,'queued')", newID(), project, user, "k"+itoa(i), sha(body))
	}
	if err := Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	var restored int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM runs WHERE request IS NOT NULL AND request->>'source_policy'='latest_at_acquire'").Scan(&restored); err != nil || restored != 3 {
		t.Fatalf("restored %d %v", restored, err)
	}
}

// A failing statement inside 0003 rolls back the whole application.
func TestW06MigrationFailureRollsBack(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := applyFS(ctx, c, embeddedPrefix(t, 2), "m"); err != nil {
		t.Fatal(err)
	}
	broken := embeddedPrefix(t, 3)
	for name, f := range broken {
		if name[2:6] == "0003" {
			broken[name] = &fstest.MapFile{Data: append(append([]byte{}, f.Data...), []byte("\nSELECT 1/0;\n")...)}
		}
	}
	expectState(t, applyFS(ctx, c, broken, "m"), "22012")
	if relationExists(t, ctx, c, schema, "nodes") {
		t.Fatal("partial 0003 committed")
	}
	if h := historyRows(t, ctx, c); len(h) != 2 {
		t.Fatalf("history %v", h)
	}
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("apply after failure: %v", err)
	}
}
