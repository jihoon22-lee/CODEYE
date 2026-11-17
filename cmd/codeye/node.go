package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	workerrun "github.com/jihoon22-lee/CODEYE/internal/worker/run"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// runNode handles `codeye agent` and `codeye worker` (W06). main.go calls it
// before the control-plane commands; ok=false means "not a node command".
func runNode(ctx context.Context, args []string, out, stderr io.Writer) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "agent":
		return runAgent(ctx, args[1:], stderr), true
	case "worker":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "codeye worker takes no options (started by codeye agent)")
			return 2, true
		}
		return runWorker(ctx, os.Stdin, os.Stdout, stderr), true
	}
	return 0, false
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func runAgent(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	// Do not echo rejected arguments: they may contain credentials.
	fs.SetOutput(io.Discard)
	var roots, gitHosts multiFlag
	controller := fs.String("controller", "", "")
	stateDir := fs.String("state-dir", "", "")
	name := fs.String("name", "", "")
	tokenFile := fs.String("enroll-token-file", "", "")
	workers := fs.Int("workers", 1, "")
	toolPath := fs.String("toolchain-path", "/usr/bin:/bin", "")
	git := fs.String("git", "", "")
	goExe := fs.String("go", "", "")
	goModCache := fs.String("go-mod-cache", "", "")
	fs.Var(&roots, "source-root", "")
	fs.Var(&gitHosts, "git-host", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *controller == "" || *stateDir == "" {
		fmt.Fprintln(stderr, "invalid agent options; use codeye --help")
		return agent.ExitUsage
	}
	abs, err := filepath.Abs(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "invalid --state-dir")
		return agent.ExitUsage
	}
	mapping := map[string]string{}
	for _, r := range roots {
		id, path, ok := strings.Cut(r, "=")
		if !ok || id == "" || path == "" {
			fmt.Fprintln(stderr, "invalid --source-root; expected ID=/absolute/path")
			return agent.ExitUsage
		}
		mapping[strings.ToLower(id)] = filepath.Clean(path)
	}
	cfg := agent.Config{ControllerURL: *controller, StateDir: abs, Name: *name, EnrollTokenFile: *tokenFile, SourceRoots: mapping, Workers: *workers, ToolPath: *toolPath, GitExecutable: *git, GitHosts: gitHosts, GoExecutable: *goExe, GoModCache: *goModCache, Log: stderr}
	ag, err := agent.New(cfg)
	if err == nil {
		err = ag.Run(ctx)
	}
	if err != nil {
		var ee *agent.ExitError
		if errors.As(err, &ee) {
			fmt.Fprintln(stderr, "codeye agent:", ee.Msg)
			return ee.Code
		}
		fmt.Fprintln(stderr, "codeye agent: runtime failure")
		return agent.ExitRuntime
	}
	return agent.ExitOK
}

func runWorker(ctx context.Context, in io.Reader, out, stderr io.Writer) int {
	return workerrun.Main(ctx, workerrun.Config{In: in, Out: out, Stderr: stderr, Handlers: workerHandlers})
}

// workerHandlers registers the handlers of this build. Production without
// W08 has none, so the worker advertises no capability and is never leased.
func workerHandlers(ctx context.Context, h *workerproto.Hello, runner executor.Runner) ([]analysis.Handler, []string, error) {
	hs, missing := builtin.Handlers(ctx, builtin.Config{GoExecutable: h.GoExecutable, GoModCache: h.GoModCache, ProbeDir: h.ScratchRoot}, runner)
	var codes []string
	for _, m := range missing {
		codes = append(codes, m.Name+":"+m.Reason)
	}
	return hs, codes, nil
}
