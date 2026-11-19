package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// RetentionRoles are the artifact roles retention may remove: the tools'
// raw output. Manifests and observations stay (they are the structured
// basis a report revision is built from and checked against).
var RetentionRoles = []string{"stdout", "stderr", "raw_output", "other"}

// MinRetentionAge is the smallest age retention accepts, so that an artifact
// of work that is still being reported is never a candidate.
const MinRetentionAge = time.Hour

// RetentionArtifact is one artifact row whose content retention removes.
type RetentionArtifact struct {
	ID, RunID, Role, SHA256, StorageKey string
	Size                                int64
	CreatedAt                           time.Time
}

// RetentionPlan is what retention removes for an age: the rows (finished
// runs only), the contents no other present row references (FreedBlobs,
// the files to delete) and, for the record, the audit rows older than the
// cutoff, which are kept (append-only).
type RetentionPlan struct {
	Cutoff     time.Time
	Artifacts  []RetentionArtifact
	Runs       int
	RowBytes   int64
	FreedBlobs []Blob
	FreedBytes int64
	AuditRows  int64
}

// retentionCandidates selects the raw artifact rows older than the cutoff
// of runs that finished. lock takes them FOR UPDATE.
const retentionCandidates = `SELECT a.id::text,a.run_id::text,a.role,a.sha256,a.storage_key,a.size_bytes,a.created_at
	FROM artifacts a JOIN runs r ON r.id=a.run_id
	WHERE a.finalized AND a.storage_key IS NOT NULL AND a.content_removed_at IS NULL AND a.role=ANY($1)
	AND a.created_at<$2 AND r.status IN ('completed','partial','failed','cancelled')
	ORDER BY a.created_at,a.id`

// PlanRetention lists what ApplyRetention would remove for olderThan
// (dry run). Nothing changes.
func (s *Store) PlanRetention(ctx context.Context, olderThan time.Duration) (RetentionPlan, error) {
	if olderThan < MinRetentionAge {
		return RetentionPlan{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (RetentionPlan, error) {
		return retentionPlan(ctx, tx, olderThan, false)
	})
}

// ApplyRetention marks the planned rows' contents removed
// (artifacts.content_removed_at) and writes one storage.retention_applied
// audit row (operator CLI), in one transaction. It returns the plan; the
// caller deletes FreedBlobs from the artifact store after the commit, checking
// ContentPresent first. Reports keep working: they are read from their
// stored bytes, never from these artifacts.
func (s *Store) ApplyRetention(ctx context.Context, olderThan time.Duration) (RetentionPlan, error) {
	if olderThan < MinRetentionAge {
		return RetentionPlan{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (RetentionPlan, error) {
		p, err := retentionPlan(ctx, tx, olderThan, true)
		if err != nil || len(p.Artifacts) == 0 {
			return p, err
		}
		ids := make([]string, len(p.Artifacts))
		for i, a := range p.Artifacts {
			ids[i] = a.ID
		}
		if _, err := tx.Exec(ctx, "UPDATE artifacts SET content_removed_at=clock_timestamp() WHERE id=ANY($1::uuid[])", ids); err != nil {
			return p, err
		}
		e := principalAudit(OperatorPrincipal(), AuditRetentionApplied).with(map[string]any{
			"artifacts_removed": len(p.Artifacts), "bytes_removed": p.FreedBytes, "older_than_days": int64(olderThan / (24 * time.Hour))})
		return p, writeAudit(ctx, tx, e)
	})
}

func retentionPlan(ctx context.Context, tx pgx.Tx, olderThan time.Duration, lock bool) (RetentionPlan, error) {
	var p RetentionPlan
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()-$1*interval '1 millisecond'", olderThan.Milliseconds()).Scan(&p.Cutoff); err != nil {
		return p, err
	}
	q := retentionCandidates
	if lock {
		q += " FOR UPDATE OF a"
	}
	rows, err := tx.Query(ctx, q, RetentionRoles, p.Cutoff)
	if err != nil {
		return p, err
	}
	p.Artifacts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[RetentionArtifact])
	if err != nil {
		return p, err
	}
	runs := map[string]bool{}
	ids := make([]string, 0, len(p.Artifacts))
	for _, a := range p.Artifacts {
		runs[a.RunID] = true
		p.RowBytes += a.Size
		ids = append(ids, a.ID)
	}
	p.Runs = len(runs)
	// A content is freed when no present row outside the plan references it
	// (the store is content-addressed: newer runs may share a blob).
	rows, err = tx.Query(ctx, `SELECT a.sha256,min(a.size_bytes) FROM artifacts a WHERE a.id=ANY($1::uuid[])
		AND NOT EXISTS(SELECT 1 FROM artifacts b WHERE b.sha256=a.sha256 AND b.storage_key IS NOT NULL AND b.content_removed_at IS NULL AND b.id<>ALL($1::uuid[]))
		GROUP BY a.sha256 ORDER BY a.sha256`, ids)
	if err != nil {
		return p, err
	}
	if p.FreedBlobs, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Blob]); err != nil {
		return p, err
	}
	for _, b := range p.FreedBlobs {
		p.FreedBytes += b.Size
	}
	err = tx.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE occurred_at<$1", p.Cutoff).Scan(&p.AuditRows)
	return p, err
}

// ContentPresent reports whether a present (not removed) row references the
// content sha256: retention deletes a freed file only when this is false at
// deletion time (a new run may have uploaded the same bytes since).
func (s *Store) ContentPresent(ctx context.Context, sha string) (bool, error) {
	var present bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM artifacts WHERE sha256=$1 AND storage_key IS NOT NULL AND content_removed_at IS NULL)", sha).Scan(&present)
	return present, classify(err)
}
