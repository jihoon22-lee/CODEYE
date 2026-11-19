package report

import (
	"reflect"
	"regexp"
	"slices"
	"strconv"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// requirementConstraintPattern is the declared requirement text the
// code-free export keeps: version-constraint characters only. Anything else
// is repository text and is withheld like a tool message.
var requirementConstraintPattern = regexp.MustCompile(`^[0-9A-Za-z.*^~<>=!, |+_-]{1,64}$`)

func keepConstraint(s string) bool {
	return requirementConstraintPattern.MatchString(s) && !record.Sensitive([]byte(s))
}

// sectionsExpected reports whether a revision of this builder version must
// carry the sections (and an earlier one must carry none).
func sectionsExpected(version string) bool {
	n, err := strconv.Atoi(version)
	return err == nil && n >= SectionsSince
}

func (r Report) hasSections() bool {
	return r.Overview != nil || r.Environment != nil || r.Documents != nil || r.StaticAnalysis != nil || r.Metrics != nil || r.Secrets != nil || r.CodeReview != nil || r.Gaps != nil
}

var gapCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// validateSections enforces the section contract (docs/REPORT.md#분석-종류별-절): presence by
// builder version, the partition of observations (static analysis, metrics,
// secrets) and findings (static analysis, code review), counts that add up
// to the summary, an overview equal to its recomputation, resolvable
// evidence and the code-free rules for model text.
func validateSections(r Report) error {
	if err := validateLedger(r); err != nil {
		return err
	}
	if !sectionsExpected(r.Builder.Version) {
		if r.hasSections() {
			return invalidf("sections in a revision of builder %s", r.Builder.Version)
		}
		return nil
	}
	if r.Overview == nil || r.Environment == nil || r.Documents == nil || r.StaticAnalysis == nil || r.Metrics == nil || r.Secrets == nil || r.CodeReview == nil {
		return invalidf("builder %s report without its sections", r.Builder.Version)
	}
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	findings := map[string]Finding{}
	for _, f := range r.Findings {
		findings[f.ID] = f
	}
	for _, st := range []SectionStage{r.Environment.Stage, r.Documents.Stage, r.StaticAnalysis.Stage, r.Metrics.Stage, r.Secrets.Stage, r.CodeReview.Stage} {
		if err := validStage(st); err != nil {
			return err
		}
	}

	// Observation partition.
	placed := map[string]string{}
	place := func(id, section string) error {
		if _, ok := obs[id]; !ok {
			return invalidf("%s references an unknown observation", section)
		}
		if prev, dup := placed[id]; dup {
			return invalidf("observation %s is in both %s and %s", id, prev, section)
		}
		placed[id] = section
		return nil
	}
	staticFindings := map[string]bool{}
	tasks := map[string]bool{}
	for _, t := range r.Tasks {
		tasks[t.Key] = true
	}
	for _, l := range r.StaticAnalysis.Languages {
		for _, t := range l.Tools {
			if !tasks[t.TaskKey] {
				return invalidf("static analysis tool row of an unknown task")
			}
			unresolved, suppressed, tallied := 0, 0, 0
			for _, id := range t.ObservationIDs {
				if err := place(id, SectionStaticAnalysis); err != nil {
					return err
				}
				o := obs[id]
				if o.TaskKey != t.TaskKey || o.Metric != nil {
					return invalidf("static analysis row %s lists an observation of another task or a metric", t.TaskKey)
				}
				if o.EvidenceState != EvidenceVerified {
					unresolved++
				}
				if o.Suppressed {
					suppressed++
				}
			}
			for _, x := range t.Rules {
				tallied += x.Count
			}
			metric, secret := 0, 0
			for _, o := range r.Observations {
				if o.TaskKey == t.TaskKey && o.Metric != nil {
					metric++
				}
			}
			for _, o := range r.Observations {
				if o.TaskKey == t.TaskKey && o.Metric == nil && !slices.Contains(t.ObservationIDs, o.ID) {
					secret++
				}
			}
			if unresolved != t.Unresolved || suppressed != t.Suppressed || tallied != len(t.ObservationIDs) || metric != t.MetricObservations || secret != t.SecretObservations {
				return invalidf("static analysis row %s counts differ from its observations", t.TaskKey)
			}
			for _, id := range t.FindingIDs {
				if _, ok := findings[id]; !ok || staticFindings[id] {
					return invalidf("static analysis row %s lists an unknown or repeated finding", t.TaskKey)
				}
				staticFindings[id] = true
			}
		}
	}
	metricTotal := 0
	for _, g := range r.Metrics.Metrics {
		// The group is recomputed from its observations: one name and unit,
		// the sorted unique thresholds and the largest value.
		max, thresholds := 0, []int{}
		for _, id := range g.ObservationIDs {
			if err := place(id, SectionMetrics); err != nil {
				return err
			}
			m := obs[id].Metric
			if m == nil || m.Name != g.Name || m.Unit != g.Unit {
				return invalidf("metric group %s lists a different observation", g.Name)
			}
			if !slices.Contains(thresholds, m.Threshold) {
				thresholds = append(thresholds, m.Threshold)
			}
			if m.Value > max {
				max = m.Value
			}
			metricTotal++
		}
		slices.Sort(thresholds)
		if len(g.ObservationIDs) == 0 || max != g.MaxValue || !slices.Equal(thresholds, g.Thresholds) {
			return invalidf("metric group %s does not follow its observations", g.Name)
		}
	}
	if metricTotal != r.Summary.MetricObservations {
		return invalidf("metrics section count differs from the summary")
	}
	for _, d := range r.Secrets.Detections {
		if err := place(d.ObservationID, SectionSecrets); err != nil {
			return err
		}
		o := obs[d.ObservationID]
		if o.Metric != nil || o.TaskKey != d.TaskKey || o.RuleID != d.RuleID || o.EvidenceState != d.EvidenceState || !slices.Contains(r.Secrets.TaskKeys, d.TaskKey) ||
			(o.Location == nil) != (d.Path == nil) || (o.Location != nil && (o.Location.Path != *d.Path || o.Location.StartLine != d.StartLine || o.Location.EndLine != d.EndLine)) {
			return invalidf("secret detection %s differs from its observation", d.ObservationID)
		}
		if d.FindingID != nil && (!staticFindings[*d.FindingID] || !slices.Contains(findings[*d.FindingID].ObservationIDs, d.ObservationID)) {
			return invalidf("secret detection %s names a finding that is not its tool finding", d.ObservationID)
		}
	}
	if len(placed) != len(r.Observations) {
		return invalidf("observations outside the static analysis, metrics and secrets sections")
	}

	// Finding partition.
	reviewFindings := map[string]bool{}
	for _, id := range r.CodeReview.FindingIDs {
		if _, ok := findings[id]; !ok || reviewFindings[id] || staticFindings[id] {
			return invalidf("code review lists an unknown finding or one of static analysis")
		}
		reviewFindings[id] = true
	}
	if len(staticFindings)+len(reviewFindings) != len(r.Findings) {
		return invalidf("findings outside the static analysis and code review sections")
	}
	a, b := ComputeSummary(findingsOf(r, StaticFindingIDs(r)), nil), ComputeSummary(findingsOf(r, r.CodeReview.FindingIDs), nil)
	s := r.Summary
	if a.TotalFindings+b.TotalFindings != s.TotalFindings || a.ActionableFindings+b.ActionableFindings != s.ActionableFindings || a.PendingCandidates+b.PendingCandidates != s.PendingCandidates ||
		a.RefutedFindings+b.RefutedFindings != s.RefutedFindings || a.HiddenFindings+b.HiddenFindings != s.HiddenFindings {
		return invalidf("section finding counts do not add up to the summary")
	}
	for _, l := range r.CodeReview.Limits {
		for _, id := range l.ObservationIDs {
			if _, ok := obs[id]; !ok {
				return invalidf("interpretation limit cites an unknown observation")
			}
		}
	}

	if err := validateModelSections(r); err != nil {
		return err
	}
	for _, g := range r.Gaps {
		if _, ok := gapSectionOrder[g.Section]; !ok || !gapCode.MatchString(g.Code) || (g.Detail != nil && !codePattern.MatchString(*g.Detail)) {
			return invalidf("gap %s/%s is invalid", g.Section, g.Code)
		}
	}
	if want := overviewOf(r); !reflect.DeepEqual(want, *r.Overview) {
		return invalidf("overview differs from the sections")
	}
	return nil
}

func validStage(st SectionStage) error {
	if !domain.Stage(st.Stage).Valid() || st.Counts == nil || st.Reasons == nil || st.Succeeded > st.Tasks {
		return invalidf("section stage is invalid")
	}
	if st.State == StateNotRecorded {
		return nil
	}
	if !slices.Contains(progress.States, progress.State(st.State)) {
		return invalidf("section stage state %q is not a stage state", st.State)
	}
	return nil
}

// validateModelSections checks the model interpretation of the environment,
// documents and code review sections: a role that was not performed has no
// output, every cited excerpt resolves in the section and the code-free
// export holds no model text or repository requirement text.
func validateModelSections(r Report) error {
	free := !r.Export.IncludeCode
	// Model statements come from accepted records, whose text already passed
	// record.RoleText; a secret-like value is rejected here as well.
	text := func(s string) bool { return s != "" && (!free || s == textOmitted) && !record.Sensitive([]byte(s)) }
	model := func(m ModelInterpretation, outputs int) error {
		if (m.State != ModelPerformed && m.State != ModelNotPerformed) || (m.Reason == nil) != (m.State == ModelPerformed) || (m.State != ModelPerformed && outputs != 0) {
			return invalidf("model interpretation of %s is inconsistent", m.TaskKey)
		}
		return nil
	}
	catalog := func(ev []SectionEvidence) (map[string]bool, error) {
		ids := map[string]bool{}
		for _, e := range ev {
			if ids[e.ID] || domain.ValidateEvidencePath(e.Path) != nil || !hex64.MatchString(e.BlobSHA256) || e.StartLine < 1 || e.EndLine < e.StartLine ||
				!slices.Contains([]string{"code", "test", "document", "manifest"}, e.Kind) {
				return nil, invalidf("section evidence %s is invalid", e.ID)
			}
			ids[e.ID] = true
		}
		return ids, nil
	}
	cites := func(ids map[string]bool, lists ...[]string) error {
		for _, l := range lists {
			for _, id := range l {
				if !ids[id] {
					return invalidf("section statement cites evidence %s the section does not carry", id)
				}
			}
		}
		return nil
	}
	e := r.Environment
	if err := model(e.Model, len(e.Notes)); err != nil {
		return err
	}
	ids, err := catalog(e.Evidence)
	if err != nil {
		return err
	}
	for _, n := range e.Notes {
		if !text(n.AffectedChecks) || !text(n.StillCheckable) || !text(n.NeededObservation) {
			return invalidf("environment note text does not follow the export mode")
		}
		if err := cites(ids, n.EvidenceIDs); err != nil {
			return err
		}
	}
	for _, q := range e.Requirements {
		if (q.Constraint == nil && !free) || (q.Constraint != nil && free && !keepConstraint(*q.Constraint)) {
			return invalidf("requirement %s constraint does not follow the export mode", q.ID)
		}
	}
	d := r.Documents
	if err := model(d.Model, len(d.Contracts)+len(d.Conflicts)+len(d.Questions)+len(d.InstructionNotes)); err != nil {
		return err
	}
	if ids, err = catalog(d.Evidence); err != nil {
		return err
	}
	for _, x := range d.Contracts {
		if !text(x.Statement) {
			return invalidf("contract text does not follow the export mode")
		}
		if err := cites(ids, x.EvidenceIDs); err != nil {
			return err
		}
	}
	for _, x := range d.Conflicts {
		if !text(x.Summary) || !text(x.Impact) {
			return invalidf("conflict text does not follow the export mode")
		}
		if err := cites(ids, x.FirstEvidence, x.SecondEvidence); err != nil {
			return err
		}
	}
	for _, x := range d.Questions {
		if !text(x.Question) {
			return invalidf("question text does not follow the export mode")
		}
		if err := cites(ids, x.EvidenceIDs); err != nil {
			return err
		}
	}
	for _, x := range d.InstructionNotes {
		if !text(x.Note) {
			return invalidf("instruction note text does not follow the export mode")
		}
		if err := cites(ids, x.EvidenceIDs); err != nil {
			return err
		}
	}
	for _, l := range r.CodeReview.Limits {
		if !text(l.Statement) {
			return invalidf("interpretation limit text does not follow the export mode")
		}
	}
	return nil
}

// projectSections is the code-free projection of the sections: every model
// statement becomes the omission label (as finding prose does) and a
// requirement constraint that is not version-constraint text is withheld.
// Paths, lines, codes, counts and blob hashes stay, as in findings.
func projectSections(out *Report, r Report) {
	if e := r.Environment; e != nil {
		c := *e
		c.Requirements = make([]EnvRequirement, len(e.Requirements))
		for i, q := range e.Requirements {
			if q.Constraint != nil && !keepConstraint(*q.Constraint) {
				q.Constraint = nil
			}
			c.Requirements[i] = q
		}
		c.Notes = make([]EnvNote, len(e.Notes))
		for i, n := range e.Notes {
			n.RequirementIDs, n.EvidenceIDs = slices.Clone(n.RequirementIDs), slices.Clone(n.EvidenceIDs)
			n.AffectedChecks, n.StillCheckable, n.NeededObservation = textOmitted, textOmitted, textOmitted
			c.Notes[i] = n
		}
		out.Environment = &c
	}
	if d := r.Documents; d != nil {
		c := *d
		c.Contracts = make([]DocContract, len(d.Contracts))
		for i, x := range d.Contracts {
			x.Statement, x.EvidenceIDs = textOmitted, slices.Clone(x.EvidenceIDs)
			c.Contracts[i] = x
		}
		c.Conflicts = make([]DocConflict, len(d.Conflicts))
		for i, x := range d.Conflicts {
			x.Summary, x.Impact = textOmitted, textOmitted
			x.FirstEvidence, x.SecondEvidence = slices.Clone(x.FirstEvidence), slices.Clone(x.SecondEvidence)
			c.Conflicts[i] = x
		}
		c.Questions = make([]DocQuestion, len(d.Questions))
		for i, x := range d.Questions {
			x.Question, x.EvidenceIDs = textOmitted, slices.Clone(x.EvidenceIDs)
			c.Questions[i] = x
		}
		c.InstructionNotes = make([]DocInstructionNote, len(d.InstructionNotes))
		for i, x := range d.InstructionNotes {
			x.Note, x.EvidenceIDs = textOmitted, slices.Clone(x.EvidenceIDs)
			c.InstructionNotes[i] = x
		}
		out.Documents = &c
	}
	if cr := r.CodeReview; cr != nil {
		c := *cr
		c.Limits = make([]InterpretationLimit, len(cr.Limits))
		for i, l := range cr.Limits {
			l.Statement, l.ObservationIDs = textOmitted, slices.Clone(l.ObservationIDs)
			c.Limits[i] = l
		}
		if cr.Ledger != nil {
			// Module IDs, task keys, codes and counts: no repository text.
			l := *cr.Ledger
			l.Modules = make([]LedgerModule, len(cr.Ledger.Modules))
			for i, m := range cr.Ledger.Modules {
				m.Slices = slices.Clone(m.Slices)
				l.Modules[i] = m
			}
			c.Ledger = &l
		}
		out.CodeReview = &c
	}
}
