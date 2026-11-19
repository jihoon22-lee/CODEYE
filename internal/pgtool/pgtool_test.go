package pgtool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// W25 (docs/OPERATIONS.md#백업과-복원): the URL rules match the controller, credentials reach
// the child only through the environment and never appear in errors.
func TestParseURLEnvironmentOnly(t *testing.T) {
	c, err := ParseURL("postgresql://backup:s3cret%21@127.0.0.1:6543/codeye?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(c.env, "\n")
	for _, want := range []string{"PGHOST=127.0.0.1", "PGPORT=6543", "PGUSER=backup", "PGPASSWORD=s3cret!", "PGDATABASE=codeye", "PGSSLMODE=disable", "PGPASSFILE=" + os.DevNull} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s", want)
		}
	}
	if c.DBName != "codeye" {
		t.Fatal(c.DBName)
	}
	if c, err := ParseURL("postgres://u@db.internal.invalid/codeye"); err != nil || !strings.Contains(strings.Join(c.env, "\n"), "PGSSLMODE=verify-full") {
		t.Fatalf("default TLS mode: %v", err)
	}
	for _, bad := range []string{
		"mysql://u:p@127.0.0.1/db",
		"postgresql://u:p@db.internal.invalid/db?sslmode=disable", // plaintext off loopback
		"postgresql://u:p@127.0.0.1/db?sslmode=require",
		"postgresql://u:p@127.0.0.1/db?options=-c",
		"postgresql://u:p@127.0.0.1/host=evil",
		"postgresql://u:p@127.0.0.1/",
		"postgresql://:p@127.0.0.1/db",
		"postgresql://u:p@h1,h2/db",
	} {
		if _, err := ParseURL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		} else if strings.Contains(err.Error(), "p@") || strings.Contains(err.Error(), "evil") {
			t.Errorf("error echoes the URL: %v", err)
		}
	}
}

func TestProgramNeverSearchesPath(t *testing.T) {
	if _, err := Program("", "pg_dump"); err == nil {
		t.Fatal("empty bin dir accepted")
	}
	if _, err := Program("bin", "pg_dump"); err == nil {
		t.Fatal("relative bin dir accepted")
	}
	if _, err := Program(t.TempDir(), "pg_dump"); err == nil {
		t.Fatal("missing program accepted")
	}
}

func TestRunRedactsPassword(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "pg_fake")
	// Echo the environment password on stderr and fail, like a client error.
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"FATAL: password $PGPASSWORD rejected\" >&2\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := ParseURL("postgresql://u:topsecret@127.0.0.1/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	err = c.Run(context.Background(), script, nil, 10*time.Second)
	if err == nil || strings.Contains(err.Error(), "topsecret") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("not redacted: %v", err)
	}
}
