// Package quota names CODEYE's storage areas (docs/OPERATIONS.md, docs/OPERATIONS.md#저장-영역과-상한) and
// measures how many bytes each one holds on this host. It is shared by the
// node agent (which reports areas over their limit), doctor and the service
// status command. It never deletes anything.
//
// Area layout of one node agent state dir (internal/agent, internal/worker):
//
//	snapshot   <state>/data/acquire-*/ except its workspace/ (copied source, manifest, git data)
//	workspace  <state>/data/acquire-*/workspace and <state>/data/ws-* (attempt working copies)
//	cache      <state>/workers/ (worker HOME, TMP and scratch: tool caches), <state>/cache/
//	           (the shared dependency cache), --go-mod-cache and --dependency-cache-dir
//	logs       the service log directory (controller and agent logs)
//	artifacts  the controller artifact store (serve --artifact-dir)
package quota

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Storage areas.
const (
	Snapshot  = "snapshot"
	Workspace = "workspace"
	Cache     = "cache"
	Logs      = "logs"
	Artifacts = "artifacts"
)

// NodeAreas are measured by the node agent and reported in its heartbeat.
var NodeAreas = []string{Snapshot, Workspace, Cache, Logs}

// Areas are all storage areas in report order.
var Areas = []string{Snapshot, Workspace, Cache, Logs, Artifacts}

// ValidNodeArea reports whether a is a node-reported area.
func ValidNodeArea(a string) bool {
	for _, n := range NodeAreas {
		if a == n {
			return true
		}
	}
	return false
}

// ValidArea reports whether a is any storage area.
func ValidArea(a string) bool { return ValidNodeArea(a) || a == Artifacts }

// Layout locates the areas on this host. Empty fields are not measured.
type Layout struct {
	AgentStateDir string
	GoModCache    string
	// DependencyCache is the shared dependency cache (--dependency-cache-dir);
	// the default <state>/cache is measured with AgentStateDir.
	DependencyCache string
	LogDir          string
	ArtifactDir     string
}

// Usage is one area's measured size. Incomplete is set when some entry could
// not be read: Bytes is then a lower bound, never a claim of the full size.
type Usage struct {
	Area       string
	Bytes      int64
	Files      int64
	Incomplete bool
}

// ErrNotMeasured: the layout has no location for the area.
var ErrNotMeasured = errors.New("area_not_measured")

// Measure returns the usage of area under l.
func Measure(l Layout, area string) (Usage, error) {
	u := Usage{Area: area}
	add := func(path string, skip string) {
		b, n, incomplete := dirSize(path, skip)
		u.Bytes += b
		u.Files += n
		u.Incomplete = u.Incomplete || incomplete
	}
	switch area {
	case Snapshot, Workspace:
		if l.AgentStateDir == "" {
			return u, ErrNotMeasured
		}
		data := filepath.Join(l.AgentStateDir, "data")
		acquired, err := filepath.Glob(filepath.Join(data, "acquire-*"))
		if err != nil {
			return u, err
		}
		for _, root := range acquired {
			if area == Snapshot {
				add(root, filepath.Join(root, "workspace"))
			} else {
				add(filepath.Join(root, "workspace"), "")
			}
		}
		if area == Workspace {
			pinned, err := filepath.Glob(filepath.Join(data, "ws-*"))
			if err != nil {
				return u, err
			}
			for _, p := range pinned {
				add(p, "")
			}
		}
	case Cache:
		if l.AgentStateDir == "" && l.GoModCache == "" && l.DependencyCache == "" {
			return u, ErrNotMeasured
		}
		var roots []string
		if l.AgentStateDir != "" {
			roots = append(roots, filepath.Join(l.AgentStateDir, "workers"), filepath.Join(l.AgentStateDir, "cache"))
		}
		roots = append(roots, l.GoModCache, l.DependencyCache)
		for i, r := range roots {
			if r != "" && !nested(r, roots[:i]) {
				add(r, "")
			}
		}
	case Logs:
		if l.LogDir == "" {
			return u, ErrNotMeasured
		}
		add(l.LogDir, "")
	case Artifacts:
		if l.ArtifactDir == "" {
			return u, ErrNotMeasured
		}
		add(l.ArtifactDir, "")
	default:
		return u, errors.New("unknown storage area")
	}
	return u, nil
}

// Exceeded returns the areas (sorted) whose measured bytes reached their
// limit. A limit of 0 or less means "no limit". An area that cannot be
// measured is not reported as exceeded; incomplete measurements still count
// (their size is a lower bound).
func Exceeded(l Layout, limits map[string]int64) ([]string, map[string]Usage) {
	var out []string
	usage := map[string]Usage{}
	for area, limit := range limits {
		if limit <= 0 {
			continue
		}
		u, err := Measure(l, area)
		if err != nil {
			continue
		}
		usage[area] = u
		if u.Bytes >= limit {
			out = append(out, area)
		}
	}
	sort.Strings(out)
	return out, usage
}

// nested reports whether p is one of roots or lies inside one, so a
// directory configured inside another measured root is counted once.
func nested(p string, roots []string) bool {
	for _, r := range roots {
		if r == "" {
			continue
		}
		if rel, err := filepath.Rel(r, p); err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}

// dirSize sums regular-file and symlink sizes under path (Lstat; links are
// never followed). A missing path is empty. skip excludes one subtree.
func dirSize(path, skip string) (bytes, files int64, incomplete bool) {
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			incomplete = true
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if skip != "" && p == skip {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				incomplete = true
			}
			return nil
		}
		bytes += info.Size()
		files++
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		incomplete = true
	}
	return bytes, files, incomplete
}

// ParseLimit parses "AREA=BYTES" (agent --storage-quota).
func ParseLimit(s string) (string, int64, error) {
	area, value, ok := strings.Cut(s, "=")
	if !ok || !ValidNodeArea(area) {
		return "", 0, errors.New("expected AREA=BYTES with AREA one of snapshot, workspace, cache, logs")
	}
	var n int64
	if value == "" || len(value) > 19 {
		return "", 0, errors.New("invalid byte count")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return "", 0, errors.New("invalid byte count")
		}
		n = n*10 + int64(c-'0')
	}
	if n <= 0 {
		return "", 0, errors.New("byte count must be positive")
	}
	return area, n, nil
}
