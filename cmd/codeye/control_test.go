package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

// isolatedStore points openStore at a fresh migrated schema. Without
// CODEYE_TEST_DATABASE_URL the calling test skips (NOT executed).
func isolatedStore(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := os.Getenv("CODEYE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CODEYE_TEST_DATABASE_URL not set: CLI database test NOT executed")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("test DB connection failed")
	}
	schema := "cli_" + strings.ReplaceAll(domain.NewID(), "-", "")
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	check, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	prev := openStore
	openStore = func(ctx context.Context) (*postgres.Store, error) {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return postgres.NewFromPool(pool), nil
	}
	t.Cleanup(func() {
		openStore = prev
		check.Close()
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = conn.Close(context.Background())
	})
	return check, schema
}

func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := run(context.Background(), args, &out, &stderr)
	return code, out.String(), stderr.String()
}

func idOf(t *testing.T, stdout string) string {
	t.Helper()
	var v map[string]string
	if err := json.Unmarshal([]byte(stdout), &v); err != nil || !domain.ValidID(v["id"]) {
		t.Fatalf("stdout %q", stdout)
	}
	return v["id"]
}

func TestControlCommands(t *testing.T) {
	pool, _ := isolatedStore(t)
	ctx := context.Background()
	code, out, _ := cli(t, "user", "create", "--name", "Alice")
	if code != 0 {
		t.Fatal(out)
	}
	user := idOf(t, out)
	if code, _, _ := cli(t, "user", "create", "--name", "bad\x01"); code != 1 {
		t.Fatal("control-character name accepted")
	}
	if code, _, _ := cli(t, "user"); code != 2 {
		t.Fatal("missing subcommand accepted")
	}

	dir := t.TempDir()
	if code, _, _ := cli(t, "source-root", "add", "--label", "w", "--path", "relative/dir"); code != 2 {
		t.Fatal("relative root accepted")
	}
	if code, _, _ := cli(t, "source-root", "add", "--label", "w", "--path", filepath.Join(dir, "missing")); code != 1 {
		t.Fatal("missing root accepted")
	}
	code, out, _ = cli(t, "source-root", "add", "--label", "work", "--path", dir)
	if code != 0 {
		t.Fatal(out)
	}
	root := idOf(t, out)
	if code, _, errOut := cli(t, "source-root", "grant", "--root", root, "--user", user); code != 0 {
		t.Fatal(errOut)
	}
	var path string
	if err := pool.QueryRow(ctx, "SELECT path FROM source_roots WHERE id=$1", root).Scan(&path); err != nil || path != dir {
		t.Fatalf("stored path %q %v", path, err)
	}

	if code, _, _ := cli(t, "provider", "add", "--kind", "github", "--host", "ghe.example"); code != 1 {
		t.Fatal("github with non-github host accepted")
	}
	if code, _, _ := cli(t, "provider", "add", "--kind", "ghes", "--host", "ghe.example", "--api-url", "http://ghe.example/api"); code != 1 {
		t.Fatal("http api url accepted")
	}
	code, out, _ = cli(t, "provider", "add", "--kind", "ghes", "--host", "ghe.example", "--api-url", "https://ghe.example/api/v3")
	if code != 0 {
		t.Fatal(out)
	}
	idOf(t, out)

	if code, _, _ := cli(t, "user", "disable", "--id", domain.NewID()); code != 1 {
		t.Fatal("unknown user disabled")
	}
	code, out, _ = cli(t, "user", "create")
	if code != 0 {
		t.Fatal(out)
	}
	if code, _, errOut := cli(t, "user", "disable", "--id", idOf(t, out)); code != 0 {
		t.Fatal(errOut)
	}
}

func TestLoginLinkFile(t *testing.T) {
	pool, _ := isolatedStore(t)
	_, out, _ := cli(t, "user", "create")
	user := idOf(t, out)
	dir := t.TempDir()
	link := filepath.Join(dir, "sub", "login-link")
	code, out, errOut := cli(t, "login-link", "--user", user, "--output", link, "--origin", "http://127.0.0.1:7444")
	if code != 0 {
		t.Fatal(errOut)
	}
	raw, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(string(raw))
	prefix := "http://127.0.0.1:7444/#launch="
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("link %q", url)
	}
	token := strings.TrimPrefix(url, prefix)
	if strings.Contains(out+errOut, token) || !strings.Contains(out, link) {
		t.Fatalf("token printed or path missing: %q %q", out, errOut)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(link)
		dst, _ := os.Stat(filepath.Dir(link))
		if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
			t.Fatalf("modes file %v dir %v", st.Mode().Perm(), dst.Mode().Perm())
		}
	}
	var stored int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM auth_launch_tokens WHERE token_sha256=$1", httpapi.TokenHash(token)).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("token hash stored %d %v", stored, err)
	}
	var plain int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM auth_launch_tokens WHERE token_sha256=$1", token).Scan(&plain); err != nil || plain != 0 {
		t.Fatal("raw token stored")
	}
	// A second link replaces the file atomically; the previous token stays single-use.
	if code, _, errOut := cli(t, "login-link", "--user", user, "--output", link, "--origin", "http://127.0.0.1:7444"); code != 0 {
		t.Fatal(errOut)
	}

	if runtime.GOOS != "windows" {
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		sym := filepath.Join(dir, "sym")
		if err := os.Symlink(target, sym); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := cli(t, "login-link", "--user", user, "--output", sym); code != 1 {
			t.Fatal("symlink output accepted")
		}
		if b, _ := os.ReadFile(target); string(b) != "keep" {
			t.Fatal("symlink target overwritten")
		}
	}
	for _, origin := range []string{"https://127.0.0.1:7444", "http://evil.example:7444", "http://127.0.0.1", "http://127.0.0.1:7444/path"} {
		if code, _, _ := cli(t, "login-link", "--user", user, "--output", filepath.Join(dir, "x"), "--origin", origin); code != 1 {
			t.Fatalf("origin %q accepted", origin)
		}
	}
	other := filepath.Join(dir, "unknown-user")
	if code, _, _ := cli(t, "login-link", "--user", domain.NewID(), "--output", other); code != 1 {
		t.Fatal("link for unknown user")
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatal("file written for unknown user")
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestServeRefusesUnsafeStartAndServes(t *testing.T) {
	pool, _ := isolatedStore(t)
	if code, _, _ := cli(t, "serve", "--listen", "0.0.0.0:7331"); code != 2 {
		t.Fatal("non-loopback listen accepted")
	}
	_, out, _ := cli(t, "user", "create")
	user := idOf(t, out)
	addr := freePort(t)
	link := filepath.Join(t.TempDir(), "login-link")

	latest, _ := migrations.LatestVersion()
	if _, err := pool.Exec(context.Background(), "DELETE FROM codeye_schema_migrations WHERE version=$1", latest); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := cli(t, "serve", "--listen", addr, "--user", user, "--login-link-file", link)
	if code != 1 || !strings.Contains(errOut, "codeye migrate") {
		t.Fatalf("outdated schema served: %d %s", code, errOut)
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Fatal("login link written before readiness")
	}
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM codeye_schema_migrations").Scan(&n); err != nil || n != latest-1 {
		t.Fatal("serve migrated silently")
	}
	ms, _ := migrations.Embedded()
	m := ms[latest-1]
	if _, err := pool.Exec(context.Background(), "INSERT INTO codeye_schema_migrations(version,name,checksum) VALUES($1,$2,$3)", m.Version, m.Name, m.Checksum); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var sout, serr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--listen", addr, "--web-dir", "", "--artifact-dir", filepath.Join(t.TempDir(), "artifacts"), "--user", user, "--login-link-file", link}, &sout, &serr)
	}()
	deadline := time.Now().Add(10 * time.Second)
	var res *http.Response
	var err error
	for time.Now().Before(deadline) {
		res, err = http.Get("http://" + addr + "/api/v1/ready")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("server did not start: %v; stderr %s", err, serr.String())
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("ready = %d", res.StatusCode)
	}
	raw, err := os.ReadFile(link)
	if err != nil || !strings.HasPrefix(string(raw), "http://"+addr+"/#launch=") {
		t.Fatalf("login link %q %v", raw, err)
	}
	token := strings.TrimSpace(strings.TrimPrefix(string(raw), "http://"+addr+"/#launch="))
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit %d: %s", code, serr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop on cancellation")
	}
	if strings.Contains(sout.String()+serr.String(), token) {
		t.Fatal("serve printed the launch token")
	}
}

func TestDatabaseCommandsDoNotLeakURL(t *testing.T) {
	t.Setenv("CODEYE_DATABASE_URL", "postgresql://u:secretpw@127.0.0.1:1/db?sslmode=disable")
	for _, args := range [][]string{{"user", "create"}, {"serve", "--listen", "127.0.0.1:1"}, {"login-link", "--user", domain.NewID()}} {
		code, out, errOut := cli(t, args...)
		if code == 0 || strings.Contains(out+errOut, "secretpw") {
			t.Fatalf("%v: code %d output %q %q", args, code, out, errOut)
		}
	}
	if code, _, errOut := cli(t, "user", "create", "--name=secretpw", "extra"); code != 2 || strings.Contains(errOut, "secretpw") {
		t.Fatal("rejected argument echoed")
	}
}
