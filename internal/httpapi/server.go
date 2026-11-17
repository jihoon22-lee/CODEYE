// Package httpapi is the CODEYE control-plane HTTP API (W03 subset) and the
// same-origin static web host. It listens on loopback only and authenticates
// browser sessions created from single-use launch tokens. Bearer tokens, LAN
// exposure and TLS are not implemented.
package httpapi

import (
	"context"
	"crypto/rand"
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
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Store is the persistence the API needs. *postgres.Store implements it.
type Store interface {
	SchemaStatus(ctx context.Context) (postgres.SchemaState, error)
	ExchangeLaunchToken(ctx context.Context, tokenSHA256, sessionTokenSHA256 string, ttl time.Duration) (postgres.Session, error)
	SessionByTokenHash(ctx context.Context, tokenSHA256 string) (postgres.Session, error)
	RevokeSession(ctx context.Context, id string) error
	ListSourceRoots(ctx context.Context, actor string) ([]postgres.SourceRoot, error)
	ListProviders(ctx context.Context, actor string) ([]postgres.Provider, error)
	RegisterProject(ctx context.Context, actor string, in postgres.ProjectRegistration) (postgres.Project, error)
	ListProjects(ctx context.Context, actor, cursor string, limit int) (postgres.ProjectPage, error)
	GetProjectSummary(ctx context.Context, actor, id string) (postgres.ProjectSummary, error)
	Enqueue(ctx context.Context, actor, project, key string, body json.RawMessage) (postgres.Run, error)
	GetRunView(ctx context.Context, actor, id string) (postgres.RunView, error)
	CancelRun(ctx context.Context, actor, id string) (postgres.Run, bool, error)
}

// Config configures the server.
type Config struct {
	// Listen must be a loopback address with a non-zero port.
	Listen netip.AddrPort
	// WebDir holds the built web app (index.html). If empty or missing, the
	// API still works and non-API paths answer 503.
	WebDir string
	// SessionTTL is the absolute session lifetime (default 12h).
	SessionTTL time.Duration
	// Logger receives one line per request; nil discards.
	Logger *slog.Logger
}

// DefaultSessionTTL is the absolute browser session lifetime.
const DefaultSessionTTL = 12 * time.Hour

// Server is the HTTP handler.
type Server struct {
	store      Store
	hosts      map[string]bool
	sessionTTL time.Duration
	web        *os.Root
	log        *slog.Logger
	routes     []route
}

// ParseListen parses and validates a loopback listen address.
func ParseListen(s string) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("listen address must be IP:port: %w", err)
	}
	if !ap.Addr().IsLoopback() || ap.Port() == 0 {
		return netip.AddrPort{}, errors.New("listen address must be a loopback IP with a non-zero port; LAN exposure is not supported")
	}
	return ap, nil
}

// New builds the server. It fails for a non-loopback listen address.
func New(cfg Config, store Store) (*Server, error) {
	if !cfg.Listen.Addr().IsLoopback() || cfg.Listen.Port() == 0 {
		return nil, errors.New("httpapi: listen address must be loopback with a non-zero port")
	}
	if store == nil {
		return nil, errors.New("httpapi: nil store")
	}
	port := strconv.Itoa(int(cfg.Listen.Port()))
	s := &Server{
		store: store,
		hosts: map[string]bool{
			"127.0.0.1:" + port: true,
			"localhost:" + port: true,
			"[::1]:" + port:     true,
		},
		sessionTTL: cfg.SessionTTL,
		log:        cfg.Logger,
	}
	if s.sessionTTL <= 0 {
		s.sessionTTL = DefaultSessionTTL
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
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

// Close releases the web root.
func (s *Server) Close() error {
	if s.web != nil {
		return s.web.Close()
	}
	return nil
}

// Serve runs the HTTP server on ln until ctx is done, then shuts down
// gracefully (in-flight requests get up to 10 seconds).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
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

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; connect-src 'self'"

// ServeHTTP applies request ID, security headers and the Host allowlist, then
// dispatches to the API or the static web app.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rid := newRequestID()
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey, rid))
	rec := &recorder{ResponseWriter: w}
	h := rec.Header()
	h.Set("X-Request-ID", rid)
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
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
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Retryable = retryable
	body.Error.RequestID = requestID(r)
	body.Error.Details = map[string]any{}
	writeJSON(w, status, body)
}
