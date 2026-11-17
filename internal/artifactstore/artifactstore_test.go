package artifactstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func tmpEntries(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

func TestPutVerifiesAndStoresContentAddressed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("hello artifact")
	sha := hashOf(body)
	key, err := s.Put(bytes.NewReader(body), sha, int64(len(body)))
	if err != nil || key != "sha256/"+sha[:2]+"/"+sha {
		t.Fatalf("put %q %v", key, err)
	}
	f, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if !bytes.Equal(got, body) {
		t.Fatal("stored bytes differ")
	}
	// Wrong hash, short body and long body keep nothing.
	if _, err := s.Put(bytes.NewReader(body), hashOf([]byte("other")), int64(len(body))); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("hash mismatch: %v", err)
	}
	if _, err := s.Put(bytes.NewReader(body[:3]), sha, int64(len(body))); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("short: %v", err)
	}
	if _, err := s.Put(bytes.NewReader(append(body, 'x')), sha, int64(len(body))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("long: %v", err)
	}
	if n := tmpEntries(t, dir); n != 0 {
		t.Fatalf("%d temp files left", n)
	}
	if _, err := s.Open("sha256/aa/../../x"); !errors.Is(err, ErrIntegrity) {
		t.Fatal("traversal key accepted")
	}
	if _, err := Open("relative"); err == nil {
		t.Fatal("relative dir accepted")
	}
}

// Regression: an existing same-size file with different bytes at the key must
// not be accepted as the stored artifact.
func TestPutReplacesSameSizeTamperedFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("genuine-bytes")
	sha := hashOf(body)
	final := filepath.Join(dir, filepath.FromSlash(Key(sha)))
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []byte(strings.Repeat("X", len(body)))
	if err := os.WriteFile(final, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(bytes.NewReader(body), sha, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(final)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("tampered file kept: %q %v", got, err)
	}
	// Putting the same content again keeps it valid.
	if _, err := s.Put(bytes.NewReader(body), sha, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if !verify(final, sha, int64(len(body))) {
		t.Fatal("content no longer verifies")
	}
	if verify(final, hashOf(bad), int64(len(body))) {
		t.Fatal("verify accepted wrong hash")
	}
}
