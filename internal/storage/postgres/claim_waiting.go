package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// waitingReason explains, from structured state only, why a queued run has
// not been claimed. nil means a fresh capable node with free capacity exists
// (the run is claimable by its next idle worker).
func waitingReason(ctx context.Context, tx pgx.Tx, runID string) (*string, error) {
	reason := func(s string) (*string, error) { return &s, nil }
	var hasRequest bool
	if err := tx.QueryRow(ctx, "SELECT request IS NOT NULL FROM runs WHERE id=$1", runID).Scan(&hasRequest); err != nil {
		return nil, err
	}
	if !hasRequest {
		return reason(nodeproto.WaitingRequestUnavailable)
	}
	var nodes []string
	rows, err := tx.Query(ctx, "SELECT id::text FROM nodes WHERE revoked_at IS NULL ORDER BY id")
	if err != nil {
		return nil, err
	}
	if nodes, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return reason(nodeproto.WaitingExecutionUnavailable)
	}
	c, ok, err := scanCandidate(tx.QueryRow(ctx, candidateSQL+"AND r.id=$1", runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return reason(nodeproto.WaitingNoCapableNode)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		var pinned bool
		if err := tx.QueryRow(ctx, "SELECT snapshot_id IS NOT NULL FROM runs WHERE id=$1", runID).Scan(&pinned); err != nil {
			return nil, err
		}
		if pinned {
			return reason(nodeproto.WaitingSnapshotUnavailable)
		}
		return reason(nodeproto.WaitingNoCapableNode)
	}
	var views []nodeView
	for _, id := range nodes {
		v, _, err := loadNode(ctx, tx, id, false)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	if c.snapshotID != nil {
		located := false
		for _, v := range views {
			located = located || v.snapshots[*c.snapshotID]
		}
		if !located {
			return reason(nodeproto.WaitingSnapshotUnavailable)
		}
	}
	fresh, capable := false, false
	var capableViews []nodeView
	for _, v := range views {
		if !v.lastBeatOK {
			continue
		}
		fresh = true
		if v.eligible(c) {
			capable = true
			capableViews = append(capableViews, v)
		}
	}
	switch {
	case !fresh:
		return reason(nodeproto.WaitingNodeStale)
	case !capable:
		return reason(nodeproto.WaitingNoCapableNode)
	}
	var limit, projectLimit, active, projectActive int64
	var artifactQuota *int64
	if err := tx.QueryRow(ctx, `SELECT global_max_running,project_max_running,(SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL),
		(SELECT count(*) FROM capacity_reservations c JOIN attempts a ON a.id=c.attempt_id JOIN runs ar ON ar.id=a.run_id WHERE c.released_at IS NULL AND ar.project_id=$1),
		artifact_quota_bytes
		FROM scheduler_limits WHERE singleton`, c.projectID).Scan(&limit, &projectLimit, &active, &projectActive, &artifactQuota); err != nil {
		return nil, err
	}
	if projectActive >= projectLimit {
		return reason(nodeproto.WaitingProjectLimit)
	}
	// Pool budget: a capable node has room only if its pool is below
	// max_workers; pools with max 0 are paused. A node over a storage quota
	// has no room; quotaOnly records that it would have otherwise.
	paused, room, quotaOnly := true, false, false
	for _, v := range capableViews {
		pol, err := currentPolicy(ctx, tx, v.poolID)
		if err != nil {
			return nil, err
		}
		if pol.MaxWorkers == 0 {
			continue
		}
		paused = false
		var poolActive int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM capacity_reservations c JOIN nodes n ON n.id=c.node_id WHERE c.released_at IS NULL AND n.pool_id=$1", v.poolID).Scan(&poolActive); err != nil {
			return nil, err
		}
		free := v.hasRoom() && poolActive < int64(pol.MaxWorkers)
		room = room || (free && len(v.quotaExceeded) == 0)
		quotaOnly = quotaOnly || (free && len(v.quotaExceeded) > 0)
	}
	if paused {
		return reason(nodeproto.WaitingPoolPaused)
	}
	if active >= limit {
		return reason(nodeproto.WaitingCapacityFull)
	}
	reached, err := artifactQuotaReached(ctx, tx, artifactQuota)
	if err != nil {
		return nil, err
	}
	if reached || (!room && quotaOnly) {
		return reason(nodeproto.WaitingStorageQuotaExceeded)
	}
	if !room {
		return reason(nodeproto.WaitingCapacityFull)
	}
	return nil, nil
}
