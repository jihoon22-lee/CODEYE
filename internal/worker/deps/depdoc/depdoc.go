// Package depdoc defines the dependency preparation record
// (application/vnd.codeye.dependencies+json, schema 0.1) that every deps.*
// task writes, whatever its status. It performs no process work, so the
// analyzers of the same attempt and the report decoder validate the same
// bytes with the same rules. The record never holds an absolute path: the
// cache it names is a key under the node's dependency cache root.
package depdoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MediaType     = "application/vnd.codeye.dependencies+json"
	SchemaVersion = "0.1"
	Kind          = "dependencies"
	MaxBytes      = 1 << 20

	// TaskPrefix owns every dependency task key: deps.<ecosystem> for one
	// module, deps.<ecosystem>.mNN when an ecosystem has several.
	TaskPrefix = "deps."

	// MaxFailedItems bounds the names listed in failed_items.
	MaxFailedItems = 50
	// MaxInstalledDirs bounds installed_dirs.
	MaxInstalledDirs = 256
	// InstalledDirName is the only directory name installed_dirs holds.
	InstalledDirName = "node_modules"
)

// Record states.
const (
	// StatePrepared: every dependency the manager names is in the cache.
	StatePrepared = "prepared"
	// StatePartial: some dependencies were fetched, others were not.
	StatePartial = "partial"
	// StateFailed: nothing usable was prepared.
	StateFailed = "failed"
	// StateSkipped: the fetch was not attempted (nothing to fetch, nothing
	// would use it, or the manager is not available).
	StateSkipped = "skipped"
)

// Cache kinds.
const (
	CacheShared  = "shared"
	CacheAttempt = "attempt"
	CacheNone    = "none"
)

// Reasons a module needs no fetch. They are stored as the task reason with
// status skipped_policy and never lower the environment stage
// (domain.NothingToDo).
const (
	ReasonNoDependencies                = domain.ReasonNoDependencies
	ReasonVendoredDependencies          = domain.ReasonVendoredDependencies
	ReasonNoDependencyConsumer          = domain.ReasonNoDependencyConsumer
	ReasonDependencyConsumersNotAllowed = domain.ReasonDependencyConsumersNotAllowed
)

// Failure reasons shared by the ecosystems.
const (
	ReasonRegistryUnreachable = "registry_unreachable"
	// ReasonOfflineNoRegistry: offline mode, and neither a registry the
	// operator set nor the operator's cache holds what the fetch needs.
	ReasonOfflineNoRegistry           = "offline_no_registry_configured"
	ReasonToolchainUnavailable        = "toolchain_unavailable"
	ReasonPackageManagerNotConfigured = "package_manager_not_configured"
	ReasonTimeout                     = "timeout"
	ReasonCancelled                   = "cancelled"
	ReasonDownloadLimitExceeded       = "download_limit_exceeded"
	ReasonBudgetExhausted             = "dependency_budget_exhausted"
	ReasonCacheQuotaExceeded          = "dependency_cache_full"
	ReasonFetchFailed                 = "dependency_fetch_failed"
	ReasonRecordFailed                = "dependency_record_failed"
	ReasonCleanupUncertain            = "cleanup_uncertain"
	ReasonOutputTruncated             = "output_truncated"

	// LimVersionsNotPinned: no lockfile pinned the versions; the manager
	// resolved them for this attempt.
	LimVersionsNotPinned = "dependency_versions_not_pinned"
	// LimSeededFromOperatorCache: the fetch read the operator's module
	// cache before the registry.
	LimSeededFromOperatorCache = "seeded_from_operator_cache"
)

// Record is one dependency task's outcome.
type Record struct {
	SchemaVersion string    `json:"schema_version"`
	Kind          string    `json:"kind"`
	Snapshot      string    `json:"snapshot_manifest_digest"`
	RunID         string    `json:"run_id"`
	AttemptID     string    `json:"attempt_id"`
	TaskKey       string    `json:"task_key"`
	Module        Module    `json:"module"`
	Manager       Manager   `json:"manager"`
	Lockfile      *Lockfile `json:"lockfile"`
	// Pinned: the versions come from the project's own lock (go.sum,
	// Cargo.lock, a package lockfile); false when the manager chose them.
	Pinned bool    `json:"pinned"`
	State  string  `json:"state"`
	Reason *string `json:"reason"`
	// TaskStatus is the status the task reported with this record.
	TaskStatus    string    `json:"task_status"`
	Counts        Counts    `json:"counts"`
	Cache         Cache     `json:"cache"`
	DroppedInputs []Dropped `json:"dropped_inputs"`
	FailedItems   []Failed  `json:"failed_items"`
	Limitations   []string  `json:"limitations"`
	// InstalledDirs are the snapshot-relative node_modules directories a
	// JS/TS fetch built in the attempt's workspace copy (cache kind
	// attempt), sorted. The tsc and type-aware ESLint tasks accept exactly
	// these as the module's dependency code. Absent for every other
	// ecosystem.
	InstalledDirs []string `json:"installed_dirs,omitempty"`
}

// Module is the analyzer module the dependencies belong to; Path is the
// snapshot-relative module directory ("." = root).
type Module struct {
	ID        string `json:"id"`
	Ecosystem string `json:"ecosystem"`
	Path      string `json:"path"`
}

// Manager is the package manager that fetched (go, cargo, npm, ...).
type Manager struct {
	Name    string  `json:"name"`
	Version *string `json:"version"`
}

// Lockfile is the snapshot file that pinned the versions.
type Lockfile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Counts are the manager's own numbers; Declared is null when the manager
// does not say how many it meant to fetch.
type Counts struct {
	Declared *int `json:"declared"`
	Prepared int  `json:"prepared"`
	Failed   int  `json:"failed"`
}

// Cache says where the prepared dependencies live: a shared download
// cache under the node's dependency cache root (Key is its relative
// directory), a tree of this attempt, or nothing.
type Cache struct {
	Kind string  `json:"kind"`
	Key  *string `json:"key"`
}

// Dropped is an input the fetch did not use, with why.
type Dropped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Failed names one dependency that could not be prepared.
type Failed struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Expect binds a record to its attempt, task and snapshot.
type Expect struct {
	Snapshot, RunID, AttemptID, TaskKey string
	// Lookup returns the manifest sha256 of an included snapshot file.
	Lookup func(path string) (string, bool)
}

var (
	errInvalid  = errors.New("dependency record invalid")
	keyPattern  = regexp.MustCompile(`^deps\.(go|rust|javascript|python)(\.m[0-9]{2,3})?$`)
	codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	hex64       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	ecosystems  = map[string]bool{"go": true, "rust": true, "javascript": true, "python": true}
	states      = map[string]bool{StatePrepared: true, StatePartial: true, StateFailed: true, StateSkipped: true}
	cacheKinds  = map[string]bool{CacheShared: true, CacheAttempt: true, CacheNone: true}
	statuses    = map[string]bool{
		string(domain.TaskSucceeded): true, string(domain.TaskFailed): true, string(domain.TaskTimedOut): true, string(domain.TaskCancelled): true,
		string(domain.TaskUnsupported): true, string(domain.TaskSkippedPolicy): true, string(domain.TaskBudgetExhausted): true,
	}
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// ValidTaskKey reports whether key is a dependency task key.
func ValidTaskKey(key string) bool { return keyPattern.MatchString(key) }

// IsTask reports whether key belongs to the dependency task namespace.
func IsTask(key string) bool {
	return len(key) >= len(TaskPrefix) && key[:len(TaskPrefix)] == TaskPrefix
}

// TaskKey is the dependency task of an ecosystem's module, given the module
// suffix the ecosystem's planner uses ("" or ".mNN").
func TaskKey(ecosystem, suffix string) string { return TaskPrefix + ecosystem + suffix }

// Path is where the task key writes its record in attempt scratch.
func Path(scratch, key string) string {
	return filepath.Join(scratch, "deps", key, "dependencies.json")
}

func validPath(p string) bool { return p == "." || domain.ValidateEvidencePath(p) == nil }

// Validate checks a record against its binding.
func Validate(r Record, want Expect) error {
	if r.SchemaVersion != SchemaVersion || r.Kind != Kind {
		return invalid("schema_version/kind")
	}
	if r.Snapshot != want.Snapshot || r.RunID != want.RunID || r.AttemptID != want.AttemptID || r.TaskKey != want.TaskKey {
		return invalid("binding")
	}
	if !ValidTaskKey(r.TaskKey) {
		return invalid("task_key")
	}
	m := r.Module
	if !ecosystems[m.Ecosystem] || !validPath(m.Path) || m.ID != m.Ecosystem+":"+m.Path || (r.TaskKey != TaskPrefix+m.Ecosystem && !strings.HasPrefix(r.TaskKey, TaskPrefix+m.Ecosystem+".")) {
		return invalid("module")
	}
	if !codePattern.MatchString(r.Manager.Name) || (r.Manager.Version != nil && (*r.Manager.Version == "" || len(*r.Manager.Version) > 128)) {
		return invalid("manager")
	}
	if l := r.Lockfile; l != nil {
		if domain.ValidateEvidencePath(l.Path) != nil || !hex64.MatchString(l.SHA256) {
			return invalid("lockfile")
		}
		if want.Lookup != nil {
			if sha, ok := want.Lookup(l.Path); !ok || sha != l.SHA256 {
				return invalid("lockfile not in snapshot")
			}
		}
	}
	if !states[r.State] || !statuses[r.TaskStatus] {
		return invalid("state/status")
	}
	succeeded := r.TaskStatus == string(domain.TaskSucceeded)
	switch {
	case succeeded != (r.State == StatePrepared):
		return invalid("only a prepared record succeeds")
	case succeeded && r.Reason != nil:
		return invalid("succeeded with a reason")
	case !succeeded && (r.Reason == nil || !codePattern.MatchString(*r.Reason)):
		return invalid("reason")
	case r.TaskStatus == string(domain.TaskSkippedPolicy) && r.State != StateSkipped:
		return invalid("skipped_policy needs state skipped")
	}
	c := r.Counts
	if c.Prepared < 0 || c.Failed < 0 || (c.Declared != nil && *c.Declared < 0) {
		return invalid("counts")
	}
	if !cacheKinds[r.Cache.Kind] || (r.Cache.Kind == CacheShared) != (r.Cache.Key != nil) {
		return invalid("cache")
	}
	if k := r.Cache.Key; k != nil && (domain.ValidateEvidencePath(*k) != nil) {
		return invalid("cache key")
	}
	if r.DroppedInputs == nil || r.FailedItems == nil || r.Limitations == nil || len(r.FailedItems) > MaxFailedItems {
		return invalid("lists")
	}
	for _, d := range r.DroppedInputs {
		if domain.ValidateEvidencePath(d.Path) != nil || !codePattern.MatchString(d.Reason) {
			return invalid("dropped input")
		}
	}
	for _, f := range r.FailedItems {
		if f.Name == "" || len(f.Name) > 512 || !codePattern.MatchString(f.Reason) || filepath.IsAbs(f.Name) {
			return invalid("failed item")
		}
	}
	if !slices.IsSorted(r.Limitations) || len(slices.Compact(slices.Clone(r.Limitations))) != len(r.Limitations) {
		return invalid("limitations must be sorted and unique")
	}
	for _, l := range r.Limitations {
		if !codePattern.MatchString(l) {
			return invalid("limitation")
		}
	}
	if err := validInstalled(r); err != nil {
		return err
	}
	return nil
}

// validInstalled checks installed_dirs: JS/TS records only, an attempt
// cache, node_modules directories at or below the module directory,
// sorted and unique.
func validInstalled(r Record) error {
	if len(r.InstalledDirs) == 0 {
		if r.InstalledDirs != nil {
			return invalid("installed_dirs must be absent when empty")
		}
		return nil
	}
	if r.Module.Ecosystem != "javascript" || r.Cache.Kind != CacheAttempt || len(r.InstalledDirs) > MaxInstalledDirs {
		return invalid("installed_dirs")
	}
	if !slices.IsSorted(r.InstalledDirs) || len(slices.Compact(slices.Clone(r.InstalledDirs))) != len(r.InstalledDirs) {
		return invalid("installed_dirs must be sorted and unique")
	}
	for _, d := range r.InstalledDirs {
		if domain.ValidateEvidencePath(d) != nil || path.Base(d) != InstalledDirName || !under(path.Dir(d), r.Module.Path) {
			return invalid("installed dir")
		}
		for _, seg := range strings.Split(path.Dir(d), "/") {
			if seg == InstalledDirName {
				return invalid("installed dir nested in another")
			}
		}
	}
	return nil
}

// under reports whether snapshot-relative p is dir or below it.
func under(p, dir string) bool {
	return dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// Marshal validates and encodes a record canonically.
func Marshal(r Record, want Expect) ([]byte, error) {
	if err := Validate(r, want); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, invalid("encoding: %v", err)
	}
	b, err := domain.CanonicalJSON(raw)
	if err != nil || len(b) > MaxBytes {
		return nil, invalid("encoding")
	}
	return b, nil
}

// Decode strictly decodes and validates a record.
func Decode(body []byte, want Expect) (Record, error) {
	var r Record
	if len(body) > MaxBytes {
		return Record{}, invalid("record too large")
	}
	if err := domain.DecodeStrict(body, &r); err != nil {
		return Record{}, invalid("not strict JSON: %v", err)
	}
	if err := Validate(r, want); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Usable reports whether analyzers should read the record's cache: some or
// all dependencies were prepared in a shared cache.
func (r Record) Usable() bool {
	return (r.State == StatePrepared || r.State == StatePartial) && r.Cache.Kind == CacheShared && r.Cache.Key != nil
}

// TreeUsable reports whether analyzers should read the dependency trees
// the record lists: some or all dependencies were installed into this
// attempt's workspace copy (installed_dirs).
func (r Record) TreeUsable() bool {
	return (r.State == StatePrepared || r.State == StatePartial) && r.Cache.Kind == CacheAttempt && len(r.InstalledDirs) > 0
}

// ReadForModule reads the record a dependency task of this attempt wrote for
// the module at moduleDir and validates it again. ok is false when there is
// no record (the fetch was off or not planned), it does not decode, or it
// names another module: the analyzer then runs exactly as without one.
func ReadForModule(scratch, key, ecosystem, moduleDir string, want Expect) (Record, bool) {
	want.TaskKey = key
	b, err := readBounded(Path(scratch, key))
	if err != nil {
		return Record{}, false
	}
	r, err := Decode(b, want)
	if err != nil || r.Module.Ecosystem != ecosystem || r.Module.Path != moduleDir {
		return Record{}, false
	}
	return r, true
}

// InstalledTrees lists the installed_dirs of every usable JS/TS record this
// attempt wrote (TreeUsable), validated again. Tools that walk the
// workspace tree skip exactly these directories.
func InstalledTrees(scratch string, want Expect) []string {
	entries, err := os.ReadDir(filepath.Join(scratch, "deps"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		key := e.Name()
		if !e.IsDir() || !ValidTaskKey(key) || (key != TaskPrefix+"javascript" && !strings.HasPrefix(key, TaskPrefix+"javascript.")) {
			continue
		}
		w := want
		w.TaskKey = key
		b, err := readBounded(Path(scratch, key))
		if err != nil {
			continue
		}
		if r, err := Decode(b, w); err == nil && r.TreeUsable() {
			out = append(out, r.InstalledDirs...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func readBounded(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > MaxBytes {
		return nil, invalid("record file")
	}
	b := make([]byte, st.Size())
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	return b, nil
}
