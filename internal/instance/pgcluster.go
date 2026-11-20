package instance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Managed PostgreSQL (database.mode "managed"): CODEYE initializes and runs
// its own PostgreSQL cluster in <state_dir>/postgres with the operator's
// installed binaries. The server listens on 127.0.0.1 only, uses
// scram-sha-256 and a generated password kept in <state_dir>/postgres/password
// (0600). Nothing is downloaded.

// PostgreSQL programs a managed cluster needs.
var postgresPrograms = []string{"initdb", "pg_ctl", "postgres", "pg_dump", "pg_restore"}

// MinPostgresMajor is the oldest PostgreSQL major version a managed cluster uses.
const MinPostgresMajor = 14

// Managed cluster constants.
const (
	ManagedDatabase = "codeye"
	ManagedUser     = "codeye"
	firstPGPort     = 55432
	pgPortRange     = 200
)

// PostgresBin is a directory holding every program of postgresPrograms.
type PostgresBin struct {
	Dir     string
	Version string // e.g. "17.10"
	Major   int
	// Source says where Dir came from (flag, config, environment, PATH, system).
	Source string
}

// PostgresSearch says where to look for PostgreSQL binaries. Explicit
// directories (Flag, Config, Env, in that order) must hold a usable
// installation; otherwise PATH and the usual system locations are searched
// and the highest major version is used (or the one matching DataMajor, the
// version of an existing data directory, when it is installed).
type PostgresSearch struct {
	Flag, Config, Env string
	PathEnv           string
	DataMajor         int
	// SystemGlobs replace the system locations (tests).
	SystemGlobs []string
}

var defaultSystemGlobs = []string{"/usr/lib/postgresql/*/bin", "/usr/pgsql-*/bin", "/usr/local/pgsql/bin"}

// ErrPostgresNotFound is returned (wrapped with the searched list) when no
// usable installation exists.
var ErrPostgresNotFound = errors.New("PostgreSQL binaries not found")

// FindPostgres locates the PostgreSQL binaries of a managed cluster.
func FindPostgres(ctx context.Context, s PostgresSearch) (PostgresBin, error) {
	for _, e := range []struct{ dir, source string }{{s.Flag, "--postgres-bin-dir"}, {s.Config, "database.postgres_bin_dir"}, {s.Env, "CODEYE_POSTGRES_BIN_DIR"}} {
		if e.dir == "" {
			continue
		}
		b, err := probePostgresDir(ctx, e.dir)
		if err != nil {
			return PostgresBin{}, fmt.Errorf("%s %s: %w", e.source, e.dir, err)
		}
		b.Source = e.source
		return b, nil
	}
	seen := map[string]bool{}
	var found []PostgresBin
	consider := func(dir, source string) {
		dir = filepath.Clean(dir)
		if dir == "" || !filepath.IsAbs(dir) || seen[dir] {
			return
		}
		seen[dir] = true
		if b, err := probePostgresDir(ctx, dir); err == nil {
			b.Source = source
			found = append(found, b)
		}
	}
	for _, dir := range filepath.SplitList(s.PathEnv) {
		if _, err := os.Stat(filepath.Join(dir, exeName("initdb"))); err == nil {
			consider(dir, "PATH")
		}
	}
	globs := s.SystemGlobs
	if globs == nil {
		globs = defaultSystemGlobs
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		if len(matches) == 0 && !strings.Contains(g, "*") {
			matches = []string{g}
		}
		sort.Strings(matches)
		for _, m := range matches {
			consider(m, "system")
		}
	}
	if len(found) == 0 {
		return PostgresBin{}, fmt.Errorf("%w (searched: CODEYE_POSTGRES_BIN_DIR, PATH, %s; version %d or newer with %s). Install `postgresql` (Ubuntu: `sudo apt install postgresql`), or set `database.postgres_bin_dir`, or use `database.mode: external`",
			ErrPostgresNotFound, strings.Join(globs, ", "), MinPostgresMajor, strings.Join(postgresPrograms, ", "))
	}
	if s.DataMajor > 0 {
		for _, b := range found {
			if b.Major == s.DataMajor {
				return b, nil
			}
		}
	}
	best := found[0]
	for _, b := range found[1:] {
		if b.Major > best.Major {
			best = b
		}
	}
	return best, nil
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

var pgVersionPattern = regexp.MustCompile(`\(PostgreSQL\) ([0-9]+)(\.[0-9]+)?`)

// probePostgresDir checks that dir holds every program and reads the version
// from pg_ctl --version.
func probePostgresDir(ctx context.Context, dir string) (PostgresBin, error) {
	if !filepath.IsAbs(dir) {
		return PostgresBin{}, errors.New("not an absolute path")
	}
	var missing []string
	for _, p := range postgresPrograms {
		st, err := os.Stat(filepath.Join(dir, exeName(p)))
		if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return PostgresBin{}, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	out, _, err := runPG(ctx, filepath.Join(dir, exeName("pg_ctl")), []string{"--version"}, 30*time.Second)
	if err != nil {
		return PostgresBin{}, err
	}
	m := pgVersionPattern.FindStringSubmatch(string(out))
	if m == nil {
		return PostgresBin{}, errors.New("pg_ctl version unrecognized")
	}
	major, _ := strconv.Atoi(m[1])
	if major < MinPostgresMajor {
		return PostgresBin{}, fmt.Errorf("PostgreSQL %s is older than %d", m[1]+m[2], MinPostgresMajor)
	}
	return PostgresBin{Dir: dir, Version: m[1] + m[2], Major: major}, nil
}

// Cluster is the managed PostgreSQL cluster of one state directory.
type Cluster struct {
	Bin    PostgresBin
	Layout Layout
}

func (l Layout) PostgresDir() string      { return filepath.Join(l.Root, "postgres") }
func (l Layout) PostgresData() string     { return filepath.Join(l.PostgresDir(), "data") }
func (l Layout) PostgresRun() string      { return filepath.Join(l.PostgresDir(), "run") }
func (l Layout) PostgresPassword() string { return filepath.Join(l.PostgresDir(), "password") }
func (l Layout) PostgresLog() string      { return filepath.Join(l.LogDir(), "postgres.log") }

// managedConf is included from postgresql.conf and rewritten before every
// start (listen address, port, socket directory).
const managedConf = "codeye.conf"

// DataMajor returns the major version recorded in the data directory
// (PG_VERSION), or 0 when the cluster was never initialized.
func (l Layout) DataMajor() (int, error) {
	raw, err := os.ReadFile(filepath.Join(l.PostgresData(), "PG_VERSION"))
	if errors.Is(err, fs.ErrNotExist) {
		if entries, derr := os.ReadDir(l.PostgresData()); derr == nil && len(entries) > 0 {
			return 0, fmt.Errorf("%s is not empty but is not a PostgreSQL data directory; move it away", l.PostgresData())
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cannot read %s", filepath.Join(l.PostgresData(), "PG_VERSION"))
	}
	major, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || major <= 0 {
		return 0, fmt.Errorf("%s has an unreadable PG_VERSION", l.PostgresData())
	}
	return major, nil
}

// CheckVersion refuses binaries whose major version differs from the data
// directory's.
func (c Cluster) CheckVersion() error {
	major, err := c.Layout.DataMajor()
	if err != nil {
		return err
	}
	if major != 0 && major != c.Bin.Major {
		return fmt.Errorf("the managed data directory %s was created by PostgreSQL %d, found %d binaries (%s) - point `database.postgres_bin_dir` at PostgreSQL %d binaries, or back up with those and restore into a new state_dir",
			c.Layout.PostgresData(), major, c.Bin.Major, c.Bin.Dir, major)
	}
	return nil
}

// Initialized reports whether the data directory holds a cluster.
func (c Cluster) Initialized() bool {
	major, err := c.Layout.DataMajor()
	return err == nil && major > 0
}

// Init creates the cluster: superuser codeye, UTF8, C locale,
// scram-sha-256 for every connection and a generated password.
func (c Cluster) Init(ctx context.Context) error {
	if err := os.MkdirAll(c.Layout.PostgresDir(), 0o700); err != nil {
		return errors.New("cannot create the postgres state directory")
	}
	if err := checkPrivateDir(c.Layout.PostgresDir()); err != nil {
		return err
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return errors.New("cannot generate the database password")
	}
	password := hex.EncodeToString(secret)
	pw, err := os.CreateTemp(c.Layout.PostgresDir(), ".pwfile-*")
	if err != nil {
		return errors.New("cannot create the initdb password file")
	}
	pwName := pw.Name()
	defer os.Remove(pwName)
	if err := pw.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = pw.Close()
		return errors.New("cannot restrict the initdb password file")
	}
	if _, err := pw.WriteString(password + "\n"); err != nil {
		_ = pw.Close()
		return errors.New("cannot write the initdb password file")
	}
	if err := pw.Close(); err != nil {
		return errors.New("cannot write the initdb password file")
	}
	args := []string{"-D", c.Layout.PostgresData(), "-U", ManagedUser, "-E", "UTF8", "--locale=C", "--auth=scram-sha-256", "--pwfile=" + pwName, "--no-instructions"}
	if out, _, err := runPG(ctx, c.program("initdb"), args, 5*time.Minute); err != nil {
		return fmt.Errorf("initdb failed: %v%s", err, tail(out))
	}
	conf := filepath.Join(c.Layout.PostgresData(), "postgresql.conf")
	f, err := os.OpenFile(conf, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return errors.New("cannot extend postgresql.conf")
	}
	_, werr := f.WriteString("\n# Written by codeye: listen address, port and socket directory.\ninclude_if_exists = '" + managedConf + "'\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		return errors.New("cannot extend postgresql.conf")
	}
	// Only after initdb succeeded: a stored password always matches a cluster.
	return writePrivate(c.Layout.PostgresPassword(), []byte(password+"\n"))
}

func (c Cluster) program(name string) string { return filepath.Join(c.Bin.Dir, exeName(name)) }

// Status returns the port of the running server (0, false when stopped).
func (c Cluster) Status(ctx context.Context) (int, bool, error) {
	_, code, err := runPG(ctx, c.program("pg_ctl"), []string{"status", "-D", c.Layout.PostgresData()}, 30*time.Second)
	switch {
	case err == nil:
	case code == 3 || code == 4:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("pg_ctl status failed: %v", err)
	}
	port, err := c.livePort()
	if err != nil {
		return 0, true, err
	}
	return port, true, nil
}

// livePort reads the port from postmaster.pid (line 4).
func (c Cluster) livePort() (int, error) {
	f, err := os.Open(filepath.Join(c.Layout.PostgresData(), "postmaster.pid"))
	if err != nil {
		return 0, errors.New("cannot read postmaster.pid")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		if line == 4 {
			port, err := strconv.Atoi(strings.TrimSpace(sc.Text()))
			if err != nil || port <= 0 || port > 65535 {
				break
			}
			return port, nil
		}
	}
	return 0, errors.New("postmaster.pid has no port")
}

// Start starts the server on 127.0.0.1 (the previous port when it is free,
// else the first free port from 55432) and waits until it accepts
// connections. It returns the port.
func (c Cluster) Start(ctx context.Context) (int, error) {
	if err := c.CheckVersion(); err != nil {
		return 0, err
	}
	port, err := c.choosePort()
	if err != nil {
		return 0, err
	}
	conf := "listen_addresses = '127.0.0.1'\nport = " + strconv.Itoa(port) + "\n"
	sockets := c.Layout.PostgresRun()
	// A Unix socket path is limited to about 107 bytes; TCP is what CODEYE
	// uses, so a too long directory just disables the socket.
	if len(sockets)+len("/.s.PGSQL.65535") >= 100 {
		conf += "unix_socket_directories = ''\n"
	} else {
		if err := os.MkdirAll(sockets, 0o700); err != nil {
			return 0, errors.New("cannot create the postgres socket directory")
		}
		conf += "unix_socket_directories = '" + strings.ReplaceAll(sockets, "'", "''") + "'\n"
	}
	if err := writePrivate(filepath.Join(c.Layout.PostgresData(), managedConf), []byte(conf)); err != nil {
		return 0, errors.New("cannot write " + managedConf)
	}
	if err := os.MkdirAll(c.Layout.LogDir(), 0o700); err != nil {
		return 0, errors.New("cannot create the log directory")
	}
	args := []string{"start", "-D", c.Layout.PostgresData(), "-l", c.Layout.PostgresLog(), "-w", "-t", "90", "-s"}
	if out, _, err := runPG(ctx, c.program("pg_ctl"), args, 2*time.Minute); err != nil {
		return 0, fmt.Errorf("PostgreSQL did not start (%v%s); see %s", err, tail(out), c.Layout.PostgresLog())
	}
	if live, err := c.livePort(); err == nil {
		port = live
	}
	return port, nil
}

// Stop shuts the server down (fast mode: sessions are disconnected,
// transactions rolled back, the data is consistent).
func (c Cluster) Stop(ctx context.Context) error {
	if out, _, err := runPG(ctx, c.program("pg_ctl"), []string{"stop", "-D", c.Layout.PostgresData(), "-m", "fast", "-w", "-t", "90", "-s"}, 2*time.Minute); err != nil {
		return fmt.Errorf("pg_ctl stop failed: %v%s", err, tail(out))
	}
	return nil
}

// choosePort keeps the port of codeye.conf when it is free, else takes the
// first free port from 55432.
func (c Cluster) choosePort() (int, error) {
	if raw, err := os.ReadFile(filepath.Join(c.Layout.PostgresData(), managedConf)); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "port = "); ok {
				if p, err := strconv.Atoi(v); err == nil && PortFree("127.0.0.1", p) {
					return p, nil
				}
			}
		}
	}
	for p := firstPGPort; p < firstPGPort+pgPortRange; p++ {
		if PortFree("127.0.0.1", p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in 127.0.0.1:%d-%d for PostgreSQL", firstPGPort, firstPGPort+pgPortRange-1)
}

// PortFree reports whether a TCP listener can bind host:port now.
func PortFree(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// Password reads the generated password.
func (c Cluster) Password() (string, error) {
	raw, err := os.ReadFile(c.Layout.PostgresPassword())
	if err != nil {
		return "", fmt.Errorf("cannot read %s", c.Layout.PostgresPassword())
	}
	pw := strings.TrimSpace(string(raw))
	if pw == "" {
		return "", fmt.Errorf("%s is empty", c.Layout.PostgresPassword())
	}
	return pw, nil
}

// URL is the CODEYE_DATABASE_URL of database db on the running server.
func (c Cluster) URL(port int, db string) (string, error) {
	pw, err := c.Password()
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(ManagedUser, pw), Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), Path: "/" + db, RawQuery: "sslmode=disable"}
	return u.String(), nil
}

// EnsureDatabase creates the codeye database when it does not exist. It
// reports whether it was created.
func (c Cluster) EnsureDatabase(ctx context.Context, port int) (bool, error) {
	dsn, err := c.URL(port, "postgres")
	if err != nil {
		return false, err
	}
	cfg, err := pgx.ParseConfig(dsn + "&connect_timeout=10")
	if err != nil {
		return false, errors.New("cannot build the managed database connection")
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return false, errors.New("cannot connect to the managed PostgreSQL")
	}
	defer conn.Close(context.Background())
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", ManagedDatabase).Scan(&exists); err != nil {
		return false, errors.New("cannot list the managed databases")
	}
	if exists {
		return false, nil
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+ManagedDatabase+" ENCODING 'UTF8' TEMPLATE template0"); err != nil {
		return false, errors.New("cannot create the codeye database")
	}
	return true, nil
}

// runPG runs a PostgreSQL program with an explicit environment (no PG*
// variable, C locale) and returns its capped combined output and exit code.
func runPG(ctx context.Context, program string, args []string, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C", "PATH=" + filepath.Dir(program) + string(os.PathListSeparator) + "/usr/bin:/bin"}
	if runtime.GOOS == "windows" {
		if v, ok := os.LookupEnv("SystemRoot"); ok {
			cmd.Env = append(cmd.Env, "SystemRoot="+v)
		}
	}
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 5 * time.Second
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), -1, fmt.Errorf("%s timed out", filepath.Base(program))
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), ee.ExitCode(), fmt.Errorf("%s exited with %d", filepath.Base(program), ee.ExitCode())
	}
	if err != nil {
		return out.Bytes(), -1, fmt.Errorf("cannot run %s", filepath.Base(program))
	}
	return out.Bytes(), 0, nil
}

type cappedBuffer struct{ bytes.Buffer }

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if room := 64*1024 - w.Len(); room > 0 {
		if len(p) > room {
			w.Buffer.Write(p[:room])
		} else {
			w.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// tail returns ": <last line>" of a program's output ("" when empty).
func tail(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300]
			}
			return ": " + l
		}
	}
	return ""
}
