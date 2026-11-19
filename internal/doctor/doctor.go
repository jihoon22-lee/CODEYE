package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Version string `json:"version,omitempty"`
	// Blocking marks a check whose failure makes the exit code 1: something
	// that matters for the mode doctor was asked about.
	Blocking bool `json:"blocking,omitempty"`
}
type Host struct {
	OS     string  `json:"os"`
	Arch   string  `json:"arch"`
	CPUs   int     `json:"logical_cpus"`
	Memory *uint64 `json:"memory_total_bytes"`
	Disk   *uint64 `json:"disk_available_bytes"`
}

// Tool is one detected tool as doctor reports it. It carries no path.
type Tool struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Version       string `json:"version,omitempty"`
	VersionTested bool   `json:"version_tested"`
	Source        string `json:"source,omitempty"`
}
type Report struct {
	SchemaVersion string  `json:"schema_version"`
	Stage         string  `json:"stage"`
	Host          Host    `json:"host"`
	Checks        []Check `json:"checks"`
	// Tools are the tools detection found, searched the way the agent searches
	// (docs/ANALYZERS.md#도구-찾기).
	Tools []Tool `json:"tools"`
	// Operations is present only with --config (docs/OPERATIONS.md#codeye-doctor).
	Operations *Operations `json:"operations,omitempty"`

	detected toolfind.Result
}
type Options struct {
	WorkDir, DatabaseURL string
	Timeout              time.Duration
	// Personal adds the operations section (storage, backup, processes) and
	// supplies the explicit tool settings and the database mode.
	Personal *personal.Config
	// Detect finds the tools; nil runs toolfind with the explicit settings of
	// Personal (and only those when agent.auto_detect is false).
	Detect func(context.Context) toolfind.Result
}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]+\.[0-9]+(?:\.[0-9]+)?)(?:[^0-9]|$)`)

func version(s string) string {
	m := versionPattern.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// explicitTools maps the personal configuration onto the settings that always
// win over detection.
func explicitTools(cfg *personal.Config) toolfind.Explicit {
	if cfg == nil {
		return toolfind.Explicit{}
	}
	a := cfg.Agent
	ex := toolfind.Explicit{Go: a.Go, GoModCache: a.GoModCache, Tools: a.Tools}
	if a.Model != nil {
		ex.OpenCode = a.Model.OpenCode
	}
	return ex
}

func (o Options) detect(ctx context.Context) toolfind.Result {
	if o.Detect != nil {
		return o.Detect(ctx)
	}
	return toolfind.Detect(ctx, toolfind.Options{Explicit: explicitTools(o.Personal), OnlyExplicit: o.Personal != nil && !o.Personal.Agent.AutoDetectEnabled()})
}

// toolChecks are the detected tools doctor reports as checks, by check id.
var toolChecks = []struct{ id, tool string }{{"git", "git"}, {"go", "go"}, {"node", "node"}, {"rust", "cargo"}, {"opencode_cli", "opencode"}}

func Collect(ctx context.Context, opts Options, runner Runner) Report {
	report := Report{SchemaVersion: "0.1", Stage: "foundation", Host: Host{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}, Checks: []Check{}, Tools: []Tool{}}
	disk := workDisk(opts.WorkDir)
	disk.Blocking = disk.Status != "available"
	report.Checks = append(report.Checks, Check{ID: "host", Status: "available", Reason: "observed_runtime"}, disk)
	report.Host.Memory, report.Host.Disk = resources(opts.WorkDir)
	res := opts.detect(ctx)
	report.detected = res
	explicit := explicitTools(opts.Personal)
	for _, tc := range toolChecks {
		report.Checks = append(report.Checks, toolCheck(tc.id, res, tc.tool, explicit.Path(tc.tool) != ""))
	}
	for _, t := range res.Tools {
		report.Tools = append(report.Tools, Tool{Name: t.Name, Status: string(t.Status), Version: t.Version, VersionTested: t.Status == toolfind.Found && t.VersionTested, Source: t.Source})
	}
	// The database client is only used by doctor's own database query.
	psql := runner.Run(ctx, Command{Name: "psql", Args: []string{"--version"}, Timeout: opts.Timeout})
	client := Check{ID: "postgres_client", Status: "available", Reason: "version_only", Version: version(psql.Output)}
	if psql.Reason != "" {
		client.Status, client.Reason, client.Version = "unavailable", psql.Reason, ""
	} else if client.Version == "" {
		client.Status, client.Reason = "unverified", "version_unrecognized"
	}
	report.Checks = append(report.Checks, client, analyzers(res), configuredTools(res))
	db := database(ctx, opts, runner)
	if db.Status == "not_configured" {
		// Without a URL the database is required only by the external mode;
		// the managed mode brings its own.
		db.Blocking = opts.Personal != nil && opts.Personal.Database.Mode == personal.DatabaseExternal
	} else {
		db.Blocking = db.Status != "available"
	}
	report.Checks = append(report.Checks, db)
	if opts.Personal != nil {
		report.Operations = operations(opts.Personal, time.Now())
	}
	return report
}

// toolCheck turns a detection result into a check. Only a tool set explicitly
// in the configuration and not usable blocks: the agent would run with a tool
// that does not work.
func toolCheck(id string, res toolfind.Result, name string, explicit bool) Check {
	t, ok := res.Tool(name)
	c := Check{ID: id, Status: "unavailable", Reason: "executable_missing"}
	switch {
	case !ok || t.Status == toolfind.NotFound:
	case t.Status == toolfind.Found:
		c.Status, c.Reason, c.Version = "available", "version_only", version(t.Version)
		if c.Version == "" {
			c.Status, c.Reason = "unverified", "version_unrecognized"
		}
	default:
		c.Reason = "invalid_executable"
	}
	c.Blocking = explicit && c.Status != "available"
	return c
}

// analyzers reports whether any baseline analyzer is usable: without one an
// analysis records every tool as not run.
func analyzers(res toolfind.Result) Check {
	if res.BaselineUsable() {
		return Check{ID: "analyzers", Status: "available", Reason: "baseline_analyzer_usable"}
	}
	return Check{ID: "analyzers", Status: "unavailable", Reason: "no_baseline_analyzer", Blocking: true}
}

// configuredTools checks the tools the configuration names: a path set by the
// operator that is not usable stays what the agent is given, so it blocks.
func configuredTools(res toolfind.Result) Check {
	for _, t := range res.Tools {
		if t.Status == toolfind.Unusable && t.Source == toolfind.SourceConfig {
			return Check{ID: "configured_tools", Status: "unavailable", Reason: "configured_tool_unusable", Blocking: true}
		}
	}
	return Check{ID: "configured_tools", Status: "available", Reason: "configured_tools_usable"}
}

func workDisk(path string) Check {
	c := Check{ID: "work_disk", Status: "unavailable"}
	if err := os.MkdirAll(path, 0700); err != nil {
		c.Reason = "directory_unavailable"
		return c
	}
	f, err := os.CreateTemp(path, ".doctor-*")
	if err != nil {
		c.Reason = "write_failed"
		return c
	}
	name := f.Name()
	defer os.Remove(name)
	_, writeErr := f.WriteString("codeye-doctor\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		c.Reason = "write_failed"
		return c
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "codeye-doctor\n" {
		c.Reason = "readback_failed"
		return c
	}
	if err = os.Remove(name); err != nil {
		c.Reason = "cleanup_failed"
		return c
	}
	c.Status = "available"
	c.Reason = "write_read_remove_verified"
	return c
}

func database(ctx context.Context, opts Options, runner Runner) Check {
	c := Check{ID: "database", Status: "unavailable"}
	if opts.DatabaseURL == "" {
		c.Status = "not_configured"
		c.Reason = "database_url_missing"
		return c
	}
	// Only explicit single-host URLs are accepted. No inherited service/passfile or host fallback.
	u, err := url.Parse(opts.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || len(u.Path) < 2 || u.Fragment != "" || strings.Contains(u.Host, ",") {
		c.Reason = "invalid_database_url"
		return c
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		c.Reason = "invalid_database_url"
		return c
	}
	for key, values := range query {
		if len(values) != 1 || (key != "sslmode" && key != "sslrootcert") {
			c.Reason = "unsupported_database_option"
			return c
		}
	}
	mode := query.Get("sslmode")
	if mode == "" {
		query.Set("sslmode", "verify-full")
	} else if mode != "verify-full" && mode != "disable" {
		c.Reason = "unsupported_tls_mode"
		return c
	}
	if mode == "disable" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		c.Reason = "tls_required"
		return c
	}
	password, _ := u.User.Password()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	env := []string{"PGHOST=" + u.Hostname(), "PGPORT=" + port, "PGUSER=" + u.User.Username(), "PGPASSWORD=" + password, "PGDATABASE=" + strings.TrimPrefix(u.Path, "/"), "PGSSLMODE=" + query.Get("sslmode"), "PGCONNECT_TIMEOUT=5"}
	if cert := query.Get("sslrootcert"); cert != "" {
		env = append(env, "PGSSLROOTCERT="+cert)
	}
	r := runner.Run(ctx, Command{Name: "psql", Args: []string{"-X", "-w", "-A", "-t", "--set=ON_ERROR_STOP=1", "--set=VERBOSITY=verbose", "-c", "SELECT current_setting('server_version');"}, Env: env, Timeout: opts.Timeout})
	if r.Reason != "" {
		c.Reason = r.Reason
		if r.Reason == "probe_failed" {
			switch {
			case strings.Contains(r.Output, "28P01") || strings.Contains(r.Output, "28000") || strings.Contains(r.Output, "no password supplied") || strings.Contains(r.Output, "password authentication failed"):
				c.Reason = "authentication_failed"
			case strings.Contains(r.Output, "3D000") || (strings.Contains(r.Output, "FATAL:  database ") && strings.Contains(r.Output, "does not exist")):
				c.Reason = "database_missing"
			default:
				c.Reason = "connection_failed"
			}
		}
		return c
	}
	c.Version = version(r.Output)
	if c.Version == "" {
		c.Status = "unverified"
		c.Reason = "server_version_unrecognized"
		return c
	}
	c.Status = "available"
	c.Reason = "authenticated_query_verified"
	return c
}

// ExitCode is 1 when a check that matters for the chosen mode failed (see
// Check.Blocking) and 0 otherwise. A tool that is only missing, or a
// capability doctor does not check, does not change it.
func (r Report) ExitCode() int {
	for _, c := range r.Checks {
		if c.Blocking {
			return 1
		}
	}
	return 0
}
func (r Report) Write(w io.Writer, format string) error {
	if format == "json" {
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		return e.Encode(r)
	}
	if _, err := fmt.Fprintf(w, "CODEYE foundation · %s/%s · %d CPUs\n", r.Host.OS, r.Host.Arch, r.Host.CPUs); err != nil {
		return err
	}
	for _, c := range r.Checks {
		mark := ""
		if c.Blocking {
			mark = "  <- blocks"
		}
		if _, err := fmt.Fprintf(w, "%-18s %-16s %-38s %s%s\n", c.ID, c.Status, c.Reason, c.Version, mark); err != nil {
			return err
		}
	}
	if len(r.detected.Tools) > 0 {
		home, _ := os.UserHomeDir()
		if _, err := fmt.Fprintln(w, "\nTools"); err != nil {
			return err
		}
		if err := toolfind.WriteTable(w, r.detected, home); err != nil {
			return err
		}
	}
	return r.Operations.write(w)
}
