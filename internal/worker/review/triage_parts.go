package review

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A triage answer that classifies part of a group (docs/REVIEW.md#도구-진단-분류).
//
// A group is the diagnostics one rule of one tool reports in one unit. They
// usually agree, but one real defect and one harmless diagnostic of the same
// rule can share a unit, and one classification for both would publish the
// harmless one as part of the problem. An entry of the answer may therefore
// list the 1-based positions of the diagnostics it classifies (the position
// in the group's lines, which the request numbers in its examples), and the
// group is answered by one entry per part.
//
// The platform validates the parts (positions in range, none listed twice,
// at most triageStoredPerGroup stored groups for one sent group) and stores
// each part as a group of its own in the review record: its own observations,
// the same context, one candidate per problem. The record keeps its shape,
// every observation stays in exactly one group and the report counts each
// diagnostic by its own classification. A position no part lists becomes a
// stored group without classification (triage_answer_missing).

// triageStoredPerGroup bounds the stored groups of one sent group (its parts
// and the positions no part lists): a request holds at most triageGroupLimit
// groups and a record at most record.MaxTriageGroups.
func triageStoredPerGroup(b Budget) int {
	return min(3, record.MaxTriageGroups/triageGroupLimit(b))
}

// triagePart is the outcome of one part of a group.
type triagePart struct {
	out   triageOutcome
	diags []int // 0-based positions in the group's diagnostics
}

// triageEntry is one entry of an answer with its 1-based position.
type triageEntry struct {
	a     triageAnswer
	index int
}

// readEntries picks the entries of one group the platform reads and the
// entries that repeat or overlap them. A whole-group entry is read alone
// when it comes first; otherwise the entries that list diagnostics are the
// parts and a whole-group entry among them is a repeat.
func readEntries(es []triageEntry) (read, repeated []triageEntry, parts bool) {
	if len(es[0].a.Diagnostics) == 0 {
		return es[:1], es[1:], false
	}
	for _, e := range es {
		if len(e.a.Diagnostics) == 0 {
			repeated = append(repeated, e)
		} else {
			read = append(read, e)
		}
	}
	return read, repeated, true
}

// partPositions validates the parts of a group of count diagnostics: every
// position in 1..count and listed once. It returns each part's 0-based
// positions in ascending order and the positions no part lists. ok is false
// for a position out of range or repeated, or when the stored groups would
// exceed limit.
func partPositions(parts []triageEntry, count, limit int) (positions [][]int, uncovered []int, ok bool) {
	listed := make([]bool, count)
	for _, e := range parts {
		var own []int
		for _, p := range e.a.Diagnostics {
			if p < 1 || p > count || listed[p-1] {
				return nil, nil, false
			}
			listed[p-1] = true
			own = append(own, p-1)
		}
		slices.Sort(own)
		positions = append(positions, own)
	}
	for i, l := range listed {
		if !l {
			uncovered = append(uncovered, i)
		}
	}
	stored := len(parts)
	if len(uncovered) > 0 {
		stored++
	}
	return positions, uncovered, stored <= limit
}

// checkTriageEntry checks one entry (a whole group or one part) of an answer
// for group g. defs and extra serve the unclear answers (triage_unclear.go):
// extra holds the excerpts a repair added to a group's context.
func (s *roleStage) checkTriageEntry(a triageAnswer, g triageSent, index int, reply *citer, final bool, defs *definitionIndex, extra map[string][]string) (triageOutcome, []string, []itemIssue) {
	gg := g
	gg.context = append(slices.Clone(g.context), extra[g.id]...)
	o, codes, issues := s.checkTriageAnswer(a, gg, index, reply, final)
	if a.Classification != record.TriageUnclear {
		return o, codes, issues
	}
	valid, fault := s.checkMissing(a.Missing)
	if fault {
		codes = addLim(codes, errTriageMissingInvalid)
		issues = append(issues, itemIssue{List: "triage", Index: index, Field: "missing", Error: errTriageMissingInvalid})
	}
	if id := defs.presentDefinition(g, valid); id != "" {
		codes = addLim(codes, errTriageUnclearPresent)
		issues = append(issues, itemIssue{List: "triage", Index: index, Field: "missing", ID: id, Error: errTriageUnclearPresent})
		extra[g.id] = addLim(extra[g.id], id)
		if final && o.omitted == "" {
			// The answer contradicts the request: no resolution inside the item.
			o.omitted = record.TriageItemOmitted
		}
	}
	if final && o.omitted == "" && fault && len(valid) == 0 {
		// The answer stands, without a list of what is missing.
		o.lims = addLim(o.lims, limTriageMissingUnstated)
	}
	return o, codes, issues
}

// checkParts checks the parts of a group answered part by part. Faults of the
// parts are the group's faults (the repair asks for the whole group again);
// once the repair is spent each part is resolved or left out on its own.
func (s *roleStage) checkParts(read []triageEntry, g triageSent, reply *citer, final bool, defs *definitionIndex, extra map[string][]string, issues []itemIssue) (triageOutcome, []string, []itemIssue) {
	positions, uncovered, ok := partPositions(read, g.input.Count, triageStoredPerGroup(s.h.Budget))
	if !ok {
		var o triageOutcome
		if final {
			o.omitted = record.TriageItemOmitted
		}
		return o, []string{errTriageDiagnostics}, append(issues, itemIssue{List: "triage", Index: read[0].index, Field: "diagnostics", Error: errTriageDiagnostics})
	}
	var out triageOutcome
	var codes []string
	omitted := ""
	allOmitted := true
	for i, e := range read {
		po, pc, pi := s.checkTriageEntry(e.a, g, e.index, reply, final, defs, extra)
		codes = addLim(codes, pc...)
		issues = append(issues, pi...)
		out.parts = append(out.parts, triagePart{out: po, diags: positions[i]})
		if po.omitted == "" {
			allOmitted = false
		} else if omitted == "" {
			omitted = po.omitted
		}
	}
	out.uncovered = uncovered
	if allOmitted {
		out.omitted = omitted
	}
	return out, codes, issues
}
