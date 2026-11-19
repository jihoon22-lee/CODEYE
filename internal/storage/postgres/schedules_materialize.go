package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/schedule"
)

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
// each transaction locks the project and then the schedule row, so a second
// controller waits and then finds the schedule no longer due, and the
// occurrence key (schedule_id, scheduled_for) makes a repeated decision
// impossible.
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
