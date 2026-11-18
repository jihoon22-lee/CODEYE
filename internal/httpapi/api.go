package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

const (
	apiPrefix      = "/api/v1"
	sessionCookie  = "codeye_session"
	csrfHeader     = "X-CODEYE-CSRF"
	maxBodyBytes   = 64 << 10
	storeTimeout   = 15 * time.Second
	readyTimeout   = 3 * time.Second
	tokenBytes     = 32
	maxIdempotency = 200
)

type request struct {
	w       http.ResponseWriter
	r       *http.Request
	params  []string
	session postgres.Session
	cookie  string
	node    postgres.NodeIdentity
}

type route struct {
	method string
	parts  []string // "{id}" segments must be canonical UUIDs
	name   string
	auth   bool
	body   bool
	// node marks node-protocol routes: nodePublic (enroll) or nodeAuth
	// (Bearer node secret). They never read cookies and reject Origin.
	node   nodeAuthKind
	handle func(*Server, *request)
}

func (s *Server) apiRoutes() []route {
	r := func(method, path, name string, auth, body bool, h func(*Server, *request)) route {
		return route{method: method, parts: strings.Split(strings.TrimPrefix(path, "/"), "/"), name: name, auth: auth, body: body, handle: h}
	}
	return append([]route{
		r("GET", "health", "health", false, false, (*Server).health),
		r("GET", "ready", "ready", false, false, (*Server).ready),
		r("POST", "session", "session.create", false, true, (*Server).createSession),
		r("GET", "session", "session.get", true, false, (*Server).getSession),
		r("DELETE", "session", "session.delete", true, false, (*Server).deleteSession),
		r("GET", "source-roots", "source_roots.list", true, false, (*Server).listSourceRoots),
		r("GET", "providers", "providers.list", true, false, (*Server).listProviders),
		r("GET", "projects", "projects.list", true, false, (*Server).listProjects),
		r("POST", "projects", "projects.create", true, true, (*Server).createProject),
		r("GET", "projects/{id}", "projects.get", true, false, (*Server).getProject),
		r("POST", "projects/{id}/runs", "runs.create", true, true, (*Server).createRun),
		r("GET", "projects/{id}/schedules", "schedules.list", true, false, (*Server).listSchedules),
		r("POST", "projects/{id}/schedules", "schedules.create", true, true, (*Server).createSchedule),
		r("PATCH", "projects/{id}/schedules/{id}", "schedules.update", true, true, (*Server).updateSchedule),
		r("DELETE", "projects/{id}/schedules/{id}", "schedules.delete", true, false, (*Server).deleteSchedule),
		r("GET", "runs/{id}", "runs.get", true, false, (*Server).getRun),
		r("GET", "runs/{id}/events", "runs.events", true, false, (*Server).streamRunEvents),
		r("POST", "runs/{id}/cancel", "runs.cancel", true, false, (*Server).cancelRun),
		r("GET", "runs/{id}/report", "runs.report", true, false, (*Server).getReport),
		r("GET", "runs/{id}/reports", "runs.reports", true, false, (*Server).listReports),
		r("GET", "runs/{id}/comparison", "runs.comparison", true, false, (*Server).getComparison),
		r("GET", "projects/{id}/finding-reviews", "finding_reviews.list", true, false, (*Server).listFindingReviews),
		r("POST", "projects/{id}/finding-reviews", "finding_reviews.create", true, true, (*Server).setFindingReview),
	}, s.nodeRoutes()...)
}

func (rt route) match(segs []string) ([]string, bool) {
	if len(segs) != len(rt.parts) {
		return nil, false
	}
	var params []string
	for i, p := range rt.parts {
		if p == "{id}" {
			params = append(params, segs[i])
			continue
		}
		if p != segs[i] {
			return nil, false
		}
	}
	return params, true
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// serveAPI routes an /api request and returns the route name for logging.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) string {
	if !strings.HasPrefix(r.URL.Path, apiPrefix+"/") {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "not found", false)
		return "not_found"
	}
	segs := strings.Split(strings.TrimPrefix(r.URL.Path, apiPrefix+"/"), "/")
	var allowed []string
	for _, rt := range s.routes {
		params, ok := rt.match(segs)
		if !ok {
			continue
		}
		if rt.method != r.Method {
			allowed = append(allowed, rt.method)
			continue
		}
		for _, p := range params {
			if !domain.ValidID(p) {
				writeError(w, r, http.StatusNotFound, "NOT_FOUND", "not found", false)
				return rt.name
			}
		}
		s.dispatch(rt, &request{w: w, r: r, params: params})
		return rt.name
	}
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", false)
		return "method_not_allowed"
	}
	writeError(w, r, http.StatusNotFound, "NOT_FOUND", "not found", false)
	return "not_found"
}

func (s *Server) dispatch(rt route, q *request) {
	w, r := q.w, q.r
	if rt.node != nodeNone {
		s.dispatchNode(rt, q)
		return
	}
	if unsafeMethod(r.Method) {
		// Cross-site and DNS-rebinding defence: the browser-sent Origin must be
		// exactly this server's origin (Host was already allowlisted).
		if origin := r.Header.Get("Origin"); origin == "" || origin != "http://"+r.Host {
			writeError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "origin is not allowed", false)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			writeError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "cross-site request is not allowed", false)
			return
		}
	}
	if rt.auth {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !validToken(c.Value) {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required", false)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
		sess, err := s.store.SessionByTokenHash(ctx, domain.Digest([]byte(c.Value)))
		cancel()
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required", false)
			} else {
				s.storeError(q, err)
			}
			return
		}
		q.session, q.cookie = sess, c.Value
		if unsafeMethod(r.Method) {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(csrfToken(c.Value))) != 1 {
				writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "missing or invalid CSRF token", false)
				return
			}
		}
	}
	if rt.body {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
			writeError(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "content type must be application/json", false)
			return
		}
	}
	rt.handle(s, q)
}

// readBody reads at most maxBodyBytes. It writes the error response itself.
func (q *request) readBody() ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(q.w, q.r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(q.w, q.r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body is too large", false)
		} else {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "cannot read request body", false)
		}
		return nil, false
	}
	return body, true
}

func (q *request) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(q.r.Context(), storeTimeout)
}

// storeError maps storage error classes to the public envelope without detail.
func (s *Server) storeError(q *request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(q.w, q.r, http.StatusNotFound, "NOT_FOUND", "not found", false)
	case errors.Is(err, domain.ErrForbidden):
		writeError(q.w, q.r, http.StatusForbidden, "FORBIDDEN", "insufficient project role", false)
	case errors.Is(err, domain.ErrStaleAttempt):
		writeError(q.w, q.r, http.StatusConflict, "STALE_ATTEMPT", "the attempt lease is not held by this worker and epoch", false)
	case errors.Is(err, domain.ErrConflict):
		writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "the request conflicts with the current state", false)
	case errors.Is(err, domain.ErrUnsupported):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "SOURCE_UNSUPPORTED", "this source form is not supported", false)
	case errors.Is(err, domain.ErrInvalid):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "the request failed validation", false)
	default:
		writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "service temporarily unavailable", true)
	}
}

// --- tokens ----------------------------------------------------------------

// NewToken returns a random URL-safe token (32 bytes, base64url, no padding).
func NewToken() string {
	var b [tokenBytes]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// TokenHash is the stored form of a token: lowercase hex SHA-256 of its text.
func TokenHash(token string) string { return domain.Digest([]byte(token)) }

func validToken(t string) bool {
	if len(t) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(t)
	return err == nil && len(b) == tokenBytes
}

// csrfToken derives the CSRF token from the session token. Only a holder of
// the HttpOnly session cookie (the server) can compute it; the browser app
// receives it from /session responses.
func csrfToken(session string) string {
	sum := sha256.Sum256([]byte("codeye-csrf\x00" + session))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// --- health / readiness ----------------------------------------------------

func (s *Server) health(q *request) {
	writeJSON(q.w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(q *request) {
	ctx, cancel := context.WithTimeout(q.r.Context(), readyTimeout)
	defer cancel()
	st, err := s.store.SchemaStatus(ctx)
	reason := ""
	switch {
	case err != nil:
		reason = "database_unavailable"
	case st.State == "current":
		writeJSON(q.w, http.StatusOK, map[string]any{"status": "ready", "schema_version": st.Applied})
		return
	case st.State == "outdated":
		reason = "schema_outdated"
	case st.State == "newer":
		reason = "schema_newer"
	default:
		reason = "schema_inconsistent"
	}
	writeJSON(q.w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": reason})
}

// --- session ---------------------------------------------------------------

type sessionView struct {
	UserID    string    `json:"user_id"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) createSession(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		LaunchToken string `json:"launch_token"`
	}
	if domain.DecodeStrict(body, &in) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be {\"launch_token\": string}", false)
		return
	}
	if !validToken(in.LaunchToken) {
		writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
		return
	}
	token := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	sess, err := s.store.ExchangeLaunchToken(ctx, TokenHash(in.LaunchToken), TokenHash(token), s.sessionTTL)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	http.SetCookie(q.w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()),
	})
	writeJSON(q.w, http.StatusCreated, sessionView{sess.UserID, csrfToken(token), sess.ExpiresAt.UTC()})
}

func (s *Server) getSession(q *request) {
	writeJSON(q.w, http.StatusOK, sessionView{q.session.UserID, csrfToken(q.cookie), q.session.ExpiresAt.UTC()})
}

func (s *Server) deleteSession(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RevokeSession(ctx, q.session.ID); err != nil {
		s.storeError(q, err)
		return
	}
	http.SetCookie(q.w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	q.w.WriteHeader(http.StatusNoContent)
}

// --- registry --------------------------------------------------------------

func (s *Server) listSourceRoots(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	roots, err := s.store.ListSourceRoots(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	type item struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	items := make([]item, 0, len(roots))
	for _, r := range roots {
		items = append(items, item{r.ID, r.Label})
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listProviders(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	provs, err := s.store.ListProviders(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	type item struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Host string `json:"host"`
	}
	items := make([]item, 0, len(provs))
	for _, p := range provs {
		items = append(items, item{p.ID, p.Kind, p.Host})
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}

// --- projects --------------------------------------------------------------

type runRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type projectView struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Source         json.RawMessage `json:"source"`
	Revision       int64           `json:"revision"`
	SourceStatus   string          `json:"source_status"`
	CapabilityGaps []string        `json:"capability_gaps"`
	LastRun        *runRef         `json:"last_run"`
	Activity       activityView    `json:"activity"`
}

// stageView is one display stage (docs/37 §2, §4). Planned is null until the
// plan is fixed and always null for source/report.
type stageView struct {
	Key        string         `json:"key"`
	State      string         `json:"state"`
	Planned    *int           `json:"planned"`
	Finished   int            `json:"finished"`
	Succeeded  int            `json:"succeeded"`
	Counts     map[string]int `json:"counts"`
	Reasons    []string       `json:"reasons"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
}

func stagesOf(v *progress.View) (*string, []stageView) {
	out := []stageView{}
	if v == nil {
		return nil, out
	}
	for _, s := range v.Stages {
		out = append(out, stageView{Key: s.Key, State: string(s.State), Planned: s.Planned, Finished: s.Finished, Succeeded: s.Succeeded,
			Counts: s.Counts, Reasons: s.Reasons, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt})
	}
	detail := string(v.Detail)
	return &detail, out
}

type activityRunView struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	RequestedAt time.Time `json:"requested_at"`
}

type reportRefView struct {
	RunID       string    `json:"run_id"`
	Revision    int       `json:"revision"`
	PublishedAt time.Time `json:"published_at"`
}

// activityView is the project's representative run and its stages. detail is
// null exactly when run is null (no run recorded).
type activityView struct {
	Run             *activityRunView `json:"run"`
	OtherActiveRuns int64            `json:"other_active_runs"`
	Detail          *string          `json:"detail"`
	Stages          []stageView      `json:"stages"`
	WaitingReason   *string          `json:"waiting_reason"`
	LastReport      *reportRefView   `json:"last_report"`
	LastEventAt     *time.Time       `json:"last_event_at"`
	CheckedAt       time.Time        `json:"checked_at"`
}

func activityOf(a postgres.Activity) activityView {
	v := activityView{OtherActiveRuns: a.OtherActiveRuns, WaitingReason: a.WaitingReason, LastEventAt: a.LastEventAt, CheckedAt: a.CheckedAt}
	v.Detail, v.Stages = stagesOf(a.Stages)
	if a.Run != nil {
		v.Run = &activityRunView{ID: a.Run.ID, Status: string(a.Run.Status), RequestedAt: a.Run.RequestedAt}
	}
	if r := a.LastReport; r != nil {
		v.LastReport = &reportRefView{RunID: r.RunID, Revision: r.Revision, PublishedAt: r.PublishedAt}
	}
	return v
}

// projectGaps lists the product capabilities a project view cannot claim.
// The source declaration is never verified by registration.
// execution_unavailable is a structured, global fact: no enrolled, unrevoked
// node exists. Whether a specific run can be placed (capabilities, grants,
// capacity) is reported per run as waiting_reason. report_unavailable is added
// per project by viewOf until one of its runs has a published report.
func projectGaps(executionAvailable bool) []string {
	gaps := []string{string(domain.GapSourceUnverified)}
	if !executionAvailable {
		gaps = append(gaps, string(domain.GapExecutionUnavailable))
	}
	return gaps
}

// executionGaps reads the global execution fact for a request.
func (s *Server) executionGaps(q *request) ([]string, bool) {
	ctx, cancel := q.ctx()
	defer cancel()
	ok, err := s.store.NodesEnrolled(ctx)
	if err != nil {
		s.storeError(q, err)
		return nil, false
	}
	return projectGaps(ok), true
}

func viewOf(p postgres.ProjectSummary, gaps []string) projectView {
	gaps = append([]string{}, gaps...)
	if !p.HasReport {
		gaps = append(gaps, string(domain.GapReportUnavailable))
	}
	v := projectView{ID: p.ID, Name: p.Name, Source: p.Source, Revision: p.Revision,
		SourceStatus: "declared_unverified", CapabilityGaps: gaps, Activity: activityOf(p.Activity)}
	if v.Source == nil {
		v.Source = json.RawMessage("null")
	}
	if p.LastRun != nil {
		v.LastRun = &runRef{p.LastRun.ID, string(p.LastRun.Status)}
	}
	return v
}

func (s *Server) createProject(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Name          *string         `json:"name"`
		Source        json.RawMessage `json:"source"`
		Description   *string         `json:"description"`
		AttachmentIDs []string        `json:"attachment_ids"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Source == nil || string(in.Source) == "null" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a project object with a source", false)
		return
	}
	if len(in.AttachmentIDs) > 0 {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "ATTACHMENTS_UNSUPPORTED", "attachments are not supported yet", false)
		return
	}
	src, err := domain.ParseSource(in.Source)
	if err != nil {
		s.storeError(q, err)
		return
	}
	reg := postgres.ProjectRegistration{Source: src}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "name must not be blank", false)
			return
		}
		reg.Name = *in.Name
	}
	if in.Description != nil {
		reg.Description = *in.Description
	}
	ctx, cancel := q.ctx()
	defer cancel()
	p, err := s.store.RegisterProject(ctx, q.session.UserID, reg)
	if err != nil {
		s.storeError(q, err)
		return
	}
	var cfg struct {
		Source json.RawMessage `json:"source"`
	}
	_ = json.Unmarshal(p.Config, &cfg)
	gaps, ok := s.executionGaps(q)
	if !ok {
		return
	}
	q.w.Header().Set("Location", apiPrefix+"/projects/"+p.ID)
	// A new project has no run: an empty activity checked now (server clock).
	created := postgres.ProjectSummary{ID: p.ID, Name: p.Name, Revision: p.Revision, Source: cfg.Source,
		Activity: postgres.Activity{CheckedAt: time.Now().UTC()}}
	writeJSON(q.w, http.StatusCreated, viewOf(created, gaps))
}

func (s *Server) listProjects(q *request) {
	query := q.r.URL.Query()
	limit := 20
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1..100", false)
			return
		}
		limit = n
	}
	cursor := query.Get("cursor")
	if len(cursor) > 400 {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "invalid cursor", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	page, err := s.store.ListProjects(ctx, q.session.UserID, cursor, limit)
	if err != nil {
		if errors.Is(err, domain.ErrInvalid) {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "invalid cursor", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	gaps, ok := s.executionGaps(q)
	if !ok {
		return
	}
	items := make([]projectView, 0, len(page.Items))
	for _, p := range page.Items {
		items = append(items, viewOf(p, gaps))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) getProject(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	p, err := s.store.GetProjectSummary(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	gaps, ok := s.executionGaps(q)
	if !ok {
		return
	}
	writeJSON(q.w, http.StatusOK, viewOf(p, gaps))
}

// --- runs ------------------------------------------------------------------

type runAck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	URL     string `json:"url"`
}

func ackOf(r postgres.Run) runAck {
	return runAck{r.ID, string(r.Status), r.Version, apiPrefix + "/runs/" + r.ID}
}

func validIdempotencyKey(k string) bool {
	if k == "" || len(k) > maxIdempotency {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func (s *Server) createRun(q *request) {
	key := q.r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must be 1..200 visible ASCII characters", false)
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	var req domain.RunRequest
	if domain.DecodeStrict(body, &req) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a run request object", false)
		return
	}
	if err := req.Validate(); err != nil {
		s.storeError(q, err)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	run, err := s.store.Enqueue(ctx, q.session.UserID, q.params[0], key, body)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeError(q.w, q.r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with a different body", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	ack := ackOf(run)
	q.w.Header().Set("Location", ack.URL)
	writeJSON(q.w, http.StatusAccepted, ack)
}

// progressView keeps the R0 fields (stage is the run status), adds the W15
// stage view computed exactly like the project activity and the W22 plan
// revision and weighted estimate (progress.Plan, same computation).
type progressView struct {
	Stage            string           `json:"stage"`
	PlanRevision     *int             `json:"plan_revision"`
	CompletedTasks   int64            `json:"completed_tasks"`
	TotalTasks       *int64           `json:"total_tasks"`
	EstimatedPercent *float64         `json:"estimated_percent"`
	PlannedWeight    *int             `json:"planned_weight"`
	FinishedWeight   *int             `json:"finished_weight"`
	WeightBasis      *string          `json:"weight_basis"`
	PlanChanges      []planChangeView `json:"plan_changes"`
	WaitingReason    *string          `json:"waiting_reason"`
	Detail           string           `json:"detail"`
	Stages           []stageView      `json:"stages"`
	LastEventAt      *time.Time       `json:"last_event_at"`
	CheckedAt        time.Time        `json:"checked_at"`
}

// planChangeView explains one plan revision after the first: which task
// (stage, weight) was added after the plan was fixed and the denominator
// after it.
type planChangeView struct {
	Revision      int        `json:"revision"`
	Reason        string     `json:"reason"`
	Stage         *string    `json:"stage"`
	Weight        int        `json:"weight"`
	PlannedWeight int        `json:"planned_weight"`
	RecordedAt    *time.Time `json:"recorded_at"`
}

// triggerView says why a run exists: a manual request or a schedule
// occurrence (W22).
type triggerView struct {
	Kind         string     `json:"kind"`
	ScheduleID   *string    `json:"schedule_id,omitempty"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
}

type runView struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id"`
	Status         string         `json:"status"`
	Version        int64          `json:"version"`
	EventSequence  int64          `json:"event_sequence"`
	Trigger        triggerView    `json:"trigger"`
	SnapshotID     *string        `json:"snapshot_id"`
	QualityVerdict *string        `json:"quality_verdict"`
	Report         *runReportView `json:"report"`
	Progress       progressView   `json:"progress"`
}

// runReportView points at the newest published report revision. Its quality
// verdict comes from that stored report, never from the run status alone.
type runReportView struct {
	Revision int    `json:"revision"`
	Status   string `json:"status"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url"`
}

func (s *Server) getRun(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	v, err := s.store.GetRunView(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, runViewOf(v))
}

// runViewOf is the GET /runs/{id} body; the SSE run.view event carries the
// same document.
func runViewOf(v postgres.RunView) runView {
	out := runView{ID: v.ID, ProjectID: v.ProjectID, Status: string(v.Status), Version: v.Version, EventSequence: v.EventSequence, SnapshotID: v.SnapshotID,
		Trigger:  triggerView{Kind: postgres.TriggerManual},
		Progress: progressView{Stage: string(v.Status), CompletedTasks: v.CompletedTasks, LastEventAt: v.Stages.LastEventAt, CheckedAt: v.Stages.CheckedAt}}
	if o := v.Origin; o != nil {
		id, at := o.ScheduleID, o.ScheduledFor
		out.Trigger = triggerView{Kind: postgres.TriggerSchedule, ScheduleID: &id, ScheduledFor: &at}
	}
	detail, stages := stagesOf(&v.Stages.View)
	out.Progress.Detail, out.Progress.Stages = *detail, stages
	plan := v.Stages.View.Plan
	out.Progress.PlanRevision, out.Progress.EstimatedPercent, out.Progress.PlannedWeight = plan.Revision, plan.EstimatedPercent, plan.PlannedWeight
	out.Progress.PlanChanges = []planChangeView{}
	if plan.Revision != nil {
		finished, basis := plan.FinishedWeight, plan.WeightBasis
		out.Progress.FinishedWeight, out.Progress.WeightBasis = &finished, &basis
	}
	for _, c := range plan.Changes {
		var stage *string
		if c.Stage != "" {
			st := string(c.Stage)
			stage = &st
		}
		out.Progress.PlanChanges = append(out.Progress.PlanChanges, planChangeView{Revision: c.Revision, Reason: c.Reason, Stage: stage, Weight: c.Weight, PlannedWeight: c.PlannedWeight, RecordedAt: c.At})
	}
	if v.TotalTasks > 0 {
		total := v.TotalTasks
		out.Progress.TotalTasks = &total
	}
	if r := v.LatestReport; r != nil {
		verdict := r.QualityVerdict
		out.QualityVerdict = &verdict
		out.Report = &runReportView{Revision: r.Revision, Status: r.Status, SHA256: r.BodySHA256, URL: apiPrefix + "/runs/" + v.ID + "/report?revision=" + strconv.Itoa(r.Revision)}
	}
	// A queued run's reason is derived by the store from enrolled nodes,
	// capabilities, grants, snapshot locations and limits (never predicted).
	if v.Status == domain.RunQueued {
		out.Progress.WaitingReason = v.WaitingReason
	}
	return out
}

func (s *Server) cancelRun(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	run, changed, err := s.store.CancelRun(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	status := http.StatusOK
	if changed {
		status = http.StatusAccepted
	}
	writeJSON(q.w, status, ackOf(run))
}
