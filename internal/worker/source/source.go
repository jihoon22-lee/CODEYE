// Package source acquires immutable inputs and independent writable workspaces.
package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

var (
	ErrInvalid          = errors.New("invalid_source")
	ErrUnavailable      = errors.New("source_unavailable")
	ErrUnstable         = errors.New("source_unstable")
	ErrLimit            = errors.New("source_limit_exceeded")
	ErrUnsupported      = errors.New("source_unsupported")
	ErrRecoveryRequired = errors.New("recovery_required")
	ErrIntegrity        = errors.New("snapshot_integrity_failed")
	// ErrAuthRequired: the Git host refused to serve the repository without
	// credentials (a private or nonexistent repository). It also matches
	// ErrUnavailable.
	ErrAuthRequired = errors.New("source_auth_required")
	// ErrHoldsProtected: the project folder contains, or lies inside, a
	// directory the node keeps private (personal mode: CODEYE's state and
	// backup directories).
	ErrHoldsProtected = errors.New("source_holds_codeye_data")
	// ErrUnreachable: the Git host could not be reached (name resolution,
	// connection, TLS). It also matches ErrUnavailable.
	ErrUnreachable = errors.New("source_unreachable")
)

type Limits struct {
	MaxFiles                    int
	MaxFileBytes, MaxTotalBytes int64
	Retries                     int
}

func DefaultLimits() Limits {
	return Limits{MaxFiles: 50000, MaxFileBytes: 16 << 20, MaxTotalBytes: 512 << 20, Retries: 2}
}
func (l Limits) valid() bool {
	return l.MaxFiles > 0 && l.MaxFileBytes > 0 && l.MaxTotalBytes >= l.MaxFileBytes && l.Retries >= 0 && l.Retries <= 5
}

type Entry struct {
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	Size           *int64 `json:"size_bytes"`
	SHA256         string `json:"sha256,omitempty"`
	Mode           uint32 `json:"mode"`
	Encoding       string `json:"encoding,omitempty"`
	ExcludedReason string `json:"excluded_reason,omitempty"`
}
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind"`
	Consistency   string `json:"consistency"`
	SourceSHA     string `json:"source_sha,omitempty"`
	// Git only: the requested branch name and when the acquisition was requested.
	// They never hold a URL, remote name or credential.
	SourceBranch string `json:"source_branch,omitempty"`
	// SourceRef is the fetched ref of a pull-request snapshot
	// (refs/pull/N/head) in place of a branch: a Git snapshot has exactly one.
	SourceRef   string    `json:"source_ref,omitempty"`
	RequestedAt time.Time `json:"requested_at,omitzero"`
	FixedAt     time.Time `json:"source_fixed_at"`
	Entries     []Entry   `json:"entries"`
	Limitations []string  `json:"limitations"`
}

// Snapshot.Digest is the SHA-256 of the exact stored manifest.json bytes, which are
// the canonical JSON of the whole manifest (including source_fixed_at, requested_at
// and limitations). A new acquisition therefore has a new digest even for identical
// bytes; a retained snapshot keeps its digest for every retry and clone.
type Snapshot struct {
	Root, SnapshotDir, WorkspaceDir, ManifestPath, Digest string
	Manifest                                              Manifest
}

const maxManifestBytes = 16 << 20

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// CheckProtected refuses a local project folder (root and relative path,
// symlinks resolved) that contains or lies inside one of the protected
// directories. A folder that does not resolve is left to AcquireLocal.
func CheckProtected(root, relative string, protected []string) error {
	project, err := ResolveLocal(root, relative)
	if err != nil {
		return nil
	}
	for _, d := range protected {
		if d == "" {
			continue
		}
		if r, e := filepath.EvalSymlinks(d); e == nil {
			d = r
		}
		d = filepath.Clean(d)
		if within(project, d) || within(d, project) {
			return ErrHoldsProtected
		}
	}
	return nil
}

func ResolveLocal(root, relative string) (string, error) {
	if !filepath.IsAbs(root) || relative == "" || (relative != "." && domain.ValidateEvidencePath(relative) != nil) {
		return "", ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrUnavailable
	}
	target, err := filepath.EvalSymlinks(filepath.Join(resolved, filepath.FromSlash(relative)))
	if err != nil {
		return "", ErrUnavailable
	}
	if !within(resolved, target) {
		return "", ErrInvalid
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return "", ErrUnavailable
	}
	return target, nil
}
func newSnapshot(dataRoot, kind, consistency string) (Snapshot, error) {
	if !filepath.IsAbs(dataRoot) {
		return Snapshot{}, ErrInvalid
	}
	if err := os.MkdirAll(dataRoot, 0700); err != nil {
		return Snapshot{}, ErrUnavailable
	}
	root, err := os.MkdirTemp(dataRoot, "acquire-")
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	s := Snapshot{Root: root, SnapshotDir: filepath.Join(root, "snapshot"), WorkspaceDir: filepath.Join(root, "workspace"), ManifestPath: filepath.Join(root, "manifest.json"), Manifest: Manifest{SchemaVersion: "0.1", Kind: kind, Consistency: consistency, Entries: []Entry{}, Limitations: []string{}}}
	if err = os.Mkdir(s.SnapshotDir, 0700); err != nil {
		_ = os.RemoveAll(root)
		return Snapshot{}, ErrUnavailable
	}
	return s, nil
}

// exclusion returns why a path is not copied, or "". Name-based and deliberately
// conservative; it is not secret scanning. Standard cache names are excluded at
// any depth. build/dist/target are generated output only at the top of the selected
// tree, so a source package such as internal/build is kept. venv/.venv are excluded at
// the top or wherever marker reports a pyvenv.cfg inside them.
func exclusion(path string, isDir bool, marker func(dir string) bool) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case ".git", ".codeye", "node_modules", "__pycache__":
			return "generated_or_cache"
		case "build", "dist", "target":
			if i == 0 {
				return "generated_or_cache"
			}
		case "venv", ".venv":
			if i == 0 || (marker != nil && marker(strings.Join(parts[:i+1], "/"))) {
				return "generated_or_cache"
			}
		case ".ssh", ".aws", ".gnupg", ".docker", ".kube", ".azure", ".gcloud":
			return "credential_directory"
		}
		if lower == ".env" || (strings.HasPrefix(lower, ".env.") && lower != ".env.example" && lower != ".env.sample") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || lower == "id_rsa" || lower == "id_ed25519" {
			return "secret_file_policy"
		}
		if i == len(parts)-1 && secretFileName(lower, isDir) {
			return "secret_file_policy"
		}
	}
	return ""
}

var secretNames = map[string]bool{
	".npmrc": true, ".yarnrc": true, ".yarnrc.yml": true, ".netrc": true, "_netrc": true,
	".git-credentials": true, ".pypirc": true, ".pgpass": true, ".htpasswd": true, "htpasswd": true,
	".dockercfg": true, "kubeconfig": true, ".vault_pass": true, ".vault-pass": true,
	"id_dsa": true, "id_ecdsa": true,
	"credentials.json": true, "credentials.yml": true, "credentials.yaml": true, "credentials.toml": true, "credentials.ini": true,
	"secrets.json": true, "secrets.yml": true, "secrets.yaml": true, "secrets.toml": true, "secrets.ini": true,
}
var secretSuffixes = []string{".p12", ".pfx", ".pkcs12", ".jks", ".keystore", ".kdbx", ".ppk", ".tfstate", ".tfstate.backup", ".tfvars"}

// secretFileName is the common credential-file name policy. lower is already
// lower-cased; a bare "credentials" is a file only (aws-sdk-style packages keep theirs).
func secretFileName(lower string, isDir bool) bool {
	if secretNames[lower] || (lower == "credentials" && !isDir) {
		return true
	}
	for _, suffix := range secretSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	for _, prefix := range []string{"service-account", "service_account", "serviceaccount"} {
		if strings.HasPrefix(lower, prefix) && strings.HasSuffix(lower, ".json") {
			return true
		}
	}
	return false
}
func portablePath(path string) bool {
	if !utf8.ValidString(path) || domain.ValidateEvidencePath(path) != nil {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		name := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || (len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9') {
			return false
		}
	}
	return true
}
func hashBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

const readChunk = 64 << 10

// readLimited reads at most max bytes (ErrLimit beyond that), checking ctx between
// chunks. A single blocked read(2) on a hung network filesystem cannot be interrupted
// from here; bounding that needs the executor's subprocess orchestration.
func readLimited(ctx context.Context, r io.Reader, max int64) ([]byte, error) {
	var out []byte
	chunk := make([]byte, readChunk)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		want := int64(len(chunk))
		if left := max + 1 - int64(len(out)); left < want {
			want = left
		}
		n, err := r.Read(chunk[:want])
		out = append(out, chunk[:n]...)
		if int64(len(out)) > max {
			return nil, ErrLimit
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, ErrUnavailable
		}
	}
}
func writeFile(root, path string, data []byte, mode fs.FileMode) error {
	dest := filepath.Join(root, filepath.FromSlash(path))
	if !within(root, dest) {
		return ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return ErrUnavailable
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrUnavailable
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return ErrUnavailable
	}
	if err = os.Chmod(dest, mode.Perm()&0555|0444); err != nil {
		return ErrUnavailable
	}
	return nil
}
func entryFor(path string, data []byte, mode fs.FileMode) Entry {
	encoding := "binary_or_unknown"
	if utf8.Valid(data) {
		encoding = "utf-8"
	}
	size := int64(len(data))
	return Entry{Path: path, Kind: "file", Size: &size, SHA256: hashBytes(data), Mode: uint32(mode.Perm()), Encoding: encoding}
}

// canonicalManifest is the one stored form of a manifest: canonical JSON of the whole
// struct, so the same content always yields the same bytes and the same digest.
func canonicalManifest(m Manifest) ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, ErrInvalid
	}
	canonical, err := domain.CanonicalJSON(encoded)
	if err != nil {
		return nil, ErrInvalid
	}
	if len(canonical) > maxManifestBytes {
		return nil, ErrLimit
	}
	return canonical, nil
}
func finalize(ctx context.Context, s *Snapshot) error {
	sort.Slice(s.Manifest.Entries, func(i, j int) bool { return s.Manifest.Entries[i].Path < s.Manifest.Entries[j].Path })
	s.Manifest.FixedAt = time.Now().UTC()
	manifest, err := canonicalManifest(s.Manifest)
	if err != nil {
		return err
	}
	if err = os.WriteFile(s.ManifestPath, manifest, 0600); err != nil {
		return ErrUnavailable
	}
	s.Digest = hashBytes(manifest)
	if err = cloneWorkspace(ctx, *s, s.WorkspaceDir); err != nil {
		return err
	}
	return nil
}

// CloneWorkspace verifies all retained snapshot hashes before creating another
// independent execution copy. It never reacquires the original source.
func CloneWorkspace(ctx context.Context, s Snapshot, destination string) error {
	if !filepath.IsAbs(destination) || !filepath.IsAbs(s.Root) {
		return ErrInvalid
	}
	resolved, err := outsideSource(destination, s.SnapshotDir)
	if err != nil {
		return err
	}
	managed, err := filepath.EvalSymlinks(filepath.Dir(s.Root))
	if err != nil || !within(managed, resolved) {
		return ErrInvalid
	}
	return cloneWorkspace(ctx, s, resolved)
}
func cloneWorkspace(ctx context.Context, s Snapshot, destination string) error {
	if err := os.Mkdir(destination, 0700); err != nil {
		return ErrUnavailable
	}
	success := false
	defer func() {
		if !success {
			_ = Remove(destination)
		}
	}()
	root, err := os.OpenRoot(s.SnapshotDir)
	if err != nil {
		return ErrUnavailable
	}
	defer root.Close()
	for _, e := range s.Manifest.Entries {
		if e.ExcludedReason != "" {
			continue
		}
		if e.Kind == "directory" {
			if !portablePath(e.Path) {
				return ErrIntegrity
			}
			if err := os.MkdirAll(filepath.Join(destination, filepath.FromSlash(e.Path)), 0700); err != nil {
				return ErrUnavailable
			}
			continue
		}
		if e.Kind != "file" && e.Kind != "lfs_pointer" {
			continue
		}
		if !portablePath(e.Path) || e.Size == nil || *e.Size < 0 {
			return ErrIntegrity
		}
		info, err := root.Lstat(filepath.FromSlash(e.Path))
		if err != nil || !info.Mode().IsRegular() {
			return ErrIntegrity
		}
		f, err := root.Open(filepath.FromSlash(e.Path))
		if err != nil {
			return ErrIntegrity
		}
		data, readErr := readLimited(ctx, f, *e.Size)
		_ = f.Close()
		if readErr != nil || int64(len(data)) != *e.Size || hashBytes(data) != e.SHA256 {
			return ErrIntegrity
		}
		if err = writeFile(destination, e.Path, data, fs.FileMode(e.Mode)|0600); err != nil {
			return err
		}
		if err = os.Chmod(filepath.Join(destination, filepath.FromSlash(e.Path)), fs.FileMode(e.Mode).Perm()|0600); err != nil {
			return ErrUnavailable
		}
	}
	success = true
	return nil
}

// Remove is for platform-owned snapshot/workspace directories, never source roots.
func Remove(path string) error {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			if d.IsDir() {
				_ = os.Chmod(p, 0700)
			} else if d.Type().IsRegular() {
				_ = os.Chmod(p, 0600)
			}
		}
		return nil
	})
	return os.RemoveAll(path)
}

// Resolve existing ancestors before creating platform storage, so a symlinked
// data directory cannot cause writes inside the original project.
func outsideSource(dataRoot, original string) (string, error) {
	if !filepath.IsAbs(dataRoot) {
		return "", ErrInvalid
	}
	current := filepath.Clean(dataRoot)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, e := filepath.EvalSymlinks(current)
			if e != nil {
				return "", ErrInvalid
			}
			rel, e := filepath.Rel(current, dataRoot)
			if e != nil {
				return "", ErrInvalid
			}
			target := filepath.Join(resolved, rel)
			if within(original, target) || within(target, original) {
				return "", ErrInvalid
			}
			return target, nil
		}
		if !os.IsNotExist(err) {
			return "", ErrUnavailable
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", ErrInvalid
		}
		current = parent
	}
}

// Load checks the retained manifest.json byte-for-byte against the digest held by the
// control plane, requires it to be in canonical form, and validates every field.
// CloneWorkspace additionally verifies each included file's bytes.
func Load(ctx context.Context, root, expectedDigest string) (Snapshot, error) {
	if !filepath.IsAbs(root) || len(expectedDigest) != 64 {
		return Snapshot{}, ErrInvalid
	}
	f, err := os.Open(filepath.Join(root, "manifest.json"))
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer f.Close()
	b, err := readLimited(ctx, f, maxManifestBytes)
	if err != nil {
		return Snapshot{}, err
	}
	if hashBytes(b) != expectedDigest {
		return Snapshot{}, ErrIntegrity
	}
	if canonical, err := domain.CanonicalJSON(b); err != nil || !bytes.Equal(canonical, b) {
		return Snapshot{}, ErrIntegrity
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil || validateManifest(manifest) != nil {
		return Snapshot{}, ErrIntegrity
	}
	return Snapshot{Root: root, SnapshotDir: filepath.Join(root, "snapshot"), WorkspaceDir: filepath.Join(root, "workspace"), ManifestPath: filepath.Join(root, "manifest.json"), Manifest: manifest, Digest: expectedDigest}, nil
}

// DecodeManifest strictly decodes and validates manifest bytes whose SHA-256
// the caller has already matched to the snapshot digest (controller readers,
// the report builder). It never touches the filesystem. Invalid bytes are ErrIntegrity.
func DecodeManifest(b []byte) (Manifest, error) {
	if len(b) > maxManifestBytes {
		return Manifest{}, ErrIntegrity
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || !json.Valid(b) || validateManifest(manifest) != nil {
		return Manifest{}, ErrIntegrity
	}
	return manifest, nil
}

var (
	hashPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	shaPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	pullRef       = regexp.MustCompile(`^refs/pull/[1-9][0-9]{0,9}/head$`)
	entryKinds    = map[string]bool{"file": true, "directory": true, "symlink": true, "special": true, "submodule": true, "lfs_pointer": true}
	excludeReason = map[string]bool{"": true, "generated_or_cache": true, "credential_directory": true, "secret_file_policy": true, "link_not_followed": true, "special_file_not_copied": true, "submodule_not_acquired": true}
	limitations   = map[string]bool{"local_copy_is_not_atomic": true, "excluded_links_and_special_files_are_not_analyzed": true, "submodule_objects_not_acquired": true, "lfs_objects_not_acquired": true}
)

// validateManifest enforces the documented schema on a decoded manifest.
func validateManifest(m Manifest) error {
	if m.SchemaVersion != "0.1" || m.FixedAt.IsZero() || m.Entries == nil || m.Limitations == nil || len(m.Entries) > 50000 {
		return ErrIntegrity
	}
	switch m.Kind {
	case "local":
		if m.Consistency != "stable_copy_best_effort" || m.SourceSHA != "" || m.SourceBranch != "" || m.SourceRef != "" || !m.RequestedAt.IsZero() {
			return ErrIntegrity
		}
	case "git":
		if m.Consistency != "git_commit" || !shaPattern.MatchString(m.SourceSHA) || (m.SourceBranch == "") == (m.SourceRef == "") || len(m.SourceBranch) > 255 ||
			(m.SourceRef != "" && !pullRef.MatchString(m.SourceRef)) || m.RequestedAt.IsZero() {
			return ErrIntegrity
		}
	default:
		return ErrIntegrity
	}
	for _, l := range m.Limitations {
		if !limitations[l] {
			return ErrIntegrity
		}
	}
	previous := ""
	for i, e := range m.Entries {
		if !portablePath(e.Path) || (i > 0 && e.Path <= previous) || !entryKinds[e.Kind] || !excludeReason[e.ExcludedReason] || e.Mode > 0777 || (e.Size != nil && *e.Size < 0) {
			return ErrIntegrity
		}
		previous = e.Path
		if (e.Kind == "file" || e.Kind == "lfs_pointer") && e.ExcludedReason == "" {
			if e.Size == nil || !hashPattern.MatchString(e.SHA256) || (e.Encoding != "utf-8" && e.Encoding != "binary_or_unknown") {
				return ErrIntegrity
			}
		}
	}
	return nil
}
