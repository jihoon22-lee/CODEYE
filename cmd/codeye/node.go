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
	"slices"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	analyzerpack "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	workerrun "github.com/jihoon22-lee/CODEYE/internal/worker/run"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// runNode handles `codeye agent` and `codeye worker`. main.go calls it
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
	var roots, gitHosts, modelRoots, codeRoots, storageQuota, protectedDirs multiFlag
	modelExe := fs.String("opencode", "", "")
	modelName := fs.String("model", "", "")
	modelRelay := fs.String("model-relay-file", "", "")
	// The provider API key is read by workers from this file at each model
	// call; it is never an argument (docs/REVIEW.md#모델-설정).
	modelCredential := fs.String("model-credential-file", "", "")
	modelConfig := fs.String("model-config-file", "", "")
	modelInstance := fs.String("model-instance-config", "", "")
	modelTimeout := fs.Duration("model-timeout", opencode.DefaultTimeout, "")
	reviewMaxCalls := fs.Int("review-max-calls", review.DefaultMaxCalls, "")
	reviewRequestBytes := fs.Int("review-request-bytes", review.DefaultRequestBytes, "")
	reviewOutputTokens := fs.Int("review-output-tokens", review.DefaultOutputTokens, "")
	fs.Var(&modelRoots, "model-source-root", "")
	// The repository-code grant (docs/ANALYZERS.md#저장소-코드-실행-허가) is a node operator setting
	// per local source root, like the model-source grant; nothing in a run
	// or its snapshot can raise it.
	fs.Var(&codeRoots, "repo-code-source-root", "")
	// The service: each project's own analysis choices (send source to
	// the model, run build scripts) decide instead of the two per-root grants.
	projectPolicy := fs.Bool("project-analysis-policy", false, "")
	// A local project folder holding one of these directories is refused.
	fs.Var(&protectedDirs, "protected-dir", "")
	controller := fs.String("controller", "", "")
	stateDir := fs.String("state-dir", "", "")
	name := fs.String("name", "", "")
	tokenFile := fs.String("enroll-token-file", "", "")
	workers := fs.Int("workers", 1, "")
	autoscale := fs.Bool("autoscale", false, "")
	maxWorkers := fs.Int("max-workers", 2, "")
	toolPath := fs.String("toolchain-path", "/usr/bin:/bin", "")
	javaClasspath := fs.String("java-classpath-catalog", "", "")
	git := fs.String("git", "", "")
	// A Git host token file (the service's GitHub token): workers read it
	// for a fetch of its host; the token is never an argument.
	gitCredential := fs.String("git-credential-file", "", "")
	controllerSources := fs.Bool("controller-sources", false, "")
	goExe := fs.String("go", "", "")
	goModCache := fs.String("go-mod-cache", "", "")
	// Dependency fetching (docs/ARCHITECTURE.md#의존성-준비): the operator's
	// switch, the shared cache, the Go registry and the budgets.
	depFetch := fs.Bool("dependency-fetch", false, "")
	depCache := fs.String("dependency-cache-dir", "", "")
	goProxy := fs.String("go-proxy", "", "")
	goSumDB := fs.String("go-sumdb", "", "")
	goPrivate := fs.String("go-private", "", "")
	goNoProxy := fs.String("go-noproxy", "", "")
	goNoSumDB := fs.String("go-nosumdb", "", "")
	depTimeout := fs.Duration("dependency-timeout", 0, "")
	depTotal := fs.Duration("dependency-total-timeout", 0, "")
	depMax := fs.Int64("dependency-max-bytes", 0, "")
	npmRegistry := fs.String("npm-registry", "", "")
	// Package managers of the dependency fetch (--npm, --pnpm, --yarn,
	// --python, --uv; deps.ManagerFlags) and the Python package index.
	managerFlags := map[string]*string{}
	for _, name := range deps.ManagerFlags() {
		managerFlags[name] = fs.String(name, "", "")
	}
	pipIndex := fs.String("pip-index-url", "", "")
	var testSettings workerproto.TestSettings
	fs.StringVar(&testSettings.GoRun, "test-go-run", "", "")
	fs.BoolVar(&testSettings.GoRace, "test-go-race", false, "")
	fs.StringVar(&testSettings.MavenRepository, "test-maven-repository", "", "")
	fs.StringVar(&testSettings.GradleHome, "test-gradle-home", "", "")
	// Offline mode and the internal CA (docs/OPERATIONS.md#오프라인-모드).
	offline := fs.Bool("offline", false, "")
	caBundle := fs.String("ca-bundle", "", "")
	// One flag per analyzer tool of this build (--staticcheck PATH) and per
	// shared toolchain (--node PATH, docs/ANALYZERS.md#도구-경로). Both travel in the hello's
	// analyzer_tools map; toolchain names are reserved there.
	toolFlags := map[string]*string{}
	for _, tool := range append(builtin.ToolFlags(), builtin.ToolchainFlags()...) {
		toolFlags[tool] = fs.String(tool, "", "")
	}
	// mypy and pyright are alternative Python type checkers (docs/ANALYZERS.md#타입-검사기-선택):
	// configuring both runs both only when the operator says so.
	bothTypeCheckers := fs.Bool("python-both-type-checkers", false, "")
	// The service: take analysis work even without a usable baseline
	// analyzer; the tasks of unusable tools are recorded unsupported.
	advertiseAlways := fs.Bool("advertise-without-baseline", false, "")
	fs.Var(&roots, "source-root", "")
	fs.Var(&gitHosts, "git-host", "")
	// Storage quota (docs/OPERATIONS.md#저장-영역과-상한): AREA=BYTES for snapshot, workspace,
	// cache and logs; areas at their limit refuse new claims for this node.
	fs.Var(&storageQuota, "storage-quota", "")
	storageLogDir := fs.String("storage-log-dir", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *controller == "" || *stateDir == "" {
		fmt.Fprintln(stderr, "invalid agent options; use codeye --help")
		return agent.ExitUsage
	}
	if !review.ValidMaxCalls(*reviewMaxCalls) {
		fmt.Fprintln(stderr, "invalid --review-max-calls; expected 15..2013 (default 99)")
		return agent.ExitUsage
	}
	if !review.ValidRequestBytes(*reviewRequestBytes) {
		fmt.Fprintf(stderr, "invalid --review-request-bytes; expected %d..%d (default %d)\n", review.MinRequestBytes, review.MaxRequestBytes, review.DefaultRequestBytes)
		return agent.ExitUsage
	}
	if !review.ValidOutputTokens(*reviewOutputTokens) {
		fmt.Fprintf(stderr, "invalid --review-output-tokens; expected %d..%d (default %d)\n", review.MinOutputTokens, review.MaxOutputTokens, review.DefaultOutputTokens)
		return agent.ExitUsage
	}
	if len(testSettings.GoRun) > 512 || strings.ContainsAny(testSettings.GoRun, "\x00\r\n") {
		fmt.Fprintln(stderr, "invalid --test-go-run; expected one line of at most 512 bytes")
		return agent.ExitUsage
	}
	for _, path := range []string{testSettings.MavenRepository, testSettings.GradleHome} {
		if path != "" && (!filepath.IsAbs(path) || filepath.Clean(path) != path) {
			fmt.Fprintln(stderr, "invalid test cache directory; expected an absolute, clean path")
			return agent.ExitUsage
		}
	}
	// --autoscale starts with zero workers and follows controller
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
	limits := map[string]int64{}
	for _, q := range storageQuota {
		area, n, err := quota.ParseLimit(q)
		if err != nil || limits[area] != 0 {
			fmt.Fprintln(stderr, "invalid --storage-quota; expected AREA=BYTES once per area (snapshot, workspace, cache, logs)")
			return agent.ExitUsage
		}
		limits[area] = n
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
		ToolPath: *toolPath, JavaClasspathCatalog: *javaClasspath, GitExecutable: *git, GitHosts: gitHosts, GitCredentialFile: *gitCredential, GoExecutable: *goExe, GoModCache: *goModCache, AnalyzerTools: tools,
		ModelExecutable: *modelExe, ModelName: *modelName, ModelRelayFile: *modelRelay, ModelCredentialFile: *modelCredential, ModelTimeout: *modelTimeout, ModelSourceRoots: approved, ReviewMaxCalls: *reviewMaxCalls,
		ReviewRequestBytes: *reviewRequestBytes, ReviewOutputTokens: *reviewOutputTokens,
		RepositoryCodeSourceRoots: codeApproved,
		StorageQuota:              limits,
		StorageLogDir:             *storageLogDir,
		Log:                       stderr,
	}
	cfg.ModelConfigFile = *modelConfig
	if testSettings != (workerproto.TestSettings{}) {
		cfg.Tests = &testSettings
	}
	if *modelInstance != "" {
		cfg.ModelSettings = localModelSettings(*modelInstance, filepath.Dir(abs), instanceModelEnv())
	}
	cfg.AdvertiseWithoutBaseline = *advertiseAlways
	cfg.DependencyFetch, cfg.DependencyCacheDir, cfg.GoProxy = *depFetch, *depCache, *goProxy
	cfg.GoSumDB, cfg.GoPrivate, cfg.GoNoProxy, cfg.GoNoSumDB = *goSumDB, *goPrivate, *goNoProxy, *goNoSumDB
	cfg.DependencyTimeout, cfg.DependencyTotalTimeout, cfg.DependencyMaxBytes = *depTimeout, *depTotal, *depMax
	cfg.NpmRegistry = *npmRegistry
	cfg.PackageManagers, cfg.PipIndexURL = map[string]string{}, *pipIndex
	cfg.Offline, cfg.CABundle = *offline, *caBundle
	for name, v := range managerFlags {
		if *v != "" {
			cfg.PackageManagers[name] = *v
		}
	}
	cfg.ProjectAnalysisPolicy = *projectPolicy
	cfg.ControllerSources = *controllerSources
	for _, d := range protectedDirs {
		if !filepath.IsAbs(d) {
			fmt.Fprintln(stderr, "invalid --protected-dir; expected an absolute path")
			return 2
		}
		cfg.ProtectedDirs = append(cfg.ProtectedDirs, filepath.Clean(d))
	}
	// SIGUSR1 (codeye stop) drains: no new claims, running work
	// finishes, then a clean exit.
	drain, stopDrain := drainSignal()
	defer stopDrain()
	cfg.Drain = drain
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

// pythonConsumers reports whether mypy or pyright is configured and usable
// (not among the unavailable analyzers).
func pythonConsumers(cfg builtin.Config, missing []builtin.Unavailable) bool {
	for _, name := range []string{python.MypyName, python.PyrightName} {
		tool := python.MypyTool
		if name == python.PyrightName {
			tool = python.PyrightTool
		}
		if cfg.Tools[tool] == "" {
			continue
		}
		if !slices.ContainsFunc(missing, func(u builtin.Unavailable) bool { return u.Name == name }) {
			return true
		}
	}
	return false
}

func runWorker(ctx context.Context, in io.Reader, out, stderr io.Writer) int {
	model := &instanceModelCaller{}
	tools := &workerRuntimePaths{caller: model}
	return workerrun.Main(ctx, workerrun.Config{In: in, Out: out, Stderr: stderr,
		PrepareAttempt:  model.prepareAttempt,
		RefreshHandlers: tools.refresh,
		Handlers: func(ctx context.Context, h *workerproto.Hello, runner executor.Runner) ([]analysis.Handler, []string, error) {
			tools.hello, tools.current = h, helloPaths(h)
			model.settings, model.static, model.offline, model.caBundle = h.ModelSettings, workerModel(h), h.Offline, h.CABundle
			return workerHandlersWithCaller(ctx, h, runner, model)
		}})
}

func workerModel(h *workerproto.Hello) opencode.Config {
	timeout := opencode.DefaultTimeout
	if h.ModelTimeoutSeconds > 0 {
		timeout = time.Duration(h.ModelTimeoutSeconds) * time.Second
	}
	return opencode.Config{Executable: h.ModelExecutable, Model: h.ModelName, RelayFile: h.ModelRelayFile, CredentialFile: h.ModelCredentialFile,
		ConfigFile: h.ModelConfigFile, Timeout: timeout, Offline: h.Offline, CABundle: h.CABundle,
		OutputTokens: h.ReviewOutputTokens, RequestBytes: h.ReviewRequestBytes}
}

// workerHandlers registers the handlers of this build. Without a usable
// baseline analyzer (go vet needs --go, ruff needs --ruff) the worker
// advertises no capability and is never leased, unless the operator asked for
// it with --advertise-without-baseline (then the tasks of unusable tools end
// unsupported). The deep-review profile is
// discovery (env.discover, docs.discover) → dependency tasks (deps.*, when
// the operator and the project allow fetching) → the analyzer composite (go vet,
// staticcheck, ruff, mypy, pyright, bandit in catalog order; unconfigured
// tools plan unsupported tasks) → review roles. Discovery observes the configured go and shared
// toolchains (--node) and every configured analyzer tool. The test-only fixture handler is left
// unwrapped.
func workerHandlers(ctx context.Context, h *workerproto.Hello, runner executor.Runner) ([]analysis.Handler, []string, error) {
	return workerHandlersWithCaller(ctx, h, runner, workerModel(h))
}

func workerHandlersWithCaller(ctx context.Context, h *workerproto.Hello, runner executor.Runner, caller review.Caller) ([]analysis.Handler, []string, error) {
	if h.ReviewMaxCalls != 0 && !review.ValidMaxCalls(h.ReviewMaxCalls) {
		return nil, nil, errors.New("review call ceiling out of range")
	}
	if !review.ValidRequestBytes(h.ReviewRequestBytes) || !review.ValidOutputTokens(h.ReviewOutputTokens) {
		return nil, nil, errors.New("review request budget out of range")
	}
	cfg := builtin.Config{GoExecutable: h.GoExecutable, GoModCache: h.GoModCache, Tools: h.AnalyzerTools, ProbeDir: h.ScratchRoot, ToolPath: h.ToolPath, JavaClasspathCatalog: h.JavaClasspathCatalog}
	var depCfg deps.Config
	if d := h.Dependencies; d != nil {
		if !filepath.IsAbs(d.CacheRoot) {
			return nil, nil, errors.New("dependency cache root must be absolute")
		}
		cfg.DependencyCacheRoot = d.CacheRoot
		depCfg = deps.Config{CacheRoot: d.CacheRoot, AgentStateDir: d.AgentStateDir, GoExecutable: h.GoExecutable, GoModCache: h.GoModCache, GoProxy: d.GoProxy,
			GoSumDB: d.GoSumDB, GoPrivate: d.GoPrivate, GoNoProxy: d.GoNoProxy, GoNoSumDB: d.GoNoSumDB,
			ModuleTimeout: time.Duration(d.ModuleTimeoutS) * time.Second, TotalTimeout: time.Duration(d.TotalTimeoutS) * time.Second,
			MaxDownloadBytes: d.MaxDownloadBytes, CacheQuotaBytes: d.CacheQuotaBytes, Offline: h.Offline, CABundle: h.CABundle}
		// cargo and node are toolchains of the analyzers; the package
		// managers travel in dependencies.package_managers.
		pm := d.PackageManagers
		depCfg.CargoExecutable, depCfg.NodeExecutable, depCfg.NpmRegistry = h.AnalyzerTools["cargo"], h.AnalyzerTools["node"], d.NpmRegistry
		depCfg.NpmScript, depCfg.PnpmScript, depCfg.YarnScript = pm["npm"], pm["pnpm"], pm["yarn"]
		// Python: the interpreter and uv, and the index; mypy asks the same
		// interpreter for a prepared tree's search paths.
		depCfg.Python, depCfg.UV, depCfg.PipIndexURL = pm["python"], pm["uv"], d.PipIndexURL
		cfg.DependencyPython = depCfg.Python
	}
	cfg.AdvertiseWithoutBaseline = h.AdvertiseWithoutBaseline
	hs, missing := builtin.Handlers(ctx, cfg, runner)
	// Fetched dependencies are only worth fetching for an analyzer that
	// reads them: cargo check for crates, tsc or type-aware ESLint for
	// node_modules, mypy or pyright for Python packages.
	unusable := map[string]bool{}
	for _, m := range missing {
		unusable[m.Name] = true
	}
	depCfg.RustConsumer = !unusable["cargo-check"]
	depCfg.JSConsumer = !unusable["tsc"] || !unusable["eslint-typed"]
	depCfg.PythonConsumers = pythonConsumers(cfg, missing)
	var testHandler analysis.Handler
	for i, handler := range hs {
		if handler.Info().Name == analyzerpack.CompositeName {
			reviewed := review.Handler{Static: handler, Caller: caller, MaxCalls: h.ReviewMaxCalls,
				Budget: review.Budget{RequestBytes: h.ReviewRequestBytes, OutputTokens: h.ReviewOutputTokens}}
			// C/C++ compile DB generation runs after the dependency tasks and
			// before the analyzers that read it (docs/ANALYZERS.md#compile-db-생성).
			generated, err := cpp.WrapGeneration(reviewed, cpp.GenerationHandlerConfig{Generate: cpp.GenerateConfig{ToolPath: h.ToolPath},
				Consumer: !unusable[cpp.CppcheckName] || !unusable[cpp.ClangTidyName] || !unusable[cpp.GCCAnalyzerName]})
			if err != nil {
				return nil, nil, err
			}
			// Dependency tasks run after discovery and before the analyzers;
			// review plans only through the static handler and never sees them.
			fetched, err := deps.Wrap(generated, depCfg, deps.Fetchers()...)
			if err != nil {
				return nil, nil, err
			}
			observer := discovery.ProbeObserver{GoExecutable: h.GoExecutable, Analyzers: builtin.Probes(cfg), Toolchains: builtin.ToolchainProbes(cfg)}
			wrapped, err := discovery.Wrap(fetched, discovery.Config{Observer: observer})
			if err != nil {
				return nil, nil, err
			}
			hs[i] = wrapped
			testCfg := depCfg
			// The test suites can consume dependencies on nodes without a
			// separately configured type checker.
			testCfg.JSConsumer = true
			testCfg.PythonConsumers = true
			testCfg.RustConsumer = true
			testDeps, err := deps.Wrap(generated, testCfg, deps.Fetchers()...)
			if err != nil {
				return nil, nil, err
			}
			testBase, err := discovery.Wrap(testDeps, discovery.Config{Observer: observer})
			if err != nil {
				return nil, nil, err
			}
			testNpm, testPython := h.TestPackageManagers["npm"], h.TestPackageManagers["python"]
			if testNpm == "" {
				testNpm = depCfg.NpmScript
			}
			if testPython == "" {
				testPython = depCfg.Python
			}
			testTools := testexec.Config{Go: h.GoExecutable, GoModCache: h.GoModCache, Node: h.AnalyzerTools["node"], Npm: testNpm,
				Python: testPython, Cargo: h.AnalyzerTools["cargo"], Java: h.AnalyzerTools["java"], ToolPath: h.ToolPath, DependencyCacheRoot: cfg.DependencyCacheRoot}
			if t := h.Tests; t != nil {
				testTools.GoTestRun, testTools.GoRace = t.GoRun, t.GoRace
				testTools.MavenRepository, testTools.GradleHome = t.MavenRepository, t.GradleHome
			}
			testHandler = testexec.New(testBase, testTools)
		}
	}
	if testHandler != nil {
		hs = append(hs, testHandler)
	}
	var codes []string
	for _, m := range missing {
		codes = append(codes, m.Name+":"+m.Reason)
	}
	return hs, codes, nil
}
