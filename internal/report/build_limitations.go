package report

import (
	"sort"
)

// Limitation and note codes. Renderers translate known codes; JSON keeps codes.
const (
	LimAIReviewNotPerformed   = "ai_review_not_performed"
	LimAIReviewPartial        = "ai_review_partial"
	LimResultManifestMissing  = "result_manifest_unavailable"
	LimResultManifestRejected = "result_manifest_rejected"
	LimRequiredTaskIncomplete = "required_task_not_succeeded"
	LimCleanupUncertain       = "cleanup_uncertain"
	LimArtifactRejected       = "artifact_rejected"
	LimArtifactMissing        = "analysis_artifact_missing"
	LimUnrecognizedArtifact   = "unrecognized_artifact"
	LimNoCodeExcerpts         = "controller_holds_no_snapshot_bytes"
	LimTaskNotInManifest      = "task_not_in_result_manifest"
	LimTaskStatusMismatch     = "artifact_status_differs_from_accepted_task"
	LimRunCancelled           = "run_cancelled"
	LimRunFailed              = "run_failed"
	LimTestsNotPerformed      = "required_tests_not_performed"
	// LimStaticAnalysisGap (":<ecosystem>"): source of a language ecosystem
	// was in scope and the worker had no analyzer for it (a pack-gap task,
	// docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백). The report is partial; the gap is not a required task.
	LimStaticAnalysisGap = "static_analysis_gap"
	// LimStaticAnalysisNotPlanned: the run planned no analyzer task besides
	// the secrets task (no supported language source in scope). The report is
	// partial.
	LimStaticAnalysisNotPlanned = "static_analysis_not_planned"
	NoteInventoryAllLanguages   = "inventory_counts_all_included_files"
	NoteToolFilesUnknown        = "tool_processed_files_unknown"
	NoteNoTestsPlanned          = "tests_not_planned"
	NoteToolZeroIsNotClean      = "tool_reported_zero_is_not_clean"
)

type stringSet struct {
	m map[string]bool
}

func newSet() *stringSet               { return &stringSet{m: map[string]bool{}} }
func (s *stringSet) add(v string)      { s.m[v] = true }
func (s *stringSet) has(v string) bool { return s.m[v] }
func (s *stringSet) list() []string {
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
