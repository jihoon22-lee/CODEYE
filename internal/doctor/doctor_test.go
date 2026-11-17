package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	result   Result
	commands []Command
}

func (f *fakeRunner) Run(_ context.Context, c Command) Result {
	f.commands = append(f.commands, c)
	return f.result
}
func TestDatabasePolicy(t *testing.T) {
	for _, tc := range []struct{ dsn, reason string }{
		{"", "database_url_missing"}, {"secret", "invalid_database_url"},
		{"postgres://u:secret@db.invalid/app?sslmode=disable", "tls_required"},
		{"postgres://u:secret@db.invalid/app?sslmode=prefer", "unsupported_tls_mode"},
		{"postgres://u:secret@db.invalid/app?host=other.invalid", "unsupported_database_option"},
		{"postgres://u:secret@db.invalid/app?service=hidden", "unsupported_database_option"},
		{"postgres://u:secret@db.invalid/app?sslmode=disable&sslmode=verify-full", "unsupported_database_option"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			runner := &fakeRunner{}
			got := database(context.Background(), Options{DatabaseURL: tc.dsn}, runner)
			if got.Reason != tc.reason || len(runner.commands) != 0 {
				t.Fatalf("unexpected check: %+v", got)
			}
		})
	}
}
func TestDatabaseQueryAndRedaction(t *testing.T) {
	runner := &fakeRunner{result: Result{Output: "17.10\n"}}
	got := database(context.Background(), Options{DatabaseURL: "postgres://u:secret@127.0.0.1/app?sslmode=disable", Timeout: time.Second}, runner)
	if got.Status != "available" || got.Version != "17.10" {
		t.Fatalf("%+v", got)
	}
	cmd := runner.commands[0]
	if strings.Contains(strings.Join(cmd.Args, " "), "secret") || !strings.Contains(strings.Join(cmd.Env, " "), "secret") {
		t.Fatal("credentials must only be in child environment")
	}
	for _, tc := range []struct {
		result Result
		reason string
	}{
		{Result{Reason: "probe_failed", Output: "28P01 secret"}, "authentication_failed"},
		{Result{Reason: "probe_failed", Output: "3D000 secret"}, "database_missing"},
		{Result{Reason: "probe_failed", Output: "private server secret"}, "connection_failed"},
		{Result{Reason: "recovery_required", Output: "secret"}, "recovery_required"},
		{Result{Output: "secret"}, "server_version_unrecognized"},
	} {
		runner.result = tc.result
		got = database(context.Background(), Options{DatabaseURL: "postgres://u:secret@127.0.0.1/app"}, runner)
		data, _ := json.Marshal(got)
		if got.Reason != tc.reason || strings.Contains(string(data), "secret") {
			t.Fatalf("%s", data)
		}
	}
}
func TestCollectPreservesGapsAndCleansDisk(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{result: Result{Reason: "executable_missing"}}
	report := Collect(context.Background(), Options{WorkDir: dir, Timeout: time.Second}, runner)
	if report.ExitCode() != 1 {
		t.Fatal("incomplete capabilities must not pass")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("doctor left probe files")
	}
	for _, c := range report.Checks {
		if c.ID == "model" && (c.Status != "unverified" || c.Reason != "adapter_contract_pending_w10") {
			t.Fatalf("%+v", c)
		}
	}
	var b bytes.Buffer
	if err := report.Write(&b, "json"); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Checks) != 13 {
		t.Fatalf("got %d checks", len(decoded.Checks))
	}
}
func TestDiskFailure(t *testing.T) {
	name := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(name, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := workDisk(name); got.Status != "unavailable" {
		t.Fatalf("%+v", got)
	}
	data, _ := os.ReadFile(name)
	if string(data) != "original" {
		t.Fatal("existing file changed")
	}
}
func TestDatabaseIntegration(t *testing.T) {
	dsn := os.Getenv("CODEYE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL URL not configured")
	}
	got := database(context.Background(), Options{DatabaseURL: dsn, Timeout: 5 * time.Second}, NativeRunner{})
	if got.Status != "available" || got.Reason != "authenticated_query_verified" {
		t.Fatalf("%+v", got)
	}
}

func TestDatabaseFailureIntegration(t *testing.T) {
	dsn := os.Getenv("CODEYE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL URL not configured")
	}
	for _, kind := range []string{"authentication_failed", "database_missing"} {
		t.Run(kind, func(t *testing.T) {
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal("invalid test URL")
			}
			if kind == "authentication_failed" {
				u.User = url.UserPassword(u.User.Username(), "wrong-test-password")
			} else {
				u.Path = "/codeye_absent_database"
			}
			got := database(context.Background(), Options{DatabaseURL: u.String(), Timeout: 5 * time.Second}, NativeRunner{})
			if got.Status != "unavailable" || got.Reason != kind {
				t.Fatalf("%+v", got)
			}
		})
	}
}
