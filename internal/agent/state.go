package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Identity is the enrolled node identity kept in <state-dir>/node.json
// (0600). The secret never leaves the agent process.
type Identity struct {
	NodeID           string `json:"node_id"`
	NodeSecret       string `json:"node_secret"`
	PoolID           string `json:"pool_id"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
	LeaseSeconds     int    `json:"lease_seconds"`
	Controller       string `json:"controller"`
}

func identityPath(stateDir string) string { return filepath.Join(stateDir, "node.json") }

func loadIdentity(stateDir string) (*Identity, error) {
	b, err := os.ReadFile(identityPath(stateDir))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil || id.NodeID == "" || id.NodeSecret == "" {
		return nil, errors.New("node.json is corrupt")
	}
	return &id, nil
}

func saveIdentity(stateDir string, id *Identity) error {
	b, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return writeFileAtomic(identityPath(stateDir), b, 0600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// snapshotCache is the agent-private map manifest_digest → local snapshot
// root (<state-dir>/snapshots.jsonl, fsynced). The server never sees paths.
type snapshotCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]string
}

type snapshotEntry struct {
	ManifestDigest string    `json:"manifest_digest"`
	Root           string    `json:"root"`
	CreatedAt      time.Time `json:"created_at"`
}

func openSnapshotCache(path string) (*snapshotCache, error) {
	c := &snapshotCache{path: path, entries: map[string]string{}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e snapshotEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && len(e.ManifestDigest) == 64 && filepath.IsAbs(e.Root) {
			c.entries[e.ManifestDigest] = e.Root
		}
	}
	return c, nil
}

func (c *snapshotCache) get(digest string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[digest]
}

func (c *snapshotCache) put(digest, root string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[digest] == root {
		return nil
	}
	b, _ := json.Marshal(snapshotEntry{ManifestDigest: digest, Root: root, CreatedAt: time.Now().UTC()})
	f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	c.entries[digest] = root
	return nil
}
