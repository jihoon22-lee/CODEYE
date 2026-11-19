package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
)

// Stage display (docs/ARCHITECTURE.md#표시-단계와-task-단계). Both the project list/detail and the run
// detail load their facts with loadRunStages (one statement, so one consistent
// read per call) and compute them with progress.Compute.

// RunStages is the stage view of one run plus the facts it was computed from.
type RunStages struct {
	progress.View
	Status      domain.RunStatus
	RequestedAt time.Time
	// LastEventAt is the time of the run's latest accepted event.
	LastEventAt *time.Time
	// CheckedAt is the database time of the read.
	CheckedAt time.Time
}

// lifecycleTypes must equal the events_lifecycle_idx predicate (0006): only
// these few events per run delimit the current attempt and its milestones.
const lifecycleTypes = `('attempt.leased','run.retried','run.status','snapshot.fixed','snapshot.confirmed')`

// runStagesSQL reads, per run: status, snapshot presence, request time, the
// latest event time, the current attempt's milestones (events after the latest
// lease or retry, including the plan-fixing running event's sequence), its
// published report, completion and rejection reasons, and all logical tasks
// with their weight and creation event (plan revision). The current attempt is the one whose epoch is the run's
// lease_epoch; earlier attempts' reports and reasons are history.
const runStagesSQL = `SELECT x.id::text,x.status,x.snapshot_id IS NOT NULL,x.created_at,
	(SELECT e.created_at FROM events e WHERE e.run_id=x.id AND e.sequence=x.event_sequence),
	cur.leased_at,cur.snapshot_at,cur.running_at,cur.running_seq,cur.finalizing_at,
	(SELECT max(rp.created_at) FROM reports rp WHERE rp.run_id=x.id AND rp.lease_epoch=x.lease_epoch),
	(SELECT a.completion_reason FROM attempts a WHERE a.run_id=x.id AND a.epoch=x.lease_epoch),
	(SELECT j.reason FROM report_rejections j JOIN attempts a ON a.id=j.attempt_id WHERE a.run_id=x.id AND a.epoch=x.lease_epoch),
	COALESCE((SELECT json_agg(json_build_object('stage',t.stage,'status',t.status,'reason',t.reason,'started_at',t.started_at,'finished_at',t.finished_at,
			'weight',t.weight,'created_sequence',t.created_sequence,
			'created_at',(SELECT ce.created_at FROM events ce WHERE ce.run_id=t.run_id AND ce.sequence=t.created_sequence)) ORDER BY t.id)
		FROM tasks t WHERE t.run_id=x.id),'[]'::json),
	statement_timestamp()
	FROM runs x
	LEFT JOIN LATERAL (SELECT max(b.sequence) AS seq FROM events b
		WHERE b.run_id=x.id AND b.type IN ` + lifecycleTypes + ` AND b.type IN ('attempt.leased','run.retried')) bound ON true
	LEFT JOIN LATERAL (SELECT
		min(c.created_at) FILTER (WHERE c.type='attempt.leased') AS leased_at,
		min(c.created_at) FILTER (WHERE c.type IN ('snapshot.fixed','snapshot.confirmed')) AS snapshot_at,
		min(c.created_at) FILTER (WHERE c.type='run.status' AND c.payload->>'status'='running') AS running_at,
		min(c.sequence) FILTER (WHERE c.type='run.status' AND c.payload->>'status'='running') AS running_seq,
		min(c.created_at) FILTER (WHERE c.type='run.status' AND c.payload->>'status'='finalizing') AS finalizing_at
		FROM events c WHERE c.run_id=x.id AND c.type IN ` + lifecycleTypes + ` AND c.sequence>=COALESCE(bound.seq,0)) cur ON true
	WHERE x.id=ANY($1::uuid[])`

type stageTaskRow struct {
	Stage      *string    `json:"stage"`
	Status     string     `json:"status"`
	Reason     *string    `json:"reason"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Weight     *int       `json:"weight"`
	CreatedSeq *int64     `json:"created_sequence"`
	CreatedAt  *time.Time `json:"created_at"`
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadRunStages computes the stage view of each run in ids. It performs no
// ACL check: callers pass only runs the actor may read.
func loadRunStages(ctx context.Context, tx pgx.Tx, ids []string) (map[string]RunStages, error) {
	out := make(map[string]RunStages, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, runStagesSQL, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                    string
			in                    progress.Run
			rs                    RunStages
			lastEvent, report     *time.Time
			attemptReason, reject *string
			tasks                 []byte
		)
		if err := rows.Scan(&id, &rs.Status, &in.SnapshotFixed, &rs.RequestedAt, &lastEvent,
			&in.LeasedAt, &in.SnapshotAt, &in.RunningAt, &in.RunningSeq, &in.FinalizingAt, &report, &attemptReason, &reject, &tasks, &rs.CheckedAt); err != nil {
			return nil, err
		}
		var stored []stageTaskRow
		if err := json.Unmarshal(tasks, &stored); err != nil {
			return nil, err
		}
		in.Status = rs.Status
		in.LeasedAt, in.SnapshotAt, in.RunningAt, in.FinalizingAt, in.ReportAt = utc(in.LeasedAt), utc(in.SnapshotAt), utc(in.RunningAt), utc(in.FinalizingAt), utc(report)
		in.AttemptReason, in.ReportRejection = deref(attemptReason), deref(reject)
		for _, t := range stored {
			in.Tasks = append(in.Tasks, progress.Task{Stage: domain.Stage(deref(t.Stage)), Status: domain.TaskStatus(t.Status), Reason: deref(t.Reason),
				StartedAt: utc(t.StartedAt), FinishedAt: utc(t.FinishedAt), Weight: t.Weight, CreatedSeq: t.CreatedSeq, CreatedAt: utc(t.CreatedAt)})
		}
		rs.View = progress.Compute(in)
		rs.RequestedAt, rs.LastEventAt, rs.CheckedAt = rs.RequestedAt.UTC(), utc(lastEvent), rs.CheckedAt.UTC()
		out[id] = rs
	}
	return out, rows.Err()
}

// Activity is a project's progress summary (docs/ARCHITECTURE.md#대표-실행-선택). Run is nil when the
// project has no run; then Stages is nil too.
type Activity struct {
	Run             *ActivityRun
	OtherActiveRuns int64
	Stages          *progress.View
	// WaitingReason is set only when the representative run is queued; it is
	// the same structured reason as the run's progress.waiting_reason.
	WaitingReason *string
	LastReport    *ReportRef
	LastEventAt   *time.Time
	CheckedAt     time.Time
}

// ActivityRun identifies the representative run.
type ActivityRun struct {
	ID          string
	Status      domain.RunStatus
	RequestedAt time.Time
}

// ReportRef pins the latest published report of a project to its run and
// revision.
type ReportRef struct {
	RunID       string
	Revision    int
	PublishedAt time.Time
}

// attachActivity fills the stage view of each summary's representative run
// (chosen by projectSummarySQL) with one statement for the whole page, plus
// the waiting reason of queued representatives.
func attachActivity(ctx context.Context, tx pgx.Tx, items []ProjectSummary) error {
	var ids []string
	for _, p := range items {
		if p.representative != "" {
			ids = append(ids, p.representative)
		}
	}
	stages, err := loadRunStages(ctx, tx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		p := &items[i]
		a := &p.Activity
		rs, ok := stages[p.representative]
		if !ok {
			continue
		}
		a.Run = &ActivityRun{ID: p.representative, Status: rs.Status, RequestedAt: rs.RequestedAt}
		view := rs.View
		// CheckedAt stays the page read time: one value per response.
		a.Stages, a.LastEventAt = &view, rs.LastEventAt
		if rs.Status == domain.RunQueued {
			if a.WaitingReason, err = waitingReason(ctx, tx, p.representative); err != nil {
				return err
			}
		}
	}
	return nil
}
