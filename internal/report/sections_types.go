package report

import (
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gograph"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// Per-analysis sections (docs/REPORT.md#분석-종류별-절). Builder 5 adds them to every report it
// builds; earlier revisions carry none and keep the flat layout. Sections
// reference observations and findings by ID and carry computed counts; the
// pre-existing fields keep their exact shape and meaning, and the summary,
// status and verdict never depend on a section.

// SectionsSince is the first builder version whose reports carry the
// sections. A revision of an earlier builder must carry none.
const SectionsSince = 5

// Section keys in reading order. The overview has one line per key.
const (
	SectionEnvironment    = "environment"
	SectionDocuments      = "documents"
	SectionStaticAnalysis = "static_analysis"
	SectionMetrics        = "metrics"
	SectionSecrets        = "secrets"
	SectionCodeReview     = "code_review"
	SectionGaps           = "gaps"
	// SectionReport is the gap section of facts that concern the whole run
	// (result manifest, tests).
	SectionReport = "report"
)

// SectionKeys lists the overview lines in reading order.
var SectionKeys = []string{SectionEnvironment, SectionDocuments, SectionStaticAnalysis, SectionMetrics, SectionSecrets, SectionCodeReview, SectionGaps}

// StateNotRecorded is the section state of a run whose tasks carry no stage
// record (docs/ARCHITECTURE.md#표시-단계와-task-단계 detail not_recorded). Every other state is a
// display stage state of internal/progress.
const StateNotRecorded = "not_recorded"

// Analyzer purposes (the envelope's analyzer.purpose, docs/ANALYZERS.md#분석기-목적).
const (
	PurposeDiagnostics = "diagnostics"
	PurposeMetrics     = "metrics"
	PurposeSecrets     = "secrets"
)

// Discovery results of an environment or documents section: whether the
// platform discovery document of that kind reached the report.
const (
	DiscoveryAccepted     = "accepted"
	DiscoveryNotPlanned   = "not_planned"
	DiscoveryNotSucceeded = "not_succeeded"
	DiscoveryMissing      = "result_missing"
	DiscoveryRejected     = "result_rejected"
)

// Model interpretation states of a section.
const (
	ModelPerformed    = "performed"
	ModelNotPerformed = "not_performed"
)

// Secret screening of model inputs as the review records state it.
const (
	ScreenWithheld = "withheld"
	// ScreenScreened: a completed secrets scan screened the inputs and
	// withheld nothing (builder 6).
	ScreenScreened    = "screened"
	ScreenNotScreened = "not_screened"
	ScreenFailed      = "screen_failed"
	ScreenNoModel     = "no_model_input"
)

// SectionStage is the display stage state of the tasks a section covers,
// computed with the run screen's classifier (progress.TaskStage) so the
// report and the run detail cannot disagree about the same tasks.
type SectionStage struct {
	// Stage is the task stage the tasks belong to (domain.TaskStages).
	Stage string `json:"stage"`
	// State is a progress state, or not_recorded when a task of the run has
	// no stage record.
	State     string         `json:"state"`
	Tasks     int            `json:"tasks"`
	Succeeded int            `json:"succeeded"`
	Counts    map[string]int `json:"counts"`
	Reasons   []string       `json:"reasons"`
}

// ModelInterpretation tells whether the model role of a section ran and was
// accepted. Reason is a code and is null only when the role was performed.
type ModelInterpretation struct {
	TaskKey    string  `json:"task_key"`
	State      string  `json:"state"`
	TaskStatus *string `json:"task_status"`
	Reason     *string `json:"reason"`
}

// SectionEvidence is a snapshot excerpt a model statement of the section
// cites: the role record's catalog entry, bound to path, blob and lines.
type SectionEvidence struct {
	Excerpt          *sourceexcerpt.Excerpt  `json:"excerpt,omitempty"`
	ID               string                  `json:"id"`
	Kind             string                  `json:"kind"`
	Path             string                  `json:"path"`
	StartLine        int                     `json:"start_line"`
	EndLine          int                     `json:"end_line"`
	BlobSHA256       string                  `json:"blob_sha256"`
	DirectivePath    string                  `json:"directive_path,omitempty"`
	PackageReference record.PackageReference `json:"package_reference,omitzero"`
}

// Overview is the 종합 section: one line per section and what to look at
// first. It is a pure function of the other sections (overviewOf).
type Overview struct {
	Sections  []OverviewSection `json:"sections"`
	Attention []Attention       `json:"attention"`
}

// OverviewSection is one section line: its state (null for the gap list)
// and its headline numbers, named per section.
type OverviewSection struct {
	Key    string         `json:"key"`
	State  *string        `json:"state"`
	Counts map[string]int `json:"counts"`
}

// Attention is one item to look at first, in priority order.
type Attention struct {
	Section string `json:"section"`
	Code    string `json:"code"`
	Count   int    `json:"count"`
}

// --- 환경 분석 -----------------------------------------------------------------

type EnvironmentSection struct {
	Stage     SectionStage `json:"stage"`
	Discovery string       `json:"discovery"`
	// Lists of the accepted env.discover document; empty without one.
	Modules       []EnvModule       `json:"modules"`
	Manifests     []EnvManifest     `json:"manifests"`
	Requirements  []EnvRequirement  `json:"requirements"`
	Observed      []EnvObserved     `json:"observed"`
	Verdicts      []EnvVerdict      `json:"verdicts"`
	AnalyzerTools []EnvAnalyzerTool `json:"analyzer_tools"`
	// Dependencies are the run's dependency tasks (deps.*) in key order;
	// absent when none was planned (fetching was off).
	Dependencies []EnvDependency `json:"dependencies,omitempty"`
	Limitations  []string        `json:"limitations"`
	// Model is the review.environment role; Notes are its accepted output.
	Model    ModelInterpretation `json:"model"`
	Notes    []EnvNote           `json:"notes"`
	Evidence []SectionEvidence   `json:"evidence"`
}

// EnvDependency is one dependency task. The record fields are null when its
// record did not reach the report (Result says why).
type EnvDependency struct {
	TaskKey        string                 `json:"task_key"`
	TaskStatus     string                 `json:"task_status"`
	TaskReason     *string                `json:"task_reason"`
	Result         string                 `json:"result"`
	Module         *string                `json:"module"`
	Ecosystem      *string                `json:"ecosystem"`
	Path           *string                `json:"path"`
	Manager        *string                `json:"manager"`
	ManagerVersion *string                `json:"manager_version"`
	State          *string                `json:"state"`
	Reason         *string                `json:"reason"`
	Pinned         *bool                  `json:"pinned"`
	Lockfile       *string                `json:"lockfile"`
	Declared       *int                   `json:"declared"`
	Prepared       *int                   `json:"prepared"`
	Failed         *int                   `json:"failed"`
	CacheKind      *string                `json:"cache_kind"`
	FailedItems    []EnvDependencyFailure `json:"failed_items"`
	Limitations    []string               `json:"limitations"`
}

// EnvDependencyFailure names one dependency that was not prepared.
type EnvDependencyFailure struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type EnvModule struct {
	ID          string `json:"id"`
	Ecosystem   string `json:"ecosystem"`
	Path        string `json:"path"`
	InScope     bool   `json:"in_scope"`
	BuildSystem string `json:"build_system,omitempty"`
	BuildRoot   string `json:"build_root,omitempty"`
}

type EnvManifest struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// EnvRequirement is a version or tool requirement the project declares.
// Constraint is the declared text; the code-free export keeps it only when
// it is version-constraint text (requirementConstraintPattern).
type EnvRequirement struct {
	ID         string  `json:"id"`
	Module     string  `json:"module"`
	Tool       string  `json:"tool"`
	Kind       string  `json:"kind"`
	Constraint *string `json:"constraint"`
	Comparison string  `json:"comparison"`
	Path       string  `json:"path"`
	StartLine  int     `json:"start_line"`
	EndLine    int     `json:"end_line"`
}

// EnvObserved is what the node reported for one runtime tool.
type EnvObserved struct {
	Tool    string  `json:"tool"`
	Status  string  `json:"status"`
	Version *string `json:"version"`
	Detail  *string `json:"detail"`
}

// EnvVerdict is the platform comparison of a requirement (or its absence)
// with the observation: satisfied, mismatch or unknown with a reason.
type EnvVerdict struct {
	Module      string  `json:"module"`
	Tool        string  `json:"tool"`
	Requirement *string `json:"requirement"`
	Status      string  `json:"status"`
	Reason      *string `json:"reason"`
}

type EnvAnalyzerTool struct {
	Tool      string   `json:"tool"`
	Analyzers []string `json:"analyzers"`
	Status    string   `json:"status"`
	Version   *string  `json:"version"`
	Detail    *string  `json:"detail"`
}

// EnvNote is model interpretation (review.environment), shown labeled.
type EnvNote struct {
	Module            string   `json:"module"`
	RequirementIDs    []string `json:"requirement_ids"`
	EvidenceIDs       []string `json:"evidence_ids"`
	AffectedChecks    string   `json:"affected_checks"`
	StillCheckable    string   `json:"still_checkable"`
	NeededObservation string   `json:"needed_observation"`
}

// --- 문서 분석 -----------------------------------------------------------------

type DocumentsSection struct {
	Quality           []record.DocumentQuality `json:"quality,omitempty"`
	Comparisons       []DocComparison          `json:"comparisons,omitempty"`
	DocumentationGaps []DocCoverageGap         `json:"documentation_gaps,omitempty"`
	Stage             SectionStage             `json:"stage"`
	Discovery         string                   `json:"discovery"`
	// Budget is null without an accepted docs.discover document.
	Budget      *DocBudget   `json:"budget"`
	Items       []DocItem    `json:"items"`
	Modules     []DocModule  `json:"modules"`
	Overlaps    []DocOverlap `json:"overlaps"`
	Limitations []string     `json:"limitations"`
	// Model is the review.documents role; the lists below are its accepted
	// output (model interpretation).
	Model            ModelInterpretation  `json:"model"`
	Contracts        []DocContract        `json:"contracts"`
	Conflicts        []DocConflict        `json:"conflicts"`
	Questions        []DocQuestion        `json:"questions"`
	InstructionNotes []DocInstructionNote `json:"instruction_notes"`
	Evidence         []SectionEvidence    `json:"evidence"`
}

// DocCoverageGap is a separate discovery observation. It has no model verdict.
type DocCoverageGap struct {
	Kind     string              `json:"kind"`
	Symbol   string              `json:"symbol,omitempty"`
	Default  string              `json:"default,omitempty"`
	Scope    string              `json:"scope"`
	Source   DocCoverageLocation `json:"source"`
	Searched []DocCoverageSearch `json:"searched"`
}

type DocCoverageLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type DocCoverageSearch struct {
	Path      string `json:"path"`
	LineCount int    `json:"line_count"`
}

type DocBudget struct {
	TotalBytes       int64 `json:"total_bytes"`
	PerDocumentBytes int64 `json:"per_document_bytes"`
	MaxDocuments     int   `json:"max_documents"`
	ReadBytes        int64 `json:"read_bytes"`
}

type DocItem struct {
	Path      string  `json:"path"`
	Class     string  `json:"class"`
	AppliesTo string  `json:"applies_to"`
	Status    string  `json:"status"`
	Reason    *string `json:"reason"`
	SizeBytes *int64  `json:"size_bytes"`
	LineCount *int    `json:"line_count"`
}

type DocModule struct {
	Module    string   `json:"module"`
	State     string   `json:"state"`
	Own       []string `json:"own"`
	Inherited []string `json:"inherited"`
}

type DocOverlap struct {
	Path       string   `json:"path"`
	Documents  []string `json:"documents"`
	Assessment string   `json:"assessment"`
}

type DocContract struct {
	Statement   string   `json:"statement"`
	AppliesTo   string   `json:"applies_to"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type DocConflict struct {
	Scope          string   `json:"scope"`
	Summary        string   `json:"summary"`
	Impact         string   `json:"impact"`
	FirstEvidence  []string `json:"first_evidence_ids"`
	SecondEvidence []string `json:"second_evidence_ids"`
}

type DocQuestion struct {
	Question    string   `json:"question"`
	AppliesTo   string   `json:"applies_to"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type DocInstructionNote struct {
	Note        string   `json:"note"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// --- 정적 분석 -----------------------------------------------------------------

// StaticAnalysisSection lists every static_analysis task by language
// (ecosystem) and tool. Its stage state is the whole static_analysis stage,
// as on the run screen; metric and secret observations of a tool are in
// their own sections and only counted here.
type StaticAnalysisSection struct {
	Stage     SectionStage     `json:"stage"`
	Languages []StaticLanguage `json:"languages"`
}

// StaticLanguage groups tools by envelope ecosystem; Ecosystem is null for
// tasks without an accepted envelope (no ecosystem is inferred from a key).
type StaticLanguage struct {
	Ecosystem *string      `json:"ecosystem"`
	Tools     []StaticTool `json:"tools"`
}

// StaticTool is one analyzer task. ObservationIDs are its diagnostic
// observations and FindingIDs its tool findings (the partition members);
// Result says whether its envelope reached the report.
type StaticTool struct {
	TaskKey        string      `json:"task_key"`
	Required       bool        `json:"required"`
	TaskStatus     string      `json:"task_status"`
	TaskReason     *string     `json:"task_reason"`
	Result         string      `json:"result"`
	Analyzer       *string     `json:"analyzer"`
	Purpose        *string     `json:"purpose"`
	Tool           *string     `json:"tool"`
	ToolVersion    *string     `json:"tool_version"`
	ExitClass      *string     `json:"exit_class"`
	PartialParse   bool        `json:"partial_parse"`
	FilesInScope   *int        `json:"files_in_scope"`
	ProcessedFiles *int        `json:"processed_files"`
	RulePack       *string     `json:"rule_pack"`
	ObservationIDs []string    `json:"observation_ids"`
	FindingIDs     []string    `json:"finding_ids"`
	Rules          []RuleTally `json:"rules"`
	Suppressed     int         `json:"suppressed"`
	Unresolved     int         `json:"unresolved"`
	// Counts of this task's observations shown in the metrics and secrets
	// sections.
	MetricObservations int `json:"metric_observations"`
	SecretObservations int `json:"secret_observations"`
}

// RuleTally counts a tool's diagnostic observations of one rule and raw
// severity (null: the tool gave none or a free-text grade).
type RuleTally struct {
	RuleID      string  `json:"rule_id"`
	RawSeverity *string `json:"raw_severity"`
	Count       int     `json:"count"`
}

// --- 구조·중복 지표 ----------------------------------------------------------

// MetricsSection holds the structure measurements over their thresholds.
// They are never defects. TaskKeys are the analyzer tasks of purpose
// metrics; Stage is their state with the stage classifier.
type MetricsSection struct {
	Stage          SectionStage       `json:"stage"`
	TaskKeys       []string           `json:"task_keys"`
	Metrics        []MetricGroup      `json:"metrics"`
	GoDependencies []gograph.Document `json:"go_package_dependencies,omitempty"`
}

type MetricGroup struct {
	Name           string   `json:"name"`
	Unit           string   `json:"unit"`
	Thresholds     []int    `json:"thresholds"`
	MaxValue       int      `json:"max_value"`
	ObservationIDs []string `json:"observation_ids"`
}

// --- 비밀정보 ------------------------------------------------------------------

// SecretsSection lists secret detections. No value, part or hash of a
// secret is stored anywhere; a detection is a rule and a location.
type SecretsSection struct {
	Stage      SectionStage      `json:"stage"`
	TaskKeys   []string          `json:"task_keys"`
	Detections []SecretDetection `json:"detections"`
	// ModelInput is how review inputs were screened, as the accepted review
	// records and task reasons state it.
	ModelInput string `json:"model_input"`
}

type SecretDetection struct {
	ObservationID string  `json:"observation_id"`
	TaskKey       string  `json:"task_key"`
	RuleID        string  `json:"rule_id"`
	Path          *string `json:"path"`
	StartLine     int     `json:"start_line"`
	EndLine       int     `json:"end_line"`
	EvidenceState string  `json:"evidence_state"`
	// FindingID is the tool finding of this detection (listed in the static
	// analysis section), null when the location was not verified.
	FindingID *string `json:"finding_id"`
}

// --- 코드 분석(모델 검토) -------------------------------------------------------

// CodeReviewSection is the model code review: pack, roles, calls, what the
// model saw, the candidates and their verification outcome.
type CodeReviewSection struct {
	Strengths   []record.Strength `json:"strengths,omitempty"`
	Evidence    []SectionEvidence `json:"evidence,omitempty"`
	Stage       SectionStage      `json:"stage"`
	PackID      *string           `json:"pack_id"`
	PackVersion *string           `json:"pack_version"`
	// Roles are the code_review stage review tasks with their result.
	Roles         []AIReviewTask `json:"roles"`
	Calls         int            `json:"calls"`
	AcceptedCalls int            `json:"accepted_calls"`
	// RequestBytes is the sum of the recorded model request sizes (prompt
	// bytes, not source bytes); null when a record does not state them.
	RequestBytes   *int     `json:"request_bytes"`
	ExposedFiles   *int     `json:"exposed_files"`
	InventoryFiles int      `json:"inventory_files"`
	FindingIDs     []string `json:"finding_ids"`
	// Limits are the interpret role's statements about tool observations it
	// could not relate to code (model interpretation).
	Limits []InterpretationLimit `json:"interpretation_limits"`
	// Ledger is the per-module review ledger (builder 6), present when the
	// plan held review slices (review.scope). Roles then list the whole-run
	// roles only; the slice tasks are in the ledger rows.
	Ledger *ReviewLedger `json:"ledger,omitempty"`
	// DiagnosticTriage is what the triage slots did with the tool
	// diagnostics (builder 22, review_triage.go); present when the plan held
	// them.
	DiagnosticTriage *DiagnosticTriage `json:"diagnostic_triage,omitempty"`
}

// ReviewLedger tells, per module, how much of the eligible code and test
// files was sent to the model and how far its review got. Sent to the model
// is not verified scope: a file the model saw may hold defects no candidate
// named.
type ReviewLedger struct {
	// Scope is the review.scope task with its result (accepted when the
	// report kept its scope artifact).
	Scope AIReviewTask `json:"scope"`
	// MaxCalls and MaxRequestBytes are the attempt's call ceiling and request
	// cap as the scope artifact records them; null without that artifact.
	MaxCalls        *int `json:"max_calls"`
	MaxRequestBytes *int `json:"max_request_bytes"`
	// CallsUsed is the calls before the slices (the scope artifact's count
	// from the attempt counter) plus the calls of the slice records; null
	// when a review task that may have called left no usable record.
	CallsUsed *int `json:"calls_used"`
	// RequestBytesSent is the sum of the recorded request sizes of every
	// review record (prompt bytes); null under the same condition.
	RequestBytesSent *int `json:"request_bytes_sent"`
	// Status is complete, partial or overview_only.
	Status           string         `json:"status"`
	Modules          []LedgerModule `json:"modules"`
	Files            []LedgerFile   `json:"files,omitempty"`
	ExtendedCoverage bool           `json:"extended_coverage,omitempty"`
	// PrimarySlotsBudgetExhausted counts the filled slots of primary files
	// whose investigate the call ceiling refused (builder 8 with a schema 0.2
	// scope artifact): about two calls each would review them.
	PrimarySlotsBudgetExhausted int `json:"primary_slots_budget_exhausted,omitempty"`
}

// LedgerFile records coverage over the snapshot's original line numbers.
type LedgerFile struct {
	Path          string        `json:"path"`
	ModuleID      string        `json:"module_id"`
	TotalLines    int           `json:"total_lines"`
	SentLines     int           `json:"sent_lines"`
	ReviewedLines int           `json:"reviewed_lines"`
	Investigation string        `json:"investigation"`
	Remaining     []LedgerRange `json:"remaining"`
	Reason        string        `json:"reason,omitempty"`
}

type LedgerRange struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Reason    string `json:"reason"`
}

// LedgerModule is one module of the scope artifact. Every eligible file is
// counted once: sent to the model, withheld (secret screen), unreadable,
// budget exhausted (a primary file whose slot was refused), not planned (a
// primary file no slot was left for), secondary not reached (a secondary
// file whose slot was refused or that no slot was left for) or not sent
// (any other reason: the overview did not send it, its slot failed or was
// skipped).
type LedgerModule struct {
	ModuleID   string `json:"module_id"`
	Discovered bool   `json:"discovered"`
	// Class is the module's class (primary, secondary); FilesSecondary and
	// FilesSecondarySent count its secondary files and those sent. The three
	// come with builder 8 from a schema 0.2 scope artifact.
	Class                    string `json:"class,omitempty"`
	FilesSecondary           int    `json:"files_secondary,omitempty"`
	FilesSecondarySent       int    `json:"files_secondary_sent,omitempty"`
	FilesSecondaryNotReached int    `json:"files_secondary_not_reached,omitempty"`
	FilesEligible            int    `json:"files_eligible"`
	FilesRequested           *int   `json:"files_requested,omitempty"`
	FilesAccepted            *int   `json:"files_accepted,omitempty"`
	FilesSentToModel         int    `json:"files_sent_to_model"`
	BytesSent                int    `json:"bytes_sent"`
	FilesTruncated           int    `json:"files_truncated"`
	FilesWithheld            int    `json:"files_withheld"`
	FilesUnreadable          int    `json:"files_unreadable"`
	FilesBudgetExhausted     int    `json:"files_budget_exhausted"`
	FilesNotPlanned          int    `json:"files_not_planned"`
	FilesNotSent             int    `json:"files_not_sent"`
	// FilesWithCompletedReview counts accepted investigation files cited by
	// a candidate's accepted verification. Older ledgers used the verify
	// task state (including a successful task without a model request).
	FilesWithCompletedReview int           `json:"files_with_completed_review"`
	Slices                   []LedgerSlice `json:"slices"`
}

// LedgerSlice is one slice task holding a file of the module.
type LedgerSlice struct {
	TaskKey string  `json:"task_key"`
	Status  string  `json:"status"`
	Reason  *string `json:"reason"`
}

type InterpretationLimit struct {
	ObservationIDs []string `json:"observation_ids"`
	Statement      string   `json:"statement"`
}

// --- 분석 공백 -----------------------------------------------------------------

// Gap is one thing that was not analyzed or only partly: the section, a
// code, what it concerns (task key, module, path, tool) and a detail code.
type Gap struct {
	Section string  `json:"section"`
	Code    string  `json:"code"`
	Subject *string `json:"subject"`
	Detail  *string `json:"detail"`
}
