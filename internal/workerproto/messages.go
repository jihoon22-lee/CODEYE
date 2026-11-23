// Package workerproto defines the agent↔worker JSONL protocol
// (codeye-worker/0.1): one UTF-8 JSON object per line on the worker's stdin
// (agent→worker) and stdout (worker→agent). It holds no HTTP client and no
// credential: the worker never sees the node secret, DSN or tokens.
package workerproto

import (
	"encoding/json"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Protocol is the version string exchanged in hello/ready.
const Protocol = "codeye-worker/0.1"

// MaxLine bounds one encoded message (including the newline).
const MaxLine = 1 << 20

// Message types.
const (
	TypeHello          = "hello"
	TypeJob            = "job"
	TypeReply          = "reply"
	TypeStopNewTasks   = "stop_new_tasks"
	TypeCancel         = "cancel"
	TypeShutdown       = "shutdown"
	TypeReady          = "ready"
	TypeEvent          = "event"
	TypeArtifact       = "artifact"
	TypeIntent         = "intent"
	TypeProcessStarted = "process_started"
	TypeProcessEnded   = "process_ended"
	TypeComplete       = "complete"
	TypeIdle           = "idle"
	TypeLog            = "log"
)

// Reply error codes.
const (
	ErrStaleAttempt      = "stale_attempt"
	ErrConflict          = "conflict"
	ErrUnavailable       = "unavailable"
	ErrIntegrityMismatch = "integrity_mismatch"
	ErrInvalid           = "invalid"
)

// Intent kinds (§7): recorded before any child can exist.
const (
	IntentSourceAcquireStarted = "source_acquire_started"
	IntentSourceAcquireDone    = "source_acquire_done"
	IntentTaskExecStarted      = "task_exec_started"
)

// Cancel reasons.
const (
	CancelRunRequested = "run_cancel_requested"
	CancelLeaseLost    = "lease_lost"
	CancelShutdown     = "agent_shutdown"
)

// Header is common to every message.
type Header struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// Limits mirrors source.Limits on the wire.
type Limits struct {
	MaxFiles      int   `json:"max_files"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	Retries       int   `json:"retries"`
}

// Hello is the first agent→worker message.
type Hello struct {
	ModelExecutable string `json:"model_executable,omitempty"`
	// ReviewMaxCalls is the node operator's model call ceiling per review
	// attempt; 0 means the default (docs/REVIEW.md#모듈-단위-검토-분할).
	ReviewMaxCalls int `json:"review_max_calls,omitempty"`
	// ReviewRequestBytes and ReviewOutputTokens are the node operator's
	// review request budget; 0 means the default (docs/REVIEW.md#요청-크기).
	ReviewRequestBytes int `json:"review_request_bytes,omitempty"`
	ReviewOutputTokens int `json:"review_output_tokens,omitempty"`
	// ModelName is PROVIDER/MODEL.
	ModelName      string `json:"model_name,omitempty"`
	ModelRelayFile string `json:"model_relay_file,omitempty"`
	// ModelCredentialFile is the file the worker reads the provider's API
	// key from at every model call (docs/REVIEW.md#모델-설정); the key itself
	// never travels in the hello.
	ModelCredentialFile string `json:"model_credential_file,omitempty"`
	ModelConfigFile     string `json:"model_config_file,omitempty"`
	// ModelSettings is present only on the native instance's local worker.
	ModelSettings *ModelSettings `json:"model_settings,omitempty"`
	// ModelTimeoutSeconds bounds one model call; 0 means the default (600).
	ModelTimeoutSeconds int `json:"model_timeout_seconds,omitempty"`
	// GitCredentialFile is the file the worker reads a Git host token from
	// for one fetch of that host (the service's GitHub token,
	// docs/GITHUB.md); the token never travels in the hello, and analyzers
	// and the model never see it.
	GitCredentialFile string `json:"git_credential_file,omitempty"`
	Header
	Protocol      string `json:"protocol"`
	WorkerID      string `json:"worker_id"`
	DataRoot      string `json:"data_root"`
	ScratchRoot   string `json:"scratch_root"`
	Limits        Limits `json:"limits"`
	GitExecutable string `json:"git_executable"`
	GoExecutable  string `json:"go_executable"`
	GoModCache    string `json:"go_mod_cache"`
	// AnalyzerTools are operator analyzer tool paths by tool name and the
	// shared toolchain paths under their reserved names (node, docs/ANALYZERS.md
	// §2.4); absent when none is configured.
	AnalyzerTools map[string]string `json:"analyzer_tools,omitempty"`
	// TestPackageManagers carries the operator's npm and Python paths even
	// when dependency fetching is off.
	TestPackageManagers  map[string]string `json:"test_package_managers,omitempty"`
	Tests                *TestSettings     `json:"tests,omitempty"`
	ToolPath             string            `json:"tool_path"`
	JavaClasspathCatalog string            `json:"java_classpath_catalog,omitempty"`
	LeaseMarginSeconds   int               `json:"lease_margin_seconds"`

	// AdvertiseWithoutBaseline: advertise the analysis capability even when no
	// baseline analyzer is usable; its tasks end unsupported (the service).
	AdvertiseWithoutBaseline bool `json:"advertise_without_baseline,omitempty"`

	// Offline is the node operator's offline mode (codeye agent --offline,
	// docs/OPERATIONS.md#오프라인-모드): no built-in public registry, checksum
	// database or model endpoint is used.
	Offline bool `json:"offline,omitempty"`
	// CABundle is the PEM file the processes that reach a registry, a Git
	// host or the model endpoint trust (--ca-bundle).
	CABundle string `json:"ca_bundle,omitempty"`

	// Dependencies is the node operator's dependency fetch configuration
	// (--dependency-fetch and its options, docs/ARCHITECTURE.md#의존성-준비);
	// absent when the operator did not turn fetching on.
	Dependencies *Dependencies `json:"dependencies,omitempty"`
}

// TestSettings are node operator settings for the isolated test runners.
type TestSettings struct {
	GoRun           string `json:"go_run,omitempty"`
	GoRace          bool   `json:"go_race,omitempty"`
	MavenRepository string `json:"maven_repository,omitempty"`
	GradleHome      string `json:"gradle_home,omitempty"`
}

// ModelSettings names the trusted host settings a managed worker reloads at the
// beginning of a review attempt. These fields carry paths, never key values.
type ModelSettings struct {
	InstancePath   string `json:"instance_path"`
	StateDir       string `json:"state_dir"`
	Home           string `json:"home,omitempty"`
	Executable     string `json:"executable,omitempty"`
	OpenCodeConfig string `json:"opencode_config,omitempty"`
	OpenCodeAuth   string `json:"opencode_auth,omitempty"`
}

// Dependencies configures the dependency tasks of a worker.
type Dependencies struct {
	// CacheRoot is the absolute shared dependency cache.
	CacheRoot string `json:"cache_root"`
	// AgentStateDir locates the rest of the cache storage area for the
	// quota check.
	AgentStateDir string `json:"agent_state_dir"`
	// GoProxy is the registry part of GOPROXY ("" = proxy.golang.org);
	// GoSumDB, GoPrivate, GoNoProxy and GoNoSumDB are the operator's GOSUMDB,
	// GOPRIVATE, GONOPROXY and GONOSUMDB ("" = Go's defaults).
	GoProxy          string `json:"go_proxy,omitempty"`
	GoSumDB          string `json:"go_sumdb,omitempty"`
	GoPrivate        string `json:"go_private,omitempty"`
	GoNoProxy        string `json:"go_noproxy,omitempty"`
	GoNoSumDB        string `json:"go_nosumdb,omitempty"`
	ModuleTimeoutS   int    `json:"module_timeout_s"`
	TotalTimeoutS    int    `json:"total_timeout_s"`
	MaxDownloadBytes int64  `json:"max_download_bytes"`
	// CacheQuotaBytes is the cache storage area limit (0 = none).
	CacheQuotaBytes int64 `json:"cache_quota_bytes"`
	// NpmRegistry is the registry URL of npm, pnpm and yarn ("" = npm's,
	// or yarn's for yarn).
	NpmRegistry string `json:"npm_registry,omitempty"`

	// PackageManagers are the operator's package manager paths by flag name
	// (deps.ManagerFlags: npm, pnpm, python, uv, yarn); a fetcher without its manager
	// ends package_manager_not_configured.
	PackageManagers map[string]string `json:"package_managers,omitempty"`
	// PipIndexURL is the Python package index (--pip-index-url, "" = PyPI).
	PipIndexURL string `json:"pip_index_url,omitempty"`
}

// AttemptRef identifies the attempt a message belongs to.
type AttemptRef struct {
	ID    string `json:"id"`
	RunID string `json:"run_id"`
	Epoch int64  `json:"epoch"`
}

// JobSource is the source the agent resolved from its private mapping.
type JobSource struct {
	Kind          string `json:"kind"` // local | git
	ApprovedRoot  string `json:"approved_root,omitempty"`
	RelativePath  string `json:"relative_path,omitempty"`
	RepositoryURL string `json:"repository_url,omitempty"`
	Branch        string `json:"branch,omitempty"`
	AllowedHost   string `json:"allowed_host,omitempty"`
	// AllowedLocalRoot lets a Git source be a file:// repository under this
	// directory. Only test builds (codeye_fixture) set it.
	AllowedLocalRoot string `json:"allowed_local_root,omitempty"`
	// ProtectedDirs: a local project folder that contains or lies inside
	// one of these is refused before acquisition (the service: the state
	// and backup directories).
	ProtectedDirs []string `json:"protected_dirs,omitempty"`
}

// JobSnapshot pins a retry to a cached snapshot. Root is "" when the agent
// has no cached copy (the worker then fails with snapshot_unavailable).
type JobSnapshot struct {
	ID             string `json:"id"`
	ManifestDigest string `json:"manifest_digest"`
	Root           string `json:"root"`
}

// Job hands one claimed attempt to the worker.
type Job struct {
	// AllowModelReview: the job's source may be sent to the configured
	// model (the operator's per-root grant, or the project's choice under
	// --project-analysis-policy).
	AllowModelReview bool `json:"allow_model_review,omitempty"`
	// AllowRepositoryCode is the repository-code grant for the job
	// (docs/ANALYZERS.md#저장소-코드-실행-허가): the operator's per-root grant, or the project's
	// choice under --project-analysis-policy. Absent means not granted.
	AllowRepositoryCode bool `json:"allow_repository_code,omitempty"`
	// AllowDependencyFetch: the dependency tasks may fetch this job's
	// third-party dependencies (the operator's --dependency-fetch and the
	// project's fetch_dependencies choice). Absent means no fetching.
	AllowDependencyFetch bool `json:"allow_dependency_fetch,omitempty"`
	// ModelReviewWithheld and RepositoryCodeWithheld: the project chose the
	// model or its build scripts and the node allows them, but the service
	// does not offer them to the project's owner (the administrator's
	// switches); the tasks record their own reason for it.
	ModelReviewWithheld    bool `json:"model_review_withheld,omitempty"`
	RepositoryCodeWithheld bool `json:"repository_code_withheld,omitempty"`

	Header
	Attempt   AttemptRef      `json:"attempt"`
	Profile   string          `json:"profile"`
	Scope     json.RawMessage `json:"scope"`
	Resources json.RawMessage `json:"resources"`
	Source    JobSource       `json:"source"`
	Snapshot  *JobSnapshot    `json:"snapshot"`
	// RunVersion is the run version after claim (expected_version of the first
	// run_advance).
	RunVersion int64 `json:"run_version"`
	// PullRequest is the pull request of a Git job (nodeproto.Job).
	PullRequest *domain.PullRequestJob `json:"pull_request,omitempty"`
}

// ReplyError carries a stable code only.
type ReplyError struct {
	Code string `json:"code"`
}

// Reply answers a request message.
type Reply struct {
	Header
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ReplyError     `json:"error,omitempty"`
}

// StopNewTasks asks the worker to start no further task.
type StopNewTasks struct {
	Header
	Reason string `json:"reason"`
}

// Cancel asks the worker to cancel the running attempt.
type Cancel struct {
	Header
	Reason string `json:"reason"`
}

// Shutdown asks an idle worker to exit.
type Shutdown struct {
	Header
}

// HandlerInfo is advertised in ready.
type HandlerInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Capability string `json:"capability"`
}

// Ready is the worker's answer to hello.
type Ready struct {
	Header
	Protocol string        `json:"protocol"`
	PID      int           `json:"pid"`
	Handlers []HandlerInfo `json:"handlers"`
}

// Event is forwarded by the agent to POST /attempts/{id}/events.
type Event struct {
	Header
	AttemptID     string          `json:"attempt_id"`
	Epoch         int64           `json:"epoch"`
	ClientEventID string          `json:"client_event_id"`
	Kind          string          `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	// SnapshotRoot is IPC-only (snapshot_fixed): the local snapshot root the
	// agent records in its private cache. It is never sent to the server.
	SnapshotRoot string `json:"snapshot_root,omitempty"`
}

// Artifact asks the agent to upload a file.
type Artifact struct {
	Header
	AttemptID        string `json:"attempt_id"`
	Epoch            int64  `json:"epoch"`
	ClientArtifactID string `json:"client_artifact_id"`
	Path             string `json:"path"`
	Role             string `json:"role"`
	MediaType        string `json:"media_type"`
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
}

// Intent is journaled before a step that may create unidentified children.
type Intent struct {
	Header
	AttemptID string `json:"attempt_id"`
	Epoch     int64  `json:"epoch"`
	Kind      string `json:"kind"`
	TaskKey   string `json:"task_key,omitempty"`
	Ordinal   int    `json:"ordinal,omitempty"`
	Result    string `json:"result,omitempty"`
}

// ProcessStarted is sent from executor OnStarted.
type ProcessStarted struct {
	Header
	AttemptID   string `json:"attempt_id"`
	Epoch       int64  `json:"epoch"`
	TaskKey     string `json:"task_key"`
	Ordinal     int    `json:"ordinal"`
	PID         int    `json:"pid"`
	StartTime   uint64 `json:"start_time"`
	PGID        int    `json:"pgid"`
	Containment string `json:"containment"`
}

// ProcessEnded is sent after executor.Run returned.
type ProcessEnded struct {
	Header
	AttemptID      string `json:"attempt_id"`
	Epoch          int64  `json:"epoch"`
	TaskKey        string `json:"task_key"`
	Ordinal        int    `json:"ordinal"`
	PID            int    `json:"pid"`
	StartTime      uint64 `json:"start_time"`
	Cleanup        string `json:"cleanup"`
	LeftoverKilled bool   `json:"leftover_killed"`
}

// Complete ends the attempt.
type Complete struct {
	Header
	AttemptID                    string  `json:"attempt_id"`
	Epoch                        int64   `json:"epoch"`
	Outcome                      string  `json:"outcome"`
	CleanupState                 string  `json:"cleanup_state"`
	ResultManifestClientArtifact *string `json:"result_manifest_client_artifact_id"`
	Reason                       *string `json:"reason"`
	CleanupMethod                string  `json:"cleanup_method"`
}

// Idle says the worker is free for the next claim.
type Idle struct {
	Header
}

// Log is a bounded diagnostic line without secrets.
type Log struct {
	Header
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// newByType returns a zero message for strict decoding.
func newByType(t string) any {
	switch t {
	case TypeHello:
		return &Hello{}
	case TypeJob:
		return &Job{}
	case TypeReply:
		return &Reply{}
	case TypeStopNewTasks:
		return &StopNewTasks{}
	case TypeCancel:
		return &Cancel{}
	case TypeShutdown:
		return &Shutdown{}
	case TypeReady:
		return &Ready{}
	case TypeEvent:
		return &Event{}
	case TypeArtifact:
		return &Artifact{}
	case TypeIntent:
		return &Intent{}
	case TypeProcessStarted:
		return &ProcessStarted{}
	case TypeProcessEnded:
		return &ProcessEnded{}
	case TypeComplete:
		return &Complete{}
	case TypeIdle:
		return &Idle{}
	case TypeLog:
		return &Log{}
	}
	return nil
}

// header returns the embedded header of a message pointer.
func header(m any) *Header {
	switch v := m.(type) {
	case *Hello:
		return &v.Header
	case *Job:
		return &v.Header
	case *Reply:
		return &v.Header
	case *StopNewTasks:
		return &v.Header
	case *Cancel:
		return &v.Header
	case *Shutdown:
		return &v.Header
	case *Ready:
		return &v.Header
	case *Event:
		return &v.Header
	case *Artifact:
		return &v.Header
	case *Intent:
		return &v.Header
	case *ProcessStarted:
		return &v.Header
	case *ProcessEnded:
		return &v.Header
	case *Complete:
		return &v.Header
	case *Idle:
		return &v.Header
	case *Log:
		return &v.Header
	}
	return nil
}
