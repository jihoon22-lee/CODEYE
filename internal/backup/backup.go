// Package backup writes and restores a CODEYE backup set (W25, docs/OPERATIONS.md#백업과-복원):
// a PostgreSQL dump and the finalized artifact contents of the SAME logical
// point, plus a manifest with versions, hashes and the migration version.
//
// Consistency: the database transaction that lists the artifacts exports its
// snapshot, pg_dump reads exactly that snapshot (--snapshot), and the blobs
// listed in it are copied and re-hashed afterwards. The artifact store is
// content-addressed and append-only (nothing deletes blobs), so a blob that
// a row in the snapshot references stays readable; a blob finalized after
// the snapshot is neither in the dump nor in the backup.
//
// Restore accepts only an empty database and an empty artifact root,
// verifies the whole set before touching them, re-checks every restored
// artifact row against the copied bytes, and fences the past (FenceRestored)
// before any controller can serve it.
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/pgtool"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

// Format of manifest.json.
const Format = "codeye-backup/0.1"

// Files of a backup set.
const (
	ManifestFile  = "manifest.json"
	DumpFile      = "database.dump"
	ArtifactsFile = "artifacts.json"
	BlobDir       = "artifacts"
)

// Manifest describes a backup set. It holds no credential, host or path.
type Manifest struct {
	Format           string           `json:"format"`
	CreatedAt        time.Time        `json:"created_at"`
	Consistency      string           `json:"consistency"`
	MigrationVersion int              `json:"migration_version"`
	CodeyeMigrations int              `json:"codeye_binary_migrations"`
	ServerVersion    string           `json:"postgres_server_version"`
	PgDumpVersion    string           `json:"pg_dump_version"`
	Database         FileEntry        `json:"database"`
	Artifacts        ArtifactsEntry   `json:"artifacts"`
	Rows             map[string]int64 `json:"rows"`
	PersonalUserID   string           `json:"personal_user_id,omitempty"`
}

// FileEntry is one file of the set with its SHA-256.
type FileEntry struct {
	File   string `json:"file"`
	Format string `json:"format,omitempty"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size_bytes"`
}

// ArtifactsEntry summarizes the finalized artifact contents.
type ArtifactsEntry struct {
	List         FileEntry `json:"list"`
	Count        int       `json:"count"`
	Bytes        int64     `json:"bytes"`
	MetadataOnly int64     `json:"metadata_only_rows"`
}

// Options of Create.
type Options struct {
	DatabaseURL    string
	PostgresBinDir string
	ArtifactDir    string
	Output         string
	PersonalUserID string
	// AfterSnapshot (tests) runs after the snapshot is exported and the blob
	// list read, before pg_dump starts.
	AfterSnapshot func()
	Timeout       time.Duration
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Create writes a backup set to opts.Output (absent or empty; created 0700,
// files 0600). On failure everything it wrote is removed.
func Create(ctx context.Context, store *postgres.Store, opts Options) (m Manifest, err error) {
	if opts.Timeout <= 0 {
		opts.Timeout = time.Hour
	}
	conn, err := pgtool.ParseURL(opts.DatabaseURL)
	if err != nil {
		return m, err
	}
	pgDump, err := pgtool.Program(opts.PostgresBinDir, "pg_dump")
	if err != nil {
		return m, err
	}
	dumpVersion, dumpMajor, err := pgtool.Version(ctx, pgDump)
	if err != nil {
		return m, err
	}
	if !filepath.IsAbs(opts.ArtifactDir) || !filepath.IsAbs(opts.Output) {
		return m, errors.New("artifact and output directories must be absolute")
	}
	if err := prepareOutput(opts.Output); err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			_ = removeContents(opts.Output)
		}
	}()
	blobRoot := filepath.Join(opts.Output, BlobDir)
	err = store.WithBackupPoint(ctx, func(p postgres.BackupPoint) error {
		if major := serverMajor(p.ServerVersion); major > dumpMajor {
			return fmt.Errorf("pg_dump %s is older than the PostgreSQL server %s", dumpVersion, p.ServerVersion)
		}
		if opts.AfterSnapshot != nil {
			opts.AfterSnapshot()
		}
		// Created 0600 first: pg_dump then writes into it without widening it.
		if err := writeFile(filepath.Join(opts.Output, DumpFile), nil); err != nil {
			return err
		}
		args := []string{"--format=custom", "--no-owner", "--no-privileges", "--snapshot=" + p.SnapshotID,
			"--schema=" + p.Schema, "--file=" + filepath.Join(opts.Output, DumpFile), "--dbname=" + conn.DBName}
		if err := conn.Run(ctx, pgDump, args, opts.Timeout); err != nil {
			return err
		}
		var total int64
		for _, b := range p.Blobs {
			if err := copyBlob(opts.ArtifactDir, blobRoot, b); err != nil {
				return err
			}
			total += b.Size
		}
		m = Manifest{Format: Format, Consistency: "exported_snapshot", MigrationVersion: p.MigrationVersion,
			ServerVersion: p.ServerVersion, PgDumpVersion: dumpVersion, Rows: p.Rows, PersonalUserID: opts.PersonalUserID}
		m.Artifacts.Count, m.Artifacts.Bytes, m.Artifacts.MetadataOnly = len(p.Blobs), total, p.MetadataOnly
		list, err := json.MarshalIndent(p.Blobs, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(opts.Output, ArtifactsFile), append(list, '\n')); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return m, err
	}
	latest, err := migrations.LatestVersion()
	if err != nil {
		return m, err
	}
	m.CodeyeMigrations = latest
	if m.Database, err = entry(opts.Output, DumpFile); err != nil {
		return m, err
	}
	m.Database.Format = "pg_dump_custom"
	if m.Artifacts.List, err = entry(opts.Output, ArtifactsFile); err != nil {
		return m, err
	}
	m.CreatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	// The manifest is written last: a set without it is incomplete.
	return m, writeFile(filepath.Join(opts.Output, ManifestFile), append(raw, '\n'))
}

func serverMajor(v string) int {
	var major int
	fmt.Sscanf(v, "%d", &major)
	return major
}

// prepareOutput creates dir (0700) or accepts an existing empty directory.
func prepareOutput(dir string) error {
	st, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return errors.New("cannot create the backup parent directory")
		}
		return os.Mkdir(dir, 0o700)
	}
	if err != nil || !st.IsDir() {
		return errors.New("--output exists and is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("cannot read --output")
	}
	if len(entries) != 0 {
		return errors.New("--output is not empty; a backup never overwrites files")
	}
	return os.Chmod(dir, 0o700)
}

func removeContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	return nil
}

// blobPath is the content-addressed location of sha under root.
func blobPath(root, sha string) string {
	return filepath.Join(root, filepath.FromSlash(artifactstore.Key(sha)))
}

// copyBlob copies one blob from the store into the backup, hashing the bytes
// it writes; a missing, short or altered source fails the backup.
func copyBlob(storeDir, blobRoot string, b postgres.Blob) error {
	if !shaPattern.MatchString(b.SHA256) {
		return errors.New("artifact row has an invalid sha256")
	}
	src, err := os.Open(blobPath(storeDir, b.SHA256))
	if err != nil {
		return fmt.Errorf("artifact %s referenced by the database is missing from the artifact store", b.SHA256)
	}
	defer src.Close()
	dst := blobPath(blobRoot, b.SHA256)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, b.Size+1))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != b.Size || hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
		return fmt.Errorf("artifact %s in the artifact store does not match its recorded size and sha256", b.SHA256)
	}
	return nil
}

func writeFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func entry(dir, name string) (FileEntry, error) {
	sha, n, err := hashFile(filepath.Join(dir, name))
	return FileEntry{File: name, SHA256: sha, Size: n}, err
}

// --- verification and restore -------------------------------------------------

// Verified is a backup set whose every file matched its manifest.
type Verified struct {
	Dir      string
	Manifest Manifest
	Blobs    []postgres.Blob
}

// Verify reads dir's manifest and checks the dump, the artifact list and
// every blob (size and SHA-256) without touching any target.
func Verify(dir string) (Verified, error) {
	v := Verified{Dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return v, errors.New("backup manifest.json is missing (incomplete or not a backup set)")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v.Manifest); err != nil || v.Manifest.Format != Format {
		return v, errors.New("backup manifest.json is not a " + Format + " manifest")
	}
	m := v.Manifest
	if m.Database.File != DumpFile || m.Artifacts.List.File != ArtifactsFile || m.MigrationVersion < 1 {
		return v, errors.New("backup manifest.json is inconsistent")
	}
	for _, e := range []FileEntry{m.Database, m.Artifacts.List} {
		got, err := entry(dir, e.File)
		if err != nil || got.SHA256 != e.SHA256 || got.Size != e.Size {
			return v, fmt.Errorf("backup file %s does not match the manifest", e.File)
		}
	}
	list, err := os.ReadFile(filepath.Join(dir, ArtifactsFile))
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(list, &v.Blobs); err != nil || len(v.Blobs) != m.Artifacts.Count {
		return v, errors.New("backup artifacts.json does not match the manifest")
	}
	var total int64
	for i, b := range v.Blobs {
		if !shaPattern.MatchString(b.SHA256) || (i > 0 && v.Blobs[i-1].SHA256 >= b.SHA256) {
			return v, errors.New("backup artifacts.json is malformed")
		}
		sha, n, err := hashFile(blobPath(filepath.Join(dir, BlobDir), b.SHA256))
		if err != nil || sha != b.SHA256 || n != b.Size {
			return v, fmt.Errorf("backup artifact %s is missing or altered", b.SHA256)
		}
		total += b.Size
	}
	if total != m.Artifacts.Bytes {
		return v, errors.New("backup artifact bytes do not match the manifest")
	}
	return v, nil
}

// RestoreOptions of Restore.
type RestoreOptions struct {
	DatabaseURL    string
	PostgresBinDir string
	ArtifactDir    string
	Timeout        time.Duration
}

// RestoreReport is what Restore did.
type RestoreReport struct {
	Manifest  Manifest             `json:"manifest"`
	Rows      map[string]int64     `json:"rows"`
	Blobs     int                  `json:"artifacts_restored"`
	Fence     postgres.FenceReport `json:"fence"`
	Migration int                  `json:"migration_version"`
}

// ArtifactRootEmpty reports whether dir is absent or holds only empty
// directories (what artifactstore.Open creates).
func ArtifactRootEmpty(dir string) (bool, error) {
	empty := true
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			empty = false
			return fs.SkipAll
		}
		return nil
	})
	return empty, err
}

// Restore restores the verified set v into an empty database (store, which
// must be connected to opts.DatabaseURL) and an empty artifact root. Order:
// target checks → blobs (artifactstore.Put re-verifies each) → pg_restore in
// one transaction → re-check every restored artifact row against the bytes
// and the row counts against the manifest → FenceRestored.
func Restore(ctx context.Context, store *postgres.Store, v Verified, opts RestoreOptions) (r RestoreReport, err error) {
	r = RestoreReport{Manifest: v.Manifest}
	if opts.Timeout <= 0 {
		opts.Timeout = time.Hour
	}
	latest, err := migrations.LatestVersion()
	if err != nil {
		return r, err
	}
	if v.Manifest.MigrationVersion > latest {
		return r, fmt.Errorf("the backup has migration version %d; this codeye binary knows only %d (use a newer binary)", v.Manifest.MigrationVersion, latest)
	}
	conn, err := pgtool.ParseURL(opts.DatabaseURL)
	if err != nil {
		return r, err
	}
	pgRestore, err := pgtool.Program(opts.PostgresBinDir, "pg_restore")
	if err != nil {
		return r, err
	}
	if _, _, err := pgtool.Version(ctx, pgRestore); err != nil {
		return r, err
	}
	empty, err := store.SchemaEmpty(ctx)
	if err != nil {
		return r, fmt.Errorf("cannot inspect the target database: %w", err)
	}
	if !empty {
		return r, errors.New("the target database is not empty; restore only into a new, empty database")
	}
	if !filepath.IsAbs(opts.ArtifactDir) {
		return r, errors.New("the artifact directory must be absolute")
	}
	if ok, err := ArtifactRootEmpty(opts.ArtifactDir); err != nil || !ok {
		return r, errors.New("the target artifact directory is not empty; restore only into an empty artifact directory")
	}
	art, err := artifactstore.Open(opts.ArtifactDir)
	if err != nil {
		return r, err
	}
	// The artifact root was empty: on any failure it is emptied again, so a
	// retry finds the same target. A database that pg_restore committed is
	// reported and must be recreated by the operator.
	restored := false
	defer func() {
		if err != nil {
			_ = removeContents(opts.ArtifactDir)
			if restored {
				err = fmt.Errorf("%w; the database already holds the restored rows: drop and recreate it before retrying", err)
			}
		}
	}()
	for _, b := range v.Blobs {
		f, err := os.Open(blobPath(filepath.Join(v.Dir, BlobDir), b.SHA256))
		if err != nil {
			return r, err
		}
		_, err = art.Put(f, b.SHA256, b.Size)
		_ = f.Close()
		if err != nil {
			return r, fmt.Errorf("artifact %s failed verification while restoring", b.SHA256)
		}
		r.Blobs++
	}
	list, cleanup, err := restoreList(ctx, conn, pgRestore, filepath.Join(v.Dir, DumpFile))
	if err != nil {
		return r, err
	}
	defer cleanup()
	args := []string{"--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges", "--use-list=" + list, "--dbname=" + conn.DBName, filepath.Join(v.Dir, DumpFile)}
	if err := conn.Run(ctx, pgRestore, args, opts.Timeout); err != nil {
		return r, fmt.Errorf("database restore failed (the database was left unchanged: pg_restore ran in one transaction): %w", err)
	}
	restored = true
	st, err := store.ReadRestoredState(ctx)
	if err != nil {
		return r, fmt.Errorf("cannot read the restored database: %w", err)
	}
	r.Rows, r.Migration = st.Rows, st.MigrationVersion
	if st.MigrationVersion != v.Manifest.MigrationVersion {
		return r, errors.New("the restored migration version differs from the manifest")
	}
	for _, t := range sortedKeys(v.Manifest.Rows) {
		if st.Rows[t] != v.Manifest.Rows[t] {
			return r, fmt.Errorf("restored %s rows (%d) differ from the backup point (%d)", t, st.Rows[t], v.Manifest.Rows[t])
		}
	}
	// Every finalized artifact row of the restored database must have its
	// verified bytes in the new artifact root.
	if len(st.Blobs) != len(v.Blobs) {
		return r, errors.New("restored artifact rows differ from the backup artifact list")
	}
	for i, b := range st.Blobs {
		if b != v.Blobs[i] {
			return r, errors.New("restored artifact rows differ from the backup artifact list")
		}
		sha, n, err := hashFile(blobPath(opts.ArtifactDir, b.SHA256))
		if err != nil || sha != b.SHA256 || n != b.Size {
			return r, fmt.Errorf("restored artifact %s is missing or altered", b.SHA256)
		}
	}
	if r.Fence, err = store.FenceRestored(ctx); err != nil {
		return r, fmt.Errorf("restore incomplete: %w", err)
	}
	return r, nil
}

// restoreList writes the dump's table of contents without the entries that
// create (or comment) the dumped schema itself: the empty target database
// already has its current schema (pg_dump --schema emits CREATE SCHEMA).
func restoreList(ctx context.Context, conn pgtool.Conn, pgRestore, dump string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "codeye-restore-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	full := filepath.Join(dir, "toc.list")
	if err := conn.Run(ctx, pgRestore, []string{"--list", "--file=" + full, dump}, 5*time.Minute); err != nil {
		cleanup()
		return "", nil, err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	var kept []string
	dropped := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if schemaEntry.MatchString(line) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	if dropped == 0 {
		cleanup()
		return "", nil, errors.New("the dump's table of contents has no schema entry (not a CODEYE backup dump)")
	}
	filtered := filepath.Join(dir, "restore.list")
	if err := writeFile(filtered, []byte(strings.Join(kept, "\n"))); err != nil {
		cleanup()
		return "", nil, err
	}
	return filtered, cleanup, nil
}

// schemaEntry matches "N; OID OID SCHEMA - name owner" and
// "N; 0 0 COMMENT - SCHEMA name owner" TOC lines.
var schemaEntry = regexp.MustCompile(`^[0-9]+; [0-9]+ [0-9]+ (SCHEMA - |COMMENT - SCHEMA )\S+ `)

func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Newest returns the newest complete backup set under dir and its creation time
// (doctor). Sets without a readable manifest are ignored.
func Newest(dir string) (string, time.Time, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", time.Time{}, err
	}
	var best string
	var at time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), ManifestFile))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(raw, &m) != nil || m.Format != Format {
			continue
		}
		if m.CreatedAt.After(at) {
			best, at = e.Name(), m.CreatedAt
		}
	}
	if best == "" {
		return "", time.Time{}, fs.ErrNotExist
	}
	return best, at, nil
}
