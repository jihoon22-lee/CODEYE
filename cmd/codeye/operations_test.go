package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TC-230: the user unit runs codeye start/stop with the absolute
// executable and configuration, the explicit PATH, oneshot with
// RemainAfterExit (codeye start exits after spawning) and a stop budget
// covering the drain and the cancellation; systemd specials are escaped.
func TestServiceUnitText(t *testing.T) {
	unit := serviceUnit("/opt/co deye/codeye", "/home/a b/.config/codeye/personal.json", "/home/a/.local/bin:/usr/bin:/bin", 450*time.Second)
	for _, want := range []string{
		"Type=oneshot\n", "RemainAfterExit=yes\n",
		`ExecStart="/opt/co deye/codeye" start --config "/home/a b/.config/codeye/personal.json"` + "\n",
		`ExecStop="/opt/co deye/codeye" stop --config "/home/a b/.config/codeye/personal.json"` + "\n",
		`Environment="PATH=/home/a/.local/bin:/usr/bin:/bin"` + "\n",
		"TimeoutStopSec=450\n", "WantedBy=default.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, " up ") || strings.Contains(unit, "login") {
		t.Error("the unit must not print a login link into the journal")
	}
	escaped := serviceUnit("/x/%h$y", "/c", "/p%", time.Minute)
	if !strings.Contains(escaped, `"/x/%%h$$y" start`) || !strings.Contains(escaped, `PATH=/p%%"`) {
		t.Fatalf("specials not escaped:\n%s", escaped)
	}
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{"30d": 30 * 24 * time.Hour, "1d": 24 * time.Hour, "36h": 36 * time.Hour} {
		if got, err := parseAge(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"0d", "-1d", "xd", "", "1y"} {
		if _, err := parseAge(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The operator CLI refuses a recovery without the explicit confirmation and
// cleanup refuses ages under an hour, before touching any database.
func TestOperationsUsageErrors(t *testing.T) {
	ctx := context.Background()
	cases := map[string][]string{
		"no confirm": {"run", "recover", "--id", "8f3c3a3e-6d65-4c1b-9bb5-0d4f7a1e2b3c"},
		"no id":      {"run", "recover", "--confirm-cleanup"},
		"bad id":     {"run", "recover", "--id", "x", "--confirm-cleanup"},
		"short age":  {"cleanup", "--agent-state-dir", t.TempDir(), "--older-than", "10m"},
		"two homes":  {"cleanup", "--config", "/c.json", "--artifact-dir", "/a"},
		"service":    {"service"},
	}
	for name, args := range cases {
		var out, errb bytes.Buffer
		if code := run(ctx, args, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d (%s)", name, code, errb.String())
		}
	}
}

// TC-230: uninstall needs no configuration and removes only a unit that
// codeye wrote; a missing unit is not an error.
func TestServiceUninstallRemovesOnlyItsUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd user units are Linux only")
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PATH", t.TempDir()) // no systemctl: the file work only
	dir := filepath.Join(home, "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	uninstall := func(name string) int {
		var out, errb bytes.Buffer
		return run(ctx, []string{"service", "uninstall", "--name", name}, &out, &errb)
	}
	foreign := filepath.Join(dir, "other.service")
	if err := os.WriteFile(foreign, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := uninstall("other"); code != 1 {
		t.Fatalf("foreign unit: exit %d", code)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("a unit codeye did not write was removed")
	}
	ours := filepath.Join(dir, "codeye.service")
	if err := os.WriteFile(ours, []byte(serviceUnit("/x/codeye", "/c.json", "/bin", time.Minute)), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := uninstall("codeye"); code != 0 {
		t.Fatalf("own unit: exit %d", code)
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Fatal("own unit kept")
	}
	if code := uninstall("codeye"); code != 0 {
		t.Fatalf("missing unit: exit %d", code)
	}
}

// TC-229 (node only): a dry run lists old snapshot copies with no database
// and changes nothing; --apply removes them.
func TestCleanupNodeOnlyDryRunThenApply(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "data", "acquire-1")
	if err := os.MkdirAll(filepath.Join(dir, "snapshot"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot", "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{filepath.Join(dir, "snapshot", "a.go"), filepath.Join(dir, "snapshot"), dir} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	var out, errb bytes.Buffer
	if code := run(ctx, []string{"cleanup", "--agent-state-dir", state, "--older-than", "1d", "--format", "json"}, &out, &errb); code != 0 {
		t.Fatalf("dry run %d %s", code, errb.String())
	}
	var rep cleanupReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "linux" {
		// The agent lock cannot be checked here: snapshot copies are kept.
		if rep.NodeData == nil || len(rep.NodeData.Entries) != 0 || len(rep.Notes) == 0 {
			t.Fatalf("dry run %s", out.String())
		}
		return
	}
	if rep.Applied || rep.Artifacts != nil || rep.NodeData == nil || len(rep.NodeData.Entries) != 1 || rep.NodeData.Entries[0].Kind != "snapshot" {
		t.Fatalf("dry run %s", out.String())
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("dry run removed data")
	}
	out.Reset()
	if code := run(ctx, []string{"cleanup", "--agent-state-dir", state, "--older-than", "1d", "--apply"}, &out, &errb); code != 0 {
		t.Fatalf("apply %d %s", code, errb.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("apply kept the old snapshot copy")
	}
	if !strings.Contains(out.String(), "node data: removed 1 directories") {
		t.Fatal(out.String())
	}
}
