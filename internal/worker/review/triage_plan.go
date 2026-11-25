package review

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Diagnostic triage planning (docs/REVIEW.md#도구-진단-분류).
//
// The plan fixes only the number of triage slots, from the manifest and
// scope (A37 2): every slot is one triage task and its verify task. At run
// time each slot recomputes the same assignment from the same inputs (the
// accepted analyzer envelopes, the whole-run interpretation, the snapshot,
// the secrets screen and the node's request budget, all fixed within an
// attempt) and takes the bin with its number: the groups of diagnostics
// most severe by default severity first, then by count.

const (
	// triageFilesPerSlot: one slot per this many eligible source files.
	triageFilesPerSlot = 12
	// triageShownDiagnostics bounds the diagnostics of a group whose
	// message travels; the other lines are listed by number.
	triageShownDiagnostics = 8
	// triageGroupObservations splits a larger group into chunks.
	triageGroupObservations = 64

	limDiagnosticsInterpreted = "diagnostics_interpreted"
	limDiagnosticsNoLocation  = "diagnostics_without_location"
	limDiagnosticsNoContext   = "diagnostic_context_unavailable"
	limDiagnosticsOutside     = "diagnostics_outside_triage_slots"
)

// triageSlotCount is the planned slot count: one per triageFilesPerSlot
// eligible source files, at least two and at most record.MaxTriageSlots.
func triageSlotCount(eligibleFiles int) int {
	if eligibleFiles <= 0 {
		return 0
	}
	return min(record.MaxTriageSlots, max(2, (eligibleFiles+triageFilesPerSlot-1)/triageFilesPerSlot))
}

func triageKey(i int) string { return fmt.Sprintf("t%03d", i+1) }

// triageDiag is one eligible diagnostic.
type triageDiag struct {
	obs  toolObservation
	task string
	def  severity.Result
}

// triageUnit is the code a group is triaged in: one review unit of a file
// and, when it does not start the file, the file's header lines.
type triageUnit struct {
	entry  source.Entry
	kind   string
	span   lineSpan
	header int
	text   []string // the file's lines
}

func (u triageUnit) key() string { return u.entry.Path + "\x00" + fmt.Sprint(u.span.start) }

// cost is the numbered JSON size of the unit and its header.
func (u triageUnit) cost() int {
	n := numberedSize(u.text, u.span.start, u.span.end)
	if h := u.headerEnd(); h > 0 {
		n += numberedSize(u.text, 1, h)
	}
	return n
}

func (u triageUnit) headerEnd() int {
	if u.span.start <= 1 {
		return 0
	}
	return min(u.header, u.span.start-1)
}

// triageGroup is one group of diagnostics.
type triageGroup struct {
	diags     []triageDiag
	unit      triageUnit
	severity  string
	crossTool bool
}

func (g *triageGroup) analyzers() []string {
	var out []string
	for _, d := range g.diags {
		out = addLim(out, d.obs.Tool)
	}
	slices.Sort(out)
	return out
}

func (g *triageGroup) rules() []string {
	var out []string
	for _, d := range g.diags {
		out = addLim(out, d.obs.RuleID)
	}
	slices.Sort(out)
	return out
}

// triagePlan is the assignment of one attempt.
type triagePlan struct {
	bins [][]*triageGroup
	// lims are the plan-wide limitations every slot record carries.
	lims []string
	// eligible is the diagnostics considered.
	eligible int
}

// planTriage groups and packs the diagnostics of docs, leaving out the
// observations an accepted interpretation hypothesis already carries.
func (s *roleStage) planTriage(docs []toolDoc, interpreted map[string]bool, slots int) triagePlan {
	var plan triagePlan
	files := s.entries()
	cached := map[string][]string{}
	unitCache := map[string]fileUnits{}
	groups := map[string]*triageGroup{}
	var order []string
	for _, d := range docs {
		if d.upstream.State != record.UpstreamAccepted || d.doc.Purpose == obsdoc.PurposeMetrics || d.doc.Purpose == obsdoc.PurposeSecrets {
			continue
		}
		for _, o := range d.doc.Observations {
			plan.eligible++
			grade := ""
			if o.Severity != nil {
				grade = *o.Severity
			}
			def := builtin.DefaultSeverity(o.Tool, d.doc.RulePack, d.doc.RulePackVersion, o.RuleID, grade)
			switch {
			case interpreted[o.ID]:
				plan.lims = addLim(plan.lims, limDiagnosticsInterpreted)
				continue
			case o.Location == nil:
				plan.lims = addLim(plan.lims, limDiagnosticsNoLocation)
				continue
			case s.secretFiles()[o.Location.Path]:
				plan.lims = addLim(plan.lims, limSecretWithheld)
				continue
			}
			e, ok := files[o.Location.Path]
			kind := windowKind(o.Location.Path)
			if !ok || kind == "" {
				plan.lims = addLim(plan.lims, limDiagnosticsNoContext)
				continue
			}
			lines, ok, err := s.sourceLines(e, cached)
			if err != nil || !ok {
				plan.lims = addLim(plan.lims, limDiagnosticsNoContext)
				continue
			}
			fu, seen := unitCache[e.Path]
			if !seen {
				fu, _ = unitsOf(e.Path, lines, 1, len(lines), s.limit().unit)
				unitCache[e.Path] = fu
			}
			line := min(max(1, o.Location.StartLine), len(lines))
			i := slices.IndexFunc(fu.units, func(u lineSpan) bool { return u.start <= line && line <= u.end })
			if i < 0 {
				plan.lims = addLim(plan.lims, limDiagnosticsNoContext)
				continue
			}
			unit := triageUnit{entry: e, kind: kind, span: fu.units[i], header: fu.header, text: lines}
			key := strings.Join([]string{o.Tool, o.RuleID, unit.key()}, "\x00")
			g, ok := groups[key]
			if !ok {
				g = &triageGroup{unit: unit}
				groups[key] = g
				order = append(order, key)
			}
			g.diags = append(g.diags, triageDiag{obs: o, task: d.key, def: def})
		}
	}
	list := make([]*triageGroup, 0, len(order))
	for _, k := range order {
		list = append(list, groups[k])
	}
	list = mergeCrossTool(list)
	var chunks []*triageGroup
	for _, g := range list {
		slices.SortFunc(g.diags, func(a, b triageDiag) int {
			return cmp.Or(cmp.Compare(a.obs.Location.StartLine, b.obs.Location.StartLine), cmp.Compare(a.obs.Tool, b.obs.Tool), cmp.Compare(a.obs.ID, b.obs.ID))
		})
		for start := 0; start < len(g.diags); start += triageGroupObservations {
			c := &triageGroup{diags: g.diags[start:min(len(g.diags), start+triageGroupObservations)], unit: g.unit}
			// A merged group split at the limit is cross-tool only in the
			// chunks that still hold two tools.
			c.crossTool = len(c.analyzers()) > 1
			for _, d := range c.diags {
				if severity.Rank(d.def.Severity) > severity.Rank(c.severity) {
					c.severity = d.def.Severity
				}
			}
			chunks = append(chunks, c)
		}
	}
	slices.SortStableFunc(chunks, func(a, b *triageGroup) int {
		return cmp.Or(cmp.Compare(severity.Rank(b.severity), severity.Rank(a.severity)), cmp.Compare(len(b.diags), len(a.diags)),
			cmp.Compare(a.unit.entry.Path, b.unit.entry.Path), cmp.Compare(a.diags[0].obs.Location.StartLine, b.diags[0].obs.Location.StartLine),
			cmp.Compare(a.diags[0].obs.Tool, b.diags[0].obs.Tool), cmp.Compare(a.diags[0].obs.RuleID, b.diags[0].obs.RuleID))
	})
	plan.bins = s.packTriage(chunks, slots, &plan.lims)
	return plan
}

// mergeCrossTool joins groups of different analyzers in one unit when two of
// their diagnostics are on the same line and their rules are of one mergeable
// family (the tables' families; an unknown family never merges). Groups are
// joined, never the observations or findings.
func mergeCrossTool(list []*triageGroup) []*triageGroup {
	parent := make([]int, len(list))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			i = parent[i]
		}
		return i
	}
	same := func(a, b *triageGroup) bool {
		if a.unit.key() != b.unit.key() || a.diags[0].obs.Tool == b.diags[0].obs.Tool {
			return false
		}
		for _, x := range a.diags {
			fx, ok := severity.FamilyOf(x.def.Family)
			if !ok || !fx.Merge {
				continue
			}
			for _, y := range b.diags {
				if x.obs.Location.StartLine == y.obs.Location.StartLine && x.def.Family == y.def.Family {
					return true
				}
			}
		}
		return false
	}
	for i := range list {
		for j := i + 1; j < len(list); j++ {
			if same(list[i], list[j]) {
				if a, b := find(i), find(j); a != b {
					parent[b] = a
				}
			}
		}
	}
	var out []*triageGroup
	for i, g := range list {
		root := find(i)
		if root == i {
			out = append(out, g)
			continue
		}
		r := list[root]
		r.diags = append(r.diags, g.diags...)
		r.crossTool = true
	}
	return out
}

// packTriage puts each group, in priority order, into the first bin with
// room for its group count, unit bytes and observations; a unit already in
// a bin costs nothing more there. Bins past the planned slots are not made:
// their groups are not triaged (diagnostics_outside_triage_slots).
func (s *roleStage) packTriage(groups []*triageGroup, slots int, lims *[]string) [][]*triageGroup {
	type bin struct {
		groups []*triageGroup
		units  map[string]bool
		bytes  int
		obs    int
		input  int
	}
	l := s.limit()
	maxGroups, inputCap := triageGroupLimit(s.h.Budget), triageInputBytes(l)
	var bins []*bin
	for _, g := range groups {
		in := s.triageGroupInputBytes(g)
		placed := false
		for _, b := range bins {
			extra := 0
			if !b.units[g.unit.key()] {
				extra = g.unit.cost()
			}
			if len(b.groups) < maxGroups && b.bytes+extra <= l.source && b.obs+len(g.diags) <= record.MaxTriageObservations && b.input+in <= inputCap {
				b.groups, b.bytes, b.obs, b.input = append(b.groups, g), b.bytes+extra, b.obs+len(g.diags), b.input+in
				b.units[g.unit.key()] = true
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		if len(bins) >= slots {
			*lims = addLim(*lims, limDiagnosticsOutside)
			continue
		}
		bins = append(bins, &bin{groups: []*triageGroup{g}, units: map[string]bool{g.unit.key(): true}, bytes: g.unit.cost(), obs: len(g.diags), input: in})
	}
	out := make([][]*triageGroup, len(bins))
	for i, b := range bins {
		out[i] = b.groups
	}
	return out
}

// triageGroupLimit is the groups of one request: about 1024 output tokens
// a group with every field written out, from 2 at the smallest output to
// 24.
func triageGroupLimit(b Budget) int {
	out := b.OutputTokens
	if out == 0 {
		out = DefaultOutputTokens
	}
	out = min(max(out, MinOutputTokens), MaxOutputTokens)
	return min(max((out-1024)/1024, 2), 24)
}

// triageInputBytes bounds the group descriptions of one request (the
// diagnostics with their messages): a share of the fixed part of a request.
func triageInputBytes(l limits) int { return max(8000, l.request/16) }

// triageGroupInputBytes estimates one group's description in a request.
func (s *roleStage) triageGroupInputBytes(g *triageGroup) int {
	n := 256 + 8*len(g.diags)
	for i, d := range g.diags {
		if i < triageShownDiagnostics {
			m, _ := s.shownMessage(d.obs)
			n += 160 + len(m)
		}
	}
	return n
}
