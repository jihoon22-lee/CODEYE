// Package httpapi is the CODEYE control-plane HTTP API (user subset and
// node protocol) and the same-origin static web host. Without TLS it
// listens on loopback only unless the operator chose plain HTTP; any other
// address needs TLS or that explicit choice, and has a public origin
// (tls.go). Browser sessions come from single-use launch tokens or a
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/hostaddr"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/prcomment"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/reportsvc"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Store is the persistence the API needs. *postgres.Store implements it.
type Store interface {
	SchemaStatus(ctx context.Context) (postgres.SchemaState, error)
	ExchangeLaunchTokenFrom(ctx context.Context, client, tokenSHA256, sessionTokenSHA256 string, ttl time.Duration) (postgres.Session, error)
	RegisterAccount(ctx context.Context, in postgres.Registration) (postgres.Session, error)
	PasswordLogin(ctx context.Context, in postgres.PasswordLogin) (postgres.Session, error)
	ChangePassword(ctx context.Context, in postgres.PasswordChange) (postgres.Session, error)
	SessionByTokenHash(ctx context.Context, tokenSHA256 string) (postgres.Session, error)
	RevokeSession(ctx context.Context, id string) error
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
	AddMemberByUsername(ctx context.Context, actor, project, username string, role domain.Role) (postgres.Member, error)
	AddMemberByEmail(ctx context.Context, actor, project, email string, role domain.Role) (postgres.Member, error)
	RemoveMember(ctx context.Context, actor, project, user string) error
	ListSourceRoots(ctx context.Context, actor string) ([]postgres.SourceRoot, error)
	ListProviders(ctx context.Context, actor string) ([]postgres.Provider, error)
	ProviderForHost(ctx context.Context, host string) (postgres.Provider, error)
	RegisterProject(ctx context.Context, actor string, in postgres.ProjectRegistration) (postgres.Project, error)
	ListProjects(ctx context.Context, actor, cursor string, limit int) (postgres.ProjectPage, error)
	GetProjectSummary(ctx context.Context, actor, id string) (postgres.ProjectSummary, error)
	Enqueue(ctx context.Context, actor, project, key string, body json.RawMessage) (postgres.Run, error)
	EnqueuePullRequest(ctx context.Context, actor, project, key string, body json.RawMessage, pin domain.PullRequest) (postgres.Run, error)
	StoreWebhookDelivery(ctx context.Context, d postgres.WebhookDelivery) error
	ClaimWebhookDelivery(ctx context.Context) (postgres.WebhookDelivery, error)
	FinishWebhookDelivery(ctx context.Context, d postgres.WebhookDelivery, reason string, retry bool) error
	EnqueueWebhookPullRequest(ctx context.Context, d postgres.WebhookDelivery, pin domain.PullRequest) error
	GetRunView(ctx context.Context, actor, id string) (postgres.RunView, error)
	CancelRun(ctx context.Context, actor, id string) (postgres.Run, bool, error)
	SetProjectAnalysis(ctx context.Context, actor, id string, c domain.AnalysisChoices) (int64, error)
	RetryRun(ctx context.Context, actor, id string) (postgres.Run, string, error)
	ConfirmRecovery(ctx context.Context, by postgres.Principal, id string) (postgres.Recovery, error)
	SourceRootPath(ctx context.Context, id string) (string, error)
	LocalSourceForPath(ctx context.Context, path string) (domain.Source, error)
	// The shared service: folders and Git hosts the administrator adds, the
	// service switches, what they offer a user, and token audit rows.
	AddSourceRootBy(ctx context.Context, by postgres.Principal, label, path string) (postgres.SourceRootRecord, error)
	SetSourceRootEveryone(ctx context.Context, by postgres.Principal, root string, everyone bool) error
	AddProviderBy(ctx context.Context, by postgres.Principal, host string) (postgres.Provider, bool, error)
	GetServiceSettings(ctx context.Context) (postgres.ServiceSettings, error)
	SetServiceSettings(ctx context.Context, by postgres.Principal, p postgres.ServiceSettingsPatch) (postgres.ServiceSettings, error)
	RecordOpenCodeOperation(ctx context.Context, by postgres.Principal, operation string) error
	OfferFor(ctx context.Context, user string) (nodeproto.AnalysisOffer, error)
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
	// Pull-request comments.
	prcomment.Store
	PullRequestCommentPreview(ctx context.Context, actor, runID string, revision int) (postgres.CommentPreview, error)
	// Comparison and finding reviews.
	LoadComparison(ctx context.Context, actor, runID string, revision int, baselineRun string, baselineRevision int) (postgres.ComparisonData, error)
	DefaultBaseline(ctx context.Context, actor, runID string) (*postgres.BaselineRef, error)
	SetFindingReview(ctx context.Context, actor, project string, in postgres.ReviewRequest) (postgres.FindingReview, bool, error)
	ListFindingReviews(ctx context.Context, actor, project, fingerprint, cursor string, limit int) (postgres.ReviewPage, error)
}

// Config configures the server.
type Config struct {
	// Listen needs a non-zero port. A non-loopback address requires TLS or
	// PlainHTTP.
	Listen netip.AddrPort
	// TLSCertFile and TLSKeyFile (PEM) switch the server to HTTPS; the key
	// file must not be group or world accessible. At least one
	// PublicOrigins entry (https://HOST[:PORT]) is then required.
	TLSCertFile, TLSKeyFile string
	// PublicOrigins are the addresses other people open (https:// with TLS,
	// http:// with PlainHTTP), the first one the address to share. Their
	// Hosts are accepted besides this machine's own names and addresses on
	// the listen port (hostaddr.Machine): localhost, the hostname, any
	// loopback address and every interface address.
	PublicOrigins []string
	// PlainHTTP serves a non-loopback Listen without TLS, when the operator
	// says so (a trusted network): sign-in cookies then travel unencrypted.
	PlainHTTP bool
	// LocalListen is an optional second, loopback, plain-HTTP address (served
	// by ServeLocal) for the agent and the commands on the controller's own
	// machine when Listen is a public address.
	LocalListen netip.AddrPort
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
	// Instance is the installation's state directory (`codeye serve
	// --state-dir`, always set by `codeye up`): GET /setup reads the model,
	// Git and the detected tools from it, the platform GitHub credentials live in
	// it and no project may contain it. Nil serves the same API without
	// those (tokens cannot be stored).
	Instance *Instance
	// Machine answers which Hosts name this machine (nil: the real one,
	// hostaddr.SystemMachine; tests give a fixed one).
	Machine *hostaddr.Machine
}

// DefaultSessionTTL is the absolute browser session lifetime.
const DefaultSessionTTL = 12 * time.Hour

// ServiceSessionTTL is the session lifetime of the controller `codeye up`
// starts (`codeye serve --session-ttl 168h`): people sign in once a week.
const ServiceSessionTTL = 7 * 24 * time.Hour

// face is how one listener meets browsers: the Host values it accepts (the
// configured ones, and with ownNames those naming this machine on port),
// TLS, whether plain HTTP was chosen for a non-loopback address, and the
// session cookie name. A state-changing request's Origin must be
// scheme://Host of that very request.
type face struct {
	hosts map[string]bool
	// machine lists this machine's addresses (the service's addresses and
	// the 403 page); ownNames also accepts every one of them as Host, which
	// only a non-loopback listener needs (nobody else reaches a loopback
	// one, and a page resolving a name to 127.0.0.1 must not get in).
	machine    *hostaddr.Machine
	ownNames   bool
	port       uint16
	tls        *tls.Config
	plain      bool
	cookieName string
}

// Server is the HTTP handler.
type Server struct {
	store Store
	// pub is the face of Listen; local the face of LocalListen (nil without).
	pub, local *face
	// listen and origins are Config.Listen and the canonical PublicOrigins.
	listen     netip.AddrPort
	origins    []string
	sessionTTL time.Duration
	web        *os.Root
	log        *slog.Logger
	routes     []route
	artifacts  *artifactstore.Store
	sweepEvery time.Duration
	scaleEvery time.Duration

	publisher *reportsvc.Publisher
	// commenter posts the pull-request comments, each with the GitHub token
	// of the user who started the run.
	commenter    *prcomment.Poster
	publishEvery time.Duration
	publishKick  chan struct{}

	stream StreamConfig
	// inst is Config.Instance (nil without a state directory).
	inst *Instance
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

// loopbackFace accepts the loopback names of port.
func loopbackFace(port uint16) *face {
	p := strconv.Itoa(int(port))
	return &face{cookieName: sessionCookie, port: port, hosts: map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true, "[::1]:" + p: true}}
}

// New builds the server. Without TLS the listen address must be loopback
// unless PlainHTTP is set (ErrTLSRequired otherwise); with TLS the
// certificate, key and public origin must all be given and valid.
func New(cfg Config, store Store) (*Server, error) {
	if !cfg.Listen.IsValid() || cfg.Listen.Port() == 0 {
		return nil, errors.New("httpapi: listen address needs a non-zero port")
	}
	if store == nil {
		return nil, errors.New("httpapi: nil store")
	}
	s := &Server{
		store:       store,
		listen:      cfg.Listen,
		pub:         loopbackFace(cfg.Listen.Port()),
		sessionTTL:  cfg.SessionTTL,
		log:         cfg.Logger,
		stream:      cfg.Stream.withDefaults(),
		inst:        cfg.Instance,
		stopStreams: make(chan struct{}),
		userStreams: map[string]int32{},
	}
	if s.sessionTTL <= 0 {
		s.sessionTTL = DefaultSessionTTL
	}
	switch {
	case cfg.TLSCertFile != "" || cfg.TLSKeyFile != "":
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" || len(cfg.PublicOrigins) == 0 {
			return nil, errors.New("TLS needs all of --tls-cert, --tls-key and --public-origin")
		}
		if cfg.PlainHTTP {
			return nil, errors.New("--plain-http and TLS are alternatives")
		}
		tc, err := loadTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, err
		}
		s.pub = &face{tls: tc, cookieName: secureSessionCookie, hosts: map[string]bool{}}
	case cfg.PlainHTTP:
		s.pub = &face{plain: true, cookieName: sessionCookie, hosts: map[string]bool{}}
	case len(cfg.PublicOrigins) != 0:
		return nil, errors.New("--public-origin needs TLS (--tls-cert, --tls-key) or --plain-http")
	case !cfg.Listen.Addr().IsLoopback():
		return nil, ErrTLSRequired
	}
	s.pub.port = cfg.Listen.Port()
	s.pub.machine = hostaddr.SystemMachine()
	if cfg.Machine != nil {
		s.pub.machine = cfg.Machine
	}
	if cfg.Listen.Addr().IsLoopback() {
		// Loopback only: the loopback names, the listen address itself and
		// the configured origins.
		for h := range loopbackFace(cfg.Listen.Port()).hosts {
			s.pub.hosts[h] = true
		}
		s.pub.hosts[strings.ToLower(cfg.Listen.String())] = true
	} else {
		s.pub.ownNames = true
	}
	for _, o := range cfg.PublicOrigins {
		canon, hosts, err := parseOrigin(o, s.scheme())
		if err != nil {
			return nil, err
		}
		if !slices.Contains(s.origins, canon) {
			s.origins = append(s.origins, canon)
		}
		for _, h := range hosts {
			s.pub.hosts[h] = true
		}
	}
	if cfg.LocalListen.IsValid() {
		if !cfg.LocalListen.Addr().IsLoopback() || cfg.LocalListen.Port() == 0 {
			return nil, errors.New("the local listen address must be loopback with a port")
		}
		s.local = loopbackFace(cfg.LocalListen.Port())
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.inst == nil {
		s.inst = &Instance{}
	} else {
		inst := *s.inst
		s.inst = &inst
	}
	s.inst.protected = []string{cfg.ArtifactDir}
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
		s.commenter = &prcomment.Poster{Store: store, GitHub: s.platformGitHub, Log: s.log}
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

// httpServer is the HTTP server of one listener; f is its face.
func (s *Server) httpServer(f *face) *http.Server {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
		ConnContext:       func(ctx context.Context, _ net.Conn) context.Context { return context.WithValue(ctx, faceKey, f) },
	}
	// Event streams clear their own read deadline and extend the write
	// deadline per write; they end here when shutdown begins.
	srv.RegisterOnShutdown(s.StopStreams)
	return srv
}

// ServeLocal serves the LocalListen face on ln (a loopback listener) until
// ctx is done. Serve runs the background loops; this only answers requests.
func (s *Server) ServeLocal(ctx context.Context, ln net.Listener) error {
	if s.local == nil {
		return errors.New("httpapi: no local listen address configured")
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return errors.New("httpapi: the local listener must be loopback")
	}
	srv := s.httpServer(s.local)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
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

// Serve runs the HTTP server on ln until ctx is done, then shuts down
// gracefully (in-flight requests get up to 10 seconds).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	ln, err := s.guardListener(ln)
	if err != nil {
		return err
	}
	srv := s.httpServer(s.pub)
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
	if s.inst != nil && s.inst.StateDir != "" {
		go s.webhookLoop(sweepCtx)
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

const (
	requestIDKey ctxKey = 1
	// faceKey carries the *face of the listener a connection arrived on.
	faceKey ctxKey = 2
)

// faceOf is the face of r's listener (the public one for a handler used
// without Serve, as in tests).
func (s *Server) faceOf(r *http.Request) *face {
	if f, ok := r.Context().Value(faceKey).(*face); ok && f != nil {
		return f
	}
	return s.pub
}

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
	if s.faceOf(r).tls != nil {
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
	if !s.faceOf(r).accepts(r.Host) {
		route = "host_rejected"
		s.rejectHost(rec, r)
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
