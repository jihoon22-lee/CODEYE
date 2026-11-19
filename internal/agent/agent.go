// Package agent is the node agent: it enrolls with the controller, keeps
// node heartbeats, supervises worker processes, claims work, renews leases
// with a monotonic watchdog, forwards worker events/artifacts, journals every
// process it may own, and reconciles that journal after a restart. It never
// touches the database and is the only holder of the node secret.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// Exit codes of `codeye agent`.
const (
	ExitOK        = 0
	ExitRuntime   = 1
	ExitUsage     = 2
	ExitEnroll    = 3
	ExitRevoked   = 4
	maxWorkers    = 16
	defaultMargin = 10 * time.Second
)

// ExitError carries a process exit code.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Config of one agent.
type Config struct {
	ModelExecutable, ModelName, ModelRelayFile string
	// ModelCredentialFile is where workers read the provider API key at each
	// model call (--model-credential-file).
	ModelCredentialFile string
	// ModelTimeout bounds one model call (--model-timeout); 0 means 180 s.
	ModelTimeout time.Duration
	// ReviewPack selects the review instruction pack of this node's workers:
	// "review-r1" (default when empty) or the legacy "review-v1", kept so
	// earlier runs can be reproduced.
	ReviewPack string
	// ReviewMaxCalls is the model call ceiling per review attempt
	// (--review-max-calls); 0 means record.DefaultMaxCalls.
	ReviewMaxCalls   int
	ModelSourceRoots map[string]bool
	ControllerURL    string
	StateDir         string
	Name             string
	EnrollTokenFile  string
	// SourceRoots is the private mapping source_root_id → absolute path.
	SourceRoots map[string]string
	// RepositoryCodeSourceRoots are the local source roots whose jobs may run
	// repository-controlled code (--repo-code-source-root, docs/ANALYZERS.md#저장소-코드-실행-허가).
	// Git sources never get the grant.
	RepositoryCodeSourceRoots map[string]bool
	// ProjectAnalysisPolicy (--project-analysis-policy, personal mode) makes
	// every job follow its project's analysis choices (nodeproto.Job.Analysis)
	// for local and Git sources alike, instead of ModelSourceRoots and
	// RepositoryCodeSourceRoots.
	ProjectAnalysisPolicy bool
	// ProtectedDirs (--protected-dir): a local project folder that contains
	// or lies inside one of these is refused before acquisition, however
	// the project was registered (personal mode: the state and backup
	// directories).
	ProtectedDirs []string
	// Workers is the static worker count (default mode).
	Workers int
	// Autoscale starts with zero workers and starts/drains/stops
	// workers only on controller commands; MaxWorkers is then the local
	// hard cap reported to the controller (1..16).
	Autoscale     bool
	MaxWorkers    int
	ToolPath      string
	GitExecutable string
	GitHosts      []string
	GoExecutable  string
	GoModCache    string
	// DependencyFetch (--dependency-fetch) is the operator's switch for the
	// dependency tasks; a job fetches only when its project's
	// fetch_dependencies choice is on too. The other Dependency fields are its
	// options; zero values are the defaults.
	DependencyFetch        bool
	DependencyCacheDir     string
	GoProxy                string
	GoSumDB                string
	GoPrivate              string
	GoNoProxy              string
	GoNoSumDB              string
	DependencyTimeout      time.Duration
	DependencyTotalTimeout time.Duration
	DependencyMaxBytes     int64
	// AnalyzerTools are absolute analyzer tool paths by tool name, one
	// agent flag per tool (--staticcheck PATH, docs/ANALYZERS.md), and the shared
	// toolchains under their reserved names (--node PATH, docs/ANALYZERS.md#도구-경로). The
	// worker build decides which names it knows.
	AnalyzerTools map[string]string
	// AdvertiseWithoutBaseline (--advertise-without-baseline) lets the node
	// take analysis work even when no baseline analyzer is usable: the tasks of
	// unusable tools are recorded unsupported instead of the run waiting for a
	// node that would never appear.
	AdvertiseWithoutBaseline bool
	Limits                   workerproto.Limits
	// WorkerCommand is the worker argv (default: this executable + "worker").
	WorkerCommand []string
	Log           io.Writer
	// Transcript, if set, records all worker IPC (tests).
	Transcript io.Writer

	// Drain (personal stop) asks the agent to finish: no new claims and
	// no new workers; running attempts finish normally. Once nothing runs,
	// the agent shuts down like on a signal and exits 0 when every cleanup is
	// confirmed. A nil channel never drains.
	Drain <-chan struct{}
	// StorageQuota (docs/OPERATIONS.md#저장-영역과-상한) are node storage area limits in bytes
	// (quota.NodeAreas). Areas at or over their limit are reported in the
	// heartbeat (storage_quota_exceeded) and the controller refuses new
	// claims for this node; running attempts are never interrupted.
	StorageQuota map[string]int64
	// StorageLogDir locates the "logs" area (personal mode log directory).
	StorageLogDir string

	// Timing overrides (tests); zero uses the controller's values.
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	LeaseMargin       time.Duration
	ShutdownTimeout   time.Duration
}

// Agent is one running node agent.
type Agent struct {
	cfg     Config
	client  *Client
	journal *Journal
	cache   *snapshotCache
	spawner *spawner
	ident   *Identity
	scaler  Scaler
	boot    string

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	workers   map[string]*workerProc
	shutting  bool
	draining  bool
	claiming  int
	starting  int
	kickCh    chan struct{}
	hbNow     chan struct{}
	logMu     sync.Mutex
	failures  int
	lastStart time.Time

	// Capability probe (autoscale): analysis handlers advertised by the most
	// recent worker handshake (probe worker or a started worker).
	probeHandlers []workerproto.HandlerInfo
	probed        bool
	probing       bool
	lastProbe     time.Time

	// Storage areas over their limit, re-measured at most every
	// storageMeasureEvery (heartbeat path).
	quotaExceeded []string
	quotaChecked  time.Time
}

// storageMeasureEvery bounds how often the node storage areas are walked.
const storageMeasureEvery = 30 * time.Second

// probeRetry is the delay between failed capability probes (autoscale).
const probeRetry = 30 * time.Second

// New validates cfg.
func New(cfg Config) (*Agent, error) {
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, &ExitError{ExitUsage, "--state-dir must be an absolute path"}
	}
	if cfg.Workers < 0 || cfg.Workers > maxWorkers {
		return nil, &ExitError{ExitUsage, "--workers must be 0..16"}
	}
	if cfg.Autoscale && (cfg.MaxWorkers < 1 || cfg.MaxWorkers > maxWorkers) {
		return nil, &ExitError{ExitUsage, "--max-workers must be 1..16"}
	}
	for id, p := range cfg.SourceRoots {
		if !domain.ValidID(id) || !filepath.IsAbs(p) {
			return nil, &ExitError{ExitUsage, "--source-root must be UUID=/absolute/path"}
		}
	}
	for id := range cfg.ModelSourceRoots {
		if cfg.SourceRoots[id] == "" {
			return nil, &ExitError{ExitUsage, "model source root must have a local source mapping"}
		}
	}
	for id := range cfg.RepositoryCodeSourceRoots {
		if cfg.SourceRoots[id] == "" {
			return nil, &ExitError{ExitUsage, "repository-code source root must have a local source mapping"}
		}
	}
	for _, h := range cfg.GitHosts {
		if domain.ValidateHost(strings.ToLower(h)) != nil {
			return nil, &ExitError{ExitUsage, "invalid --git-host"}
		}
	}
	switch cfg.ReviewPack {
	case "":
		cfg.ReviewPack = "review-r1"
	case "review-r1", "review-v1":
	default:
		return nil, &ExitError{ExitUsage, "--review-pack must be review-r1 (default) or review-v1 (legacy)"}
	}
	if cfg.ReviewMaxCalls == 0 {
		cfg.ReviewMaxCalls = record.DefaultMaxCalls
	} else if !record.ValidMaxCalls(cfg.ReviewMaxCalls) {
		return nil, &ExitError{ExitUsage, "--review-max-calls must be between 15 and 207"}
	}
	if cfg.ModelRelayFile != "" && cfg.ModelCredentialFile != "" {
		return nil, &ExitError{ExitUsage, "--model-relay-file and --model-credential-file are alternatives"}
	}
	if cfg.ModelTimeout < 0 || cfg.ModelTimeout > 30*time.Minute || (cfg.ModelTimeout != 0 && cfg.ModelTimeout < time.Second) {
		return nil, &ExitError{ExitUsage, "--model-timeout must be between 1s and 30m"}
	}
	if cfg.DependencyTimeout < 0 || cfg.DependencyTotalTimeout < 0 || cfg.DependencyMaxBytes < 0 {
		return nil, &ExitError{ExitUsage, "dependency limits must not be negative"}
	}
	for _, p := range []string{cfg.GitExecutable, cfg.GoExecutable, cfg.GoModCache, cfg.DependencyCacheDir, cfg.ModelExecutable, cfg.ModelRelayFile, cfg.ModelCredentialFile} {
		if p != "" && !filepath.IsAbs(p) {
			return nil, &ExitError{ExitUsage, "tool paths must be absolute"}
		}
	}
	for _, p := range cfg.AnalyzerTools {
		if p != "" && !filepath.IsAbs(p) {
			return nil, &ExitError{ExitUsage, "tool paths must be absolute"}
		}
	}
	for area, limit := range cfg.StorageQuota {
		if !quota.ValidNodeArea(area) || limit <= 0 {
			return nil, &ExitError{ExitUsage, "--storage-quota must be AREA=BYTES (snapshot, workspace, cache, logs)"}
		}
	}
	if cfg.StorageQuota[quota.Logs] > 0 && (cfg.StorageLogDir == "" || !filepath.IsAbs(cfg.StorageLogDir)) {
		return nil, &ExitError{ExitUsage, "a logs quota needs an absolute --storage-log-dir"}
	}
	if cfg.ToolPath == "" {
		cfg.ToolPath = "/usr/bin:/bin"
	}
	if len(cfg.WorkerCommand) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cfg.WorkerCommand = []string{exe, "worker"}
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
	}
	client, err := NewClient(cfg.ControllerURL)
	if err != nil {
		return nil, &ExitError{ExitUsage, err.Error()}
	}
	var scaler Scaler = &StaticScaler{N: cfg.Workers}
	if cfg.Autoscale {
		scaler = &ControllerScaler{Cap: cfg.MaxWorkers}
	}
	return &Agent{cfg: cfg, client: client, workers: map[string]*workerProc{}, kickCh: make(chan struct{}, 1), hbNow: make(chan struct{}, 1), boot: domain.NewID()[:8], scaler: scaler}, nil
}

func (ag *Agent) dataRoot() string { return filepath.Join(ag.cfg.StateDir, "data") }

// dependencyCache is the shared dependency cache: --dependency-cache-dir or
// <state>/cache/deps.
func (ag *Agent) dependencyCache() string {
	if ag.cfg.DependencyCacheDir != "" {
		return ag.cfg.DependencyCacheDir
	}
	return filepath.Join(ag.cfg.StateDir, "cache", "deps")
}

// dependencies is the hello's dependency configuration; nil while the
// operator's switch is off.
func (ag *Agent) dependencies() *workerproto.Dependencies {
	if !ag.cfg.DependencyFetch {
		return nil
	}
	return &workerproto.Dependencies{CacheRoot: ag.dependencyCache(), AgentStateDir: ag.cfg.StateDir, GoProxy: ag.cfg.GoProxy,
		GoSumDB: ag.cfg.GoSumDB, GoPrivate: ag.cfg.GoPrivate, GoNoProxy: ag.cfg.GoNoProxy, GoNoSumDB: ag.cfg.GoNoSumDB,
		ModuleTimeoutS: int(ag.cfg.DependencyTimeout / time.Second), TotalTimeoutS: int(ag.cfg.DependencyTotalTimeout / time.Second),
		MaxDownloadBytes: ag.cfg.DependencyMaxBytes, CacheQuotaBytes: ag.cfg.StorageQuota[quota.Cache]}
}

func (ag *Agent) heartbeatInterval() time.Duration {
	if ag.cfg.HeartbeatInterval > 0 {
		return ag.cfg.HeartbeatInterval
	}
	if ag.ident != nil && ag.ident.HeartbeatSeconds > 0 {
		return time.Duration(ag.ident.HeartbeatSeconds) * time.Second
	}
	return nodeproto.HeartbeatSeconds * time.Second
}

func (ag *Agent) leaseDuration() time.Duration {
	if ag.cfg.LeaseDuration > 0 {
		return ag.cfg.LeaseDuration
	}
	if ag.ident != nil && ag.ident.LeaseSeconds > 0 {
		return time.Duration(ag.ident.LeaseSeconds) * time.Second
	}
	return nodeproto.LeaseSeconds * time.Second
}

func (ag *Agent) leaseMargin() time.Duration {
	if ag.cfg.LeaseMargin > 0 {
		return ag.cfg.LeaseMargin
	}
	return defaultMargin
}

// log writes one JSON line: code plus key/value pairs. Never pass secrets.
func (ag *Agent) log(level, code string, kv ...string) {
	m := map[string]string{"time": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "code": code}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	ag.logMu.Lock()
	defer ag.logMu.Unlock()
	_, _ = ag.cfg.Log.Write(append(b, '\n'))
}

func (ag *Agent) kick() {
	select {
	case ag.kickCh <- struct{}{}:
	default:
	}
}

func (ag *Agent) heartbeatSoon() {
	select {
	case ag.hbNow <- struct{}{}:
	default:
	}
}

// Run executes the agent until ctx ends or the node is revoked.
func (ag *Agent) Run(ctx context.Context) error {
	if err := os.MkdirAll(ag.cfg.StateDir, 0700); err != nil {
		return &ExitError{ExitRuntime, "cannot create state dir"}
	}
	_ = os.Chmod(ag.cfg.StateDir, 0700)
	for _, d := range []string{ag.dataRoot(), filepath.Join(ag.cfg.StateDir, "workers")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return &ExitError{ExitRuntime, "cannot create state dir"}
		}
	}
	// Exclusive ownership before reading or writing anything else in the
	// state dir: a second agent fails without touching journal or identity.
	unlock, err := lockStateDir(ag.cfg.StateDir)
	if err != nil {
		return &ExitError{ExitRuntime, "state dir is in use by another agent"}
	}
	defer unlock()
	if err := ag.loadOrEnroll(ctx); err != nil {
		return err
	}
	var j *Journal
	j, err = OpenJournal(filepath.Join(ag.cfg.StateDir, "journal.jsonl"))
	if err != nil {
		return &ExitError{ExitRuntime, "cannot open journal"}
	}
	ag.journal = j
	defer j.Close()
	if ag.cache, err = openSnapshotCache(filepath.Join(ag.cfg.StateDir, "snapshots.jsonl")); err != nil {
		return &ExitError{ExitRuntime, "cannot open snapshot cache"}
	}
	// Reconcile before the first heartbeat: unfinished attempts become
	// cleanup reports; they are never re-run.
	ag.reconcileStartup()
	if err := j.Compact(); err != nil {
		ag.log("warn", "journal_compact_failed")
	}
	ag.spawner = newSpawner()
	ag.ctx, ag.cancel = context.WithCancel(context.Background())
	defer ag.cancel()
	if ag.cfg.Autoscale {
		// Zero workers at rest: learn the analysis capabilities from one
		// short-lived probe worker before the first heartbeat, so the
		// controller can see this node as capable without a running worker.
		ag.probe(ctx)
	}

	revoked := false
	if err := ag.heartbeat(ctx); IsUnauthenticated(err) {
		revoked = true
	}
	hb := time.NewTicker(ag.heartbeatInterval())
	defer hb.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	drain := ag.cfg.Drain
	for !revoked {
		ag.reconcileWorkers(ctx)
		ag.claimIdle()
		ag.retryProbe(ctx)
		if ag.drained() {
			ag.log("info", "agent_drained")
			return ag.shutdown()
		}
		select {
		case <-ctx.Done():
			return ag.shutdown()
		case <-drain:
			drain = nil
			ag.beginDrain()
			ag.heartbeatSoon()
		case <-hb.C:
			if err := ag.heartbeat(ctx); IsUnauthenticated(err) {
				revoked = true
			}
		case <-ag.hbNow:
			if err := ag.heartbeat(ctx); IsUnauthenticated(err) {
				revoked = true
			}
		case <-ag.kickCh:
		case <-poll.C:
		}
	}
	ag.log("error", "node_revoked")
	_ = ag.shutdown()
	return &ExitError{ExitRevoked, "node revoked"}
}

var errStateLocked = errors.New("state dir locked")

// normalizeURL is the identity binding key of a controller URL.
func normalizeURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }
