package review

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Contract comparisons of the overview investigation
// (docs/REVIEW.md#문서-계약-대조). A comparison's code evidence is the cited
// range itself, narrowed like a candidate's, so the rules that an
// implementation is not the quoted doc comment and lies in the documented
// API's body are checked on the lines the model named, not on the whole
// file or declaration that carried them.

// comparisonProblems lists every rejection of one comparison with the field
// it concerns. citeErrs are the citation codes of its code evidence; seen
// are the contract indices already compared by an accepted comparison.
func (s *roleStage) comparisonProblems(x record.ContractComparison, citeErrs []string, catalog map[string]record.Excerpt, contracts []record.Contract,
	java map[string]javaAPIBinding, rust []rustContractWitness, seen map[int]bool) []record.FieldProblem {
	var out []record.FieldProblem
	add := func(field, code string) {
		if p := (record.FieldProblem{Field: field, Code: code}); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if s.slice != nil {
		add("contract_comparisons", record.CompareInSlice)
		return out
	}
	for _, code := range citeErrs {
		if code == errCitationMissing {
			add("code_evidence_ids", record.CompareCodeRangeMissing)
		} else {
			add("code_evidence_ids", record.CompareCodeRangeInvalid)
		}
	}
	if x.ContractIndex < 1 || x.ContractIndex > len(contracts) {
		add("contract_index", record.CompareIndexInvalid)
	} else {
		if seen[x.ContractIndex] {
			add("contract_index", record.CompareIndexDuplicate)
		}
		contract := contracts[x.ContractIndex-1]
		if !slices.Equal(x.DocumentEvidenceIDs, contract.EvidenceIDs) {
			add("document_evidence_ids", record.CompareDocumentsChanged)
		}
		if record.ValidScopePath(x.AppliesTo) && !record.ScopeUnder(x.AppliesTo, contract.AppliesTo) {
			add("applies_to", record.CompareScopeOutside)
		}
	}
	if record.ValidScopePath(x.AppliesTo) && !s.existingDir(x.AppliesTo) {
		add("applies_to", record.CompareScopeInvalid)
	}
	for _, p := range x.Problems(catalog, s.limit().text) {
		add(p.Field, p.Code)
	}
	if len(out) == 0 && (!javaAPIComparisonValid(x, java, catalog) || !rustAPIComparisonValid(x, rust, catalog)) {
		add("code_evidence_ids", record.CompareCodeOutsideAPI)
	}
	return out
}

// fitComparisonRepair adds the rejected comparisons to a repair request, as
// many as the request budget leaves room for.
func (s *roleStage) fitComparisonRepair(in investigateInput, repair *outputRepair, issues []comparisonIssue, instructions string) ([]byte, error) {
	in.Repair = repair
	if repair == nil || len(issues) == 0 {
		return marshal(in)
	}
	for n := len(issues); n > 0; n-- {
		in.Repair = &outputRepair{OutputErrors: repair.OutputErrors, ContractComparisons: issues[:n]}
		body, err := marshal(in)
		if err != nil {
			return nil, err
		}
		if _, ok := s.fits(instructions, body, false); ok {
			return body, nil
		}
	}
	in.Repair = repair
	return marshal(in)
}
