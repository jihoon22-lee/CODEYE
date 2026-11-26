package domain

// Stage is the user-facing pipeline stage a planned task belongs to
// (docs/ARCHITECTURE.md#표시-단계와-task-단계). The worker sets it at plan time; the server validates and
// stores it once. Tasks recorded without a stage have none and are never
// assigned one by inference.
type Stage string

const (
	StageEnvironment    Stage = "environment"
	StageDocuments      Stage = "documents"
	StageStaticAnalysis Stage = "static_analysis"
	StageCodeReview     Stage = "code_review"
	StageBuild          Stage = "build"
	StageTest           Stage = "test"
)

// TaskStages lists the task stage keys in reading order. The display also has
// server-derived "source" (before) and "report" (after) stages that no task
// carries.
var TaskStages = []Stage{StageEnvironment, StageDocuments, StageBuild, StageTest, StageStaticAnalysis, StageCodeReview}

// Valid reports whether s is a known task stage. The empty stage is not valid;
// callers that accept legacy records must check for "" separately.
func (s Stage) Valid() bool {
	switch s {
	case StageEnvironment, StageDocuments, StageStaticAnalysis, StageCodeReview, StageBuild, StageTest:
		return true
	}
	return false
}

// Controlled skip reasons for a planned role that had nothing to review
// (docs/ARCHITECTURE.md#표시-단계와-task-단계). They are stored in the task reason with status skipped_policy
// and are never counted as a successful model review.
const (
	ReasonModelSourceNotApproved = "model_source_not_approved"
	// ReasonModelReviewNotOffered: the project chose the model review but the
	// service administrator does not offer it to runs other users start.
	ReasonModelReviewNotOffered = "model_review_not_offered"
	ReasonNoDocuments           = "no_documents"
	ReasonNoToolObservations    = "no_tool_observations"
	ReasonNoEnvironmentGap      = "no_environment_gap"
	// ReasonNoReviewScope: a review slice whose planned files the overview
	// already sent, or that are all withheld or unreadable.
	ReasonNoReviewScope = "no_review_scope"
	// ReasonNoLeads: a lead follow-up whose investigation named no lead
	// (docs/REVIEW.md#단서-후속-조사); ReasonNoLeadsResolved: one whose
	// leads the platform found none of (each lead keeps its reason).
	ReasonNoLeads         = "no_leads"
	ReasonNoLeadsResolved = "no_leads_resolved"
)

// Controlled skip reasons for a planned dependency task that had nothing to
// fetch or nothing that would use what it fetched
// (docs/ARCHITECTURE.md#의존성-준비). Stored with status skipped_policy.
const (
	ReasonNoDependencies                = "no_dependencies"
	ReasonVendoredDependencies          = "vendored_dependencies"
	ReasonNoDependencyConsumer          = "no_dependency_consumer"
	ReasonDependencyConsumersNotAllowed = "dependency_consumers_not_allowed"
)

// NothingToFetch reports whether reason marks a dependency task skipped
// because there was nothing to fetch or nothing would use it.
func NothingToFetch(reason string) bool {
	switch reason {
	case ReasonNoDependencies, ReasonVendoredDependencies, ReasonNoDependencyConsumer, ReasonDependencyConsumersNotAllowed:
		return true
	}
	return false
}

// NothingToDo reports whether a skipped_policy task with this reason had no
// input (a review role with nothing to review, a dependency task with
// nothing to fetch). Such a skip does not make an otherwise successful stage
// partial.
func NothingToDo(reason string) bool { return NothingToReview(reason) || NothingToFetch(reason) }

// NothingToReview reports whether reason marks a planned role skipped because
// its input was empty (docs/ARCHITECTURE.md#표시-단계와-task-단계). Such a skip is never a successful model
// review, but it does not make an otherwise successful stage partial. Policy
// exclusion and unreadable inputs are not in this set.
func NothingToReview(reason string) bool {
	switch reason {
	case ReasonNoDocuments, ReasonNoToolObservations, ReasonNoEnvironmentGap, ReasonNoReviewScope, ReasonNoLeads, ReasonNoLeadsResolved:
		return true
	}
	return false
}
