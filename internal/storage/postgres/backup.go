package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

// W25 backup and restore support (docs/OPERATIONS.md#백업과-복원). The database dump and the
// artifact list come from ONE exported snapshot: WithBackupPoint holds a
// REPEATABLE READ READ ONLY transaction, exports its snapshot for pg_dump
// --snapshot and reads the finalized blobs in the same snapshot.

// Blob is one finalized artifact content in the controller store.
type Blob struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size_bytes"`
}

// BackupPoint is what one exported snapshot shows.
type BackupPoint struct {
	SnapshotID       string
	MigrationVersion int
	ServerVersion    string
	Schema           string
	Blobs            []Blob
	// MetadataOnly counts finalized artifact rows without stored bytes (the
	// W02 metadata path); they have nothing to copy.
	MetadataOnly int64
	Rows         map[string]int64
}

// countedTables are compared between a backup point and the restored
// database (docs/OPERATIONS.md: projects, runs, reports, evidence, ACL, schedules).
var countedTables = []string{"users", "projects", "project_members", "source_roots", "source_root_grants", "runs", "attempts", "tasks", "events",
	"artifacts", "evidence", "reports", "finding_reviews", "schedules", "schedule_occurrences", "nodes"}

const blobsSQL = `SELECT sha256,min(size_bytes) FROM artifacts WHERE finalized AND storage_key IS NOT NULL GROUP BY sha256 ORDER BY sha256`

func rowCounts(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range countedTables {
		var n int64
		if err := q.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

// WithBackupPoint runs fn while one exported snapshot is held open. fn must
// finish pg_dump --snapshot=<SnapshotID> before it returns.
func (s *Store) WithBackupPoint(ctx context.Context, fn func(BackupPoint) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return classify(err)
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return classify(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var p BackupPoint
	// The snapshot is taken by the first statement of the transaction.
	if err := tx.QueryRow(ctx, "SELECT pg_export_snapshot(),current_setting('server_version'),current_schema()").Scan(&p.SnapshotID, &p.ServerVersion, &p.Schema); err != nil {
		return classify(err)
	}
	if p.MigrationVersion, err = migrations.Inspect(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, blobsSQL)
	if err != nil {
		return classify(err)
	}
	if p.Blobs, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Blob]); err != nil {
		return classify(err)
	}
	if p.Blobs == nil {
		p.Blobs = []Blob{}
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM artifacts WHERE finalized AND storage_key IS NULL").Scan(&p.MetadataOnly); err != nil {
		return classify(err)
	}
	if p.Rows, err = rowCounts(ctx, tx); err != nil {
		return classify(err)
	}
	return fn(p)
}

// SchemaEmpty reports whether the connection's current schema holds no
// relation (restore target check).
func (s *Store) SchemaEmpty(ctx context.Context) (bool, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=current_schema() AND c.relkind IN ('r','p','v','m','S','f')`).Scan(&n)
	return n == 0, classify(err)
}

// RestoredState is what a restored database shows (verification).
type RestoredState struct {
	MigrationVersion int
	Blobs            []Blob
	Rows             map[string]int64
}

// ReadRestoredState reads the migration version, the finalized blobs and the
// row counts in one read-only snapshot.
func (s *Store) ReadRestoredState(ctx context.Context) (RestoredState, error) {
	var st RestoredState
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return st, classify(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if st.MigrationVersion, err = migrations.Inspect(ctx, tx); err != nil {
		return st, err
	}
	rows, err := tx.Query(ctx, blobsSQL)
	if err != nil {
		return st, classify(err)
	}
	if st.Blobs, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Blob]); err != nil {
		return st, classify(err)
	}
	st.Rows, err = rowCounts(ctx, tx)
	return st, classify(err)
}

// FenceReport counts what FenceRestored invalidated.
type FenceReport struct {
	NodesRevoked      int64 `json:"nodes_revoked"`
	SessionsRevoked   int64 `json:"sessions_revoked"`
	LaunchTokens      int64 `json:"launch_tokens_invalidated"`
	EnrollmentTokens  int64 `json:"enrollment_tokens_revoked"`
	AttemptsFenced    int64 `json:"attempts_fenced"`
	ReservationsHeld  int64 `json:"reservations_held"`
	RecoveryRequired  int64 `json:"runs_recovery_required"`
	QueuedRunsKept    int64 `json:"queued_runs"`
	ScheduledKept     int64 `json:"schedules"`
	OccurrencesKept   int64 `json:"schedule_occurrences"`
	UnfencedRemaining int64 `json:"leased_remaining"`
}

// FenceRestored makes a freshly restored database safe to serve (docs/OPERATIONS.md
// §5.3). The backup is a past state, so nothing issued before it is trusted:
//
//   - every node is revoked: its secret, command cursor and leases belong to
//     the past; the local agent enrolls again;
//   - every session is revoked, unused launch tokens are consumed and unused
//     enrollment tokens revoked;
//   - every leased attempt is fenced exactly like an expired lease
//     (ExpireAttempt: recovery_required, cleanup uncertain, run
//     recovery_required, attempt.expired event), whatever its expires_at.
//
// Reservations of fenced attempts stay held: their cleanup is not confirmed.
// Queued runs and schedules are kept; the schedule occurrence keys keep
// de-duplicating. It must run before any controller serves the database.
func (s *Store) FenceRestored(ctx context.Context) (FenceReport, error) {
	r, err := transaction(ctx, s, func(tx pgx.Tx) (FenceReport, error) {
		var r FenceReport
		steps := []struct {
			sql string
			n   *int64
		}{
			{"UPDATE workers SET state='stopped',state_since=clock_timestamp() WHERE state NOT IN ('stopped','failed') AND node_id IN (SELECT id FROM nodes WHERE revoked_at IS NULL)", nil},
			{"UPDATE nodes SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL", &r.NodesRevoked},
			{"UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL", &r.SessionsRevoked},
			{"UPDATE auth_launch_tokens SET used_at=clock_timestamp() WHERE used_at IS NULL", &r.LaunchTokens},
			{"UPDATE node_enrollment_tokens SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL AND used_at IS NULL", &r.EnrollmentTokens},
			// Lapse every lease now so ExpireAttempt applies regardless of
			// the expiry recorded at backup time.
			{"UPDATE attempts SET expires_at=clock_timestamp() WHERE status='leased' AND expires_at>clock_timestamp()", nil},
		}
		for _, st := range steps {
			tag, err := tx.Exec(ctx, st.sql)
			if err != nil {
				return r, err
			}
			if st.n != nil {
				*st.n = tag.RowsAffected()
			}
		}
		return r, nil
	})
	if err != nil {
		return r, err
	}
	rows, err := s.pool.Query(ctx, "SELECT id::text FROM attempts WHERE status='leased' ORDER BY id")
	if err != nil {
		return r, classify(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return r, classify(err)
	}
	for _, id := range ids {
		if _, err := s.ExpireAttempt(ctx, id); err != nil {
			if !errors.Is(err, domain.ErrConflict) {
				return r, err
			}
			continue
		}
		r.AttemptsFenced++
	}
	err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM attempts WHERE status='leased'),
		(SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL),
		(SELECT count(*) FROM runs WHERE status='recovery_required'),
		(SELECT count(*) FROM runs WHERE status='queued'),
		(SELECT count(*) FROM schedules),(SELECT count(*) FROM schedule_occurrences)`).Scan(&r.UnfencedRemaining, &r.ReservationsHeld, &r.RecoveryRequired, &r.QueuedRunsKept, &r.ScheduledKept, &r.OccurrencesKept)
	if err != nil {
		return r, classify(err)
	}
	if r.UnfencedRemaining > 0 {
		return r, errors.New("restore fence incomplete: leased attempts remain")
	}
	return r, nil
}
