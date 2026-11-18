// Package personal runs CODEYE on one machine (W25, docs/47): one JSON
// configuration file, a 0700 state directory, one loopback controller and one
// local node agent started, stopped and inspected by `codeye start|stop|status`.
//
// It never stores the database URL or any token in the state directory or
// the configuration: the operator provides CODEYE_DATABASE_URL in the
// environment, and the one-time enrollment token lives in a 0600 file only
// until the local agent has enrolled.
package personal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// SchemaVersion of the personal configuration file.
const SchemaVersion = "0.1"

// Database modes.
const (
	// DatabaseExternal: the operator provides CODEYE_DATABASE_URL (default).
	DatabaseExternal = "external"
)

// Config is the personal configuration (docs/47 §2). Unknown fields are
// rejected; paths must be absolute and clean.
type Config struct {
	SchemaVersion string       `json:"schema_version"`
	StateDir      string       `json:"state_dir"`
	Listen        string       `json:"listen,omitempty"`
	WebDir        string       `json:"web_dir,omitempty"`
	Database      Database     `json:"database"`
	SourceRoots   []SourceRoot `json:"source_roots"`
	Agent         Agent        `json:"agent"`
	Quota         Quota        `json:"quota"`
	BackupDir     string       `json:"backup_dir,omitempty"`
	Stop          Stop         `json:"stop"`
}

// Database selects how the controller reaches PostgreSQL.
type Database struct {
	Mode string `json:"mode"`
	// PostgresBinDir holds pg_dump and pg_restore (backup, restore). They are
	// never searched in PATH.
	PostgresBinDir string `json:"postgres_bin_dir,omitempty"`
}

// SourceRoot is a local directory under which projects are registered. The
// label is the source root's identity in the database.
type SourceRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	// RepositoryCode grants analyzers that execute repository code
	// (docs/39 §2.7) for this root. Default false.
	RepositoryCode bool `json:"repository_code,omitempty"`
	// ModelReview approves sending this root's code to the configured model
	// (agent.model). Default false.
	ModelReview bool `json:"model_review,omitempty"`
}

// Agent configures the local node agent (codeye agent flags, docs/28).
type Agent struct {
	Name                   string            `json:"name,omitempty"`
	MaxWorkers             int               `json:"max_workers,omitempty"`
	ToolchainPath          string            `json:"toolchain_path,omitempty"`
	Go                     string            `json:"go,omitempty"`
	GoModCache             string            `json:"go_mod_cache,omitempty"`
	Tools                  map[string]string `json:"tools,omitempty"`
	PythonBothTypeCheckers bool              `json:"python_both_type_checkers,omitempty"`
	Model                  *Model            `json:"model,omitempty"`
}

// Model is the optional review model (an approved relay file or OpenCode).
// Without it the review stage is skipped, never replaced by another model.
type Model struct {
	OpenCode   string `json:"opencode"`
	Name       string `json:"name"`
	RelayFile  string `json:"relay_file,omitempty"`
	ReviewPack string `json:"review_pack,omitempty"`
}

// Quota are storage area limits in bytes (0 = no limit).
type Quota struct {
	SnapshotBytes  int64 `json:"snapshot_bytes,omitempty"`
	WorkspaceBytes int64 `json:"workspace_bytes,omitempty"`
	CacheBytes     int64 `json:"cache_bytes,omitempty"`
	LogsBytes      int64 `json:"logs_bytes,omitempty"`
	ArtifactsBytes int64 `json:"artifacts_bytes,omitempty"`
}

// NodeLimits are the agent's area limits (--storage-quota).
func (q Quota) NodeLimits() map[string]int64 {
	out := map[string]int64{}
	for area, v := range map[string]int64{"snapshot": q.SnapshotBytes, "workspace": q.WorkspaceBytes, "cache": q.CacheBytes, "logs": q.LogsBytes} {
		if v > 0 {
			out[area] = v
		}
	}
	return out
}

// Limit returns the limit of area (0 = none).
func (q Quota) Limit(area string) int64 {
	switch area {
	case "snapshot":
		return q.SnapshotBytes
	case "workspace":
		return q.WorkspaceBytes
	case "cache":
		return q.CacheBytes
	case "logs":
		return q.LogsBytes
	case "artifacts":
		return q.ArtifactsBytes
	}
	return 0
}

// Stop bounds `codeye stop`.
type Stop struct {
	// DrainTimeoutSeconds: how long running work may finish after the drain
	// request before it is cancelled (default 300).
	DrainTimeoutSeconds int `json:"drain_timeout_seconds,omitempty"`
	// CancelTimeoutSeconds: how long the agent may take to cancel running
	// work and confirm cleanup (default 90).
	CancelTimeoutSeconds int `json:"cancel_timeout_seconds,omitempty"`
}

// Defaults.
const (
	DefaultListen         = "127.0.0.1:7331"
	DefaultMaxWorkers     = 1
	DefaultToolchainPath  = "/usr/bin:/bin"
	DefaultDrainSeconds   = 300
	DefaultCancelSeconds  = 90
	maxConfigBytes        = 1 << 20
	maxSourceRoots        = 32
	maxAgentWorkersConfig = 16
)

// DefaultPath is the configuration path used when --config is not given.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("cannot determine the user configuration directory; pass --config")
	}
	return filepath.Join(dir, "codeye", "personal.json"), nil
}

// Load reads and validates path. knownTools are the analyzer tool and
// toolchain flag names of this build (agent.tools keys).
func Load(path string, knownTools []string) (*Config, error) {
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, errors.New("config path cannot be resolved")
		}
		path = abs
	}
	// The path is not echoed: --config is operator input.
	st, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("cannot read the configuration file (--config, default $XDG_CONFIG_HOME/codeye/personal.json)")
	}
	if !st.Mode().IsRegular() || st.Size() > maxConfigBytes {
		return nil, errors.New("the configuration must be a regular file of at most 1 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read the configuration file")
	}
	return Parse(raw, knownTools)
}

// Parse decodes a configuration strictly and validates it.
func Parse(raw []byte, knownTools []string) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config is not valid: %s", jsonProblem(err))
	}
	if dec.More() {
		return nil, errors.New("config is not valid: trailing data")
	}
	if err := c.validate(knownTools); err != nil {
		return nil, fmt.Errorf("config is not valid: %w", err)
	}
	return &c, nil
}

// jsonProblem describes a decode error without echoing configuration values.
func jsonProblem(err error) string {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Sprintf("JSON syntax error at byte %d", syntax.Offset)
	case errors.As(err, &typ):
		return fmt.Sprintf("field %q has the wrong type", typ.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return "unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field ")
	}
	return "JSON decode error"
}

func absClean(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && utf8.ValidString(p) && !strings.ContainsRune(p, 0)
}

func (c *Config) validate(knownTools []string) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if !absClean(c.StateDir) {
		return errors.New("state_dir must be an absolute, clean path")
	}
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	ap, err := netip.ParseAddrPort(c.Listen)
	if err != nil || !ap.Addr().IsLoopback() || ap.Port() == 0 {
		return errors.New("listen must be a loopback address with a port (for example 127.0.0.1:7331)")
	}
	if c.WebDir != "" && !absClean(c.WebDir) {
		return errors.New("web_dir must be an absolute, clean path")
	}
	switch c.Database.Mode {
	case DatabaseExternal:
	case "managed":
		return errors.New("database.mode \"managed\" is not implemented (W25 implements \"external\" only)")
	default:
		return errors.New(`database.mode must be "external"`)
	}
	if c.Database.PostgresBinDir != "" && !absClean(c.Database.PostgresBinDir) {
		return errors.New("database.postgres_bin_dir must be an absolute, clean path")
	}
	if len(c.SourceRoots) == 0 || len(c.SourceRoots) > maxSourceRoots {
		return fmt.Errorf("source_roots needs 1..%d entries", maxSourceRoots)
	}
	labels, paths := map[string]bool{}, map[string]bool{}
	for _, r := range c.SourceRoots {
		label := strings.TrimSpace(r.Label)
		if label == "" || label != r.Label || utf8.RuneCountInString(label) > 100 || !utf8.ValidString(label) {
			return errors.New("source_roots[].label must be 1..100 characters without surrounding spaces")
		}
		if !absClean(r.Path) {
			return errors.New("source_roots[].path must be an absolute, clean path")
		}
		if labels[label] || paths[r.Path] {
			return errors.New("source_roots labels and paths must be unique")
		}
		labels[label], paths[r.Path] = true, true
		if r.ModelReview && c.Agent.Model == nil {
			return errors.New("source_roots[].model_review needs agent.model")
		}
		if within(r.Path, c.StateDir) || within(c.StateDir, r.Path) {
			return errors.New("a source root and state_dir must not contain each other")
		}
	}
	a := &c.Agent
	if a.MaxWorkers == 0 {
		a.MaxWorkers = DefaultMaxWorkers
	}
	if a.MaxWorkers < 1 || a.MaxWorkers > maxAgentWorkersConfig {
		return errors.New("agent.max_workers must be 1..16")
	}
	if a.Name != "" && (strings.TrimSpace(a.Name) != a.Name || utf8.RuneCountInString(a.Name) > 100) {
		return errors.New("agent.name must be at most 100 characters without surrounding spaces")
	}
	if a.ToolchainPath == "" {
		a.ToolchainPath = DefaultToolchainPath
	}
	for _, p := range filepath.SplitList(a.ToolchainPath) {
		if !absClean(p) {
			return errors.New("agent.toolchain_path entries must be absolute, clean paths")
		}
	}
	for name, p := range map[string]string{"agent.go": a.Go, "agent.go_mod_cache": a.GoModCache} {
		if p != "" && !absClean(p) {
			return fmt.Errorf("%s must be an absolute, clean path", name)
		}
	}
	known := map[string]bool{}
	for _, t := range knownTools {
		known[t] = true
	}
	for name, p := range a.Tools {
		if !known[name] {
			return fmt.Errorf("agent.tools has an unknown tool %q (known: %s)", name, strings.Join(sorted(knownTools), ", "))
		}
		if !absClean(p) {
			return fmt.Errorf("agent.tools.%s must be an absolute, clean path", name)
		}
	}
	if m := a.Model; m != nil {
		if !absClean(m.OpenCode) || m.Name == "" || strings.ContainsAny(m.Name, " \t\r\n") {
			return errors.New("agent.model needs an absolute opencode path and a PROVIDER/MODEL name")
		}
		if m.RelayFile != "" && !absClean(m.RelayFile) {
			return errors.New("agent.model.relay_file must be an absolute, clean path")
		}
		if m.ReviewPack != "" && m.ReviewPack != "review-v1" && m.ReviewPack != "review-r1" {
			return errors.New("agent.model.review_pack must be review-v1 or review-r1")
		}
	}
	for _, v := range []int64{c.Quota.SnapshotBytes, c.Quota.WorkspaceBytes, c.Quota.CacheBytes, c.Quota.LogsBytes, c.Quota.ArtifactsBytes} {
		if v < 0 {
			return errors.New("quota values must be 0 (no limit) or positive byte counts")
		}
	}
	if c.BackupDir != "" && !absClean(c.BackupDir) {
		return errors.New("backup_dir must be an absolute, clean path")
	}
	if c.BackupDir != "" && (within(c.BackupDir, c.StateDir) || within(c.StateDir, c.BackupDir)) {
		return errors.New("backup_dir and state_dir must not contain each other")
	}
	if c.Stop.DrainTimeoutSeconds == 0 {
		c.Stop.DrainTimeoutSeconds = DefaultDrainSeconds
	}
	if c.Stop.CancelTimeoutSeconds == 0 {
		c.Stop.CancelTimeoutSeconds = DefaultCancelSeconds
	}
	if c.Stop.DrainTimeoutSeconds < 1 || c.Stop.DrainTimeoutSeconds > 86400 || c.Stop.CancelTimeoutSeconds < 45 || c.Stop.CancelTimeoutSeconds > 3600 {
		return errors.New("stop.drain_timeout_seconds must be 1..86400 and stop.cancel_timeout_seconds 45..3600")
	}
	return nil
}

// within reports whether p is base or below it.
func within(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

// Origin is the controller URL on the loopback listen address.
func (c *Config) Origin() string { return "http://" + c.Listen }
