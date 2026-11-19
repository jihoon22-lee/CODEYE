package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/backup"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
)

func personalConfig(t *testing.T, base, listen string) *personal.Config {
	t.Helper()
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	raw := `{"schema_version":"0.1","state_dir":` + q(filepath.Join(base, "state")) + `,"listen":` + q(listen) +
		`,"database":{"mode":"external"},"source_roots":[{"label":"work","path":` + q(filepath.Join(base, "src")) + `}]` +
		`,"quota":{"logs_bytes":10,"workspace_bytes":1073741824},"backup_dir":` + q(filepath.Join(base, "backups")) + `}`
	cfg, err := personal.Parse([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeManifest(t *testing.T, dir string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"format": backup.Format, "created_at": at})
	if err := os.WriteFile(filepath.Join(dir, backup.ManifestFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TC-126 (W25, docs/OPERATIONS.md#codeye-doctor): doctor --config reports every storage area
// against its limit, the newest backup's age and the start/stop state; the
// section is absent without --config.
func TestOperationsStorageBackupAndProcesses(t *testing.T) {
	base := t.TempDir()
	// A listener nobody recorded: the listen address is in use.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := personalConfig(t, base, ln.Addr().String())
	layout := personal.Layout{Root: cfg.StateDir}
	if err := os.MkdirAll(layout.LogDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.LogDir(), "agent.log"), []byte(strings.Repeat("x", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ops := operations(cfg, now)
	want := map[string]string{"snapshot": AreaNoLimit, "workspace": AreaWithinLimit, "cache": AreaNoLimit, "logs": AreaOverLimit, "artifacts": AreaNoLimit}
	if len(ops.Storage) != 5 {
		t.Fatalf("areas %+v", ops.Storage)
	}
	for _, a := range ops.Storage {
		if a.Status != want[a.Area] || a.Bytes == nil {
			t.Errorf("%s: %+v", a.Area, a)
		}
		if (a.Area == "artifacts") != (a.Enforcement == "claim_refusal_database_accounting") {
			t.Errorf("%s enforcement %s", a.Area, a.Enforcement)
		}
		if a.Area == "logs" && (*a.Bytes != 64 || *a.LimitBytes != 10) {
			t.Errorf("logs %d/%d", *a.Bytes, *a.LimitBytes)
		}
	}
	if ops.Backup.Status != "none" || ops.Backup.Latest != nil {
		t.Fatalf("backup %+v", ops.Backup)
	}
	if personal.Supported() == nil {
		if ops.Processes.Status != "inconsistent" || strings.Join(ops.Processes.Issues, ",") != personal.IssueListenWithoutRecord ||
			ops.Processes.Controller != personal.ProcNone || ops.Processes.Agent != personal.ProcNone {
			t.Fatalf("processes %+v", ops.Processes)
		}
	} else if ops.Processes.Status != "unsupported" {
		t.Fatalf("processes %+v", ops.Processes)
	}
	ln.Close()

	// The newest complete set wins; an older one or a foreign directory does not.
	writeManifest(t, filepath.Join(cfg.BackupDir, "old"), now.Add(-10*24*time.Hour))
	writeManifest(t, filepath.Join(cfg.BackupDir, "new"), now.Add(-time.Hour))
	if err := os.MkdirAll(filepath.Join(cfg.BackupDir, "partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	b := backupStatus(cfg.BackupDir, now)
	if b.Status != "recent" || *b.Latest != "new" || *b.AgeSeconds != 3600 {
		t.Fatalf("recent %+v", b)
	}
	if b = backupStatus(cfg.BackupDir, now.Add(8*24*time.Hour)); b.Status != "stale" {
		t.Fatalf("stale %+v", b)
	}
	if b = backupStatus("", now); b.Status != "not_configured" {
		t.Fatalf("not configured %+v", b)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if b = backupStatus(file, now); b.Status != "unreadable" {
		t.Fatalf("unreadable %+v", b)
	}

	// A record whose pid now belongs to another process is stale.
	if personal.Supported() == nil {
		if err := layout.Prepare(); err != nil {
			t.Fatal(err)
		}
		if err := layout.WriteRecord(personal.Record{Role: personal.RoleAgent, PID: os.Getpid(), StartTime: 1, Executable: "/x", StartedAt: now}); err != nil {
			t.Fatal(err)
		}
		ops = operations(cfg, now)
		if ops.Processes.Status != "inconsistent" || ops.Processes.Agent != personal.ProcReused || !strings.Contains(strings.Join(ops.Processes.Issues, ","), personal.IssueStaleRecord) {
			t.Fatalf("stale record %+v", ops.Processes)
		}
		if err := layout.RemoveRecord(personal.RoleAgent); err != nil {
			t.Fatal(err)
		}
		// A running start/stop holds the personal lock: reported, not probed.
		unlock, err := layout.Lock()
		if err != nil {
			t.Fatal(err)
		}
		ops = operations(cfg, now)
		unlock()
		if ops.Processes.Status != "inconsistent" || strings.Join(ops.Processes.Issues, ",") != IssueOperationInProgress {
			t.Fatalf("busy %+v", ops.Processes)
		}
		// An agent holding its state lock without a record is found.
		if _, err := exec.LookPath("/usr/bin/flock"); err == nil {
			lock := filepath.Join(layout.AgentDir(), "agent.lock")
			if err := os.WriteFile(lock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			holder := exec.Command("/usr/bin/flock", lock, "/bin/sleep", "60")
			if err := holder.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if held, _ := layout.AgentLockHeld(); held {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("lock never observed held")
				}
				time.Sleep(20 * time.Millisecond)
			}
			ops = operations(cfg, now)
			if ops.Processes.Status != "inconsistent" || strings.Join(ops.Processes.Issues, ",") != personal.IssueAgentWithoutRecord {
				t.Fatalf("unrecorded agent %+v", ops.Processes)
			}
		}
	}

	// Only --config adds the section; text and JSON both carry it.
	runner := &fakeRunner{result: Result{Reason: "executable_missing"}}
	plain := Collect(context.Background(), Options{WorkDir: t.TempDir(), Timeout: time.Second}, runner)
	var out bytes.Buffer
	if err := plain.Write(&out, "json"); err != nil || strings.Contains(out.String(), "operations") {
		t.Fatalf("operations without --config: %v", err)
	}
	full := Collect(context.Background(), Options{WorkDir: t.TempDir(), Timeout: time.Second, Personal: cfg}, runner)
	out.Reset()
	if err := full.Write(&out, "json"); err != nil || !strings.Contains(out.String(), `"operations"`) {
		t.Fatalf("operations missing: %v", err)
	}
	out.Reset()
	if err := full.Write(&out, "text"); err != nil || !strings.Contains(out.String(), "storage logs       over_limit") {
		t.Fatalf("text %q %v", out.String(), err)
	}
}
