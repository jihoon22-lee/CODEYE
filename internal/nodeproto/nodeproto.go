// Package nodeproto defines the JSON wire types of the CODEYE node protocol
// (protocol "0.1"): enrollment, node heartbeat, claim, attempt heartbeat,
// attempt events, artifact upload, completion and cleanup reports.
//
// It is shared by the controller (internal/httpapi node routes) and the node
// agent HTTP client. It imports only the standard library and internal/domain;
// it must never import storage, migrations or a database driver (the node
// never touches the database).
package nodeproto

import (
	"regexp"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// ProtocolVersion is the only node protocol version this build speaks.
const ProtocolVersion = "0.1"

// Timing defaults the controller announces at enrollment.
const (
	HeartbeatSeconds = 5
	LeaseSeconds     = 30
	// NodeStaleSeconds: a node whose last heartbeat is older than this gets no
	// new claims (waiting_reason node_stale).
	NodeStaleSeconds = 20
)

// Request/response headers used by node routes.
const (
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderWorkerID       = "X-CODEYE-Worker-ID"
	HeaderLeaseEpoch     = "X-CODEYE-Lease-Epoch"
	HeaderArtifactRole   = "X-CODEYE-Artifact-Role"
	HeaderContentSHA256  = "X-Content-SHA256"
)

// MaxArtifactBytes is the largest single artifact upload.
const MaxArtifactBytes = 64 << 20

// Error codes added by the node protocol (W03 envelope).
const (
	CodeNodeUnauthenticated = "NODE_UNAUTHENTICATED" // 401
	CodeStaleAttempt        = "STALE_ATTEMPT"        // 409
	CodeIntegrityMismatch   = "INTEGRITY_MISMATCH"   // 422
	CodeProtocolUnsupported = "PROTOCOL_UNSUPPORTED" // 422
)

// --- enrollment ------------------------------------------------------------

// EnrollRequest is POST /api/v1/nodes/enroll (no bearer).
type EnrollRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	Name            string `json:"name"`
	ProtocolVersion string `json:"protocol_version"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
}

// EnrollResponse is the 201 body. NodeSecret is returned exactly once.
type EnrollResponse struct {
	NodeID           string `json:"node_id"`
	NodeSecret       string `json:"node_secret"`
	PoolID           string `json:"pool_id"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
	LeaseSeconds     int    `json:"lease_seconds"`
}

// --- node heartbeat --------------------------------------------------------

// Worker states an agent may report.
const (
	WorkerStarting = "starting"
	WorkerIdle     = "idle"
	WorkerBusy     = "busy"
	WorkerDraining = "draining"
	WorkerStopped  = "stopped"
	WorkerFailed   = "failed"
)

// ObservedWorker is one worker process the agent supervises.
type ObservedWorker struct {
	ID        string  `json:"id"`
	State     string  `json:"state"`
	AttemptID *string `json:"attempt_id"`
}

// Capacity is what the node reports as available. Units are bytes.
type Capacity struct {
	CPUCores             float64 `json:"cpu_cores"`
	MemoryAvailableBytes int64   `json:"memory_available_bytes"`
	DiskAvailableBytes   int64   `json:"disk_available_bytes"`
}

// Cleanup states and verification methods.
const (
	CleanupConfirmed = "confirmed"
	CleanupUncertain = "uncertain"

	MethodProcessGroupEmpty = "process_group_empty"
	MethodJobObjectClosed   = "job_object_closed"
	MethodNoProcessStarted  = "no_process_started"
)

// CleanupReport tells the controller the real cleanup result of an attempt.
type CleanupReport struct {
	AttemptID    string    `json:"attempt_id"`
	WorkerID     string    `json:"worker_id"`
	LeaseEpoch   int64     `json:"lease_epoch"`
	CleanupState string    `json:"cleanup_state"`
	Method       string    `json:"method"`
	VerifiedAt   time.Time `json:"verified_at"`
}

// NodeHeartbeatRequest is POST /api/v1/nodes/{node_id}/heartbeat.
type NodeHeartbeatRequest struct {
	ProtocolVersion     string           `json:"protocol_version"`
	ObservedWorkers     []ObservedWorker `json:"observed_workers"`
	Capacity            Capacity         `json:"capacity"`
	Capabilities        []string         `json:"capabilities"`
	LastCommandSequence int64            `json:"last_command_sequence"`
	CleanupReports      []CleanupReport  `json:"cleanup_reports"`
	// WorkerScaling (W07, optional) is ScalingStatic (absent: the agent keeps
	// its own --workers N) or ScalingController (the agent starts and stops
	// workers only on controller commands). Static agents omit it, so their
	// heartbeat bytes are unchanged.
	WorkerScaling string `json:"worker_scaling,omitempty"`
	// MaxWorkers (W07) is the agent's local hard worker cap; required with
	// ScalingController, absent otherwise.
	MaxWorkers int `json:"max_workers,omitempty"`
	// StorageQuotaExceeded (W25, optional) lists the node storage areas
	// (snapshot, workspace, cache, logs) at or over the operator's limit. The
	// controller then creates no new attempt on this node. Agents without a
	// quota omit it, so their heartbeat bytes are unchanged.
	StorageQuotaExceeded []string `json:"storage_quota_exceeded,omitempty"`
}

// Worker scaling modes (heartbeat worker_scaling).
const (
	ScalingStatic     = "static"
	ScalingController = "controller"
	// MaxNodeWorkers bounds a controller-scaled node's reported max_workers.
	MaxNodeWorkers = 64
)

// Command kinds. cancel_attempt is derived from run cancellation (W06);
// start_worker/drain_worker/stop_worker are issued by the W07 autoscaler only
// to nodes that declared ScalingController. drain_worker lets a busy worker
// finish its attempt and then exit; stop_worker is sent only for idle workers.
const (
	CommandCancelAttempt = "cancel_attempt"
	CommandStartWorker   = "start_worker"
	CommandDrainWorker   = "drain_worker"
	CommandStopWorker    = "stop_worker"
)

// Command is a desired-state instruction. Sequence is per node and strictly
// increasing in commit order; the agent dedupes by CommandID.
type Command struct {
	CommandID string  `json:"command_id"`
	Sequence  int64   `json:"sequence"`
	Kind      string  `json:"kind"`
	WorkerID  *string `json:"worker_id"`
	AttemptID *string `json:"attempt_id"`
}

// CleanupResult answers one CleanupReport. Code is null when accepted.
type CleanupResult struct {
	AttemptID string  `json:"attempt_id"`
	Accepted  bool    `json:"accepted"`
	Code      *string `json:"code"`
}

// NodeHeartbeatResponse is the 200 body.
type NodeHeartbeatResponse struct {
	ServerTime     time.Time       `json:"server_time"`
	Commands       []Command       `json:"commands"`
	CleanupResults []CleanupResult `json:"cleanup_results"`
}

// --- claim -----------------------------------------------------------------

// ClaimRequest is POST /api/v1/nodes/{node_id}/claims with header Idempotency-Key.
type ClaimRequest struct {
	WorkerID string `json:"worker_id"`
}

// Lease is the ownership the controller granted. RunVersion is the run's
// version right after the claim committed.
type Lease struct {
	RunID           string    `json:"run_id"`
	AttemptID       string    `json:"attempt_id"`
	WorkerID        string    `json:"worker_id"`
	LeaseEpoch      int64     `json:"lease_epoch"`
	ExpiresAt       time.Time `json:"expires_at"`
	ProjectRevision int64     `json:"project_revision"`
	SnapshotID      *string   `json:"snapshot_id"`
	RunVersion      int64     `json:"run_version"`
}

// JobSource is the declared source. For kind "local" only SourceRootID and
// RelativePath are set (the agent maps the root ID to its own private path;
// the controller never sends a filesystem path). For kind "git" only
// RepositoryURL, Branch and Host are set.
type JobSource struct {
	Kind          string `json:"kind"`
	SourceRootID  string `json:"source_root_id,omitempty"`
	RelativePath  string `json:"relative_path,omitempty"`
	RepositoryURL string `json:"repository_url,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Host          string `json:"host,omitempty"`
}

// JobSnapshot is set only for a retry of a run whose snapshot is already fixed.
// The worker must load it locally and must never re-acquire the source.
type JobSnapshot struct {
	ID             string  `json:"id"`
	ManifestDigest string  `json:"manifest_digest"`
	Kind           string  `json:"kind"`
	Consistency    string  `json:"consistency"`
	SourceSHA      *string `json:"source_sha"`
}

// Resources is the reservation the claim made for this attempt (admission
// bookkeeping, not an OS limit).
type Resources struct {
	CPUMillis   int64 `json:"cpu_millis"`
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
}

// Job is the work description of a lease.
type Job struct {
	Profile   string       `json:"profile"`
	Scope     domain.Scope `json:"scope"`
	Source    JobSource    `json:"source"`
	Snapshot  *JobSnapshot `json:"snapshot"`
	Resources Resources    `json:"resources"`
}

// ClaimResponse is the 200 body. 204 (no body) means nothing claimable.
type ClaimResponse struct {
	Lease Lease `json:"lease"`
	Job   Job   `json:"job"`
}

// --- attempt heartbeat -----------------------------------------------------

// AttemptHeartbeatRequest is POST /api/v1/attempts/{attempt_id}/heartbeat.
type AttemptHeartbeatRequest struct {
	WorkerID   string `json:"worker_id"`
	LeaseEpoch int64  `json:"lease_epoch"`
}

// AttemptHeartbeatResponse is the 200 body.
type AttemptHeartbeatResponse struct {
	ExpiresAt       time.Time `json:"expires_at"`
	CancelRequested bool      `json:"cancel_requested"`
}

// --- attempt events --------------------------------------------------------

// Event kinds.
const (
	EventSnapshotFixed = "snapshot_fixed"
	EventTaskCreated   = "task_created"
	EventTaskStatus    = "task_status"
	EventRunAdvance    = "run_advance"
)

// EventRequest is POST /api/v1/attempts/{attempt_id}/events. Payload is one
// of the *Payload types below, chosen by Kind. The same ClientEventID with the
// same canonical body replays the stored response; a different body is 409.
type EventRequest struct {
	WorkerID      string `json:"worker_id"`
	LeaseEpoch    int64  `json:"lease_epoch"`
	ClientEventID string `json:"client_event_id"`
	Kind          string `json:"kind"`
	Payload       any    `json:"payload"`
}

// SnapshotFixedPayload fixes the run input. ManifestArtifactID must be a
// finalized source_manifest artifact of this attempt whose sha256 equals
// ManifestDigest. kind/consistency: local/stable_copy_best_effort with
// SourceSHA null, or git/git_commit with SourceSHA 40|64 hex.
type SnapshotFixedPayload struct {
	ManifestDigest     string  `json:"manifest_digest"`
	Kind               string  `json:"kind"`
	Consistency        string  `json:"consistency"`
	SourceSHA          *string `json:"source_sha"`
	ManifestArtifactID string  `json:"manifest_artifact_id"`
}

// TaskCreatedPayload declares a task. Key is unique per run; on a retried run
// the same key returns the existing task (its current version). Stage is the
// optional display stage (domain.Stage, docs/ARCHITECTURE.md#표시-단계와-task-단계); pre-W15 workers omit it
// and the task is stored without one. On key reuse an omitted stage keeps the
// stored one, a stage for a task stored without one is recorded once, and a
// different stage is a conflict. Weight (W22) is the optional estimate weight
// 1..1000 of the task for the weighted progress (undeclared counts as 1); it
// follows the same first-record rule as Stage. Controllers before W22 reject
// the field, so the controller is upgraded before the workers.
type TaskCreatedPayload struct {
	Key      string  `json:"key"`
	Required bool    `json:"required"`
	Stage    *string `json:"stage,omitempty"`
	Weight   *int    `json:"weight,omitempty"`
}

// TaskStatusPayload moves a task. ExpectedVersion is the task version.
type TaskStatusPayload struct {
	TaskID          string  `json:"task_id"`
	ExpectedVersion int64   `json:"expected_version"`
	Status          string  `json:"status"`
	Reason          *string `json:"reason"`
}

// RunAdvancePayload moves the run to running or finalizing. ExpectedVersion is
// optional: when set it must equal the current run version (409 otherwise).
type RunAdvancePayload struct {
	ExpectedVersion *int64 `json:"expected_version,omitempty"`
	Status          string `json:"status"`
}

// EventResponse is the 200 body. Sequence is the run event sequence and
// RunVersion the run version after the event. SnapshotID is set for
// snapshot_fixed, TaskID and Version (task version) for task_created and
// Version for task_status.
type EventResponse struct {
	Sequence   int64   `json:"sequence"`
	RunVersion int64   `json:"run_version"`
	SnapshotID *string `json:"snapshot_id,omitempty"`
	TaskID     *string `json:"task_id,omitempty"`
	Version    *int64  `json:"version,omitempty"`
}

// --- artifacts -------------------------------------------------------------

// Artifact roles.
const (
	RoleSourceManifest = "source_manifest"
	RoleResultManifest = "result_manifest"
	RoleStdout         = "stdout"
	RoleStderr         = "stderr"
	RoleObservations   = "observations"
	RoleRawOutput      = "raw_output"
	RoleOther          = "other"
)

// ValidArtifactRole reports whether r is a known artifact role.
func ValidArtifactRole(r string) bool {
	switch r {
	case RoleSourceManifest, RoleResultManifest, RoleStdout, RoleStderr, RoleObservations, RoleRawOutput, RoleOther:
		return true
	}
	return false
}

// ArtifactResponse is the 201 body of
// PUT /api/v1/attempts/{attempt_id}/artifacts/{client_artifact_id}.
type ArtifactResponse struct {
	ArtifactID string `json:"artifact_id"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	Finalized  bool   `json:"finalized"`
}

// --- completion and cleanup ------------------------------------------------

// Outcomes.
const (
	OutcomeSucceeded = "succeeded"
	OutcomePartial   = "partial"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"
)

// CompleteRequest is POST /api/v1/attempts/{attempt_id}/complete.
// ResultManifestID is required for succeeded/partial.
type CompleteRequest struct {
	WorkerID         string  `json:"worker_id"`
	LeaseEpoch       int64   `json:"lease_epoch"`
	Outcome          string  `json:"outcome"`
	CleanupState     string  `json:"cleanup_state"`
	ResultManifestID *string `json:"result_manifest_id"`
	Reason           *string `json:"reason"`
}

// CleanupRequest is POST /api/v1/attempts/{attempt_id}/cleanup. Only
// cleanup_state "confirmed" is accepted.
type CleanupRequest struct {
	WorkerID     string    `json:"worker_id"`
	LeaseEpoch   int64     `json:"lease_epoch"`
	CleanupState string    `json:"cleanup_state"`
	Method       string    `json:"method"`
	VerifiedAt   time.Time `json:"verified_at"`
}

// RunAck is the 202 body of complete and cleanup (W03 RunAck).
type RunAck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	URL     string `json:"url"`
}

// --- reason codes ------------------------------------------------------------

// Run waiting reasons (GET /runs/{id} progress.waiting_reason for queued runs).
const (
	WaitingExecutionUnavailable = "execution_unavailable" // no enrolled, unrevoked node
	WaitingNodeStale            = "node_stale"
	WaitingNoCapableNode        = "no_capable_node"
	WaitingCapacityFull         = "capacity_full"
	WaitingSnapshotUnavailable  = "snapshot_unavailable"
	WaitingRequestUnavailable   = "request_unavailable" // run created before W06 whose request body is unknown
	WaitingProjectLimit         = "project_limit"       // W07: the project already runs project_max_running attempts
	WaitingPoolPaused           = "pool_paused"         // W07: every capable node's pool has max_workers = 0
	// W25: a storage quota is reached (the artifact store, or a storage area
	// of every capable node that would otherwise have room).
	WaitingStorageQuotaExceeded = "storage_quota_exceeded"
)

// Completion/task reasons.
const (
	ReasonSnapshotUnavailable     = "snapshot_unavailable"
	ReasonSourceUnavailable       = "source_unavailable"
	ReasonSourceUnstable          = "source_unstable"
	ReasonSourceLimitExceeded     = "source_limit_exceeded"
	ReasonSourceUnsupported       = "source_unsupported"
	ReasonSnapshotIntegrityFailed = "snapshot_integrity_failed"
	ReasonAnalysisUnsupported     = "analysis_unsupported"
	ReasonLeaseLost               = "lease_lost"
	ReasonCancelled               = "cancelled"
	ReasonHandlerError            = "handler_error"
	ReasonArtifactUploadFailed    = "artifact_upload_failed"
	ReasonDependencyFailed        = "dependency_failed"
	ReasonActorRevoked            = "actor_revoked"
	ReasonSourceGrantRevoked      = "source_grant_revoked"
)

var reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidReason reports whether s is a well-formed reason code (the vocabulary
// is open; the format is fixed).
func ValidReason(s string) bool { return reasonPattern.MatchString(s) }

// --- capabilities ------------------------------------------------------------

var (
	profilePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	taskKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

// ValidTaskKey reports whether k is a valid task key.
func ValidTaskKey(k string) bool { return taskKeyPattern.MatchString(k) }

// AnalysisCapability is "analysis:<profile>".
func AnalysisCapability(profile string) string { return "analysis:" + profile }

// LocalSourceCapability is "source:local:<source_root_id>".
func LocalSourceCapability(rootID string) string { return "source:local:" + rootID }

// GitSourceCapability is "source:git:<host>" (lowercase host[:port]).
func GitSourceCapability(host string) string { return "source:git:" + strings.ToLower(host) }

// MaxCapabilities bounds a heartbeat's capability list.
const MaxCapabilities = 128

// ValidCapability reports whether c is one of the three capability forms.
func ValidCapability(c string) bool {
	switch {
	case strings.HasPrefix(c, "analysis:"):
		return profilePattern.MatchString(strings.TrimPrefix(c, "analysis:"))
	case strings.HasPrefix(c, "source:local:"):
		return domain.ValidID(strings.TrimPrefix(c, "source:local:"))
	case strings.HasPrefix(c, "source:git:"):
		h := strings.TrimPrefix(c, "source:git:")
		return h == strings.ToLower(h) && domain.ValidateHost(h) == nil
	}
	return false
}
