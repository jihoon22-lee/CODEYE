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

// ErrIdempotencyConflict is returned when a claim Idempotency-Key was already
// used by this node for a different worker.
var ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")

var (
	reasonPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	poolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	osArchPattern   = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
)

const (
	maxEnrollTTL = 24 * time.Hour
	// LeaseTTL is the attempt lease length granted by claims and renewals.
	LeaseTTL = nodeproto.LeaseSeconds * time.Second
	// NodeStaleAfter: no claims for a node whose last heartbeat is older.
	NodeStaleAfter = nodeproto.NodeStaleSeconds * time.Second
)

// EnrollmentToken describes an issued token. The raw token is never stored.
type EnrollmentToken struct {
	ID, PoolID, PoolName string
	SourceRootIDs        []string
	ExpiresAt            time.Time
}

// IssueEnrollmentToken stores the SHA-256 of a single-use node enrollment
// token bound to pool (created by name if missing) and the listed approved
// source roots. It is a trusted operator action (CLI).
func (s *Store) IssueEnrollmentToken(ctx context.Context, pool string, roots []string, tokenSHA256 string, ttl time.Duration) (EnrollmentToken, error) {
	if !poolNamePattern.MatchString(pool) || !hexDigest.MatchString(tokenSHA256) || ttl < time.Second || ttl > maxEnrollTTL || len(roots) > 100 {
		return EnrollmentToken{}, domain.ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range roots {
		if !validIDs(r) || seen[r] {
			return EnrollmentToken{}, domain.ErrInvalid
		}
		seen[r] = true
	}
	return transaction(ctx, s, func(tx pgx.Tx) (EnrollmentToken, error) {
		t := EnrollmentToken{ID: domain.NewID(), PoolName: pool, SourceRootIDs: append([]string{}, roots...)}
		if _, err := tx.Exec(ctx, "INSERT INTO worker_pools(id,name) VALUES($1,$2) ON CONFLICT(name) DO NOTHING", domain.NewID(), pool); err != nil {
			return t, err
		}
		if err := tx.QueryRow(ctx, "SELECT id::text FROM worker_pools WHERE name=$1", pool).Scan(&t.PoolID); err != nil {
			return t, err
		}
		if err := ensurePoolRows(ctx, tx, t.PoolID); err != nil {
			return t, err
		}
		if err := tx.QueryRow(ctx, "INSERT INTO node_enrollment_tokens(id,token_sha256,pool_id,expires_at) VALUES($1,$2,$3,clock_timestamp()+$4*interval '1 millisecond') RETURNING expires_at",
			t.ID, tokenSHA256, t.PoolID, ttl.Milliseconds()).Scan(&t.ExpiresAt); err != nil {
			return t, err
		}
		for _, r := range roots {
			var exists bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM source_roots WHERE id=$1)", r).Scan(&exists); err != nil {
				return t, err
			}
			if !exists {
				return t, domain.ErrNotFound
			}
			if _, err := tx.Exec(ctx, "INSERT INTO node_enrollment_token_roots(token_id,source_root_id) VALUES($1,$2)", t.ID, r); err != nil {
				return t, err
			}
		}
		return t, nil
	})
}

// RevokeEnrollmentToken prevents a not yet used token from enrolling. Revoking
// twice succeeds; revoking a used token is ErrConflict (revoke the node instead).
func (s *Store) RevokeEnrollmentToken(ctx context.Context, id string) error {
	if !validIDs(id) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var used, revoked *time.Time
		if err := tx.QueryRow(ctx, "SELECT used_at,revoked_at FROM node_enrollment_tokens WHERE id=$1 FOR UPDATE", id).Scan(&used, &revoked); err != nil {
			return false, err
		}
		if revoked != nil {
			return true, nil
		}
		if used != nil {
			return false, domain.ErrConflict
		}
		_, err := tx.Exec(ctx, "UPDATE node_enrollment_tokens SET revoked_at=clock_timestamp() WHERE id=$1", id)
		return true, err
	})
	return err
}

// Enrollment is the input of EnrollNode.
type Enrollment struct {
	TokenSHA256, SecretSHA256 string
	Name, OS, Arch, Protocol  string
}

// NodeIdentity is an authenticated, unrevoked node.
type NodeIdentity struct {
	ID, PoolID, Name string
}

// EnrollNode consumes an unused, unexpired, unrevoked enrollment token and
// creates the node with the token's pool and source-root grants in one
// transaction. Concurrent use of one token enrolls at most one node; the
// others get ErrUnauthenticated, as do unknown, used, expired and revoked
// tokens. An unsupported protocol is ErrUnsupported (the token stays unused).
func (s *Store) EnrollNode(ctx context.Context, in Enrollment) (NodeIdentity, error) {
	name, ok := cleanLabel(in.Name, 100)
	if !ok || !hexDigest.MatchString(in.TokenSHA256) || !hexDigest.MatchString(in.SecretSHA256) || !osArchPattern.MatchString(in.OS) || !osArchPattern.MatchString(in.Arch) {
		return NodeIdentity{}, domain.ErrInvalid
	}
	if in.Protocol != nodeproto.ProtocolVersion {
		return NodeIdentity{}, domain.ErrUnsupported
	}
	return transaction(ctx, s, func(tx pgx.Tx) (NodeIdentity, error) {
		var tokenID string
		n := NodeIdentity{ID: domain.NewID(), Name: name}
		err := tx.QueryRow(ctx, `UPDATE node_enrollment_tokens SET used_at=clock_timestamp()
			WHERE token_sha256=$1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()
			RETURNING id::text,pool_id::text`, in.TokenSHA256).Scan(&tokenID, &n.PoolID)
		if errors.Is(err, pgx.ErrNoRows) {
			return n, domain.ErrUnauthenticated
		}
		if err != nil {
			return n, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO nodes(id,pool_id,enrollment_token_id,name,secret_sha256,os,arch,protocol_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
			n.ID, n.PoolID, tokenID, name, in.SecretSHA256, in.OS, in.Arch, in.Protocol); err != nil {
			return n, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO node_source_roots(node_id,source_root_id) SELECT $1,source_root_id FROM node_enrollment_token_roots WHERE token_id=$2", n.ID, tokenID)
		return n, err
	})
}

// AuthenticateNode resolves a node secret hash. Unknown or revoked nodes are
// ErrUnauthenticated.
func (s *Store) AuthenticateNode(ctx context.Context, secretSHA256 string) (NodeIdentity, error) {
	if !hexDigest.MatchString(secretSHA256) {
		return NodeIdentity{}, domain.ErrUnauthenticated
	}
	var n NodeIdentity
	err := s.pool.QueryRow(ctx, "SELECT id::text,pool_id::text,name FROM nodes WHERE secret_sha256=$1 AND revoked_at IS NULL", secretSHA256).Scan(&n.ID, &n.PoolID, &n.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, domain.ErrUnauthenticated
	}
	return n, classify(err)
}

// RevokeNode permanently revokes a node. Its secret stops authenticating
// immediately; its leases can no longer be renewed and expire into
// recovery_required. Revoking twice succeeds.
func (s *Store) RevokeNode(ctx context.Context, id string) error {
	if !validIDs(id) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var revoked *time.Time
		if err := tx.QueryRow(ctx, "SELECT revoked_at FROM nodes WHERE id=$1 FOR UPDATE", id).Scan(&revoked); err != nil {
			return false, err
		}
		if revoked != nil {
			return true, nil
		}
		if _, err := tx.Exec(ctx, "UPDATE nodes SET revoked_at=clock_timestamp() WHERE id=$1", id); err != nil {
			return false, err
		}
		_, err := tx.Exec(ctx, "UPDATE workers SET state='stopped',state_since=clock_timestamp() WHERE node_id=$1 AND state NOT IN ('stopped','failed')", id)
		return true, err
	})
	return err
}

// NodeInfo is an operator view of a node. No secret or path is included.
type NodeInfo struct {
	ID, PoolName, Name, OS, Arch, Protocol string
	EnrolledAt                             time.Time
	RevokedAt, LastHeartbeatAt             *time.Time
	Capabilities, SourceRootIDs            []string
	ActiveReservations, Workers            int64
}

// ListNodes returns all nodes, oldest first (trusted operator CLI).
func (s *Store) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT n.id::text,p.name,n.name,n.os,n.arch,n.protocol_version,n.enrolled_at,n.revoked_at,n.last_heartbeat_at,n.capabilities,
		coalesce((SELECT array_agg(source_root_id::text ORDER BY source_root_id) FROM node_source_roots WHERE node_id=n.id),'{}'),
		(SELECT count(*) FROM capacity_reservations WHERE node_id=n.id AND released_at IS NULL),
		(SELECT count(*) FROM workers WHERE node_id=n.id AND state NOT IN ('stopped','failed'))
		FROM nodes n JOIN worker_pools p ON p.id=n.pool_id ORDER BY n.enrolled_at,n.id`)
	if err != nil {
		return nil, classify(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (NodeInfo, error) {
		var n NodeInfo
		err := r.Scan(&n.ID, &n.PoolName, &n.Name, &n.OS, &n.Arch, &n.Protocol, &n.EnrolledAt, &n.RevokedAt, &n.LastHeartbeatAt, &n.Capabilities, &n.SourceRootIDs, &n.ActiveReservations, &n.Workers)
		return n, err
	})
	if out == nil {
		out = []NodeInfo{}
	}
	return out, classify(err)
}

var workerStates = map[string]bool{
	nodeproto.WorkerStarting: true, nodeproto.WorkerIdle: true, nodeproto.WorkerBusy: true,
	nodeproto.WorkerDraining: true, nodeproto.WorkerStopped: true, nodeproto.WorkerFailed: true,
}

// ValidateHeartbeat checks a node heartbeat body. It returns ErrUnsupported for
// a protocol mismatch and ErrInvalid for anything malformed.
func ValidateHeartbeat(in nodeproto.NodeHeartbeatRequest) error {
	if in.ProtocolVersion != nodeproto.ProtocolVersion {
		return domain.ErrUnsupported
	}
	if in.ObservedWorkers == nil || in.Capabilities == nil || len(in.ObservedWorkers) > 256 || len(in.Capabilities) > nodeproto.MaxCapabilities || len(in.CleanupReports) > 256 || in.LastCommandSequence < 0 {
		return domain.ErrInvalid
	}
	switch in.WorkerScaling {
	case "", nodeproto.ScalingStatic:
		if in.MaxWorkers != 0 {
			return domain.ErrInvalid
		}
	case nodeproto.ScalingController:
		if in.MaxWorkers < 1 || in.MaxWorkers > nodeproto.MaxNodeWorkers {
			return domain.ErrInvalid
		}
	default:
		return domain.ErrInvalid
	}
	c := in.Capacity
	if c.CPUCores < 0 || c.CPUCores > 4096 || c.CPUCores != c.CPUCores || c.MemoryAvailableBytes < 0 || c.DiskAvailableBytes < 0 {
		return domain.ErrInvalid
	}
	seen := map[string]bool{}
	for _, w := range in.ObservedWorkers {
		if !validIDs(w.ID) || seen[w.ID] || !workerStates[w.State] || (w.AttemptID != nil && !validIDs(*w.AttemptID)) {
			return domain.ErrInvalid
		}
		seen[w.ID] = true
	}
	caps := map[string]bool{}
	for _, c := range in.Capabilities {
		if !nodeproto.ValidCapability(c) || caps[c] {
			return domain.ErrInvalid
		}
		caps[c] = true
	}
	for _, r := range in.CleanupReports {
		if !validIDs(r.AttemptID, r.WorkerID) || r.LeaseEpoch < 1 || (r.CleanupState != nodeproto.CleanupConfirmed && r.CleanupState != nodeproto.CleanupUncertain) || !reasonPattern.MatchString(r.Method) || r.VerifiedAt.IsZero() {
			return domain.ErrInvalid
		}
	}
	return nil
}

// NodeHeartbeat records the node's reported state and returns the commands
// after the agent's cursor. Under the node row lock it acknowledges commands
// at or below the cursor, registers/updates reported workers (unreported ones
// become stopped) and derives one cancel_attempt command per leased attempt
// of this node whose run has a cancel request. Cleanup reports are not
// processed here (see ConfirmCleanupNode).
func (s *Store) NodeHeartbeat(ctx context.Context, node string, in nodeproto.NodeHeartbeatRequest) (nodeproto.NodeHeartbeatResponse, error) {
	if !validIDs(node) {
		return nodeproto.NodeHeartbeatResponse{}, domain.ErrInvalid
	}
	if err := ValidateHeartbeat(in); err != nil {
		return nodeproto.NodeHeartbeatResponse{}, err
	}
	caps := append([]string{}, in.Capabilities...)
	sort.Strings(caps)
	return transaction(ctx, s, func(tx pgx.Tx) (nodeproto.NodeHeartbeatResponse, error) {
		out := nodeproto.NodeHeartbeatResponse{Commands: []nodeproto.Command{}, CleanupResults: []nodeproto.CleanupResult{}}
		var revoked *time.Time
		var seq int64
		if err := tx.QueryRow(ctx, "SELECT revoked_at,command_seq FROM nodes WHERE id=$1 FOR UPDATE", node).Scan(&revoked, &seq); err != nil {
			return out, err
		}
		if revoked != nil {
			return out, domain.ErrUnauthenticated
		}
		if in.LastCommandSequence > seq {
			return out, domain.ErrInvalid
		}
		scaling := in.WorkerScaling
		if scaling == "" {
			scaling = nodeproto.ScalingStatic
		}
		var maxWorkers *int
		if scaling == nodeproto.ScalingController {
			maxWorkers = &in.MaxWorkers
		}
		if err := tx.QueryRow(ctx, `UPDATE nodes SET last_heartbeat_at=clock_timestamp(),cpu_cores=$2,memory_available_bytes=$3,disk_available_bytes=$4,capabilities=$5,protocol_version=$6,
			worker_scaling=$7,max_workers=$8
			WHERE id=$1 RETURNING last_heartbeat_at`, node, in.Capacity.CPUCores, in.Capacity.MemoryAvailableBytes, in.Capacity.DiskAvailableBytes, caps, in.ProtocolVersion, scaling, maxWorkers).Scan(&out.ServerTime); err != nil {
			return out, err
		}
		ids := make([]string, 0, len(in.ObservedWorkers))
		for _, w := range in.ObservedWorkers {
			var owner string
			err := tx.QueryRow(ctx, `INSERT INTO workers(id,node_id,state,attempt_id) VALUES($1,$2,$3,$4)
				ON CONFLICT(id) DO UPDATE SET state=excluded.state,attempt_id=excluded.attempt_id,last_seen_at=clock_timestamp(),
				state_since=CASE WHEN workers.state=excluded.state THEN workers.state_since ELSE clock_timestamp() END
				WHERE workers.node_id=excluded.node_id RETURNING node_id::text`, w.ID, node, w.State, w.AttemptID).Scan(&owner)
			if errors.Is(err, pgx.ErrNoRows) {
				// The worker ID belongs to another node.
				return out, domain.ErrConflict
			}
			if err != nil {
				return out, err
			}
			ids = append(ids, w.ID)
		}
		if _, err := tx.Exec(ctx, "UPDATE workers SET state='stopped',attempt_id=NULL,state_since=clock_timestamp() WHERE node_id=$1 AND NOT (id::text = ANY($2)) AND state NOT IN ('stopped','failed')", node, ids); err != nil {
			return out, err
		}
		if _, err := tx.Exec(ctx, "UPDATE node_commands SET acked_at=clock_timestamp() WHERE node_id=$1 AND sequence<=$2 AND acked_at IS NULL", node, in.LastCommandSequence); err != nil {
			return out, err
		}
		rows, err := tx.Query(ctx, `SELECT a.id::text,a.worker_id::text FROM attempts a JOIN runs r ON r.id=a.run_id
			WHERE a.node_id=$1 AND a.status='leased' AND r.status IN ('cancel_requested','cancelling')
			AND NOT EXISTS(SELECT 1 FROM node_commands c WHERE c.attempt_id=a.id AND c.kind='cancel_attempt')
			ORDER BY a.id`, node)
		if err != nil {
			return out, err
		}
		type pending struct{ attempt, worker string }
		var cancels []pending
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.attempt, &p.worker); err != nil {
				rows.Close()
				return out, err
			}
			cancels = append(cancels, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return out, err
		}
		for _, p := range cancels {
			seq++
			if _, err := tx.Exec(ctx, "INSERT INTO node_commands(id,node_id,sequence,kind,worker_id,attempt_id) VALUES($1,$2,$3,'cancel_attempt',$4,$5)", domain.NewID(), node, seq, p.worker, p.attempt); err != nil {
				return out, err
			}
		}
		if len(cancels) > 0 {
			if _, err := tx.Exec(ctx, "UPDATE nodes SET command_seq=$2 WHERE id=$1", node, seq); err != nil {
				return out, err
			}
		}
		crows, err := tx.Query(ctx, "SELECT id::text,sequence,kind,worker_id::text,attempt_id::text FROM node_commands WHERE node_id=$1 AND sequence>$2 AND (kind<>'start_worker' OR created_at>clock_timestamp()-$3*interval '1 millisecond') ORDER BY sequence LIMIT 100", node, in.LastCommandSequence, StartTimeout.Milliseconds())
		if err != nil {
			return out, err
		}
		defer crows.Close()
		for crows.Next() {
			var c nodeproto.Command
			if err := crows.Scan(&c.CommandID, &c.Sequence, &c.Kind, &c.WorkerID, &c.AttemptID); err != nil {
				return out, err
			}
			out.Commands = append(out.Commands, c)
		}
		out.ServerTime = out.ServerTime.UTC()
		return out, crows.Err()
	})
}

// CleanupCode maps a ConfirmCleanupNode error to the heartbeat cleanup result
// code ("" when accepted).
func CleanupCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrStaleAttempt):
		return "stale_attempt"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	case errors.Is(err, domain.ErrInvalid):
		return "invalid"
	}
	return "unavailable"
}

// NodesEnrolled reports whether any enrolled, unrevoked node exists (the
// global execution_unavailable fact; run placement is per run).
func (s *Store) NodesEnrolled(ctx context.Context) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM nodes WHERE revoked_at IS NULL)").Scan(&ok)
	return ok, classify(err)
}
