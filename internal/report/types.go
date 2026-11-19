// Package report builds the CODEYE structured report (W09) from verified,
// structured run data: database task rows, the attempt's result_manifest and
// the artifacts it references. It performs no I/O and calls no model: the
// publisher (internal/reportsvc) loads and re-hashes every byte it passes in.
//
// Every number, status and verdict is computed here from structured data.
// Tool observations, finding candidates and human decisions stay separate
// records; a tool-only finding is always validation=unreviewed. A report
// cannot claim completion or a clean verdict unless the required AI review
// tasks succeeded and a registered decoder accepted their result bytes.
package report

import "time"

// Version identifiers of this builder. BuilderVersion is part of the input
// digest: a new builder version publishes a new report revision.
const (
	SchemaVersion = "0.1"
	BuilderID     = "codeye-report"
	// BuilderVersion 4 (W19) adds the tool rows' rule_context and the
	// observations' suppressed flag (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제, docs/REPORT.md#항목-상태).
	BuilderVersion = "4"
)

// Report statuses (schemas/report.schema.json).
const (
	StatusCompleted = "completed"
	StatusPartial   = "partial"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Quality verdicts. None of them is a safety certification.
const (
	VerdictAttention    = "attention_required"
	VerdictNoActionable = "no_actionable_findings"
	VerdictInconclusive = "inconclusive"
)

// AI review states (coverage.ai_review.status).
const (
	AIReviewNotRequested = "not_requested"
	AIReviewNotPerformed = "not_performed"
	AIReviewPartial      = "partial"
	AIReviewPerformed    = "performed"
)

// Report is the stored, canonical report document. Field names follow
// schemas/report.schema.json (v0.1 plus the W09 additive properties).
type Report struct {
	SchemaVersion  string        `json:"schema_version"`
	Demo           bool          `json:"demo"`
	ID             string        `json:"id"`
	RunID          string        `json:"run_id"`
	Revision       int           `json:"revision"`
	CreatedAt      string        `json:"created_at"`
	Status         string        `json:"status"`
	QualityVerdict string        `json:"quality_verdict"`
	Builder        Builder       `json:"builder"`
	Export         Export        `json:"export"`
	Inputs         Inputs        `json:"inputs"`
	Attempt        Attempt       `json:"attempt"`
	Summary        Summary       `json:"summary"`
	Coverage       Coverage      `json:"coverage"`
	Tasks          []Task        `json:"tasks"`
	Executions     []Execution   `json:"executions"`
	Artifacts      []Artifact    `json:"artifacts"`
	Observations   []Observation `json:"observations"`
	Findings       []Finding     `json:"findings"`
	Limitations    []string      `json:"limitations"`
}

// Builder identifies the code and input that produced the report.
type Builder struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	InputDigest string `json:"input_digest"`
	// ResultManifestArtifactID is null when the attempt uploaded no
	// result_manifest (failed/cancelled attempts); only DB task rows are shown.
	ResultManifestArtifactID *string `json:"result_manifest_artifact_id"`
}

// Export describes the projection a document is. The stored report is
// include_code=true; Project(false) produces the code-free export.
type Export struct {
	IncludeCode bool `json:"include_code"`
}

type Inputs struct {
	SnapshotID     string  `json:"snapshot_id"`
	SourceKind     string  `json:"source_kind"`
	Description    string  `json:"description"`
	CommitSHA      *string `json:"commit_sha"`
	ManifestSHA256 string  `json:"manifest_sha256"`
	Profile        string  `json:"profile"`
}

// Attempt is the attempt whose accepted completion the report describes.
// CompletionStatus is the run status the controller recorded at completion.
type Attempt struct {
	ID               string  `json:"id"`
	Number           int64   `json:"number"`
	LeaseEpoch       int64   `json:"lease_epoch"`
	CompletionStatus string  `json:"completion_status"`
	CleanupState     string  `json:"cleanup_state"`
	Reason           *string `json:"reason"`
}

// Summary holds the single set of counts every format renders.
type Summary struct {
	TotalFindings          int      `json:"total_findings"`
	ActionableFindings     int      `json:"actionable_findings"`
	PendingCandidates      int      `json:"pending_candidates"`
	RefutedFindings        int      `json:"refuted_findings"`
	RawObservations        int      `json:"raw_observations"`
	UnresolvedObservations int      `json:"unresolved_observations"`
	Priorities             []string `json:"priorities"`

	// HiddenFindings counts findings whose publication-time human
	// disposition hides them from the action counts (W23). They stay in
	// TotalFindings and in comparison. Omitted when zero (older revisions).
	HiddenFindings int `json:"hidden_findings,omitempty"`

	// MetricObservations counts the observations of RawObservations that
	// are structure metrics (W21, docs/ANALYZERS.md: complexity, duplication). They
	// are never findings, candidates or actionable; omitted when zero.
	MetricObservations int `json:"metric_observations,omitempty"`
}

// ReviewCall records one request once across the cumulative stage artifacts.
type ReviewCall struct {
	Role          string `json:"role"`
	Model         string `json:"model"`
	Version       string `json:"opencode_version"`
	PackDigest    string `json:"pack_sha256"`
	RequestDigest string `json:"request_sha256"`
	Outcome       string `json:"outcome"`
	// PackID/PackVersion are set for review-r1 records (schema 0.2), which
	// record the pack identity; R0 (0.1) calls leave them out.
	PackID      string `json:"pack_id,omitempty"`
	PackVersion string `json:"pack_version,omitempty"`
}

type Coverage struct {
	ReviewCalls        []ReviewCall   `json:"review_calls,omitempty"`
	ScopeMode          string         `json:"scope_mode"`
	InventoryFiles     int            `json:"inventory_files"`
	ToolProcessedFiles *int           `json:"tool_processed_files"`
	LLMExposedFiles    *int           `json:"llm_exposed_files"`
	TestStatus         string         `json:"test_status"`
	Notes              []string       `json:"notes"`
	AIReview           AIReview       `json:"ai_review"`
	Tools              []ToolCoverage `json:"tools"`
}

// AIReview reports whether the required model review actually happened.
// Reason is null only when Status is performed or not_requested.
type AIReview struct {
	Status        string         `json:"status"`
	Reason        *string        `json:"reason"`
	RequiredTasks []AIReviewTask `json:"required_tasks"`
	// OptionalTasks are planned review tasks not required for the verdict
	// (review-r1 environment/interpret, documents without documents). They
	// are listed with their outcome and reason and never count as performed.
	OptionalTasks []AIReviewTask `json:"optional_tasks,omitempty"`
}

// AIReviewTask is one required review task key and what was verified for it.
// Result: accepted (task succeeded and a registered decoder accepted its
// bytes) | not_planned | not_succeeded | result_missing | result_rejected.
type AIReviewTask struct {
	Key        string  `json:"key"`
	TaskStatus *string `json:"task_status"`
	Result     string  `json:"result"`
	// Reason is the task's reason code (review-r1 plans only).
	Reason *string `json:"reason,omitempty"`
}

// ToolCoverage is one analyzer task's scope, copied from its decoded artifact.
type ToolCoverage struct {
	TaskKey        string         `json:"task_key"`
	Analyzer       string         `json:"analyzer"`
	ToolVersion    *string        `json:"tool_version"`
	ExitClass      string         `json:"exit_class"`
	PartialParse   bool           `json:"partial_parse"`
	FilesInScope   int            `json:"files_in_scope"`
	ProcessedFiles *int           `json:"processed_files"`
	IgnoredFiles   int            `json:"ignored_files"`
	UnlistedFiles  int            `json:"unlisted_files"`
	Packages       map[string]int `json:"packages"`
	StderrFailures int            `json:"stderr_failures"`
	AnalyzerErrors int            `json:"analyzer_errors"`
	Limitations    []string       `json:"limitations"`

	// ProcessedPaths are the snapshot files the tool analyzed in an accepted
	// succeeded task (W23). Only these locations count as inspected when a
	// comparison decides a vanished finding was resolved. Omitted when the
	// adapter does not record them (and in revisions before builder 3).
	ProcessedPaths []string `json:"processed_paths,omitempty"`
	// RuleContext is the rule pack and configuration the tool ran with
	// (builder 4, docs/ANALYZERS.md#규칙-문맥과-소스-안-억제). A comparison resolves a vanished finding only
	// when it is known and equal on both sides. Omitted in revisions before
	// builder 4; RuleContextOf synthesizes go vet's.
	RuleContext *RuleContext `json:"rule_context,omitempty"`
}

// Task is one DB task row as accepted by the controller (the authority).
type Task struct {
	ID                 string  `json:"id"`
	Key                string  `json:"key"`
	Status             string  `json:"status"`
	RequiredForVerdict bool    `json:"required_for_verdict"`
	Reason             *string `json:"reason"`
}

// Execution is one process run inside a task, bound to its stream artifacts.
// ID is "<task_key>/<name>"; TaskExecutionID is the server row key
// "<attempt_id>/<task_id>/<ordinal>".
type Execution struct {
	ID               string   `json:"id"`
	TaskKey          string   `json:"task_key"`
	TaskExecutionID  string   `json:"task_execution_id"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	ExitCode         *int     `json:"exit_code"`
	Reason           *string  `json:"reason"`
	Cleanup          string   `json:"cleanup"`
	Argv             []string `json:"argv"`
	Cwd              string   `json:"cwd"`
	EnvKeys          []string `json:"env_keys"`
	StdoutArtifactID *string  `json:"stdout_artifact_id"`
	StderrArtifactID *string  `json:"stderr_artifact_id"`
	StdoutTruncated  bool     `json:"stdout_truncated"`
	StderrTruncated  bool     `json:"stderr_truncated"`
	StartedAt        *string  `json:"started_at"`
	EndedAt          *string  `json:"ended_at"`
}

// Artifact is a finalized attempt artifact bound by sha256 and size. Raw bytes
// are never embedded in the report. Decoded tells whether a registered decoder
// accepted the bytes (false for raw streams and unknown documents).
type Artifact struct {
	ID        string  `json:"id"`
	TaskKey   *string `json:"task_key"`
	Role      string  `json:"role"`
	MediaType string  `json:"media_type"`
	SHA256    string  `json:"sha256"`
	SizeBytes int64   `json:"size_bytes"`
	Decoder   *string `json:"decoder"`
}

// Observation is one raw tool diagnostic, never rewritten by later stages.
// RawMessage is null in the code-free export.
type Observation struct {
	ID              string               `json:"id"`
	TaskKey         string               `json:"task_key"`
	TaskExecutionID string               `json:"task_execution_id"`
	Tool            string               `json:"tool"`
	ToolVersion     *string              `json:"tool_version"`
	RuleID          string               `json:"rule_id"`
	RawSeverity     *string              `json:"raw_severity"`
	RawMessage      *string              `json:"raw_message"`
	Location        *ObservationLocation `json:"location"`
	LocationState   string               `json:"location_state"`
	EvidenceState   string               `json:"evidence_state"`
	RawRef          RawRef               `json:"raw_ref"`
	SuggestedFixes  int                  `json:"suggested_fix_count"`
	// Suppressed marks a diagnostic the tool reported as hidden by an
	// in-source suppression (builder 4). It is never a finding; a comparison
	// uses it to tell "suppressed" from "fixed".
	Suppressed bool `json:"suppressed,omitempty"`
	// Metric marks a structure measurement over its threshold (W21, docs/ANALYZERS.md),
	// never a defect: it is never a finding. Its numbers hold no code, so
	// the code-free export keeps them.
	Metric *ObservationMetric `json:"metric,omitempty"`
}

// ObservationMetric is the measured value of a metric observation (the
// envelope's metric, docs/ANALYZERS.md).
type ObservationMetric struct {
	Name      string `json:"name"`
	Value     int    `json:"value"`
	Threshold int    `json:"threshold"`
	Unit      string `json:"unit"`
}

// Evidence states of an observation location (report-level check).
const (
	EvidenceVerified         = "verified_against_source_manifest"
	EvidenceManifestMismatch = "source_manifest_mismatch"
	EvidenceUnresolved       = "unresolved"
)

type ObservationLocation struct {
	SnapshotID string `json:"snapshot_id"`
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	StartCol   int    `json:"start_col"`
	EndCol     int    `json:"end_col"`
	ColumnUnit string `json:"column_unit"`
	BlobSHA256 string `json:"blob_sha256"`
}

// RawRef points into a stored raw stream artifact.
type RawRef struct {
	ArtifactID  string `json:"artifact_id"`
	SHA256      string `json:"sha256"`
	StreamIndex int    `json:"stream_index"`
	JSONPointer string `json:"json_pointer"`
	ByteStart   int64  `json:"byte_start"`
	ByteEnd     int64  `json:"byte_end"`
}

// Finding follows schemas/finding.schema.json v0.1.
type Finding struct {
	SchemaVersion      string         `json:"schema_version"`
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Category           string         `json:"category"`
	Severity           string         `json:"severity"`
	SeverityReason     string         `json:"severity_reason"`
	Validation         string         `json:"validation"`
	Disposition        string         `json:"disposition"`
	Conditions         []Condition    `json:"conditions"`
	Impact             string         `json:"impact"`
	Locations          []Location     `json:"locations"`
	Claims             []Claim        `json:"claims"`
	Evidence           []Evidence     `json:"evidence"`
	CounterevidenceIDs []string       `json:"counterevidence_ids"`
	ObservationIDs     []string       `json:"observation_ids"`
	Fingerprint        string         `json:"fingerprint,omitempty"`
	Recommendation     Recommendation `json:"recommendation"`
	Verification       []string       `json:"verification"`
	Limitations        []string       `json:"limitations"`

	// FingerprintBasis records the fp2 identity inputs (W23); absent in
	// revisions before builder 3, whose fingerprints are kept as stored.
	FingerprintBasis *FingerprintBasis `json:"fingerprint_basis,omitempty"`
	// DispositionVersion is the human review version stamped at publication
	// when Disposition is not open.
	DispositionVersion *int64 `json:"disposition_version,omitempty"`
}

type Condition struct {
	Statement   string   `json:"statement"`
	Status      string   `json:"status"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Location struct {
	SnapshotID string `json:"snapshot_id"`
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Symbol     string `json:"symbol,omitempty"`
}

type Claim struct {
	Assertion   string   `json:"assertion"`
	Kind        string   `json:"kind"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// Evidence is a reference to a collected fact. Only the collection layer
// (decoders over verified artifacts) creates evidence.
type Evidence struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Description     string    `json:"description"`
	Location        *Location `json:"location,omitempty"`
	BlobSHA256      string    `json:"blob_sha256,omitempty"`
	ArtifactID      string    `json:"artifact_id,omitempty"`
	TaskExecutionID string    `json:"task_execution_id,omitempty"`
	ExitStatus      string    `json:"exit_status,omitempty"`
	JSONPointer     *string   `json:"json_pointer,omitempty"`
}

type Recommendation struct {
	Summary   string   `json:"summary"`
	Tradeoffs []string `json:"tradeoffs"`
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
