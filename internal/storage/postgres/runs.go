package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

type Run struct {
	ID, ProjectID, ActorID             string
	ProjectRevision                    int64
	SnapshotID                         *string
	Status                             domain.RunStatus
	Version, EventSequence, LeaseEpoch int64
	CreatedAt                          time.Time
}
type Event struct {
	Sequence, EntityVersion int64
	Type                    string
	Payload                 json.RawMessage
	// CreatedAt is the database time the event was recorded.
	CreatedAt time.Time
}

const runColumns = "id::text,project_id::text,actor_id::text,project_revision,snapshot_id::text,status,version,event_sequence,lease_epoch,created_at"

func readRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.ProjectID, &r.ActorID, &r.ProjectRevision, &r.SnapshotID, &r.Status, &r.Version, &r.EventSequence, &r.LeaseEpoch, &r.CreatedAt)
	return r, err
}
func lockRun(ctx context.Context, tx pgx.Tx, id string) (Run, error) {
	return readRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1 FOR UPDATE", id))
}

// Lock order for every writer: project -> run -> attempt -> task rows.

// authorizeRun checks the actor against the run's project, then locks the run FOR UPDATE.
func authorizeRun(ctx context.Context, tx pgx.Tx, actor, id string, run bool) (Run, error) {
	project, err := runProject(ctx, tx, id)
	if err != nil {
		return Run{}, err
	}
	if err = requireProject(ctx, tx, actor, project, false, run); err != nil {
		return Run{}, err
	}
	return lockRun(ctx, tx, id)
}

// authorizeRunRead is authorizeRun for pure reads: the project is locked FOR SHARE
// and the run row is not locked.
func authorizeRunRead(ctx context.Context, tx pgx.Tx, actor, id string) (Run, error) {
	project, err := runProject(ctx, tx, id)
	if err != nil {
		return Run{}, err
	}
	if err = requireProject(ctx, tx, actor, project, false, false); err != nil {
		return Run{}, err
	}
	return readRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1", id))
}

// runProject is safe to read unlocked because runs.project_id is immutable.
func runProject(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var project string
	err := tx.QueryRow(ctx, "SELECT project_id::text FROM runs WHERE id=$1", id).Scan(&project)
	return project, err
}

// transitionRun moves the locked run to next and appends the event. A same-state
// call only appends the event. Moves outside domain.CanTransitionRun are refused,
// so stored status changes follow the domain table.
func transitionRun(ctx context.Context, tx pgx.Tx, r *Run, next domain.RunStatus, kind string, payload map[string]any) error {
	if r.Status != next {
		if !domain.CanTransitionRun(r.Status, next) {
			return domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, "UPDATE runs SET status=$2 WHERE id=$1", r.ID, next); err != nil {
			return err
		}
		r.Status = next
	}
	return appendEvent(ctx, tx, r, kind, payload, true)
}
func appendEvent(ctx context.Context, tx pgx.Tx, r *Run, kind string, payload any, bump bool) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return domain.ErrInvalid
	}
	inc := int64(0)
	if bump {
		inc = 1
	}
	if err = tx.QueryRow(ctx, "UPDATE runs SET event_sequence=event_sequence+1,version=version+$2 WHERE id=$1 RETURNING event_sequence,version", r.ID, inc).Scan(&r.EventSequence, &r.Version); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO events(run_id,sequence,entity_version,type,payload,created_at) VALUES($1,$2,$3,$4,$5,clock_timestamp())", r.ID, r.EventSequence, r.Version, kind, encoded)
	return err
}
func (s *Store) Enqueue(ctx context.Context, actor, project, key string, body json.RawMessage) (Run, error) {
	if !validIDs(actor, project) || strings.TrimSpace(key) == "" || len(key) > 200 {
		return Run{}, domain.ErrInvalid
	}
	canonical, err := domain.CanonicalJSON(body)
	if err != nil {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		return enqueueTx(ctx, tx, actor, project, key, canonical, nil)
	})
}

// RunOrigin is the schedule occurrence a run was requested for (W22). A run
// without one was requested manually.
type RunOrigin struct {
	ScheduleID   string
	ScheduledFor time.Time
}

// enqueueTx is the one enqueue path for manual and scheduled runs: the
// actor's run permission is checked under the project lock, the request is
// stored canonically and the source is fixed only at acquire. The same
// (project, actor, key) with the same body returns the existing run; another
// body is ErrConflict. origin binds a scheduled run to its occurrence row,
// which must already exist in tx.
func enqueueTx(ctx context.Context, tx pgx.Tx, actor, project, key string, canonical json.RawMessage, origin *RunOrigin) (Run, error) {
	digest := domain.Digest(canonical)
	if err := requireProject(ctx, tx, actor, project, false, true); err != nil {
		return Run{}, err
	}
	var existing, stored string
	err := tx.QueryRow(ctx, "SELECT id::text,request_digest FROM runs WHERE project_id=$1 AND actor_id=$2 AND idempotency_key=$3", project, actor, key).Scan(&existing, &stored)
	if err == nil {
		if stored != digest {
			return Run{}, domain.ErrConflict
		}
		return lockRun(ctx, tx, existing)
	}
	if err != pgx.ErrNoRows {
		return Run{}, err
	}
	var scheduleID *string
	var scheduledFor *time.Time
	payload := map[string]any{"status": domain.RunQueued, "trigger": TriggerManual}
	if origin != nil {
		at := origin.ScheduledFor.UTC()
		scheduleID, scheduledFor = &origin.ScheduleID, &at
		payload["trigger"], payload["schedule_id"], payload["scheduled_for"] = TriggerSchedule, origin.ScheduleID, at
	}
	// The canonical request is stored so a claim can build the job from it.
	r, err := readRun(tx.QueryRow(ctx, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status,request,schedule_id,scheduled_for) SELECT $1,id,$2,$3,$4,current_revision,'queued',$6::text::jsonb,$7,$8 FROM projects WHERE id=$5 RETURNING "+runColumns,
		domain.NewID(), actor, key, digest, project, string(canonical), scheduleID, scheduledFor))
	if err != nil {
		return Run{}, err
	}
	err = appendEvent(ctx, tx, &r, "run.queued", payload, false)
	return r, err
}

// Run triggers (RunView.Origin, run.queued payload).
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

func (s *Store) GetRun(ctx context.Context, actor, id string) (Run, error) {
	if !validIDs(actor, id) {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) { return authorizeRunRead(ctx, tx, actor, id) })
}
func (s *Store) Events(ctx context.Context, actor, id string, after int64, limit int) ([]Event, error) {
	if !validIDs(actor, id) || after < 0 || limit < 1 || limit > 1000 {
		return nil, domain.ErrInvalid
	}
	page, err := s.RunEvents(ctx, actor, id, after, limit)
	return page.Events, err
}

// EventPage is a slice of a run's event log and the run's latest sequence
// (Head), read in one transaction after the ACL check.
type EventPage struct {
	Head   int64
	Events []Event
}

// RunEvents returns up to limit events of a run after sequence after, for an
// actor who may read the run (W22 SSE). Every call re-checks the project ACL.
func (s *Store) RunEvents(ctx context.Context, actor, id string, after int64, limit int) (EventPage, error) {
	if !validIDs(actor, id) || after < 0 || limit < 1 || limit > 1000 {
		return EventPage{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (EventPage, error) {
		r, err := authorizeRunRead(ctx, tx, actor, id)
		if err != nil {
			return EventPage{}, err
		}
		rows, err := tx.Query(ctx, "SELECT sequence,entity_version,type,payload,created_at FROM events WHERE run_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3", id, after, limit)
		if err != nil {
			return EventPage{}, err
		}
		defer rows.Close()
		out := EventPage{Head: r.EventSequence, Events: []Event{}}
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.Sequence, &e.EntityVersion, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
				return EventPage{}, err
			}
			e.CreatedAt = e.CreatedAt.UTC()
			out.Events = append(out.Events, e)
		}
		return out, rows.Err()
	})
}
func (s *Store) Cancel(ctx context.Context, actor, id string) (Run, error) {
	r, _, err := s.CancelRun(ctx, actor, id)
	return r, err
}

// CancelRun is Cancel that also reports whether this call changed the run.
// changed is false when the run was already terminal or already cancelling.
func (s *Store) CancelRun(ctx context.Context, actor, id string) (Run, bool, error) {
	if !validIDs(actor, id) {
		return Run{}, false, domain.ErrInvalid
	}
	type result struct {
		run     Run
		changed bool
	}
	out, err := transaction(ctx, s, func(tx pgx.Tx) (result, error) {
		r, err := authorizeRun(ctx, tx, actor, id, true)
		if err != nil {
			return result{run: r}, err
		}
		if r.Status.Terminal() || r.Status == domain.RunCancelRequested || r.Status == domain.RunCancelling {
			return result{run: r}, nil
		}
		if r.Status == domain.RunRecoveryRequired {
			return result{run: r}, domain.ErrConflict
		}
		next := domain.RunCancelRequested
		if r.Status == domain.RunQueued {
			var active bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE run_id=$1 AND status!='released')", id).Scan(&active); err != nil {
				return result{run: r}, err
			}
			if active {
				return result{run: r}, domain.ErrConflict
			}
			next = domain.RunCancelled
		}
		err = transitionRun(ctx, tx, &r, next, "run.cancel_requested", map[string]any{"status": next})
		return result{run: r, changed: true}, err
	})
	return out.run, out.changed, err
}

// Retry requires a retained snapshot and confirmed cleanup; it never reacquires input.
func (s *Store) Retry(ctx context.Context, actor, id string, expected int64) (Run, error) {
	if !validIDs(actor, id) || expected < 1 {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		r, err := authorizeRun(ctx, tx, actor, id, true)
		if err != nil {
			return r, err
		}
		if r.Version != expected || r.SnapshotID == nil || (r.Status != domain.RunFailed && r.Status != domain.RunCancelled) {
			return r, domain.ErrConflict
		}
		var unsafe bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE run_id=$1 AND (status!='released' OR cleanup_state!='confirmed'))", id).Scan(&unsafe); err != nil {
			return r, err
		}
		if unsafe {
			return r, domain.ErrConflict
		}
		// The logical tasks restart: the previous attempt's reasons and times stay
		// in task_executions and events, not on the queued task (stage is kept).
		if _, err = tx.Exec(ctx, "UPDATE tasks SET status='queued',version=version+1,reason=NULL,started_at=NULL,finished_at=NULL WHERE run_id=$1", id); err != nil {
			return r, err
		}
		_, err = tx.Exec(ctx, "UPDATE runs SET status='queued' WHERE id=$1", id)
		if err != nil {
			return r, err
		}
		r.Status = domain.RunQueued
		err = appendEvent(ctx, tx, &r, "run.retried", map[string]any{"snapshot_id": r.SnapshotID}, true)
		return r, err
	})
}
