package postgres

import (
	"context"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

var (
	hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// A Git commit id is SHA-1 (40) or SHA-256 (64); a local copy digest is a SHA-256 manifest.
	gitDigestPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

func validSnapshotDigest(digest, consistency string) bool {
	switch consistency {
	case "git_commit":
		return gitDigestPattern.MatchString(digest)
	case "stable_copy_best_effort":
		return hashPattern.MatchString(digest)
	}
	return false
}

// FixSnapshot persists an already acquired snapshot's metadata, not its bytes.
func (s *Store) FixSnapshot(ctx context.Context, o Ownership, digest, consistency string) (string, error) {
	if !validOwner(o) || !validSnapshotDigest(digest, consistency) {
		return "", domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (string, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return "", err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return "", err
		}
		if r.SnapshotID != nil {
			var old, kind string
			if err = tx.QueryRow(ctx, "SELECT digest,consistency FROM snapshots WHERE id=$1", *r.SnapshotID).Scan(&old, &kind); err != nil {
				return "", err
			}
			if old != digest || kind != consistency {
				return "", domain.ErrConflict
			}
			return *r.SnapshotID, nil
		}
		if r.Status != domain.RunPreparing {
			return "", domain.ErrInvalid
		}
		id := domain.NewID()
		if _, err = tx.Exec(ctx, "INSERT INTO snapshots(id,project_id,digest,consistency) VALUES($1,$2,$3,$4)", id, r.ProjectID, digest, consistency); err != nil {
			return "", err
		}
		if _, err = tx.Exec(ctx, "UPDATE runs SET snapshot_id=$2 WHERE id=$1", r.ID, id); err != nil {
			return "", err
		}
		err = appendEvent(ctx, tx, &r, "snapshot.fixed", map[string]any{"snapshot_id": id}, true)
		return id, err
	})
}
func (s *Store) CreateTask(ctx context.Context, o Ownership, required bool) (string, error) {
	if !validOwner(o) {
		return "", domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (string, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return "", err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return "", err
		}
		id, _, err := createTaskTx(ctx, tx, &r, nil, required)
		return id, err
	})
}

// createTaskTx creates a task of the locked run. With a key, an existing task of
// the run with that key (a retried run re-planning) is returned unchanged with
// its current version and no event.
func createTaskTx(ctx context.Context, tx pgx.Tx, r *Run, key *string, required bool) (string, int64, error) {
	if key != nil {
		var id string
		var version int64
		var req bool
		err := tx.QueryRow(ctx, "SELECT id::text,version,required_for_verdict FROM tasks WHERE run_id=$1 AND key=$2 FOR UPDATE", r.ID, *key).Scan(&id, &version, &req)
		if err == nil {
			if req != required {
				return "", 0, domain.ErrConflict
			}
			return id, version, nil
		}
		if err != pgx.ErrNoRows {
			return "", 0, err
		}
	}
	if r.Status != domain.RunPreparing && r.Status != domain.RunRunning {
		return "", 0, domain.ErrInvalid
	}
	id := domain.NewID()
	if _, err := tx.Exec(ctx, "INSERT INTO tasks(id,run_id,status,required_for_verdict,key) VALUES($1,$2,'queued',$3,$4)", id, r.ID, required, key); err != nil {
		return "", 0, err
	}
	payload := map[string]any{"task_id": id}
	if key != nil {
		payload["key"] = *key
		payload["required"] = required
	}
	return id, 1, appendEvent(ctx, tx, r, "task.created", payload, true)
}

func (s *Store) SetTaskStatus(ctx context.Context, o Ownership, id string, expected int64, next domain.TaskStatus) (int64, error) {
	if !validOwner(o) || !validIDs(id) || expected < 1 || !next.Valid() {
		return 0, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (int64, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return 0, err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return 0, err
		}
		return setTaskStatusTx(ctx, tx, &r, a, id, expected, next, nil)
	})
}

func setTaskStatusTx(ctx context.Context, tx pgx.Tx, r *Run, a attempt, id string, expected int64, next domain.TaskStatus, reason *string) (int64, error) {
	var status domain.TaskStatus
	var version int64
	if err := tx.QueryRow(ctx, "SELECT status,version FROM tasks WHERE run_id=$1 AND id=$2 FOR UPDATE", r.ID, id).Scan(&status, &version); err != nil {
		return 0, err
	}
	if version != expected {
		return 0, domain.ErrConflict
	}
	if !domain.CanTransitionTask(status, next) {
		return 0, domain.ErrInvalid
	}
	if (r.Status == domain.RunCancelRequested || r.Status == domain.RunCancelling) && next == domain.TaskRunning {
		return 0, domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, "INSERT INTO task_executions(run_id,attempt_id,task_id,ordinal,status,version,reason) VALUES($1,$2,$3,1,$4,1,$5) ON CONFLICT(attempt_id,task_id,ordinal) DO UPDATE SET status=excluded.status,version=task_executions.version+1,reason=excluded.reason", r.ID, a.ID, id, next, reason); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, "UPDATE tasks SET status=$3,version=version+1,reason=$4 WHERE run_id=$1 AND id=$2 RETURNING version", r.ID, id, next, reason).Scan(&version); err != nil {
		return 0, err
	}
	payload := map[string]any{"task_id": id, "status": next}
	if reason != nil {
		payload["reason"] = *reason
	}
	return version, appendEvent(ctx, tx, r, "task.status", payload, true)
}

// closeTasks settles the tasks and this attempt's executions left open when a run
// ends as failed or cancelled with cleanup confirmed. Terminal rows and all history
// are untouched: running -> failed/cancelled, queued/blocked -> skipped_policy/cancelled.
// It returns the number of logical tasks changed.
func closeTasks(ctx context.Context, tx pgx.Tx, runID, attemptID string, final domain.RunStatus) (int64, error) {
	running, waiting := domain.TaskFailed, domain.TaskSkippedPolicy
	if final == domain.RunCancelled {
		running, waiting = domain.TaskCancelled, domain.TaskCancelled
	}
	if _, err := tx.Exec(ctx, "UPDATE task_executions SET status=CASE WHEN status='running' THEN $3::text ELSE $4::text END,version=version+1 WHERE run_id=$1 AND attempt_id=$2 AND status IN ('queued','blocked','running')", runID, attemptID, string(running), string(waiting)); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, "UPDATE tasks SET status=CASE WHEN status='running' THEN $2::text ELSE $3::text END,version=version+1 WHERE run_id=$1 AND status IN ('queued','blocked','running')", runID, string(running), string(waiting))
	return tag.RowsAffected(), err
}

type EvidenceInput struct {
	TaskID, Kind           string
	SnapshotID, ArtifactID *string
	Path                   *string
	StartLine, EndLine     *int64
	BlobSHA256             *string
}

// RecordEvidence allocates the evidence ID. The task must have an execution row for
// this attempt in a started or finished state, so evidence cannot be attributed to a
// task that never ran here. That is execution metadata only: file existence, actual
// hash/range, and artifact byte verification belong to collectors/finalizers (W04/W12).
func (s *Store) RecordEvidence(ctx context.Context, o Ownership, in EvidenceInput) (string, error) {
	if !validOwner(o) || !validIDs(in.TaskID) {
		return "", domain.ErrInvalid
	}
	switch in.Kind {
	case "code", "document":
		if in.SnapshotID == nil || !validIDs(*in.SnapshotID) || in.ArtifactID != nil || in.Path == nil || domain.ValidateEvidencePath(*in.Path) != nil || in.StartLine == nil || in.EndLine == nil || *in.StartLine < 1 || *in.EndLine < *in.StartLine || in.BlobSHA256 == nil || !hashPattern.MatchString(*in.BlobSHA256) {
			return "", domain.ErrInvalid
		}
	case "tool_output":
		if in.ArtifactID == nil || !validIDs(*in.ArtifactID) || in.SnapshotID != nil || in.Path != nil || in.StartLine != nil || in.EndLine != nil || in.BlobSHA256 != nil {
			return "", domain.ErrInvalid
		}
	default:
		return "", domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (string, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return "", err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return "", err
		}
		if in.SnapshotID != nil && (r.SnapshotID == nil || *r.SnapshotID != *in.SnapshotID) {
			return "", domain.ErrInvalid
		}
		var ran bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM task_executions WHERE run_id=$1 AND attempt_id=$2 AND task_id=$3 AND status IN ('running','succeeded','failed','timed_out','cancelled'))", r.ID, a.ID, in.TaskID).Scan(&ran); err != nil {
			return "", err
		}
		if !ran {
			return "", domain.ErrInvalid
		}
		if in.ArtifactID != nil {
			var valid bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=$1 AND run_id=$2 AND project_id=$3 AND finalized)", *in.ArtifactID, r.ID, r.ProjectID).Scan(&valid); err != nil {
				return "", err
			}
			if !valid {
				return "", domain.ErrInvalid
			}
		}
		id := domain.NewID()
		_, err = tx.Exec(ctx, "INSERT INTO evidence(id,project_id,run_id,attempt_id,task_id,snapshot_id,artifact_id,path,start_line,end_line,blob_sha256,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)", id, r.ProjectID, r.ID, a.ID, in.TaskID, in.SnapshotID, in.ArtifactID, in.Path, in.StartLine, in.EndLine, in.BlobSHA256, in.Kind)
		if err != nil {
			return "", err
		}
		err = appendEvent(ctx, tx, &r, "evidence.recorded", map[string]any{"evidence_id": id}, false)
		return id, err
	})
}
