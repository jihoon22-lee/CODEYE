package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/pgtool"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// instanceDB is the database of a service installation. In managed mode
// it is the cluster in <state_dir>/postgres; CODEYE_DATABASE_URL of this
// process then points at it, so every store opened afterwards uses it.
type instanceDB struct {
	managed bool
	cluster instance.Cluster
	port    int
	running bool
	// started: this command started the server (it may stop it again).
	started bool
	// binDir holds pg_dump and pg_restore for backup and restore.
	binDir string
}

type dbOptions struct {
	// start initializes (when needed) and starts the managed server and
	// creates the codeye database. Without it a stopped server stays stopped.
	start bool
	// flagBinDir is `codeye up --postgres-bin-dir`.
	flagBinDir string
	// notices receives what was ignored from the environment and what was
	// initialized or started.
	notices io.Writer
}

// instanceDatabase resolves the installation's database. External mode only
// checks that CODEYE_DATABASE_URL is set when o.start asks for a database.
func instanceDatabase(ctx context.Context, cfg *instance.Config, layout instance.Layout, o dbOptions) (*instanceDB, error) {
	if o.notices == nil {
		o.notices = io.Discard
	}
	if !cfg.Managed() {
		if o.start && os.Getenv("CODEYE_DATABASE_URL") == "" {
			return nil, errors.New("CODEYE_DATABASE_URL is not set (database.mode external)")
		}
		return &instanceDB{binDir: cfg.Database.PostgresBinDir}, nil
	}
	clearDatabaseEnv(o.notices)
	db := &instanceDB{managed: true}
	major, err := layout.DataMajor()
	if err != nil {
		return nil, err
	}
	if major == 0 && !o.start {
		return db, nil
	}
	bin, err := instance.FindPostgres(ctx, instance.PostgresSearch{Flag: o.flagBinDir, Config: cfg.Database.PostgresBinDir,
		Env: os.Getenv("CODEYE_POSTGRES_BIN_DIR"), PathEnv: os.Getenv("PATH"), DataMajor: major})
	if err != nil {
		return nil, err
	}
	db.cluster = instance.Cluster{Bin: bin, Layout: layout}
	db.binDir = bin.Dir
	if err := db.cluster.CheckVersion(); err != nil {
		return nil, err
	}
	if db.port, db.running, err = db.cluster.Status(ctx); err != nil {
		return nil, err
	}
	if !db.running && o.start {
		if major == 0 {
			if err := db.cluster.Init(ctx); err != nil {
				return nil, err
			}
			fmt.Fprintf(o.notices, "initialized a PostgreSQL %s cluster in %s (binaries: %s)\n", bin.Version, layout.PostgresData(), bin.Dir)
		}
		if db.port, err = db.cluster.Start(ctx); err != nil {
			return nil, err
		}
		db.running, db.started = true, true
		fmt.Fprintf(o.notices, "PostgreSQL %s started on 127.0.0.1:%d (log %s)\n", bin.Version, db.port, layout.PostgresLog())
	}
	if !db.running {
		return db, nil
	}
	if o.start {
		if _, err := db.cluster.EnsureDatabase(ctx, db.port); err != nil {
			return nil, err
		}
	}
	url, err := db.cluster.URL(db.port, instance.ManagedDatabase)
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("CODEYE_DATABASE_URL", url); err != nil {
		return nil, errors.New("cannot set the database URL")
	}
	return db, nil
}

// stop shuts a running managed server down.
func (db *instanceDB) stop(ctx context.Context) error {
	if db == nil || !db.managed || !db.running {
		return nil
	}
	if err := db.cluster.Stop(ctx); err != nil {
		return err
	}
	db.running = false
	return nil
}

// clearDatabaseEnv removes PG* variables and an inherited
// CODEYE_DATABASE_URL from this process: managed mode connects only to its
// own cluster, and the store refuses to open while PG* variables are set.
func clearDatabaseEnv(notices io.Writer) {
	var dropped []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(key), "PG") || key == "CODEYE_DATABASE_URL" {
			_ = os.Unsetenv(key)
			dropped = append(dropped, key)
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		fmt.Fprintf(notices, "note: ignoring %s from the environment (database.mode managed uses its own PostgreSQL)\n", strings.Join(dropped, ", "))
	}
}

// backupDirOf is backup_dir, or <state_dir>-backups next to the state directory.
func backupDirOf(cfg *instance.Config) string {
	if cfg.BackupDir != "" {
		return cfg.BackupDir
	}
	return cfg.StateDir + "-backups"
}

// ensureSchema brings the schema to this binary's version. In managed mode
// an outdated schema is migrated automatically; when it already holds data
// a pg_dump copy is written to the backup directory first. External mode
// keeps the explicit `codeye migrate`.
func ensureSchema(ctx context.Context, store *postgres.Store, cfg *instance.Config, db *instanceDB, out io.Writer) error {
	st, err := store.SchemaStatus(ctx)
	if err != nil {
		return fmt.Errorf("cannot read database schema status: %w", err)
	}
	switch {
	case st.State == "current":
		return nil
	case st.State == "newer":
		return fmt.Errorf("database schema is newer than this codeye binary (applied %d, this binary expects %d); use the newer binary or restore a matching backup", st.Applied, st.Latest)
	case st.State != "outdated" || !db.managed:
		return fmt.Errorf("database schema is %s (applied %d, this binary expects %d); back it up and run \"codeye migrate\" explicitly", st.State, st.Applied, st.Latest)
	}
	url := os.Getenv("CODEYE_DATABASE_URL")
	if st.Applied > 0 {
		dump, err := preMigrationDump(ctx, cfg, db, url)
		if err != nil {
			return fmt.Errorf("database schema is outdated but the copy before migrating failed (%v); nothing was changed", err)
		}
		fmt.Fprintf(out, "saved the database before migrating: %s\n", dump)
	}
	mctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := postgres.Migrate(mctx, url); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	if st.Applied == 0 {
		fmt.Fprintf(out, "database schema created (migration %d)\n", st.Latest)
	} else {
		fmt.Fprintf(out, "database schema migrated from %d to %d\n", st.Applied, st.Latest)
	}
	return nil
}

// preMigrationDump writes pg_dump --format=custom of the whole database to
// <backup dir>/codeye-pre-migrate-<UTC>/database.dump (0700 directory, 0600
// file). Restore it with pg_restore into an empty database.
func preMigrationDump(ctx context.Context, cfg *instance.Config, db *instanceDB, url string) (string, error) {
	pgDump, err := pgtool.Program(db.binDir, "pg_dump")
	if err != nil {
		return "", err
	}
	conn, err := pgtool.ParseURL(url)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(backupDirOf(cfg), "codeye-pre-migrate-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errors.New("cannot create " + dir)
	}
	file := filepath.Join(dir, "database.dump")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", errors.New("cannot create " + file)
	}
	_ = f.Close()
	if err := conn.Run(ctx, pgDump, []string{"--format=custom", "--file=" + file, "--dbname=" + conn.DBName}, 30*time.Minute); err != nil {
		_ = os.Remove(file)
		return "", err
	}
	return file, nil
}
