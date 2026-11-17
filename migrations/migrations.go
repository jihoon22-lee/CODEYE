// Package migrations는 CODEYE PostgreSQL schema를 내장 SQL로 적용한다.
//
// Apply는 호출자가 준 연결의 search_path 첫 스키마(current_schema)에 schema와 적용 이력을 만든다.
// 운영 배치는 전용 데이터베이스의 public 스키마를 사용하고, 통합 시험은 연결별 격리 스키마를 사용한다.
// 서버 시작이나 doctor가 사용자 DB를 조용히 변경하지 않도록, 이 패키지는 명시적으로 호출될 때만 동작한다.
package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// HistoryTable은 적용 이력 테이블 이름이다. current_schema에 생성된다.
const HistoryTable = "codeye_schema_migrations"

// lockKey는 pg_advisory_xact_lock 키다. advisory lock은 데이터베이스 범위이므로
// 서로 다른 스키마에 대한 동시 적용도 직렬화된다(정확성에는 영향 없음).
const lockKey int64 = 0x434f4445594500 // "CODEYE\x00"

// schema 트리거가 사용하는 사용자 정의 SQLSTATE. 저장 서비스는 이 값으로 오류를 분류한다.
const (
	// SQLStateImmutable은 불변 행·열을 변경하거나 append-only 행을 삭제할 때 발생한다.
	SQLStateImmutable = "CY001"
	// SQLStateFixedReference는 한 번 고정된 참조(run snapshot/project_revision, attempt completion)를 교체·해제할 때 발생한다.
	SQLStateFixedReference = "CY002"
	// SQLStateCounterDecrease는 version·sequence·epoch 같은 단조 증가 값을 줄일 때 발생한다.
	SQLStateCounterDecrease = "CY003"
	// SQLStateIllegalTransition은 종료 상태에서 벗어나거나 금지된 상태 역행을 시도할 때 발생한다.
	SQLStateIllegalTransition = "CY004"
)

var (
	// ErrInvalidMigrationSet은 내장 migration 파일 이름·번호·내용이 규칙에 맞지 않을 때 반환된다.
	ErrInvalidMigrationSet = errors.New("migrations: invalid migration set")
	// ErrChecksumMismatch는 이미 적용된 migration의 이름 또는 checksum이 내장 파일과 다를 때 반환된다.
	ErrChecksumMismatch = errors.New("migrations: applied migration checksum drift")
	// ErrUnknownVersion은 DB에 이 바이너리가 모르는(미래) 버전이 적용되어 있을 때 반환된다.
	ErrUnknownVersion = errors.New("migrations: database has unknown migration version")
	// ErrMissingVersion은 적용 이력이 1부터 연속된 prefix가 아닐 때 반환된다.
	ErrMissingVersion = errors.New("migrations: applied history has a missing version")
	// ErrConnectionState는 연결이 이미 트랜잭션 중이거나 사용할 스키마가 없을 때 반환된다.
	ErrConnectionState = errors.New("migrations: connection not usable for migration")
	// ErrTransactionBroken은 migration SQL이 적용 트랜잭션을 종료·교체했을 때 반환된다.
	ErrTransactionBroken = errors.New("migrations: migration SQL left the migration transaction")
)

//go:embed sql/*.sql
var embedded embed.FS

// Migration은 내장 migration 한 개다.
type Migration struct {
	Version  int
	Name     string
	Checksum string // SQL 원본 bytes의 SHA-256 소문자 hex
	sql      string
}

var fileNamePattern = regexp.MustCompile(`^([0-9]{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

// Embedded는 바이너리에 내장된 migration 목록을 버전 순서로 반환한다.
func Embedded() ([]Migration, error) {
	return load(embedded, "sql")
}

// LatestVersion은 내장 migration의 최신 버전이다.
func LatestVersion() (int, error) {
	ms, err := Embedded()
	if err != nil {
		return 0, err
	}
	return ms[len(ms)-1].Version, nil
}

func load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidMigrationSet, dir, err)
	}
	var ms []Migration
	for _, e := range entries {
		if e.IsDir() {
			return nil, fmt.Errorf("%w: unexpected directory %q", ErrInvalidMigrationSet, e.Name())
		}
		m := fileNamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("%w: file name %q must match NNNN_name.sql", ErrInvalidMigrationSet, e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidMigrationSet, e.Name(), err)
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("%w: %s is empty", ErrInvalidMigrationSet, e.Name())
		}
		sum := sha256.Sum256(body)
		ms = append(ms, Migration{Version: v, Name: m[2], Checksum: hex.EncodeToString(sum[:]), sql: string(body)})
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("%w: no migrations", ErrInvalidMigrationSet)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("%w: versions must be contiguous from 1; position %d has version %d",
				ErrInvalidMigrationSet, i+1, m.Version)
		}
	}
	return ms, nil
}

// Apply는 내장 migration을 적용한다.
//
// 전체 과정(잠금, 이력 테이블 생성, 이력 검증, 미적용 migration 실행, 이력 기록)은 READ COMMITTED
// 단일 트랜잭션이며 pg_advisory_xact_lock으로 동시 적용을 직렬화한다. 어느 단계든 실패하면 이력을
// 포함해 전부 rollback한다. 이미 적용된 버전의 checksum·이름이 다르거나, 이력에 알 수 없는(미래)
// 버전 또는 빠진 버전이 있으면 아무것도 변경하지 않고 거부한다. 최신 상태에서 다시 호출하면 변경 없이
// 성공한다.
//
// conn은 트랜잭션 밖(idle) 상태여야 한다. 대상 스키마는 conn의 search_path가 결정한다.
func Apply(ctx context.Context, conn *pgx.Conn) error {
	return applyFS(ctx, conn, embedded, "sql")
}

func applyFS(ctx context.Context, conn *pgx.Conn, fsys fs.FS, dir string) (err error) {
	ms, err := load(fsys, dir)
	if err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("%w: nil connection", ErrConnectionState)
	}
	if st := conn.PgConn().TxStatus(); st != 'I' {
		return fmt.Errorf("%w: connection transaction status %q, want idle", ErrConnectionState, st)
	}

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("migrations: begin: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// 호출자 ctx가 취소되었어도 rollback은 시도한다.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if rbErr := tx.Rollback(rctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("migrations: rollback: %w", rbErr))
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return fmt.Errorf("migrations: acquire advisory lock: %w", err)
	}

	var schema *string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return fmt.Errorf("migrations: read current_schema: %w", err)
	}
	if schema == nil {
		return fmt.Errorf("%w: search_path has no existing schema", ErrConnectionState)
	}

	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+HistoryTable+` (
		version    integer     PRIMARY KEY CHECK (version >= 1),
		name       text        NOT NULL,
		checksum   text        NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
		applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
	)`); err != nil {
		return fmt.Errorf("migrations: create history table: %w", err)
	}

	applied, err := readHistory(ctx, tx)
	if err != nil {
		return err
	}
	if err := verifyHistory(ms, applied); err != nil {
		return err
	}

	var xid string
	if err := tx.QueryRow(ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
		return fmt.Errorf("migrations: read transaction id: %w", err)
	}

	for _, m := range ms[len(applied):] {
		// 다중 statement SQL은 simple protocol로 실행한다(인자 없음).
		if _, err := tx.Conn().PgConn().Exec(ctx, m.sql).ReadAll(); err != nil {
			return fmt.Errorf("migrations: apply %04d_%s: %w", m.Version, m.Name, err)
		}
		if err := ensureSameTx(ctx, tx, xid); err != nil {
			// 이미 일부가 commit되었을 수 있다. 자동 복구하지 않고 운영자 확인이 필요한 상태로 보고한다.
			return fmt.Errorf("%w: after %04d_%s: %v (earlier statements may already be committed; manual recovery required)",
				ErrTransactionBroken, m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO "+HistoryTable+" (version, name, checksum) VALUES ($1, $2, $3)",
			m.Version, m.Name, m.Checksum); err != nil {
			return fmt.Errorf("migrations: record %04d_%s: %w", m.Version, m.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrations: commit: %w", err)
	}
	committed = true
	return nil
}

// ensureSameTx는 migration SQL이 COMMIT/ROLLBACK 등으로 적용 트랜잭션을 끝내거나 바꾸지 않았는지 확인한다.
func ensureSameTx(ctx context.Context, tx pgx.Tx, xid string) error {
	if st := tx.Conn().PgConn().TxStatus(); st != 'T' {
		return fmt.Errorf("transaction status %q", st)
	}
	var now string
	if err := tx.QueryRow(ctx, "SELECT pg_current_xact_id()::text").Scan(&now); err != nil {
		return err
	}
	if now != xid {
		return fmt.Errorf("transaction id changed from %s to %s", xid, now)
	}
	return nil
}

type appliedMigration struct {
	Version  int
	Name     string
	Checksum string
}

func readHistory(ctx context.Context, tx pgx.Tx) ([]appliedMigration, error) {
	rows, err := tx.Query(ctx, "SELECT version, name, checksum FROM "+HistoryTable+" ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("migrations: read history: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appliedMigration, error) {
		var a appliedMigration
		err := r.Scan(&a.Version, &a.Name, &a.Checksum)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("migrations: read history: %w", err)
	}
	return out, nil
}

// verifyHistory는 적용 이력이 내장 목록의 1..k prefix와 정확히 일치하는지 확인한다.
func verifyHistory(ms []Migration, applied []appliedMigration) error {
	latest := ms[len(ms)-1].Version
	for _, a := range applied {
		if a.Version > latest {
			return fmt.Errorf("%w: version %d (%s) is newer than this binary's latest %d",
				ErrUnknownVersion, a.Version, a.Name, latest)
		}
	}
	for i, a := range applied {
		if a.Version != i+1 {
			return fmt.Errorf("%w: expected version %d in history, found %d", ErrMissingVersion, i+1, a.Version)
		}
		m := ms[i]
		if a.Name != m.Name || a.Checksum != m.Checksum {
			return fmt.Errorf("%w: version %d applied as %s (%s), embedded %s (%s)",
				ErrChecksumMismatch, a.Version, a.Name, a.Checksum, m.Name, m.Checksum)
		}
	}
	return nil
}

// Inspect reports how many embedded migrations the schema visible through tx
// (its current_schema) has applied, without changing anything. A schema with
// no history table has applied 0. An inconsistent history returns
// ErrChecksumMismatch, ErrUnknownVersion or ErrMissingVersion, the same checks
// Apply performs before it would change anything.
func Inspect(ctx context.Context, tx pgx.Tx) (applied int, err error) {
	ms, err := Embedded()
	if err != nil {
		return 0, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('"+HistoryTable+"') IS NOT NULL").Scan(&exists); err != nil {
		return 0, fmt.Errorf("migrations: inspect: %w", err)
	}
	if !exists {
		return 0, nil
	}
	history, err := readHistory(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := verifyHistory(ms, history); err != nil {
		return len(history), err
	}
	return len(history), nil
}
