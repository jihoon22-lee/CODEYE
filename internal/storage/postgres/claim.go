package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Default per-attempt reservation (W06 fixed demand; W07 derives it from the
// pool/profile policy). Reservations are admission bookkeeping, not OS limits.
const (
	ReserveCPUMillis   = 1000
	ReserveMemoryBytes = 1 << 30
	ReserveDiskBytes   = 1 << 30
)

// candidatePage is how many queued runs one keyset page reads; a claim pages
// through the whole queue until it finds a compatible run.
const candidatePage = 200

// ClaimResult is a claim outcome. Response is nil for "nothing claimable" (204).
type ClaimResult struct {
	Response *nodeproto.ClaimResponse
}

type candidate struct {
	runID, projectID, actorID string
	createdAt                 time.Time
	request                   domain.RunRequest
	source                    domain.Source
	gitHost                   string
	snapshotID                *string
}

// nodeView is a node's scheduling state. Accounting model (docs/09): CPU is a
// policy budget (reported cores) minus all active reservations; memory and
// disk are the host's observed free amounts (which already exclude running
// processes) minus only the reservations made after the last heartbeat, which
// that observation cannot reflect yet. Observed free space is never reduced
// by every reservation again (no double counting).
type nodeView struct {
	id           string
	caps         map[string]bool
	roots        map[string]bool
	snapshots    map[string]bool
	cpuMillis    int64
	freeMemory   int64
	freeDisk     int64
	reservedCPU  int64
	unseenMemory int64
	unseenDisk   int64
	lastBeatOK   bool
}

func (n nodeView) eligible(c candidate) bool {
	if !n.caps[nodeproto.AnalysisCapability(c.request.Profile)] {
		return false
	}
	switch c.source.Kind {
	case domain.SourceLocal:
		if !n.caps[nodeproto.LocalSourceCapability(c.source.SourceRootID)] || !n.roots[c.source.SourceRootID] {
			return false
		}
	case domain.SourceGit:
		if c.gitHost == "" || !n.caps[nodeproto.GitSourceCapability(c.gitHost)] {
			return false
		}
	default:
		return false
	}
	return c.snapshotID == nil || n.snapshots[*c.snapshotID]
}

func (n nodeView) hasRoom() bool {
	return n.reservedCPU+ReserveCPUMillis <= n.cpuMillis &&
		n.freeMemory-n.unseenMemory >= ReserveMemoryBytes &&
		n.freeDisk-n.unseenDisk >= ReserveDiskBytes
}

// loadNode reads a node's scheduling view. lock takes the node row FOR UPDATE.
func loadNode(ctx context.Context, tx pgx.Tx, id string, lock bool) (nodeView, *time.Time, error) {
	q := `SELECT revoked_at,capabilities,coalesce(cpu_cores,0),coalesce(memory_available_bytes,0),coalesce(disk_available_bytes,0),
		coalesce(last_heartbeat_at>clock_timestamp()-$2*interval '1 millisecond',false) FROM nodes WHERE id=$1`
	if lock {
		q += " FOR UPDATE"
	}
	n := nodeView{id: id, caps: map[string]bool{}, roots: map[string]bool{}, snapshots: map[string]bool{}}
	var revoked *time.Time
	var caps []string
	var cores float64
	if err := tx.QueryRow(ctx, q, id, NodeStaleAfter.Milliseconds()).Scan(&revoked, &caps, &cores, &n.freeMemory, &n.freeDisk, &n.lastBeatOK); err != nil {
		return n, nil, err
	}
	for _, c := range caps {
		n.caps[c] = true
	}
	n.cpuMillis = int64(cores * 1000)
	rows, err := tx.Query(ctx, "SELECT source_root_id::text FROM node_source_roots WHERE node_id=$1", id)
	if err != nil {
		return n, revoked, err
	}
	roots, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return n, revoked, err
	}
	for _, r := range roots {
		n.roots[r] = true
	}
	rows, err = tx.Query(ctx, "SELECT snapshot_id::text FROM snapshot_locations WHERE node_id=$1", id)
	if err != nil {
		return n, revoked, err
	}
	snaps, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return n, revoked, err
	}
	for _, s := range snaps {
		n.snapshots[s] = true
	}
	err = tx.QueryRow(ctx, `SELECT coalesce(sum(c.cpu_millis),0),
		coalesce(sum(c.memory_bytes) FILTER (WHERE n.last_heartbeat_at IS NULL OR c.created_at>n.last_heartbeat_at),0),
		coalesce(sum(c.disk_bytes) FILTER (WHERE n.last_heartbeat_at IS NULL OR c.created_at>n.last_heartbeat_at),0)
		FROM capacity_reservations c JOIN nodes n ON n.id=c.node_id WHERE c.node_id=$1 AND c.released_at IS NULL`, id).Scan(&n.reservedCPU, &n.unseenMemory, &n.unseenDisk)
	return n, revoked, err
}

const candidateSQL = `SELECT r.id::text,r.project_id::text,r.actor_id::text,r.request,pr.config->'source',r.snapshot_id::text,s.manifest_digest,r.created_at
	FROM runs r
	JOIN projects p ON p.id=r.project_id AND p.deleted_at IS NULL
	JOIN project_revisions pr ON pr.project_id=r.project_id AND pr.revision=r.project_revision
	LEFT JOIN snapshots s ON s.id=r.snapshot_id
	WHERE r.status='queued' AND r.request IS NOT NULL `

func scanCandidate(row pgx.Row) (candidate, bool, error) {
	var c candidate
	var req, src []byte
	var manifest *string
	if err := row.Scan(&c.runID, &c.projectID, &c.actorID, &req, &src, &c.snapshotID, &manifest, &c.createdAt); err != nil {
		return c, false, err
	}
	// A stored request or source that does not decode under the current rules
	// is never dispatched (it stays queued; it is not reinterpreted).
	if domain.DecodeStrict(req, &c.request) != nil || c.request.Validate() != nil || src == nil {
		return c, false, nil
	}
	parsed, err := domain.ParseSource(src)
	if err != nil {
		return c, false, nil
	}
	c.source = parsed
	if parsed.Kind == domain.SourceGit {
		host, err := domain.RepositoryHost(parsed.RepositoryURL)
		if err != nil {
			return c, false, nil
		}
		c.gitHost = host
	}
	// A pre-W06 snapshot has no manifest digest and no location: never pinned-retried.
	if c.snapshotID != nil && manifest == nil {
		return c, false, nil
	}
	return c, true, nil
}

// ClaimAttempt atomically assigns the oldest compatible queued run to worker
// of node. One transaction takes, in order, the scheduler_limits row, the node
// row, then (per candidate) the project and run rows; it checks the global
// limit, node freshness/capability/grants/snapshot location and reserved
// capacity, the actor's run permission, and then creates the attempt (epoch+1),
// the reservation, the run transition and the attempt.leased event, and stores
// the response under (node, key). The same key from the same worker replays
// the stored response; from another worker it is ErrIdempotencyConflict. An
// unknown/stopped worker of this node, or one that still owns a leased or
// recovery_required attempt, is ErrConflict.
func (s *Store) ClaimAttempt(ctx context.Context, node, worker, key string) (ClaimResult, error) {
	if !validIDs(node, worker) || !validIdemKey(key) {
		return ClaimResult{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (ClaimResult, error) {
		var limit int64
		if err := tx.QueryRow(ctx, "SELECT global_max_running FROM scheduler_limits WHERE singleton FOR UPDATE").Scan(&limit); err != nil {
			return ClaimResult{}, err
		}
		n, revoked, err := loadNode(ctx, tx, node, true)
		if err != nil {
			return ClaimResult{}, err
		}
		if revoked != nil {
			return ClaimResult{}, domain.ErrUnauthenticated
		}
		var storedWorker string
		var stored []byte
		err = tx.QueryRow(ctx, "SELECT worker_id::text,response FROM node_claims WHERE node_id=$1 AND idempotency_key=$2", node, key).Scan(&storedWorker, &stored)
		if err == nil {
			if storedWorker != worker {
				return ClaimResult{}, ErrIdempotencyConflict
			}
			if stored == nil {
				return ClaimResult{}, nil
			}
			var resp nodeproto.ClaimResponse
			if err := json.Unmarshal(stored, &resp); err != nil {
				return ClaimResult{}, err
			}
			return ClaimResult{Response: &resp}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ClaimResult{}, err
		}
		var state string
		if err := tx.QueryRow(ctx, "SELECT state FROM workers WHERE id=$1 AND node_id=$2 FOR UPDATE", worker, node).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ClaimResult{}, domain.ErrConflict
			}
			return ClaimResult{}, err
		}
		var busy bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE worker_id=$1 AND status IN ('leased','recovery_required'))", worker).Scan(&busy); err != nil {
			return ClaimResult{}, err
		}
		// Only a worker the agent last reported idle, owning no active attempt,
		// may claim (starting/busy/draining/stopped/failed are refused).
		if busy || state != nodeproto.WorkerIdle {
			return ClaimResult{}, domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, "DELETE FROM node_claims WHERE node_id=$1 AND created_at<clock_timestamp()-interval '1 day'", node); err != nil {
			return ClaimResult{}, err
		}
		none := func() (ClaimResult, error) {
			_, err := tx.Exec(ctx, "INSERT INTO node_claims(node_id,idempotency_key,worker_id) VALUES($1,$2,$3)", node, key, worker)
			return ClaimResult{}, err
		}
		var active int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL").Scan(&active); err != nil {
			return ClaimResult{}, err
		}
		if !n.lastBeatOK || active >= limit || !n.hasRoom() {
			return none()
		}
		// Page through the whole queue (oldest first) so incompatible runs at
		// the head never starve a compatible one behind them.
		var afterTime *time.Time
		var afterID *string
		for {
			page, last, err := candidatePageFor(ctx, tx, n, afterTime, afterID)
			if err != nil {
				return ClaimResult{}, err
			}
			for _, c := range page {
				resp, ok, err := claimCandidate(ctx, tx, c, node, worker)
				if err != nil {
					return ClaimResult{}, err
				}
				if !ok {
					continue
				}
				encoded, err := json.Marshal(resp)
				if err != nil {
					return ClaimResult{}, err
				}
				if _, err := tx.Exec(ctx, "INSERT INTO node_claims(node_id,idempotency_key,worker_id,attempt_id,response) VALUES($1,$2,$3,$4,$5)", node, key, worker, resp.Lease.AttemptID, encoded); err != nil {
					return ClaimResult{}, err
				}
				return ClaimResult{Response: resp}, nil
			}
			if last == nil {
				break
			}
			afterTime, afterID = &last.createdAt, &last.runID
		}
		return none()
	})
}

// candidatePageFor reads one keyset page of queued runs after (afterTime,
// afterID) and returns those node n may run, plus the page's last row (nil
// when the queue is exhausted).
func candidatePageFor(ctx context.Context, tx pgx.Tx, n nodeView, afterTime *time.Time, afterID *string) ([]candidate, *candidate, error) {
	rows, err := tx.Query(ctx, candidateSQL+"AND ($1::timestamptz IS NULL OR (r.created_at,r.id)>($1::timestamptz,$2::uuid)) ORDER BY r.created_at,r.id LIMIT $3", afterTime, afterID, candidatePage)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []candidate
	var last *candidate
	count := 0
	for rows.Next() {
		c, ok, err := scanCandidate(rows)
		if err != nil {
			return nil, nil, err
		}
		count++
		lc := c
		last = &lc
		if ok && n.eligible(c) {
			out = append(out, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if count < candidatePage {
		last = nil
	}
	return out, last, nil
}

func validIdemKey(k string) bool {
	if k == "" || len(k) > 200 {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

// claimCandidate locks project -> run, re-checks the run and its actor, and
// creates the lease. ok=false means the candidate is no longer claimable.
func claimCandidate(ctx context.Context, tx pgx.Tx, c candidate, node, worker string) (*nodeproto.ClaimResponse, bool, error) {
	var owner string
	if err := tx.QueryRow(ctx, "SELECT owner_id::text FROM projects WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", c.projectID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	r, err := lockRun(ctx, tx, c.runID)
	if err != nil {
		return nil, false, err
	}
	if r.Status != domain.RunQueued {
		return nil, false, nil
	}
	var unreleased bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE run_id=$1 AND status!='released')", r.ID).Scan(&unreleased); err != nil {
		return nil, false, err
	}
	if unreleased {
		return nil, false, nil
	}
	allowed, err := actorMayRun(ctx, tx, r.ActorID, r.ProjectID, owner)
	if err != nil {
		return nil, false, err
	}
	reason := nodeproto.ReasonActorRevoked
	if allowed && c.source.Kind == domain.SourceLocal {
		// The requester must still hold the source-root grant the project was
		// registered under (FOR SHARE: ordered against a concurrent revoke).
		var granted bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM source_root_grants WHERE source_root_id=$1 AND user_id=$2 FOR SHARE)", c.source.SourceRootID, r.ActorID).Scan(&granted); err != nil {
			return nil, false, err
		}
		if !granted {
			allowed, reason = false, nodeproto.ReasonSourceGrantRevoked
		}
	}
	if !allowed {
		// The requester lost run permission (or was disabled) while the run
		// waited: it must not execute on their behalf.
		if _, err := tx.Exec(ctx, "UPDATE tasks SET status='skipped_policy',version=version+1,reason=$2 WHERE run_id=$1 AND status IN ('queued','blocked')", r.ID, reason); err != nil {
			return nil, false, err
		}
		return nil, false, transitionRun(ctx, tx, &r, domain.RunFailed, "run.failed", map[string]any{"status": domain.RunFailed, "reason": reason})
	}
	a, err := readAttempt(tx.QueryRow(ctx, "INSERT INTO attempts(id,run_id,node_id,worker_id,number,epoch,expires_at,status) SELECT $1,$2,$3,$4,coalesce(max(number),0)+1,$5,clock_timestamp()+$6*interval '1 second','leased' FROM attempts WHERE run_id=$2 RETURNING "+attemptColumns,
		domain.NewID(), r.ID, node, worker, r.LeaseEpoch+1, LeaseTTL.Seconds()))
	if err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, "UPDATE runs SET lease_epoch=$2,status='preparing' WHERE id=$1", r.ID, a.Epoch); err != nil {
		return nil, false, err
	}
	r.Status, r.LeaseEpoch = domain.RunPreparing, a.Epoch
	if _, err = tx.Exec(ctx, "INSERT INTO capacity_reservations(attempt_id,node_id,worker_id,cpu_millis,memory_bytes,disk_bytes) VALUES($1,$2,$3,$4,$5,$6)", a.ID, node, worker, ReserveCPUMillis, ReserveMemoryBytes, ReserveDiskBytes); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, "UPDATE workers SET state='busy',attempt_id=$2 WHERE id=$1", worker, a.ID); err != nil {
		return nil, false, err
	}
	if err = appendEvent(ctx, tx, &r, "attempt.leased", map[string]any{"attempt_id": a.ID, "epoch": a.Epoch, "node_id": node, "worker_id": worker}, true); err != nil {
		return nil, false, err
	}
	resp := &nodeproto.ClaimResponse{
		Lease: nodeproto.Lease{RunID: r.ID, AttemptID: a.ID, WorkerID: worker, LeaseEpoch: a.Epoch, ExpiresAt: a.ExpiresAt.UTC(),
			ProjectRevision: r.ProjectRevision, SnapshotID: r.SnapshotID, RunVersion: r.Version},
		Job: nodeproto.Job{Profile: c.request.Profile, Scope: c.request.Scope,
			Resources: nodeproto.Resources{CPUMillis: ReserveCPUMillis, MemoryBytes: ReserveMemoryBytes, DiskBytes: ReserveDiskBytes}},
	}
	switch c.source.Kind {
	case domain.SourceLocal:
		resp.Job.Source = nodeproto.JobSource{Kind: "local", SourceRootID: c.source.SourceRootID, RelativePath: c.source.RelativePath}
	case domain.SourceGit:
		resp.Job.Source = nodeproto.JobSource{Kind: "git", RepositoryURL: c.source.RepositoryURL, Branch: c.source.Branch, Host: c.gitHost}
	}
	if r.SnapshotID != nil {
		snap := nodeproto.JobSnapshot{ID: *r.SnapshotID}
		if err = tx.QueryRow(ctx, "SELECT manifest_digest,consistency,source_sha FROM snapshots WHERE id=$1", *r.SnapshotID).Scan(&snap.ManifestDigest, &snap.Consistency, &snap.SourceSHA); err != nil {
			return nil, false, err
		}
		snap.Kind = snapshotKind(snap.Consistency)
		resp.Job.Snapshot = &snap
	}
	return resp, true, nil
}

func snapshotKind(consistency string) string {
	if consistency == "git_commit" {
		return "git"
	}
	return "local"
}

// actorMayRun: enabled user who owns the project or is a runner/manager member.
func actorMayRun(ctx context.Context, tx pgx.Tx, actor, project, owner string) (bool, error) {
	var enabled bool
	if err := tx.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1", actor).Scan(&enabled); err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	if actor == owner {
		return true, nil
	}
	var role domain.Role
	err := tx.QueryRow(ctx, "SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2", project, actor).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role.Valid() && role.CanRun(), nil
}

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
	fresh, capable, room := false, false, false
	for _, v := range views {
		if !v.lastBeatOK {
			continue
		}
		fresh = true
		if v.eligible(c) {
			capable = true
			room = room || v.hasRoom()
		}
	}
	switch {
	case !fresh:
		return reason(nodeproto.WaitingNodeStale)
	case !capable:
		return reason(nodeproto.WaitingNoCapableNode)
	}
	var limit, active int64
	if err := tx.QueryRow(ctx, "SELECT global_max_running,(SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL) FROM scheduler_limits WHERE singleton").Scan(&limit, &active); err != nil {
		return nil, err
	}
	if active >= limit || !room {
		return reason(nodeproto.WaitingCapacityFull)
	}
	return nil, nil
}
