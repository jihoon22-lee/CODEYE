package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

// Every test here runs against a real PostgreSQL in an isolated schema. Without
// CODEYE_TEST_DATABASE_URL the tests skip, which means NOT executed.

type env struct {
	t      *testing.T
	url    string
	schema string
	pool   *pgxpool.Pool
	store  *postgres.Store
	srv    *Server
	hs     *httptest.Server
	host   string
	logs   *syncBuffer
	// artifacts is the controller artifact directory (node uploads).
	artifacts string
	// stream is the event stream configuration start uses.
	stream StreamConfig
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newPool(t *testing.T, url, schema string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("CODEYE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CODEYE_TEST_DATABASE_URL not set: HTTP API integration test NOT executed")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("test DB connection failed")
	}
	schema := "httpapi_" + strings.ReplaceAll(domain.NewID(), "-", "")
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, url: url, schema: schema, logs: &syncBuffer{}}
	t.Cleanup(func() {
		if e.hs != nil {
			e.srv.StopStreams() // open event streams would hold Close
			e.hs.Close()
		}
		if e.pool != nil {
			e.pool.Close()
		}
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = conn.Close(context.Background())
	})
	e.start(t.TempDir())
	return e
}

// start (re)creates the pool, store and HTTP server on a fresh loopback port.
func (e *env) start(webDir string) {
	t := e.t
	if e.hs != nil {
		e.srv.StopStreams()
		e.hs.Close()
	}
	if e.pool != nil {
		e.pool.Close()
	}
	e.pool = newPool(t, e.url, e.schema)
	e.store = postgres.NewFromPool(e.pool)
	e.store.SetPasswordIterations(account.MinIterations)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(ln.Addr().String())
	if e.artifacts == "" {
		e.artifacts = filepath.Join(t.TempDir(), "artifacts")
	}
	srv, err := New(Config{Listen: ap, WebDir: webDir, Logger: slog.New(slog.NewJSONHandler(e.logs, nil)), ArtifactDir: e.artifacts, SweepInterval: -1, Stream: e.stream}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	e.srv = srv
	e.hs = &httptest.Server{Listener: ln, Config: &http.Server{Handler: srv}}
	e.hs.Start()
	e.host = ap.String()
}

type opts struct {
	host, origin, cookie, csrf, ctype, idem, fetchSite string
	ifMatch, ifNoneMatch                               string
	noOrigin                                           bool
}

type resp struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

func (e *env) do(method, path string, body any, o opts) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.hs.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = e.host
	if o.host != "" {
		req.Host = o.host
	}
	if unsafeMethod(method) && !o.noOrigin {
		origin := "http://" + req.Host
		if o.origin != "" {
			origin = o.origin
		}
		req.Header.Set("Origin", origin)
	}
	if rd != nil {
		ct := "application/json"
		if o.ctype != "" {
			ct = o.ctype
		}
		req.Header.Set("Content-Type", ct)
	}
	if o.cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: o.cookie})
	}
	if o.csrf != "" {
		req.Header.Set(csrfHeader, o.csrf)
	}
	if o.idem != "" {
		req.Header.Set("Idempotency-Key", o.idem)
	}
	if o.fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", o.fetchSite)
	}
	if o.ifMatch != "" {
		req.Header.Set("If-Match", o.ifMatch)
	}
	if o.ifNoneMatch != "" {
		req.Header.Set("If-None-Match", o.ifNoneMatch)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, header: res.Header, raw: raw}
	_ = json.Unmarshal(raw, &r.body)
	if res.Header.Get("X-Request-ID") == "" {
		e.t.Fatalf("%s %s: missing X-Request-ID", method, path)
	}
	return r
}

func (r resp) code() string {
	if e, ok := r.body["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

func (r resp) want(t *testing.T, status int, code string) resp {
	t.Helper()
	if r.status != status || r.code() != code {
		t.Fatalf("got %d %s (%s), want %d %s", r.status, r.code(), r.raw, status, code)
	}
	if code != "" {
		e := r.body["error"].(map[string]any)
		if e["request_id"] != r.header.Get("X-Request-ID") {
			t.Fatalf("error request_id %v != header %s", e["request_id"], r.header.Get("X-Request-ID"))
		}
	}
	return r
}

type user struct {
	id, cookie, csrf string
}

// login provisions a launch token like the CLI and exchanges it over HTTP.
func (e *env) login(id string) user {
	e.t.Helper()
	token := NewToken()
	if err := e.store.IssueLaunchToken(context.Background(), id, TokenHash(token), 10*time.Minute); err != nil {
		e.t.Fatal(err)
	}
	r := e.do("POST", "/api/v1/session", map[string]string{"launch_token": token}, opts{}).want(e.t, 201, "")
	var cookie string
	for _, c := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, sessionCookie+"=") {
			cookie = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], sessionCookie+"=")
			if !strings.Contains(c, "HttpOnly") || !strings.Contains(c, "SameSite=Strict") || !strings.Contains(c, "Path=/") {
				e.t.Fatalf("cookie attributes: %s", c)
			}
		}
	}
	if cookie == "" || r.body["user_id"] != id || r.body["csrf_token"] == "" {
		e.t.Fatalf("session response %s", r.raw)
	}
	return user{id: id, cookie: cookie, csrf: r.body["csrf_token"].(string)}
}

func (e *env) newUser() user {
	e.t.Helper()
	id, err := e.store.CreateUserNamed(context.Background(), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return e.login(id)
}

func (u user) o() opts { return opts{cookie: u.cookie, csrf: u.csrf} }

func TestHealthAndReadiness(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/api/v1/health", nil, opts{}).want(t, 200, "")
	if r.body["status"] != "ok" {
		t.Fatal(string(r.raw))
	}
	latest, _ := migrations.LatestVersion()
	r = e.do("GET", "/api/v1/ready", nil, opts{}).want(t, 200, "")
	if r.body["status"] != "ready" || r.body["schema_version"] != float64(latest) {
		t.Fatal(string(r.raw))
	}
	if _, err := e.pool.Exec(context.Background(), "DELETE FROM codeye_schema_migrations WHERE version=$1", latest); err != nil {
		t.Fatal(err)
	}
	r = e.do("GET", "/api/v1/ready", nil, opts{}).want(t, 503, "")
	if r.body["reason"] != "schema_outdated" {
		t.Fatal(string(r.raw))
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM codeye_schema_migrations").Scan(&n); err != nil || n != latest-1 {
		t.Fatalf("readiness changed migration history: %d %v", n, err)
	}
	e.do("GET", "/api/v1/nope", nil, opts{}).want(t, 404, "NOT_FOUND")
	e.do("PUT", "/api/v1/health", nil, opts{}).want(t, 405, "METHOD_NOT_ALLOWED")
}

func TestHostOriginAndCSRF(t *testing.T) {
	e := newEnv(t)
	port := e.host[strings.LastIndex(e.host, ":")+1:]
	// DNS rebinding: a foreign Host is rejected even for public endpoints and static files.
	e.do("GET", "/api/v1/health", nil, opts{host: "evil.example:" + port}).want(t, 403, "HOST_REJECTED")
	e.do("GET", "/", nil, opts{host: "127.0.0.1"}).want(t, 403, "HOST_REJECTED")
	e.do("GET", "/api/v1/health", nil, opts{host: "localhost:" + port}).want(t, 200, "")
	e.do("GET", "/api/v1/health", nil, opts{host: "[::1]:" + port}).want(t, 200, "")

	token := NewToken()
	u, _ := e.store.CreateUserNamed(context.Background(), "")
	if err := e.store.IssueLaunchToken(context.Background(), u, TokenHash(token), time.Minute); err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"launch_token": token}
	e.do("POST", "/api/v1/session", body, opts{noOrigin: true}).want(t, 403, "ORIGIN_REJECTED")
	e.do("POST", "/api/v1/session", body, opts{origin: "http://evil.example"}).want(t, 403, "ORIGIN_REJECTED")
	e.do("POST", "/api/v1/session", body, opts{origin: "http://localhost:" + port}).want(t, 403, "ORIGIN_REJECTED") // Host is 127.0.0.1
	e.do("POST", "/api/v1/session", body, opts{fetchSite: "cross-site"}).want(t, 403, "ORIGIN_REJECTED")
	// The rejected attempts did not consume the token.
	e.do("POST", "/api/v1/session", body, opts{fetchSite: "same-origin"}).want(t, 201, "")

	a := e.newUser()
	root, _ := e.store.AddSourceRoot(context.Background(), "r", "/does/not/exist")
	_ = e.store.GrantSourceRoot(context.Background(), root.ID, a.id)
	proj := map[string]any{"source": map[string]any{"kind": "local", "source_root_id": root.ID, "relative_path": "."}}
	e.do("POST", "/api/v1/projects", proj, opts{cookie: a.cookie}).want(t, 403, "CSRF_REJECTED")
	e.do("POST", "/api/v1/projects", proj, opts{cookie: a.cookie, csrf: "wrong"}).want(t, 403, "CSRF_REJECTED")
	other := e.newUser()
	e.do("POST", "/api/v1/projects", proj, opts{cookie: a.cookie, csrf: other.csrf}).want(t, 403, "CSRF_REJECTED")
	e.do("POST", "/api/v1/projects", proj, opts{csrf: a.csrf}).want(t, 401, "UNAUTHENTICATED")
	e.do("POST", "/api/v1/projects", proj, a.o()).want(t, 201, "")
}

func TestSessionLifecycle(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/session", map[string]string{"launch_token": "short"}, opts{}).want(t, 401, "UNAUTHENTICATED")
	e.do("POST", "/api/v1/session", map[string]string{"launch_token": NewToken()}, opts{}).want(t, 401, "UNAUTHENTICATED")
	e.do("POST", "/api/v1/session", `{"launch_token":1}`, opts{}).want(t, 400, "INVALID_REQUEST")
	e.do("POST", "/api/v1/session", `{"launch_token":"x"}`, opts{ctype: "text/plain"}).want(t, 415, "UNSUPPORTED_MEDIA_TYPE")

	id, _ := e.store.CreateUserNamed(context.Background(), "")
	token := NewToken()
	if err := e.store.IssueLaunchToken(context.Background(), id, TokenHash(token), time.Minute); err != nil {
		t.Fatal(err)
	}
	first := e.do("POST", "/api/v1/session", map[string]string{"launch_token": token}, opts{}).want(t, 201, "")
	e.do("POST", "/api/v1/session", map[string]string{"launch_token": token}, opts{}).want(t, 401, "UNAUTHENTICATED")
	var cookie string
	for _, c := range first.header.Values("Set-Cookie") {
		cookie = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], sessionCookie+"=")
	}
	u := user{id: id, cookie: cookie, csrf: first.body["csrf_token"].(string)}
	g := e.do("GET", "/api/v1/session", nil, opts{cookie: u.cookie}).want(t, 200, "")
	if g.body["user_id"] != id || g.body["csrf_token"] != u.csrf {
		t.Fatal(string(g.raw))
	}
	e.do("GET", "/api/v1/session", nil, opts{}).want(t, 401, "UNAUTHENTICATED")
	e.do("GET", "/api/v1/session", nil, opts{cookie: NewToken()}).want(t, 401, "UNAUTHENTICATED")
	e.do("DELETE", "/api/v1/session", nil, opts{cookie: u.cookie}).want(t, 403, "CSRF_REJECTED")
	d := e.do("DELETE", "/api/v1/session", nil, u.o()).want(t, 204, "")
	if !strings.Contains(strings.Join(d.header.Values("Set-Cookie"), ";"), "Max-Age=0") {
		t.Fatalf("logout did not clear cookie: %v", d.header.Values("Set-Cookie"))
	}
	e.do("GET", "/api/v1/session", nil, opts{cookie: u.cookie}).want(t, 401, "UNAUTHENTICATED")

	// Disabling the user ends its sessions.
	v := e.newUser()
	if err := e.store.DisableUser(context.Background(), v.id); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/v1/projects", nil, opts{cookie: v.cookie}).want(t, 401, "UNAUTHENTICATED")
}

func TestProjectsSourcesAndACL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.newUser(), e.newUser()
	// The root path does not exist: registration must not touch the filesystem.
	root, err := e.store.AddSourceRoot(ctx, "workspace", "/nonexistent/codeye-root")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.GrantSourceRoot(ctx, root.ID, a.id); err != nil {
		t.Fatal(err)
	}
	gh, err := e.store.AddProvider(ctx, "github", "github.com", "")
	if err != nil {
		t.Fatal(err)
	}
	r := e.do("GET", "/api/v1/source-roots", nil, a.o()).want(t, 200, "")
	if items := r.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["label"] != "workspace" || strings.Contains(string(r.raw), "nonexistent") {
		t.Fatalf("source roots %s", r.raw)
	}
	if items := e.do("GET", "/api/v1/source-roots", nil, b.o()).want(t, 200, "").body["items"].([]any); len(items) != 0 {
		t.Fatal("ungranted root listed")
	}
	if items := e.do("GET", "/api/v1/providers", nil, b.o()).want(t, 200, "").body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["host"] != "github.com" {
		t.Fatal("providers")
	}

	local := func(path string) map[string]any {
		return map[string]any{"source": map[string]any{"kind": "local", "source_root_id": root.ID, "relative_path": path}}
	}
	p := e.do("POST", "/api/v1/projects", local("svc/api"), a.o()).want(t, 201, "")
	pid := p.body["id"].(string)
	if p.body["name"] != "api" || p.body["source_status"] != "declared_unverified" || p.body["revision"] != float64(1) || p.body["last_run"] != nil ||
		len(p.body["capability_gaps"].([]any)) != 3 || p.header.Get("Location") != "/api/v1/projects/"+pid {
		t.Fatalf("project %s", p.raw)
	}
	src := p.body["source"].(map[string]any)
	if src["kind"] != "local" || src["relative_path"] != "svc/api" {
		t.Fatalf("source %v", src)
	}
	git := map[string]any{"name": "Repo", "description": "d", "source": map[string]any{"kind": "git", "provider_id": gh.ID, "repository_url": "https://github.com/o/repo.git", "branch": "main"}}
	e.do("POST", "/api/v1/projects", git, a.o()).want(t, 201, "")

	cases := []struct {
		body   any
		status int
		code   string
	}{
		{local("/etc"), 422, "VALIDATION_FAILED"},
		{local("../x"), 422, "VALIDATION_FAILED"},
		{local(`a\b`), 422, "VALIDATION_FAILED"},
		{map[string]any{"source": map[string]any{"kind": "git", "provider_id": gh.ID, "repository_url": "git@github.com:o/r.git", "branch": "main"}}, 422, "SOURCE_UNSUPPORTED"},
		{map[string]any{"source": map[string]any{"kind": "git", "provider_id": gh.ID, "repository_url": "https://tok@github.com/o/r", "branch": "main"}}, 422, "VALIDATION_FAILED"},
		{map[string]any{"source": map[string]any{"kind": "git", "provider_id": gh.ID, "repository_url": "https://gitlab.com/o/r", "branch": "main"}}, 422, "VALIDATION_FAILED"},
		{map[string]any{"source": map[string]any{"kind": "git", "provider_id": domain.NewID(), "repository_url": "https://github.com/o/r", "branch": "main"}}, 404, "NOT_FOUND"},
		{map[string]any{"source": map[string]any{"kind": "local", "source_root_id": domain.NewID(), "relative_path": "."}}, 404, "NOT_FOUND"},
		{map[string]any{"source": local(".")["source"], "attachment_ids": []string{"x"}}, 422, "ATTACHMENTS_UNSUPPORTED"},
		{map[string]any{"source": local(".")["source"], "name": "  "}, 422, "VALIDATION_FAILED"},
		{map[string]any{"source": local(".")["source"], "extra": 1}, 400, "INVALID_REQUEST"},
		{map[string]any{"name": "x"}, 400, "INVALID_REQUEST"},
		{`[1]`, 400, "INVALID_REQUEST"},
		{`{"source":` + strings.Repeat(" ", maxBodyBytes) + `{}}`, 413, "PAYLOAD_TOO_LARGE"},
	}
	for i, c := range cases {
		t.Logf("case %d", i)
		e.do("POST", "/api/v1/projects", c.body, a.o()).want(t, c.status, c.code)
	}
	// b has no grant on the root: same 404 as a nonexistent root.
	e.do("POST", "/api/v1/projects", local("."), b.o()).want(t, 404, "NOT_FOUND")

	// Listing and pagination.
	l1 := e.do("GET", "/api/v1/projects?limit=1", nil, a.o()).want(t, 200, "")
	if len(l1.body["items"].([]any)) != 1 || l1.body["next_cursor"] == nil {
		t.Fatalf("list %s", l1.raw)
	}
	l2 := e.do("GET", "/api/v1/projects?limit=1&cursor="+l1.body["next_cursor"].(string), nil, a.o()).want(t, 200, "")
	if items := l2.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != pid || l2.body["next_cursor"] != nil {
		t.Fatalf("list page 2 %s", l2.raw)
	}
	e.do("GET", "/api/v1/projects?limit=0", nil, a.o()).want(t, 400, "INVALID_REQUEST")
	e.do("GET", "/api/v1/projects?cursor=zzz", nil, a.o()).want(t, 400, "INVALID_REQUEST")
	if items := e.do("GET", "/api/v1/projects", nil, b.o()).want(t, 200, "").body["items"].([]any); len(items) != 0 {
		t.Fatal("non-member sees projects")
	}

	// Non-members get 404 (existence hidden); members with a low role get 403.
	e.do("GET", "/api/v1/projects/"+pid, nil, b.o()).want(t, 404, "NOT_FOUND")
	e.do("GET", "/api/v1/projects/"+domain.NewID(), nil, a.o()).want(t, 404, "NOT_FOUND")
	e.do("GET", "/api/v1/projects/not-a-uuid", nil, a.o()).want(t, 404, "NOT_FOUND")
	runBody := map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}
	ob := b.o()
	ob.idem = "k-b"
	e.do("POST", "/api/v1/projects/"+pid+"/runs", runBody, ob).want(t, 404, "NOT_FOUND")
	if err := e.store.SetMember(ctx, a.id, pid, b.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/v1/projects/"+pid, nil, b.o()).want(t, 200, "")
	e.do("POST", "/api/v1/projects/"+pid+"/runs", runBody, ob).want(t, 403, "FORBIDDEN")
	if err := e.store.SetMember(ctx, a.id, pid, b.id, ""); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/v1/projects/"+pid, nil, b.o()).want(t, 404, "NOT_FOUND")
}

func registerProject(e *env, u user) string {
	e.t.Helper()
	root, err := e.store.AddSourceRoot(context.Background(), "root-"+domain.NewID()[:8], "/srv/x")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.GrantSourceRoot(context.Background(), root.ID, u.id); err != nil {
		e.t.Fatal(err)
	}
	p := e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "local", "source_root_id": root.ID, "relative_path": "."}}, u.o()).want(e.t, 201, "")
	return p.body["id"].(string)
}

func TestRunsIdempotencyStatusAndCancel(t *testing.T) {
	e := newEnv(t)
	a := e.newUser()
	pid := registerProject(e, a)
	path := "/api/v1/projects/" + pid + "/runs"
	body := map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}
	e.do("POST", path, body, a.o()).want(t, 400, "INVALID_REQUEST") // no key
	o := a.o()
	o.idem = "bad key"
	e.do("POST", path, body, o).want(t, 400, "INVALID_REQUEST")
	o.idem = "key-1"
	e.do("POST", path, `{"profile":"deep-review"`, o).want(t, 400, "INVALID_REQUEST")
	e.do("POST", path, map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}, "command": "rm"}, o).want(t, 400, "INVALID_REQUEST")
	e.do("POST", path, map[string]any{"profile": "fast", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, o).want(t, 422, "VALIDATION_FAILED")

	r1 := e.do("POST", path, body, o).want(t, 202, "")
	rid := r1.body["id"].(string)
	if r1.body["status"] != "queued" || r1.body["url"] != "/api/v1/runs/"+rid || r1.header.Get("Location") != "/api/v1/runs/"+rid {
		t.Fatalf("ack %s", r1.raw)
	}
	r2 := e.do("POST", path, `{ "scope": {"kind":"full"}, "source_policy":"latest_at_acquire", "profile":"deep-review" }`, o).want(t, 202, "")
	if r2.body["id"] != rid {
		t.Fatalf("replay created a new run: %s", r2.raw)
	}
	e.do("POST", path, map[string]any{"profile": "focused", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, o).want(t, 409, "IDEMPOTENCY_CONFLICT")
	var runs int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM runs").Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("runs = %d %v", runs, err)
	}

	v := e.do("GET", "/api/v1/runs/"+rid, nil, a.o()).want(t, 200, "")
	prog := v.body["progress"].(map[string]any)
	if v.body["status"] != "queued" || v.body["snapshot_id"] != nil || v.body["quality_verdict"] != nil || v.body["project_id"] != pid ||
		prog["waiting_reason"] != "execution_unavailable" || prog["estimated_percent"] != nil || prog["total_tasks"] != nil ||
		prog["plan_revision"] != nil || prog["completed_tasks"] != float64(0) {
		t.Fatalf("run view %s", v.raw)
	}
	p := e.do("GET", "/api/v1/projects/"+pid, nil, a.o()).want(t, 200, "")
	if lr := p.body["last_run"].(map[string]any); lr["id"] != rid || lr["status"] != "queued" {
		t.Fatalf("last_run %s", p.raw)
	}
	b := e.newUser()
	e.do("GET", "/api/v1/runs/"+rid, nil, b.o()).want(t, 404, "NOT_FOUND")
	e.do("POST", "/api/v1/runs/"+rid+"/cancel", nil, b.o()).want(t, 404, "NOT_FOUND")
	e.do("POST", "/api/v1/runs/"+rid+"/cancel", nil, opts{cookie: a.cookie}).want(t, 403, "CSRF_REJECTED")
	c1 := e.do("POST", "/api/v1/runs/"+rid+"/cancel", nil, a.o()).want(t, 202, "")
	if c1.body["status"] != "cancelled" {
		t.Fatalf("cancel %s", c1.raw)
	}
	c2 := e.do("POST", "/api/v1/runs/"+rid+"/cancel", nil, a.o()).want(t, 200, "")
	if c2.body["status"] != "cancelled" || c2.body["version"] != c1.body["version"] {
		t.Fatalf("repeat cancel %s", c2.raw)
	}
	v = e.do("GET", "/api/v1/runs/"+rid, nil, a.o()).want(t, 200, "")
	if v.body["progress"].(map[string]any)["waiting_reason"] != nil {
		t.Fatalf("cancelled run still waiting: %s", v.raw)
	}
	e.do("GET", "/api/v1/runs/"+domain.NewID(), nil, a.o()).want(t, 404, "NOT_FOUND")
}

func TestDatabaseFailureNeverAccepts(t *testing.T) {
	e := newEnv(t)
	a := e.newUser()
	pid := registerProject(e, a)
	// Close the pool behind the running server: every store call now fails.
	e.pool.Close()
	o := a.o()
	o.idem = "k"
	body := map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}
	r := e.do("POST", "/api/v1/projects/"+pid+"/runs", body, o).want(t, 503, "UNAVAILABLE")
	if r.body["error"].(map[string]any)["retryable"] != true {
		t.Fatal("503 not retryable")
	}
	rd := e.do("GET", "/api/v1/ready", nil, opts{}).want(t, 503, "")
	if rd.body["reason"] != "database_unavailable" {
		t.Fatal(string(rd.raw))
	}
	check := newPool(t, e.url, e.schema)
	defer check.Close()
	var runs int
	if err := check.QueryRow(context.Background(), "SELECT count(*) FROM runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("runs after failure = %d %v", runs, err)
	}
	e.pool = nil
}

func TestRestartKeepsRunsAndSessions(t *testing.T) {
	e := newEnv(t)
	a := e.newUser()
	pid := registerProject(e, a)
	o := a.o()
	o.idem = "durable"
	body := map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}
	rid := e.do("POST", "/api/v1/projects/"+pid+"/runs", body, o).want(t, 202, "").body["id"].(string)
	e.start(t.TempDir()) // new pool, store, server and port
	v := e.do("GET", "/api/v1/runs/"+rid, nil, a.o()).want(t, 200, "")
	if v.body["status"] != "queued" {
		t.Fatal(string(v.raw))
	}
	again := e.do("POST", "/api/v1/projects/"+pid+"/runs", body, o).want(t, 202, "")
	if again.body["id"] != rid {
		t.Fatal("idempotency lost across restart")
	}
}

func TestStaticWebApp(t *testing.T) {
	e := newEnv(t)
	base := t.TempDir()
	web := filepath.Join(base, "web")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(web, "assets"), 0o755))
	must(os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>CODEYE</title>"), 0o644))
	must(os.WriteFile(filepath.Join(web, "assets", "app.js"), []byte("console.log(1)"), 0o644))
	must(os.WriteFile(filepath.Join(base, "secret.txt"), []byte("TOP-SECRET"), 0o644))
	must(os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(web, "leak.txt")))
	must(os.Symlink(base, filepath.Join(web, "up")))
	e.start(web)

	get := func(path string) resp { return e.do("GET", path, nil, opts{}) }
	r := get("/")
	if r.status != 200 || !strings.Contains(string(r.raw), "CODEYE") || r.header.Get("Content-Security-Policy") == "" || r.header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("index %d %s %v", r.status, r.raw, r.header)
	}
	if r := get("/assets/app.js"); r.status != 200 || !strings.Contains(r.header.Get("Content-Type"), "javascript") {
		t.Fatalf("asset %d %v", r.status, r.header)
	}
	if r := get("/projects/123"); r.status != 200 || !strings.Contains(string(r.raw), "CODEYE") {
		t.Fatalf("spa fallback %d", r.status)
	}
	for _, p := range []string{"/assets/missing.js", "/leak.txt", "/up/secret.txt", "/../secret.txt", "/%2e%2e/secret.txt"} {
		r := get(p)
		if r.status != 404 || strings.Contains(string(r.raw), "TOP-SECRET") {
			t.Fatalf("%s -> %d %s", p, r.status, r.raw)
		}
	}
	if r := get("/assets/"); strings.Contains(string(r.raw), "app.js") {
		t.Fatalf("directory listing: %s", r.raw)
	}
	if r := e.do("POST", "/", "x", opts{}); r.status != 405 {
		t.Fatalf("POST static = %d", r.status)
	}
	if r := e.do("GET", "/api/v1/unknown", nil, opts{}); r.status != 404 || r.code() != "NOT_FOUND" {
		t.Fatalf("api fallback %d %s", r.status, r.raw)
	}
	e.start("")
	if r := get("/"); r.status != 503 {
		t.Fatalf("missing web dir = %d", r.status)
	}
}

func TestNoSecretsInLogsOrErrors(t *testing.T) {
	e := newEnv(t)
	id, _ := e.store.CreateUserNamed(context.Background(), "")
	token := NewToken()
	if err := e.store.IssueLaunchToken(context.Background(), id, TokenHash(token), time.Minute); err != nil {
		t.Fatal(err)
	}
	r := e.do("POST", "/api/v1/session", map[string]string{"launch_token": token}, opts{}).want(t, 201, "")
	var cookie string
	for _, c := range r.header.Values("Set-Cookie") {
		cookie = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], sessionCookie+"=")
	}
	u := user{id: id, cookie: cookie, csrf: r.body["csrf_token"].(string)}
	pid := registerProject(e, u)
	o := u.o()
	o.idem = "secret-idem"
	e.do("POST", "/api/v1/projects/"+pid+"/runs", map[string]any{"profile": "x"}, o)
	e.do("GET", "/api/v1/projects?cursor=SECRETCURSOR", nil, u.o())
	e.pool.Close()
	errResp := e.do("GET", "/api/v1/projects", nil, u.o()).want(t, 503, "UNAVAILABLE")
	e.pool = nil
	logs := e.logs.String()
	if !strings.Contains(logs, "projects.list") {
		t.Fatalf("expected request logs, got %q", logs)
	}
	for _, secret := range []string{token, cookie, u.csrf, "secret-idem", "SECRETCURSOR", e.schema, "/srv/x"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log contains secret %q", secret)
		}
		if strings.Contains(string(errResp.raw), secret) {
			t.Fatalf("error contains secret %q", secret)
		}
	}
	for _, leak := range []string{"SELECT", "pool", "pgx", "postgres"} {
		if strings.Contains(string(errResp.raw), leak) {
			t.Fatalf("error leaks %q: %s", leak, errResp.raw)
		}
	}
}

func mustLoopback(t *testing.T) netip.AddrPort {
	t.Helper()
	return netip.MustParseAddrPort("127.0.0.1:7331")
}
