package review

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A bare alias is a location only in a future inspection clause. Quoted
// symbols, layer names, and ordinary mentions remain as the model wrote them.
var pendingLocationReference = regexp.MustCompile(`(?:^|[.!?][ \t]+)(?:(?:위치|후보)[ \t]+)?(L[1-9][0-9]*)에서[ \t]+(?:구현 (?:여부|경로|분기)|호출 경로|테스트 단언)[^.!?\n]{0,180}(?:확인|검토)(?:할 수 있|해야|할 필요가 있)`)

func resolveContractProbeReason(c *record.ContractComparison, probe record.ContractProbe, navigation []contractNavigationCandidate, in investigateInput) {
	if !probe.Valid(in.Scope.Snapshot, *c) || probe.ContractIndex > len(in.Documents.Contracts) {
		return
	}
	matches := pendingLocationReference.FindAllStringSubmatchIndex(c.Reason, -1)
	if len(matches) == 0 {
		return
	}
	selected := map[string]record.ContractLocation{}
	for i, candidate := range navigation {
		if slices.Contains(probe.Candidates, candidate.location) {
			selected[navigationAlias(i)] = candidate.location
		}
	}
	var resolved strings.Builder
	offset := 0
	for _, match := range matches {
		start, end := match[2], match[3]
		alias := c.Reason[start:end]
		p, ok := selected[alias]
		if !ok || slices.Contains(probe.Literals, alias) || p.Symbol == alias ||
			containsContractLiteral(in.Documents.Contracts[probe.ContractIndex-1].Statement, alias) ||
			slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return record.ScopeUnder(e.Path, c.AppliesTo) && containsContractLiteral(e.Text, alias)
			}) {
			continue
		}
		resolved.WriteString(c.Reason[offset:start])
		fmt.Fprintf(&resolved, "%s:%d–%d", p.Path, p.Start, p.End)
		offset = end
	}
	if offset != 0 {
		resolved.WriteString(c.Reason[offset:])
		if reason := resolved.String(); record.ModelText(reason) && !record.ForeignInsightProse(reason) {
			c.Reason = reason
		}
	}
}
