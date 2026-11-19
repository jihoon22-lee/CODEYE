package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
)

// W22 schedules (TC-25), plan revision and weighted progress (TC-99) and the
// event page (SSE) over real PostgreSQL. Skips (NOT executed) without
// CODEYE_TEST_DATABASE_URL.

const scheduleRun = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func strp(s string) *string { return &s }

func (e *w06) schedule(cron, tz, overlap, misfire string) Schedule {
	e.t.Helper()
	sc, err := e.s.CreateSchedule(e.ctx, e.user, e.project.ID, domain.NewID(), ScheduleInput{Cron: cron, Timezone: tz, Overlap: overlap, Misfire: misfire, Run: json.RawMessage(scheduleRun)})
	if err != nil {
		e.t.Fatal(err)
	}
	return sc
}

// setNext moves a schedule's next occurrence (the materializer's watermark)
// to a fixed instant so a test controls "now".
func (e *w06) setNext(id string, at time.Time) {
	e.t.Helper()
	sqlExec(e.t, e.s, "UPDATE schedules SET next_at=$2 WHERE id=$1", id, at)
}

func (e *w06) materializeAt(at time.Time) []ScheduleDecision {
	e.t.Helper()
	ds, err := e.s.materializeSchedules(e.ctx, &at)
	if err != nil {
		e.t.Fatal(err)
	}
	return ds
}

type occRow struct {
	at, state, reason, missed string
	run                       bool
}

func (e *w06) occurrences(id string) []occRow {
	e.t.Helper()
	rows, err := e.s.pool.Query(e.ctx, `SELECT o.scheduled_for,o.state,COALESCE(o.reason,''),o.missed_from,r.id IS NOT NULL
		FROM schedule_occurrences o LEFT JOIN runs r ON r.schedule_id=o.schedule_id AND r.scheduled_for=o.scheduled_for
		WHERE o.schedule_id=$1 ORDER BY o.scheduled_for`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []occRow
	for rows.Next() {
		var at time.Time
		var missed *time.Time
		var r occRow
		if err := rows.Scan(&at, &r.state, &r.reason, &missed, &r.run); err != nil {
			e.t.Fatal(err)
		}
		r.at = at.UTC().Format(time.RFC3339)
		if missed != nil {
			r.missed = missed.UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out
}

func occText(rows []occRow) string {
	var out []string
	for _, r := range rows {
		s := r.at + " " + r.state
		if r.reason != "" {
			s += "/" + r.reason
		}
		if r.missed != "" {
			s += " missed_from=" + r.missed
		}
		if r.run {
			s += " +run"
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

func (e *w06) wantOccurrences(id string, want ...string) {
	e.t.Helper()
	if got := occText(e.occurrences(id)); got != strings.Join(want, "; ") {
		e.t.Fatalf("occurrences:\n got %s\nwant %s", got, strings.Join(want, "; "))
	}
}

// scheduledRun returns the run of an occurrence.
func (e *w06) scheduledRun(id string, at time.Time) string {
	e.t.Helper()
	var run string
	if err := e.s.pool.QueryRow(e.ctx, "SELECT id::text FROM runs WHERE schedule_id=$1 AND scheduled_for=$2", id, at).Scan(&run); err != nil {
		e.t.Fatal(err)
	}
	return run
}

func (e *w06) endRun(run string) {
	e.t.Helper()
	if _, err := e.s.Cancel(e.ctx, e.user, run); err != nil {
		e.t.Fatal(err)
	}
}

func (e *w06) nextAt(id string) string {
	e.t.Helper()
	var next *time.Time
	if err := e.s.pool.QueryRow(e.ctx, "SELECT next_at FROM schedules WHERE id=$1", id).Scan(&next); err != nil {
		e.t.Fatal(err)
	}
	if next == nil {
		return ""
	}
	return next.UTC().Format(time.RFC3339)
}

// TC-25: the same occurrence evaluated by two controllers at once (separate
// pools, like two processes) is materialized once with one run; the database
// rejects a duplicate occurrence key outright.
func TestTC25DuplicateOccurrenceIsUnique(t *testing.T) {
	e := newW15(t)
	sc := e.schedule("*/5 * * * *", "Asia/Seoul", "", "")
	if sc.Timezone != "Asia/Seoul" || sc.Overlap != "coalesce_latest" || sc.Misfire != "latest_once" || sc.NextAt == nil || !sc.Enabled {
		t.Fatalf("defaults %+v", sc)
	}
	second, err := pgxpool.NewWithConfig(e.ctx, e.s.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other := &Store{pool: second}
	at := ts("2026-01-01T00:00:00Z")
	for round := 0; round < 5; round++ {
		e.setNext(sc.ID, at)
		var wg sync.WaitGroup
		results := make([][]ScheduleDecision, 4)
		errs := make([]error, 4)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				store := e.s
				if i%2 == 1 {
					store = other
				}
				now := at.Add(10 * time.Second)
				results[i], errs[i] = store.materializeSchedules(e.ctx, &now)
			}(i)
		}
		wg.Wait()
		decided := 0
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("round %d controller %d: %v", round, i, errs[i])
			}
			decided += len(results[i])
		}
		if n := e.count("SELECT count(*) FROM schedule_occurrences WHERE schedule_id=$1 AND scheduled_for=$2", sc.ID, at); n != 1 || decided < 1 {
			t.Fatalf("round %d: %d occurrences, %d decisions", round, n, decided)
		}
		if n := e.count("SELECT count(*) FROM runs WHERE schedule_id=$1 AND scheduled_for=$2", sc.ID, at); n > 1 {
			t.Fatalf("round %d: %d runs for one occurrence", round, n)
		}
		at = at.Add(5 * time.Minute)
	}
	_, err = e.s.pool.Exec(e.ctx, "INSERT INTO schedule_occurrences(schedule_id,scheduled_for,state,decided_at) VALUES($1,$2,'enqueued',now())", sc.ID, ts("2026-01-01T00:00:00Z"))
	if !errors.Is(classify(err), domain.ErrConflict) {
		t.Fatalf("duplicate occurrence key accepted: %v", err)
	}
	// One run exists for the first occurrence; later ones coalesced behind it.
	if n := e.count("SELECT count(*) FROM runs WHERE schedule_id=$1", sc.ID); n != 1 {
		t.Fatalf("runs %d", n)
	}
}

// TC-25 coalesce_latest: several occurrences while the schedule's run is
// active leave only the newest one waiting; it runs when the run ends.
func TestTC25CoalesceLatestWhileRunning(t *testing.T) {
	e := newW15(t)
	sc := e.schedule("* * * * *", "UTC", "coalesce_latest", "")
	t0 := ts("2026-02-01T09:00:00Z")
	e.setNext(sc.ID, t0)
	e.materializeAt(t0.Add(5 * time.Second))
	first := e.scheduledRun(sc.ID, t0)
	for i := 1; i <= 3; i++ {
		e.materializeAt(t0.Add(time.Duration(i)*time.Minute + 5*time.Second))
	}
	e.wantOccurrences(sc.ID,
		"2026-02-01T09:00:00Z enqueued +run",
		"2026-02-01T09:01:00Z coalesced/superseded",
		"2026-02-01T09:02:00Z coalesced/superseded",
		"2026-02-01T09:03:00Z pending")
	// Still running: nothing changes between occurrences.
	if ds := e.materializeAt(t0.Add(3*time.Minute + 30*time.Second)); len(ds) != 0 {
		t.Fatalf("decisions while waiting %+v", ds)
	}
	e.endRun(first)
	ds := e.materializeAt(t0.Add(3*time.Minute + 40*time.Second))
	if len(ds) != 1 || ds[0].State != OccurrenceEnqueued || !ds[0].ScheduledFor.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("promotion %+v", ds)
	}
	e.wantOccurrences(sc.ID,
		"2026-02-01T09:00:00Z enqueued +run",
		"2026-02-01T09:01:00Z coalesced/superseded",
		"2026-02-01T09:02:00Z coalesced/superseded",
		"2026-02-01T09:03:00Z enqueued +run")
	// The run went through the normal enqueue path and records its origin.
	v, err := e.s.GetRunView(e.ctx, e.user, e.scheduledRun(sc.ID, t0.Add(3*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != domain.RunQueued || v.Origin == nil || v.Origin.ScheduleID != sc.ID || !v.Origin.ScheduledFor.Equal(t0.Add(3*time.Minute)) || v.ActorID != e.user {
		t.Fatalf("scheduled run %+v origin %+v", v.Run, v.Origin)
	}
	events, err := e.s.Events(e.ctx, e.user, v.ID, 0, 10)
	if err != nil || len(events) != 1 || !strings.Contains(string(events[0].Payload), `"trigger": "schedule"`) {
		t.Fatalf("run.queued %v %v", events, err)
	}
	// A manual run is not the schedule's run and does not block it.
	manual := e.enqueue("manual", deepReview)
	mv, _ := e.s.GetRunView(e.ctx, e.user, manual.ID)
	if mv.Origin != nil {
		t.Fatalf("manual run origin %+v", mv.Origin)
	}
	list, manage, err := e.s.ListSchedules(e.ctx, e.user, e.project.ID)
	if err != nil || !manage || len(list) != 1 || list[0].LastOccurrence == nil || list[0].LastOccurrence.State != OccurrenceEnqueued || list[0].LastOccurrence.RunID == nil || list[0].Pending != nil {
		t.Fatalf("list %+v %v %v", list, manage, err)
	}
}

// TC-25 skip and queue overlap policies.
func TestTC25SkipAndQueueOverlap(t *testing.T) {
	e := newW15(t)
	skip := e.schedule("* * * * *", "UTC", "skip", "")
	queue := e.schedule("* * * * *", "UTC", "queue", "")
	t0 := ts("2026-02-01T09:00:00Z")
	e.setNext(skip.ID, t0)
	e.setNext(queue.ID, t0)
	old := MaxQueuedScheduleRuns
	MaxQueuedScheduleRuns = 2
	defer func() { MaxQueuedScheduleRuns = old }()
	for i := 0; i < 3; i++ {
		e.materializeAt(t0.Add(time.Duration(i)*time.Minute + 5*time.Second))
	}
	e.wantOccurrences(skip.ID,
		"2026-02-01T09:00:00Z enqueued +run",
		"2026-02-01T09:01:00Z skipped/overlap_active",
		"2026-02-01T09:02:00Z skipped/overlap_active")
	e.wantOccurrences(queue.ID,
		"2026-02-01T09:00:00Z enqueued +run",
		"2026-02-01T09:01:00Z enqueued +run",
		"2026-02-01T09:02:00Z skipped/queue_full")
	// After the skip schedule's run ends the next occurrence runs again.
	e.endRun(e.scheduledRun(skip.ID, t0))
	e.materializeAt(t0.Add(3*time.Minute + 5*time.Second))
	if rows := e.occurrences(skip.ID); rows[3].state != OccurrenceEnqueued || !rows[3].run {
		t.Fatalf("after end %s", occText(rows))
	}
}

// TC-25 server stopped across several occurrences: latest_once runs only the
// latest (earlier ones are recorded as missed_from, never run); misfire=skip
// records a late latest one as skipped. Nothing is enumerated per missed
// occurrence.
func TestTC25ControllerStoppedLatestOnce(t *testing.T) {
	e := newW15(t)
	once := e.schedule("*/15 * * * *", "Asia/Seoul", "", "latest_once")
	skip := e.schedule("*/15 * * * *", "Asia/Seoul", "", "skip")
	e.setNext(once.ID, ts("2026-03-01T00:00:00Z"))
	e.setNext(skip.ID, ts("2026-03-01T00:00:00Z"))
	// Stopped from 00:00 to 03:34 (14 occurrences); now is 4 minutes past the latest.
	e.materializeAt(ts("2026-03-01T03:34:00Z"))
	e.wantOccurrences(once.ID, "2026-03-01T03:30:00Z enqueued missed_from=2026-03-01T00:00:00Z +run")
	e.wantOccurrences(skip.ID, "2026-03-01T03:30:00Z skipped/misfire missed_from=2026-03-01T00:00:00Z")
	if e.nextAt(once.ID) != "2026-03-01T03:45:00Z" || e.nextAt(skip.ID) != "2026-03-01T03:45:00Z" {
		t.Fatalf("next %s %s", e.nextAt(once.ID), e.nextAt(skip.ID))
	}
	// A controller stopped for a year on an every-minute schedule: one row.
	minute := e.schedule("* * * * *", "UTC", "", "")
	e.setNext(minute.ID, ts("2025-03-01T00:00:00Z"))
	start := time.Now()
	e.materializeAt(ts("2026-03-01T03:34:30Z"))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("catch-up took %s", took)
	}
	e.wantOccurrences(minute.ID, "2026-03-01T03:34:00Z enqueued missed_from=2025-03-01T00:00:00Z +run")
}

// TC-25 daylight saving time: America/New_York spring-forward and fall-back
// occurrences are the documented UTC instants; Asia/Seoul is stored as given.
func TestTC25DaylightSavingOccurrences(t *testing.T) {
	e := newW15(t)
	gap := e.schedule("30 2 * * *", "America/New_York", "skip", "")
	e.setNext(gap.ID, ts("2026-03-07T07:30:00Z"))
	e.materializeAt(ts("2026-03-07T07:30:05Z"))
	if got := e.nextAt(gap.ID); got != "2026-03-08T07:00:00Z" { // 02:30 does not exist: the transition instant
		t.Fatalf("spring forward next %s", got)
	}
	e.endRun(e.scheduledRun(gap.ID, ts("2026-03-07T07:30:00Z")))
	e.materializeAt(ts("2026-03-08T07:00:05Z"))
	if got := e.nextAt(gap.ID); got != "2026-03-09T06:30:00Z" {
		t.Fatalf("after gap next %s", got)
	}
	repeat := e.schedule("30 1 * * *", "America/New_York", "skip", "")
	e.setNext(repeat.ID, ts("2026-11-01T05:30:00Z"))
	e.materializeAt(ts("2026-11-01T05:30:05Z"))
	if got := e.nextAt(repeat.ID); got != "2026-11-02T06:30:00Z" { // the repeated 01:30 EST does not run again
		t.Fatalf("fall back next %s", got)
	}
	hourly := e.schedule("0 * * * *", "America/New_York", "queue", "")
	e.setNext(hourly.ID, ts("2026-11-01T05:00:00Z"))
	e.materializeAt(ts("2026-11-01T05:00:05Z"))
	if got := e.nextAt(hourly.ID); got != "2026-11-01T06:00:00Z" { // elapsed time: 01:00 EST also runs
		t.Fatalf("hourly fall back next %s", got)
	}
	list, _, err := e.s.ListSchedules(e.ctx, e.user, e.project.ID)
	if err != nil || list[0].Timezone != "America/New_York" {
		t.Fatalf("stored timezone %v %v", list, err)
	}
}

// Fire-time policy: an occurrence is enqueued only if the schedule's actor
// may still request runs, like a manual request.
func TestScheduleOccurrenceRechecksActor(t *testing.T) {
	e := newW15(t)
	manager, err := e.s.CreateUserNamed(e.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetMember(e.ctx, e.user, e.project.ID, manager, domain.RoleManager); err != nil {
		t.Fatal(err)
	}
	sc, err := e.s.CreateSchedule(e.ctx, manager, e.project.ID, "k", ScheduleInput{Cron: "@hourly", Timezone: "UTC", Overlap: "queue", Run: json.RawMessage(scheduleRun)})
	if err != nil {
		t.Fatal(err)
	}
	t0 := ts("2026-04-01T10:00:00Z")
	e.setNext(sc.ID, t0)
	if err := e.s.SetMember(e.ctx, e.user, e.project.ID, manager, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	e.materializeAt(t0.Add(time.Second))
	if err := e.s.SetMember(e.ctx, e.user, e.project.ID, manager, domain.RoleRunner); err != nil {
		t.Fatal(err)
	}
	e.materializeAt(t0.Add(time.Hour + time.Second))
	if err := e.s.DisableUser(e.ctx, manager); err != nil {
		t.Fatal(err)
	}
	e.materializeAt(t0.Add(2*time.Hour + time.Second))
	e.wantOccurrences(sc.ID,
		"2026-04-01T10:00:00Z skipped/actor_forbidden",
		"2026-04-01T11:00:00Z enqueued +run",
		"2026-04-01T12:00:00Z skipped/actor_disabled")
	var actor string
	if err := e.s.pool.QueryRow(e.ctx, "SELECT actor_id::text FROM runs WHERE schedule_id=$1", sc.ID).Scan(&actor); err != nil || actor != manager {
		t.Fatalf("scheduled run actor %s %v", actor, err)
	}
}

// Schedule API rules in the store: manager-only changes, idempotent create,
// version checks, validation, soft delete with pending cancelled.
func TestScheduleManagement(t *testing.T) {
	e := newW15(t)
	in := ScheduleInput{Cron: "0 9 * * 1-5", Timezone: "Asia/Seoul", Run: json.RawMessage(scheduleRun)}
	a, err := e.s.CreateSchedule(e.ctx, e.user, e.project.ID, "same", in)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := e.s.CreateSchedule(e.ctx, e.user, e.project.ID, "same", in); err != nil || again.ID != a.ID {
		t.Fatalf("idempotent create %v %v", again.ID, err)
	}
	other := in
	other.Cron = "0 10 * * 1-5"
	if _, err := e.s.CreateSchedule(e.ctx, e.user, e.project.ID, "same", other); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("key reuse with another body: %v", err)
	}
	// The first occurrence is after creation and is a weekday 09:00 KST.
	if a.NextAt == nil || a.NextAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("next %v", a.NextAt)
	}
	if k := a.NextAt.In(time.FixedZone("KST", 9*3600)); k.Hour() != 9 || k.Minute() != 0 || k.Weekday() == time.Saturday || k.Weekday() == time.Sunday {
		t.Fatalf("next local %v", k)
	}
	for _, bad := range []ScheduleInput{
		{Cron: "0 0 30 2 *", Timezone: "UTC", Run: json.RawMessage(scheduleRun)},
		{Cron: "* * * *", Timezone: "UTC", Run: json.RawMessage(scheduleRun)},
		{Cron: "@daily", Timezone: "Local", Run: json.RawMessage(scheduleRun)},
		{Cron: "@daily", Timezone: "", Run: json.RawMessage(scheduleRun)},
		{Cron: "@daily", Timezone: "Mars/Olympus", Run: json.RawMessage(scheduleRun)},
		{Cron: "@daily", Timezone: "UTC", Overlap: "catch_up", Run: json.RawMessage(scheduleRun)},
		{Cron: "@daily", Timezone: "UTC", Misfire: "all", Run: json.RawMessage(scheduleRun)},
		{Cron: "@daily", Timezone: "UTC", Run: json.RawMessage(`{"profile":"deep-review","source_policy":"pinned","scope":{"kind":"full"}}`)},
	} {
		if _, err := e.s.CreateSchedule(e.ctx, e.user, e.project.ID, domain.NewID(), bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("accepted %+v: %v", bad, err)
		}
	}
	viewer, runner, stranger := e.member(domain.RoleViewer), e.member(domain.RoleRunner), e.newUser()
	for _, u := range []string{viewer, runner} {
		if _, err := e.s.CreateSchedule(e.ctx, u, e.project.ID, "x", in); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("non-manager create: %v", err)
		}
		if _, err := e.s.UpdateSchedule(e.ctx, u, e.project.ID, a.ID, a.Version, SchedulePatch{Enabled: new(bool)}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("non-manager update: %v", err)
		}
		if err := e.s.DeleteSchedule(e.ctx, u, e.project.ID, a.ID, a.Version); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("non-manager delete: %v", err)
		}
	}
	if list, manage, err := e.s.ListSchedules(e.ctx, viewer, e.project.ID); err != nil || manage || len(list) != 1 {
		t.Fatalf("viewer list %v %v %v", len(list), manage, err)
	}
	if _, _, err := e.s.ListSchedules(e.ctx, stranger, e.project.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger list %v", err)
	}
	// Stale version, then a real change; the next occurrence restarts.
	if _, err := e.s.UpdateSchedule(e.ctx, e.user, e.project.ID, a.ID, a.Version+1, SchedulePatch{Cron: strp("@hourly")}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale version %v", err)
	}
	b, err := e.s.UpdateSchedule(e.ctx, e.user, e.project.ID, a.ID, a.Version, SchedulePatch{Cron: strp("@hourly"), Timezone: strp("UTC"), Overlap: strp("skip")})
	if err != nil || b.Version != a.Version+1 || b.Cron != "@hourly" || b.Overlap != "skip" || b.NextAt == nil || b.NextAt.Minute() != 0 {
		t.Fatalf("update %+v %v", b, err)
	}
	if _, err := e.s.UpdateSchedule(e.ctx, e.user, e.project.ID, a.ID, b.Version, SchedulePatch{Cron: strp("bad")}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid update %v", err)
	}
	// Disable cancels a pending occurrence and clears next; re-enable restarts after now.
	c := e.schedule("* * * * *", "UTC", "", "")
	e.setNext(c.ID, ts("2026-05-01T00:00:00Z"))
	e.materializeAt(ts("2026-05-01T00:00:05Z"))
	e.materializeAt(ts("2026-05-01T00:01:05Z"))
	list, _, _ := e.s.ListSchedules(e.ctx, e.user, e.project.ID)
	var cv Schedule
	for _, s := range list {
		if s.ID == c.ID {
			cv = s
		}
	}
	if cv.Pending == nil {
		t.Fatalf("expected pending %+v", cv)
	}
	off, err := e.s.UpdateSchedule(e.ctx, e.user, e.project.ID, c.ID, cv.Version, SchedulePatch{Enabled: new(bool)})
	if err != nil || off.Enabled || off.NextAt != nil || off.Pending != nil || off.LastOccurrence.State != OccurrenceCancelled || *off.LastOccurrence.Reason != ReasonScheduleDisabled {
		t.Fatalf("disable %+v %v", off, err)
	}
	on := true
	again, err := e.s.UpdateSchedule(e.ctx, e.user, e.project.ID, c.ID, off.Version, SchedulePatch{Enabled: &on})
	if err != nil || again.NextAt == nil || again.NextAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("re-enable %+v %v", again, err)
	}
	if err := e.s.DeleteSchedule(e.ctx, e.user, e.project.ID, c.ID, again.Version+5); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale delete %v", err)
	}
	if err := e.s.DeleteSchedule(e.ctx, e.user, e.project.ID, c.ID, again.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteSchedule(e.ctx, e.user, e.project.ID, c.ID, again.Version+1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete %v", err)
	}
	if ds := e.materializeAt(ts("2030-01-01T00:00:00Z")); len(ds) != 1 || ds[0].ScheduleID == c.ID {
		t.Fatalf("deleted schedule materialized %+v", ds)
	}
	// History stays; the occurrence table refuses deletion.
	if _, err := e.s.pool.Exec(e.ctx, "DELETE FROM schedule_occurrences WHERE schedule_id=$1", c.ID); err == nil {
		t.Fatal("occurrence history deleted")
	}
}

func (e *w06) member(role domain.Role) string {
	e.t.Helper()
	id := e.newUser()
	if err := e.s.SetMember(e.ctx, e.user, e.project.ID, id, role); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *w06) newUser() string {
	e.t.Helper()
	id, err := e.s.CreateUserNamed(e.ctx, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func weightp(n int) *int { return &n }

// TC-99 (W22): the plan revision grows when a task is created after the plan
// was fixed, the denominator change is explained, the estimate is weighted,
// the same values reach the project activity, and a retried run re-plans from
// revision 1. Heartbeats change nothing.
func TestTC99PlanRevisionAndWeightedProgress(t *testing.T) {
	e := newW15(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	r := e.enqueue("plan", deepReview)
	o, _ := e.startRun(n, n.workers[0])
	sa, cr := string(domain.StageStaticAnalysis), string(domain.StageCodeReview)
	create := func(key, stage string, w *int) *plannedTask {
		t.Helper()
		resp := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: key, Required: true, Stage: stagePtr(stage), Weight: w})
		return &plannedTask{id: *resp.TaskID, version: *resp.Version}
	}
	vet := create("vet", sa, weightp(1))
	review := create("review", cr, weightp(3))
	if p := e.runStages(r.ID).Plan; p.Revision != nil || p.EstimatedPercent != nil {
		t.Fatalf("plan before running %+v", p)
	}
	e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	e.move(n, o, vet, "running", "")
	e.move(n, o, vet, "succeeded", "")
	p := e.runStages(r.ID).Plan
	if *p.Revision != 1 || *p.PlannedWeight != 4 || *p.EstimatedPercent != 25 || p.WeightBasis != progress.BasisDeclared {
		t.Fatalf("revision 1 %+v", p)
	}
	// Heartbeats are not progress.
	if _, err := e.s.RenewAttempt(e.ctx, n.id, o); err != nil {
		t.Fatal(err)
	}
	e.beat(n, hb(n.workers, "busy", capable(e.root.ID)))
	if q := e.runStages(r.ID).Plan; *q.EstimatedPercent != 25 || *q.Revision != 1 {
		t.Fatalf("heartbeat moved progress %+v", q)
	}
	// A task created after the plan was fixed: revision 2, denominator 4 -> 6.
	extra := create("review.more", cr, weightp(2))
	p = e.runStages(r.ID).Plan
	if *p.Revision != 2 || *p.PlannedWeight != 6 || *p.EstimatedPercent != 16.7 || len(p.Changes) != 1 {
		t.Fatalf("revision 2 %+v", p)
	}
	if c := p.Changes[0]; c.Revision != 2 || c.Reason != progress.PlanChangeTaskAdded || string(c.Stage) != cr || c.Weight != 2 || c.PlannedWeight != 6 || c.At == nil {
		t.Fatalf("change %+v", c)
	}
	// List and detail read the same aggregation.
	if a := e.activity(e.project.ID); a.Stages == nil || a.Stages.Plan.Revision == nil || *a.Stages.Plan.Revision != 2 || *a.Stages.Plan.EstimatedPercent != 16.7 {
		t.Fatalf("activity plan %+v", a.Stages)
	}
	// Weight rules mirror stage: re-declaring with another weight is a
	// conflict, out of range is invalid, the same weight is the same task.
	if _, err := e.event(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "vet", Required: true, Stage: stagePtr(sa), Weight: weightp(5)}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("weight change %v", err)
	}
	for _, bad := range []int{0, 1001, -1} {
		if _, err := e.event(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "w" + strings.Repeat("x", 3), Required: true, Weight: weightp(bad)}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("weight %d: %v", bad, err)
		}
	}
	if again := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "vet", Required: true, Stage: stagePtr(sa), Weight: weightp(1)}); *again.TaskID != vet.id {
		t.Fatal("re-declared task is a new task")
	}
	e.move(n, o, review, "running", "")
	e.move(n, o, review, "failed", "tool_failed")
	e.move(n, o, extra, "running", "")
	e.move(n, o, extra, "succeeded", "")
	if p = e.runStages(r.ID).Plan; *p.EstimatedPercent != 100 || p.FinishedWeight != 6 {
		t.Fatalf("finished %+v", p)
	}
	// Retry: the new attempt re-plans the same keys before running; the
	// earlier attempt's late task is part of revision 1 now.
	reason := "tool_failed"
	if _, err := e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "failed", Cleanup: "confirmed", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if _, err := e.s.Retry(e.ctx, e.user, r.ID, got.Version); err != nil {
		t.Fatal(err)
	}
	if p = e.runStages(r.ID).Plan; p.Revision != nil {
		t.Fatalf("retried run before plan %+v", p)
	}
	e.idle(n)
	c2 := e.claimOK(n, n.workers[1], "second")
	o2 := owner(n, c2)
	for _, d := range []struct {
		key, stage string
		w          int
	}{{"vet", sa, 1}, {"review", cr, 3}, {"review.more", cr, 2}} {
		e.mustEvent(n, o2, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: d.key, Required: true, Stage: stagePtr(d.stage), Weight: weightp(d.w)})
	}
	e.mustEvent(n, o2, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	if p = e.runStages(r.ID).Plan; *p.Revision != 1 || *p.PlannedWeight != 6 || *p.EstimatedPercent != 0 || len(p.Changes) != 0 {
		t.Fatalf("retried plan %+v", p)
	}
}

// The SSE event page: head and contiguous sequences, ACL per call.
func TestRunEventsPage(t *testing.T) {
	e := newW15(t)
	r := e.enqueue("events", deepReview)
	page, err := e.s.RunEvents(e.ctx, e.user, r.ID, 0, 10)
	if err != nil || page.Head != 1 || len(page.Events) != 1 || page.Events[0].Sequence != 1 || page.Events[0].Type != "run.queued" || page.Events[0].CreatedAt.IsZero() {
		t.Fatalf("page %+v %v", page, err)
	}
	if page, err = e.s.RunEvents(e.ctx, e.user, r.ID, 1, 10); err != nil || page.Head != 1 || len(page.Events) != 0 {
		t.Fatalf("empty page %+v %v", page, err)
	}
	if _, err := e.s.RunEvents(e.ctx, e.newUser(), r.ID, 0, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger %v", err)
	}
	if _, err := e.s.RunEvents(e.ctx, e.user, r.ID, -1, 10); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("negative cursor %v", err)
	}
}
