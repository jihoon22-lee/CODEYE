package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

const launchTTL = 10 * time.Minute

// openStore is replaced in tests to bind an isolated schema.
var openStore = func(ctx context.Context) (*postgres.Store, error) {
	return postgres.Open(ctx, os.Getenv("CODEYE_DATABASE_URL"))
}

// runControl handles the control-plane commands. ok is false for commands
// it does not own.
func runControl(ctx context.Context, args []string, out, stderr io.Writer) (code int, ok bool) {
	key := args[0]
	if len(args) > 1 && (key == "user" || key == "source-root" || key == "provider") {
		key += " " + args[1]
		args = args[2:]
	} else {
		args = args[1:]
	}
	var cmd func(context.Context, []string, io.Writer, io.Writer) int
	switch key {
	case "user create":
		cmd = userCreate
	case "user disable":
		cmd = userDisable
	case "user passwd":
		cmd = userPasswd
	case "user list":
		cmd = userList
	case "user admin":
		cmd = userAdmin
	case "source-root add":
		cmd = sourceRootAdd
	case "source-root grant":
		cmd = sourceRootGrant
	case "provider add":
		cmd = providerAdd
	case "login-link":
		cmd = loginLink
	case "serve":
		cmd = serve
	case "user", "source-root", "provider":
		fmt.Fprintln(stderr, "missing subcommand; use codeye --help")
		return 2, true
	default:
		return 0, false
	}
	return cmd(ctx, args, out, stderr), true
}

// parse parses flags without echoing rejected values (they may be secrets).
func parse(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) bool {
	_, ok := parseSet(name, args, stderr, define)
	return ok
}

// parseSet is parse that also returns the flag set (fs.Visit lists the flags
// given explicitly).
func parseSet(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (*flag.FlagSet, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	define(fs)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintf(stderr, "invalid %s options; use codeye --help\n", name)
		return fs, false
	}
	return fs, true
}

func withStore(ctx context.Context, stderr io.Writer, fn func(context.Context, *postgres.Store) int) int {
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := openStore(opCtx)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer store.Close()
	return fn(opCtx, store)
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func sourceRootAdd(ctx context.Context, args []string, out, stderr io.Writer) int {
	var label, path string
	if !parse("source-root add", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&label, "label", "", "")
		fs.StringVar(&path, "path", "", "")
	}) || label == "" || path == "" {
		fmt.Fprintln(stderr, "--label and --path are required")
		return 2
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		fmt.Fprintln(stderr, "--path must be an absolute, clean directory path")
		return 2
	}
	// This only checks the directory at registration time. It is not a claim
	// that projects under it will be readable when a snapshot is acquired.
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		fmt.Fprintln(stderr, "--path is not an existing directory")
		return 1
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		r, err := s.AddSourceRoot(ctx, label, path)
		if err != nil {
			fmt.Fprintln(stderr, "cannot add source root:", err)
			return 1
		}
		printJSON(out, map[string]string{"id": r.ID, "label": r.Label})
		return 0
	})
}

func sourceRootGrant(ctx context.Context, args []string, out, stderr io.Writer) int {
	var root, user string
	if !parse("source-root grant", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&root, "root", "", "")
		fs.StringVar(&user, "user", "", "")
	}) || root == "" || user == "" {
		fmt.Fprintln(stderr, "--root and --user are required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if err := s.GrantSourceRoot(ctx, root, user); err != nil {
			fmt.Fprintln(stderr, "cannot grant source root:", err)
			return 1
		}
		fmt.Fprintln(out, "source root granted")
		return 0
	})
}

func providerAdd(ctx context.Context, args []string, out, stderr io.Writer) int {
	var kind, host, api string
	if !parse("provider add", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&kind, "kind", "", "")
		fs.StringVar(&host, "host", "", "")
		fs.StringVar(&api, "api-url", "", "")
	}) || kind == "" || host == "" {
		fmt.Fprintln(stderr, "--kind and --host are required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		p, err := s.AddProvider(ctx, kind, host, api)
		if err != nil {
			fmt.Fprintln(stderr, "cannot add provider:", err)
			return 1
		}
		printJSON(out, map[string]string{"id": p.ID, "kind": p.Kind, "host": p.Host})
		return 0
	})
}

// loginOrigin accepts the origins a login link may point at: http on a
// loopback host with a port (plain loopback serve), or an https public
// origin (serve with TLS).
func loginOrigin(origin string) bool {
	if strings.HasPrefix(origin, "https://") {
		canonical, _, err := httpapi.ParsePublicOrigin(origin)
		return err == nil && canonical == origin
	}
	if loopbackOrigin(origin) {
		return true
	}
	// A plain-HTTP service's public origin (http://HOST[:PORT]); a loopback
	// host must name its port (above).
	canonical, _, err := httpapi.ParseHTTPOrigin(origin)
	if u, perr := url.Parse(origin); perr != nil || isLoopbackName(u.Hostname()) {
		return false
	}
	return err == nil && canonical == origin
}

// loopbackOrigin validates an http origin on a loopback host with a port.
func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return false
	}
	return isLoopbackName(u.Hostname())
}

func isLoopbackName(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// issueLoginURL issues a single-use launch token for user and returns the
// login URL (origin/#launch=TOKEN).
func issueLoginURL(ctx context.Context, s *postgres.Store, user, origin string) (string, error) {
	if !loginOrigin(origin) {
		return "", errors.New("origin must be http://127.0.0.1:PORT, http://localhost:PORT, http://[::1]:PORT or the service's public origin (https://HOST[:PORT], or http://HOST[:PORT] for plain HTTP)")
	}
	token := httpapi.NewToken()
	if err := s.IssueLaunchToken(ctx, user, httpapi.TokenHash(token), launchTTL); err != nil {
		return "", fmt.Errorf("cannot issue login link: %w", err)
	}
	return origin + "/#launch=" + token, nil
}

// writeLoginLink issues a single-use launch token for user and writes the
// login URL to path with mode 0600 via a same-directory rename, so an existing
// symlink at path is replaced rather than followed. The token is not printed.
func writeLoginLink(ctx context.Context, s *postgres.Store, user, origin, path string, out io.Writer) error {
	if !loginOrigin(origin) {
		return errors.New("origin must be http://127.0.0.1:PORT, http://localhost:PORT, http://[::1]:PORT or the service's public origin (https://HOST[:PORT], or http://HOST[:PORT] for plain HTTP)")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the login-link directory")
	}
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		return errors.New("login-link path exists and is not a regular file")
	}
	tmp, err := os.CreateTemp(dir, ".login-link-*")
	if err != nil {
		return errors.New("cannot create the login-link file")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = tmp.Close()
		return errors.New("cannot restrict login-link file permissions")
	}
	link, err := issueLoginURL(ctx, s, user, origin)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(link + "\n"); err != nil {
		_ = tmp.Close()
		return errors.New("cannot write the login-link file")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("cannot write the login-link file")
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errors.New("cannot place the login-link file")
	}
	expires := time.Now().Add(launchTTL).UTC().Format(time.RFC3339)
	fmt.Fprintf(out, "login link written to %s (single use, expires about %s); open the URL in it with a browser\n", path, expires)
	return nil
}

func loginLink(ctx context.Context, args []string, out, stderr io.Writer) int {
	var user, output, origin string
	if !parse("login-link", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "user", "", "")
		fs.StringVar(&output, "output", filepath.Join(".codeye", "login-link"), "")
		fs.StringVar(&origin, "origin", "http://127.0.0.1:7331", "")
	}) || user == "" || output == "" {
		fmt.Fprintln(stderr, "--user is required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if err := writeLoginLink(ctx, s, user, origin, output, out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	})
}

// serveConfig is the parsed serve command line.
type serveConfig struct {
	addr                   netip.AddrPort
	webDir, user, linkFile string
	artifactDir            string
	reconcile              time.Duration
	tlsCert, tlsKey        string
	// publicOrigins are the --public-origin values (repeatable).
	publicOrigins []string
	// plainHTTP serves a non-loopback address without TLS (--plain-http).
	plainHTTP bool
	// local is the optional loopback address for the agent and commands on
	// this machine (--local-listen).
	local netip.AddrPort
	// sessionTTL is the browser session lifetime (--session-ttl; 0 is
	// httpapi.DefaultSessionTTL). codeye start gives httpapi.ServiceSessionTTL.
	sessionTTL time.Duration
	// stateDir is the installation's state directory (httpapi.Instance):
	// the setup summary, detected tools and platform GitHub credentials.
	stateDir       string
	instanceConfig string
	offline        bool
	gitHosts       []string
	caBundle       string
}

func serve(ctx context.Context, args []string, out, stderr io.Writer) int {
	var listen, local string
	var c serveConfig
	if !parse("serve", args, stderr, func(fs *flag.FlagSet) {
		fs.BoolVar(&c.plainHTTP, "plain-http", false, "")
		fs.StringVar(&local, "local-listen", "", "")
		fs.DurationVar(&c.reconcile, "reconcile-interval", 5*time.Second, "")
		fs.StringVar(&listen, "listen", "127.0.0.1:7331", "")
		fs.StringVar(&c.webDir, "web-dir", filepath.Join("web", "dist"), "")
		fs.StringVar(&c.artifactDir, "artifact-dir", filepath.Join(".codeye", "artifacts"), "")
		fs.StringVar(&c.user, "user", "", "")
		fs.StringVar(&c.linkFile, "login-link-file", filepath.Join(".codeye", "login-link"), "")
		fs.StringVar(&c.tlsCert, "tls-cert", "", "")
		fs.StringVar(&c.tlsKey, "tls-key", "", "")
		fs.Func("public-origin", "", func(v string) error { c.publicOrigins = append(c.publicOrigins, v); return nil })
		fs.DurationVar(&c.sessionTTL, "session-ttl", 0, "")
		fs.StringVar(&c.stateDir, "state-dir", "", "")
		fs.StringVar(&c.instanceConfig, "instance-config", "", "")
		fs.BoolVar(&c.offline, "offline", false, "")
		fs.Func("git-host", "", func(v string) error {
			if err := domain.ValidateHost(v); err != nil {
				return err
			}
			c.gitHosts = append(c.gitHosts, strings.ToLower(v))
			return nil
		})
		fs.StringVar(&c.caBundle, "ca-bundle", "", "")
	}) {
		return 2
	}
	addr, err := httpapi.ParseListen(listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	c.addr = addr
	if c.sessionTTL < 0 || c.sessionTTL > httpapi.ServiceSessionTTL {
		fmt.Fprintln(stderr, "--session-ttl must be between 0 (the default, 12h) and 168h")
		return 2
	}
	if local != "" {
		la, err := httpapi.ParseListen(local)
		if err != nil || !la.Addr().IsLoopback() {
			fmt.Fprintln(stderr, "--local-listen must be a loopback IP:port")
			return 2
		}
		c.local = la
	}
	tls := c.tlsCert != "" || c.tlsKey != ""
	switch {
	case tls && c.plainHTTP:
		fmt.Fprintln(stderr, "--plain-http and --tls-cert/--tls-key are alternatives")
		return 2
	case tls && (c.tlsCert == "" || c.tlsKey == "" || len(c.publicOrigins) == 0):
		fmt.Fprintln(stderr, "TLS needs all of --tls-cert, --tls-key and --public-origin https://HOST[:PORT]")
		return 2
	case !tls && !c.plainHTTP && len(c.publicOrigins) != 0:
		fmt.Fprintln(stderr, "--public-origin needs TLS (--tls-cert, --tls-key) or --plain-http")
		return 2
	case !tls && !c.plainHTTP && !addr.Addr().IsLoopback():
		fmt.Fprintln(stderr, httpapi.ErrTLSRequired)
		return 2
	}
	for _, o := range c.publicOrigins {
		parseOrigin := httpapi.ParseHTTPOrigin
		if tls {
			parseOrigin = httpapi.ParsePublicOrigin
		}
		if _, _, err := parseOrigin(o); err != nil {
			fmt.Fprintln(stderr, "--public-origin:", err)
			return 2
		}
	}
	if c.reconcile == 0 || c.reconcile > time.Hour || (c.reconcile > 0 && c.reconcile < 100*time.Millisecond) {
		fmt.Fprintln(stderr, "--reconcile-interval must be between 100ms and 1h (negative disables the autoscaler and schedules)")
		return 2
	}
	if c.artifactDir == "" {
		fmt.Fprintln(stderr, "--artifact-dir must not be empty")
		return 2
	}
	if c.stateDir != "" && !filepath.IsAbs(c.stateDir) {
		fmt.Fprintln(stderr, "--state-dir must be an absolute path")
		return 2
	}
	if c.instanceConfig != "" && (c.stateDir == "" || !filepath.IsAbs(c.instanceConfig)) {
		fmt.Fprintln(stderr, "--instance-config needs an absolute configuration path and --state-dir")
		return 2
	}
	if c.artifactDir, err = filepath.Abs(c.artifactDir); err != nil {
		fmt.Fprintln(stderr, "--artifact-dir cannot be resolved")
		return 2
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := openStore(startCtx)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer store.Close()
	st, err := store.SchemaStatus(startCtx)
	if err != nil {
		fmt.Fprintln(stderr, "cannot read database schema status:", err)
		return 1
	}
	if st.State != "current" {
		fmt.Fprintf(stderr, "database schema is %s (applied %d, this binary expects %d); run \"codeye migrate\" explicitly after reviewing it\n", st.State, st.Applied, st.Latest)
		return 1
	}
	return serveOn(ctx, startCtx, store, c, out, stderr)
}

func serveOn(ctx, startCtx context.Context, store *postgres.Store, c serveConfig, out, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	cfg := httpapi.Config{Listen: c.addr, WebDir: c.webDir, Logger: logger, ArtifactDir: c.artifactDir, ReconcileInterval: c.reconcile,
		TLSCertFile: c.tlsCert, TLSKeyFile: c.tlsKey, PublicOrigins: c.publicOrigins, PlainHTTP: c.plainHTTP, LocalListen: c.local}
	cfg.SessionTTL = c.sessionTTL
	if c.stateDir != "" || c.offline {
		cfg.Instance = &httpapi.Instance{StateDir: c.stateDir, Offline: c.offline, GitHosts: c.gitHosts, CABundle: c.caBundle}
		if c.instanceConfig != "" {
			cfg.Instance.RuntimePaths = managedRuntimePaths{path: c.instanceConfig, layout: instance.Layout{Root: c.stateDir}}
			cfg.Instance.OpenCode = newManagedOpenCode(c.instanceConfig, c.stateDir, c.offline, c.caBundle)
		}
	}
	srv, err := httpapi.New(cfg, store)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer srv.Close()
	ln, err := net.Listen("tcp", c.addr.String())
	if err != nil {
		fmt.Fprintln(stderr, "cannot listen:", strings.TrimSpace(err.Error()))
		return 1
	}
	if c.local.IsValid() {
		lln, err := net.Listen("tcp", c.local.String())
		if err != nil {
			_ = ln.Close()
			fmt.Fprintln(stderr, "cannot listen on the local address:", strings.TrimSpace(err.Error()))
			return 1
		}
		go func() {
			if err := srv.ServeLocal(ctx, lln); err != nil {
				fmt.Fprintln(stderr, "local listener stopped:", err)
			}
		}()
	}
	origin := "http://" + c.addr.String()
	if o := srv.Origin(); o != "" {
		origin = o
	}
	if c.plainHTTP {
		fmt.Fprintln(out, "plain HTTP on", c.addr.String(), "- sign-in cookies and passwords travel unencrypted on this network")
	}
	if c.user != "" {
		if err := writeLoginLink(startCtx, store, c.user, origin, c.linkFile, out); err != nil {
			_ = ln.Close()
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if srv.Secure() {
		fmt.Fprintf(out, "CODEYE controller API on %s over TLS, listening on %s (node protocol enabled; runs wait until a capable node claims them)\n", origin, c.addr)
	} else {
		fmt.Fprintf(out, "CODEYE controller API on %s (node protocol enabled; runs wait until a capable node claims them)\n", origin)
	}
	if err := srv.Serve(ctx, ln); err != nil {
		fmt.Fprintln(stderr, "server stopped:", err)
		return 1
	}
	return 0
}
