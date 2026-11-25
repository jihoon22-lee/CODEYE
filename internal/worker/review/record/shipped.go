package record

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
