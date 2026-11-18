package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// lockNodeAttempt locks the attempt (project -> run -> attempt) and requires
// it to belong to node; another node's attempt is ErrNotFound.
func lockNodeAttempt(ctx context.Context, tx pgx.Tx, node string, o Ownership) (Run, attempt, error) {
	if !validIDs(node) || node != o.NodeID || !validOwner(o) {
		return Run{}, attempt{}, domain.ErrInvalid
	}
	r, a, err := lockAttempt(ctx, tx, o.AttemptID)
	if err != nil {
		return r, a, err
	}
	if a.NodeID != node {
		return r, a, domain.ErrNotFound
	}
	return r, a, nil
}

// RenewAttempt extends the lease of an active attempt by LeaseTTL using the
// database clock, and reports whether the run has a cancel request.
func (s *Store) RenewAttempt(ctx context.Context, node string, o Ownership) (nodeproto.AttemptHeartbeatResponse, error) {
	return transaction(ctx, s, func(tx pgx.Tx) (nodeproto.AttemptHeartbeatResponse, error) {
		var out nodeproto.AttemptHeartbeatResponse
		r, a, err := lockNodeAttempt(ctx, tx, node, o)
		if err != nil {
			return out, err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return out, err
		}
		if err = tx.QueryRow(ctx, "UPDATE attempts SET expires_at=clock_timestamp()+$2*interval '1 second' WHERE id=$1 RETURNING expires_at", a.ID, LeaseTTL.Seconds()).Scan(&out.ExpiresAt); err != nil {
			return out, err
		}
		out.ExpiresAt = out.ExpiresAt.UTC()
		out.CancelRequested = r.Status == domain.RunCancelRequested || r.Status == domain.RunCancelling
		return out, nil
	})
}

// AttemptEvent is one node event. Payload is the raw JSON object of the kind.
type AttemptEvent struct {
	ClientEventID, Kind string
	Payload             json.RawMessage
}

var (
	gitSHAPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// RecordAttemptEvent applies one node event under the attempt's lease. The
// same client_event_id with the same kind and canonical payload replays the
// stored response (for the same worker and epoch, even after the lease ended);
// a different body is ErrConflict.
func (s *Store) RecordAttemptEvent(ctx context.Context, node string, o Ownership, ev AttemptEvent) (nodeproto.EventResponse, error) {
	if !validIDs(ev.ClientEventID) {
		return nodeproto.EventResponse{}, domain.ErrInvalid
	}
	canonical, err := domain.CanonicalJSON(ev.Payload)
	if err != nil {
		return nodeproto.EventResponse{}, domain.ErrInvalid
	}
	digest := domain.Digest([]byte(ev.Kind + "\x00" + string(canonical)))
	return transaction(ctx, s, func(tx pgx.Tx) (nodeproto.EventResponse, error) {
		var out nodeproto.EventResponse
		r, a, err := lockNodeAttempt(ctx, tx, node, o)
		if err != nil {
			return out, err
		}
		if !sameOwner(r, a, o) {
			return out, domain.ErrStaleAttempt
		}
		var storedDigest string
		var stored []byte
		err = tx.QueryRow(ctx, "SELECT request_digest,response FROM attempt_client_events WHERE attempt_id=$1 AND client_event_id=$2", a.ID, ev.ClientEventID).Scan(&storedDigest, &stored)
		if err == nil {
			if storedDigest != digest {
				return out, domain.ErrConflict
			}
			return out, json.Unmarshal(stored, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return out, err
		}
		switch ev.Kind {
		case nodeproto.EventSnapshotFixed:
			var p nodeproto.SnapshotFixedPayload
			if domain.DecodeStrict(ev.Payload, &p) != nil {
				return out, domain.ErrInvalid
			}
			id, err := fixSnapshotNode(ctx, tx, &r, a, node, p)
			if err != nil {
				return out, err
			}
			out.SnapshotID = &id
		case nodeproto.EventTaskCreated:
			var p nodeproto.TaskCreatedPayload
			if domain.DecodeStrict(ev.Payload, &p) != nil || !nodeproto.ValidTaskKey(p.Key) || (p.Stage != nil && !domain.Stage(*p.Stage).Valid()) || (p.Weight != nil && !validWeight(*p.Weight)) {
				return out, domain.ErrInvalid
			}
			var stage *domain.Stage
			if p.Stage != nil {
				st := domain.Stage(*p.Stage)
				stage = &st
			}
			id, version, err := createTaskTx(ctx, tx, &r, &p.Key, p.Required, stage, p.Weight)
			if err != nil {
				return out, err
			}
			out.TaskID, out.Version = &id, &version
		case nodeproto.EventTaskStatus:
			var p nodeproto.TaskStatusPayload
			if domain.DecodeStrict(ev.Payload, &p) != nil || !validIDs(p.TaskID) || p.ExpectedVersion < 1 || !domain.TaskStatus(p.Status).Valid() || (p.Reason != nil && !reasonPattern.MatchString(*p.Reason)) {
				return out, domain.ErrInvalid
			}
			version, err := setTaskStatusTx(ctx, tx, &r, a, p.TaskID, p.ExpectedVersion, domain.TaskStatus(p.Status), p.Reason)
			if err != nil {
				return out, err
			}
			out.Version = &version
		case nodeproto.EventRunAdvance:
			var p nodeproto.RunAdvancePayload
			if domain.DecodeStrict(ev.Payload, &p) != nil || (p.Status != string(domain.RunRunning) && p.Status != string(domain.RunFinalizing)) {
				return out, domain.ErrInvalid
			}
			if err := advanceTx(ctx, tx, &r, p.ExpectedVersion, domain.RunStatus(p.Status)); err != nil {
				return out, err
			}
		default:
			return out, domain.ErrInvalid
		}
		out.Sequence, out.RunVersion = r.EventSequence, r.Version
		encoded, err := json.Marshal(out)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO attempt_client_events(attempt_id,client_event_id,request_digest,response) VALUES($1,$2,$3,$4)", a.ID, ev.ClientEventID, digest, encoded)
		return out, err
	})
}

// fixSnapshotNode fixes (or, for a pinned retry, confirms) the run's snapshot
// from an uploaded source manifest and records that node holds a copy.
func fixSnapshotNode(ctx context.Context, tx pgx.Tx, r *Run, a attempt, node string, p nodeproto.SnapshotFixedPayload) (string, error) {
	if !hashPattern.MatchString(p.ManifestDigest) || !validIDs(p.ManifestArtifactID) {
		return "", domain.ErrInvalid
	}
	switch {
	case p.Kind == "local" && p.Consistency == "stable_copy_best_effort" && p.SourceSHA == nil:
	case p.Kind == "git" && p.Consistency == "git_commit" && p.SourceSHA != nil && gitSHAPattern.MatchString(*p.SourceSHA):
	default:
		return "", domain.ErrInvalid
	}
	var ok bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=$1 AND attempt_id=$2 AND role='source_manifest' AND finalized AND sha256=$3)", p.ManifestArtifactID, a.ID, p.ManifestDigest).Scan(&ok); err != nil {
		return "", err
	}
	if !ok {
		return "", domain.ErrInvalid
	}
	if r.SnapshotID != nil {
		var manifest, sha *string
		var consistency string
		if err := tx.QueryRow(ctx, "SELECT manifest_digest,consistency,source_sha FROM snapshots WHERE id=$1", *r.SnapshotID).Scan(&manifest, &consistency, &sha); err != nil {
			return "", err
		}
		if manifest == nil || *manifest != p.ManifestDigest || consistency != p.Consistency || (sha == nil) != (p.SourceSHA == nil) || (sha != nil && *sha != *p.SourceSHA) {
			return "", domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, "INSERT INTO snapshot_locations(snapshot_id,node_id) VALUES($1,$2) ON CONFLICT DO NOTHING", *r.SnapshotID, node); err != nil {
			return "", err
		}
		return *r.SnapshotID, appendEvent(ctx, tx, r, "snapshot.confirmed", map[string]any{"snapshot_id": *r.SnapshotID, "attempt_id": a.ID}, false)
	}
	if r.Status != domain.RunPreparing {
		return "", domain.ErrInvalid
	}
	id := domain.NewID()
	if _, err := tx.Exec(ctx, "INSERT INTO snapshots(id,project_id,digest,consistency,manifest_digest,source_sha) VALUES($1,$2,$3,$4,$3,$5)", id, r.ProjectID, p.ManifestDigest, p.Consistency, p.SourceSHA); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "UPDATE runs SET snapshot_id=$2 WHERE id=$1", r.ID, id); err != nil {
		return "", err
	}
	r.SnapshotID = &id
	if _, err := tx.Exec(ctx, "INSERT INTO snapshot_locations(snapshot_id,node_id) VALUES($1,$2)", id, node); err != nil {
		return "", err
	}
	return id, appendEvent(ctx, tx, r, "snapshot.fixed", map[string]any{"snapshot_id": id, "manifest_digest": p.ManifestDigest, "consistency": p.Consistency}, true)
}

// ArtifactUpload describes uploaded bytes already stored durably under
// StorageKey in the controller artifact store.
type ArtifactUpload struct {
	ClientArtifactID, Role, MediaType, SHA256, StorageKey string
	SizeBytes                                             int64
}

var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]{0,63}/[a-z0-9][a-z0-9!#$&^_.+-]{0,126}$`)

// CheckArtifactLease verifies, without changing anything, that o currently
// holds the lease (or that client already uploaded this artifact), so bytes are
// not written for a stale owner. FinalizeArtifact re-checks.
func (s *Store) CheckArtifactLease(ctx context.Context, node string, o Ownership, client string) error {
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		r, a, err := lockNodeAttempt(ctx, tx, node, o)
		if err != nil {
			return false, err
		}
		if !sameOwner(r, a, o) {
			return false, domain.ErrStaleAttempt
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM artifacts WHERE attempt_id=$1 AND client_artifact_id=$2)", a.ID, client).Scan(&exists); err != nil || exists {
			return exists, err
		}
		return true, activeLease(ctx, tx, r, a, o)
	})
	return err
}

// FinalizeArtifact records a durable, hash-verified upload as a finalized
// artifact of the attempt. The same client_artifact_id with the same
// hash/size/role/media type replays; anything else for that ID is ErrConflict.
func (s *Store) FinalizeArtifact(ctx context.Context, node string, o Ownership, in ArtifactUpload) (nodeproto.ArtifactResponse, error) {
	if !validIDs(in.ClientArtifactID) || !nodeproto.ValidArtifactRole(in.Role) || !mediaTypePattern.MatchString(in.MediaType) || !hashPattern.MatchString(in.SHA256) ||
		in.SizeBytes < 0 || in.SizeBytes > nodeproto.MaxArtifactBytes || in.StorageKey != "sha256/"+in.SHA256[:2]+"/"+in.SHA256 {
		return nodeproto.ArtifactResponse{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (nodeproto.ArtifactResponse, error) {
		out := nodeproto.ArtifactResponse{SHA256: in.SHA256, SizeBytes: in.SizeBytes, Finalized: true}
		r, a, err := lockNodeAttempt(ctx, tx, node, o)
		if err != nil {
			return out, err
		}
		if !sameOwner(r, a, o) {
			return out, domain.ErrStaleAttempt
		}
		var sha, role, media string
		var size int64
		err = tx.QueryRow(ctx, "SELECT id::text,sha256,size_bytes,role,media_type FROM artifacts WHERE attempt_id=$1 AND client_artifact_id=$2", a.ID, in.ClientArtifactID).Scan(&out.ArtifactID, &sha, &size, &role, &media)
		if err == nil {
			if sha != in.SHA256 || size != in.SizeBytes || role != in.Role || media != in.MediaType {
				return out, domain.ErrConflict
			}
			return out, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return out, err
		}
		out.ArtifactID = domain.NewID()
		_, err = tx.Exec(ctx, `INSERT INTO artifacts(id,project_id,run_id,sha256,size_bytes,finalized,attempt_id,client_artifact_id,role,media_type,storage_key)
			VALUES($1,$2,$3,$4,$5,true,$6,$7,$8,$9,$10)`, out.ArtifactID, r.ProjectID, r.ID, in.SHA256, in.SizeBytes, a.ID, in.ClientArtifactID, in.Role, in.MediaType, in.StorageKey)
		if err != nil {
			return out, err
		}
		return out, appendEvent(ctx, tx, &r, "artifact.finalized", map[string]any{"artifact_id": out.ArtifactID, "attempt_id": a.ID, "role": in.Role, "sha256": in.SHA256, "size_bytes": in.SizeBytes}, false)
	})
}

// ArtifactRecord is a stored artifact's metadata (controller-side readers, W09).
type ArtifactRecord struct {
	ID, RunID, AttemptID, Role, MediaType, SHA256, StorageKey string
	SizeBytes                                                 int64
}

// ArtifactByID returns a finalized attempt artifact. It performs no ACL check:
// callers are controller services, not HTTP users.
func (s *Store) ArtifactByID(ctx context.Context, id string) (ArtifactRecord, error) {
	if !validIDs(id) {
		return ArtifactRecord{}, domain.ErrInvalid
	}
	var a ArtifactRecord
	err := s.pool.QueryRow(ctx, "SELECT id::text,run_id::text,attempt_id::text,role,media_type,sha256,storage_key,size_bytes FROM artifacts WHERE id=$1 AND finalized AND attempt_id IS NOT NULL", id).
		Scan(&a.ID, &a.RunID, &a.AttemptID, &a.Role, &a.MediaType, &a.SHA256, &a.StorageKey, &a.SizeBytes)
	return a, classify(err)
}

// SweepExpiredAttempts moves every leased attempt whose lease expired (by the
// database clock) to recovery_required with uncertain cleanup. The reservation
// is kept until the owning node confirms cleanup. It returns how many moved.
func (s *Store) SweepExpiredAttempts(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text FROM attempts WHERE status='leased' AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 100")
	if err != nil {
		return 0, classify(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, classify(err)
	}
	moved := 0
	for _, id := range ids {
		if _, err := s.ExpireAttempt(ctx, id); err == nil {
			moved++
		} else if !errors.Is(err, domain.ErrConflict) {
			return moved, err
		}
	}
	return moved, nil
}

// SetGlobalMaxRunning changes the scheduler's global concurrent attempt limit
// (trusted operator action; see SetSchedulerLimits).
func (s *Store) SetGlobalMaxRunning(ctx context.Context, n int) error {
	_, err := s.SetSchedulerLimits(ctx, &n, nil)
	return err
}
