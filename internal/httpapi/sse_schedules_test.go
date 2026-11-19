package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// SSE (TC-99 extension, TC-02/TC-31 for streams) and the schedule API
// over real HTTP and PostgreSQL. Skips (NOT executed) without
// CODEYE_TEST_DATABASE_URL.

func fastStreams(e *env, c StreamConfig) {
	e.t.Helper()
	if c.Poll == 0 {
		c.Poll = 40 * time.Millisecond
	}
	if c.Heartbeat == 0 {
		c.Heartbeat = 10 * time.Second
	}
	e.stream = c
	e.start(e.t.TempDir())
}

type sseMessage struct {
	id    string // "" when the message has no id line
	event string // "" for an event row (message)
	data  map[string]any
}

type sseClient struct {
	t      *testing.T
	status int
	header http.Header
	body   []byte // error body when status != 200
	resp   *http.Response
	msgs   chan sseMessage
	done   chan struct{}
}

// openStream connects to /runs/{id}/events with a cursor query and an
// optional Last-Event-ID.
func (e *env) openStream(base string, u user, run, query, lastID string) *sseClient {
	e.t.Helper()
	host := e.host
	if base == "" {
		base = e.hs.URL
	} else {
		host = strings.TrimPrefix(base, "http://")
	}
	req, err := http.NewRequest("GET", base+"/api/v1/runs/"+run+"/events"+query, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = host
	if u.cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: u.cookie})
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		e.t.Fatal(err)
	}
	c := &sseClient{t: e.t, status: res.StatusCode, header: res.Header, resp: res, msgs: make(chan sseMessage, 1000), done: make(chan struct{})}
	if res.StatusCode != http.StatusOK {
		c.body, _ = io.ReadAll(res.Body)
		res.Body.Close()
		close(c.done)
		close(c.msgs)
		return c
	}
	e.t.Cleanup(c.close)
	go func() {
		defer close(c.done)
		defer close(c.msgs)
		sc := bufio.NewScanner(res.Body)
		var m sseMessage
		var data string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if data != "" {
					_ = json.Unmarshal([]byte(data), &m.data)
					c.msgs <- m
				}
				m, data = sseMessage{}, ""
			case strings.HasPrefix(line, "id: "):
				m.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				m.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return c
}

func (c *sseClient) close() {
	if c.resp != nil {
		c.resp.Body.Close()
		<-c.done
	}
}

// next returns the next message, failing after timeout. ok is false at EOF.
func (c *sseClient) next(timeout time.Duration) (sseMessage, bool) {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		return m, ok
	case <-time.After(timeout):
		c.t.Fatal("no SSE message in time")
	}
	return sseMessage{}, false
}

// until reads messages until one matches, returning everything read.
func (c *sseClient) until(match func(sseMessage) bool) []sseMessage {
	c.t.Helper()
	var seen []sseMessage
	for {
		m, ok := c.next(10 * time.Second)
		if !ok {
			c.t.Fatalf("stream ended before the expected message; read %v", seen)
		}
		seen = append(seen, m)
		if match(m) {
			return seen
		}
	}
}

// ended waits for EOF (the server closed the stream).
func (c *sseClient) ended() {
	c.t.Helper()
	for {
		if _, ok := c.next(10 * time.Second); !ok {
			return
		}
	}
}

func isView(m sseMessage) bool { return m.event == "run.view" }

func (e *env) createRun(u user, project string) string {
	e.t.Helper()
	r := e.do("POST", "/api/v1/projects/"+project+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, opts{cookie: u.cookie, csrf: u.csrf, idem: domain.NewID()}).want(e.t, 202, "")
	return r.body["id"].(string)
}

// TC-99: the stream delivers stored events in sequence after the
// cursor with safe fields and the same run view as GET; Last-Event-ID wins
// over the URL cursor on reconnect; heartbeats carry no id and no progress.
func TestSSEDeliversRunEventsInSequence(t *testing.T) {
	e := newEnv(t)
	fastStreams(e, StreamConfig{Heartbeat: 300 * time.Millisecond})
	f := e.nodeFixture()
	u := f.user
	runID := e.createRun(u, f.projectID)
	first := e.do("GET", "/api/v1/runs/"+runID, nil, u.o()).want(t, 200, "")
	if first.body["event_sequence"] != float64(1) || !reflect.DeepEqual(first.body["trigger"], map[string]any{"kind": "manual"}) {
		t.Fatalf("run view %s", first.raw)
	}
	p := first.body["progress"].(map[string]any)
	if p["plan_revision"] != nil || p["estimated_percent"] != nil || p["planned_weight"] != nil || p["weight_basis"] != nil || !reflect.DeepEqual(p["plan_changes"], []any{}) {
		t.Fatalf("queued progress %v", p)
	}
	s := e.openStream("", u, runID, "?cursor=0", "")
	if s.status != 200 || !strings.HasPrefix(s.header.Get("Content-Type"), "text/event-stream") || s.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("stream %d %v %s", s.status, s.header, s.body)
	}
	got := s.until(isView)
	if len(got) != 2 || got[0].id != "1" || got[0].event != "" || got[0].data["type"] != "run.queued" || got[0].data["trigger"] != "manual" || got[0].data["sequence"] != float64(1) {
		t.Fatalf("first batch %+v", got)
	}

	n := e.enrollNode(f.root.ID)
	a := e.claimHTTP(f, n, domain.NewID(), 1)
	a.snapshot()
	a.task("vet", "static_analysis")
	a.ev("task_created", map[string]any{"key": "review", "required": true, "stage": "code_review", "weight": 3})
	a.ev("run_advance", map[string]any{"status": "running"})
	a.move("vet", "running")
	a.move("vet", "succeeded")
	var seq int64
	if err := e.pool.QueryRow(context.Background(), "SELECT event_sequence FROM runs WHERE id=$1", runID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	head := float64(seq)
	var rows []sseMessage
	var last sseMessage
	for {
		m, ok := s.next(10 * time.Second)
		if !ok {
			t.Fatal("stream ended")
		}
		if m.event == "" {
			rows = append(rows, m)
		}
		if isView(m) && m.data["event_sequence"] == head {
			last = m
			break
		}
	}
	types := []string{}
	for i, m := range rows {
		if m.id != strconv.Itoa(i+2) || m.data["sequence"] != float64(i+2) {
			t.Fatalf("row %d out of sequence: %+v", i, m)
		}
		types = append(types, m.data["type"].(string))
		raw, _ := json.Marshal(m.data)
		for _, secret := range []string{"node_id", "worker_id", "manifest_digest", "sha256", n.NodeID, a.worker} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("event row leaks %q: %s", secret, raw)
			}
		}
	}
	if want := "attempt.leased,artifact.finalized,snapshot.fixed,task.created,task.created,run.status,task.status,task.status"; strings.Join(types, ",") != want {
		t.Fatalf("event types %s", strings.Join(types, ","))
	}
	if rows[4].data["weight"] != float64(3) || rows[4].data["stage"] != "code_review" {
		t.Fatalf("task.created row %+v", rows[4])
	}
	// The run.view event is the GET /runs/{id} document (same aggregation).
	now := e.do("GET", "/api/v1/runs/"+runID, nil, u.o()).want(t, 200, "").body
	strip := func(v map[string]any) map[string]any {
		out := map[string]any{}
		for k, x := range v {
			out[k] = x
		}
		pr := map[string]any{}
		for k, x := range v["progress"].(map[string]any) {
			if k != "checked_at" {
				pr[k] = x
			}
		}
		out["progress"] = pr
		return out
	}
	if !reflect.DeepEqual(strip(last.data), strip(now)) {
		t.Fatalf("run.view differs from GET:\n%v\n%v", last.data, now)
	}
	if pr := now["progress"].(map[string]any); pr["plan_revision"] != float64(1) || pr["planned_weight"] != float64(4) || pr["estimated_percent"] != float64(25) || pr["weight_basis"] != "mixed" {
		t.Fatalf("weighted progress %v", pr)
	}
	// Idle: heartbeats only, without id; the view does not change.
	hb := s.until(func(m sseMessage) bool { return m.event == "heartbeat" })
	if m := hb[len(hb)-1]; m.id != "" || m.data["run_id"] != runID || len(m.data) != 1 {
		t.Fatalf("heartbeat %+v", m)
	}
	s.close()

	// Reconnect: Last-Event-ID (the browser's) wins over the stale URL cursor.
	r := e.openStream("", u, runID, "?cursor=0", "5")
	if m, _ := r.next(10 * time.Second); m.id != "6" {
		t.Fatalf("resume from Last-Event-ID 5 delivered %+v", m)
	}
	r.close()
	// A task added after the plan was fixed moves the denominator: revision 2.
	a.ev("task_created", map[string]any{"key": "late", "required": false, "stage": "code_review", "weight": 2})
	pr := e.do("GET", "/api/v1/runs/"+runID, nil, u.o()).want(t, 200, "").body["progress"].(map[string]any)
	changes := pr["plan_changes"].([]any)
	if pr["plan_revision"] != float64(2) || pr["planned_weight"] != float64(6) || len(changes) != 1 {
		t.Fatalf("revised progress %v", pr)
	}
	if c := changes[0].(map[string]any); c["revision"] != float64(2) || c["reason"] != "task_added" || c["stage"] != "code_review" || c["weight"] != float64(2) || c["planned_weight"] != float64(6) || c["recorded_at"] == nil {
		t.Fatalf("plan change %v", c)
	}
}

// Cursor errors: absent/invalid is 400, ahead of the run or older than the
// replay window is state.resync (no id) followed by the end of the stream.
func TestSSECursorResync(t *testing.T) {
	e := newEnv(t)
	fastStreams(e, StreamConfig{ReplayWindow: 2})
	f := e.nodeFixture()
	u := f.user
	runID := e.createRun(u, f.projectID)
	for _, q := range []string{"", "?cursor=", "?cursor=-1", "?cursor=01", "?cursor=x", "?cursor=99999999999999999"} {
		if s := e.openStream("", u, runID, q, ""); s.status != 400 || !strings.Contains(string(s.body), "INVALID_REQUEST") {
			t.Fatalf("cursor %q: %d %s", q, s.status, s.body)
		}
	}
	s := e.openStream("", u, runID, "?cursor=9", "")
	m, _ := s.next(10 * time.Second)
	if m.event != "state.resync" || m.id != "" || m.data["reason"] != ResyncCursorAhead || m.data["head"] != float64(1) {
		t.Fatalf("ahead %+v", m)
	}
	s.ended()
	n := e.enrollNode(f.root.ID)
	a := e.claimHTTP(f, n, domain.NewID(), 1)
	a.snapshot() // head is now 4 (queued, leased, artifact, snapshot)
	s = e.openStream("", u, runID, "?cursor=0", "")
	if m, _ := s.next(10 * time.Second); m.event != "state.resync" || m.data["reason"] != ResyncCursorExpired {
		t.Fatalf("expired %+v", m)
	}
	s.ended()
	s = e.openStream("", u, runID, "?cursor=2", "")
	got := s.until(isView)
	if len(got) != 3 || got[0].id != "3" || got[1].id != "4" {
		t.Fatalf("within the window %+v", got)
	}
}

// extraServer starts another API server on its own loopback port over store.
func (e *env) extraServer(c StreamConfig, store Store) (*Server, *httptest.Server) {
	e.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.t.Fatal(err)
	}
	srv, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), Logger: slog.New(slog.DiscardHandler), SweepInterval: -1, Stream: c}, store)
	if err != nil {
		e.t.Fatal(err)
	}
	hs := &httptest.Server{Listener: ln, Config: &http.Server{Handler: srv}}
	hs.Start()
	return srv, hs
}

// gapStore drops one stored event to prove the server reports a sequence gap
// instead of skipping it (stored events are contiguous and never deleted, so
// a gap cannot be produced through PostgreSQL itself).
type gapStore struct {
	*postgres.Store
	drop int64
}

func (g gapStore) RunEvents(ctx context.Context, actor, id string, after int64, limit int) (postgres.EventPage, error) {
	p, err := g.Store.RunEvents(ctx, actor, id, after, limit)
	kept := p.Events[:0]
	for _, ev := range p.Events {
		if ev.Sequence != g.drop {
			kept = append(kept, ev)
		}
	}
	p.Events = kept
	return p, err
}

func TestSSEGapIsReported(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	u := f.user
	runID := e.createRun(u, f.projectID)
	n := e.enrollNode(f.root.ID)
	e.claimHTTP(f, n, domain.NewID(), 1).snapshot()
	srv, hs := e.extraServer(StreamConfig{Poll: 40 * time.Millisecond}, gapStore{Store: e.store, drop: 2})
	defer func() { srv.StopStreams(); hs.Close() }()
	s := e.openStream(hs.URL, u, runID, "?cursor=0", "")
	got := s.until(func(m sseMessage) bool { return m.event == "state.resync" })
	if len(got) != 2 || got[0].id != "1" || got[1].data["reason"] != ResyncGap || got[1].data["expected"] != float64(2) {
		t.Fatalf("gap %+v", got)
	}
	s.ended()
}

// TC-02/TC-31 for streams: a stranger gets 404 before any stream, no session
// is 401, logout and loss of membership close an open stream.
// TC-99: events committed between a row batch and the run.view read
// are sent as rows before that view, so a run.view never describes an
// event_sequence beyond the rows the client has received.
func TestSSEViewNeverAheadOfRows(t *testing.T) {
	e := newEnv(t)
	fastStreams(e, StreamConfig{})
	f := e.nodeFixture()
	u := f.user
	runID := e.createRun(u, f.projectID)
	a := e.claimHTTP(f, e.enrollNode(f.root.ID), domain.NewID(), 1)
	reached, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hook := func() {
		once.Do(func() {
			close(reached)
			select {
			case <-resume:
			case <-time.After(10 * time.Second):
			}
		})
	}
	testHookBeforeView.Store(&hook)
	t.Cleanup(func() { testHookBeforeView.Store(nil) })
	s := e.openStream("", u, runID, "?cursor=0", "")
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("stream never reached the run.view read")
	}
	// Rows 1-2 are sent; two more events commit before the view is read.
	a.snapshot()
	close(resume)
	var head int64
	if err := e.pool.QueryRow(context.Background(), "SELECT event_sequence FROM runs WHERE id=$1", runID).Scan(&head); err != nil {
		t.Fatal(err)
	}
	got := s.until(isView)
	view := got[len(got)-1]
	var types []string
	for i, m := range got[:len(got)-1] {
		if m.event != "" || m.id != strconv.Itoa(i+1) {
			t.Fatalf("message %d before the view: %+v", i, m)
		}
		types = append(types, m.data["type"].(string))
	}
	if head != 4 || view.data["event_sequence"] != float64(head) || len(types) != int(head) {
		t.Fatalf("view event_sequence %v after %d rows (head %d)", view.data["event_sequence"], len(types), head)
	}
	if want := "run.queued,attempt.leased,artifact.finalized,snapshot.fixed"; strings.Join(types, ",") != want {
		t.Fatalf("event types %s", strings.Join(types, ","))
	}
}

func TestSSEAccessControl(t *testing.T) {
	e := newEnv(t)
	fastStreams(e, StreamConfig{})
	f := e.nodeFixture()
	owner := f.user
	runID := e.createRun(owner, f.projectID)
	stranger := e.newUser()
	if s := e.openStream("", stranger, runID, "?cursor=0", ""); s.status != 404 || strings.Contains(string(s.body), runID) {
		t.Fatalf("stranger %d %s", s.status, s.body)
	}
	if s := e.openStream("", user{}, runID, "?cursor=0", ""); s.status != 401 {
		t.Fatalf("anonymous %d", s.status)
	}
	if s := e.openStream("", owner, domain.NewID(), "?cursor=0", ""); s.status != 404 {
		t.Fatalf("unknown run %d", s.status)
	}
	// Logout ends the stream with closed/unauthenticated.
	s := e.openStream("", owner, runID, "?cursor=1", "")
	e.do("DELETE", "/api/v1/session", nil, owner.o()).want(t, 204, "")
	m := s.until(func(m sseMessage) bool { return m.event == "closed" })
	if last := m[len(m)-1]; last.data["reason"] != ClosedUnauthenticated || last.id != "" {
		t.Fatalf("logout %+v", last)
	}
	s.ended()
	// Membership removed while streaming: closed/access_revoked.
	owner = e.login(owner.id)
	viewer := e.newUser()
	if err := e.store.SetMember(context.Background(), owner.id, f.projectID, viewer.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	s = e.openStream("", viewer, runID, "?cursor=1", "")
	if err := e.store.SetMember(context.Background(), owner.id, f.projectID, viewer.id, ""); err != nil {
		t.Fatal(err)
	}
	m = s.until(func(m sseMessage) bool { return m.event == "closed" })
	if last := m[len(m)-1]; last.data["reason"] != ClosedAccessRevoked {
		t.Fatalf("revoked %+v", last)
	}
	s.ended()
	// Stopping the server ends open streams (graceful shutdown is not held).
	s = e.openStream("", owner, runID, "?cursor=1", "")
	e.srv.StopStreams()
	s.ended()
}

// One user cannot take every stream slot: over the per-user cap that user
// gets 503 while another user still connects; a closed stream frees its slot.
func TestSSEPerUserStreamCap(t *testing.T) {
	e := newEnv(t)
	fastStreams(e, StreamConfig{MaxStreamsPerUser: 2, MaxStreams: 3})
	f := e.nodeFixture()
	a := f.user
	runID := e.createRun(a, f.projectID)
	b := e.newUser()
	if err := e.store.SetMember(context.Background(), a.id, f.projectID, b.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	first, second := open2(e, a, runID), open2(e, a, runID)
	if s := e.openStream("", a, runID, "?cursor=1", ""); s.status != 503 || !strings.Contains(string(s.body), "UNAVAILABLE") {
		t.Fatalf("user over cap %d %s", s.status, s.body)
	}
	other := open2(e, b, runID)
	// The global cap (3) is now reached for everyone.
	if s := e.openStream("", b, runID, "?cursor=1", ""); s.status != 503 {
		t.Fatalf("global cap %d", s.status)
	}
	first.close()
	waitSlots(t, e, 2)
	again := open2(e, a, runID)
	again.close()
	second.close()
	other.close()
	waitSlots(t, e, 0)
}

// open2 opens a stream and waits until the server holds its slot.
func open2(e *env, u user, run string) *sseClient {
	e.t.Helper()
	before := streamCount(e)
	s := e.openStream("", u, run, "?cursor=1", "")
	if s.status != 200 {
		e.t.Fatalf("open %d %s", s.status, s.body)
	}
	waitSlots(e.t, e, before+1)
	return s
}

func streamCount(e *env) int32 {
	e.srv.streamsMu.Lock()
	defer e.srv.streamsMu.Unlock()
	return e.srv.streams
}

func waitSlots(t *testing.T, e *env, want int32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for streamCount(e) != want {
		if time.Now().After(deadline) {
			t.Fatalf("open streams %d, want %d", streamCount(e), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The stream survives the production server's read/write timeouts (deadlines
// are cleared/extended through the request recorder) and Serve's graceful
// shutdown is not held by an open stream.
func TestSSEOnRealServerTimeouts(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	runID := e.createRun(f.user, f.projectID)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(ln.Addr().String())
	srv, err := New(Config{Listen: ap, Logger: slog.New(slog.DiscardHandler), SweepInterval: -1, ReconcileInterval: -1, PublishInterval: -1,
		Stream: StreamConfig{Poll: 50 * time.Millisecond, Heartbeat: 200 * time.Millisecond}}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: srv, ReadTimeout: 500 * time.Millisecond, WriteTimeout: 500 * time.Millisecond}
	go func() { _ = hs.Serve(ln) }()
	defer hs.Close()
	s := e.openStream("http://"+ap.String(), f.user, runID, "?cursor=1", "")
	if s.status != 200 {
		t.Fatalf("status %d %s", s.status, s.body)
	}
	deadline := time.Now().Add(2 * time.Second)
	beats := 0
	for time.Now().Before(deadline) {
		if m, ok := s.next(2 * time.Second); !ok {
			t.Fatal("stream cut by a server timeout")
		} else if m.event == "heartbeat" {
			beats++
		}
	}
	if beats < 4 {
		t.Fatalf("heartbeats %d", beats)
	}
	s.close()

	// Serve: shutdown with an open stream returns promptly.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap2 := netip.MustParseAddrPort(ln2.Addr().String())
	srv2, err := New(Config{Listen: ap2, Logger: slog.New(slog.DiscardHandler), SweepInterval: -1, ReconcileInterval: -1, PublishInterval: -1,
		Stream: StreamConfig{Poll: 50 * time.Millisecond}}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv2.Serve(ctx, ln2) }()
	s2 := e.openStream("http://"+ap2.String(), f.user, runID, "?cursor=0", "")
	if s2.status != 200 {
		t.Fatalf("status %d", s2.status)
	}
	s2.until(isView)
	start := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil || time.Since(start) > 5*time.Second {
			t.Fatalf("serve %v after %s", err, time.Since(start))
		}
	case <-time.After(9 * time.Second):
		t.Fatal("shutdown held by an open stream")
	}
	s2.ended()
}

// The schedule API: manager-only mutations, Idempotency-Key on create,
// If-Match on change/delete, viewers list, strangers get 404.
func TestScheduleAPI(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	m := f.user
	path := "/api/v1/projects/" + f.projectID + "/schedules"
	body := map[string]any{"cron": "0 9 * * 1-5", "timezone": "Asia/Seoul", "run": map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}}
	e.do("POST", path, body, m.o()).want(t, 400, "INVALID_REQUEST") // no Idempotency-Key
	created := e.do("POST", path, body, opts{cookie: m.cookie, csrf: m.csrf, idem: "s1"}).want(t, 201, "")
	id := created.body["id"].(string)
	if created.header.Get("ETag") != `"1"` || created.header.Get("Location") != path+"/"+id || created.body["overlap"] != "coalesce_latest" || created.body["misfire"] != "latest_once" ||
		created.body["timezone"] != "Asia/Seoul" || created.body["next_run_at"] == nil || created.body["last_occurrence"] != nil || created.body["pending_occurrence"] != nil {
		t.Fatalf("created %v %s", created.header, created.raw)
	}
	if again := e.do("POST", path, body, opts{cookie: m.cookie, csrf: m.csrf, idem: "s1"}).want(t, 201, ""); again.body["id"] != id {
		t.Fatalf("replay %s", again.raw)
	}
	changed := map[string]any{"cron": "0 10 * * *", "timezone": "UTC", "run": body["run"]}
	e.do("POST", path, changed, opts{cookie: m.cookie, csrf: m.csrf, idem: "s1"}).want(t, 409, "IDEMPOTENCY_CONFLICT")
	for _, bad := range []map[string]any{
		{"cron": "0 0 30 2 *", "timezone": "UTC", "run": body["run"]},
		{"cron": "@daily", "timezone": "Local", "run": body["run"]},
		{"cron": "@daily", "timezone": "UTC", "overlap": "", "run": body["run"]},
		{"cron": "@daily", "timezone": "UTC", "misfire": "catch_up", "run": body["run"]},
	} {
		e.do("POST", path, bad, opts{cookie: m.cookie, csrf: m.csrf, idem: domain.NewID()}).want(t, 422, "VALIDATION_FAILED")
	}
	e.do("POST", path, map[string]any{"cron": "@daily", "timezone": "UTC"}, opts{cookie: m.cookie, csrf: m.csrf, idem: domain.NewID()}).want(t, 400, "INVALID_REQUEST")
	viewer, stranger := e.newUser(), e.newUser()
	if err := e.store.SetMember(context.Background(), m.id, f.projectID, viewer.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	list := e.do("GET", path, nil, viewer.o()).want(t, 200, "")
	if list.body["can_manage"] != false || len(list.body["items"].([]any)) != 1 {
		t.Fatalf("viewer list %s", list.raw)
	}
	if mine := e.do("GET", path, nil, m.o()).want(t, 200, ""); mine.body["can_manage"] != true {
		t.Fatalf("manager list %s", mine.raw)
	}
	e.do("POST", path, body, opts{cookie: viewer.cookie, csrf: viewer.csrf, idem: "v"}).want(t, 403, "FORBIDDEN")
	e.do("GET", path, nil, stranger.o()).want(t, 404, "NOT_FOUND")
	one := path + "/" + id
	e.do("PATCH", one, map[string]any{"enabled": false}, m.o()).want(t, 400, "INVALID_REQUEST") // no If-Match
	patch := func(u user, ifMatch string, b any) resp {
		return e.do("PATCH", one, b, opts{cookie: u.cookie, csrf: u.csrf, ifMatch: ifMatch})
	}
	del := func(ifMatch string) resp {
		return e.do("DELETE", one, nil, opts{cookie: m.cookie, csrf: m.csrf, ifMatch: ifMatch})
	}
	patch(m, `"7"`, map[string]any{"enabled": false}).want(t, 409, "CONFLICT")
	patch(viewer, `"1"`, map[string]any{"enabled": false}).want(t, 403, "FORBIDDEN")
	patch(m, `"1"`, map[string]any{}).want(t, 400, "INVALID_REQUEST")
	patch(m, `"1"`, map[string]any{"cron": "61 * * * *"}).want(t, 422, "VALIDATION_FAILED")
	off := patch(m, `"1"`, map[string]any{"enabled": false, "overlap": "queue"}).want(t, 200, "")
	if off.header.Get("ETag") != `"2"` || off.body["enabled"] != false || off.body["next_run_at"] != nil || off.body["overlap"] != "queue" {
		t.Fatalf("patch %v %s", off.header, off.raw)
	}
	del("").want(t, 400, "INVALID_REQUEST")
	del(`"1"`).want(t, 409, "CONFLICT")
	del(`"2"`).want(t, 204, "")
	del(`"3"`).want(t, 404, "NOT_FOUND")
	if left := e.do("GET", path, nil, m.o()).want(t, 200, ""); len(left.body["items"].([]any)) != 0 {
		t.Fatalf("after delete %s", left.raw)
	}
}

// TC-25 through the serve path: the reconcile loop of Serve materializes a due
// occurrence into a normal queued run whose trigger names the schedule, seen
// over GET /runs/{id}, the schedule list and the run's event stream.
func TestServeLoopMaterializesSchedule(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	m := f.user
	path := "/api/v1/projects/" + f.projectID + "/schedules"
	created := e.do("POST", path, map[string]any{"cron": "*/5 * * * *", "timezone": "UTC", "run": map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}},
		opts{cookie: m.cookie, csrf: m.csrf, idem: "serve"}).want(t, 201, "")
	id := created.body["id"].(string)
	var due time.Time
	if err := e.pool.QueryRow(context.Background(), "UPDATE schedules SET next_at=date_bin('5 minutes', now(), timestamptz 'epoch') - interval '10 minutes' WHERE id=$1 RETURNING next_at", id).Scan(&due); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(ln.Addr().String())
	srv, err := New(Config{Listen: ap, Logger: slog.New(slog.DiscardHandler), SweepInterval: -1, PublishInterval: -1, ReconcileInterval: 50 * time.Millisecond}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln) }()
	defer func() { cancel(); <-served }()
	var last map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for last == nil {
		if time.Now().After(deadline) {
			t.Fatal("the serve loop did not materialize the due occurrence")
		}
		time.Sleep(50 * time.Millisecond)
		items := e.do("GET", path, nil, m.o()).want(t, 200, "").body["items"].([]any)
		last, _ = items[0].(map[string]any)["last_occurrence"].(map[string]any)
	}
	runID, _ := last["run_id"].(string)
	// The controller was "down" for two occurrences: latest_once runs only the
	// latest and records the first missed one.
	at, _ := time.Parse(time.RFC3339, last["scheduled_for"].(string))
	if last["state"] != "enqueued" || runID == "" || last["missed_from"] != due.UTC().Format(time.RFC3339) || !at.After(due) || at.Sub(due) > 15*time.Minute {
		t.Fatalf("occurrence %v (due %s)", last, due.UTC().Format(time.RFC3339))
	}
	run := e.do("GET", "/api/v1/runs/"+runID, nil, m.o()).want(t, 200, "").body
	if want := map[string]any{"kind": "schedule", "schedule_id": id, "scheduled_for": last["scheduled_for"]}; !reflect.DeepEqual(run["trigger"], want) || run["status"] != "queued" {
		t.Fatalf("scheduled run %v", run)
	}
	s := e.openStream("", m, runID, "?cursor=0", "")
	got := s.until(isView)
	if got[0].data["type"] != "run.queued" || got[0].data["trigger"] != "schedule" || got[0].data["schedule_id"] != id || got[0].data["scheduled_for"] != last["scheduled_for"] {
		t.Fatalf("run.queued row %+v", got[0])
	}
}
