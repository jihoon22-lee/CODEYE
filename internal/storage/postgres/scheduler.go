package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Scheduler policy and autoscaler. Policy lives in append-only revision
// rows (pool_policies); the autoscaler's per-pool decision state (epoch,
// cooldown timestamps, last result) lives in pool_scaler_state so a
// controller restart neither forgets cooldowns nor re-issues starts. Starts
// still waiting for their worker to appear are derived from node_commands.

// PoolPolicy is one revision of a pool's worker policy.
type PoolPolicy struct {
	PoolID                   string
	PoolName                 string
	Revision                 int64
	MinWorkers               int
	MaxWorkers               int
	IdleTimeoutSeconds       int
	ScaleUpCooldownSeconds   int
	ScaleDownCooldownSeconds int
	CreatedAt                *time.Time
}

// DefaultPoolPolicy is the docs/EXECUTION.md starting point (not a measured value).
var DefaultPoolPolicy = PoolPolicy{MinWorkers: 0, MaxWorkers: 2, IdleTimeoutSeconds: 180, ScaleUpCooldownSeconds: 10, ScaleDownCooldownSeconds: 60}

// StartTimeout is how long an issued start_worker command counts as a pending
// worker before its worker must have appeared in a node heartbeat. A start
// the agent journaled but never spawned (crash in between) or refused stops
// counting after this and the autoscaler may issue a replacement.
var StartTimeout = 90 * time.Second

func (p PoolPolicy) validate() error {
	switch {
	case p.MinWorkers < 0 || p.MaxWorkers < 0 || p.MinWorkers > 256 || p.MaxWorkers > 256 || p.MinWorkers > p.MaxWorkers:
		return domain.ErrInvalid
	case p.IdleTimeoutSeconds < 1 || p.IdleTimeoutSeconds > 86400:
		return domain.ErrInvalid
	case p.ScaleUpCooldownSeconds < 0 || p.ScaleUpCooldownSeconds > 3600 || p.ScaleDownCooldownSeconds < 0 || p.ScaleDownCooldownSeconds > 3600:
		return domain.ErrInvalid
	}
	return nil
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// currentPolicy returns the pool's newest policy revision, or the defaults
// (revision 0) for a pool that has none yet.
func currentPolicy(ctx context.Context, q queryRower, poolID string) (PoolPolicy, error) {
	p := PoolPolicy{PoolID: poolID}
	var created time.Time
	err := q.QueryRow(ctx, `SELECT revision,min_workers,max_workers,idle_timeout_seconds,scale_up_cooldown_seconds,scale_down_cooldown_seconds,created_at
		FROM pool_policies WHERE pool_id=$1 ORDER BY revision DESC LIMIT 1`, poolID).Scan(&p.Revision, &p.MinWorkers, &p.MaxWorkers, &p.IdleTimeoutSeconds, &p.ScaleUpCooldownSeconds, &p.ScaleDownCooldownSeconds, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		d := DefaultPoolPolicy
		d.PoolID = poolID
		return d, nil
	}
	if err != nil {
		return p, err
	}
	p.CreatedAt = &created
	return p, nil
}

// ensurePoolRows gives a pool its default policy revision 1 and scaler state
// row if missing (called in the transaction that creates the pool).
func ensurePoolRows(ctx context.Context, tx pgx.Tx, poolID string) error {
	d := DefaultPoolPolicy
	if _, err := tx.Exec(ctx, `INSERT INTO pool_policies(pool_id,revision,min_workers,max_workers,idle_timeout_seconds,scale_up_cooldown_seconds,scale_down_cooldown_seconds)
		SELECT $1,1,$2,$3,$4,$5,$6 WHERE NOT EXISTS(SELECT 1 FROM pool_policies WHERE pool_id=$1)`,
		poolID, d.MinWorkers, d.MaxWorkers, d.IdleTimeoutSeconds, d.ScaleUpCooldownSeconds, d.ScaleDownCooldownSeconds); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "INSERT INTO pool_scaler_state(pool_id) VALUES($1) ON CONFLICT(pool_id) DO NOTHING", poolID)
	return err
}

// PoolPolicyUpdate changes selected fields; nil keeps the current value.
type PoolPolicyUpdate struct {
	Name                     string
	MinWorkers, MaxWorkers   *int
	IdleTimeoutSeconds       *int
	ScaleUpCooldownSeconds   *int
	ScaleDownCooldownSeconds *int
	// ExpectRevision, if set, must equal the current revision (else ErrConflict).
	ExpectRevision *int64
}

// Policy warnings (saved anyway; the pool shows degraded_capacity when the
// target cannot be met).
const (
	WarnMinExceedsGlobalRunning = "min_exceeds_global_running"
	WarnMinExceedsNodeCapacity  = "min_exceeds_node_capacity"
)

// SetPoolPolicy appends a policy revision for the named pool (created if it
// does not exist yet). An update that changes nothing returns the current
// revision without appending. It is a trusted operator action (CLI).
func (s *Store) SetPoolPolicy(ctx context.Context, u PoolPolicyUpdate) (PoolPolicy, []string, error) {
	if !poolNamePattern.MatchString(u.Name) {
		return PoolPolicy{}, nil, domain.ErrInvalid
	}
	type result struct {
		p PoolPolicy
		w []string
	}
	r, err := transaction(ctx, s, func(tx pgx.Tx) (result, error) {
		if _, err := tx.Exec(ctx, "INSERT INTO worker_pools(id,name) VALUES($1,$2) ON CONFLICT(name) DO NOTHING", domain.NewID(), u.Name); err != nil {
			return result{}, err
		}
		var poolID string
		if err := tx.QueryRow(ctx, "SELECT id::text FROM worker_pools WHERE name=$1", u.Name).Scan(&poolID); err != nil {
			return result{}, err
		}
		if err := ensurePoolRows(ctx, tx, poolID); err != nil {
			return result{}, err
		}
		// Serializes with the autoscaler's decision for this pool.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM pool_scaler_state WHERE pool_id=$1 FOR UPDATE", poolID); err != nil {
			return result{}, err
		}
		// Serialize policy changes with claims admitted under the old limits.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM scheduler_limits WHERE singleton FOR SHARE"); err != nil {
			return result{}, err
		}
		cur, err := currentPolicy(ctx, tx, poolID)
		if err != nil {
			return result{}, err
		}
		if u.ExpectRevision != nil && *u.ExpectRevision != cur.Revision {
			return result{}, domain.ErrConflict
		}
		next := cur
		set := func(dst *int, v *int) {
			if v != nil {
				*dst = *v
			}
		}
		set(&next.MinWorkers, u.MinWorkers)
		set(&next.MaxWorkers, u.MaxWorkers)
		set(&next.IdleTimeoutSeconds, u.IdleTimeoutSeconds)
		set(&next.ScaleUpCooldownSeconds, u.ScaleUpCooldownSeconds)
		set(&next.ScaleDownCooldownSeconds, u.ScaleDownCooldownSeconds)
		if err := next.validate(); err != nil {
			return result{}, err
		}
		if next != cur {
			next.Revision = cur.Revision + 1
			var created time.Time
			if err := tx.QueryRow(ctx, `INSERT INTO pool_policies(pool_id,revision,min_workers,max_workers,idle_timeout_seconds,scale_up_cooldown_seconds,scale_down_cooldown_seconds)
				VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, poolID, next.Revision, next.MinWorkers, next.MaxWorkers, next.IdleTimeoutSeconds, next.ScaleUpCooldownSeconds, next.ScaleDownCooldownSeconds).Scan(&created); err != nil {
				return result{}, err
			}
			next.CreatedAt = &created
		}
		next.PoolName = u.Name
		warnings := []string{}
		var sumMin, global int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(p.min_workers),0),(SELECT global_max_running FROM scheduler_limits WHERE singleton)
			FROM pool_policies p WHERE p.revision=(SELECT max(revision) FROM pool_policies q WHERE q.pool_id=p.pool_id)`).Scan(&sumMin, &global); err != nil {
			return result{}, err
		}
		if sumMin > global {
			warnings = append(warnings, WarnMinExceedsGlobalRunning)
		}
		var nodeCap int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(max_workers),0) FROM nodes WHERE pool_id=$1 AND revoked_at IS NULL AND worker_scaling='controller'
			AND last_heartbeat_at>clock_timestamp()-$2*interval '1 millisecond'`, poolID, NodeStaleAfter.Milliseconds()).Scan(&nodeCap); err != nil {
			return result{}, err
		}
		if int64(next.MinWorkers) > nodeCap {
			warnings = append(warnings, WarnMinExceedsNodeCapacity)
		}
		return result{next, warnings}, nil
	})
	return r.p, r.w, err
}

// PoolStatus is a pool's current policy and the autoscaler's last decision.
type PoolStatus struct {
	Policy          PoolPolicy
	Epoch           int64
	CapacityState   string
	Reasons         []string
	DesiredWorkers  int
	LiveWorkers     int
	BusyWorkers     int
	DrainingWorkers int
	PendingStarts   int
	QueuedDemand    int
	LastScaleUpAt   *time.Time
	LastScaleDownAt *time.Time
	ReconciledAt    *time.Time
}

// ListPools returns every pool with its current policy and last autoscaler
// state (trusted operator CLI).
func (s *Store) ListPools(ctx context.Context) ([]PoolStatus, error) {
	return transaction(ctx, s, func(tx pgx.Tx) ([]PoolStatus, error) {
		rows, err := tx.Query(ctx, `SELECT p.id::text,p.name,coalesce(st.epoch,0),coalesce(st.capacity_state,'healthy'),coalesce(st.reasons,'{}'),
			coalesce(st.desired_workers,0),coalesce(st.live_workers,0),coalesce(st.busy_workers,0),coalesce(st.draining_workers,0),
			coalesce(st.pending_starts,0),coalesce(st.queued_demand,0),st.last_scale_up_at,st.last_scale_down_at,st.reconciled_at
			FROM worker_pools p LEFT JOIN pool_scaler_state st ON st.pool_id=p.id ORDER BY p.name`)
		if err != nil {
			return nil, err
		}
		out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PoolStatus, error) {
			var p PoolStatus
			err := r.Scan(&p.Policy.PoolID, &p.Policy.PoolName, &p.Epoch, &p.CapacityState, &p.Reasons, &p.DesiredWorkers, &p.LiveWorkers, &p.BusyWorkers,
				&p.DrainingWorkers, &p.PendingStarts, &p.QueuedDemand, &p.LastScaleUpAt, &p.LastScaleDownAt, &p.ReconciledAt)
			return p, err
		})
		if err != nil {
			return nil, err
		}
		for i := range out {
			name := out[i].Policy.PoolName
			pol, err := currentPolicy(ctx, tx, out[i].Policy.PoolID)
			if err != nil {
				return nil, err
			}
			pol.PoolName = name
			out[i].Policy = pol
		}
		if out == nil {
			out = []PoolStatus{}
		}
		return out, nil
	})
}

// SchedulerLimits are the global and per-project concurrent attempt limits.
type SchedulerLimits struct {
	GlobalMaxRunning  int
	ProjectMaxRunning int
	Revision          int64
}

// SetSchedulerLimits changes the global and/or per-project concurrent
// attempt limits (nil keeps the value) and bumps the revision when anything
// changed. Trusted operator action.
func (s *Store) SetSchedulerLimits(ctx context.Context, global, project *int) (SchedulerLimits, error) {
	if (global == nil && project == nil) || (global != nil && (*global < 0 || *global > 10000)) || (project != nil && (*project < 1 || *project > 10000)) {
		return SchedulerLimits{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (SchedulerLimits, error) {
		var l SchedulerLimits
		if err := tx.QueryRow(ctx, "SELECT global_max_running,project_max_running,revision FROM scheduler_limits WHERE singleton FOR UPDATE").Scan(&l.GlobalMaxRunning, &l.ProjectMaxRunning, &l.Revision); err != nil {
			return l, err
		}
		next := l
		if global != nil {
			next.GlobalMaxRunning = *global
		}
		if project != nil {
			next.ProjectMaxRunning = *project
		}
		if next == l {
			return l, nil
		}
		next.Revision++
		_, err := tx.Exec(ctx, "UPDATE scheduler_limits SET global_max_running=$1,project_max_running=$2,revision=$3,updated_at=clock_timestamp() WHERE singleton",
			next.GlobalMaxRunning, next.ProjectMaxRunning, next.Revision)
		return next, err
	})
}
