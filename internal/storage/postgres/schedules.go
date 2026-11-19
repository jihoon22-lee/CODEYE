package postgres

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/schedule"
)

// Schedules (docs/EXECUTION.md#예약, REQ-023, TC-25). A schedule stores a cron
// expression with an IANA time zone and a run request; the controller's
// reconcile loop materializes due occurrences as UTC rows keyed by
// (schedule_id, scheduled_for) and enqueues their runs through enqueueTx, the
// manual request path. All state is in the database and every decision is
// taken under the project row lock then the schedule row lock (the API's lock
// order), using the database clock, so several controllers can run the loop.

// Occurrence states (schedule_occurrences.state).
const (
	OccurrencePending   = "pending"
	OccurrenceEnqueued  = "enqueued"
	OccurrenceSkipped   = "skipped"
	OccurrenceCoalesced = "coalesced"
	OccurrenceCancelled = "cancelled"
)

// Occurrence reasons. Reasons are structured codes, never free text.
const (
	ReasonOverlapActive    = "overlap_active"    // skip: an earlier run of the schedule has not ended
	ReasonQueueFull        = "queue_full"        // queue: MaxQueuedScheduleRuns not ended runs exist
	ReasonSuperseded       = "superseded"        // coalesce_latest: a newer occurrence replaced it
	ReasonMisfire          = "misfire"           // misfire=skip: found later than MisfireGrace
	ReasonActorForbidden   = "actor_forbidden"   // the schedule's actor may no longer run the project
	ReasonActorDisabled    = "actor_disabled"    // the schedule's actor is disabled
	ReasonProjectDeleted   = "project_deleted"   // the project was deleted
	ReasonScheduleDisabled = "schedule_disabled" // a pending occurrence when the schedule was disabled
	ReasonScheduleDeleted  = "schedule_deleted"  // a pending occurrence when the schedule was deleted
)

var (
	// MisfireGrace is how late an occurrence may be found and still count as
	// on time. With misfire "skip" a later one is recorded as skipped.
	MisfireGrace = 2 * time.Minute
	// MaxQueuedScheduleRuns bounds the not ended runs one "queue" schedule
	// can accumulate; later occurrences are skipped with queue_full.
	MaxQueuedScheduleRuns = 10
	// MaxSchedulesPerProject bounds the live schedules of a project.
	MaxSchedulesPerProject = 50
	// materializeBatch bounds the schedules one reconcile pass evaluates.
	materializeBatch = 100
)

// Occurrence is one materialized schedule occurrence.
type Occurrence struct {
	ScheduledFor time.Time
	State        string
	Reason       *string
	MissedFrom   *time.Time
	DecidedAt    *time.Time
	RunID        *string
}

// Schedule is a live schedule as shown to project members.
type Schedule struct {
	ID, ProjectID, ActorID string
	Cron, Timezone         string
	Overlap, Misfire       string
	Enabled                bool
	Run                    json.RawMessage // canonical run request
	Version                int64
	NextAt                 *time.Time
	CreatedAt, UpdatedAt   time.Time
	// LastOccurrence is the latest decided occurrence (with its run when it
	// was enqueued); Pending is the occurrence waiting under coalesce_latest.
	LastOccurrence, Pending *Occurrence
}

// ScheduleInput is a schedule creation request. Empty Overlap/Misfire take
// the defaults (coalesce_latest, latest_once); nil Enabled is true.
type ScheduleInput struct {
	Cron, Timezone   string
	Overlap, Misfire string
	Enabled          *bool
	Run              json.RawMessage
}

// SchedulePatch changes selected fields; nil keeps the current value.
type SchedulePatch struct {
	Cron, Timezone, Overlap, Misfire *string
	Enabled                          *bool
	Run                              json.RawMessage
}

// normalized is a validated schedule definition.
type normalized struct {
	Cron     string          `json:"cron"`
	Timezone string          `json:"timezone"`
	Overlap  string          `json:"overlap"`
	Misfire  string          `json:"misfire"`
	Enabled  bool            `json:"enabled"`
	Run      json.RawMessage `json:"run"`
	spec     schedule.Spec
	loc      *time.Location
}

// validate parses the cron expression and zone, checks the policies and the
// run request (the manual request contract), and requires an occurrence
// within the search bound so a never-firing expression is rejected.
func (n *normalized) validate(now time.Time) error {
	var err error
	if n.spec, err = schedule.Parse(n.Cron); err != nil {
		return domain.ErrInvalid
	}
	if n.loc, err = schedule.LoadLocation(n.Timezone); err != nil {
		return domain.ErrInvalid
	}
	if !schedule.ValidOverlap(n.Overlap) || !schedule.ValidMisfire(n.Misfire) {
		return domain.ErrInvalid
	}
	// A schedule repeats a request on the current source; a pull request is
	// pinned per run and is not scheduled.
	if req, err := domain.ParseRunRequest(n.Run); err != nil || req.PullRequest != nil {
		return domain.ErrInvalid
	}
	if n.Run, err = domain.CanonicalJSON(n.Run); err != nil {
		return domain.ErrInvalid
	}
	if _, ok := n.spec.Next(now, n.loc); !ok {
		return domain.ErrInvalid
	}
	return nil
}

func (n *normalized) next(now time.Time) *time.Time {
	if !n.Enabled {
		return nil
	}
	t, ok := n.spec.Next(now, n.loc)
	if !ok {
		return nil
	}
	t = t.UTC()
	return &t
}

func dbNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now.UTC(), err
}

// CreateSchedule creates a schedule of the project for a manager. The same
// (project, actor, key) with the same normalized definition returns the
// existing schedule; another definition is ErrIdempotencyConflict. The first
// occurrence is the first one after creation: nothing runs retroactively.
func (s *Store) CreateSchedule(ctx context.Context, actor, project, key string, in ScheduleInput) (Schedule, error) {
	if !validIDs(actor, project) || strings.TrimSpace(key) == "" || len(key) > 200 {
		return Schedule{}, domain.ErrInvalid
	}
	n := normalized{Cron: strings.TrimSpace(in.Cron), Timezone: in.Timezone, Overlap: in.Overlap, Misfire: in.Misfire, Enabled: true, Run: in.Run}
	if n.Overlap == "" {
		n.Overlap = schedule.OverlapCoalesceLatest
	}
	if n.Misfire == "" {
		n.Misfire = schedule.MisfireLatestOnce
	}
	if in.Enabled != nil {
		n.Enabled = *in.Enabled
	}
	if err := n.validate(time.Now()); err != nil {
		return Schedule{}, err
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return Schedule{}, domain.ErrInvalid
	}
	canonical, err := domain.CanonicalJSON(raw)
	if err != nil {
		return Schedule{}, domain.ErrInvalid
	}
	digest := domain.Digest(canonical)
	return transaction(ctx, s, func(tx pgx.Tx) (Schedule, error) {
		if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
			return Schedule{}, err
		}
		var existing, stored string
		err := tx.QueryRow(ctx, "SELECT id::text,request_digest FROM schedules WHERE project_id=$1 AND created_by=$2 AND idempotency_key=$3", project, actor, key).Scan(&existing, &stored)
		if err == nil {
			if stored != digest {
				return Schedule{}, ErrIdempotencyConflict
			}
			return readSchedule(ctx, tx, project, existing)
		}
		if err != pgx.ErrNoRows {
			return Schedule{}, err
		}
		var live int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM schedules WHERE project_id=$1 AND deleted_at IS NULL", project).Scan(&live); err != nil {
			return Schedule{}, err
		}
		if live >= MaxSchedulesPerProject {
			return Schedule{}, domain.ErrConflict
		}
		now, err := dbNow(ctx, tx)
		if err != nil {
			return Schedule{}, err
		}
		id := domain.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO schedules(id,project_id,created_by,actor_id,idempotency_key,request_digest,cron,timezone,run_request,overlap,misfire,enabled,next_at)
			VALUES($1,$2,$3,$3,$4,$5,$6,$7,$8::text::jsonb,$9,$10,$11,$12)`, id, project, actor, key, digest, n.Cron, n.Timezone, string(n.Run), n.Overlap, n.Misfire, n.Enabled, n.next(now)); err != nil {
			return Schedule{}, err
		}
		e := userAudit(ctx, actor, AuditScheduleCreated).target("schedule", id).in(project).
			with(map[string]any{"cron": truncateAudit(n.Cron), "timezone": truncateAudit(n.Timezone), "enabled": n.Enabled})
		if err := writeAudit(ctx, tx, e); err != nil {
			return Schedule{}, err
		}
		return readSchedule(ctx, tx, project, id)
	})
}

const scheduleColumns = `s.id::text,s.project_id::text,s.actor_id::text,s.cron,s.timezone,s.overlap,s.misfire,s.enabled,s.run_request,s.version,s.next_at,s.created_at,s.updated_at,
	lo.scheduled_for,lo.state,lo.reason,lo.missed_from,lo.decided_at,lr.id::text,po.scheduled_for,po.missed_from`

// scheduleFrom adds each schedule's latest decided occurrence (with its run)
// and its pending occurrence.
const scheduleFrom = ` FROM schedules s
	LEFT JOIN LATERAL (SELECT o.scheduled_for,o.state,o.reason,o.missed_from,o.decided_at FROM schedule_occurrences o
		WHERE o.schedule_id=s.id AND o.state<>'pending' ORDER BY o.scheduled_for DESC LIMIT 1) lo ON true
	LEFT JOIN runs lr ON lr.schedule_id=s.id AND lr.scheduled_for=lo.scheduled_for
	LEFT JOIN schedule_occurrences po ON po.schedule_id=s.id AND po.state='pending' `

func scanSchedule(row pgx.Row) (Schedule, error) {
	var (
		sc                    Schedule
		raw                   []byte
		lastAt, lastMissed    *time.Time
		lastDecided           *time.Time
		lastState, lastReason *string
		lastRun               *string
		pendingAt, pendMissed *time.Time
	)
	if err := row.Scan(&sc.ID, &sc.ProjectID, &sc.ActorID, &sc.Cron, &sc.Timezone, &sc.Overlap, &sc.Misfire, &sc.Enabled, &raw, &sc.Version, &sc.NextAt, &sc.CreatedAt, &sc.UpdatedAt,
		&lastAt, &lastState, &lastReason, &lastMissed, &lastDecided, &lastRun, &pendingAt, &pendMissed); err != nil {
		return sc, err
	}
	canonical, err := domain.CanonicalJSON(raw)
	if err != nil {
		return sc, err
	}
	sc.Run, sc.NextAt, sc.CreatedAt, sc.UpdatedAt = canonical, utc(sc.NextAt), sc.CreatedAt.UTC(), sc.UpdatedAt.UTC()
	if lastAt != nil && lastState != nil {
		sc.LastOccurrence = &Occurrence{ScheduledFor: lastAt.UTC(), State: *lastState, Reason: lastReason, MissedFrom: utc(lastMissed), DecidedAt: utc(lastDecided), RunID: lastRun}
	}
	if pendingAt != nil {
		sc.Pending = &Occurrence{ScheduledFor: pendingAt.UTC(), State: OccurrencePending, MissedFrom: utc(pendMissed)}
	}
	return sc, nil
}

func readSchedule(ctx context.Context, tx pgx.Tx, project, id string) (Schedule, error) {
	return scanSchedule(tx.QueryRow(ctx, "SELECT "+scheduleColumns+scheduleFrom+"WHERE s.id=$1 AND s.project_id=$2 AND s.deleted_at IS NULL", id, project))
}

// ListSchedules returns the live schedules of a project the actor may read,
// oldest first, and whether the actor may manage them (manager role).
func (s *Store) ListSchedules(ctx context.Context, actor, project string) ([]Schedule, bool, error) {
	if !validIDs(actor, project) {
		return nil, false, domain.ErrInvalid
	}
	type result struct {
		items  []Schedule
		manage bool
	}
	out, err := transaction(ctx, s, func(tx pgx.Tx) (result, error) {
		if err := requireProject(ctx, tx, actor, project, false, false); err != nil {
			return result{}, err
		}
		var manage bool
		if err := tx.QueryRow(ctx, `SELECT p.owner_id=$2 OR EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=$2 AND m.role='manager')
			FROM projects p WHERE p.id=$1`, project, actor).Scan(&manage); err != nil {
			return result{}, err
		}
		rows, err := tx.Query(ctx, "SELECT "+scheduleColumns+scheduleFrom+"WHERE s.project_id=$1 AND s.deleted_at IS NULL ORDER BY s.created_at,s.id", project)
		if err != nil {
			return result{}, err
		}
		defer rows.Close()
		r := result{items: []Schedule{}, manage: manage}
		for rows.Next() {
			sc, err := scanSchedule(rows)
			if err != nil {
				return result{}, err
			}
			r.items = append(r.items, sc)
		}
		return r, rows.Err()
	})
	return out.items, out.manage, err
}

// lockSchedule locks a live schedule of the project (after the project lock).
func lockSchedule(ctx context.Context, tx pgx.Tx, project, id string) (normalized, int64, error) {
	var n normalized
	var raw []byte
	var version int64
	err := tx.QueryRow(ctx, "SELECT cron,timezone,overlap,misfire,enabled,run_request,version FROM schedules WHERE id=$1 AND project_id=$2 AND deleted_at IS NULL FOR UPDATE", id, project).
		Scan(&n.Cron, &n.Timezone, &n.Overlap, &n.Misfire, &n.Enabled, &raw, &version)
	n.Run = raw
	return n, version, err
}

// cancelPending settles a schedule's pending occurrence as cancelled.
func cancelPending(ctx context.Context, tx pgx.Tx, id, reason string) error {
	_, err := tx.Exec(ctx, "UPDATE schedule_occurrences SET state='cancelled',reason=$2,decided_at=clock_timestamp() WHERE schedule_id=$1 AND state='pending'", id, reason)
	return err
}

// UpdateSchedule changes a schedule for a manager when expected equals its
// version (else ErrConflict). The updater becomes the schedule's actor. A
// changed expression, zone or re-enable restarts from the next occurrence
// after now (no catch-up of the disabled time); disabling cancels a pending
// occurrence.
func (s *Store) UpdateSchedule(ctx context.Context, actor, project, id string, expected int64, p SchedulePatch) (Schedule, error) {
	if !validIDs(actor, project, id) || expected < 1 {
		return Schedule{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Schedule, error) {
		if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
			return Schedule{}, err
		}
		n, version, err := lockSchedule(ctx, tx, project, id)
		if err != nil {
			return Schedule{}, err
		}
		if version != expected {
			return Schedule{}, domain.ErrConflict
		}
		before := n
		set := func(dst *string, v *string) {
			if v != nil {
				*dst = *v
			}
		}
		set(&n.Cron, p.Cron)
		n.Cron = strings.TrimSpace(n.Cron)
		set(&n.Timezone, p.Timezone)
		set(&n.Overlap, p.Overlap)
		set(&n.Misfire, p.Misfire)
		if p.Enabled != nil {
			n.Enabled = *p.Enabled
		}
		if p.Run != nil {
			n.Run = p.Run
		}
		now, err := dbNow(ctx, tx)
		if err != nil {
			return Schedule{}, err
		}
		if err := n.validate(now); err != nil {
			return Schedule{}, err
		}
		restart := n.Cron != before.Cron || n.Timezone != before.Timezone || (n.Enabled && !before.Enabled)
		if !n.Enabled && before.Enabled {
			if err := cancelPending(ctx, tx, id, ReasonScheduleDisabled); err != nil {
				return Schedule{}, err
			}
		}
		next := "next_at"
		args := []any{id, n.Cron, n.Timezone, n.Overlap, n.Misfire, n.Enabled, string(n.Run), actor}
		if restart || !n.Enabled {
			next = "$9"
			args = append(args, n.next(now))
		}
		if _, err := tx.Exec(ctx, `UPDATE schedules SET cron=$2,timezone=$3,overlap=$4,misfire=$5,enabled=$6,run_request=$7::text::jsonb,actor_id=$8,next_at=`+next+`,
			version=version+1,updated_at=clock_timestamp() WHERE id=$1`, args...); err != nil {
			return Schedule{}, err
		}
		var fields []string
		for name, set := range map[string]bool{"cron": p.Cron != nil, "timezone": p.Timezone != nil, "overlap": p.Overlap != nil, "misfire": p.Misfire != nil, "enabled": p.Enabled != nil, "run": p.Run != nil} {
			if set {
				fields = append(fields, name)
			}
		}
		slices.Sort(fields)
		e := userAudit(ctx, actor, AuditScheduleUpdated).target("schedule", id).in(project).
			with(map[string]any{"fields": fields, "enabled": n.Enabled, "cron": truncateAudit(n.Cron), "timezone": truncateAudit(n.Timezone)})
		if err := writeAudit(ctx, tx, e); err != nil {
			return Schedule{}, err
		}
		return readSchedule(ctx, tx, project, id)
	})
}

// DeleteSchedule soft-deletes a schedule for a manager when expected equals
// its version. Occurrences and runs stay as history; a pending occurrence is
// cancelled.
func (s *Store) DeleteSchedule(ctx context.Context, actor, project, id string, expected int64) error {
	if !validIDs(actor, project, id) || expected < 1 {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
			return false, err
		}
		_, version, err := lockSchedule(ctx, tx, project, id)
		if err != nil {
			return false, err
		}
		if version != expected {
			return false, domain.ErrConflict
		}
		if err := cancelPending(ctx, tx, id, ReasonScheduleDeleted); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, "UPDATE schedules SET deleted_at=clock_timestamp(),enabled=false,next_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1", id); err != nil {
			return false, err
		}
		return true, writeAudit(ctx, tx, userAudit(ctx, actor, AuditScheduleDeleted).target("schedule", id).in(project))
	})
	return err
}
