package postgres

import (
	"context"
	"encoding/json"
	"strconv"
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
		return enqueueTx(ctx, tx, actor, project, key, canonical, nil, nil)
	})
}

// EnqueuePullRequest is Enqueue for a pull-request run: body is the client
// request (it names the pull request; the idempotency digest is of body)
// and pin the pull request the caller resolved through the provider API
// (domain.PullRequest), stored once in runs.pull_request. The project's
// current source must be the pin's Git repository; anything else is
// ErrInvalid. A replay with the same key and body returns the existing run
// and its original pin.
func (s *Store) EnqueuePullRequest(ctx context.Context, actor, project, key string, body json.RawMessage, pin domain.PullRequest) (Run, error) {
	if !validIDs(actor, project) || strings.TrimSpace(key) == "" || len(key) > 200 || pin.Validate() != nil {
		return Run{}, domain.ErrInvalid
	}
	canonical, err := domain.CanonicalJSON(body)
	if err != nil {
		return Run{}, domain.ErrInvalid
	}
	stored, err := pin.Marshal()
	if err != nil {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		return enqueueTx(ctx, tx, actor, project, key, canonical, nil, stored)
	})
}

// RunOrigin is the non-manual trigger of a run.
type RunOrigin struct {
	ScheduleID   string
	ScheduledFor time.Time
	Webhook      bool
}

// enqueueTx is the one enqueue path for manual and scheduled runs: the
// actor's run permission is checked under the project lock, the request is
// stored canonically and the source is fixed only at acquire. The same
// (project, actor, key) with the same body returns the existing run; another
// body is ErrConflict. origin binds a scheduled run to its occurrence row,
// which must already exist in tx.
func enqueueTx(ctx context.Context, tx pgx.Tx, actor, project, key string, canonical json.RawMessage, origin *RunOrigin, pin json.RawMessage) (Run, error) {
	digest := domain.Digest(canonical)
	if err := requireProject(ctx, tx, actor, project, false, true); err != nil {
		return Run{}, err
	}
	if origin != nil && origin.Webhook {
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1 FOR SHARE", actor).Scan(&enabled); err != nil {
			return Run{}, err
		}
		if !enabled {
			return Run{}, domain.ErrForbidden
		}
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
	// A request that names a pull request needs its pin, and a pin needs a
	// request of the same pull request.
	var named struct {
		PullRequest *domain.PullRequestRef `json:"pull_request"`
	}
	if json.Unmarshal(canonical, &named) != nil || (named.PullRequest != nil) != (pin != nil) {
		return Run{}, domain.ErrInvalid
	}
	var source domain.Source
	var analysis []byte
	var gitHost string
	if _, offline := ctx.Value(offlineGitHostsKey{}).([]string); offline || pin != nil {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT pr.config->'source',pr.config->'analysis'
			FROM projects p JOIN project_revisions pr ON pr.project_id=p.id AND pr.revision=p.current_revision WHERE p.id=$1`, project).Scan(&raw, &analysis); err != nil {
			return Run{}, err
		}
		source, err = domain.ParseSource(raw)
		if err != nil {
			return Run{}, err
		}
		if source.Kind == domain.SourceGit {
			gitHost, _ = domain.RepositoryHost(source.RepositoryURL)
			if !freshGitAllowed(ctx, gitHost) {
				return Run{}, domain.ErrUnsupported
			}
		}
	}
	var pinText *string
	if pin != nil {
		p, err := domain.ParsePullRequest(pin)
		if err != nil || p.Number != named.PullRequest.Number || !strings.EqualFold(gitHost, p.Host) ||
			!strings.EqualFold(domain.RepositoryPath(source.RepositoryURL), p.Repository) {
			return Run{}, domain.ErrInvalid
		}
		if origin != nil && origin.Webhook {
			p.Comment = domain.AnalysisChoicesOf(analysis, source.Kind).CommentOnPullRequest
			pin, err = p.Marshal()
			if err != nil {
				return Run{}, err
			}
		}
		t := string(pin)
		pinText = &t
	}
	var scheduleID *string
	var scheduledFor *time.Time
	payload := map[string]any{"status": domain.RunQueued, "trigger": TriggerManual}
	if origin != nil && origin.Webhook {
		payload["trigger"] = TriggerWebhook
	} else if origin != nil {
		at := origin.ScheduledFor.UTC()
		scheduleID, scheduledFor = &origin.ScheduleID, &at
		payload["trigger"], payload["schedule_id"], payload["scheduled_for"] = TriggerSchedule, origin.ScheduleID, at
	}
	// The canonical request is stored so a claim can build the job from it.
	r, err := readRun(tx.QueryRow(ctx, "INSERT INTO runs(id,project_id,actor_id,idempotency_key,request_digest,project_revision,status,request,schedule_id,scheduled_for,pull_request) SELECT $1,id,$2,$3,$4,current_revision,'queued',$6::text::jsonb,$7,$8,$9::text::jsonb FROM projects WHERE id=$5 RETURNING "+runColumns,
		domain.NewID(), actor, key, digest, project, string(canonical), scheduleID, scheduledFor, pinText))
	if err != nil {
		return Run{}, err
	}
	if err = appendEvent(ctx, tx, &r, "run.queued", payload, false); err != nil {
		return r, err
	}
	var req struct {
		Profile string `json:"profile"`
		Scope   struct {
			Kind string `json:"kind"`
		} `json:"scope"`
	}
	_ = json.Unmarshal(canonical, &req)
	detail := map[string]any{"profile": truncateAudit(req.Profile), "scope_kind": truncateAudit(req.Scope.Kind), "trigger": payload["trigger"]}
	if scheduleID != nil {
		detail["schedule_id"] = origin.ScheduleID
	}
	if pin != nil {
		if p, err := domain.ParsePullRequest(pin); err == nil {
			detail["pull_request"] = p.Number
		}
	}
	return r, writeAudit(ctx, tx, userAudit(ctx, actor, AuditRunRequested).target("run", r.ID).in(project).with(detail))
}

// Run triggers (RunView.Origin, run.queued payload).
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
	TriggerWebhook  = "github_webhook"
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
// actor who may read the run (SSE). Every call re-checks the project ACL.
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
		if err = transitionRun(ctx, tx, &r, next, "run.cancel_requested", map[string]any{"status": next}); err != nil {
			return result{run: r}, err
		}
		e := userAudit(ctx, actor, AuditRunCancelRequested).target("run", r.ID).in(r.ProjectID).with(map[string]any{"status": string(next)})
		return result{run: r, changed: true}, writeAudit(ctx, tx, e)
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
		ok, err := pinnedRetryable(ctx, tx, r, false)
		if err != nil {
			return r, err
		}
		if !ok {
			return r, domain.ErrConflict
		}
		return r, requeuePinned(ctx, tx, &r)
	})
}

// pinnedRetryable reports whether the failed or cancelled run r (locked) can
// run again on its own snapshot: every attempt is released with confirmed
// cleanup and, when located is true, a live node (not revoked, heartbeat
// fresh) still holds the snapshot. A snapshot held only by a node that went
// silent would keep the run waiting for that node, so it is not "held".
func pinnedRetryable(ctx context.Context, tx pgx.Tx, r Run, located bool) (bool, error) {
	if r.SnapshotID == nil || (r.Status != domain.RunFailed && r.Status != domain.RunCancelled) {
		return false, nil
	}
	var unsafe, held bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attempts WHERE run_id=$1 AND (status!='released' OR cleanup_state!='confirmed')),
		EXISTS(SELECT 1 FROM snapshot_locations l JOIN nodes n ON n.id=l.node_id WHERE l.snapshot_id=$2 AND n.revoked_at IS NULL
			AND n.last_heartbeat_at>clock_timestamp()-$3*interval '1 millisecond')`, r.ID, *r.SnapshotID, NodeStaleAfter.Milliseconds()).Scan(&unsafe, &held); err != nil {
		return false, err
	}
	return !unsafe && (held || !located), nil
}

// requeuePinned queues the locked run r again on its fixed snapshot.
func requeuePinned(ctx context.Context, tx pgx.Tx, r *Run) error {
	// The logical tasks restart: the previous attempt's reasons and times stay
	// in task_executions and events, not on the queued task (stage is kept).
	if _, err := tx.Exec(ctx, "UPDATE tasks SET status='queued',version=version+1,reason=NULL,started_at=NULL,finished_at=NULL WHERE run_id=$1", r.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE runs SET status='queued' WHERE id=$1", r.ID); err != nil {
		return err
	}
	r.Status = domain.RunQueued
	return appendEvent(ctx, tx, r, "run.retried", map[string]any{"snapshot_id": r.SnapshotID}, true)
}

// Ways RetryRun ran a run again.
const (
	// RetrySameRun: the failed or cancelled run itself was queued again on
	// its fixed snapshot (Retry).
	RetrySameRun = "same_run"
	// RetryNewRun: a new run of the project with the same request was queued;
	// it acquires the current source.
	RetryNewRun = "new_run"
)

// RetryRun runs a finished run again for an actor who may run the project
// (POST /runs/{id}/retry). A failed or cancelled run whose snapshot a node
// still holds, with every attempt cleaned up, is queued again on that
// snapshot (Retry). Any other failed, cancelled, partial or completed run
// gets a new run with the same request: it acquires the source anew, which
// is what a failed acquisition (an unreachable repository) or a partial
// analysis after installing a tool needs. Repeating the call for the same
// run version returns the same new run. A run that has not finished, or that
// needs recovery, is ErrConflict. Both ways record run.requested.
func (s *Store) RetryRun(ctx context.Context, actor, id string) (Run, string, error) {
	if !validIDs(actor, id) {
		return Run{}, "", domain.ErrInvalid
	}
	type result struct {
		run Run
		how string
	}
	out, err := transaction(ctx, s, func(tx pgx.Tx) (result, error) {
		r, err := authorizeRun(ctx, tx, actor, id, true)
		if err != nil {
			return result{run: r}, err
		}
		switch r.Status {
		case domain.RunFailed, domain.RunCancelled, domain.RunPartial, domain.RunCompleted:
		default:
			return result{run: r}, domain.ErrConflict
		}
		// The same run again keeps its project revision; after the project's
		// configuration changed (its analysis choices), a new run follows it.
		var current int64
		if err := tx.QueryRow(ctx, "SELECT current_revision FROM projects WHERE id=$1", r.ProjectID).Scan(&current); err != nil {
			return result{run: r}, err
		}
		if ok, err := pinnedRetryable(ctx, tx, r, true); err != nil {
			return result{run: r}, err
		} else if ok && current == r.ProjectRevision {
			if err := requeuePinned(ctx, tx, &r); err != nil {
				return result{run: r}, err
			}
			e := userAudit(ctx, actor, AuditRunRequested).target("run", r.ID).in(r.ProjectID).with(map[string]any{"trigger": "retry", "status": string(r.Status)})
			return result{r, RetrySameRun}, writeAudit(ctx, tx, e)
		}
		var request, pin []byte
		if err := tx.QueryRow(ctx, "SELECT request,pull_request FROM runs WHERE id=$1", r.ID).Scan(&request, &pin); err != nil {
			return result{run: r}, err
		}
		if request == nil {
			// A run from before requests were stored cannot be repeated.
			return result{run: r}, domain.ErrConflict
		}
		canonical, err := domain.CanonicalJSON(request)
		if err != nil {
			return result{run: r}, domain.ErrConflict
		}
		// A pull-request run runs again on the same pinned head; a new head
		// is a new request of the pull request.
		next, err := enqueueTx(ctx, tx, actor, r.ProjectID, "retry:"+r.ID+":"+strconv.FormatInt(r.Version, 10), canonical, nil, pin)
		return result{next, RetryNewRun}, err
	})
	return out.run, out.how, err
}
