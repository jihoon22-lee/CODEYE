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
	"github.com/jihoon22-lee/CODEYE/internal/personal"
)

// `codeye backup` and `codeye restore` (docs/OPERATIONS.md#백업과-복원) for a personal
// installation: the database is the managed cluster (started for the
// command when it is stopped) or CODEYE_DATABASE_URL, the artifact store is
// <state_dir>/artifacts, and pg_dump/pg_restore come from the managed
// cluster's binaries or database.postgres_bin_dir.

func personalBackup(ctx context.Context, args []string, out, stderr io.Writer) int {
	var output, format string
	cfg, layout, ok := loadPersonalConfig("backup", args, stderr, func(fs *flag.FlagSet) {
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
	if err := personal.Supported(); err != nil {
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
	store, db, err := personalStore(opCtx, cfg, layout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer stopStartedDB(db)
	defer store.Close()
	m, err := backup.Create(opCtx, store, backup.Options{DatabaseURL: os.Getenv("CODEYE_DATABASE_URL"), PostgresBinDir: db.binDir,
		ArtifactDir: layout.ArtifactDir(), Output: abs, PersonalUserID: user})
	if err != nil {
		fmt.Fprintln(stderr, "backup failed:", err)
		return 1
	}
	if format == "json" {
		printJSON(out, map[string]any{"output": abs, "manifest": m})
		return 0
	}
	fmt.Fprintf(out, "backup written to %s\n", abs)
	fmt.Fprintf(out, "  database: migration %d, PostgreSQL %s, dump %d bytes sha256 %s\n", m.MigrationVersion, m.ServerVersion, m.Database.Size, m.Database.SHA256)
	fmt.Fprintf(out, "  artifacts: %d contents, %d bytes (finalized at the same snapshot)\n", m.Artifacts.Count, m.Artifacts.Bytes)
	fmt.Fprintf(out, "  rows: projects %d, runs %d, reports %d, evidence %d, schedules %d\n", m.Rows["projects"], m.Rows["runs"], m.Rows["reports"], m.Rows["evidence"], m.Rows["schedules"])
	return 0
}

func personalRestore(ctx context.Context, args []string, out, stderr io.Writer) int {
	var from, format string
	cfg, layout, ok := loadPersonalConfig("restore", args, stderr, func(fs *flag.FlagSet) {
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
	if err := personal.Supported(); err != nil {
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
	state := layout.Inspect(cfg.Listen, true)
	if state.Running(personal.RoleController) || state.Running(personal.RoleAgent) || len(state.Issues) > 0 {
		fmt.Fprintln(stderr, "the target installation is running or inconsistent; run codeye stop and check codeye status first")
		return 1
	}
	if node, err := layout.AgentNodeID(); err != nil || node != "" {
		fmt.Fprintln(stderr, "the target state_dir has an enrolled agent; restore needs a fresh state_dir")
		return 1
	}
	if id, err := layout.ReadIdentity(); err != nil || id != nil {
		fmt.Fprintln(stderr, "the target state_dir already has personal.json; restore needs a fresh state_dir")
		return 1
	}
	v, err := backup.Verify(from)
	if err != nil {
		fmt.Fprintln(stderr, "backup verification failed:", err)
		return 1
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	store, db, err := personalStore(opCtx, cfg, layout, stderr)
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
	if v.Manifest.PersonalUserID != "" {
		if err := layout.WriteIdentity(v.Manifest.PersonalUserID); err != nil {
			fmt.Fprintln(stderr, "restored, but cannot write personal.json:", err)
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
func stopStartedDB(db *personalDB) {
	if db != nil && db.started {
		_ = db.stop(context.Background())
	}
}
