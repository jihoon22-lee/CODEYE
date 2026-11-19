package personal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A configuration holding only schema_version is complete: managed database,
// state under $XDG_STATE_HOME, no source roots, the default listen address
// (not explicit, so it may move to a free port).
func TestMinimalConfigDefaults(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	c, err := Parse([]byte(`{"schema_version":"0.1"}`), tools)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != filepath.Join(xdg, "codeye") || !c.Managed() || len(c.SourceRoots) != 0 || c.ListenExplicit() || c.Listen != DefaultListen {
		t.Fatalf("%+v", c)
	}
	t.Setenv("XDG_STATE_HOME", "")
	// The home directory comes from HOME on Unix and USERPROFILE on Windows.
	t.Setenv("HOME", xdg)
	t.Setenv("USERPROFILE", xdg)
	if c, err = Parse([]byte(`{"schema_version":"0.1","listen":"127.0.0.1:7400","database":{"mode":"external"}}`), tools); err != nil {
		t.Fatal(err)
	}
	if c.StateDir != filepath.Join(xdg, ".local", "state", "codeye") || c.Managed() || !c.ListenExplicit() {
		t.Fatalf("%+v", c)
	}
}

// codeye up writes a minimal file when none exists and changes only what it
// owns: field order and the operator's fields stay, defaults are not written.
func TestConfigFileEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg", "personal.json")
	if created, err := EnsureFile(path); err != nil || !created {
		t.Fatal(created, err)
	}
	if created, err := EnsureFile(path); err != nil || created {
		t.Fatal("second EnsureFile", created, err)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte(`{"schema_version":"0.1","state_dir":`+q(state)+`,"agent":{"max_workers":2},"database":{"mode":"managed"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src")
	c, err := EditFile(path, tools, ConfigEdit{PostgresBinDir: filepath.Join(dir, "pg"), AddRoot: &SourceRoot{Label: "src", Path: src}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Database.PostgresBinDir != filepath.Join(dir, "pg") || len(c.SourceRoots) != 1 || c.SourceRoots[0].Path != src || c.Agent.MaxWorkers != 2 {
		t.Fatalf("%+v", c)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !(strings.Index(text, `"state_dir"`) < strings.Index(text, `"agent"`) && strings.Index(text, `"agent"`) < strings.Index(text, `"database"`) && strings.Index(text, `"database"`) < strings.Index(text, `"source_roots"`)) {
		t.Fatalf("order changed:\n%s", text)
	}
	if strings.Contains(text, "listen") || strings.Contains(text, "drain_timeout") {
		t.Fatalf("defaults written:\n%s", text)
	}
	// An edit that makes the configuration invalid leaves the file as it was.
	if _, err := EditFile(path, tools, ConfigEdit{AddRoot: &SourceRoot{Label: "src", Path: filepath.Join(dir, "other")}}); err == nil {
		t.Fatal("duplicate label accepted")
	}
	if again, _ := os.ReadFile(path); string(again) != text {
		t.Fatal("invalid edit changed the file")
	}
}

// fakePostgres writes a bin directory whose pg_ctl reports version v.
func fakePostgres(t *testing.T, dir, version string, programs ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if programs == nil {
		programs = postgresPrograms
	}
	for _, p := range programs {
		body := "#!/bin/sh\nexit 0\n"
		if p == "pg_ctl" {
			body = "#!/bin/sh\necho 'pg_ctl (PostgreSQL) " + version + "'\n"
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Binary discovery: explicit directories must be usable (no silent
// fallback); otherwise the highest major version >= 14 wins, or the one
// matching an existing data directory; nothing found names what to do.
func TestFindPostgres(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes")
	}
	ctx := context.Background()
	base := t.TempDir()
	v16 := fakePostgres(t, filepath.Join(base, "pg16", "bin"), "16.4")
	v17 := fakePostgres(t, filepath.Join(base, "pg17", "bin"), "17.10")
	old := fakePostgres(t, filepath.Join(base, "pg13", "bin"), "13.9")
	partial := fakePostgres(t, filepath.Join(base, "partial"), "17.1", "initdb", "pg_ctl", "postgres")
	globs := []string{filepath.Join(base, "pg*", "bin")}

	b, err := FindPostgres(ctx, PostgresSearch{SystemGlobs: globs})
	if err != nil || b.Dir != v17 || b.Major != 17 || b.Version != "17.10" {
		t.Fatalf("highest: %+v %v", b, err)
	}
	if b, err = FindPostgres(ctx, PostgresSearch{SystemGlobs: globs, DataMajor: 16}); err != nil || b.Dir != v16 {
		t.Fatalf("data major: %+v %v", b, err)
	}
	if b, err = FindPostgres(ctx, PostgresSearch{PathEnv: v16, SystemGlobs: []string{}}); err != nil || b.Dir != v16 || b.Source != "PATH" {
		t.Fatalf("PATH: %+v %v", b, err)
	}
	if b, err = FindPostgres(ctx, PostgresSearch{Config: v16, Flag: v17, SystemGlobs: globs}); err != nil || b.Dir != v17 || b.Source != "--postgres-bin-dir" {
		t.Fatalf("flag first: %+v %v", b, err)
	}
	for _, dir := range []string{partial, old, filepath.Join(base, "missing")} {
		if _, err := FindPostgres(ctx, PostgresSearch{Config: dir, SystemGlobs: globs}); err == nil || !strings.Contains(err.Error(), "database.postgres_bin_dir "+dir) {
			t.Fatalf("explicit %s: %v", dir, err)
		}
	}
	_, err = FindPostgres(ctx, PostgresSearch{PathEnv: partial, SystemGlobs: []string{filepath.Join(base, "none*")}})
	if !errors.Is(err, ErrPostgresNotFound) || !strings.Contains(err.Error(), "sudo apt install postgresql") || !strings.Contains(err.Error(), "database.mode: external") {
		t.Fatalf("not found: %v", err)
	}
}

// A data directory of another major version is refused with the way out.
func TestClusterVersionMismatch(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	if err := os.MkdirAll(l.PostgresData(), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Cluster{Bin: PostgresBin{Dir: "/pg/17/bin", Major: 17}, Layout: l}
	if err := c.CheckVersion(); err != nil || c.Initialized() {
		t.Fatal("empty data directory", err)
	}
	if err := os.WriteFile(filepath.Join(l.PostgresData(), "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := c.CheckVersion()
	if err == nil || !strings.Contains(err.Error(), "created by PostgreSQL 16, found 17 binaries") || !strings.Contains(err.Error(), "postgres_bin_dir") {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(l.PostgresData(), "PG_VERSION")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.PostgresData(), "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DataMajor(); err == nil {
		t.Fatal("a non-empty directory without PG_VERSION was accepted")
	}
}
