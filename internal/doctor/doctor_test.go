package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
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

// detectedTools is a detection result in which exactly the named tools are
// found (with a tested version) and every other tool is missing.
func detectedTools(found ...string) func(context.Context) toolfind.Result {
	return func(context.Context) toolfind.Result {
		res := toolfind.Result{SchemaVersion: toolfind.SchemaVersion}
		for _, name := range toolfind.Names() {
			t := toolfind.Tool{Name: name, Status: toolfind.NotFound}
			if slices.Contains(found, name) {
				t = toolfind.Tool{Name: name, Status: toolfind.Found, Path: "/fake/" + name, Version: "1.2.3", VersionTested: true, Source: toolfind.SourcePath}
			}
			res.Tools = append(res.Tools, t)
		}
		return res
	}
}

func checkOf(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %s", id)
	return Check{}
}

func TestCollectReportsGapsHonestlyAndCleansDisk(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{result: Result{Reason: "executable_missing"}}
	report := Collect(context.Background(), Options{WorkDir: dir, Timeout: time.Second, Detect: detectedTools("ruff")}, runner)
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("doctor left probe files")
	}
	// The checks that were placeholders are gone; missing tools stay visible
	// as unavailable although they do not make the exit code 1.
	for _, c := range report.Checks {
		if c.ID == "model" || c.ID == "process_control" {
			t.Fatalf("placeholder check %s", c.ID)
		}
	}
	if g := checkOf(t, report, "git"); g.Status != "unavailable" || g.Reason != "executable_missing" || g.Blocking {
		t.Fatalf("git %+v", g)
	}
	if a := checkOf(t, report, "analyzers"); a.Status != "available" || a.Reason != "baseline_analyzer_usable" {
		t.Fatalf("analyzers %+v", a)
	}
	if d := checkOf(t, report, "database"); d.Status != "not_configured" || d.Blocking {
		t.Fatalf("database %+v", d)
	}
	if len(report.Tools) != len(toolfind.Names()) {
		t.Fatalf("tools %+v", report.Tools)
	}
	var b bytes.Buffer
	if err := report.Write(&b, "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "/fake/") {
		t.Fatal("the JSON report must not carry tool paths")
	}
	var decoded Report
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if c := checkOf(t, report, "build_tools"); c.Status != "unavailable" || c.Reason != "build_tools_missing" || c.Blocking {
		t.Fatalf("build tools %+v", c)
	}
	if len(decoded.Checks) != len(report.Checks) || len(decoded.Checks) != 12 {
		t.Fatalf("got %d checks", len(decoded.Checks))
	}
	b.Reset()
	if err := report.Write(&b, "text"); err != nil || !strings.Contains(b.String(), "/fake/ruff") || !strings.Contains(b.String(), "not found") {
		t.Fatalf("text %q %v", b.String(), err)
	}
}

// The exit code is 1 only for what matters in the chosen mode.
func TestExitCodeRule(t *testing.T) {
	cfg := func(mode string, tools map[string]string) *instance.Config {
		return &instance.Config{StateDir: t.TempDir(), Database: instance.Database{Mode: mode}, Agent: instance.Agent{Tools: tools}}
	}
	missing := Result{Reason: "executable_missing"}
	psqlOK := Result{Output: "psql (PostgreSQL) 17.10\n"}
	badDB := "postgres://u:secret@127.0.0.1/app?sslmode=disable"
	unusableRuff := func(source string) func(context.Context) toolfind.Result {
		return func(context.Context) toolfind.Result {
			res := detectedTools("go")(context.Background())
			for i, tool := range res.Tools {
				if tool.Name == "ruff" {
					res.Tools[i] = toolfind.Tool{Name: "ruff", Status: toolfind.Unusable, Reason: "x", Path: "/x/ruff", Source: source}
				}
			}
			return res
		}
	}
	for _, tc := range []struct {
		name   string
		opts   Options
		runner Result
		want   int
		reason string
	}{
		{"no tool at all", Options{Detect: detectedTools()}, missing, 1, "analyzers blocks"},
		{"one baseline tool, nothing else", Options{Detect: detectedTools("ruff")}, missing, 0, "missing git, go, psql and database do not matter"},
		{"external mode without a database", Options{Detect: detectedTools("go"), Instance: cfg("external", nil)}, missing, 1, "external needs its database"},
		{"managed mode without a database url", Options{Detect: detectedTools("go"), Instance: cfg("managed", nil)}, missing, 0, "managed brings its own"},
		{"database url that cannot be queried", Options{Detect: detectedTools("go"), DatabaseURL: badDB}, missing, 1, "configured database failing"},
		{"database url that works", Options{Detect: detectedTools("go"), DatabaseURL: badDB}, psqlOK, 0, "configured database fine"},
		{"explicit tool that is unusable", Options{Detect: unusableRuff(toolfind.SourceConfig), Instance: cfg("managed", map[string]string{"ruff": "/x/ruff"})}, missing, 1, "explicit unusable tool blocks"},
		{"detected tool that is unusable", Options{Detect: unusableRuff(""), Instance: cfg("managed", nil)}, missing, 0, "only an explicit one blocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.WorkDir, tc.opts.Timeout = t.TempDir(), time.Second
			report := Collect(context.Background(), tc.opts, &fakeRunner{result: tc.runner})
			if got := report.ExitCode(); got != tc.want {
				t.Fatalf("exit %d, want %d (%s): %+v", got, tc.want, tc.reason, report.Checks)
			}
		})
	}
	// An unwritable work directory blocks.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	report := Collect(context.Background(), Options{WorkDir: file, Timeout: time.Second, Detect: detectedTools("go")}, &fakeRunner{result: Result{Reason: "executable_missing"}})
	if report.ExitCode() != 1 || !checkOf(t, report, "work_disk").Blocking {
		t.Fatalf("%+v", report.Checks)
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
