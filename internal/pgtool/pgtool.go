// Package pgtool runs the operator's PostgreSQL client programs (pg_dump,
// pg_restore) for backup and restore. Programs come only from the
// configured binary directory (no PATH search, no download). Credentials are
// passed in the child environment, never in argv, and never logged: errors
// carry the program's last diagnostic line with the password redacted.
package pgtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Conn is a database connection in libpq environment form.
type Conn struct {
	env      []string
	DBName   string
	password string
}

// ParseURL accepts the same CODEYE_DATABASE_URL forms as the controller: one
// host, user, database, only sslmode (verify-full, or disable on loopback)
// and sslrootcert.
func ParseURL(dsn string) (Conn, error) {
	invalid := errors.New("CODEYE_DATABASE_URL is not a supported PostgreSQL URL")
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), ",/\\") ||
		u.User == nil || u.User.Username() == "" || len(u.Path) < 2 || u.Fragment != "" {
		return Conn{}, invalid
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Conn{}, invalid
	}
	for k, v := range q {
		if len(v) != 1 || (k != "sslmode" && k != "sslrootcert") {
			return Conn{}, invalid
		}
	}
	mode := q.Get("sslmode")
	if mode == "" {
		mode = "verify-full"
	}
	loopback := u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if mode != "verify-full" && !(mode == "disable" && loopback) {
		return Conn{}, invalid
	}
	db := strings.TrimPrefix(u.Path, "/")
	// A database name with '=' would be read as a connection string by -d.
	if strings.ContainsAny(db, "=/ \t\r\n") {
		return Conn{}, invalid
	}
	password, _ := u.User.Password()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	env := []string{"PGHOST=" + u.Hostname(), "PGPORT=" + port, "PGUSER=" + u.User.Username(), "PGPASSWORD=" + password,
		"PGDATABASE=" + db, "PGSSLMODE=" + mode, "PGCONNECT_TIMEOUT=10", "PGPASSFILE=" + os.DevNull, "PGAPPNAME=codeye-backup"}
	if cert := q.Get("sslrootcert"); cert != "" {
		env = append(env, "PGSSLROOTCERT="+cert)
	}
	return Conn{env: env, DBName: db, password: password}, nil
}

// Program locates name in binDir: absolute directory, regular executable
// file. Missing programs are a clear error, never a PATH search.
func Program(binDir, name string) (string, error) {
	if binDir == "" {
		return "", errors.New("database.postgres_bin_dir is not configured (pg_dump and pg_restore are never searched in PATH)")
	}
	if !filepath.IsAbs(binDir) {
		return "", errors.New("database.postgres_bin_dir must be absolute")
	}
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	p := filepath.Join(binDir, exe)
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0) {
		return "", fmt.Errorf("%s not found or not executable in database.postgres_bin_dir", name)
	}
	return p, nil
}

var versionPattern = regexp.MustCompile(`\(PostgreSQL\) ([0-9]+)(\.[0-9]+)?`)

// Version runs `<program> --version` and returns e.g. "17.10" and the major.
func Version(ctx context.Context, program string) (string, int, error) {
	out, err := run(ctx, program, []string{"--version"}, nil, 30*time.Second)
	if err != nil {
		return "", 0, err
	}
	m := versionPattern.FindStringSubmatch(string(out))
	if m == nil {
		return "", 0, errors.New(filepath.Base(program) + " version unrecognized")
	}
	var major int
	fmt.Sscanf(m[1], "%d", &major)
	return m[1] + m[2], major, nil
}

// Run runs program with args against c (credentials in the environment
// only) within timeout. stdout is discarded; programs write to files named
// in args.
func (c Conn) Run(ctx context.Context, program string, args []string, timeout time.Duration) error {
	_, err := run(ctx, program, args, c.env, timeout)
	if err != nil && c.password != "" {
		err = errors.New(strings.ReplaceAll(err.Error(), c.password, "[REDACTED]"))
	}
	return err
}

type capped struct{ buf bytes.Buffer }

func (w *capped) Write(p []byte) (int, error) {
	if room := 64*1024 - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

// run executes program with an explicit environment (nothing inherited but
// PATH-free locale basics) and returns its combined output on success.
func run(ctx context.Context, program string, args, env []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = append([]string{"LC_ALL=C", "LANG=C"}, env...)
	if runtime.GOOS == "windows" {
		if v, ok := os.LookupEnv("SystemRoot"); ok {
			cmd.Env = append(cmd.Env, "SystemRoot="+v)
		}
	}
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 5 * time.Second
	var out capped
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s timed out", filepath.Base(program))
	}
	if err != nil {
		return nil, fmt.Errorf("%s failed: %s", filepath.Base(program), lastLine(out.buf.String()))
	}
	return out.buf.Bytes(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300]
			}
			return l
		}
	}
	return "no diagnostic output"
}
