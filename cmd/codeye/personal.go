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
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
)

// Personal mode (docs/OPERATIONS.md): `codeye start|stop|status` run one loopback
// controller and one local agent from a JSON config. Linux only.

// personalTimings are replaced in tests.
var personalTimings = struct {
	ready, enroll, controllerStop, poll time.Duration
}{ready: 30 * time.Second, enroll: 120 * time.Second, controllerStop: 40 * time.Second, poll: 250 * time.Millisecond}

// personalExecutable is the binary start launches (tests override it).
var personalExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func runPersonal(ctx context.Context, args []string, out, stderr io.Writer) (int, bool) {
	switch args[0] {
	case "start":
		return personalStart(ctx, args[1:], out, stderr), true
	case "stop":
		return personalStop(ctx, args[1:], out, stderr), true
	case "status":
		return personalStatus(ctx, args[1:], out, stderr), true
	case "backup":
		return personalBackup(ctx, args[1:], out, stderr), true
	case "restore":
		return personalRestore(ctx, args[1:], out, stderr), true
	}
	return 0, false
}

func knownTools() []string { return append(builtin.ToolFlags(), builtin.ToolchainFlags()...) }

// loadPersonalConfig parses --config (and the extra flags) and loads it.
func loadPersonalConfig(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (*personal.Config, personal.Layout, bool) {
	var path string
	if !parse(name, args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&path, "config", "", "")
		if define != nil {
			define(fs)
		}
	}) {
		return nil, personal.Layout{}, false
	}
	if path == "" {
		p, err := personal.DefaultPath()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return nil, personal.Layout{}, false
		}
		path = p
	}
	cfg, err := personal.Load(path, knownTools())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, personal.Layout{}, false
	}
	return cfg, personal.Layout{Root: cfg.StateDir}, true
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
func journalProblem(layout personal.Layout) string {
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

// personalStore opens the database named by CODEYE_DATABASE_URL.
func personalStore(ctx context.Context) (*postgres.Store, error) {
	if os.Getenv("CODEYE_DATABASE_URL") == "" {
		return nil, errors.New("CODEYE_DATABASE_URL is not set")
	}
	return openStore(ctx)
}
