package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/schedule"
)

// W22 schedules (docs/09 §8, REQ-023, TC-25). A schedule stores a cron
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
	if _, err = domain.ParseRunRequest(n.Run); err != nil {
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
		_, err = tx.Exec(ctx, "UPDATE schedules SET deleted_at=clock_timestamp(),enabled=false,next_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1", id)
		return true, err
	})
	return err
}

// ScheduleDecision is one occurrence decision of a reconcile pass.
type ScheduleDecision struct {
	ScheduleID   string
	ScheduledFor time.Time
	State        string
	Reason       string
	RunID        string
}

// MaterializeSchedules evaluates every due schedule (next occurrence at or
// before the database clock) and every schedule with a pending occurrence,
// one transaction per schedule. It is safe to run from several controllers:
// a schedule whose project row is locked is left for the next pass, and the
// occurrence key makes a repeated decision impossible.
func (s *Store) MaterializeSchedules(ctx context.Context) ([]ScheduleDecision, error) {
	return s.materializeSchedules(ctx, nil)
}

// materializeSchedules is MaterializeSchedules at a given clock (tests); nil
// uses the database clock.
func (s *Store) materializeSchedules(ctx context.Context, at *time.Time) ([]ScheduleDecision, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id::text,s.project_id::text FROM schedules s
		WHERE s.deleted_at IS NULL AND ((s.next_at IS NOT NULL AND s.next_at<=COALESCE($1::timestamptz,clock_timestamp()))
			OR EXISTS(SELECT 1 FROM schedule_occurrences o WHERE o.schedule_id=s.id AND o.state='pending'))
		ORDER BY s.next_at NULLS LAST,s.id LIMIT $2`, at, materializeBatch)
	if err != nil {
		return nil, classify(err)
	}
	type due struct{ id, project string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.project); err != nil {
			rows.Close()
			return nil, classify(err)
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}
	var out []ScheduleDecision
	var first error
	for _, d := range list {
		ds, err := transaction(ctx, s, func(tx pgx.Tx) ([]ScheduleDecision, error) {
			return materializeOne(ctx, tx, d.id, d.project, at)
		})
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		out = append(out, ds...)
	}
	return out, first
}

// materializer decides the occurrences of one locked schedule.
type materializer struct {
	ctx            context.Context
	tx             pgx.Tx
	id, project    string
	actor          string
	run            json.RawMessage
	now            time.Time
	projectDeleted bool
	out            []ScheduleDecision
}

func materializeOne(ctx context.Context, tx pgx.Tx, id, project string, at *time.Time) ([]ScheduleDecision, error) {
	m := &materializer{ctx: ctx, tx: tx, id: id, project: project}
	// Lock order: project, then schedule (as the API and enqueue). A second
	// controller waits here and then finds the schedule no longer due.
	if err := tx.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1 FOR UPDATE", project).Scan(&m.projectDeleted); err != nil {
		return nil, err
	}
	var n normalized
	var next *time.Time
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT cron,timezone,overlap,misfire,run_request,actor_id::text,next_at FROM schedules WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).
		Scan(&n.Cron, &n.Timezone, &n.Overlap, &n.Misfire, &raw, &m.actor, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if m.run, err = domain.CanonicalJSON(raw); err != nil {
		return nil, err
	}
	if at != nil {
		m.now = at.UTC()
	} else if m.now, err = dbNow(ctx, tx); err != nil {
		return nil, err
	}
	if n.spec, err = schedule.Parse(n.Cron); err != nil {
		return nil, domain.ErrInvalid
	}
	if n.loc, err = schedule.LoadLocation(n.Timezone); err != nil {
		return nil, domain.ErrInvalid
	}
	var pending *time.Time
	if err = tx.QueryRow(ctx, "SELECT scheduled_for FROM schedule_occurrences WHERE schedule_id=$1 AND state='pending'", id).Scan(&pending); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var active int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM runs WHERE schedule_id=$1 AND status IN "+nonTerminalRuns, id).Scan(&active); err != nil {
		return nil, err
	}
	if next != nil && !next.After(m.now) {
		// Only the latest due occurrence is decided; earlier ones in the
		// interval (the controller was stopped) are recorded as missed_from
		// on it and never run (latest_once, no catch-up).
		latest, _ := n.spec.Latest(*next, m.now, n.loc)
		latest = latest.UTC()
		var missedFrom *time.Time
		if latest.After(*next) {
			v := next.UTC()
			missedFrom = &v
		}
		switch late := m.now.Sub(latest) > MisfireGrace; {
		case late && n.Misfire == schedule.MisfireSkip:
			err = m.record(latest, OccurrenceSkipped, ReasonMisfire, missedFrom)
		case active == 0:
			if pending != nil {
				if err = m.settlePending(*pending, OccurrenceCoalesced, ReasonSuperseded); err != nil {
					return nil, err
				}
			}
			err = m.enqueue(latest, missedFrom, false)
		case n.Overlap == schedule.OverlapSkip:
			err = m.record(latest, OccurrenceSkipped, ReasonOverlapActive, missedFrom)
		case n.Overlap == schedule.OverlapQueue && active >= MaxQueuedScheduleRuns:
			err = m.record(latest, OccurrenceSkipped, ReasonQueueFull, missedFrom)
		case n.Overlap == schedule.OverlapQueue:
			err = m.enqueue(latest, missedFrom, false)
		default: // coalesce_latest: keep only the newest occurrence waiting
			if pending != nil {
				if err = m.settlePending(*pending, OccurrenceCoalesced, ReasonSuperseded); err != nil {
					return nil, err
				}
			}
			err = m.record(latest, OccurrencePending, "", missedFrom)
		}
		if err != nil {
			return nil, err
		}
		var nextAt *time.Time
		if t, ok := n.spec.Next(m.now, n.loc); ok {
			t = t.UTC()
			nextAt = &t
		}
		_, err = tx.Exec(ctx, "UPDATE schedules SET next_at=$2 WHERE id=$1", id, nextAt)
		return m.out, err
	}
	if pending != nil && active == 0 {
		err = m.enqueue(pending.UTC(), nil, true)
	}
	return m.out, err
}

// record inserts a decided or pending occurrence.
func (m *materializer) record(at time.Time, state, reason string, missedFrom *time.Time) error {
	var r *string
	if reason != "" {
		r = &reason
	}
	tag, err := m.tx.Exec(m.ctx, `INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state,reason,missed_from,decided_at)
		VALUES($1,$2,$3::text,$4,$5,CASE WHEN $3::text='pending' THEN NULL ELSE clock_timestamp() END) ON CONFLICT DO NOTHING`, m.id, at, state, r, missedFrom)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		m.out = append(m.out, ScheduleDecision{ScheduleID: m.id, ScheduledFor: at, State: state, Reason: reason})
	}
	return nil
}

func (m *materializer) settlePending(at time.Time, state, reason string) error {
	if _, err := m.tx.Exec(m.ctx, "UPDATE schedule_occurrences SET state=$3,reason=$4,decided_at=clock_timestamp() WHERE schedule_id=$1 AND scheduled_for=$2 AND state='pending'", m.id, at, state, reason); err != nil {
		return err
	}
	m.out = append(m.out, ScheduleDecision{ScheduleID: m.id, ScheduledFor: at.UTC(), State: state, Reason: reason})
	return nil
}

// enqueue requests the occurrence's run through the manual enqueue path as
// the schedule's actor. A refused request (deleted project, disabled or
// unauthorized actor) is recorded as skipped with its reason. promote moves
// the existing pending occurrence instead of inserting one.
func (m *materializer) enqueue(at time.Time, missedFrom *time.Time, promote bool) error {
	reason := ""
	if m.projectDeleted {
		reason = ReasonProjectDeleted
	} else {
		var enabled bool
		if err := m.tx.QueryRow(m.ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1", m.actor).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			reason = ReasonActorDisabled
		} else if err := requireProject(m.ctx, m.tx, m.actor, m.project, false, true); err != nil {
			if !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrForbidden) {
				return err
			}
			reason = ReasonActorForbidden
		}
	}
	if reason != "" {
		if promote {
			return m.settlePending(at, OccurrenceSkipped, reason)
		}
		return m.record(at, OccurrenceSkipped, reason, missedFrom)
	}
	if promote {
		tag, err := m.tx.Exec(m.ctx, "UPDATE schedule_occurrences SET state='enqueued',decided_at=clock_timestamp() WHERE schedule_id=$1 AND scheduled_for=$2 AND state='pending'", m.id, at)
		if err != nil || tag.RowsAffected() != 1 {
			return err
		}
	} else {
		tag, err := m.tx.Exec(m.ctx, `INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state,missed_from,decided_at)
			VALUES($1,$2,'enqueued',$3,clock_timestamp()) ON CONFLICT DO NOTHING`, m.id, at, missedFrom)
		if err != nil || tag.RowsAffected() != 1 {
			return err
		}
	}
	// The key contains spaces, which the HTTP Idempotency-Key header rejects,
	// so a manual request can never take a scheduled run's slot.
	key := "schedule " + m.id + " " + strconv.FormatInt(at.Unix(), 10)
	r, err := enqueueTx(m.ctx, m.tx, m.actor, m.project, key, m.run, &RunOrigin{ScheduleID: m.id, ScheduledFor: at})
	if err != nil {
		return err
	}
	m.out = append(m.out, ScheduleDecision{ScheduleID: m.id, ScheduledFor: at, State: OccurrenceEnqueued, RunID: r.ID})
	return nil
}
