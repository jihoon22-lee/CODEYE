package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// GET /runs/{id}/events (W22, docs/API.md#실행-조회와-진행): the run's stored events after a
// cursor as text/event-stream. Each event row is an unnamed SSE message with
// "id: <sequence>" (the run event sequence written in the transaction that
// changed the state) and a data object of allowlisted structured fields.
// Control messages carry no id so they never move the cursor:
//
//	event: run.view      the GET /runs/{id} document after a batch of rows
//	event: heartbeat     keep-alive; it never changes progress
//	event: state.resync  the stream cannot continue from this cursor (gap,
//	                     cursor ahead of the run, cursor older than the replay
//	                     window, view unavailable); refetch GET /runs/{id}
//	                     and reconnect from its event_sequence
//	event: closed        the session ended or the run is no longer readable
//
// The handler holds no database connection between polls: each poll is a
// short transaction that re-checks the session and the project ACL. Logout,
// session expiry and permission loss therefore end the stream within one
// poll interval.

// StreamConfig tunes the event stream. Zero values take the defaults.
type StreamConfig struct {
	// Poll is how often the stream reads new events (default 1s).
	Poll time.Duration
	// Heartbeat is the keep-alive interval (default 15s).
	Heartbeat time.Duration
	// ReplayWindow is how many events behind the run's head a cursor may be
	// (default 1000); an older cursor gets state.resync cursor_expired.
	ReplayWindow int64
	// MaxAge ends a stream after this long (default 30m); the browser
	// reconnects with Last-Event-ID and is authenticated again.
	MaxAge time.Duration
	// MaxStreams bounds concurrently open streams (default 32); more get 503.
	MaxStreams int32
	// MaxStreamsPerUser bounds the open streams of one session user
	// (default 4), checked before a global slot is taken, so one user cannot
	// exhaust MaxStreams for everyone; more get 503 for that user only.
	MaxStreamsPerUser int32
}

func (c StreamConfig) withDefaults() StreamConfig {
	if c.Poll <= 0 {
		c.Poll = time.Second
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = 15 * time.Second
	}
	if c.ReplayWindow <= 0 {
		c.ReplayWindow = 1000
	}
	if c.MaxAge <= 0 {
		c.MaxAge = 30 * time.Minute
	}
	if c.MaxStreams <= 0 {
		c.MaxStreams = 32
	}
	if c.MaxStreamsPerUser <= 0 {
		c.MaxStreamsPerUser = 4
	}
	return c
}

// acquireStream takes a per-user slot and then a global slot; it takes
// neither when either limit is reached. The caller must releaseStream.
func (s *Server) acquireStream(user string) bool {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.userStreams[user] >= s.stream.MaxStreamsPerUser || s.streams >= s.stream.MaxStreams {
		return false
	}
	s.userStreams[user]++
	s.streams++
	return true
}

func (s *Server) releaseStream(user string) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	s.streams--
	if s.userStreams[user]--; s.userStreams[user] <= 0 {
		delete(s.userStreams, user)
	}
}

// Stream control reasons (state.resync and closed data.reason).
const (
	ResyncGap             = "gap"
	ResyncCursorAhead     = "cursor_ahead"
	ResyncCursorExpired   = "cursor_expired"
	ResyncViewUnavailable = "view_unavailable"
	ClosedUnauthenticated = "unauthenticated"
	ClosedAccessRevoked   = "access_revoked"
	ClosedUnavailable     = "unavailable"
)

const (
	streamBatch        = 200
	streamCallTimeout  = 5 * time.Second
	streamWriteTimeout = 10 * time.Second
	// streamUnavailable ends a stream whose store calls keep failing.
	streamUnavailable = 30 * time.Second
	maxCursor         = int64(1) << 53
)

// eventFields are the payload members an event row may carry to a browser:
// identifiers, enums, counts and times. Node and worker identities, digests,
// manifests and anything unknown are dropped.
var eventFields = map[string]bool{
	"status": true, "task_id": true, "key": true, "required": true, "stage": true, "weight": true, "reason": true,
	"snapshot_id": true, "attempt_id": true, "epoch": true, "report_id": true, "revision": true, "quality_verdict": true,
	"closed_tasks": true, "trigger": true, "schedule_id": true, "scheduled_for": true,
}

// eventData builds the data object of one event row.
func eventData(run string, e postgres.Event) map[string]any {
	out := map[string]any{"run_id": run, "sequence": e.Sequence, "version": e.EntityVersion, "type": e.Type, "at": e.CreatedAt}
	var payload map[string]any
	if json.Unmarshal(e.Payload, &payload) != nil {
		return out
	}
	for k, v := range payload {
		if !eventFields[k] {
			continue
		}
		switch x := v.(type) {
		case string:
			if len(x) <= 100 {
				out[k] = x
			}
		case float64, bool, nil:
			out[k] = x
		}
	}
	return out
}

// accessLost reports a read the actor may no longer make (membership removed,
// project deleted).
func accessLost(err error) bool {
	return errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden)
}

// streamCursor reads Last-Event-ID (sent by a reconnecting browser, so it wins
// over the URL) or ?cursor=: a canonical non-negative integer.
func streamCursor(r *http.Request) (int64, bool) {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("cursor")
	}
	if raw == "" || len(raw) > 16 {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || n > maxCursor || strconv.FormatInt(n, 10) != raw {
		return 0, false
	}
	return n, true
}

type eventStream struct {
	w   http.ResponseWriter
	rc  *http.ResponseController
	err error
}

func (st *eventStream) write(format string, args ...any) {
	if st.err != nil {
		return
	}
	_ = st.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
	_, st.err = fmt.Fprintf(st.w, format, args...)
}

func (st *eventStream) send(event string, id *int64, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		st.err = err
		return
	}
	if id != nil {
		st.write("id: %d\n", *id)
	}
	if event != "" {
		st.write("event: %s\n", event)
	}
	st.write("data: %s\n\n", raw)
}

func (st *eventStream) flush() {
	if st.err == nil {
		_ = st.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		st.err = st.rc.Flush()
	}
}

// testHookBeforeView, when set by a test, runs between sending a batch of
// rows and reading the run.view for it (to append an event in between).
var testHookBeforeView atomic.Pointer[func()]

func (s *Server) streamRunEvents(q *request) {
	cursor, ok := streamCursor(q.r)
	if !ok {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "cursor query parameter or Last-Event-ID header must be a non-negative integer event sequence", false)
		return
	}
	run, actor := q.params[0], q.session.UserID
	if !s.acquireStream(actor) {
		writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "too many open event streams", true)
		return
	}
	defer s.releaseStream(actor)
	ctx, cancel := context.WithCancel(q.r.Context())
	defer cancel()
	go func() {
		select {
		case <-s.stopStreams:
			cancel()
		case <-ctx.Done():
		}
	}()
	call := func(fn func(context.Context) error) error {
		c, done := context.WithTimeout(ctx, streamCallTimeout)
		defer done()
		return fn(c)
	}
	var page postgres.EventPage
	read := func() error {
		return call(func(c context.Context) (err error) {
			page, err = s.store.RunEvents(c, actor, run, cursor, streamBatch)
			return err
		})
	}
	// The first read decides authorization: a run the actor may not read is
	// an ordinary 404 before any stream header is sent.
	if err := read(); err != nil {
		s.storeError(q, err)
		return
	}
	st := &eventStream{w: q.w, rc: http.NewResponseController(q.w)}
	// The server's ReadTimeout would otherwise cancel the request context.
	_ = st.rc.SetReadDeadline(time.Time{})
	h := q.w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("X-Accel-Buffering", "no")
	q.w.WriteHeader(http.StatusOK)
	st.write("retry: 3000\n\n")
	st.flush()

	control := func(event, reason string, extra map[string]any) {
		data := map[string]any{"run_id": run, "reason": reason}
		for k, v := range extra {
			data[k] = v
		}
		st.send(event, nil, data)
		st.flush()
	}
	ended := time.Now().Add(s.stream.MaxAge)
	lastWrite := time.Now()
	var failingSince time.Time
	ticker := time.NewTicker(s.stream.Poll)
	defer ticker.Stop()
	for {
		if cursor > page.Head {
			control("state.resync", ResyncCursorAhead, map[string]any{"head": page.Head})
			return
		}
		if page.Head-cursor > s.stream.ReplayWindow {
			control("state.resync", ResyncCursorExpired, map[string]any{"head": page.Head})
			return
		}
		sent := false
		for len(page.Events) > 0 {
			for _, e := range page.Events {
				if e.Sequence != cursor+1 {
					control("state.resync", ResyncGap, map[string]any{"head": page.Head, "expected": cursor + 1})
					return
				}
				seq := e.Sequence
				st.send("", &seq, eventData(run, e))
				cursor, sent = seq, true
			}
			if cursor >= page.Head || st.err != nil {
				break
			}
			if err := read(); err != nil {
				if accessLost(err) {
					control("closed", ClosedAccessRevoked, nil)
					return
				}
				break
			}
		}
		if sent {
			if hook := testHookBeforeView.Load(); hook != nil {
				(*hook)()
			}
			var view postgres.RunView
			err := call(func(c context.Context) (err error) {
				view, err = s.store.GetRunView(c, actor, run)
				return err
			})
			if accessLost(err) {
				control("closed", ClosedAccessRevoked, nil)
				return
			}
			if err != nil {
				control("state.resync", ResyncViewUnavailable, nil)
				return
			}
			// The view is read after the batch, so events committed in
			// between are already in it. Send those rows first: a run.view
			// never describes an event_sequence beyond the last row sent.
			// Rows newer than the view wait for the next batch.
			for cursor < view.EventSequence && st.err == nil {
				if err := read(); err != nil {
					if accessLost(err) {
						control("closed", ClosedAccessRevoked, nil)
					} else {
						control("state.resync", ResyncViewUnavailable, nil)
					}
					return
				}
				progressed := false
				for _, e := range page.Events {
					if e.Sequence > view.EventSequence {
						break
					}
					if e.Sequence != cursor+1 {
						control("state.resync", ResyncGap, map[string]any{"head": page.Head, "expected": cursor + 1})
						return
					}
					seq := e.Sequence
					st.send("", &seq, eventData(run, e))
					cursor, progressed = seq, true
				}
				if !progressed {
					control("state.resync", ResyncGap, map[string]any{"head": page.Head, "expected": cursor + 1})
					return
				}
			}
			if st.err != nil {
				return
			}
			st.send("run.view", nil, runViewOf(view))
			st.flush()
			lastWrite = time.Now()
		}
		if st.err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		if now.After(ended) {
			return // the browser reconnects with Last-Event-ID
		}
		if now.Sub(lastWrite) >= s.stream.Heartbeat {
			st.send("heartbeat", nil, map[string]any{"run_id": run})
			st.flush()
			lastWrite = now
			if st.err != nil {
				return
			}
		}
		// Re-check the session (logout, expiry, disabled user) and then the
		// project ACL with the next read.
		err := call(func(c context.Context) error {
			_, err := s.store.SessionByTokenHash(c, domain.Digest([]byte(q.cookie)))
			return err
		})
		if err == nil {
			err = read()
			if accessLost(err) {
				control("closed", ClosedAccessRevoked, nil)
				return
			}
		} else if errors.Is(err, domain.ErrNotFound) {
			control("closed", ClosedUnauthenticated, nil)
			return
		}
		switch {
		case err == nil:
			failingSince = time.Time{}
		case ctx.Err() != nil:
			return
		case failingSince.IsZero():
			failingSince = now
			page = postgres.EventPage{Head: cursor}
		case now.Sub(failingSince) > streamUnavailable:
			control("closed", ClosedUnavailable, nil)
			return
		default:
			page = postgres.EventPage{Head: cursor}
		}
	}
}
