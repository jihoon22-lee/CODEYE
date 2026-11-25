package review

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A triage answer that classifies part of a group (docs/REVIEW.md#도구-진단-분류).
//
// A group is the diagnostics one rule of one tool reports in one unit. They
// usually agree, but one real defect and one harmless diagnostic of the same
// rule can share a unit, and one classification for both would publish the
// harmless one as part of the problem. An entry of the answer may therefore
// list in positions the 1-based positions of the diagnostics it classifies
// (the position in the group's lines, which the request numbers in its
// examples as index), and the group is answered by one entry per part.
//
// The platform stores each part as a group of its own in the review record:
// its own observations, the same context, one candidate per problem. The
// record keeps its shape, every observation stays in exactly one group and
// the report counts each diagnostic by its own classification. A model may
// give at most triagePartsLimit parts for a group; the positions no part
// lists are not triaged (a stored group without classification,
// triage_answer_missing). The stored groups of a sent group (its parts and
// that remainder) never exceed what a record holds.
//
// The optional fields of an entry (positions, missing) are read leniently: an
// entry whose field has the wrong type is a fault of form of that entry
// (triage_diagnostics_invalid, triage_missing_invalid), never a schema failure
// of the whole output.

// triagePartsLimit is the parts a model may give for one group of a request
// of groups sent groups: with the remainder of the positions no part lists,
// the stored groups of all sent groups stay within record.MaxTriageGroups.
func triagePartsLimit(groups int) int {
	return max(1, min(4, record.MaxTriageGroups/max(1, groups)-1))
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

// emptyJSON reports an absent, null or empty-list optional field.
func emptyJSON(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null")) || bytes.Equal(t, []byte("[]"))
}

// positions reads the positions of an entry: ok is false for a field of the
// wrong type (or a position that is not an integer).
func (a triageAnswer) positions() (list []int, ok bool) {
	if emptyJSON(a.Positions) {
		return nil, true
	}
	if json.Unmarshal(a.Positions, &list) != nil {
		return nil, false
	}
	return list, true
}

// hasPositions reports an entry that names part of a group (a field of the
// wrong type counts: it is a part answer with a fault).
func (a triageAnswer) hasPositions() bool { return !emptyJSON(a.Positions) }

// readEntries picks the entries of one group the platform reads and the
// entries that repeat or overlap them. A whole-group entry is read alone
// when it comes first; otherwise the entries that give positions are the
// parts and a whole-group entry among them is a repeat.
func readEntries(es []triageEntry) (read, repeated []triageEntry, parts bool) {
	if !es[0].a.hasPositions() {
		return es[:1], es[1:], false
	}
	for _, e := range es {
		if !e.a.hasPositions() {
			repeated = append(repeated, e)
		} else {
			read = append(read, e)
		}
	}
	return read, repeated, true
}

// validParts picks the parts that stand: at most limit, each with positions
// that are in 1..count and listed by no earlier part. ok is false when any
// part had to be dropped. kept holds the 0-based positions of each kept part
// and uncovered the positions no kept part lists.
func validParts(parts []triageEntry, count, limit int) (kept []triageEntry, positions [][]int, uncovered []int, ok bool) {
	ok = true
	listed := make([]bool, count)
	for _, e := range parts {
		list, typed := e.a.positions()
		valid := typed && len(list) > 0 && len(kept) < limit
		var own []int
		seen := map[int]bool{}
		for _, p := range list {
			if p < 1 || p > count || listed[p-1] || seen[p] {
				valid = false
				break
			}
			seen[p] = true
			own = append(own, p-1)
		}
		if !valid {
			ok = false
			continue
		}
		for _, p := range own {
			listed[p] = true
		}
		slices.Sort(own)
		kept, positions = append(kept, e), append(positions, own)
	}
	for i, l := range listed {
		if !l {
			uncovered = append(uncovered, i)
		}
	}
	return kept, positions, uncovered, ok
}

// checkTriageEntry checks one entry (a whole group or one part) of an answer
// for group g. defs and pointed serve the unclear answers (triage_unclear.go):
// pointed holds the excerpts a repair added to a group's context.
func (s *roleStage) checkTriageEntry(a triageAnswer, g triageSent, index int, reply *citer, final bool, defs *definitionIndex, pointed map[string][]string) (triageOutcome, []string, []itemIssue) {
	gg := g
	gg.context = append(slices.Clone(g.context), pointed[g.id]...)
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
		pointed[g.id] = addLim(pointed[g.id], id)
		if final && o.omitted == "" {
			// The answer stands as the model's: the guard is exact only for
			// the declarations it can see, and a form fault never fails a
			// stage. The record says the platform found the definition.
			o.lims = addLim(o.lims, limTriageUnclearDefinitionPresent)
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
// once the repair is spent the parts that are valid stand, each resolved or
// left out on its own, and the positions of no kept part are not triaged.
func (s *roleStage) checkParts(read []triageEntry, g triageSent, reply *citer, final bool, defs *definitionIndex, pointed map[string][]string, issues []itemIssue, partsLimit int) (triageOutcome, []string, []itemIssue) {
	kept, positions, uncovered, ok := validParts(read, g.input.Count, partsLimit)
	var codes []string
	if !ok {
		codes = []string{errTriageDiagnostics}
		issues = append(issues, itemIssue{List: "triage", Index: read[0].index, Field: "positions", Error: errTriageDiagnostics})
		if !final {
			return triageOutcome{}, codes, issues
		}
		if len(kept) == 0 {
			return triageOutcome{omitted: record.TriageItemOmitted}, codes, issues
		}
	}
	var out triageOutcome
	omitted := ""
	allOmitted := true
	for i, e := range kept {
		po, pc, pi := s.checkTriageEntry(e.a, g, e.index, reply, final, defs, pointed)
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
