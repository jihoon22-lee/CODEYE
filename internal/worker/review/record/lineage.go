package record

import "slices"

// CarriesUpstreamEvidence reports whether every excerpt of r whose ID an
// upstream record also holds is one of those upstream entries unchanged.
// An evidence ID is bound to a snapshot location (path, blob and lines),
// so a range a later role derives (an enclosing declaration, an expansion
// window, a narrowed citation) can be exactly an excerpt an earlier role
// recorded under another origin. The later record keeps the earlier entry
// as it was (docs/REVIEW.md#인용). The worker checks its verify record with
// this before writing it and the report builder checks the record again.
func CarriesUpstreamEvidence(r Record, upstream ...Record) bool {
	variants := map[string][]Excerpt{}
	for _, u := range upstream {
		for _, e := range u.Evidence {
			variants[e.ID] = append(variants[e.ID], e)
		}
	}
	for _, e := range r.Evidence {
		if v, ok := variants[e.ID]; ok && !slices.Contains(v, e) {
			return false
		}
	}
	return true
}
