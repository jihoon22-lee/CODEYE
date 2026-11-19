// Package personal runs CODEYE on one machine (docs/OPERATIONS.md): one JSON
// configuration file, a 0700 state directory, one loopback controller and one
// local node agent started, stopped and inspected by `codeye up|start|stop|status`.
//
// With database.mode "managed" (the default) CODEYE runs its own PostgreSQL
// cluster in the state directory and keeps its generated password there
// (0600). With "external" the operator provides CODEYE_DATABASE_URL in the
// environment and no database credential is stored. The one-time enrollment
// token lives in a 0600 file only until the local agent has enrolled.
package personal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// SchemaVersion of the personal configuration file.
const SchemaVersion = "0.1"

// Database modes.
const (
	// DatabaseManaged: CODEYE initializes and runs PostgreSQL in
	// <state_dir>/postgres (default).
	DatabaseManaged = "managed"
	// DatabaseExternal: the operator provides CODEYE_DATABASE_URL.
	DatabaseExternal = "external"
)

// Config is the personal configuration (docs/OPERATIONS.md#개인-모드-설정-파일). Unknown fields are
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
	// GitHosts are the HTTPS Git hosts projects may be fetched from (public
	// repositories, no credentials). Absent means ["github.com"], or none in
	// offline mode; [] turns Git sources off.
	GitHosts []string `json:"git_hosts,omitempty"`
	// Offline keeps every built-in public default out (docs/OPERATIONS.md#오프라인-모드):
	// dependencies come only from the operator's module cache and the
	// registries set here or in the user's own go env and npm configuration,
	// Git only from git_hosts, the model only through a credential with
	// base_url. `codeye up --offline` sets it.
	Offline bool `json:"offline,omitempty"`
	// CABundle is a PEM file of every CA the tools that reach a registry,
	// a Git host or the model endpoint should trust (it replaces the system
	// store for some of them, so it holds the system CAs too).
	CABundle string `json:"ca_bundle,omitempty"`
	// GitHub is the token discovery of github.com (docs/GITHUB.md).
	GitHub GitHub `json:"github"`

	// listenSet records whether listen was written in the file (a default
	// listen address may move to the next free port).
	listenSet bool
	// gitHostsSet records whether git_hosts was written in the file.
	gitHostsSet bool
}

// ListenExplicit reports whether the configuration file sets listen.
func (c *Config) ListenExplicit() bool { return c.listenSet }

// GitHostsExplicit reports whether the configuration file sets git_hosts.
func (c *Config) GitHostsExplicit() bool { return c.gitHostsSet }

// Managed reports whether CODEYE runs its own PostgreSQL.
func (c *Config) Managed() bool { return c.Database.Mode == DatabaseManaged }

// Database selects how the controller reaches PostgreSQL.
type Database struct {
	Mode string `json:"mode"`
	// PostgresBinDir holds the PostgreSQL programs. In external mode it is
	// where backup and restore find pg_dump and pg_restore (never in PATH);
	// in managed mode it is the first place searched (docs/OPERATIONS.md).
	PostgresBinDir string `json:"postgres_bin_dir,omitempty"`
}

// SourceRoot is a local directory under which projects are registered. The
// label is the source root's identity in the database.
type SourceRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	// RepositoryCode and ModelReview are accepted for older configurations
	// and have no effect: in personal mode every project carries its own
	// choices (run build scripts, send source to the model; both on unless
	// the project turns them off).
	RepositoryCode bool  `json:"repository_code,omitempty"`
	ModelReview    *bool `json:"model_review,omitempty"`
	// Implicit marks the $HOME root that personal mode adds when no
	// configured root covers the home directory; it is never written to the file.
	Implicit bool `json:"-"`
}

// Agent configures the local node agent (codeye agent flags, docs/EXECUTION.md).
type Agent struct {
	Name          string `json:"name,omitempty"`
	MaxWorkers    int    `json:"max_workers,omitempty"`
	ToolchainPath string `json:"toolchain_path,omitempty"`
	Go            string `json:"go,omitempty"`
	// Git is the git executable for Git sources (detected when unset).
	Git                    string            `json:"git,omitempty"`
	GoModCache             string            `json:"go_mod_cache,omitempty"`
	Tools                  map[string]string `json:"tools,omitempty"`
	PythonBothTypeCheckers bool              `json:"python_both_type_checkers,omitempty"`
	Model                  *Model            `json:"model,omitempty"`
	// AutoDetect turns the tool detection off when false (docs/ANALYZERS.md#도구-찾기);
	// absent or true means detect. agent.go, agent.tools and the other fields
	// above always win over what detection finds.
	AutoDetect *bool `json:"auto_detect,omitempty"`
	// Dependencies configures dependency fetching (codeye agent
	// --dependency-fetch and its options, docs/ARCHITECTURE.md#의존성-준비).
	// Absent means on with the defaults; each project can still turn it off.
	Dependencies *Dependencies `json:"dependencies,omitempty"`
}

// Dependencies are the local agent's dependency fetch options. Zero values
// are the defaults.
type Dependencies struct {
	// Enabled turns fetching off for every project when false.
	Enabled *bool `json:"enabled,omitempty"`
	// CacheDir is the shared dependency cache (default
	// <state>/agent/cache/deps).
	CacheDir string `json:"cache_dir,omitempty"`
	// GoProxy, GoSumDB, GoPrivate, GoNoProxy and GoNoSumDB are the Go
	// module download settings (GOPROXY, GOSUMDB, GOPRIVATE, GONOPROXY,
	// GONOSUMDB). Unset ones come from the user's own `go env` when tools are
	// detected; otherwise Go's public proxy and checksum database apply.
	GoProxy   string `json:"go_proxy,omitempty"`
	GoSumDB   string `json:"go_sumdb,omitempty"`
	GoPrivate string `json:"go_private,omitempty"`
	GoNoProxy string `json:"go_noproxy,omitempty"`
	GoNoSumDB string `json:"go_nosumdb,omitempty"`
	// TimeoutSeconds bounds one module (default 600), TotalTimeoutSeconds
	// one run (default 1200), MaxDownloadBytes one module's download
	// (default 2 GiB, soft).
	TimeoutSeconds      int   `json:"timeout_seconds,omitempty"`
	TotalTimeoutSeconds int   `json:"total_timeout_seconds,omitempty"`
	MaxDownloadBytes    int64 `json:"max_download_bytes,omitempty"`
	// NpmRegistry is the registry of npm, pnpm and yarn (--npm-registry).
	// Unset, it comes from the user's own npm configuration when tools are
	// detected; otherwise the package managers' default applies.
	NpmRegistry string `json:"npm_registry,omitempty"`
	// PipIndexURL is the Python package index (--pip-index-url; default
	// PyPI). The interpreter and uv are agent.tools.python and
	// agent.tools.uv.
	PipIndexURL string `json:"pip_index_url,omitempty"`
}

// DependenciesEnabled reports whether the local agent fetches dependencies.
func (a Agent) DependenciesEnabled() bool {
	return a.Dependencies == nil || a.Dependencies.Enabled == nil || *a.Dependencies.Enabled
}

// DependencyCache is the shared dependency cache of the local agent: the
// configured cache_dir, else the state directory's cache/deps.
func (a Agent) DependencyCache(l Layout) string {
	if d := a.Dependencies; d != nil && d.CacheDir != "" {
		return d.CacheDir
	}
	return l.DependencyCacheDir()
}

// AutoDetectEnabled reports whether tools are detected on start.
func (a Agent) AutoDetectEnabled() bool { return a.AutoDetect == nil || *a.AutoDetect }

// Model is the optional review model (docs/REVIEW.md#모델-설정). Every field
// may be empty: the API key comes from a credential file (credential_file,
// <state>/secrets/model.json or OpenCode's auth.json), the model from
// name or the provider's default, OpenCode from ~/.opencode/bin or PATH.
// Without any model the review stage is skipped, never replaced by another model.
type Model struct {
	OpenCode string `json:"opencode,omitempty"`
	// Name is PROVIDER/MODEL, or a model name of the credential's provider.
	Name string `json:"name,omitempty"`
	// CredentialFile is a CODEYE model credential or an OpenCode auth.json.
	CredentialFile string `json:"credential_file,omitempty"`
	RelayFile      string `json:"relay_file,omitempty"`
	// TimeoutSeconds bounds one model call (default 180, 1..1800).
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	ReviewPack     string `json:"review_pack,omitempty"`
	// ReviewMaxCalls is the model call ceiling per review attempt
	// (codeye agent --review-max-calls); 0 means the default.
	ReviewMaxCalls int `json:"review_max_calls,omitempty"`
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
	DefaultToolchainPath  = "/usr/bin:/bin"
	DefaultDrainSeconds   = 300
	DefaultCancelSeconds  = 90
	maxConfigBytes        = 1 << 20
	maxSourceRoots        = 32
	maxAgentWorkersConfig = 16
)

// DefaultGitHosts are the Git hosts of a configuration without git_hosts.
var DefaultGitHosts = []string{"github.com"}

// DefaultMaxWorkers is the local worker cap when agent.max_workers is
// unset: half the logical CPUs, between 1 and 4.
func DefaultMaxWorkers() int {
	return min(4, max(1, runtime.NumCPU()/2))
}

// DefaultPath is the configuration path used when --config is not given.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("cannot determine the user configuration directory; pass --config")
	}
	return filepath.Join(dir, "codeye", "personal.json"), nil
}

// DefaultStateDir is $XDG_STATE_HOME/codeye, or ~/.local/state/codeye.
func DefaultStateDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(filepath.Clean(x), "codeye"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("cannot determine the home directory; set state_dir")
	}
	return filepath.Join(filepath.Clean(home), ".local", "state", "codeye"), nil
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
	if c.StateDir == "" {
		dir, err := DefaultStateDir()
		if err != nil {
			return err
		}
		c.StateDir = dir
	}
	if !absClean(c.StateDir) {
		return errors.New("state_dir must be an absolute, clean path")
	}
	c.listenSet = c.Listen != ""
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
	if c.Database.Mode == "" {
		c.Database.Mode = DatabaseManaged
	}
	switch c.Database.Mode {
	case DatabaseManaged, DatabaseExternal:
	default:
		return errors.New(`database.mode must be "managed" (default) or "external"`)
	}
	if c.Database.PostgresBinDir != "" && !absClean(c.Database.PostgresBinDir) {
		return errors.New("database.postgres_bin_dir must be an absolute, clean path")
	}
	if len(c.SourceRoots) > maxSourceRoots {
		return fmt.Errorf("source_roots has at most %d entries", maxSourceRoots)
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
		// A root may contain the state directory ($HOME holds both); the
		// agent copies one project at a time and refuses a project that
		// contains its data. A root inside the state directory is refused.
		if within(r.Path, c.StateDir) {
			return errors.New("a source root must not be inside state_dir")
		}
	}
	a := &c.Agent
	if a.MaxWorkers == 0 {
		a.MaxWorkers = DefaultMaxWorkers()
	}
	if a.MaxWorkers < 1 || a.MaxWorkers > maxAgentWorkersConfig {
		return errors.New("agent.max_workers must be 1..16")
	}
	if a.Name != "" && (strings.TrimSpace(a.Name) != a.Name || utf8.RuneCountInString(a.Name) > 100) {
		return errors.New("agent.name must be at most 100 characters without surrounding spaces")
	}
	// The default is a Linux PATH; personal start/stop is Linux-only, so
	// on other systems the field stays empty unless set.
	if a.ToolchainPath == "" && runtime.GOOS != "windows" {
		a.ToolchainPath = DefaultToolchainPath
	}
	for _, p := range filepath.SplitList(a.ToolchainPath) {
		if !absClean(p) {
			return errors.New("agent.toolchain_path entries must be absolute, clean paths")
		}
	}
	for name, p := range map[string]string{"agent.go": a.Go, "agent.git": a.Git, "agent.go_mod_cache": a.GoModCache} {
		if p != "" && !absClean(p) {
			return fmt.Errorf("%s must be an absolute, clean path", name)
		}
	}
	if d := a.Dependencies; d != nil {
		if d.CacheDir != "" && !absClean(d.CacheDir) {
			return errors.New("agent.dependencies.cache_dir must be an absolute, clean path")
		}
		for name, v := range map[string]string{"go_proxy": d.GoProxy, "go_sumdb": d.GoSumDB, "go_private": d.GoPrivate, "go_noproxy": d.GoNoProxy, "go_nosumdb": d.GoNoSumDB} {
			if strings.ContainsAny(v, "\x00\n\r") || len(v) > 2048 {
				return fmt.Errorf("agent.dependencies.%s must be one line", name)
			}
		}
		// A credential in a proxy URL would reach the agent's arguments
		// and the worker hello; authenticated proxies are not supported.
		for name, v := range map[string]string{"go_proxy": d.GoProxy, "go_sumdb": d.GoSumDB} {
			if domain.GoSettingCarriesCredentials(v) {
				return fmt.Errorf("agent.dependencies.%s must not carry credentials (user:token@ in a URL)", name)
			}
		}
		if !domain.ValidRegistryURL(d.NpmRegistry) {
			return errors.New("agent.dependencies.npm_registry must be an http(s) URL without credentials")
		}
		if !domain.ValidPackageIndexURL(d.PipIndexURL) {
			return errors.New("agent.dependencies.pip_index_url must be one http(s) URL without credentials (user:token@)")
		}
		if d.TimeoutSeconds < 0 || d.TotalTimeoutSeconds < 0 || d.MaxDownloadBytes < 0 {
			return errors.New("agent.dependencies limits must not be negative")
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
		if m.OpenCode != "" && !absClean(m.OpenCode) {
			return errors.New("agent.model.opencode must be an absolute, clean path")
		}
		if m.Name != "" && !executor.ValidOpenCodeModel(m.Name) && (strings.Contains(m.Name, "/") || !executor.ValidOpenCodeModel("p/"+m.Name)) {
			return errors.New("agent.model.name must be PROVIDER/MODEL or a model name (no spaces)")
		}
		for name, p := range map[string]string{"agent.model.relay_file": m.RelayFile, "agent.model.credential_file": m.CredentialFile} {
			if p != "" && !absClean(p) {
				return fmt.Errorf("%s must be an absolute, clean path", name)
			}
		}
		if m.RelayFile != "" && m.CredentialFile != "" {
			return errors.New("agent.model.relay_file and agent.model.credential_file are alternatives")
		}
		if m.TimeoutSeconds < 0 || m.TimeoutSeconds > 1800 {
			return errors.New("agent.model.timeout_seconds must be 1..1800 (0 = 180)")
		}
		if m.ReviewPack != "" && m.ReviewPack != "review-r1" && m.ReviewPack != "review-v1" {
			return errors.New("agent.model.review_pack must be review-r1 (default) or review-v1 (legacy)")
		}
		if m.ReviewMaxCalls != 0 && !record.ValidMaxCalls(m.ReviewMaxCalls) {
			return errors.New("agent.model.review_max_calls must be between 15 and 207")
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
	if c.CABundle != "" && !absClean(c.CABundle) {
		return errors.New("ca_bundle must be an absolute, clean path")
	}
	c.gitHostsSet = c.GitHosts != nil
	if c.GitHosts == nil {
		c.GitHosts = []string{}
		if !c.Offline {
			c.GitHosts = append(c.GitHosts, DefaultGitHosts...)
		}
	}
	if len(c.GitHosts) > 16 {
		return errors.New("git_hosts has at most 16 entries")
	}
	hosts := map[string]bool{}
	for i, h := range c.GitHosts {
		h = strings.ToLower(h)
		if !validGitHost(h) || hosts[h] {
			return errors.New("git_hosts entries must be unique DNS host names with an optional port (for example github.com)")
		}
		hosts[h], c.GitHosts[i] = true, h
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

// validGitHost accepts a DNS host name with an optional port (no IP literal).
func validGitHost(h string) bool {
	return gitHost.MatchString(h) && !ipLiteral(h)
}

var gitHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*(:[0-9]{1,5})?$`)

func ipLiteral(h string) bool {
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	_, err := netip.ParseAddr(h)
	return err == nil
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
