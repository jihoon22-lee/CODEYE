// Package deps adds the dependency preparation tasks (deps.<ecosystem>,
// stage environment, not required for the verdict) in front of the analyzer
// and review tasks: for every module an analyzer pack plans, the module's
// own package manager fetches the dependencies its manifest and lockfile
// name into a node cache, so the offline analyzers that resolve imports
// (go vet, staticcheck, ...) can analyze instead of reporting a gap.
//
// Fetching runs only when the job carries AllowDependencyFetch (the
// project's choice and the node operator's switch). Without it nothing is
// planned and every plan is exactly the inner handler's. The fetch runs in
// the attempt's workspace copy, never in the user's source path, with the
// registry and caches set through argv and environment; the analyzers stay
// offline and only read the prepared cache. Each task writes a
// depdoc record whatever its status; a slow or failing registry ends only
// its own task.
package deps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Defaults of the operator budgets.
const (
	DefaultModuleTimeout    = 10 * time.Minute
	DefaultTotalTimeout     = 20 * time.Minute
	DefaultMaxDownloadBytes = int64(2) << 30
	// DefaultGoProxy is the public Go module proxy.
	DefaultGoProxy = "https://proxy.golang.org"

	stdoutLimit = 16 << 20
	stderrLimit = 1 << 20
)

// pollEvery is how often the download size is measured while a fetch runs.
// The cap is soft: a fast transfer can overshoot it before the process is
// stopped.
var pollEvery = 2 * time.Second

// Config is the node operator's dependency configuration (agent flags,
// passed in the worker hello). An empty CacheRoot plans nothing.
type Config struct {
	// CacheRoot is the absolute shared dependency cache (default
	// <agent-state>/cache/deps); each ecosystem uses its own subdirectory.
	CacheRoot string
	// AgentStateDir locates the rest of the cache storage area for the
	// quota check (worker homes and scratch).
	AgentStateDir string
	// GoExecutable is the operator's go command; GoModCache the operator's
	// own module cache, read only as the first proxy (it is never written).
	GoExecutable string
	GoModCache   string
	// GoProxy is the registry part of GOPROXY (default proxy.golang.org);
	// "direct" and "off" entries are dropped (no VCS access). GoSumDB,
	// GoPrivate, GoNoProxy and GoNoSumDB are the operator's settings, passed
	// as they are ("" = Go's defaults; GOSUMDB defaults to sum.golang.org).
	GoProxy   string
	GoSumDB   string
	GoPrivate string
	GoNoProxy string
	GoNoSumDB string

	// Rust and JS/TS fetch (rust.go, javascript.go).
	// CargoExecutable is the operator's toolchain cargo (--cargo); the fetch
	// runs `cargo fetch` with it. RustConsumer: cargo check is usable on this
	// node, so fetched crates are read.
	CargoExecutable string
	RustConsumer    bool
	// NodeExecutable is the shared node toolchain (--node); NpmScript,
	// PnpmScript and YarnScript the package managers' entry scripts (--npm,
	// --pnpm, --yarn), run as <node> <script>. JSConsumer: tsc or
	// type-aware ESLint is usable on this node. NpmRegistry is the
	// registry URL given to every JS package manager ("" = npm's, or
	// yarn's for yarn).
	NodeExecutable string
	NpmScript      string
	PnpmScript     string
	YarnScript     string
	JSConsumer     bool
	NpmRegistry    string
	// Python fetch (python.go): the operator's interpreter (--python), uv
	// (--uv, used instead of pip when set), the package index
	// (--pip-index-url, "" = PyPI), and whether mypy or pyright is usable on
	// this node (nothing else reads the prepared packages).
	Python          string
	UV              string
	PipIndexURL     string
	PythonConsumers bool

	ModuleTimeout    time.Duration
	TotalTimeout     time.Duration
	MaxDownloadBytes int64
	// CacheQuotaBytes is the cache storage area limit (0 = none): a fetch
	// does not start once the area is full, and stops when it would fill it.
	CacheQuotaBytes int64
}

func (c Config) withDefaults() Config {
	if c.ModuleTimeout <= 0 {
		c.ModuleTimeout = DefaultModuleTimeout
	}
	if c.TotalTimeout <= 0 {
		c.TotalTimeout = DefaultTotalTimeout
	}
	if c.MaxDownloadBytes <= 0 {
		c.MaxDownloadBytes = DefaultMaxDownloadBytes
	}
	return c
}

// Module is one module a fetcher prepares; Suffix is the module suffix of
// the ecosystem's analyzer task keys ("" or ".mNN"), so the analyzers find
// the record of their own module.
type Module struct {
	Suffix, Dir, Title string
}

// Outcome is what a fetcher decided; the handler adds the binding and
// writes the record.
type Outcome struct {
	Status        domain.TaskStatus
	State         string
	Reason        string
	Manager       depdoc.Manager
	Lockfile      *depdoc.Lockfile
	Pinned        bool
	Counts        depdoc.Counts
	CacheKind     string
	CacheKey      string
	DroppedInputs []depdoc.Dropped
	FailedItems   []depdoc.Failed
	Limitations   []string
	// InstalledDirs are the node_modules directories a JS/TS fetch built in
	// the workspace copy (cache kind attempt).
	InstalledDirs []string
}

// Fetcher prepares the dependencies of one ecosystem.
type Fetcher interface {
	// Ecosystem is the record ecosystem and the task key part (deps.go).
	Ecosystem() string
	// Plan lists the modules from the manifest and scope only, through the
	// ecosystem pack's own module planner, so the fetch directory equals
	// the analyzer's.
	Plan(job analysis.Job) ([]Module, error)
	// Fetch prepares one module. Processes start only through env.Exec.
	Fetch(ctx context.Context, env *Env, m Module) Outcome
}

// Fetchers are this build's fetchers in plan order.
func Fetchers() []Fetcher { return []Fetcher{GoFetcher{}, RustFetcher{}, JSFetcher{}, PythonFetcher{}} }

// ManagerFlags are the agent flags (--<name> PATH) of the package managers
// this build's fetchers run, sorted. They travel in the hello's
// dependencies.package_managers; go and cargo are the analyzers' toolchains
// (--go, --cargo).
func ManagerFlags() []string {
	out := append(append([]string{}, JSManagers...), PythonManagers...)
	sort.Strings(out)
	return out
}

// Handler wraps an inner handler with the dependency tasks.
type Handler struct {
	inner    analysis.Handler
	cfg      Config
	fetchers []Fetcher
}

var _ analysis.Handler = (*Handler)(nil)

// Wrap returns inner with dependency tasks; the handler identity is
// inner's.
func Wrap(inner analysis.Handler, cfg Config, fetchers ...Fetcher) (*Handler, error) {
	if inner == nil {
		return nil, errors.New("deps: nil inner handler")
	}
	if cfg.CacheRoot != "" && !filepath.IsAbs(cfg.CacheRoot) {
		return nil, errors.New("deps: cache root must be absolute")
	}
	seen := map[string]bool{}
	for _, f := range fetchers {
		if f == nil || seen[f.Ecosystem()] || !depdoc.ValidTaskKey(depdoc.TaskKey(f.Ecosystem(), "")) {
			return nil, errors.New("deps: invalid fetcher")
		}
		seen[f.Ecosystem()] = true
	}
	return &Handler{inner: inner, cfg: cfg.withDefaults(), fetchers: fetchers}, nil
}

// Info returns the inner handler identity.
func (h *Handler) Info() analysis.HandlerInfo { return h.inner.Info() }

// Enabled reports whether this job plans dependency tasks.
func (h *Handler) enabled(job analysis.Job) bool {
	return job.AllowDependencyFetch && h.cfg.CacheRoot != ""
}

type planned struct {
	spec    analysis.TaskSpec
	fetcher Fetcher
	module  Module
}

func (h *Handler) plan(job analysis.Job) ([]planned, error) {
	var out []planned
	for _, f := range h.fetchers {
		mods, err := f.Plan(job)
		if err != nil {
			return nil, err
		}
		for _, m := range mods {
			key := depdoc.TaskKey(f.Ecosystem(), m.Suffix)
			if !depdoc.ValidTaskKey(key) {
				return nil, fmt.Errorf("deps: invalid task key %q", key)
			}
			out = append(out, planned{spec: analysis.TaskSpec{Key: key, Title: m.Title, Stage: domain.StageEnvironment}, fetcher: f, module: m})
		}
	}
	return out, nil
}

// Plan puts the dependency tasks in front of the inner plan. It reads only
// the manifest and scope.
func (h *Handler) Plan(ctx context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	inner, err := h.inner.Plan(ctx, job)
	if err != nil {
		return nil, err
	}
	for _, t := range inner {
		if depdoc.IsTask(t.Key) {
			return nil, errors.New("deps: inner handler planned a dependency task key")
		}
	}
	if !h.enabled(job) {
		return inner, nil
	}
	ps, err := h.plan(job)
	if err != nil {
		return nil, err
	}
	out := make([]analysis.TaskSpec, 0, len(ps)+len(inner))
	for _, p := range ps {
		out = append(out, p.spec)
	}
	return append(out, inner...), nil
}

// Run executes a dependency task or delegates to the inner handler.
func (h *Handler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	if !depdoc.IsTask(task.Key) {
		return h.inner.Run(ctx, job, task, runner)
	}
	if !h.enabled(job) {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "unknown_task"}
	}
	ps, err := h.plan(job)
	if err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError}
	}
	for _, p := range ps {
		if p.spec.Key == task.Key {
			return h.run(ctx, job, p, runner)
		}
	}
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: "unknown_task"}
}

// Env is what a fetcher may use while it runs one module.
type Env struct {
	Job     analysis.Job
	Config  Config
	TaskDir string // <scratch>/deps/<key>, private to this task
	runner  executor.Runner
	execDir string
	// deadline is the end of this task's share of the attempt budget.
	deadline time.Time
	// budgetBound: the attempt budget, not the module timeout, sets the deadline.
	budgetBound bool
	// downloadLeft is the most the cache may grow during this task.
	downloadLeft int64
	quotaBound   bool
	executions   []analysis.Execution
	stop         string
}

// Stop is the reason the last Exec was stopped by this package (size cap,
// cache quota, budget, timeout, cancellation, cleanup), or "".
func (e *Env) Stop() string { return e.stop }

// CacheDir is the absolute directory of a cache key under the cache root.
func (e *Env) CacheDir(key string) string {
	return filepath.Join(e.Config.CacheRoot, filepath.FromSlash(key))
}

// Exec runs one process with the task's remaining time and, when watch
// names directories, stops it once they and the process temp directory
// grew together past the download allowance ("" entries are ignored). The streams are stored as an
// execution of the task. ok is false when the process did not exit on its
// own; Stop then says why.
func (e *Env) Exec(ctx context.Context, name string, spec executor.Spec, watch ...string) (executor.Result, bool) {
	e.stop = ""
	left := time.Until(e.deadline)
	if left <= 0 {
		e.stop = depdoc.ReasonBudgetExhausted
		return executor.Result{}, false
	}
	spec.Timeout = min(left, e.Config.ModuleTimeout)
	if spec.TempParent == "" {
		// The process temp tree stays in the task directory, away from the
		// protected snapshot whatever the system temp directory holds.
		spec.TempParent = filepath.Join(e.TaskDir, "tmp")
		if err := os.MkdirAll(spec.TempParent, 0o700); err != nil {
			e.stop = string(executor.StatusRejected)
			return executor.Result{}, false
		}
	}
	if spec.StdoutLimit == 0 {
		spec.StdoutLimit = stdoutLimit
	}
	if spec.StderrLimit == 0 {
		spec.StderrLimit = stderrLimit
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var over atomic.Bool
	done, watching := make(chan struct{}), make(chan struct{})
	watched := slices.DeleteFunc(slices.Clone(watch), func(d string) bool { return d == "" })
	if len(watched) > 0 && !slices.Contains(watched, spec.TempParent) {
		// Installers unpack and build in the process temp directory.
		watched = append(watched, spec.TempParent)
	}
	if len(watched) == 0 {
		close(watching)
	} else {
		base, every := dirBytes(watched...), pollEvery
		go func() {
			// Exec returns only after the watcher stopped walking the cache.
			defer close(watching)
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					if dirBytes(watched...)-base > e.downloadLeft {
						over.Store(true)
						cancel()
						return
					}
				}
			}
		}()
	}
	res := e.runner.Run(rctx, spec)
	close(done)
	<-watching
	if x, err := analysis.WriteExecution(e.execDir, name, res); err == nil {
		e.executions = append(e.executions, x)
	}
	switch {
	case res.Cleanup == executor.CleanupUncertain:
		e.stop = depdoc.ReasonCleanupUncertain
	case over.Load():
		e.stop = depdoc.ReasonDownloadLimitExceeded
		if e.quotaBound {
			e.stop = depdoc.ReasonCacheQuotaExceeded
		}
	case ctx.Err() != nil || res.Status == executor.StatusCancelled:
		e.stop = depdoc.ReasonCancelled
	case res.Status == executor.StatusTimedOut:
		e.stop = depdoc.ReasonTimeout
		if e.budgetBound && spec.Timeout < e.Config.ModuleTimeout {
			e.stop = depdoc.ReasonBudgetExhausted
		}
	case res.Status != executor.StatusExited:
		e.stop = string(res.Status)
	case res.Stdout.Truncated || res.Stdout.Incomplete:
		e.stop = depdoc.ReasonOutputTruncated
	}
	return res, e.stop == ""
}

// StopOutcome maps a stop of Exec to the task outcome.
func StopOutcome(stop string) (domain.TaskStatus, string) {
	switch stop {
	case depdoc.ReasonCancelled:
		return domain.TaskCancelled, stop
	case depdoc.ReasonTimeout:
		return domain.TaskTimedOut, stop
	case depdoc.ReasonDownloadLimitExceeded, depdoc.ReasonCacheQuotaExceeded, depdoc.ReasonBudgetExhausted:
		return domain.TaskBudgetExhausted, stop
	case string(executor.StatusUnsupported):
		return domain.TaskUnsupported, "execution_unsupported"
	case string(executor.StatusRejected):
		return domain.TaskFailed, "execution_rejected"
	case string(executor.StatusStartFailed):
		return domain.TaskFailed, "tool_start_failed"
	case depdoc.ReasonCleanupUncertain, depdoc.ReasonOutputTruncated:
		return domain.TaskFailed, stop
	}
	return domain.TaskFailed, depdoc.ReasonFetchFailed
}

func budgetPath(scratch string) string { return filepath.Join(scratch, "deps", "budget.used") }

// budgetUsed is the fetch time this attempt has used, one line of
// nanoseconds per finished dependency task. The worker runs the tasks of an
// attempt one after another, so the file needs no lock.
func budgetUsed(scratch string) (time.Duration, error) {
	f, err := os.Open(budgetPath(scratch))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var total time.Duration
	s := bufio.NewScanner(f)
	for s.Scan() {
		n, err := strconv.ParseInt(strings.TrimSpace(s.Text()), 10, 64)
		if err != nil || n < 0 {
			return 0, errors.New("budget file unreadable")
		}
		total += time.Duration(n)
	}
	return total, s.Err()
}

func addBudget(scratch string, d time.Duration) error {
	f, err := os.OpenFile(budgetPath(scratch), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%d\n", int64(d))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// cacheArea measures the cache storage area the way the agent does.
func (h *Handler) cacheArea() (int64, bool) {
	u, err := quota.Measure(quota.Layout{AgentStateDir: h.cfg.AgentStateDir, GoModCache: h.cfg.GoModCache, DependencyCache: h.cfg.CacheRoot}, quota.Cache)
	if err != nil {
		return 0, false
	}
	return u.Bytes, true
}

func (h *Handler) run(ctx context.Context, job analysis.Job, p planned, runner executor.Runner) analysis.TaskResult {
	mod := depdoc.Module{ID: p.fetcher.Ecosystem() + ":" + p.module.Dir, Ecosystem: p.fetcher.Ecosystem(), Path: p.module.Dir}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError}
	}
	taskDir := filepath.Dir(depdoc.Path(job.Scratch, p.spec.Key))
	if err := os.MkdirAll(filepath.Dir(taskDir), 0o700); err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError}
	}
	if err := os.Mkdir(taskDir, 0o700); err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError}
	}
	env := &Env{Job: job, Config: h.cfg, TaskDir: taskDir, runner: runner, execDir: filepath.Join(taskDir, "exec")}
	var out Outcome
	used, err := budgetUsed(job.Scratch)
	started := time.Now()
	switch {
	case ctx.Err() != nil:
		out = Outcome{Status: domain.TaskCancelled, State: depdoc.StateFailed, Reason: depdoc.ReasonCancelled}
	case err != nil:
		out = Outcome{Status: domain.TaskFailed, State: depdoc.StateFailed, Reason: depdoc.ReasonRecordFailed}
	case used >= h.cfg.TotalTimeout:
		out = Outcome{Status: domain.TaskBudgetExhausted, State: depdoc.StateSkipped, Reason: depdoc.ReasonBudgetExhausted}
	default:
		left := h.cfg.TotalTimeout - used
		env.deadline, env.budgetBound = started.Add(left), left < h.cfg.ModuleTimeout
		env.downloadLeft = h.cfg.MaxDownloadBytes
		if h.cfg.CacheQuotaBytes > 0 {
			usage, ok := h.cacheArea()
			room := h.cfg.CacheQuotaBytes - usage
			if ok && room <= 0 {
				out = Outcome{Status: domain.TaskBudgetExhausted, State: depdoc.StateSkipped, Reason: depdoc.ReasonCacheQuotaExceeded}
				break
			}
			if ok && room < env.downloadLeft {
				env.downloadLeft, env.quotaBound = room, true
			}
		}
		out = p.fetcher.Fetch(ctx, env, p.module)
		if ctx.Err() != nil && out.Status != domain.TaskSucceeded {
			out.Status, out.Reason = domain.TaskCancelled, depdoc.ReasonCancelled
			if out.State == depdoc.StatePrepared {
				out.State = depdoc.StatePartial
			}
		}
		if err := addBudget(job.Scratch, time.Since(started)); err != nil && out.Status == domain.TaskSucceeded {
			out = Outcome{Status: domain.TaskFailed, State: depdoc.StateFailed, Reason: depdoc.ReasonRecordFailed, Manager: out.Manager}
		}
	}
	return h.finish(job, p.spec.Key, mod, env, out)
}

// finish writes the record and builds the task result.
func (h *Handler) finish(job analysis.Job, key string, mod depdoc.Module, env *Env, out Outcome) analysis.TaskResult {
	if out.Manager.Name == "" {
		out.Manager.Name = mod.Ecosystem
	}
	if out.State == "" {
		out.State = depdoc.StateFailed
	}
	if out.CacheKind == "" {
		out.CacheKind = depdoc.CacheNone
	}
	rec := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		TaskKey: key, Module: mod, Manager: out.Manager, Lockfile: out.Lockfile, Pinned: out.Pinned, State: out.State, TaskStatus: string(out.Status),
		Counts: out.Counts, Cache: depdoc.Cache{Kind: out.CacheKind}, DroppedInputs: nonNil(out.DroppedInputs), FailedItems: nonNil(out.FailedItems),
		Limitations: sortedUnique(out.Limitations)}
	if dirs := sortedUnique(out.InstalledDirs); len(dirs) > 0 {
		rec.InstalledDirs = dirs
	}
	if out.Reason != "" {
		rec.Reason = &out.Reason
	}
	if out.CacheKind == depdoc.CacheShared {
		k := out.CacheKey
		rec.Cache.Key = &k
	}
	if len(rec.FailedItems) > depdoc.MaxFailedItems {
		rec.FailedItems = rec.FailedItems[:depdoc.MaxFailedItems]
	}
	res := analysis.TaskResult{Status: out.Status, Reason: out.Reason, Executions: env.executions, Limitations: rec.Limitations}
	b, err := depdoc.Marshal(rec, expect(job, key))
	if err == nil {
		p := depdoc.Path(job.Scratch, key)
		if err = analysis.WriteFileExclusive(p, b); err == nil {
			res.Artifacts = []analysis.ArtifactFile{{Path: p, Role: "other", MediaType: depdoc.MediaType}}
			return res
		}
	}
	res.Status, res.Reason = domain.TaskFailed, depdoc.ReasonRecordFailed
	return res
}

// expect binds a record to the job.
func expect(job analysis.Job, key string) depdoc.Expect {
	return depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: key, Lookup: Lookup(job)}
}

// Lookup returns the manifest sha256 of an included snapshot file.
func Lookup(job analysis.Job) func(string) (string, bool) {
	index := map[string]string{}
	for _, e := range job.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			index[e.Path] = e.SHA256
		}
	}
	return func(p string) (string, bool) { s, ok := index[p]; return s, ok }
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// dirBytes sums regular file sizes under the directories (links are not
// followed); a missing directory is empty.
func dirBytes(dirs ...string) int64 {
	var n int64
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, e := d.Info(); e == nil {
					n += info.Size()
				}
			}
			return nil
		})
	}
	return n
}
