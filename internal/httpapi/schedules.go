package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// W22 schedule API (docs/09 §8, docs/38). Members read; only managers create,
// change and delete. Create takes Idempotency-Key; change and delete take
// If-Match with the schedule version (ETag "N"). The run request has the
// manual run request's contract and is re-authorized at every occurrence.

type occurrenceView struct {
	ScheduledFor time.Time  `json:"scheduled_for"`
	State        string     `json:"state"`
	Reason       *string    `json:"reason"`
	MissedFrom   *time.Time `json:"missed_from"`
	DecidedAt    *time.Time `json:"decided_at"`
	RunID        *string    `json:"run_id"`
}

type pendingView struct {
	ScheduledFor time.Time  `json:"scheduled_for"`
	MissedFrom   *time.Time `json:"missed_from"`
}

type scheduleView struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	Cron           string          `json:"cron"`
	Timezone       string          `json:"timezone"`
	Overlap        string          `json:"overlap"`
	Misfire        string          `json:"misfire"`
	Enabled        bool            `json:"enabled"`
	Run            json.RawMessage `json:"run"`
	Version        int64           `json:"version"`
	NextRunAt      *time.Time      `json:"next_run_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	LastOccurrence *occurrenceView `json:"last_occurrence"`
	Pending        *pendingView    `json:"pending_occurrence"`
}

func scheduleViewOf(s postgres.Schedule) scheduleView {
	v := scheduleView{ID: s.ID, ProjectID: s.ProjectID, Cron: s.Cron, Timezone: s.Timezone, Overlap: s.Overlap, Misfire: s.Misfire, Enabled: s.Enabled,
		Run: s.Run, Version: s.Version, NextRunAt: s.NextAt, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
	if o := s.LastOccurrence; o != nil {
		v.LastOccurrence = &occurrenceView{ScheduledFor: o.ScheduledFor, State: o.State, Reason: o.Reason, MissedFrom: o.MissedFrom, DecidedAt: o.DecidedAt, RunID: o.RunID}
	}
	if p := s.Pending; p != nil {
		v.Pending = &pendingView{ScheduledFor: p.ScheduledFor, MissedFrom: p.MissedFrom}
	}
	return v
}

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// ifMatch reads If-Match as a strong ETag ("N") or a bare version.
func ifMatch(r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" || len(raw) > 18 {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n >= 1 && strconv.FormatInt(n, 10) == raw
}

func (s *Server) scheduleError(q *request, err error) {
	if errors.Is(err, postgres.ErrIdempotencyConflict) {
		writeError(q.w, q.r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with a different body", false)
		return
	}
	s.storeError(q, err)
}

func (s *Server) listSchedules(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	items, manage, err := s.store.ListSchedules(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	out := make([]scheduleView, 0, len(items))
	for _, sc := range items {
		out = append(out, scheduleViewOf(sc))
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": out, "can_manage": manage})
}

func (s *Server) createSchedule(q *request) {
	key := q.r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must be 1..200 visible ASCII characters", false)
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Cron     string          `json:"cron"`
		Timezone string          `json:"timezone"`
		Overlap  *string         `json:"overlap"`
		Misfire  *string         `json:"misfire"`
		Enabled  *bool           `json:"enabled"`
		Run      json.RawMessage `json:"run"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Run == nil || string(in.Run) == "null" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a schedule object with cron, timezone and run", false)
		return
	}
	input := postgres.ScheduleInput{Cron: in.Cron, Timezone: in.Timezone, Enabled: in.Enabled, Run: in.Run}
	// An omitted policy takes the default; an explicit empty one is invalid.
	if in.Overlap != nil {
		input.Overlap = *in.Overlap
		if input.Overlap == "" {
			input.Overlap = "invalid"
		}
	}
	if in.Misfire != nil {
		input.Misfire = *in.Misfire
		if input.Misfire == "" {
			input.Misfire = "invalid"
		}
	}
	ctx, cancel := q.ctx()
	defer cancel()
	sc, err := s.store.CreateSchedule(ctx, q.session.UserID, q.params[0], key, input)
	if err != nil {
		s.scheduleError(q, err)
		return
	}
	q.w.Header().Set("Location", apiPrefix+"/projects/"+sc.ProjectID+"/schedules/"+sc.ID)
	q.w.Header().Set("ETag", etag(sc.Version))
	writeJSON(q.w, http.StatusCreated, scheduleViewOf(sc))
}

func (s *Server) updateSchedule(q *request) {
	expected, ok := ifMatch(q.r)
	if !ok {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "If-Match header with the schedule version is required", false)
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Cron     *string         `json:"cron"`
		Timezone *string         `json:"timezone"`
		Overlap  *string         `json:"overlap"`
		Misfire  *string         `json:"misfire"`
		Enabled  *bool           `json:"enabled"`
		Run      json.RawMessage `json:"run"`
	}
	if domain.DecodeStrict(body, &in) != nil || string(in.Run) == "null" ||
		(in.Cron == nil && in.Timezone == nil && in.Overlap == nil && in.Misfire == nil && in.Enabled == nil && in.Run == nil) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must change at least one schedule field", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	sc, err := s.store.UpdateSchedule(ctx, q.session.UserID, q.params[0], q.params[1], expected,
		postgres.SchedulePatch{Cron: in.Cron, Timezone: in.Timezone, Overlap: in.Overlap, Misfire: in.Misfire, Enabled: in.Enabled, Run: in.Run})
	if err != nil {
		s.scheduleError(q, err)
		return
	}
	q.w.Header().Set("ETag", etag(sc.Version))
	writeJSON(q.w, http.StatusOK, scheduleViewOf(sc))
}

func (s *Server) deleteSchedule(q *request) {
	expected, ok := ifMatch(q.r)
	if !ok {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "If-Match header with the schedule version is required", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.DeleteSchedule(ctx, q.session.UserID, q.params[0], q.params[1], expected); err != nil {
		s.scheduleError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}
