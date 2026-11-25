package record

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// OnlyNotShipped reports an investigation candidate about material that is
// not shipped code: it cites code or test lines, and every one of them lies
// on a test, example, benchmark, generated or vendored path (PathClass). A
// candidate that cites shipped code too is about shipped code (a test may
// only show the behaviour it expects). The verify stage does not send such a
// candidate (LimCandidateNotShipped) and the report does not list it among
// the findings; only candidates of the investigation roles are judged by
// this rule (tool diagnostics keep their own path rules).
func (c Candidate) OnlyNotShipped(catalog map[string]Excerpt) bool {
	if c.Origin != StageInvestigate {
		return false
	}
	cited := false
	for _, id := range c.Evidence {
		e, ok := catalog[id]
		if !ok || (e.Kind != "code" && e.Kind != "test") {
			continue
		}
		if PathClass(e.Path) == "" {
			return false
		}
		cited = true
	}
	return cited
}

// HypothesisNotShipped reports an interpretation hypothesis about material
// that is not shipped code: every tool diagnostic it relates to is located on
// a test, example, benchmark, generated or vendored path (PathClass of the
// path observationPath gives; a diagnostic without a location or an unknown
// one is not on such a path), and every code or test excerpt it cites is on
// one too. A hypothesis that relates to a diagnostic of shipped code, or
// cites shipped code, is about shipped code. Only the hypothesis's own
// observations and evidence count: the excerpts a verification adds (callers,
// supporting and counter evidence) do not change what the hypothesis is about.
// The verification of such a hypothesis is not skipped (its verdict is
// information, and the call is made with the others); the report lists it
// with the items of secondary paths and does not count it as an action item
// (docs/REPORT.md#조치-항목-우선-배치). The caller applies the decision only
// when the scope holds shipped code (ShippedSourceIn).
func (c Candidate) HypothesisNotShipped(observationPath func(id string) (string, bool), catalog map[string]Excerpt) bool {
	if c.Origin != StageInterpret || len(c.ObservationIDs) == 0 {
		return false
	}
	for _, id := range c.ObservationIDs {
		if p, ok := observationPath(id); !ok || PathClass(p) == "" {
			return false
		}
	}
	for _, id := range c.Evidence {
		e, ok := catalog[id]
		if ok && (e.Kind == "code" || e.Kind == "test") && PathClass(e.Path) == "" {
			return false
		}
	}
	return true
}

// PackSetsAside reports a record written by an instruction pack that sets
// candidates aside (2.10 and later): the earlier packs verified every
// candidate, and their records stay readable as they were.
func PackSetsAside(version string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(version, "%d.%d.", &major, &minor); err != nil {
		return false
	}
	return major > 2 || major == 2 && minor >= 10
}

// ShippedSourceIn reports whether the reviewable scope holds at least one
// source file that is shipped code (by PathClass). It is a pure function of
// the snapshot's included file paths, the run scope and, for a pull request,
// the changed files the review is judged by, so the worker, the record
// validation and the report builder compute the same value. When it is false
// the non-shipped code is the subject of the review (an examples-only
// repository, a pull request that changes only tests): nothing is set aside
// and the requests say so.
func ShippedSourceIn(paths []string, scope domain.Scope, pullRequest bool, changed []string) bool {
	if scope.Kind == "" {
		scope = domain.Scope{Kind: "full"}
	}
	for _, p := range paths {
		if pullRequest && !slices.Contains(changed, p) {
			continue
		}
		if SourceKind(p) == "code" && PathClass(p) == "" && scopeHas(scope, p) {
			return true
		}
	}
	return false
}

// scopeHas reports whether a snapshot path lies in the run scope.
func scopeHas(scope domain.Scope, name string) bool {
	switch scope.Kind {
	case "full":
		return true
	case "paths":
		for _, prefix := range scope.Paths {
			if name == prefix || strings.HasPrefix(name, prefix+"/") {
				return true
			}
		}
	}
	return false
}

// SubjectNonShipped is the value of the request's scope.subject, and of the
// report's code_review.subject, when the reviewable scope holds no shipped
// code (ShippedSourceIn is false). It is absent otherwise.
const SubjectNonShipped = "non_shipped"

// NotShippedMatches reports whether the verifications of a verify record carry
// LimCandidateNotShipped for exactly the candidates whose recorded evidence is
// all on paths that are not shipped code (OnlyNotShipped), unless the scope
// holds no shipped code (shipped false), when none is marked: the marker is
// the platform's decision from the evidence and the scope, so neither a forged
// marker nor a missing one is a valid record.
func NotShippedMatches(candidates []Candidate, verifications []Verification, catalog map[string]Excerpt, shipped bool) bool {
	marked := NotShippedSet(verifications)
	for _, c := range candidates {
		if (shipped && c.OnlyNotShipped(catalog)) != marked[c.ID] {
			return false
		}
	}
	return true
}

// NotShippedSet is the set of candidates whose verification is the platform's
// decision not to send them (an unreviewed verification whose only
// limitation is LimCandidateNotShipped), by candidate ID.
func NotShippedSet(list []Verification) map[string]bool {
	out := map[string]bool{}
	for _, v := range list {
		if v.Verdict == VerdictUnreviewed && len(v.Limitations) == 1 && v.Limitations[0] == LimCandidateNotShipped {
			out[v.CandidateID] = true
		}
	}
	return out
}
