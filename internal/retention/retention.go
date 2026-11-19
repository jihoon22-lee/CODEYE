// Package retention finds and removes old node data: the snapshot copies
// (data/acquire-*) and attempt workspaces (data/ws-*) an agent leaves in its
// state directory, and the agent directories personal mode retired on
// re-enrollment (agent.retired-*). It never touches the journal, the node
// identity, caches or anything a report is read from. See
// docs/OPERATIONS.md#보존-정리.
package retention

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kinds of node data entries.
const (
	KindSnapshot     = "snapshot"
	KindWorkspace    = "workspace"
	KindRetiredAgent = "retired_agent"
)

// Entry is one directory retention removes.
type Entry struct {
	Kind string `json:"kind"`
	// Path is absolute.
	Path string `json:"path"`
	// ModTime is the newest modification time of the directory and its
	// direct children (a workspace written by a recent attempt is recent).
	ModTime time.Time `json:"modified_at"`
	Bytes   int64     `json:"bytes"`
	// Incomplete: some entries could not be read while measuring.
	Incomplete bool `json:"incomplete,omitempty"`
}

// ScanAgent lists the snapshot and workspace directories under an agent
// state directory older than cutoff. A workspace whose attempt is still
// open in the journal (active, by attempt ID) is never listed.
func ScanAgent(stateDir string, cutoff time.Time, active map[string]bool) ([]Entry, error) {
	data := filepath.Join(stateDir, "data")
	items, err := os.ReadDir(data)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, it := range items {
		name := it.Name()
		var kind string
		switch {
		case strings.HasPrefix(name, "acquire-"):
			kind = KindSnapshot
		case strings.HasPrefix(name, "ws-"):
			kind = KindWorkspace
			if active[strings.TrimPrefix(name, "ws-")] {
				continue
			}
		default:
			continue
		}
		if it.Type()&fs.ModeSymlink != 0 || !it.IsDir() {
			continue
		}
		if e, ok := old(filepath.Join(data, name), kind, cutoff); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// ScanRetired lists the retired agent directories next to agentDir
// (<agentDir>.retired-*) older than cutoff.
func ScanRetired(agentDir string, cutoff time.Time) ([]Entry, error) {
	parent, base := filepath.Dir(agentDir), filepath.Base(agentDir)
	items, err := os.ReadDir(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, it := range items {
		if !strings.HasPrefix(it.Name(), base+".retired-") || it.Type()&fs.ModeSymlink != 0 || !it.IsDir() {
			continue
		}
		if e, ok := old(filepath.Join(parent, it.Name()), KindRetiredAgent, cutoff); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func old(dir, kind string, cutoff time.Time) (Entry, bool) {
	info, err := os.Lstat(dir)
	if err != nil {
		return Entry{}, false
	}
	newest := info.ModTime()
	if children, err := os.ReadDir(dir); err == nil {
		for _, c := range children {
			if ci, err := c.Info(); err == nil && ci.ModTime().After(newest) {
				newest = ci.ModTime()
			}
		}
	}
	if !newest.Before(cutoff) {
		return Entry{}, false
	}
	bytes, incomplete := measure(dir)
	return Entry{Kind: kind, Path: dir, ModTime: newest.UTC(), Bytes: bytes, Incomplete: incomplete}, true
}

// measure sums regular file sizes without following links.
func measure(dir string) (int64, bool) {
	var total int64
	incomplete := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			incomplete = true
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			} else {
				incomplete = true
			}
		}
		return nil
	})
	return total, incomplete
}

// Remove deletes the entry's directory. Snapshot copies keep read-only
// directories of the source, so write permission is restored on the
// directories (never followed through links) before a second attempt.
func Remove(e Entry) error {
	if err := os.RemoveAll(e.Path); err == nil {
		return nil
	}
	_ = filepath.WalkDir(e.Path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(e.Path)
}

// Sort orders entries by kind then path.
func Sort(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Path < entries[j].Path
	})
}
