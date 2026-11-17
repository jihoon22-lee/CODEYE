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
)

type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Version string `json:"version,omitempty"`
}
type Host struct {
	OS     string  `json:"os"`
	Arch   string  `json:"arch"`
	CPUs   int     `json:"logical_cpus"`
	Memory *uint64 `json:"memory_total_bytes"`
	Disk   *uint64 `json:"disk_available_bytes"`
}
type Report struct {
	SchemaVersion string  `json:"schema_version"`
	Stage         string  `json:"stage"`
	Host          Host    `json:"host"`
	Checks        []Check `json:"checks"`
}
type Options struct {
	WorkDir, DatabaseURL string
	Timeout              time.Duration
}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]+\.[0-9]+(?:\.[0-9]+)?)(?:[^0-9]|$)`)

func version(s string) string {
	m := versionPattern.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func Collect(ctx context.Context, opts Options, runner Runner) Report {
	report := Report{SchemaVersion: "0.1", Stage: "foundation", Host: Host{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}, Checks: []Check{}}
	report.Checks = append(report.Checks, Check{"host", "available", "observed_runtime", ""}, workDisk(opts.WorkDir))
	report.Host.Memory, report.Host.Disk = resources(opts.WorkDir)
	probes := []struct {
		id, name string
		args     []string
	}{
		{"git", "git", []string{"--version"}}, {"go", "go", []string{"version"}},
		{"node", "node", []string{"--version"}}, {"python", "python3", []string{"--version"}},
		{"c_cpp", "cc", []string{"--version"}}, {"rust", "rustc", []string{"--version"}},
		{"postgres_client", "psql", []string{"--version"}}, {"opencode_cli", "opencode", []string{"--version"}},
	}
	for _, probe := range probes {
		r := runner.Run(ctx, Command{Name: probe.name, Args: probe.args, Timeout: opts.Timeout})
		c := Check{ID: probe.id, Status: "available", Reason: "version_only", Version: version(r.Output)}
		if r.Reason != "" {
			c.Status = "unavailable"
			c.Reason = r.Reason
			c.Version = ""
		} else if c.Version == "" {
			c.Status = "unverified"
			c.Reason = "version_unrecognized"
		}
		report.Checks = append(report.Checks, c)
	}
	report.Checks = append(report.Checks, database(ctx, opts, runner), Check{"model", "unverified", "adapter_contract_pending_w10", ""}, Check{"process_control", "unverified", "runtime_process_control_not_probed", ""})
	return report
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

func (r Report) ExitCode() int {
	for _, c := range r.Checks {
		if c.Status != "available" {
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
		if _, err := fmt.Fprintf(w, "%-18s %-16s %-38s %s\n", c.ID, c.Status, c.Reason, c.Version); err != nil {
			return err
		}
	}
	return nil
}
