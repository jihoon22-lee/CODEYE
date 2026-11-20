package postgres

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Autoscaler reasons (pool_scaler_state.reasons).
const (
	ScaleReasonNoControllerNode = "no_controller_node" // no fresh, controller-scaled node in the pool
	ScaleReasonNodeCapacity     = "node_capacity"      // demand or min_workers cannot be placed (node max_workers/resources)
	ScaleReasonScaleUpCooldown  = "scale_up_cooldown"  // a start is due but the scale-up cooldown has not elapsed
	ScaleReasonOverTarget       = "over_target"        // more busy workers than max_workers: draining
	ScaleReasonPaused           = "paused"             // max_workers = 0
)

var scaleReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ScaleAction is one command the autoscaler issued.
type ScaleAction struct {
	NodeID, WorkerID, Kind string
	Sequence               int64
}

// PoolReconcile is the outcome of one autoscaler decision for a pool.
type PoolReconcile struct {
	PoolID  string
	Skipped bool // another controller holds this pool's decision lock
	Status  PoolStatus
	Actions []ScaleAction
}

// ReconcilePools runs one autoscaler decision per pool, each in its own short
// transaction. A pool whose state row is locked by another controller is
// skipped (one decision per pool at a time).
func (s *Store) ReconcilePools(ctx context.Context) ([]PoolReconcile, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text FROM worker_pools ORDER BY id")
	if err != nil {
		return nil, classify(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, classify(err)
	}
	var out []PoolReconcile
	for _, id := range ids {
		r, err := transaction(ctx, s, func(tx pgx.Tx) (PoolReconcile, error) { return reconcilePool(ctx, tx, id) })
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// scaleWorker is a live worker process of a controller-scaled node as the
// controller knows it.
type scaleWorker struct {
	id, state string
	since     time.Time
	leaving   bool // drain/stop already issued
	pending   bool // start issued, not yet reported
}

type scaleNode struct {
	view    nodeView
	workers []*scaleWorker
	starts  int // planned this round
}

// processes counts worker processes that occupy a slot on the node (live,
// pending and leaving) plus planned starts.
func (n *scaleNode) processes() int { return len(n.workers) + n.starts }

// freeCount counts non-busy, non-leaving workers (idle, starting, pending).
func (n *scaleNode) freeCount() int {
	c := 0
	for _, w := range n.workers {
		if !w.leaving && w.state != nodeproto.WorkerBusy {
			c++
		}
	}
	return c
}

// canHost: another worker fits under the agent's max_workers and the node's
// resource budget. Every non-busy worker (and planned start) is charged one
// attempt reservation so warm workers cannot overcommit the node; busy ones
// are already in reservedCPU/observed memory.
func (n *scaleNode) canHost() bool {
	if n.processes() >= n.view.maxWorkers {
		return false
	}
	future := int64(n.freeCount() + n.starts + 1)
	return n.view.reservedCPU+future*ReserveCPUMillis <= n.view.cpuMillis &&
		n.view.freeMemory-n.view.unseenMemory-(future-1)*ReserveMemoryBytes >= ReserveMemoryBytes &&
		n.view.freeDisk-n.view.unseenDisk-(future-1)*ReserveDiskBytes >= ReserveDiskBytes
}

func reconcilePool(ctx context.Context, tx pgx.Tx, poolID string) (PoolReconcile, error) {
	out := PoolReconcile{PoolID: poolID}
	if err := ensurePoolRows(ctx, tx, poolID); err != nil {
		return out, err
	}
	// Decision lock for this pool (SKIP LOCKED: a concurrent controller is
	// already deciding). Then the same order as ClaimAttempt:
	// scheduler_limits, then node rows by ID.
	var epoch int64
	var lastUp, lastDown *time.Time
	err := tx.QueryRow(ctx, "SELECT epoch,last_scale_up_at,last_scale_down_at FROM pool_scaler_state WHERE pool_id=$1 FOR UPDATE SKIP LOCKED", poolID).Scan(&epoch, &lastUp, &lastDown)
	if errors.Is(err, pgx.ErrNoRows) {
		out.Skipped = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var global, projectLimit, poolMinimumTotal int64
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT global_max_running,project_max_running,clock_timestamp(),
		(SELECT coalesce(sum(current.min_workers),0) FROM
		 (SELECT DISTINCT ON (pool_id) min_workers FROM pool_policies ORDER BY pool_id,revision DESC) current)
		FROM scheduler_limits WHERE singleton FOR SHARE`).Scan(&global, &projectLimit, &now, &poolMinimumTotal); err != nil {
		return out, err
	}
	pol, err := currentPolicy(ctx, tx, poolID)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, "SELECT id::text FROM nodes WHERE pool_id=$1 AND revoked_at IS NULL AND worker_scaling='controller' ORDER BY id FOR UPDATE", poolID)
	if err != nil {
		return out, err
	}
	nodeIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return out, err
	}
	var nodes []*scaleNode
	for _, id := range nodeIDs {
		v, _, err := loadNode(ctx, tx, id, false)
		if err != nil {
			return out, err
		}
		if !v.lastBeatOK {
			continue // a stale node gets no commands; its workers are not counted
		}
		n := &scaleNode{view: v}
		wrows, err := tx.Query(ctx, `SELECT w.id::text,w.state,w.state_since,
			EXISTS(SELECT 1 FROM node_commands c WHERE c.worker_id=w.id AND c.node_id=w.node_id AND c.kind IN ('drain_worker','stop_worker'))
			FROM workers w WHERE w.node_id=$1 AND w.state NOT IN ('stopped','failed') ORDER BY w.id`, id)
		if err != nil {
			return out, err
		}
		ws, err := pgx.CollectRows(wrows, func(r pgx.CollectableRow) (*scaleWorker, error) {
			w := &scaleWorker{}
			err := r.Scan(&w.id, &w.state, &w.since, &w.leaving)
			if w.state == nodeproto.WorkerDraining {
				w.leaving = true
			}
			return w, err
		})
		if err != nil {
			return out, err
		}
		n.workers = ws
		prows, err := tx.Query(ctx, `SELECT c.worker_id::text,c.created_at FROM node_commands c
			WHERE c.node_id=$1 AND c.kind='start_worker' AND c.created_at>$2::timestamptz-$3*interval '1 millisecond'
			AND NOT EXISTS(SELECT 1 FROM workers w WHERE w.id=c.worker_id) ORDER BY c.sequence`, id, now, StartTimeout.Milliseconds())
		if err != nil {
			return out, err
		}
		pending, err := pgx.CollectRows(prows, func(r pgx.CollectableRow) (*scaleWorker, error) {
			w := &scaleWorker{state: nodeproto.WorkerStarting, pending: true}
			err := r.Scan(&w.id, &w.since)
			return w, err
		})
		if err != nil {
			return out, err
		}
		n.workers = append(n.workers, pending...)
		nodes = append(nodes, n)
	}

	busy, live, leaving, pendingStarts := 0, 0, 0, 0
	observedBusy := 0
	for _, n := range nodes {
		for _, w := range n.workers {
			if w.state == nodeproto.WorkerBusy {
				observedBusy++
			}
			switch {
			case w.leaving:
				leaving++
			case w.state == nodeproto.WorkerBusy:
				busy++
				live++
			default:
				live++
			}
			if w.pending {
				pendingStarts++
			}
		}
	}

	// Demand: queued runs that a fresh controller node of this pool can run,
	// through the same gate as ClaimAttempt (decodable request, capability,
	// source root grant of the node, snapshot location, requester permission
	// and root grant), capped by the remaining global/pool/project budgets.
	var globalActive, poolActive int64
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL),
		(SELECT count(*) FROM capacity_reservations c JOIN nodes n ON n.id=c.node_id WHERE c.released_at IS NULL AND n.pool_id=$1)`, poolID).Scan(&globalActive, &poolActive); err != nil {
		return out, err
	}
	budget := min(global-globalActive, int64(pol.MaxWorkers)-poolActive)
	freeLeft := map[*scaleNode]int{}
	for _, n := range nodes {
		freeLeft[n] = n.freeCount()
	}
	projectUsed := map[string]int64{}
	matched, unplaced := 0, 0
	if len(nodes) > 0 && budget > 0 {
		var after *candidate
	scan:
		for {
			page, last, err := fairPage(ctx, tx, projectLimit, after)
			if err != nil {
				return out, err
			}
			for _, c := range page {
				if int64(matched) >= budget {
					break scan
				}
				var eligible []*scaleNode
				for _, n := range nodes {
					if n.view.eligible(c) {
						eligible = append(eligible, n)
					}
				}
				if len(eligible) == 0 {
					continue
				}
				used, ok := projectUsed[c.projectID]
				if !ok {
					if err := tx.QueryRow(ctx, `SELECT count(*) FROM capacity_reservations c JOIN attempts a ON a.id=c.attempt_id JOIN runs ar ON ar.id=a.run_id
						WHERE c.released_at IS NULL AND ar.project_id=$1`, c.projectID).Scan(&used); err != nil {
						return out, err
					}
				}
				if used >= projectLimit {
					continue
				}
				allowed, err := runnable(ctx, tx, c)
				if err != nil {
					return out, err
				}
				if !allowed {
					continue // the next claim fails this run (actor_revoked / source_grant_revoked)
				}
				placed := false
				for _, n := range eligible {
					if freeLeft[n] > 0 {
						freeLeft[n]--
						placed = true
						break
					}
				}
				if !placed {
					sort.SliceStable(eligible, func(a, b int) bool { return eligible[a].processes() < eligible[b].processes() })
					for _, n := range eligible {
						if live+leaving+sumStarts(nodes) < pol.MaxWorkers && n.canHost() {
							n.starts++
							placed = true
							break
						}
					}
				}
				if !placed {
					unplaced++
					continue
				}
				projectUsed[c.projectID] = used + 1
				matched++
			}
			if last == nil {
				break
			}
			after = last
		}
	}

	reasons := []string{}
	addReason := func(r string) {
		for _, x := range reasons {
			if x == r {
				return
			}
		}
		if scaleReasonPattern.MatchString(r) {
			reasons = append(reasons, r)
		}
	}
	var actions []ScaleAction
	plan := func(n *scaleNode, worker, kind string) {
		actions = append(actions, ScaleAction{NodeID: n.view.id, WorkerID: worker, Kind: kind})
	}
	if len(nodes) == 0 {
		addReason(ScaleReasonNoControllerNode)
	}
	if pol.MaxWorkers == 0 {
		addReason(ScaleReasonPaused)
	}
	if pol.MinWorkers > 0 && poolMinimumTotal > global {
		addReason(WarnMinExceedsGlobalRunning)
	}
	if unplaced > 0 {
		addReason(ScaleReasonNodeCapacity)
	}

	starts := sumStarts(nodes)
	switch {
	case live > pol.MaxWorkers:
		// Policy reduction below the live count: no starts; stop idle workers
		// at once (no idle timeout, no cooldown), then drain busy ones. Busy
		// work is never killed: drained workers exit after their attempt.
		for _, n := range nodes {
			n.starts = 0
		}
		starts = 0
		excess := live - pol.MaxWorkers
		for _, n := range nodes {
			for _, w := range n.workers {
				if excess > 0 && !w.leaving && w.state == nodeproto.WorkerIdle {
					plan(n, w.id, nodeproto.CommandStopWorker)
					w.leaving = true
					excess--
				}
			}
		}
		if busy > pol.MaxWorkers {
			addReason(ScaleReasonOverTarget)
			drain := busy - pol.MaxWorkers
			for _, n := range nodes {
				for _, w := range n.workers {
					if drain > 0 && excess > 0 && !w.leaving && w.state == nodeproto.WorkerBusy {
						plan(n, w.id, nodeproto.CommandDrainWorker)
						w.leaving = true
						drain--
						excess--
					}
				}
			}
		}
	default:
		// Warm minimum: keep at least min_workers processes.
		for live+starts < pol.MinWorkers && live+leaving+starts < pol.MaxWorkers {
			var best *scaleNode
			for _, n := range nodes {
				if n.canHost() && (best == nil || n.processes() < best.processes()) {
					best = n
				}
			}
			if best == nil {
				addReason(ScaleReasonNodeCapacity)
				break
			}
			best.starts++
			starts++
		}
		if starts > 0 && lastUp != nil && now.Sub(*lastUp) < time.Duration(pol.ScaleUpCooldownSeconds)*time.Second {
			addReason(ScaleReasonScaleUpCooldown)
			for _, n := range nodes {
				n.starts = 0
			}
			starts = 0
		}
		for _, n := range nodes {
			for i := 0; i < n.starts; i++ {
				plan(n, domain.NewID(), nodeproto.CommandStartWorker)
			}
		}
		// Demand scale-down: only when nothing starts this round, after the
		// scale-down cooldown since the last scaling action, and only idle
		// workers idle for at least idle_timeout that no demand was matched to.
		target := max(pol.MinWorkers, busy+matched)
		lastAction := lastUp
		if lastDown != nil && (lastAction == nil || lastDown.After(*lastAction)) {
			lastAction = lastDown
		}
		cooled := lastAction == nil || now.Sub(*lastAction) >= time.Duration(pol.ScaleDownCooldownSeconds)*time.Second
		if starts == 0 && live > target && cooled {
			var idle []*scaleWorker
			owner := map[*scaleWorker]*scaleNode{}
			for _, n := range nodes {
				spare := n.freeCount() - freeLeft[n] // free workers matched to demand on this node
				for _, w := range n.workers {
					if w.leaving || w.state != nodeproto.WorkerIdle {
						continue
					}
					if spare > 0 {
						spare-- // keep the matched ones (they are about to claim)
						continue
					}
					if now.Sub(w.since) >= time.Duration(pol.IdleTimeoutSeconds)*time.Second {
						idle = append(idle, w)
						owner[w] = n
					}
				}
			}
			sort.SliceStable(idle, func(a, b int) bool { return idle[a].since.Before(idle[b].since) })
			for _, w := range idle {
				if live <= target {
					break
				}
				plan(owner[w], w.id, nodeproto.CommandStopWorker)
				w.leaving = true
				live--
			}
		}
	}
	if live+starts < pol.MinWorkers && !contains(reasons, ScaleReasonScaleUpCooldown) {
		addReason(ScaleReasonNodeCapacity)
	}

	// Issue commands under the node row locks taken above; the per-node
	// sequence therefore follows commit order (same rule as cancel_attempt).
	for i := range actions {
		a := &actions[i]
		var seq int64
		if err := tx.QueryRow(ctx, "UPDATE nodes SET command_seq=command_seq+1 WHERE id=$1 RETURNING command_seq", a.NodeID).Scan(&seq); err != nil {
			return out, err
		}
		a.Sequence = seq
		if _, err := tx.Exec(ctx, "INSERT INTO node_commands(id,node_id,sequence,kind,worker_id) VALUES($1,$2,$3,$4,$5)", domain.NewID(), a.NodeID, seq, a.Kind, a.WorkerID); err != nil {
			return out, err
		}
	}
	startsIssued, stopsIssued := 0, 0
	drainingNow := leaving
	for _, a := range actions {
		if a.Kind == nodeproto.CommandStartWorker {
			startsIssued++
		} else {
			stopsIssued++
			drainingNow++
		}
	}
	state := "healthy"
	switch {
	case pol.MaxWorkers == 0:
		state = "paused"
	case observedBusy > pol.MaxWorkers:
		addReason(ScaleReasonOverTarget)
		state = "over_target_draining"
	case contains(reasons, WarnMinExceedsGlobalRunning) || contains(reasons, ScaleReasonNodeCapacity) || contains(reasons, ScaleReasonNoControllerNode) && (pol.MinWorkers > 0 || unplaced > 0):
		state = "degraded_capacity"
	}
	if len(nodes) == 0 && (pol.MinWorkers > 0) {
		state = "degraded_capacity"
	}
	liveAfter := 0
	for _, n := range nodes {
		for _, w := range n.workers {
			if !w.leaving {
				liveAfter++
			}
		}
	}
	liveAfter += startsIssued
	st := PoolStatus{Policy: pol, Epoch: epoch + 1, CapacityState: state, Reasons: reasons, DesiredWorkers: min(max(pol.MinWorkers, busy+matched), pol.MaxWorkers),
		LiveWorkers: liveAfter, BusyWorkers: observedBusy, DrainingWorkers: drainingNow, PendingStarts: pendingStarts + startsIssued, QueuedDemand: matched + unplaced}
	err = tx.QueryRow(ctx, `UPDATE pool_scaler_state SET epoch=$2,policy_revision=$3,capacity_state=$4,reasons=$5,desired_workers=$6,live_workers=$7,
		busy_workers=$8,draining_workers=$9,pending_starts=$10,queued_demand=$11,
		last_scale_up_at=CASE WHEN $12 THEN clock_timestamp() ELSE last_scale_up_at END,
		last_scale_down_at=CASE WHEN $13 THEN clock_timestamp() ELSE last_scale_down_at END,
		reconciled_at=clock_timestamp() WHERE pool_id=$1 RETURNING last_scale_up_at,last_scale_down_at,reconciled_at`,
		poolID, st.Epoch, nullRevision(pol.Revision), st.CapacityState, st.Reasons, st.DesiredWorkers, st.LiveWorkers, st.BusyWorkers, st.DrainingWorkers,
		st.PendingStarts, st.QueuedDemand, startsIssued > 0, stopsIssued > 0).Scan(&st.LastScaleUpAt, &st.LastScaleDownAt, &st.ReconciledAt)
	if err != nil {
		return out, err
	}
	out.Status, out.Actions = st, actions
	return out, nil
}

func nullRevision(r int64) *int64 {
	if r < 1 {
		return nil
	}
	return &r
}

func sumStarts(nodes []*scaleNode) int {
	s := 0
	for _, n := range nodes {
		s += n.starts
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// runnable is the read-only form of ClaimAttempt's requester check: the
// actor may still run in the project and, for a local source, still holds
// the source-root grant.
func runnable(ctx context.Context, tx pgx.Tx, c candidate) (bool, error) {
	var owner string
	if err := tx.QueryRow(ctx, "SELECT owner_id::text FROM projects WHERE id=$1 AND deleted_at IS NULL", c.projectID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	ok, err := actorMayRun(ctx, tx, c.actorID, c.projectID, owner)
	if err != nil || !ok {
		return false, err
	}
	if c.source.Kind == domain.SourceLocal {
		var granted bool
		if err := tx.QueryRow(ctx, "SELECT "+rootAllowedSQL("$1", "$2"), c.source.SourceRootID, c.actorID).Scan(&granted); err != nil {
			return false, err
		}
		return granted, nil
	}
	return true, nil
}
