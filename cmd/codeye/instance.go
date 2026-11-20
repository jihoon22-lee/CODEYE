package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps"
)

// The service (docs/OPERATIONS.md): `codeye up|start|stop|status` run one
// loopback controller, one local agent and (database.mode managed) one
// PostgreSQL server from a JSON config. Linux only.

// instanceTimings are replaced in tests.
var instanceTimings = struct {
	ready, enroll, controllerStop, poll time.Duration
}{ready: 30 * time.Second, enroll: 120 * time.Second, controllerStop: 40 * time.Second, poll: 250 * time.Millisecond}

// instanceExecutable is the binary start launches (tests override it).
var instanceExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func runInstance(ctx context.Context, args []string, out, stderr io.Writer) (int, bool) {
	switch args[0] {
	case "up":
		return cmdUp(ctx, args[1:], out, stderr), true
	case "open":
		return cmdOpen(ctx, args[1:], out, stderr), true
	case "start":
		return cmdStart(ctx, args[1:], out, stderr), true
	case "stop":
		return cmdStop(ctx, args[1:], out, stderr), true
	case "status":
		return cmdStatus(ctx, args[1:], out, stderr), true
	case "backup":
		return cmdBackup(ctx, args[1:], out, stderr), true
	case "restore":
		return cmdRestore(ctx, args[1:], out, stderr), true
	}
	return 0, false
}

// knownTools are the tool names of agent.tools: analyzer tools, shared
// toolchains and the dependency tasks' package managers.
func knownTools() []string {
	tools := append(builtin.ToolFlags(), builtin.ToolchainFlags()...)
	// The package managers of the dependency fetch (deps.ManagerFlags).
	return append(tools, deps.ManagerFlags()...)
}

// loadInstanceConfig parses --config (and the extra flags) and loads it.
func loadInstanceConfig(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (*instance.Config, instance.Layout, bool) {
	var path string
	if !parse(name, args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&path, "config", "", "")
		if define != nil {
			define(fs)
		}
	}) {
		return nil, instance.Layout{}, false
	}
	path, err := instanceConfigPath(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, instance.Layout{}, false
	}
	cfg, err := instance.Load(path, knownTools())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, instance.Layout{}, false
	}
	layout := completeConfig(cfg)
	return cfg, layout, true
}

// instanceConfigPath is --config, or the default configuration path.
func instanceConfigPath(path string) (string, error) {
	if path == "" {
		return instance.DefaultPath()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("config path cannot be resolved")
	}
	return abs, nil
}

// completeConfig fills what depends on this host and the running
// installation: the controller's recorded address (when listen is not
// configured it may have moved to a free port), web_dir next to the
// executable and the home directory as a readable folder.
func completeConfig(cfg *instance.Config) instance.Layout {
	layout := instance.Layout{Root: cfg.StateDir}
	addHomeRoot(cfg)
	if !cfg.ListenExplicit() {
		if rec, err := layout.ReadRecord(instance.RoleController); err == nil && rec != nil && rec.Listen != "" {
			cfg.Listen = rec.Listen
		}
	}
	if cfg.WebDir == "" {
		cfg.WebDir = defaultWebDir()
	}
	return layout
}

// homeDir is the user's home directory (tests replace it).
var homeDir = os.UserHomeDir

// addHomeRoot adds $HOME as a readable folder of the administrator unless a
// configured root already holds it, so any folder under the home directory
// can be analyzed from the web UI by its path. The root is implicit: it is
// not written to the configuration file. The state directory may lie inside
// it; the agent refuses only a project that contains its data.
func addHomeRoot(cfg *instance.Config) {
	home, err := homeDir()
	if err != nil || !filepath.IsAbs(home) {
		return
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	home = filepath.Clean(home)
	if home == string(filepath.Separator) || pathWithin(home, cfg.StateDir) || len(cfg.SourceRoots) >= 32 {
		return
	}
	for _, r := range cfg.SourceRoots {
		if r.Implicit || pathWithin(home, r.Path) {
			return
		}
	}
	cfg.SourceRoots = append(cfg.SourceRoots, instance.SourceRoot{Label: uniqueLabel(cfg, "home"), Path: home, Implicit: true})
}

// defaultWebDir is the built web app next to the executable: <exe dir>/web/dist
// or <exe dir>/../web/dist ("" when neither holds index.html).
func defaultWebDir() string {
	exe, err := instanceExecutable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, c := range []string{filepath.Join(dir, "web", "dist"), filepath.Join(filepath.Dir(dir), "web", "dist")} {
		if st, err := os.Stat(filepath.Join(c, "index.html")); err == nil && st.Mode().IsRegular() {
			return c
		}
	}
	return ""
}

// childEnv copies only the named variables of this process.
func childEnv(keys ...string) []string {
	var env []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func httpReady(origin string) (int, bool) {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(origin + "/api/v1/ready")
	if err != nil {
		return 0, false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return resp.StatusCode, resp.StatusCode == http.StatusOK
}

// exited reports a started child's exit without blocking.
type childWatch struct{ done chan error }

func watchChild(cmd *exec.Cmd) childWatch {
	w := childWatch{done: make(chan error, 1)}
	go func() { w.done <- cmd.Wait() }()
	return w
}

func (w childWatch) exited() (bool, int) {
	select {
	case err := <-w.done:
		w.done <- err
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return true, ee.ExitCode()
		}
		return true, 0
	default:
		return false, 0
	}
}

// journalProblem returns why the agent journal is not clean ("" if clean or absent).
func journalProblem(layout instance.Layout) string {
	j, err := agent.ReadJournal(filepath.Join(layout.AgentDir(), "journal.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return "cannot be read"
	}
	return journalUncertainty(j)
}

// journalUncertainty: unfinished attempts, live workers, a torn tail or an
// unconfirmed cleanup report make the agent's cleanup uncertain.
func journalUncertainty(j *agent.Journal) string {
	if j.Torn {
		return "has a torn record"
	}
	for _, w := range j.Workers {
		if !w.Exited {
			return "has a worker without an exit record"
		}
	}
	for _, a := range j.Attempts {
		if !a.Done {
			return "has an unfinished attempt"
		}
	}
	for _, p := range j.Pending {
		if p.CleanupState != "confirmed" {
			return "has an unconfirmed cleanup report"
		}
	}
	return ""
}

// instanceStore opens the installation's database. In managed mode a
// stopped server is started for the command (db.started) and the caller
// stops it again with db.stop.
func instanceStore(ctx context.Context, cfg *instance.Config, layout instance.Layout, notices io.Writer) (*postgres.Store, *instanceDB, error) {
	db, err := instanceDatabase(ctx, cfg, layout, dbOptions{start: true, notices: notices})
	if err != nil {
		return nil, nil, err
	}
	store, err := openStore(ctx)
	if err != nil {
		if db.started {
			_ = db.stop(context.Background())
		}
		return nil, nil, err
	}
	return store, db, nil
}
