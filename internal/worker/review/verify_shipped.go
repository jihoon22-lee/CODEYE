package review

import (
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Candidates about material that is not shipped code (docs/REVIEW.md#검토-대상은-배포-코드다).
//
// The investigation looks for defects of shipped code; tests, examples,
// benchmarks, generated and vendored code travel as context and as
// supporting evidence. A candidate whose code and test evidence all lies on
// such paths (record.Candidate.OnlyNotShipped) is not about shipped code,
// and it must not take a verification call or appear among the action
// items. The platform decides it from the structured evidence, with no
// repair call (the call ceiling belongs to the module slices): the candidate
// stays in the verify record, unreviewed with the fixed limitation
// candidate_not_shipped, is not sent, and the report counts it instead of
// listing it. A candidate that also cites shipped code is untouched, so a
// test that shows an expected behaviour or a "the tests do not cover X"
// observation (which cites X) keeps working. The decision reads the
// recorded evidence alone (the report recomputes it the same way), so a
// repository whose code is all under examples/ has its candidates listed as
// not verified, visibly, and nothing is hidden.

// notShippedCandidates are the investigation candidates the verify stage
// does not send.
func (s *roleStage) notShippedCandidates(candidates []record.Candidate, evidence []record.Excerpt) map[string]bool {
	var out map[string]bool
	catalog := catalogOf(evidence)
	for _, c := range candidates {
		if c.OnlyNotShipped(catalog) {
			if out == nil {
				out = map[string]bool{}
			}
			out[c.ID] = true
		}
	}
	return out
}
