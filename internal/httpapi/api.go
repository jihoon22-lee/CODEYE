package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
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

// secureSessionCookie is the session cookie name when the server speaks TLS:
// the __Host- prefix makes browsers accept it only with Secure, Path=/ and
// no Domain.
const secureSessionCookie = "__Host-codeye_session"

type request struct {
	w       http.ResponseWriter
	r       *http.Request
	params  []string
	session postgres.Session
	cookie  string
	node    postgres.NodeIdentity
	// route is the matched route name; epoch is the lease epoch a node
	// claimed (for the stale-write audit).
	route string
	epoch int64
}

// auditNodeProtocol marks node-protocol writes that are not audit events:
// their effects are run, attempt and task events in the run history.
const auditNodeProtocol = "node_protocol"

type route struct {
	method string
	parts  []string // "{id}" segments must be canonical UUIDs
	name   string
	auth   bool
	body   bool
	// admin requires an enabled administrator (403 FORBIDDEN otherwise).
	admin bool
	// pending lets a session whose user must change the password through
	// (session read, logout, password change); every other route answers 403
	// PASSWORD_CHANGE_REQUIRED.
	pending bool
	// audit lists the audit actions a successful request records (one of
	// them), or auditNodeProtocol. Every state-changing route declares it;
	// the isolation test checks the row exists for the request.
	audit []string
	// node marks node-protocol routes: nodePublic (enroll) or nodeAuth
	// (Bearer node secret). They never read cookies and reject Origin.
	node   nodeAuthKind
	handle func(*Server, *request)
}

func (rt route) admins() route                  { rt.admin = true; return rt }
func (rt route) allowPending() route            { rt.pending = true; return rt }
func (rt route) audits(actions ...string) route { rt.audit = actions; return rt }

func (s *Server) apiRoutes() []route {
	r := func(method, path, name string, auth, body bool, h func(*Server, *request)) route {
		return route{method: method, parts: strings.Split(strings.TrimPrefix(path, "/"), "/"), name: name, auth: auth, body: body, handle: h}
	}
	return append([]route{
		r("GET", "health", "health", false, false, (*Server).health),
		r("GET", "ready", "ready", false, false, (*Server).ready),
		r("POST", "session", "session.create", false, true, (*Server).createSession).audits(postgres.AuditLoginSucceeded),
		r("GET", "session", "session.get", true, false, (*Server).getSession).allowPending(),
		r("DELETE", "session", "session.delete", true, false, (*Server).deleteSession).allowPending().audits(postgres.AuditLogout),
		r("POST", "session/password", "session.password", true, true, (*Server).changePassword).allowPending().audits(postgres.AuditPasswordChanged),
		r("GET", "source-roots", "source_roots.list", true, false, (*Server).listSourceRoots),
		r("GET", "providers", "providers.list", true, false, (*Server).listProviders),
		r("GET", "projects", "projects.list", true, false, (*Server).listProjects),
		r("POST", "projects", "projects.create", true, true, (*Server).createProject).audits(postgres.AuditProjectRegistered),
		r("GET", "projects/{id}", "projects.get", true, false, (*Server).getProject),
		r("GET", "projects/{id}/members", "members.list", true, false, (*Server).listMembers),
		r("POST", "projects/{id}/members", "members.add", true, true, (*Server).addMember).audits(postgres.AuditMemberSet),
		r("PUT", "projects/{id}/members/{id}", "members.put", true, true, (*Server).putMember).audits(postgres.AuditMemberSet),
		r("DELETE", "projects/{id}/members/{id}", "members.delete", true, false, (*Server).deleteMember).audits(postgres.AuditMemberRemoved),
		r("GET", "projects/{id}/audit", "projects.audit", true, false, (*Server).projectAudit),
		r("POST", "projects/{id}/runs", "runs.create", true, true, (*Server).createRun).audits(postgres.AuditRunRequested),
		r("GET", "projects/{id}/schedules", "schedules.list", true, false, (*Server).listSchedules),
		r("POST", "projects/{id}/schedules", "schedules.create", true, true, (*Server).createSchedule).audits(postgres.AuditScheduleCreated),
		r("PATCH", "projects/{id}/schedules/{id}", "schedules.update", true, true, (*Server).updateSchedule).audits(postgres.AuditScheduleUpdated),
		r("DELETE", "projects/{id}/schedules/{id}", "schedules.delete", true, false, (*Server).deleteSchedule).audits(postgres.AuditScheduleDeleted),
		r("GET", "runs/{id}", "runs.get", true, false, (*Server).getRun),
		r("GET", "runs/{id}/events", "runs.events", true, false, (*Server).streamRunEvents),
		r("POST", "runs/{id}/cancel", "runs.cancel", true, false, (*Server).cancelRun).audits(postgres.AuditRunCancelRequested),
		r("GET", "runs/{id}/report", "runs.report", true, false, (*Server).getReport).audits(postgres.AuditReportExported),
		r("GET", "runs/{id}/reports", "runs.reports", true, false, (*Server).listReports),
		r("GET", "runs/{id}/comparison", "runs.comparison", true, false, (*Server).getComparison),
		r("GET", "projects/{id}/finding-reviews", "finding_reviews.list", true, false, (*Server).listFindingReviews),
		r("POST", "projects/{id}/finding-reviews", "finding_reviews.create", true, true, (*Server).setFindingReview).audits(postgres.AuditFindingReviewRecorded),
		r("GET", "admin/users", "admin.users.list", true, false, (*Server).adminListUsers).admins(),
		r("POST", "admin/users", "admin.users.create", true, true, (*Server).adminCreateUser).admins().audits(postgres.AuditUserCreated),
		r("POST", "admin/users/{id}/disable", "admin.users.disable", true, false, (*Server).adminDisableUser).admins().audits(postgres.AuditUserDisabled),
		r("PUT", "admin/users/{id}/admin", "admin.users.set_admin", true, true, (*Server).adminSetAdmin).admins().audits(postgres.AuditAdminGranted, postgres.AuditAdminRevoked),
		r("POST", "admin/users/{id}/password-reset", "admin.users.reset_password", true, false, (*Server).adminResetPassword).admins().audits(postgres.AuditPasswordReset),
		r("GET", "admin/source-roots", "admin.source_roots.list", true, false, (*Server).adminListSourceRoots).admins(),
		r("PUT", "admin/source-roots/{id}/grants/{id}", "admin.source_roots.grant", true, false, (*Server).adminGrantSourceRoot).admins().audits(postgres.AuditSourceRootGranted),
		r("DELETE", "admin/source-roots/{id}/grants/{id}", "admin.source_roots.revoke", true, false, (*Server).adminRevokeSourceRoot).admins().audits(postgres.AuditSourceRootRevoked),
		r("GET", "admin/providers", "admin.providers.list", true, false, (*Server).adminListProviders).admins(),
		r("GET", "audit", "audit.list", true, false, (*Server).listAudit).admins(),
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
		s.dispatch(rt, &request{w: w, r: r, params: params, route: rt.name})
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

// dispatch applies, in order: the node-route split, the Origin check for
// every state-changing user request (including POST /session), the session
// cookie, CSRF, the administrator and pending-password gates, and the body
// media type. Nothing reaches a handler around these checks.
func (s *Server) dispatch(rt route, q *request) {
	w, r := q.w, q.r
	if rt.node != nodeNone {
		s.dispatchNode(rt, q)
		return
	}
	if unsafeMethod(r.Method) {
		// Cross-site and DNS-rebinding defence: the browser-sent Origin must be
		// exactly this server's origin (Host was already allowlisted).
		if origin := r.Header.Get("Origin"); origin == "" || origin != s.requestOrigin(r) {
			writeError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "origin is not allowed", false)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			writeError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "cross-site request is not allowed", false)
			return
		}
	}
	if rt.auth {
		c, err := r.Cookie(s.cookieName)
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
		// Audit rows of a session that began from an administrator's login
		// link name that administrator.
		q.r = q.r.WithContext(postgres.WithSession(q.r.Context(), sess))
		if unsafeMethod(r.Method) {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(csrfToken(c.Value))) != 1 {
				writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "missing or invalid CSRF token", false)
				return
			}
		}
		if sess.MustChangePassword && !rt.pending {
			writeError(w, r, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", "the password must be changed first", false)
			return
		}
		if rt.admin && !sess.IsAdmin {
			// The store re-checks the role in the same transaction as the work.
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "administrator role required", false)
			return
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
	var throttled *postgres.ThrottledError
	switch {
	case errors.As(err, &throttled):
		secs := throttled.RetryAfterSeconds()
		q.w.Header().Set("Retry-After", itoa(secs))
		writeErrorDetails(q.w, q.r, http.StatusTooManyRequests, "RATE_LIMITED", "too many failed attempts; retry later", true, map[string]any{"retry_after_seconds": secs})
	case errors.Is(err, domain.ErrNotFound):
		writeError(q.w, q.r, http.StatusNotFound, "NOT_FOUND", "not found", false)
	case errors.Is(err, domain.ErrForbidden):
		writeError(q.w, q.r, http.StatusForbidden, "FORBIDDEN", "insufficient role", false)
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
