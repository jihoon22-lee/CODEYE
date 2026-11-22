package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/backup"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

// `codeye backup` and `codeye restore` (docs/OPERATIONS.md#백업과-복원) for the service
// installation: the database is the managed cluster (started for the
// command when it is stopped) or CODEYE_DATABASE_URL, the artifact store is
// <state_dir>/artifacts, and pg_dump/pg_restore come from the managed
// cluster's binaries or database.postgres_bin_dir.

func cmdBackup(ctx context.Context, args []string, out, stderr io.Writer) int {
	var output, format string
	cfg, layout, ok := loadInstanceConfig("backup", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&output, "output", "", "")
		fs.StringVar(&format, "format", "text", "")
	})
	if !ok || (format != "text" && format != "json") {
		return 2
	}
	if output == "" {
		output = filepath.Join(backupDirOf(cfg), "codeye-"+time.Now().UTC().Format("20060102T150405Z"))
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		fmt.Fprintln(stderr, "--output cannot be resolved")
		return 2
	}
	if err := instance.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := layout.Prepare(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := layout.Lock()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	user := ""
	if id, err := layout.ReadIdentity(); err == nil && id != nil {
		user = id.UserID
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	store, db, err := instanceStore(opCtx, cfg, layout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer stopStartedDB(db)
	defer store.Close()
	m, err := backup.Create(opCtx, store, backup.Options{DatabaseURL: os.Getenv("CODEYE_DATABASE_URL"), PostgresBinDir: db.binDir,
		ArtifactDir: layout.ArtifactDir(), Output: abs, AdminUserID: user})
	if err != nil {
		fmt.Fprintln(stderr, "backup failed:", err)
		return 1
	}
	if err := writeBackupReceipt(out, format, abs, m); err != nil {
		fmt.Fprintf(stderr, "backup saved at %s, but writing the command output failed: %v\n", abs, err)
		return 1
	}
	return 0
}

func writeBackupReceipt(out io.Writer, format, destination string, m backup.Manifest) error {
	if format == "json" {
		return printJSON(out, map[string]any{"output": destination, "manifest": m})
	}
	_, err := fmt.Fprintf(out, "backup written to %s\n"+
		"  database: migration %d, PostgreSQL %s, dump %d bytes sha256 %s\n"+
		"  artifacts: %d contents, %d bytes (finalized at the same snapshot)\n"+
		"  rows: projects %d, runs %d, reports %d, evidence %d, schedules %d\n",
		destination, m.MigrationVersion, m.ServerVersion, m.Database.Size, m.Database.SHA256,
		m.Artifacts.Count, m.Artifacts.Bytes, m.Rows["projects"], m.Rows["runs"],
		m.Rows["reports"], m.Rows["evidence"], m.Rows["schedules"])
	return err
}

func cmdRestore(ctx context.Context, args []string, out, stderr io.Writer) int {
	var from, format string
	cfg, layout, ok := loadInstanceConfig("restore", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&from, "from", "", "")
		fs.StringVar(&format, "format", "text", "")
	})
	if !ok || from == "" || (format != "text" && format != "json") {
		if ok {
			fmt.Fprintln(stderr, "--from DIR is required")
		}
		return 2
	}
	from, err := filepath.Abs(from)
	if err != nil {
		fmt.Fprintln(stderr, "--from cannot be resolved")
		return 2
	}
	if err := instance.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := layout.Prepare(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := layout.Lock()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	state := layout.Inspect(probedListen(cfg), true)
	if state.Running(instance.RoleController) || state.Running(instance.RoleAgent) || len(state.Issues) > 0 {
		fmt.Fprintln(stderr, "the target installation is running or inconsistent; run codeye stop and check codeye status first")
		return 1
	}
	if node, err := layout.AgentNodeID(); err != nil || node != "" {
		fmt.Fprintln(stderr, "the target state_dir has an enrolled agent; restore needs a fresh state_dir")
		return 1
	}
	if id, err := layout.ReadIdentity(); err != nil || id != nil {
		fmt.Fprintln(stderr, "the target state_dir already has admin.json; restore needs a fresh state_dir")
		return 1
	}
	v, err := backup.Verify(from)
	if err != nil {
		fmt.Fprintln(stderr, "backup verification failed:", err)
		return 1
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	store, db, err := instanceStore(opCtx, cfg, layout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer stopStartedDB(db)
	defer store.Close()
	rep, err := backup.Restore(opCtx, store, v, backup.RestoreOptions{DatabaseURL: os.Getenv("CODEYE_DATABASE_URL"),
		PostgresBinDir: db.binDir, ArtifactDir: layout.ArtifactDir()})
	if err != nil {
		fmt.Fprintln(stderr, "restore failed:", err)
		return 1
	}
	if v.Manifest.AdminUserID != "" {
		if err := layout.WriteIdentity(v.Manifest.AdminUserID); err != nil {
			fmt.Fprintln(stderr, "restored, but cannot write admin.json:", err)
			return 1
		}
	}
	if format == "json" {
		printJSON(out, rep)
		return 0
	}
	f := rep.Fence
	fmt.Fprintf(out, "restored %s (created %s, migration %d)\n", from, v.Manifest.CreatedAt.Format(time.RFC3339), rep.Migration)
	fmt.Fprintf(out, "  verified: dump and %d artifact contents by sha256; every restored artifact row has its bytes\n", rep.Blobs)
	fmt.Fprintf(out, "  rows: projects %d, runs %d, reports %d, evidence %d, finding reviews %d, schedules %d\n", rep.Rows["projects"], rep.Rows["runs"], rep.Rows["reports"], rep.Rows["evidence"], rep.Rows["finding_reviews"], rep.Rows["schedules"])
	fmt.Fprintf(out, "  fenced: %d nodes revoked, %d sessions revoked, %d leased attempts -> recovery_required (%d reservations kept)\n", f.NodesRevoked, f.SessionsRevoked, f.AttemptsFenced, f.ReservationsHeld)
	fmt.Fprintln(out, "next: codeye up (the local agent enrolls as a new node)")
	return 0
}

// stopStartedDB stops a managed server that this command started.
func stopStartedDB(db *instanceDB) {
	if db != nil && db.started {
		_ = db.stop(context.Background())
	}
}
