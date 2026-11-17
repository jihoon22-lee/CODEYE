// Package artifactstore keeps attempt artifact bytes on the controller's
// filesystem, content-addressed by SHA-256: <dir>/sha256/<aa>/<sha256>.
//
// Put streams the body to a temporary file in the same filesystem while
// hashing it, rejects a size or hash mismatch (nothing is kept), fsyncs the
// file, renames it into place and (where supported) fsyncs the directory, so
// a key is only returned for durable, verified bytes. An existing file at the
// key is always replaced by the newly verified bytes. The database row that
// references the key is written afterwards by the caller; a failure between
// the two leaves an unreferenced file (garbage collection is not implemented).
package artifactstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

var (
	// ErrIntegrity is a size or SHA-256 mismatch between declared and received bytes.
	ErrIntegrity = errors.New("artifactstore: size or sha256 mismatch")
	// ErrTooLarge is a body over the declared or maximum size.
	ErrTooLarge = errors.New("artifactstore: body too large")
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store is a directory of content-addressed artifacts.
type Store struct{ dir string }

// Open prepares dir (absolute; created 0700 if missing).
func Open(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errors.New("artifactstore: directory must be an absolute clean path")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("artifactstore: create directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sha256"), 0o700); err != nil {
		return nil, fmt.Errorf("artifactstore: create directory: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Key is the storage key of a sha256.
func Key(sha string) string { return "sha256/" + sha[:2] + "/" + sha }

// Put stores exactly size bytes from r whose SHA-256 must equal sha. It reads
// at most size+1 bytes. It returns the storage key.
func (s *Store) Put(r io.Reader, sha string, size int64) (string, error) {
	if !shaPattern.MatchString(sha) || size < 0 {
		return "", ErrIntegrity
	}
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "upload-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, size+1))
	if err != nil {
		return "", err
	}
	if n > size {
		return "", ErrTooLarge
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return "", ErrIntegrity
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	key := Key(sha)
	final := filepath.Join(s.dir, filepath.FromSlash(key))
	prefix := filepath.Dir(final)
	created := false
	if _, err := os.Lstat(prefix); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(prefix, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		created = true
	} else if err != nil {
		return "", err
	}
	// Always install the verified temp file: an existing file at the key is
	// never trusted by name or size alone (it could be truncated or altered).
	// Rename atomically replaces it on POSIX and Windows.
	if err := os.Rename(tmpName, final); err != nil {
		// A concurrent Windows writer or reader can hold the destination open.
		// Accept the existing file only if its bytes verify; never otherwise.
		if !verify(final, sha, size) {
			return "", err
		}
		return key, nil
	}
	keep = true
	if err := syncDir(prefix); err != nil {
		return "", err
	}
	if created {
		// The new prefix directory entry must itself be durable.
		if err := syncDir(filepath.Dir(prefix)); err != nil {
			return "", err
		}
	}
	return key, nil
}

// verify re-hashes path and reports whether it holds exactly size bytes with sha.
func verify(path, sha string, size int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, size+1))
	return err == nil && n == size && hex.EncodeToString(h.Sum(nil)) == sha
}

// syncDir fsyncs a directory where the platform supports it (not Windows,
// where directory handles cannot be flushed this way).
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// Open opens a stored artifact by key and verifies nothing; callers that need
// integrity re-hash the bytes against the database row.
func (s *Store) Open(key string) (*os.File, error) {
	if len(key) != len("sha256/aa/")+64 || key[:7] != "sha256/" || !shaPattern.MatchString(key[10:]) || key[7:9] != key[10:12] || key[9] != '/' {
		return nil, ErrIntegrity
	}
	return os.Open(filepath.Join(s.dir, filepath.FromSlash(key)))
}
