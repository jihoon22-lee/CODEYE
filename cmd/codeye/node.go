package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	analyzerpack "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
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
	var roots, gitHosts, modelRoots, codeRoots multiFlag
	modelExe := fs.String("opencode", "", "")
	modelName := fs.String("model", "", "")
	modelRelay := fs.String("model-relay-file", "", "")
	// The review pack is a node operator setting only; runs and profiles
	// cannot select it. review-r1 is the W15 candidate, not the default.
	reviewPack := fs.String("review-pack", review.PackV1, "")
	fs.Var(&modelRoots, "model-source-root", "")
	// The repository-code grant (docs/39 §2.7) is a node operator setting
	// per local source root, like the model-source grant; nothing in a run
	// or its snapshot can raise it.
	fs.Var(&codeRoots, "repo-code-source-root", "")
	controller := fs.String("controller", "", "")
	stateDir := fs.String("state-dir", "", "")
	name := fs.String("name", "", "")
	tokenFile := fs.String("enroll-token-file", "", "")
	workers := fs.Int("workers", 1, "")
	autoscale := fs.Bool("autoscale", false, "")
	maxWorkers := fs.Int("max-workers", 2, "")
	toolPath := fs.String("toolchain-path", "/usr/bin:/bin", "")
	git := fs.String("git", "", "")
	goExe := fs.String("go", "", "")
	goModCache := fs.String("go-mod-cache", "", "")
	// One flag per analyzer tool of this build (--staticcheck PATH) and per
	// shared toolchain (--node PATH, docs/39 §2.4). Both travel in the hello's
	// analyzer_tools map; toolchain names are reserved there.
	toolFlags := map[string]*string{}
	for _, tool := range append(builtin.ToolFlags(), builtin.ToolchainFlags()...) {
		toolFlags[tool] = fs.String(tool, "", "")
	}
	// mypy and pyright are alternative Python type checkers (docs/42 §5):
	// configuring both runs both only when the operator says so.
	bothTypeCheckers := fs.Bool("python-both-type-checkers", false, "")
	fs.Var(&roots, "source-root", "")
	fs.Var(&gitHosts, "git-host", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *controller == "" || *stateDir == "" {
		fmt.Fprintln(stderr, "invalid agent options; use codeye --help")
		return agent.ExitUsage
	}
	if !review.ValidPack(*reviewPack) {
		fmt.Fprintln(stderr, "invalid --review-pack; expected review-v1 or review-r1")
		return agent.ExitUsage
	}
	// --autoscale (W07) starts with zero workers and follows controller
	// commands; it excludes an explicit --workers (legacy static mode, the
	// default). --max-workers only applies to --autoscale.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if (*autoscale && explicit["workers"]) || (!*autoscale && explicit["max-workers"]) {
		fmt.Fprintln(stderr, "--autoscale [--max-workers N] and --workers N are mutually exclusive")
		return agent.ExitUsage
	}
	if *autoscale {
		*workers = 0
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
	approved := map[string]bool{}
	for _, id := range modelRoots {
		approved[id] = true
	}
	codeApproved := map[string]bool{}
	for _, id := range codeRoots {
		codeApproved[id] = true
	}
	tools := map[string]string{}
	for tool, v := range toolFlags {
		if *v != "" {
			tools[tool] = *v
		}
	}
	if both := tools[python.MypyTool] != "" && tools[python.PyrightTool] != ""; both != *bothTypeCheckers {
		if both {
			fmt.Fprintln(stderr, "--mypy and --pyright are alternative type checkers; add --python-both-type-checkers to run both")
		} else {
			fmt.Fprintln(stderr, "--python-both-type-checkers needs both --mypy and --pyright")
		}
		return agent.ExitUsage
	}
	cfg := agent.Config{
		ControllerURL: *controller, StateDir: abs, Name: *name, EnrollTokenFile: *tokenFile,
		SourceRoots: mapping, Workers: *workers, Autoscale: *autoscale, MaxWorkers: *maxWorkers,
		ToolPath: *toolPath, GitExecutable: *git, GitHosts: gitHosts, GoExecutable: *goExe, GoModCache: *goModCache, AnalyzerTools: tools,
		ModelExecutable: *modelExe, ModelName: *modelName, ModelRelayFile: *modelRelay, ModelSourceRoots: approved, ReviewPack: *reviewPack,
		RepositoryCodeSourceRoots: codeApproved,
		Log:                       stderr,
	}
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

// workerHandlers registers the handlers of this build. Without a usable
// baseline analyzer (go vet needs --go, ruff needs --ruff) the worker
// advertises no capability and is never leased. The deep-review profile is
// discovery (env.discover, docs.discover) → the analyzer composite (go vet,
// staticcheck, ruff, mypy, pyright, bandit in catalog order; unconfigured
// tools plan unsupported tasks) → review roles. Discovery observes the configured go and shared
// toolchains (--node) and every configured analyzer tool. The test-only fixture handler is left
// unwrapped.
func workerHandlers(ctx context.Context, h *workerproto.Hello, runner executor.Runner) ([]analysis.Handler, []string, error) {
	pack := h.ReviewPack
	if pack == "" {
		pack = review.PackV1
	}
	if !review.ValidPack(pack) {
		// No silent fallback to the default pack.
		return nil, nil, errors.New("unknown review pack")
	}
	cfg := builtin.Config{GoExecutable: h.GoExecutable, GoModCache: h.GoModCache, Tools: h.AnalyzerTools, ProbeDir: h.ScratchRoot, ToolPath: h.ToolPath}
	hs, missing := builtin.Handlers(ctx, cfg, runner)
	for i, handler := range hs {
		if handler.Info().Name == analyzerpack.CompositeName {
			caller := opencode.Config{Executable: h.ModelExecutable, Model: h.ModelName, RelayFile: h.ModelRelayFile, Timeout: 75 * time.Second}
			var reviewed analysis.Handler = review.Handler{Static: handler, Caller: caller}
			if pack == review.PackR1 {
				reviewed = review.R1Handler{Static: handler, Caller: caller}
			}
			observer := discovery.ProbeObserver{GoExecutable: h.GoExecutable, Analyzers: builtin.Probes(cfg), Toolchains: builtin.ToolchainProbes(cfg)}
			wrapped, err := discovery.Wrap(reviewed, discovery.Config{Observer: observer})
			if err != nil {
				return nil, nil, err
			}
			hs[i] = wrapped
		}
	}
	var codes []string
	for _, m := range missing {
		codes = append(codes, m.Name+":"+m.Reason)
	}
	return hs, codes, nil
}
