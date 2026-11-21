package report

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/eslint"
)

// At an overlapping call, one finding represents two accepted observations.
// Keep both observations and their raw references for task counts and model lineage.
func deduplicateManagedJSRejections(r *Report, states map[string]*taskState) {
	type position struct {
		path      string
		line, col int
	}
	accepted := func(o Observation) (*taskState, bool) {
		s := states[o.TaskKey]
		ok := s != nil && s.decoded && !s.rejected && s.tool != nil && s.facts.Status == string(domain.TaskSucceeded) &&
			!o.Suppressed && o.EvidenceState == EvidenceVerified && o.Location != nil &&
			o.Location.ColumnUnit == "byte" && o.Location.StartCol > 0
		return s, ok
	}
	core := map[position]bool{}
	for _, o := range r.Observations {
		if o.Tool == eslint.AnalyzerID && isPromiseRejectDiagnostic(o.Tool, o.RuleID) {
			if _, ok := accepted(o); ok {
				core[position{o.Location.Path, o.Location.StartLine, o.Location.StartCol}] = true
			}
		}
	}
	if len(core) == 0 {
		return
	}
	duplicates := map[string]bool{}
	for _, o := range r.Observations {
		if o.Tool != eslint.TypedAnalyzerID || !isPromiseRejectDiagnostic(o.Tool, o.RuleID) {
			continue
		}
		s, ok := accepted(o)
		if ok && s.tool.RuleContext != nil && s.tool.RuleContext.RulePack == eslint.ManagedJSRulePackID &&
			core[position{o.Location.Path, o.Location.StartLine, o.Location.StartCol}] {
			duplicates[o.ID] = true
		}
	}
	r.Findings = slices.DeleteFunc(r.Findings, func(f Finding) bool {
		return f.FingerprintBasis != nil && f.FingerprintBasis.Producer == ProducerTool &&
			slices.ContainsFunc(f.ObservationIDs, func(id string) bool { return duplicates[id] })
	})
}
