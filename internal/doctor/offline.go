package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// Offline is the offline readiness section of `codeye doctor --config`
// (docs/OPERATIONS.md#오프라인-준비-점검): every outbound destination the
// configuration leads to and where it comes from, the CA bundle, the
// PostgreSQL programs and, per language, what is missing to analyze and to
// prepare dependencies without the internet. It carries no file path.
type Offline struct {
	Enabled      bool          `json:"enabled"`
	CABundle     CABundle      `json:"ca_bundle"`
	Destinations []Destination `json:"destinations"`
	Postgres     Postgres      `json:"postgres"`
	Languages    []Language    `json:"languages"`
}

// CABundle is the state of the configured ca_bundle.
type CABundle struct {
	// Status: not_configured, available or unavailable.
	Status       string `json:"status"`
	Certificates int    `json:"certificates,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// Destination is one place a run may connect to.
type Destination struct {
	// Kind: go_proxy, go_sumdb, npm_registry, pip_index, crates_registry,
	// git_host or model_endpoint.
	Kind string `json:"kind"`
	// Value is the URL or host; for a built-in model default the provider
	// whose public endpoint OpenCode would use. "" with origin none.
	Value string `json:"value,omitempty"`
	// Origin: config, user_go_env, user_npm_config, model_key_file,
	// relay_file, builtin_default or none.
	Origin string `json:"origin"`
	// Reason says why there is none (offline, not configured).
	Reason string `json:"reason,omitempty"`
}

// Postgres is the PostgreSQL installation a managed cluster would use.
type Postgres struct {
	// Status: found or not_found; Source is where it was found.
	Status   string    `json:"status"`
	Version  string    `json:"version,omitempty"`
	Source   string    `json:"source,omitempty"`
	Programs []Program `json:"programs"`
}

// Program is one PostgreSQL program (found or not_found).
type Program struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Language is what one language needs for analysis without the internet.
type Language struct {
	Language string `json:"language"`
	// Status: ready (nothing missing), partial (the baseline analyzer is
	// there, something else is missing) or unavailable (no baseline).
	Status string `json:"status"`
	// Missing lists missing tool names and "dependency_source" when no
	// dependency source is usable.
	Missing []string `json:"missing"`
	// Dependencies: internal_registry, operator_cache, builtin_default,
	// fetch_disabled, none or not_fetched (the language has no fetch).
	Dependencies string `json:"dependencies"`
}

// Built-in public defaults, shown for online mode (deps, Go, npm, pip, cargo).
const (
	defaultGoProxy     = "https://proxy.golang.org"
	defaultGoSumDB     = "sum.golang.org"
	defaultNpmRegistry = "https://registry.npmjs.org/"
	defaultPipIndex    = "https://pypi.org/simple"
	defaultCrates      = "https://index.crates.io"
)

var postgresPrograms = []string{"initdb", "pg_ctl", "postgres", "pg_dump", "pg_restore", "psql"}

func offlineSection(ctx context.Context, cfg *instance.Config, res toolfind.Result, menv instance.ModelEnv) *Offline {
	o := &Offline{Enabled: cfg.Offline, CABundle: CABundle{Status: "not_configured"}}
	if cfg.CABundle != "" {
		n, err := executor.CheckCABundle(cfg.CABundle)
		if err != nil {
			o.CABundle = CABundle{Status: "unavailable", Reason: "ca_bundle_unreadable"}
		} else {
			o.CABundle = CABundle{Status: "available", Certificates: n}
		}
	}
	o.Destinations = destinations(cfg, res, menv)
	o.Postgres = findPostgres(ctx, cfg)
	o.Languages = languages(cfg, res, o.Destinations)
	return o
}

func destinations(cfg *instance.Config, res toolfind.Result, menv instance.ModelEnv) []Destination {
	d := cfg.Agent.Dependencies
	if d == nil {
		d = &instance.Dependencies{}
	}
	detected := cfg.Agent.AutoDetectEnabled()
	var gs toolfind.GoSettings
	if res.GoSettings != nil && detected {
		gs = *res.GoSettings
	}
	npmUser := ""
	if detected {
		npmUser = res.NpmRegistry
	}
	pick := func(kind, configured, user, userOrigin, builtin string) Destination {
		switch {
		case configured != "":
			return Destination{Kind: kind, Value: configured, Origin: "config"}
		case user != "":
			return Destination{Kind: kind, Value: user, Origin: userOrigin}
		case cfg.Offline:
			return Destination{Kind: kind, Origin: "none", Reason: "offline_no_registry_configured"}
		}
		return Destination{Kind: kind, Value: builtin, Origin: "builtin_default"}
	}
	var out []Destination
	if !cfg.Agent.DependenciesEnabled() {
		for _, k := range []string{"go_proxy", "go_sumdb", "npm_registry", "pip_index", "crates_registry"} {
			out = append(out, Destination{Kind: k, Origin: "none", Reason: "dependency_fetch_disabled"})
		}
	} else {
		out = append(out, pick("go_proxy", d.GoProxy, gs.GoProxy, "user_go_env", defaultGoProxy))
		sum := pick("go_sumdb", d.GoSumDB, gs.GoSumDB, "user_go_env", defaultGoSumDB)
		if sum.Origin == "none" {
			// GOSUMDB=off: go.sum alone verifies the modules.
			sum.Reason = "checksum_database_off"
		}
		out = append(out, sum,
			pick("npm_registry", d.NpmRegistry, npmUser, "user_npm_config", defaultNpmRegistry),
			pick("pip_index", d.PipIndexURL, "", "", defaultPipIndex),
			pick("crates_registry", "", "", "", defaultCrates))
	}
	haveGit := false
	for _, h := range cfg.GitHosts {
		if !github.HostAllowed(h, cfg.Offline, cfg.GitHosts) {
			continue
		}
		haveGit = true
		origin := "config"
		if !cfg.GitHostsExplicit() {
			origin = "builtin_default"
		}
		out = append(out, Destination{Kind: "git_host", Value: h, Origin: origin})
		base, _ := github.ProviderAPI(h, "")
		out = append(out, Destination{Kind: "github_api", Value: base, Origin: origin})
	}
	if !haveGit {
		out = append(out, Destination{Kind: "git_host", Origin: "none", Reason: "git_hosts_empty"})
		out = append(out, Destination{Kind: "github_api", Origin: "none", Reason: "git_hosts_empty"})
	}
	return append(out, modelDestination(cfg, menv))
}

// modelDestination resolves the model like the local agent's start.
func modelDestination(cfg *instance.Config, menv instance.ModelEnv) Destination {
	layout := instance.Layout{Root: cfg.StateDir}
	m, err := instance.ResolveModel(cfg, layout, menv)
	switch {
	case err != nil && cfg.Offline:
		return Destination{Kind: "model_endpoint", Origin: "none", Reason: opencode.ReasonEndpointNotConfigured}
	case err != nil || m == nil:
		return Destination{Kind: "model_endpoint", Origin: "none", Reason: "model_not_configured"}
	case m.RelayFile != "":
		return Destination{Kind: "model_endpoint", Value: "loopback relay", Origin: "relay_file"}
	}
	if m.CredentialFile != "" {
		if p, err := opencode.ReadCredential(m.CredentialFile, m.Provider()); err == nil && p.BaseURL != "" {
			return Destination{Kind: "model_endpoint", Value: p.BaseURL, Origin: "model_key_file"}
		}
	}
	if m.NativeEndpoint != "" {
		return Destination{Kind: "model_endpoint", Value: m.NativeEndpoint, Origin: "config"}
	}
	return Destination{Kind: "model_endpoint", Value: m.Provider(), Origin: "builtin_default"}
}

func findPostgres(ctx context.Context, cfg *instance.Config) Postgres {
	pg := Postgres{Status: "not_found", Programs: []Program{}}
	dir := ""
	if b, err := instance.FindPostgres(ctx, instance.PostgresSearch{Config: cfg.Database.PostgresBinDir, Env: os.Getenv("CODEYE_POSTGRES_BIN_DIR"), PathEnv: os.Getenv("PATH")}); err == nil {
		pg.Status, pg.Version, pg.Source, dir = "found", b.Version, b.Source, b.Dir
	}
	for _, name := range postgresPrograms {
		p := Program{Name: name, Status: "not_found"}
		if dir != "" {
			if st, err := os.Stat(filepath.Join(dir, exeName(name))); err == nil && st.Mode().IsRegular() {
				p.Status = "found"
			}
		} else if _, err := exec.LookPath(name); err == nil {
			p.Status = "found"
		}
		pg.Programs = append(pg.Programs, p)
	}
	return pg
}

// languageNeeds are the tools of each language (the first is the baseline,
// a "|" entry needs one of its alternatives) and its dependency source.
var languageNeeds = []struct {
	language string
	tools    []string
	managers []string
	source   string
}{
	{"go", []string{"go", "staticcheck", "errcheck"}, []string{"go"}, "go_proxy"},
	{"python", []string{"ruff", "mypy|pyright", "bandit", "import-linter"}, []string{"python|uv"}, "pip_index"},
	{"javascript", []string{"eslint", "node", "tsc", "dependency-cruiser"}, []string{"npm|pnpm|yarn"}, "npm_registry"},
	{"rust", []string{"cargo", "clippy"}, []string{"cargo"}, "crates_registry"},
	{"cpp", []string{"cppcheck", "clang-tidy", "gcc"}, nil, ""},
	{"java", []string{"pmd", "java", "spotbugs"}, nil, ""},
	{"common", []string{"git", "gitleaks", "lizard", "jscpd"}, nil, ""},
	{"model", []string{"opencode"}, nil, "model_endpoint"},
}

func languages(cfg *instance.Config, res toolfind.Result, dests []Destination) []Language {
	found := func(names string) bool {
		for _, n := range strings.Split(names, "|") {
			if t, ok := res.Tool(n); ok && t.Status == toolfind.Found {
				return true
			}
		}
		return false
	}
	dest := map[string]Destination{}
	for _, d := range dests {
		dest[d.Kind] = d
	}
	var out []Language
	for _, need := range languageNeeds {
		l := Language{Language: need.language, Status: "ready", Missing: []string{}, Dependencies: "not_fetched"}
		for i, t := range need.tools {
			if !found(t) {
				l.Missing = append(l.Missing, t)
				if i == 0 {
					l.Status = "unavailable"
				}
			}
		}
		if need.source != "" {
			d := dest[need.source]
			switch {
			case need.language == "model":
				l.Dependencies = "not_fetched"
				if d.Origin == "none" {
					l.Missing = append(l.Missing, "model_endpoint")
				}
			case !cfg.Agent.DependenciesEnabled():
				l.Dependencies = "fetch_disabled"
			case d.Origin == "builtin_default":
				l.Dependencies = "builtin_default"
			case d.Origin != "none":
				l.Dependencies = "internal_registry"
			case need.language == "go" && goSeed(cfg, res):
				l.Dependencies = "operator_cache"
			default:
				l.Dependencies = "none"
				l.Missing = append(l.Missing, "dependency_source")
			}
			for _, m := range need.managers {
				if l.Dependencies != "fetch_disabled" && !found(m) && !contains(l.Missing, m) {
					l.Missing = append(l.Missing, m)
				}
			}
		}
		if l.Status == "ready" && len(l.Missing) > 0 {
			l.Status = "partial"
		}
		out = append(out, l)
	}
	return out
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// goSeed reports an operator module cache with downloaded modules (the
// fetch reads it as its first proxy).
func goSeed(cfg *instance.Config, res toolfind.Result) bool {
	dir := cfg.Agent.GoModCache
	if dir == "" && cfg.Agent.AutoDetectEnabled() {
		dir = res.GoModCache
	}
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(dir, "cache", "download"))
	return err == nil && len(entries) > 0
}

func (o *Offline) write(w io.Writer) error {
	if o == nil {
		return nil
	}
	mode := "off"
	if o.Enabled {
		mode = "on"
	}
	ca := o.CABundle.Status
	if o.CABundle.Certificates > 0 {
		ca += fmt.Sprintf(" (%d certificates)", o.CABundle.Certificates)
	}
	if _, err := fmt.Fprintf(w, "\nOffline readiness (offline mode %s, ca_bundle %s)\n", mode, ca); err != nil {
		return err
	}
	for _, d := range o.Destinations {
		v := d.Value
		if v == "" {
			v = "-"
		}
		extra := ""
		if d.Reason != "" {
			extra = " (" + d.Reason + ")"
		}
		if _, err := fmt.Fprintf(w, "destination %-16s %-16s %s%s\n", d.Kind, d.Origin, v, extra); err != nil {
			return err
		}
	}
	var progs []string
	for _, p := range o.Postgres.Programs {
		if p.Status != "found" {
			progs = append(progs, p.Name)
		}
	}
	missing := "-"
	if len(progs) > 0 {
		missing = strings.Join(progs, ",")
	}
	if _, err := fmt.Fprintf(w, "postgres    %-16s %s missing=%s\n", o.Postgres.Status, o.Postgres.Version, missing); err != nil {
		return err
	}
	for _, l := range o.Languages {
		m := "-"
		if len(l.Missing) > 0 {
			m = strings.Join(l.Missing, ",")
		}
		if _, err := fmt.Fprintf(w, "language    %-16s %-12s dependencies=%s missing=%s\n", l.Language, l.Status, l.Dependencies, m); err != nil {
			return err
		}
	}
	return nil
}
