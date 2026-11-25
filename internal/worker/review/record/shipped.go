package record

import "fmt"

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

// NotShippedMatches reports whether the verifications of a verify record carry
// LimCandidateNotShipped for exactly the candidates whose recorded evidence is
// all on paths that are not shipped code (OnlyNotShipped): the marker is the
// platform's decision from the evidence, so neither a forged marker nor a
// missing one is a valid record.
func NotShippedMatches(candidates []Candidate, verifications []Verification, catalog map[string]Excerpt) bool {
	marked := NotShippedSet(verifications)
	for _, c := range candidates {
		if c.OnlyNotShipped(catalog) != marked[c.ID] {
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
