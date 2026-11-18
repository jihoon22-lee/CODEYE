package domain

// Stage is the user-facing pipeline stage a planned task belongs to
// (docs/37 §2). The worker sets it at plan time; the server validates and
// stores it once. Tasks recorded before W15 have no stage and are never
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
// (docs/37 §2). They are stored in the task reason with status skipped_policy
// and are never counted as a successful model review.
const (
	ReasonModelSourceNotApproved = "model_source_not_approved"
	ReasonNoDocuments            = "no_documents"
	ReasonNoToolObservations     = "no_tool_observations"
	ReasonNoEnvironmentGap       = "no_environment_gap"
)
