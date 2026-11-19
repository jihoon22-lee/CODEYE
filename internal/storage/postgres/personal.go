package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Operator queries for personal mode (codeye start/stop/status, doctor).
// They are trusted CLI operations: no ACL, never exposed through the API.

// SourceRootRecord is a source root with its server-local path (operator only).
type SourceRootRecord struct{ ID, Label, Path string }

// EnsureProvider returns the provider of host, registering it (github for
// github.com, ghes otherwise) when there is none. created reports a new row.
func (s *Store) EnsureProvider(ctx context.Context, host string) (Provider, bool, error) {
	host = strings.ToLower(host)
	var p Provider
	err := s.pool.QueryRow(ctx, "SELECT id::text,kind,host,coalesce(api_url,'') FROM providers WHERE host=$1 ORDER BY kind LIMIT 1", host).Scan(&p.ID, &p.Kind, &p.Host, &p.APIURL)
	if err == nil {
		return p, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return p, false, classify(err)
	}
	kind := "ghes"
	if host == "github.com" {
		kind = "github"
	}
	p, err = s.AddProvider(ctx, kind, host, "")
	return p, err == nil, err
}

// GrantedRootPaths are the source roots granted to actor with their paths
// (personal mode maps an absolute folder path onto them), ordered by label.
func (s *Store) GrantedRootPaths(ctx context.Context, actor string) ([]SourceRootRecord, error) {
	if !validIDs(actor) {
		return nil, domain.ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,r.label,r.path FROM source_roots r
		JOIN source_root_grants g ON g.source_root_id=r.id WHERE g.user_id=$1 ORDER BY r.label`, actor)
	if err != nil {
		return nil, classify(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[SourceRootRecord])
	if out == nil {
		out = []SourceRootRecord{}
	}
	return out, classify(err)
}

// SourceRootByLabel returns the source root registered under label.
func (s *Store) SourceRootByLabel(ctx context.Context, label string) (SourceRootRecord, error) {
	var r SourceRootRecord
	err := s.pool.QueryRow(ctx, "SELECT id::text,label,path FROM source_roots WHERE label=$1", label).Scan(&r.ID, &r.Label, &r.Path)
	return r, classify(err)
}

// UserActive reports whether user exists and is not disabled.
func (s *Store) UserActive(ctx context.Context, user string) (bool, error) {
	if !validIDs(user) {
		return false, domain.ErrInvalid
	}
	var active bool
	err := s.pool.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1", user).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, classify(err)
}

// SetArtifactQuota sets the controller artifact store limit (bytes <= 0
// removes it) and bumps the scheduler limits revision when it changed.
func (s *Store) SetArtifactQuota(ctx context.Context, bytes int64) error {
	var next *int64
	if bytes > 0 {
		next = &bytes
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var cur *int64
		if err := tx.QueryRow(ctx, "SELECT artifact_quota_bytes FROM scheduler_limits WHERE singleton FOR UPDATE").Scan(&cur); err != nil {
			return false, err
		}
		if (cur == nil && next == nil) || (cur != nil && next != nil && *cur == *next) {
			return true, nil
		}
		if _, err := tx.Exec(ctx, "UPDATE scheduler_limits SET artifact_quota_bytes=$1,revision=revision+1,updated_at=clock_timestamp() WHERE singleton", next); err != nil {
			return false, err
		}
		var quota any
		if next != nil {
			quota = *next
		}
		return true, writeAudit(ctx, tx, principalAudit(OperatorPrincipal(), AuditArtifactQuotaSet).target("scheduler", "limits").with(map[string]any{"artifact_quota_bytes": quota}))
	})
	return err
}

// ArtifactUsage is the controller artifact accounting (ArtifactUsageSQL) and
// its limit (nil = none).
type ArtifactUsage struct {
	Bytes int64
	Quota *int64
}

// ArtifactUsage returns the artifact accounting the claim gate uses.
func (s *Store) ArtifactUsage(ctx context.Context) (ArtifactUsage, error) {
	var u ArtifactUsage
	if err := s.pool.QueryRow(ctx, "SELECT artifact_quota_bytes FROM scheduler_limits WHERE singleton").Scan(&u.Quota); err != nil {
		return u, classify(err)
	}
	err := s.pool.QueryRow(ctx, ArtifactUsageSQL).Scan(&u.Bytes)
	return u, classify(err)
}

// NodeState is an operator view of one node for personal status.
type NodeState struct {
	ID              string
	Revoked         bool
	LastHeartbeatAt *time.Time
	// Fresh: the last heartbeat is within NodeStaleAfter (database clock).
	Fresh         bool
	QuotaExceeded []string
	SourceRootIDs []string
	// Workers counts the node's workers by state (stopped/failed omitted).
	Workers map[string]int
}

// NodeStateByID returns the operator view of node id.
func (s *Store) NodeStateByID(ctx context.Context, id string) (NodeState, error) {
	if !validIDs(id) {
		return NodeState{}, domain.ErrInvalid
	}
	n := NodeState{ID: id, Workers: map[string]int{}}
	err := s.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL,last_heartbeat_at,coalesce(last_heartbeat_at>clock_timestamp()-$2*interval '1 millisecond',false),storage_quota_exceeded,
		coalesce((SELECT array_agg(source_root_id::text ORDER BY source_root_id) FROM node_source_roots WHERE node_id=n.id),'{}')
		FROM nodes n WHERE id=$1`, id, NodeStaleAfter.Milliseconds()).Scan(&n.Revoked, &n.LastHeartbeatAt, &n.Fresh, &n.QuotaExceeded, &n.SourceRootIDs)
	if err != nil {
		return n, classify(err)
	}
	rows, err := s.pool.Query(ctx, "SELECT state,count(*) FROM workers WHERE node_id=$1 AND state NOT IN ('stopped','failed') GROUP BY state", id)
	if err != nil {
		return n, classify(err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var c int
		if err := rows.Scan(&st, &c); err != nil {
			return n, classify(err)
		}
		n.Workers[st] = c
	}
	return n, classify(rows.Err())
}

// AttemptState is an attempt's ownership state and its run's status.
type AttemptState struct {
	ID, RunID, Status, Cleanup, RunStatus string
	Epoch                                 int64
	ExpiresAt                             time.Time
}

// NodeAttempts returns the node's active attempts (leased or
// recovery_required) plus any attempt listed in also, ordered by ID.
func (s *Store) NodeAttempts(ctx context.Context, node string, also []string) ([]AttemptState, error) {
	if !validIDs(node) || !validIDs(also...) {
		return nil, domain.ErrInvalid
	}
	if also == nil {
		also = []string{}
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,a.run_id::text,a.status,a.cleanup_state,r.status,a.epoch,a.expires_at
		FROM attempts a JOIN runs r ON r.id=a.run_id
		WHERE a.node_id=$1 AND (a.status IN ('leased','recovery_required') OR a.id::text = ANY($2)) ORDER BY a.id`, node, also)
	if err != nil {
		return nil, classify(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AttemptState, error) {
		var a AttemptState
		err := r.Scan(&a.ID, &a.RunID, &a.Status, &a.Cleanup, &a.RunStatus, &a.Epoch, &a.ExpiresAt)
		return a, err
	})
	if out == nil {
		out = []AttemptState{}
	}
	return out, classify(err)
}
