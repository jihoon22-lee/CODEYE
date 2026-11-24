package review

import "github.com/jihoon22-lee/CODEYE/internal/worker/review/record"

// Output error classes (docs/REVIEW.md#출력-오류-분류). Every code the
// output handling of a role emits is registered here; an unregistered code
// counts as integrity.
//
// integrity: the output states something the platform cannot accept: an ID
// or observation the request did not hold, lines it did not send, a claim of
// execution, a widened scope, a severity only the platform assigns, a
// verdict without the evidence its rule needs, an unparseable result. These
// keep their handling: the response is rejected (verify: the candidate is
// quarantined; an optional item such as a strength or a contract comparison
// is left out).
//
// form: the cited evidence is real and was sent, or an item fails its own
// rule. Once the one format repair is spent (or cannot be sent) the platform
// resolves the fault in the item or leaves the item out, records a
// limitation and keeps the stage.
type codeClass string

const (
	classIntegrity codeClass = "integrity"
	classForm      codeClass = "form"
)

// Limitations of items the platform left out after the repair.
const (
	limCandidateOmitted      = record.LimCandidateOmitted
	limDocumentItemsOmitted  = record.LimDocumentItemsOmitted
	limInterpretItemsOmitted = record.LimInterpretItemsOmitted
	limEnvironmentNotesOmit  = record.LimEnvironmentNotesOmitted
	limVerificationTrimmed   = "verification_list_trimmed"
)

// outputCodes maps each output error code to its class.
var outputCodes = map[string]codeClass{
	// Whole output.
	"output_not_json":       classIntegrity,
	"output_schema_invalid": classIntegrity,
	"item_schema_invalid":   classIntegrity,
	"no_items":              classIntegrity,
	"too_many_items":        classForm,
	// Citations and evidence lists.
	errCitationOutside:       classIntegrity,
	errCitationMissing:       classForm,
	errCitationInvalid:       classForm,
	"evidence_id_unknown":    classIntegrity,
	"evidence_kind_invalid":  classIntegrity,
	"evidence_id_duplicate":  classForm,
	"evidence_list_too_long": classForm,
	"evidence_missing":       classForm,
	"code_evidence_missing":  classForm,
	// Prose.
	record.ErrExecutionClaim:      classIntegrity,
	"text_invalid":                classForm,
	"mechanism_missing":           classForm,
	record.InsightLanguageError:   classForm,
	"evidence_not_at_observation": classIntegrity,
	// Interpret observations.
	"observation_id_unknown":   classIntegrity,
	"observation_id_duplicate": classForm,
	"observation_ids_invalid":  classForm,
	// Environment notes.
	"module_out_of_scope":           classIntegrity,
	"requirement_id_unknown":        classIntegrity,
	"requirement_of_other_module":   classIntegrity,
	"evidence_of_other_requirement": classIntegrity,
	"module_duplicate":              classForm,
	"requirement_id_duplicate":      classForm,
	"requirement_missing":           classForm,
	// Documents.
	"scope_invalid":                          classIntegrity,
	"scope_unknown_path":                     classIntegrity,
	"scope_generalized":                      classIntegrity,
	"conflict_same_document":                 classForm,
	"document_quality_invalid":               classForm,
	"contracts_evidence_ids_invalid":         classForm,
	"conflicts_first_evidence_ids_invalid":   classForm,
	"conflicts_second_evidence_ids_invalid":  classForm,
	"questions_evidence_ids_invalid":         classForm,
	"instruction_notes_evidence_ids_invalid": classForm,
	"document_quality_evidence_ids_invalid":  classForm,
	// Verify items.
	"unknown_candidate_id":             classIntegrity,
	"duplicate_candidate_verification": classIntegrity,
	"verification_missing":             classIntegrity,
	"verdict_invalid":                  classIntegrity,
	"category_invalid":                 classIntegrity,
	"severity_invalid":                 classIntegrity,
	"severity_not_assignable":          classIntegrity,
	"supported_without_code_evidence":  classIntegrity,
	"refuted_without_counterevidence":  classIntegrity,
	"valid_conditions_missing":         classIntegrity,
	"invalid_conditions_missing":       classIntegrity,
	"unresolved_assumptions_missing":   classIntegrity,
	"verification_invalid":             classIntegrity,
	"list_too_long":                    classForm,
	"next_verification_invalid":        classForm,
	// An accepted verdict whose observation IDs among its evidence were
	// left out (the platform resolved it).
	"evidence_id_dropped": classForm,
	// The optional route selection call of the documents role: a selection
	// it cannot bind is left out (workflow_selection_invalid), never repaired.
	record.LimWorkflowSelectionInvalid: classIntegrity,
	// Contract comparisons of the overview investigation: optional items,
	// left out after the repair whatever the class.
	record.CompareIndexInvalid:     classIntegrity,
	record.CompareIndexDuplicate:   classForm,
	record.CompareScopeInvalid:     classIntegrity,
	record.CompareScopeOutside:     classIntegrity,
	record.CompareStatusInvalid:    classIntegrity,
	record.CompareReasonInvalid:    classForm,
	record.CompareDocumentsChanged: classIntegrity,
	record.CompareCodeMissing:      classForm,
	record.CompareCodeInvalid:      classIntegrity,
	record.CompareCodeDuplicate:    classForm,
	record.CompareCodeTooMany:      classForm,
	record.CompareCodeOutsideScope: classIntegrity,
	record.CompareCodeIsDocument:   classForm,
	record.CompareCodeOutsideAPI:   classForm,
	record.CompareCodeRangeInvalid: classForm,
	record.CompareCodeRangeMissing: classForm,
	record.CompareInSlice:          classIntegrity,
	record.CompareTooMany:          classForm,
	// Contract follow-up.
	"contract_comparison_invalid":      classIntegrity,
	"contract_evidence_not_new":        classIntegrity,
	"contract_partial_support_invalid": classIntegrity,
}

// integrityCode reports a code that keeps rejecting after the repair; an
// unregistered code counts as one.
func integrityCode(code string) bool {
	class, ok := outputCodes[code]
	return !ok || class == classIntegrity
}

// formOnly reports a non-empty list of codes that are all of form.
func formOnly(codes []string) bool {
	for _, c := range codes {
		if integrityCode(c) {
			return false
		}
	}
	return len(codes) > 0
}
