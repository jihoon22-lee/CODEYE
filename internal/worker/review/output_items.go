package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Item-level handling of a role's output (docs/REVIEW.md#출력-오류-분류).
// Before the format repair every fault is named, with its item, field and
// cited excerpt, so the repair can fix it. After the repair (or when none can
// be sent) a fault of form is resolved in its item or the item is left out
// with a limitation; an integrity fault still rejects the output.

// outputFaults gathers the faults of one output's items.
type outputFaults struct {
	codes     []string    // every fault (the call's output_errors)
	issues    []itemIssue // the repair's items
	integrity bool        // a required item has a fault the repair alone may resolve
	lims      []string    // limitations of what the platform resolved
}

// item adds the faults of a required item (a candidate, a note, a
// document item): an integrity fault rejects the whole output.
func (f *outputFaults) item(codes []string, issues []itemIssue, lims []string) {
	f.codes = addLim(f.codes, codes...)
	f.issues = append(f.issues, issues...)
	f.lims = addLim(f.lims, lims...)
	if len(codes) > 0 && !formOnly(codes) {
		f.integrity = true
	}
}

// optional adds the faults of an optional item (a contract comparison):
// after the repair it is left out whatever the fault.
func (f *outputFaults) optional(codes []string, issues []itemIssue, lims []string) {
	f.codes = addLim(f.codes, codes...)
	f.issues = append(f.issues, issues...)
	f.lims = addLim(f.lims, lims...)
}

// output adds a fault of the output as a whole.
func (f *outputFaults) output(code string) {
	f.item([]string{code}, nil, nil)
}

// outcome is parse's answer. Before the repair it names every fault and
// sets repairable when all are of form. After it, the faults of form are
// resolved (degraded lists them) and only an integrity fault rejects.
func (s *roleStage) outcome(f *outputFaults) []string {
	if !s.lastChance {
		s.repairable = len(f.codes) > 0 && !f.integrity
		return f.codes
	}
	if f.integrity {
		return f.codes
	}
	s.degraded = f.codes
	return nil
}

// candidateCheck is one candidate (investigate) or hypothesis (interpret)
// after its check.
type candidateCheck struct {
	text   record.CandidateText
	keep   bool
	codes  []string
	issues []itemIssue
	lims   []string
}

// checkCandidate checks one candidate. more adds the role's own problems
// given the excerpts its citations name. After the repair (final) a
// candidate whose faults are all of form keeps its citations with repeated
// or surplus IDs cut; one with a citation without a usable range, or any
// other fault of form, is left out with the limitation omitted.
func (s *roleStage) checkCandidate(c record.CandidateText, list string, index int, reply *citer, mechanism, final bool, omitted string, more func(base []string) []record.FieldProblem) candidateCheck {
	raw := c.Evidence
	ids, base, citeIssues := reply.resolveIssues(raw, citeNarrow)
	c.Evidence = ids
	problems := append(s.candidateProblems(c, reply.catalog(), mechanism), more(base)...)
	r := candidateCheck{text: c}
	for _, is := range citeIssues {
		r.codes = addLim(r.codes, is.code)
		r.issues = append(r.issues, itemIssue{List: list, Index: index, Field: "evidence_ids", ID: is.id, Error: is.code})
	}
	for _, p := range problems {
		r.codes = addLim(r.codes, p.Code)
		r.issues = append(r.issues, itemIssue{List: list, Index: index, Field: p.Field, Error: p.Code})
	}
	r.keep = len(r.codes) == 0
	if r.keep || !final || !formOnly(r.codes) {
		return r
	}
	ids, lims, errs := reply.degrade(raw, citeNarrow, 6, nil)
	if len(errs) == 0 && !slices.Contains(lims, limCitationUnspecified) {
		c.Evidence = ids
		if len(s.candidateProblems(c, reply.catalog(), mechanism))+len(more(base)) == 0 {
			r.text, r.keep, r.lims = c, true, lims
			return r
		}
	}
	r.lims = []string{omitted}
	return r
}

// candidateProblems are the faults of one candidate's prose and citations
// with the field each concerns; mechanism says whether the role's contract
// asks for one.
func (s *roleStage) candidateProblems(c record.CandidateText, catalog map[string]record.Excerpt, mechanism bool) []record.FieldProblem {
	var out []record.FieldProblem
	add := func(field string, codes ...string) {
		for _, code := range codes {
			out = append(out, record.FieldProblem{Field: field, Code: code})
		}
	}
	for _, f := range []struct{ field, text string }{
		{"title", c.Title}, {"condition", c.Condition}, {"impact", c.Impact}, {"improvement", c.Improvement}, {"counterpoint", c.Counterpoint},
	} {
		add(f.field, s.textErrors(f.text)...)
	}
	switch {
	case mechanism && strings.TrimSpace(c.Mechanism) == "":
		add("mechanism", "mechanism_missing")
	case mechanism:
		add("mechanism", proseErrors(s.limit().mechanism, c.Mechanism)...)
	case c.Mechanism != "":
		add("mechanism", "item_schema_invalid")
	}
	add("evidence_ids", idErrors(c.Evidence, catalog, 6, "code", "test", "document")...)
	code := false
	for _, id := range c.Evidence {
		k := catalog[id].Kind
		code = code || k == "code" || k == "test"
	}
	if !code {
		add("evidence_ids", "code_evidence_missing")
	}
	return out
}

// comparisonCheck is one contract comparison after its check.
type comparisonCheck struct {
	comparison record.ContractComparison
	keep       bool
	codes      []string
	issues     []itemIssue
	lims       []string
}

// citationFormCodes are the comparison codes a resolution of its code
// citations may clear.
var citationFormCodes = []string{record.CompareCodeRangeMissing, record.CompareCodeRangeInvalid, record.CompareCodeDuplicate, record.CompareCodeTooMany}

// checkComparison checks one comparison (index is 1-based). After the
// repair (final) one whose only faults are in the form of its code
// citations is kept with them resolved (the whole sent excerpt, repeated or
// surplus IDs cut) when it then passes every rule; any other invalid
// comparison is left out (contract_comparison_omitted).
func (s *roleStage) checkComparison(x record.ContractComparison, index int, reply *citer, contracts []record.Contract,
	java map[string]javaAPIBinding, rust []rustContractWitness, seen map[int]bool, final bool) comparisonCheck {
	raw := x.CodeEvidenceIDs
	ids, _, citeIssues := reply.resolveIssues(raw, citeNarrow)
	x.CodeEvidenceIDs = ids
	var citeErrs []string
	for _, is := range citeIssues {
		citeErrs = append(citeErrs, is.code)
	}
	problems := s.comparisonProblems(x, citeErrs, reply.catalog(), contracts, java, rust, seen)
	if index > record.MaxContractComparisons {
		problems = append(problems, record.FieldProblem{Field: "contract_comparisons", Code: record.CompareTooMany})
	}
	r := comparisonCheck{comparison: x, keep: len(problems) == 0}
	for _, p := range problems {
		r.codes = addLim(r.codes, p.Code)
		if p.Code == record.CompareCodeRangeMissing || p.Code == record.CompareCodeRangeInvalid {
			for _, is := range citeIssues {
				if is.id != "" && comparisonCitationCode(is.code) == p.Code {
					r.issues = append(r.issues, itemIssue{List: "contract_comparisons", Index: index, Field: p.Field, ID: is.id, Error: p.Code})
				}
			}
			continue
		}
		r.issues = append(r.issues, itemIssue{List: "contract_comparisons", Index: index, Field: p.Field, Error: p.Code})
	}
	if r.keep || !final {
		return r
	}
	if index <= record.MaxContractComparisons && !slices.ContainsFunc(problems, func(p record.FieldProblem) bool { return !slices.Contains(citationFormCodes, p.Code) }) {
		ids, lims, errs := reply.degrade(raw, citeNarrow, record.MaxComparisonCodeEvidence, nil)
		y := x
		y.CodeEvidenceIDs = ids
		if len(errs) == 0 && len(s.comparisonProblems(y, nil, reply.catalog(), contracts, java, rust, seen)) == 0 {
			r.comparison, r.keep, r.lims = y, true, lims
			return r
		}
	}
	r.lims = []string{record.LimContractComparisonOmitted}
	return r
}

// comparisonCitationCode is the comparison code of a citation fault.
func comparisonCitationCode(code string) string {
	if code == errCitationMissing {
		return record.CompareCodeRangeMissing
	}
	return record.CompareCodeRangeInvalid
}

// tidyIDs removes repeated IDs from a citation list and keeps its first
// limit IDs.
func tidyIDs(ids []string, limit int) []string {
	out := []string{}
	for _, id := range ids {
		if !slices.Contains(out, id) && len(out) < limit {
			out = append(out, id)
		}
	}
	return out
}
