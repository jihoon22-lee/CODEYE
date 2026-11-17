package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 이 파일의 시험은 실제 PostgreSQL이 필요하다. CODEYE_TEST_DATABASE_URL이 없으면 skip하며,
// skip은 통과가 아니라 미실행이다. 각 시험은 고유 스키마를 만들고 search_path로 격리한다.

const testDBEnv = "CODEYE_TEST_DATABASE_URL"

func testURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv(testDBEnv)
	if url == "" {
		t.Skipf("%s not set: PostgreSQL integration test NOT executed", testDBEnv)
	}
	return url
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func digest(c byte) string { return strings.Repeat(string(c), 64) }

// isolatedSchema는 고유 스키마를 만들고, 그 스키마를 search_path로 쓰는 새 연결을 여는 함수를 반환한다.
func isolatedSchema(t *testing.T) (string, func() *pgx.Conn) {
	t.Helper()
	url := testURL(t)
	ctx := testCtx(t)
	schema := "codeye_mt_" + randomHex(6)
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	admin.Close(ctx)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), url)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("cleanup drop schema: %v", err)
		}
	})
	open := func() *pgx.Conn {
		t.Helper()
		cfg, err := pgx.ParseConfig(url)
		if err != nil {
			t.Fatalf("parse url: %v", err)
		}
		cfg.RuntimeParams["search_path"] = schema
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { c.Close(context.Background()) })
		return c
	}
	return schema, open
}

func historyRows(t *testing.T, ctx context.Context, c *pgx.Conn) []appliedMigration {
	t.Helper()
	rows, err := c.Query(ctx, "SELECT version, name, checksum FROM "+HistoryTable+" ORDER BY version")
	if err != nil {
		t.Fatalf("query history: %v", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[appliedMigration])
	if err != nil {
		t.Fatalf("collect history: %v", err)
	}
	return out
}

func relationExists(t *testing.T, ctx context.Context, c *pgx.Conn, schema, name string) bool {
	t.Helper()
	var exists bool
	if err := c.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", schema+"."+name).Scan(&exists); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	return exists
}

func expectState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want SQLSTATE %s", err, code)
	}
	if pgErr.Code != code {
		t.Fatalf("SQLSTATE = %s (%s), want %s", pgErr.Code, pgErr.Message, code)
	}
}

func mustExec(t *testing.T, ctx context.Context, c *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := c.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func execState(t *testing.T, ctx context.Context, c *pgx.Conn, code, sql string, args ...any) {
	t.Helper()
	_, err := c.Exec(ctx, sql, args...)
	expectState(t, err, code)
}

var coreTables = []string{
	"users", "projects", "project_members", "project_revisions", "snapshots", "runs",
	"attempts", "events", "tasks", "task_executions", "artifacts", "evidence",
	"auth_launch_tokens", "auth_sessions", "source_roots", "source_root_grants", "providers",
}

func TestApplyFreshThenReapply(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ms, _ := Embedded()
	h := historyRows(t, ctx, c)
	if len(h) != len(ms) {
		t.Fatalf("history rows = %d, want %d", len(h), len(ms))
	}
	for i := range ms {
		if h[i].Version != ms[i].Version || h[i].Checksum != ms[i].Checksum || h[i].Name != ms[i].Name {
			t.Fatalf("history[%d] = %+v, want %+v", i, h[i], ms[i])
		}
	}
	for _, tbl := range coreTables {
		if !relationExists(t, ctx, c, schema, tbl) {
			t.Fatalf("table %s missing in %s", tbl, schema)
		}
	}
	var before time.Time
	if err := c.QueryRow(ctx, "SELECT max(applied_at) FROM "+HistoryTable).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	var after time.Time
	if err := c.QueryRow(ctx, "SELECT max(applied_at) FROM "+HistoryTable).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if len(historyRows(t, ctx, c)) != len(ms) || !after.Equal(before) {
		t.Fatal("reapply changed history")
	}
	// 다른 연결(재접속)에서도 최신 상태로 인식한다.
	if err := Apply(ctx, open()); err != nil {
		t.Fatalf("reapply on new connection: %v", err)
	}
}

func TestApplyConcurrentConnections(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	const n = 8
	conns := make([]*pgx.Conn, n)
	for i := range conns {
		conns[i] = open()
	}
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = Apply(ctx, conns[i])
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Apply %d: %v", i, err)
		}
	}
	ms, _ := Embedded()
	if got := len(historyRows(t, ctx, conns[0])); got != len(ms) {
		t.Fatalf("history rows = %d, want %d", got, len(ms))
	}
}

func TestApplyFailedSQLRollsBackEverything(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	fsys := fstest.MapFS{
		"m/0001_first.sql":  {Data: []byte("CREATE TABLE first_table (id int);")},
		"m/0002_broken.sql": {Data: []byte("CREATE TABLE second_table (id int);\nSELECT 1/0;")},
	}
	err := applyFS(ctx, c, fsys, "m")
	expectState(t, err, "22012")
	for _, rel := range []string{HistoryTable, "first_table", "second_table"} {
		if relationExists(t, ctx, c, schema, rel) {
			t.Fatalf("%s exists after failed first Apply; rollback incomplete", rel)
		}
	}

	// 일부가 이미 적용된 상태에서 실패해도 기존 이력은 유지되고 새 변경은 남지 않는다.
	if err := applyFS(ctx, c, fstest.MapFS{"m/0001_first.sql": fsys["m/0001_first.sql"]}, "m"); err != nil {
		t.Fatalf("apply first only: %v", err)
	}
	expectState(t, applyFS(ctx, c, fsys, "m"), "22012")
	if h := historyRows(t, ctx, c); len(h) != 1 || h[0].Version != 1 {
		t.Fatalf("history after failed 0002 = %+v, want only version 1", h)
	}
	if relationExists(t, ctx, c, schema, "second_table") {
		t.Fatal("second_table exists after failed 0002")
	}
	if st := c.PgConn().TxStatus(); st != 'I' {
		t.Fatalf("connection left in tx status %q", st)
	}
}

func TestApplyCancelledContextRollsBack(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	fsys := fstest.MapFS{"m/0001_slow.sql": {Data: []byte("CREATE TABLE slow_table (id int);\nSELECT pg_sleep(30);")}}
	if err := applyFS(cctx, c, fsys, "m"); err == nil {
		t.Fatal("Apply with cancelled context succeeded")
	}
	// 취소 시 pgx는 연결을 닫을 수 있으므로 새 연결로 확인한다.
	v := open()
	for _, rel := range []string{HistoryTable, "slow_table"} {
		if relationExists(t, ctx, v, schema, rel) {
			t.Fatalf("%s exists after cancelled Apply", rel)
		}
	}
}

func TestApplyRejectsChecksumDrift(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	orig := fstest.MapFS{"m/0001_a.sql": {Data: []byte("CREATE TABLE drift_a (id int);")}}
	if err := applyFS(ctx, c, orig, "m"); err != nil {
		t.Fatal(err)
	}
	changed := fstest.MapFS{
		"m/0001_a.sql": {Data: []byte("CREATE TABLE drift_a (id bigint);")},
		"m/0002_b.sql": {Data: []byte("CREATE TABLE drift_b (id int);")},
	}
	if err := applyFS(ctx, c, changed, "m"); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Apply = %v, want ErrChecksumMismatch", err)
	}
	if relationExists(t, ctx, c, schema, "drift_b") || len(historyRows(t, ctx, c)) != 1 {
		t.Fatal("drift refusal still applied later migration")
	}

	// 내장 migration 이력 변조도 감지한다.
	_, open2 := isolatedSchema(t)
	c2 := open2()
	if err := Apply(ctx, c2); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, c2, "UPDATE "+HistoryTable+" SET checksum = $1 WHERE version = 1", digest('0'))
	if err := Apply(ctx, c2); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Apply = %v, want ErrChecksumMismatch", err)
	}
}

func TestApplyRejectsUnknownFutureVersion(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	latest, _ := LatestVersion()
	mustExec(t, ctx, c, "INSERT INTO "+HistoryTable+" (version, name, checksum) VALUES ($1, 'from_newer_binary', $2)",
		latest+1, digest('f'))
	if err := Apply(ctx, c); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("Apply = %v, want ErrUnknownVersion", err)
	}
	if got := len(historyRows(t, ctx, c)); got != latest+1 {
		t.Fatalf("history rows changed to %d", got)
	}
}

func TestApplyRejectsMissingVersion(t *testing.T) {
	schema, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	fsys := fstest.MapFS{
		"m/0001_a.sql": {Data: []byte("CREATE TABLE miss_a (id int);")},
		"m/0002_b.sql": {Data: []byte("CREATE TABLE miss_b (id int);")},
		"m/0003_c.sql": {Data: []byte("CREATE TABLE miss_c (id int);")},
	}
	if err := applyFS(ctx, c, fstest.MapFS{"m/0001_a.sql": fsys["m/0001_a.sql"]}, "m"); err != nil {
		t.Fatal(err)
	}
	ms, _ := load(fsys, "m")
	mustExec(t, ctx, c, "INSERT INTO "+HistoryTable+" (version, name, checksum) VALUES (3, $1, $2)", ms[2].Name, ms[2].Checksum)
	if err := applyFS(ctx, c, fsys, "m"); !errors.Is(err, ErrMissingVersion) {
		t.Fatalf("Apply = %v, want ErrMissingVersion", err)
	}
	if relationExists(t, ctx, c, schema, "miss_b") {
		t.Fatal("missing-version refusal still applied 0002")
	}
}

func TestApplyRejectsTransactionEscape(t *testing.T) {
	for name, body := range map[string]string{
		"commit":       "CREATE TABLE esc_a (id int);\nCOMMIT;",
		"commit_begin": "CREATE TABLE esc_b (id int);\nCOMMIT;\nBEGIN;",
	} {
		t.Run(name, func(t *testing.T) {
			_, open := isolatedSchema(t)
			ctx := testCtx(t)
			c := open()
			err := applyFS(ctx, c, fstest.MapFS{"m/0001_escape.sql": {Data: []byte(body)}}, "m")
			if !errors.Is(err, ErrTransactionBroken) {
				t.Fatalf("Apply = %v, want ErrTransactionBroken", err)
			}
			if st := c.PgConn().TxStatus(); st != 'I' {
				t.Fatalf("connection left in tx status %q", st)
			}
			if len(historyRowsIfExists(t, ctx, c)) != 0 {
				t.Fatal("escaped migration was recorded as applied")
			}
		})
	}
}

func historyRowsIfExists(t *testing.T, ctx context.Context, c *pgx.Conn) []appliedMigration {
	t.Helper()
	var exists bool
	if err := c.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", HistoryTable).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		return nil
	}
	return historyRows(t, ctx, c)
}

func TestApplyRejectsUnusableConnection(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, c); !errors.Is(err, ErrConnectionState) {
		t.Fatalf("Apply in caller transaction = %v, want ErrConnectionState", err)
	}
	_ = tx.Rollback(ctx)

	cfg, err := pgx.ParseConfig(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = "codeye_missing_" + randomHex(6)
	nc, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close(ctx)
	if err := Apply(ctx, nc); !errors.Is(err, ErrConnectionState) {
		t.Fatalf("Apply with missing schema = %v, want ErrConnectionState", err)
	}
}

// ---------------------------------------------------------------------------
// schema 계약 시험
// ---------------------------------------------------------------------------

type fixture struct {
	ctx                   context.Context
	c                     *pgx.Conn
	owner, other          string
	projA, projB          string
	snapA, snapA2, snapB  string
	runA, runA2, runB     string
	attemptA, attemptA2   string
	taskA, taskA2         string
	artifactA, artifactA2 string
}

func createProject(t *testing.T, ctx context.Context, c *pgx.Conn, owner string) string {
	t.Helper()
	id := newID()
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "INSERT INTO projects (id, owner_id, name, current_revision) VALUES ($1, $2, 'p', 1)", id, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO project_revisions (project_id, revision, config, config_digest) VALUES ($1, 1, '{}', $2)", id, digest('c')); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'manager')", id, owner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit project: %v", err)
	}
	return id
}

func createRun(t *testing.T, f *fixture, project string, key string) string {
	t.Helper()
	id := newID()
	mustExec(t, f.ctx, f.c, `INSERT INTO runs (id, project_id, actor_id, idempotency_key, request_digest, project_revision, status)
		VALUES ($1, $2, $3, $4, $5, 1, 'queued')`, id, project, f.owner, key, digest('d'))
	return id
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	f := &fixture{ctx: ctx, c: c, owner: newID(), other: newID()}
	mustExec(t, ctx, c, "INSERT INTO users (id) VALUES ($1), ($2)", f.owner, f.other)
	f.projA = createProject(t, ctx, c, f.owner)
	f.projB = createProject(t, ctx, c, f.owner)
	f.snapA, f.snapA2, f.snapB = newID(), newID(), newID()
	mustExec(t, ctx, c, `INSERT INTO snapshots (id, project_id, digest, consistency) VALUES
		($1, $4, $5, 'stable_copy_best_effort'), ($2, $4, $6, 'stable_copy_best_effort'), ($3, $7, $5, 'immutable_commit')`,
		f.snapA, f.snapA2, f.snapB, f.projA, strings.Repeat("a", 40), digest('b'), f.projB)
	f.runA = createRun(t, f, f.projA, "key-a")
	f.runA2 = createRun(t, f, f.projA, "key-a2")
	f.runB = createRun(t, f, f.projB, "key-b")
	mustExec(t, ctx, c, "UPDATE runs SET snapshot_id = $1 WHERE id = $2", f.snapA, f.runA)
	f.attemptA, f.attemptA2 = newID(), newID()
	mustExec(t, ctx, c, `INSERT INTO attempts (id, run_id, node_id, worker_id, number, epoch, expires_at, status)
		VALUES ($1, $2, $3, $4, 1, 1, now() + interval '1 minute', 'leased')`, f.attemptA, f.runA, newID(), newID())
	mustExec(t, ctx, c, `INSERT INTO attempts (id, run_id, node_id, worker_id, number, epoch, expires_at, status)
		VALUES ($1, $2, $3, $4, 1, 1, now() + interval '1 minute', 'leased')`, f.attemptA2, f.runA2, newID(), newID())
	f.taskA, f.taskA2 = newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO tasks (id, run_id, status, required_for_verdict) VALUES ($1, $2, 'queued', true)", f.taskA, f.runA)
	mustExec(t, ctx, c, "INSERT INTO tasks (id, run_id, status, required_for_verdict) VALUES ($1, $2, 'queued', true)", f.taskA2, f.runA2)
	f.artifactA, f.artifactA2 = newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO artifacts (id, project_id, run_id, sha256, size_bytes) VALUES ($1, $2, $3, $4, 10)", f.artifactA, f.projA, f.runA, digest('e'))
	mustExec(t, ctx, c, "INSERT INTO artifacts (id, project_id, run_id, sha256, size_bytes) VALUES ($1, $2, $3, $4, 10)", f.artifactA2, f.projA, f.runA2, digest('e'))
	return f
}

func TestSchemaProjectsAndRevisions(t *testing.T) {
	f := newFixture(t)
	ctx, c := f.ctx, f.c

	// project만 있고 current revision 행이 없으면 commit 시 거부된다.
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO projects (id, owner_id, name, current_revision) VALUES ($1, $2, 'orphan', 1)", newID(), f.owner); err != nil {
		t.Fatalf("deferred FK must allow insert before revision: %v", err)
	}
	expectState(t, tx.Commit(ctx), "23503")

	// 서비스 순서: project counter 증가 후 새 revision 삽입(같은 트랜잭션).
	tx, err = c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE projects SET current_revision = 2 WHERE id = $1", f.projA); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO project_revisions (project_id, revision, config, config_digest) VALUES ($1, 2, '{\"a\":1}', $2)", f.projA, digest('1')); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("revise project: %v", err)
	}

	execState(t, ctx, c, SQLStateImmutable, "UPDATE project_revisions SET config = '{\"x\":1}' WHERE project_id = $1 AND revision = 1", f.projA)
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM project_revisions WHERE project_id = $1 AND revision = 1", f.projA)
	execState(t, ctx, c, SQLStateImmutable, "TRUNCATE project_revisions CASCADE")
	execState(t, ctx, c, SQLStateCounterDecrease, "UPDATE projects SET current_revision = 1 WHERE id = $1", f.projA)
	execState(t, ctx, c, "23514", "INSERT INTO project_revisions (project_id, revision, config, config_digest) VALUES ($1, 3, '{}', 'sha256:abc')", f.projA)
	execState(t, ctx, c, "23514", "INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'owner')", f.projA, f.other)
	mustExec(t, ctx, c, "INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'viewer')", f.projA, f.other)
	execState(t, ctx, c, "23503", "DELETE FROM users WHERE id = $1", f.other)
}

func TestSchemaRunsFixedInputs(t *testing.T) {
	f := newFixture(t)
	ctx, c := f.ctx, f.c

	// snapshot: 같은 값 재설정은 허용, 교체·해제는 거부.
	mustExec(t, ctx, c, "UPDATE runs SET snapshot_id = $1 WHERE id = $2", f.snapA, f.runA)
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE runs SET snapshot_id = $1 WHERE id = $2", f.snapA2, f.runA)
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE runs SET snapshot_id = NULL WHERE id = $1", f.runA)
	// 다른 project의 snapshot은 연결할 수 없다.
	execState(t, ctx, c, "23503", "UPDATE runs SET snapshot_id = $1 WHERE id = $2", f.snapB, f.runA2)
	// project_revision 변경 거부, 다른 project revision 참조 거부.
	mustExec(t, ctx, c, "INSERT INTO project_revisions (project_id, revision, config, config_digest) VALUES ($1, 2, '{}', $2)", f.projA, digest('2'))
	mustExec(t, ctx, c, "UPDATE projects SET current_revision = 2 WHERE id = $1", f.projA)
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE runs SET project_revision = 2 WHERE id = $1", f.runA)
	execState(t, ctx, c, "23503", `INSERT INTO runs (id, project_id, actor_id, idempotency_key, request_digest, project_revision, status)
		VALUES ($1, $2, $3, 'k-rev', $4, 2, 'queued')`, newID(), f.projB, f.owner, digest('d'))
	// 멱등 키 유일성.
	execState(t, ctx, c, "23505", `INSERT INTO runs (id, project_id, actor_id, idempotency_key, request_digest, project_revision, status)
		VALUES ($1, $2, $3, 'key-a', $4, 1, 'queued')`, newID(), f.projA, f.owner, digest('9'))
	// 상태 enum, 단조 증가, 종료 상태.
	execState(t, ctx, c, "23514", "UPDATE runs SET status = 'succeeded' WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE runs SET request_digest = $1 WHERE id = $2", digest('7'), f.runA)
	mustExec(t, ctx, c, "UPDATE runs SET version = 3, event_sequence = 5, lease_epoch = 2 WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateCounterDecrease, "UPDATE runs SET version = 2 WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateCounterDecrease, "UPDATE runs SET lease_epoch = 1 WHERE id = $1", f.runA)
	mustExec(t, ctx, c, "UPDATE runs SET status = 'recovery_required', version = 4 WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE runs SET status = 'queued', version = 5 WHERE id = $1", f.runA)
	mustExec(t, ctx, c, "UPDATE runs SET status = 'failed', version = 5 WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE runs SET status = 'completed', version = 6 WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE runs SET status = 'running', version = 6 WHERE id = $1", f.runA)
	// 재시도(failed/cancelled -> queued)는 모든 attempt가 정리 확인 후 released일 때만.
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE runs SET status = 'queued', version = 6 WHERE id = $1", f.runA)
	mustExec(t, ctx, c, "UPDATE attempts SET status = 'released', cleanup_state = 'confirmed' WHERE id = $1", f.attemptA)
	mustExec(t, ctx, c, "UPDATE runs SET status = 'queued', version = 6 WHERE id = $1", f.runA)
	// snapshot이 없는 run은 재시도할 수 없다.
	mustExec(t, ctx, c, "UPDATE runs SET status = 'cancelled', version = 2 WHERE id = $1", f.runA2)
	mustExec(t, ctx, c, "UPDATE attempts SET status = 'released', cleanup_state = 'confirmed' WHERE id = $1", f.attemptA2)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE runs SET status = 'queued', version = 3 WHERE id = $1", f.runA2)
	// completed/partial은 종료 상태다.
	mustExec(t, ctx, c, "UPDATE runs SET status = 'completed', version = 2 WHERE id = $1", f.runB)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE runs SET status = 'queued', version = 3 WHERE id = $1", f.runB)
	// 근거가 있는 run은 삭제할 수 없고 cascade도 없다.
	execState(t, ctx, c, "23503", "DELETE FROM runs WHERE id = $1", f.runA)
}

func TestSchemaAttemptOwnership(t *testing.T) {
	f := newFixture(t)
	ctx, c := f.ctx, f.c
	insert := func(number, epoch int, status string) error {
		_, err := c.Exec(ctx, `INSERT INTO attempts (id, run_id, node_id, worker_id, number, epoch, expires_at, status)
			VALUES ($1, $2, $3, $4, $5, $6, now() + interval '1 minute', $7)`, newID(), f.runA, newID(), newID(), number, epoch, status)
		return err
	}
	expectState(t, insert(2, 2, "leased"), "23505")   // 활성 attempt 하나
	expectState(t, insert(3, 3, "released"), "23514") // released는 cleanup confirmed 필요
	insertReleased := func(number, epoch int) error {
		_, err := c.Exec(ctx, `INSERT INTO attempts (id, run_id, node_id, worker_id, number, epoch, expires_at, status, cleanup_state)
			VALUES ($1, $2, $3, $4, $5, $6, now(), 'released', 'confirmed')`, newID(), f.runA, newID(), newID(), number, epoch)
		return err
	}
	expectState(t, insertReleased(1, 3), "23505") // 번호 중복
	expectState(t, insertReleased(3, 1), "23505") // epoch 중복
	execState(t, ctx, c, "23514", "UPDATE attempts SET status = 'released' WHERE id = $1", f.attemptA)
	mustExec(t, ctx, c, "UPDATE attempts SET status = 'recovery_required', cleanup_state = 'uncertain' WHERE id = $1", f.attemptA)
	expectState(t, insert(2, 2, "leased"), "23505") // recovery_required도 활성 소유권
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE attempts SET status = 'leased' WHERE id = $1", f.attemptA)
	mustExec(t, ctx, c, "UPDATE attempts SET cleanup_state = 'confirmed' WHERE id = $1", f.attemptA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE attempts SET cleanup_state = 'uncertain' WHERE id = $1", f.attemptA)
	mustExec(t, ctx, c, "UPDATE attempts SET status = 'released', completion_digest = $1, completion_status = 'failed' WHERE id = $2", digest('5'), f.attemptA)
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE attempts SET completion_digest = $1 WHERE id = $2", digest('6'), f.attemptA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE attempts SET status = 'leased' WHERE id = $1", f.attemptA)
	execState(t, ctx, c, "23514", "UPDATE attempts SET completion_status = 'failed' WHERE id = $1", f.attemptA2) // digest 없이 status만
	if err := insert(2, 2, "leased"); err != nil {
		t.Fatalf("new attempt after confirmed release: %v", err)
	}
	execState(t, ctx, c, SQLStateImmutable, "UPDATE attempts SET node_id = $1 WHERE id = $2", newID(), f.attemptA2)
}

func TestSchemaConcurrentActiveAttempt(t *testing.T) {
	f := newFixture(t)
	_, open := isolatedSchemaFor(t, f)
	const n = 6
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		conn := open()
		wg.Add(1)
		go func(i int, conn *pgx.Conn) {
			defer wg.Done()
			<-start
			_, errs[i] = conn.Exec(f.ctx, `INSERT INTO attempts (id, run_id, node_id, worker_id, number, epoch, expires_at, status)
				VALUES ($1, $2, $3, $4, $5, $5, now() + interval '1 minute', 'leased')`, newID(), f.runB, newID(), newID(), i+1)
		}(i, conn)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		expectState(t, err, "23505")
	}
	if ok != 1 {
		t.Fatalf("%d concurrent active attempts succeeded, want exactly 1", ok)
	}
	var active int
	if err := f.c.QueryRow(f.ctx, "SELECT count(*) FROM attempts WHERE run_id = $1", f.runB).Scan(&active); err != nil || active != 1 {
		t.Fatalf("attempt rows = %d, %v; want 1", active, err)
	}
}

// isolatedSchemaFor는 fixture 연결과 같은 search_path로 새 연결을 연다.
func isolatedSchemaFor(t *testing.T, f *fixture) (string, func() *pgx.Conn) {
	t.Helper()
	var schema string
	if err := f.c.QueryRow(f.ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema, func() *pgx.Conn {
		cfg, err := pgx.ParseConfig(testURL(t))
		if err != nil {
			t.Fatal(err)
		}
		cfg.RuntimeParams["search_path"] = schema
		c, err := pgx.ConnectConfig(f.ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close(context.Background()) })
		return c
	}
}

func TestSchemaEventsTasksArtifacts(t *testing.T) {
	f := newFixture(t)
	ctx, c := f.ctx, f.c
	mustExec(t, ctx, c, "INSERT INTO events (run_id, sequence, entity_version, type, payload) VALUES ($1, 1, 1, 'run.created', '{}')", f.runA)
	execState(t, ctx, c, "23505", "INSERT INTO events (run_id, sequence, entity_version, type, payload) VALUES ($1, 1, 1, 'run.created', '{}')", f.runA)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE events SET payload = '{\"x\":1}' WHERE run_id = $1", f.runA)
	execState(t, ctx, c, SQLStateImmutable, "DELETE FROM events WHERE run_id = $1", f.runA)
	execState(t, ctx, c, "23514", "INSERT INTO events (run_id, sequence, entity_version, type, payload) VALUES ($1, 0, 1, 'run.created', '{}')", f.runA)
	execState(t, ctx, c, "23514", "INSERT INTO events (run_id, sequence, entity_version, type, payload) VALUES ($1, 2, 1, 'run.created', '[]')", f.runA)

	// 논리 task는 재시도 시 queued로 돌아갈 수 있다. 실행 이력 행의 종료 상태는 고정된다.
	mustExec(t, ctx, c, "UPDATE tasks SET status = 'failed', version = 2 WHERE run_id = $1 AND id = $2", f.runA, f.taskA)
	mustExec(t, ctx, c, "UPDATE tasks SET status = 'queued', version = 3 WHERE run_id = $1 AND id = $2", f.runA, f.taskA)
	execState(t, ctx, c, SQLStateCounterDecrease, "UPDATE tasks SET version = 1 WHERE run_id = $1 AND id = $2", f.runA, f.taskA)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE tasks SET required_for_verdict = false WHERE run_id = $1 AND id = $2", f.runA, f.taskA)
	execState(t, ctx, c, "23514", "UPDATE tasks SET status = 'skipped' WHERE run_id = $1 AND id = $2", f.runA, f.taskA)
	mustExec(t, ctx, c, "INSERT INTO task_executions (run_id, attempt_id, task_id, status) VALUES ($1, $2, $3, 'running')", f.runA, f.attemptA, f.taskA)
	mustExec(t, ctx, c, "UPDATE task_executions SET status = 'timed_out', version = 2 WHERE attempt_id = $1 AND task_id = $2", f.attemptA, f.taskA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE task_executions SET status = 'succeeded', version = 3 WHERE attempt_id = $1 AND task_id = $2", f.attemptA, f.taskA)
	// 다른 run의 attempt/task 조합 거부.
	execState(t, ctx, c, "23503", "INSERT INTO task_executions (run_id, attempt_id, task_id, status) VALUES ($1, $2, $3, 'queued')", f.runA, f.attemptA2, f.taskA)
	execState(t, ctx, c, "23503", "INSERT INTO task_executions (run_id, attempt_id, task_id, status) VALUES ($1, $2, $3, 'queued')", f.runA, f.attemptA, f.taskA2)

	// artifact: 다른 project/run 조합 거부, hash/size 불변, finalize 단방향.
	execState(t, ctx, c, "23503", "INSERT INTO artifacts (id, project_id, run_id, sha256, size_bytes) VALUES ($1, $2, $3, $4, 1)", newID(), f.projB, f.runA, digest('e'))
	execState(t, ctx, c, SQLStateImmutable, "UPDATE artifacts SET sha256 = $1 WHERE id = $2", digest('f'), f.artifactA)
	mustExec(t, ctx, c, "UPDATE artifacts SET finalized = true WHERE id = $1", f.artifactA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE artifacts SET finalized = false WHERE id = $1", f.artifactA)
}

func TestSchemaEvidenceScope(t *testing.T) {
	f := newFixture(t)
	ctx, c := f.ctx, f.c
	const ins = `INSERT INTO evidence (id, project_id, run_id, attempt_id, task_id, snapshot_id, artifact_id, path, start_line, end_line, blob_sha256, kind)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	type ev struct {
		project, run, attempt, task string
		snapshot, artifact, path    any
		start, end                  any
		blob                        any
		kind                        string
	}
	good := func() ev {
		return ev{f.projA, f.runA, f.attemptA, f.taskA, f.snapA, nil, "src/main.c", 3, 7, digest('b'), "code"}
	}
	exec := func(e ev) error {
		_, err := c.Exec(ctx, ins, newID(), e.project, e.run, e.attempt, e.task, e.snapshot, e.artifact, e.path, e.start, e.end, e.blob, e.kind)
		return err
	}
	if err := exec(good()); err != nil {
		t.Fatalf("valid code evidence: %v", err)
	}
	tool := ev{f.projA, f.runA, f.attemptA, f.taskA, nil, f.artifactA, nil, 1, 2, nil, "tool_output"}
	if err := exec(tool); err != nil {
		t.Fatalf("valid tool_output evidence: %v", err)
	}
	gen := ev{f.projA, f.runA, f.attemptA, f.taskA, nil, f.artifactA, "gen/out.c", nil, nil, nil, "code"}
	if err := exec(gen); err != nil {
		t.Fatalf("valid generated-file evidence: %v", err)
	}

	reject := map[string]struct {
		mut  func(*ev)
		code string
	}{
		"cross project run":      {func(e *ev) { e.project = f.projB }, "23503"},
		"cross run attempt":      {func(e *ev) { e.attempt = f.attemptA2 }, "23503"},
		"cross run task":         {func(e *ev) { e.task = f.taskA2 }, "23503"},
		"cross project snapshot": {func(e *ev) { e.snapshot = f.snapB }, "23503"},
		"cross run artifact":     {func(e *ev) { e.snapshot, e.artifact, e.kind = nil, f.artifactA2, "tool_output" }, "23503"},
		"both bases":             {func(e *ev) { e.artifact = f.artifactA }, "23514"},
		"no basis":               {func(e *ev) { e.snapshot = nil }, "23514"},
		"tool_output snapshot":   {func(e *ev) { e.kind = "tool_output" }, "23514"},
		"snapshot without path":  {func(e *ev) { e.path = nil; e.start, e.end = nil, nil }, "23514"},
		"absolute path":          {func(e *ev) { e.path = "/etc/passwd" }, "23514"},
		"parent path":            {func(e *ev) { e.path = "src/../../x" }, "23514"},
		"drive path":             {func(e *ev) { e.path = "C:/x" }, "23514"},
		"backslash path":         {func(e *ev) { e.path = `src\x.c` }, "23514"},
		"inverted lines":         {func(e *ev) { e.start, e.end = 9, 3 }, "23514"},
		"zero line":              {func(e *ev) { e.start, e.end = 0, 3 }, "23514"},
		"half range":             {func(e *ev) { e.end = nil }, "23514"},
		"bad blob hash":          {func(e *ev) { e.blob = "ABC" }, "23514"},
		"unknown kind":           {func(e *ev) { e.kind = "llm_claim" }, "23514"},
	}
	for name, r := range reject {
		t.Run(name, func(t *testing.T) {
			e := good()
			r.mut(&e)
			expectState(t, exec(e), r.code)
		})
	}

	execState(t, ctx, c, SQLStateImmutable, "UPDATE evidence SET path = 'other.c' WHERE kind = 'code'")
	// 근거가 참조하는 부모는 삭제되지 않는다(no cascade).
	execState(t, ctx, c, "23503", "DELETE FROM artifacts WHERE id = $1", f.artifactA)
	execState(t, ctx, c, "23503", "DELETE FROM tasks WHERE id = $1", f.taskA)
	execState(t, ctx, c, "23503", "DELETE FROM attempts WHERE id = $1", f.attemptA)
	var n int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM evidence").Scan(&n); err != nil || n != 3 {
		t.Fatalf("evidence rows = %d, %v; want 3", n, err)
	}
}

func TestSchemaNoCascadeForeignKeys(t *testing.T) {
	f := newFixture(t)
	rows, err := f.c.Query(f.ctx, `SELECT conrelid::regclass::text, conname, confdeltype::text
		FROM pg_constraint WHERE contype = 'f' AND connamespace = current_schema()::regnamespace`)
	if err != nil {
		t.Fatal(err)
	}
	type fk struct{ Rel, Name, Del string }
	fks, err := pgx.CollectRows(rows, pgx.RowToStructByPos[fk])
	if err != nil {
		t.Fatal(err)
	}
	if len(fks) == 0 {
		t.Fatal("no foreign keys found")
	}
	for _, k := range fks {
		if k.Del != "r" {
			t.Errorf("%s.%s ON DELETE action %q, want RESTRICT", k.Rel, k.Name, k.Del)
		}
	}
}

// 트리거가 호출자 search_path에 의존하지 않는지 확인한다(스키마를 제외한 search_path에서 한정 이름으로 갱신).
func TestSchemaRetryGuardIndependentOfSearchPath(t *testing.T) {
	f := newFixture(t)
	schema, _ := isolatedSchemaFor(t, f)
	ctx := f.ctx
	cfg, err := pgx.ParseConfig(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = "pg_catalog"
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	runs := pgx.Identifier{schema, "runs"}.Sanitize()
	attempts := pgx.Identifier{schema, "attempts"}.Sanitize()
	mustExec(t, ctx, c, "UPDATE "+runs+" SET status = 'failed', version = 2 WHERE id = $1", f.runA)
	execState(t, ctx, c, SQLStateIllegalTransition, "UPDATE "+runs+" SET status = 'queued', version = 3 WHERE id = $1", f.runA)
	mustExec(t, ctx, c, "UPDATE "+attempts+" SET status = 'released', cleanup_state = 'confirmed' WHERE id = $1", f.attemptA)
	mustExec(t, ctx, c, "UPDATE "+runs+" SET status = 'queued', version = 3 WHERE id = $1", f.runA)
}

func TestSchema0002GuardsAndUpgradeFromW02(t *testing.T) {
	_, open := isolatedSchema(t)
	ctx := testCtx(t)
	c := open()
	// A W02 database with rows: apply 0001 only, add data, then upgrade.
	ms, _ := Embedded()
	only1 := fstest.MapFS{"m/0001_core_domain.sql": {Data: []byte(ms[0].sql)}}
	if err := applyFS(ctx, c, only1, "m"); err != nil {
		t.Fatal(err)
	}
	user, project := newID(), newID()
	mustExec(t, ctx, c, "INSERT INTO users (id) VALUES ($1)", user)
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO projects (id, owner_id, name, current_revision) VALUES ($1, $2, 'p', 1)", project, user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO project_revisions (project_id, revision, config, config_digest) VALUES ($1, 1, '{}', $2)", project, digest('c')); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, c); err != nil {
		t.Fatalf("upgrade 0001 -> latest: %v", err)
	}
	var created time.Time
	var disabled *time.Time
	if err := c.QueryRow(ctx, "SELECT created_at, disabled_at FROM users WHERE id=$1", user).Scan(&created, &disabled); err != nil || created.IsZero() || disabled != nil {
		t.Fatalf("existing user after upgrade: %v %v %v", created, disabled, err)
	}
	if err := c.QueryRow(ctx, "SELECT created_at FROM projects WHERE id=$1", project).Scan(&created); err != nil || created.IsZero() {
		t.Fatalf("existing project after upgrade: %v", err)
	}

	// Launch token: used once, then fixed.
	mustExec(t, ctx, c, "INSERT INTO auth_launch_tokens (token_sha256, user_id, expires_at) VALUES ($1, $2, now() + interval '1 minute')", digest('1'), user)
	mustExec(t, ctx, c, "UPDATE auth_launch_tokens SET used_at = clock_timestamp() WHERE token_sha256 = $1", digest('1'))
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE auth_launch_tokens SET used_at = NULL WHERE token_sha256 = $1", digest('1'))
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_launch_tokens SET expires_at = expires_at + interval '1 hour' WHERE token_sha256 = $1", digest('1'))
	execState(t, ctx, c, "23514", "INSERT INTO auth_launch_tokens (token_sha256, user_id, expires_at) VALUES ('raw-token', $1, now() + interval '1 minute')", user)
	execState(t, ctx, c, "23514", "INSERT INTO auth_launch_tokens (token_sha256, user_id, expires_at) VALUES ($1, $2, now() - interval '1 minute')", digest('2'), user)
	// Session: revoked once, expiry fixed, hash unique.
	sess := newID()
	mustExec(t, ctx, c, "INSERT INTO auth_sessions (id, user_id, token_sha256, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')", sess, user, digest('3'))
	execState(t, ctx, c, "23505", "INSERT INTO auth_sessions (id, user_id, token_sha256, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')", newID(), user, digest('3'))
	execState(t, ctx, c, SQLStateImmutable, "UPDATE auth_sessions SET expires_at = expires_at + interval '1 day' WHERE id = $1", sess)
	mustExec(t, ctx, c, "UPDATE auth_sessions SET revoked_at = clock_timestamp() WHERE id = $1", sess)
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE auth_sessions SET revoked_at = NULL WHERE id = $1", sess)
	// User: disable is permanent; identity immutable.
	mustExec(t, ctx, c, "UPDATE users SET display_name = 'Alice' WHERE id = $1", user)
	mustExec(t, ctx, c, "UPDATE users SET disabled_at = clock_timestamp() WHERE id = $1", user)
	execState(t, ctx, c, SQLStateFixedReference, "UPDATE users SET disabled_at = NULL WHERE id = $1", user)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE users SET created_at = created_at - interval '1 day' WHERE id = $1", user)
	execState(t, ctx, c, "23514", "UPDATE users SET display_name = '  ' WHERE id = $1", user)
	// Source roots and providers are immutable registrations.
	root := newID()
	mustExec(t, ctx, c, "INSERT INTO source_roots (id, label, path) VALUES ($1, 'work', '/srv/work')", root)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE source_roots SET path = '/etc' WHERE id = $1", root)
	execState(t, ctx, c, "23505", "INSERT INTO source_roots (id, label, path) VALUES ($1, 'work', '/srv/other')", newID())
	mustExec(t, ctx, c, "INSERT INTO source_root_grants (source_root_id, user_id) VALUES ($1, $2)", root, user)
	execState(t, ctx, c, "23503", "DELETE FROM source_roots WHERE id = $1", root)
	prov := newID()
	mustExec(t, ctx, c, "INSERT INTO providers (id, kind, host) VALUES ($1, 'ghes', 'ghe.example')", prov)
	execState(t, ctx, c, SQLStateImmutable, "UPDATE providers SET host = 'evil.example' WHERE id = $1", prov)
	execState(t, ctx, c, "23514", "INSERT INTO providers (id, kind, host) VALUES ($1, 'github', 'ghe.example')", newID())
	execState(t, ctx, c, "23514", "INSERT INTO providers (id, kind, host) VALUES ($1, 'gitlab', 'gitlab.com')", newID())
	execState(t, ctx, c, "23514", "INSERT INTO providers (id, kind, host, api_url) VALUES ($1, 'ghes', 'b.example', 'http://b.example/api')", newID())
}
