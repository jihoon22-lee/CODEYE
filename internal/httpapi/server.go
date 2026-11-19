// Package httpapi is the CODEYE control-plane HTTP API (user subset and
// node protocol) and the same-origin static web host. Without TLS it
// listens on loopback only; any other address requires TLS and a public
// origin (tls.go). Browser sessions come from single-use launch tokens or a
// username and password; node routes accept only a node Bearer secret
// (issued at enrollment) and reject any Origin header. User Bearer tokens
// are not implemented.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/reportsvc"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Store is the persistence the API needs. *postgres.Store implements it.
type Store interface {
	SchemaStatus(ctx context.Context) (postgres.SchemaState, error)
	ExchangeLaunchTokenFrom(ctx context.Context, client, tokenSHA256, sessionTokenSHA256 string, ttl time.Duration) (postgres.Session, error)
	PasswordLogin(ctx context.Context, in postgres.PasswordLogin) (postgres.Session, error)
	ChangePassword(ctx context.Context, in postgres.PasswordChange) (postgres.Session, error)
	SessionByTokenHash(ctx context.Context, tokenSHA256 string) (postgres.Session, error)
	RevokeSession(ctx context.Context, id string) error
	AcknowledgePasswordReset(ctx context.Context, user string) error
	// Accounts and access (administrators; the store re-checks the role).
	ListUsers(ctx context.Context, by postgres.Principal, cursor string, limit int) (postgres.UserPage, error)
	CreateAccount(ctx context.Context, by postgres.Principal, in postgres.NewAccount) (postgres.User, error)
	DisableAccount(ctx context.Context, by postgres.Principal, id string) (postgres.User, error)
	SetAdmin(ctx context.Context, by postgres.Principal, id string, admin bool) (postgres.User, error)
	ResetPassword(ctx context.Context, by postgres.Principal, id, tokenSHA256 string, ttl time.Duration) (postgres.User, error)
	ListSourceRootAccess(ctx context.Context, by postgres.Principal) ([]postgres.SourceRootAccess, error)
	GrantSourceRootBy(ctx context.Context, by postgres.Principal, root, user string) error
	RevokeSourceRootGrant(ctx context.Context, by postgres.Principal, root, user string) error
	ListProvidersBy(ctx context.Context, by postgres.Principal) ([]postgres.Provider, error)
	ListAudit(ctx context.Context, actor string, f postgres.AuditFilter) (postgres.AuditPage, error)
	ListProjectAudit(ctx context.Context, actor, project string, f postgres.AuditFilter) (postgres.AuditPage, error)
	RecordStaleRejection(ctx context.Context, node, attempt string, epoch int64, route string) error
	// Project membership (project managers).
	ListMembers(ctx context.Context, actor, project string) ([]postgres.Member, error)
	PutMember(ctx context.Context, actor, project, user string, role domain.Role) (postgres.Member, error)
	RemoveMember(ctx context.Context, actor, project, user string) error
	ListSourceRoots(ctx context.Context, actor string) ([]postgres.SourceRoot, error)
	ListProviders(ctx context.Context, actor string) ([]postgres.Provider, error)
	RegisterProject(ctx context.Context, actor string, in postgres.ProjectRegistration) (postgres.Project, error)
	ListProjects(ctx context.Context, actor, cursor string, limit int) (postgres.ProjectPage, error)
	GetProjectSummary(ctx context.Context, actor, id string) (postgres.ProjectSummary, error)
	Enqueue(ctx context.Context, actor, project, key string, body json.RawMessage) (postgres.Run, error)
	GetRunView(ctx context.Context, actor, id string) (postgres.RunView, error)
	CancelRun(ctx context.Context, actor, id string) (postgres.Run, bool, error)
	// Run event stream and schedules.
	RunEvents(ctx context.Context, actor, id string, after int64, limit int) (postgres.EventPage, error)
	ListSchedules(ctx context.Context, actor, project string) ([]postgres.Schedule, bool, error)
	CreateSchedule(ctx context.Context, actor, project, key string, in postgres.ScheduleInput) (postgres.Schedule, error)
	UpdateSchedule(ctx context.Context, actor, project, id string, expected int64, p postgres.SchedulePatch) (postgres.Schedule, error)
	DeleteSchedule(ctx context.Context, actor, project, id string, expected int64) error
	MaterializeSchedules(ctx context.Context) ([]postgres.ScheduleDecision, error)

	// Node protocol.
	EnrollNode(ctx context.Context, in postgres.Enrollment) (postgres.NodeIdentity, error)
	AuthenticateNode(ctx context.Context, secretSHA256 string) (postgres.NodeIdentity, error)
	NodeHeartbeat(ctx context.Context, node string, in nodeproto.NodeHeartbeatRequest) (nodeproto.NodeHeartbeatResponse, error)
	ClaimAttempt(ctx context.Context, node, worker, key string) (postgres.ClaimResult, error)
	RenewAttempt(ctx context.Context, node string, o postgres.Ownership) (nodeproto.AttemptHeartbeatResponse, error)
	RecordAttemptEvent(ctx context.Context, node string, o postgres.Ownership, ev postgres.AttemptEvent) (nodeproto.EventResponse, error)
	CheckArtifactLease(ctx context.Context, node string, o postgres.Ownership, client string) error
	FinalizeArtifact(ctx context.Context, node string, o postgres.Ownership, in postgres.ArtifactUpload) (nodeproto.ArtifactResponse, error)
	CompleteAttempt(ctx context.Context, node string, o postgres.Ownership, c postgres.Completion) (postgres.Run, error)
	ConfirmCleanupNode(ctx context.Context, node string, o postgres.Ownership) (postgres.Run, error)
	SweepExpiredAttempts(ctx context.Context) (int, error)
	// Autoscaler.
	ReconcilePools(ctx context.Context) ([]postgres.PoolReconcile, error)
	NodesEnrolled(ctx context.Context) (bool, error)

	// Reports.
	ExportReport(ctx context.Context, actor, runID string, revision int, ex postgres.ReportExport) (postgres.ReportRecord, error)
	ListReports(ctx context.Context, actor, runID string) ([]postgres.ReportSummary, error)
	reportsvc.Store
	// Comparison and finding reviews.
	LoadComparison(ctx context.Context, actor, runID string, revision int, baselineRun string, baselineRevision int) (postgres.ComparisonData, error)
	DefaultBaseline(ctx context.Context, actor, runID string) (*postgres.BaselineRef, error)
	SetFindingReview(ctx context.Context, actor, project string, in postgres.ReviewRequest) (postgres.FindingReview, bool, error)
	ListFindingReviews(ctx context.Context, actor, project, fingerprint, cursor string, limit int) (postgres.ReviewPage, error)
}

// Config configures the server.
type Config struct {
	// Listen needs a non-zero port. A non-loopback address requires TLS.
	Listen netip.AddrPort
	// TLSCertFile and TLSKeyFile (PEM) switch the server to HTTPS; the key
	// file must not be group or world accessible. PublicOrigin
	// (https://HOST[:PORT]) is then required: it is the only accepted Host
	// and Origin and the origin of login links.
	TLSCertFile, TLSKeyFile, PublicOrigin string
	// WebDir holds the built web app (index.html). If empty or missing, the
	// API still works and non-API paths answer 503.
	WebDir string
	// SessionTTL is the absolute session lifetime (default 12h).
	SessionTTL time.Duration
	// Logger receives one line per request; nil discards.
	Logger *slog.Logger
	// ArtifactDir is the absolute controller directory for attempt artifact
	// bytes. Empty disables artifact upload (503).
	ArtifactDir string
	// SweepInterval is how often Serve expires lapsed attempt leases
	// (default 5s; negative disables, for tests that drive it directly).
	SweepInterval time.Duration
	// ReconcileInterval is how often Serve runs the pool autoscaler
	// (docs/EXECUTION.md reconcile_seconds, default 5s; negative disables).
	ReconcileInterval time.Duration
	// PublishInterval is how often Serve reconciles pending reports (default
	// 2s; negative disables, for tests that drive the publisher directly).
	// Publication needs ArtifactDir.
	PublishInterval time.Duration
	// Stream configures GET /runs/{id}/events (SSE); zero fields take
	// the defaults of StreamConfig.
	Stream StreamConfig
}

// DefaultSessionTTL is the absolute browser session lifetime.
const DefaultSessionTTL = 12 * time.Hour

// Server is the HTTP handler.
type Server struct {
	store Store
	hosts map[string]bool
	// tls is non-nil when serving HTTPS; origin is then the public origin.
	tls        *tls.Config
	origin     string
	cookieName string
	sessionTTL time.Duration
	web        *os.Root
	log        *slog.Logger
	routes     []route
	artifacts  *artifactstore.Store
	sweepEvery time.Duration
	scaleEvery time.Duration

	publisher    *reportsvc.Publisher
	publishEvery time.Duration
	publishKick  chan struct{}

	stream StreamConfig
	// streams counts open event streams in total and per session user (a
	// per-process connection limit, not scheduling state); stopStreams ends
	// them before a graceful shutdown.
	streamsMu   sync.Mutex
	streams     int32
	userStreams map[string]int32
	stopStreams chan struct{}
	stopOnce    sync.Once
}

// ParseListen parses an IP:port listen address with a non-zero port. New
// decides whether the address needs TLS.
func ParseListen(s string) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("listen address must be IP:port: %w", err)
	}
	if ap.Port() == 0 {
		return netip.AddrPort{}, errors.New("listen address must have a non-zero port")
	}
	return ap, nil
}

// New builds the server. Without TLS the listen address must be loopback
// (ErrTLSRequired otherwise); with TLS the certificate, key and public
// origin must all be given and valid.
func New(cfg Config, store Store) (*Server, error) {
	if !cfg.Listen.IsValid() || cfg.Listen.Port() == 0 {
		return nil, errors.New("httpapi: listen address needs a non-zero port")
	}
	if store == nil {
		return nil, errors.New("httpapi: nil store")
	}
	port := strconv.Itoa(int(cfg.Listen.Port()))
	s := &Server{
		store:      store,
		cookieName: sessionCookie,
		hosts: map[string]bool{
			"127.0.0.1:" + port: true,
			"localhost:" + port: true,
			"[::1]:" + port:     true,
		},
		sessionTTL:  cfg.SessionTTL,
		log:         cfg.Logger,
		stream:      cfg.Stream.withDefaults(),
		stopStreams: make(chan struct{}),
		userStreams: map[string]int32{},
	}
	if s.sessionTTL <= 0 {
		s.sessionTTL = DefaultSessionTTL
	}
	switch {
	case cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" || cfg.PublicOrigin != "":
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" || cfg.PublicOrigin == "" {
			return nil, errors.New("TLS needs all of --tls-cert, --tls-key and --public-origin")
		}
		origin, hosts, err := ParsePublicOrigin(cfg.PublicOrigin)
		if err != nil {
			return nil, err
		}
		tc, err := loadTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, err
		}
		s.tls, s.origin, s.cookieName = tc, origin, secureSessionCookie
		s.hosts = map[string]bool{}
		for _, h := range hosts {
			s.hosts[h] = true
		}
	case !cfg.Listen.Addr().IsLoopback():
		return nil, ErrTLSRequired
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if cfg.ArtifactDir != "" {
		a, err := artifactstore.Open(cfg.ArtifactDir)
		if err != nil {
			return nil, err
		}
		s.artifacts = a
	}
	s.sweepEvery = cfg.SweepInterval
	if s.sweepEvery == 0 {
		s.sweepEvery = 5 * time.Second
	}
	s.scaleEvery = cfg.ReconcileInterval
	if s.scaleEvery == 0 {
		s.scaleEvery = 5 * time.Second
	}
	s.publishEvery = cfg.PublishInterval
	if s.publishEvery == 0 {
		s.publishEvery = 2 * time.Second
	}
	if s.artifacts != nil {
		s.publisher = &reportsvc.Publisher{Store: store, Artifacts: s.artifacts, Registry: report.DefaultRegistry(), Log: s.log}
		s.publishKick = make(chan struct{}, 1)
	}
	if cfg.WebDir != "" {
		if root, err := os.OpenRoot(cfg.WebDir); err == nil {
			if f, err := root.Open("index.html"); err == nil {
				_ = f.Close()
				s.web = root
			} else {
				_ = root.Close()
			}
		}
	}
	s.routes = s.apiRoutes()
	return s, nil
}

// StopStreams ends every open event stream (idempotent). Serve calls it when
// shutdown begins so open streams do not hold the graceful shutdown.
func (s *Server) StopStreams() { s.stopOnce.Do(func() { close(s.stopStreams) }) }

// Close ends open event streams and releases the web root.
func (s *Server) Close() error {
	s.StopStreams()
	if s.web != nil {
		return s.web.Close()
	}
	return nil
}

// Serve runs the HTTP server on ln until ctx is done, then shuts down
// gracefully (in-flight requests get up to 10 seconds).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	ln, err := s.guardListener(ln)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	// Event streams clear their own read deadline and extend the write
	// deadline per write; they end here when shutdown begins.
	srv.RegisterOnShutdown(s.StopStreams)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	if s.sweepEvery > 0 {
		go s.sweepLoop(sweepCtx)
	}
	if s.scaleEvery > 0 {
		go s.scaleLoop(sweepCtx)
	}
	if s.publisher != nil && s.publishEvery > 0 {
		go s.publishLoop(sweepCtx)
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type ctxKey int

const requestIDKey ctxKey = 1

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "req_" + hex.EncodeToString(b[:])
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (w *recorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection (flush and
// deadlines for event streams).
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; connect-src 'self'"

// ServeHTTP applies request ID, security headers and the Host allowlist, then
// dispatches to the API or the static web app.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rid := newRequestID()
	r = r.WithContext(postgres.WithRequestID(context.WithValue(r.Context(), requestIDKey, rid), rid))
	rec := &recorder{ResponseWriter: w}
	h := rec.Header()
	h.Set("X-Request-ID", rid)
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if s.tls != nil {
		h.Set("Strict-Transport-Security", hstsValue)
	}
	route := "static"
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("http panic", "request_id", rid, "panic_type", fmt.Sprintf("%T", p))
			if rec.status == 0 {
				writeError(rec, r, http.StatusInternalServerError, "INTERNAL", "internal error", false)
			}
		}
		s.log.Info("http", "request_id", rid, "method", r.Method, "route", route, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	}()
	if !s.hosts[strings.ToLower(r.Host)] {
		route = "host_rejected"
		writeError(rec, r, http.StatusForbidden, "HOST_REJECTED", "host is not allowed", false)
		return
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		h.Set("Cache-Control", "no-store")
		route = s.serveAPI(rec, r)
		return
	}
	s.serveStatic(rec, r)
}

// errorBody is the API error envelope.
type errorBody struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Retryable bool           `json:"retryable"`
		RequestID string         `json:"request_id"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	writeErrorDetails(w, r, status, code, message, retryable, map[string]any{})
}

// writeErrorDetails is writeError with structured details (never secrets).
func writeErrorDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool, details map[string]any) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Retryable = retryable
	body.Error.RequestID = requestID(r)
	body.Error.Details = details
	writeJSON(w, status, body)
}
