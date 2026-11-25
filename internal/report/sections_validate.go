package report

import (
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
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
	if err := validateDiagnosticTriage(r); err != nil {
		return err
	}
	if err := validateLedger(r); err != nil {
		return err
	}
	if cr := r.CodeReview; cr != nil && cr.Subject != "" && (cr.Subject != record.SubjectNonShipped || !builderAtLeast(r.Builder.Version, ShippedFocusSince)) {
		return invalidf("code_review.subject %q in a revision of builder %s", cr.Subject, r.Builder.Version)
	}
	if cr := r.CodeReview; cr != nil && (cr.CandidatesNotShipped < 0 || (cr.CandidatesNotShipped > 0 && !builderAtLeast(r.Builder.Version, ShippedFocusSince))) {
		return invalidf("candidates_not_shipped in a revision of builder %s", r.Builder.Version)
	}
	if err := validateNotShipped(r); err != nil {
		return err
	}
	if cr := r.CodeReview; cr != nil && builderAtLeast(r.Builder.Version, ShippedFocusSince) {
		n := 0
		for _, f := range r.Findings {
			if FindingNotShipped(f) && slices.Contains(cr.FindingIDs, f.ID) {
				n++
			}
		}
		if n != cr.CandidatesNotShipped {
			return invalidf("candidates_not_shipped %d does not match the %d findings that were not sent to verification", cr.CandidatesNotShipped, n)
		}
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

	if err := validateDocumentationGaps(r.Documents, r.Export.IncludeCode); err != nil {
		return err
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
	// record.ModelText; a secret-like value is rejected here as well.
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
			provenance := record.Excerpt{Path: e.Path, Kind: e.Kind, DirectivePath: e.DirectivePath, PackageReference: e.PackageReference}
			if ids[e.ID] || domain.ValidateEvidencePath(e.Path) != nil || !hex64.MatchString(e.BlobSHA256) || e.StartLine < 1 || e.EndLine < e.StartLine ||
				!slices.Contains([]string{"code", "test", "document", "manifest"}, e.Kind) || !provenance.ValidDirectivePath() || !provenance.ValidPackageReference(free) {
				return nil, invalidf("section evidence %s is invalid", e.ID)
			}
			if !e.PackageReference.IsZero() && e.ID != record.ExcerptID(r.Inputs.ManifestSHA256, e.Path, e.BlobSHA256, e.StartLine, e.EndLine) {
				return nil, invalidf("section package evidence is not bound to the document citation")
			}
			if e.Excerpt != nil && (free || !sourceexcerpt.SafePath(e.Path) || !validReportCitation(*e.Excerpt, e.StartLine, e.EndLine, e.Kind, r.Builder.Version) ||
				e.ID != record.ExcerptID(r.Inputs.ManifestSHA256, e.Path, e.BlobSHA256, e.StartLine, e.EndLine)) {
				return nil, invalidf("section excerpt is invalid")
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
	if err := model(d.Model, len(d.Contracts)+len(d.Conflicts)+len(d.Questions)+len(d.InstructionNotes)+len(d.Quality)+len(d.Comparisons)); err != nil {
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
	if _, err := catalog(r.CodeReview.Evidence); err != nil {
		return err
	}
	if err := validateInsights(r); err != nil {
		return err
	}
	for _, l := range r.CodeReview.Limits {
		if !text(l.Statement) {
			return invalidf("interpretation limit text does not follow the export mode")
		}
	}
	return nil
}

func validateDocumentationGaps(d *DocumentsSection, includeCode bool) error {
	if len(d.DocumentationGaps) == 0 {
		return nil
	}
	if d.Discovery != DiscoveryAccepted || len(d.DocumentationGaps) > envdoc.MaxDocumentationGaps {
		return invalidf("documentation gaps without accepted discovery")
	}
	items := map[string]DocItem{}
	for _, item := range d.Items {
		items[item.Path] = item
	}
	for _, g := range d.DocumentationGaps {
		if g.Kind != envdoc.DocGapOption && g.Kind != envdoc.DocGapEnvironment ||
			includeCode && (!envdoc.ValidDocumentationSymbol(g.Kind, g.Symbol) || !envdoc.ValidDocumentationDefault(g.Default)) ||
			!includeCode && (g.Symbol != "" || g.Default != "") || !envdoc.ValidPath(g.Scope) ||
			domain.ValidateEvidencePath(g.Source.Path) != nil || !envdoc.Under(g.Source.Path, g.Scope) ||
			g.Source.StartLine < 1 || g.Source.EndLine < g.Source.StartLine || g.Source.EndLine-g.Source.StartLine >= 25 ||
			len(g.Searched) == 0 || len(g.Searched) > envdoc.MaxGapDocuments {
			return invalidf("documentation gap provenance")
		}
		guideCount := 0
		for _, item := range d.Items {
			if !envdoc.UserGuide(envdoc.Doc{AppliesTo: item.AppliesTo, Class: item.Class}, g.Scope) {
				continue
			}
			if item.Status != envdoc.DocSelected || item.LineCount == nil {
				return invalidf("documentation gap has unread guidance")
			}
			guideCount++
		}
		if guideCount != len(g.Searched) {
			return invalidf("documentation gap has incomplete search")
		}
		for i, x := range g.Searched {
			item := items[x.Path]
			if (i > 0 && g.Searched[i-1].Path >= x.Path) || item.Status != envdoc.DocSelected ||
				item.LineCount == nil || x.LineCount != *item.LineCount ||
				!envdoc.UserGuide(envdoc.Doc{AppliesTo: item.AppliesTo, Class: item.Class}, g.Scope) {
				return invalidf("documentation gap search provenance")
			}
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
		c.Evidence = projectSectionEvidence(e.Evidence)
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
		c.Evidence = projectSectionEvidence(d.Evidence)
		c.Quality = slices.Clone(d.Quality)
		for i := range c.Quality {
			c.Quality[i].Statement = textOmitted
		}
		c.Comparisons = slices.Clone(d.Comparisons)
		for i := range c.Comparisons {
			c.Comparisons[i].Statement = textOmitted
			c.Comparisons[i].Reason = textOmitted
			if original := d.Comparisons[i].AdditionalObservation; original != nil {
				c.Comparisons[i].AdditionalObservation = &AdditionalCodeObservation{Reason: textOmitted, CodeEvidenceIDs: slices.Clone(original.CodeEvidenceIDs)}
			}
			if original := d.Comparisons[i].RelatedWorkflow; original != nil {
				location := *original
				c.Comparisons[i].RelatedWorkflow = &location
			}
		}
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
		c.DocumentationGaps = slices.Clone(c.DocumentationGaps)
		for i := range c.DocumentationGaps {
			c.DocumentationGaps[i].Symbol = ""
			c.DocumentationGaps[i].Default = ""
		}
		out.Documents = &c
	}
	if cr := r.CodeReview; cr != nil {
		c := *cr
		c.Evidence = projectSectionEvidence(cr.Evidence)
		c.Strengths = slices.Clone(cr.Strengths)
		for i := range c.Strengths {
			c.Strengths[i].Title = textOmitted
			c.Strengths[i].Benefit = textOmitted
		}
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
		c.DiagnosticTriage = projectTriage(cr.DiagnosticTriage)
		out.CodeReview = &c
	}
}

func projectSectionEvidence(src []SectionEvidence) []SectionEvidence {
	out := slices.Clone(src)
	for i := range out {
		out[i].Excerpt = nil
		p := &out[i].PackageReference
		p.Name, p.Callable, p.Export = "", "", ""
	}
	return out
}

func validateInsights(r Report) error {
	docs := r.Documents
	code := r.CodeReview
	catalog := func(entries []SectionEvidence) map[string]record.Excerpt {
		out := make(map[string]record.Excerpt, len(entries))
		for _, e := range entries {
			out[e.ID] = record.Excerpt{ID: e.ID, Path: e.Path, Kind: e.Kind, DirectivePath: e.DirectivePath, PackageReference: e.PackageReference,
				Start: e.StartLine, End: e.EndLine, SHA256: e.BlobSHA256}
		}
		return out
	}
	limit := proseLimit(r.Builder.Version)
	text := func(s string) bool { return record.ProseText(s, limit) && (r.Export.IncludeCode || s == textOmitted) }
	if len(docs.Quality) > record.MaxDocumentQuality || len(docs.Comparisons) > record.MaxContractComparisons ||
		(len(docs.Comparisons) > 0 && len(docs.Contracts) != len(docs.Comparisons)) ||
		len(code.Strengths) > MaxReportedStrengths {
		return invalidf("review insight counts are invalid")
	}
	byDoc := catalog(docs.Evidence)
	byCode := catalog(code.Evidence)
	claimScope := func(ids []string, scope, statement string) bool {
		if !r.Export.IncludeCode {
			statement = ""
		}
		return record.DocumentClaimCovers(ids, byDoc, scope, statement)
	}
	for _, d := range docs.Contracts {
		if !claimScope(d.EvidenceIDs, d.AppliesTo, d.Statement) {
			return invalidf("document evidence does not support contract scope")
		}
	}
	for _, q := range docs.Questions {
		if !claimScope(q.EvidenceIDs, q.AppliesTo, q.Question) {
			return invalidf("document evidence does not support question scope")
		}
	}
	for _, q := range docs.Quality {
		if !q.ValidWithin(byDoc, limit) || !text(q.Statement) {
			return invalidf("document quality is invalid")
		}
	}
	followups, workflows := 0, 0
	for i, x := range docs.Comparisons {
		d := docs.Contracts[i]
		comparison := record.ContractComparison{ContractIndex: i + 1, AppliesTo: x.AppliesTo,
			Status: x.Status, Reason: x.Reason, DocumentEvidenceIDs: x.DocumentEvidenceIDs, CodeEvidenceIDs: x.CodeEvidenceIDs}
		if !comparison.ValidWithin(byDoc, limit) || !record.ScopeUnder(x.AppliesTo, d.AppliesTo) ||
			!slices.Equal(x.DocumentEvidenceIDs, d.EvidenceIDs) || x.Statement != d.Statement || !text(x.Reason) {
			return invalidf("document comparison is invalid")
		}
		if x.Followup || x.AdditionalObservation != nil {
			followups++
			if followups > 1 || !hasAcceptedContractReview(r) ||
				(x.Followup && (x.Status == "unknown" || x.AdditionalObservation != nil)) ||
				(x.AdditionalObservation != nil && x.Status != "unknown") {
				return invalidf("document comparison followup is invalid")
			}
		}
		if extra := x.AdditionalObservation; extra != nil {
			var original []record.Excerpt
			for _, id := range x.CodeEvidenceIDs {
				original = append(original, byDoc[id])
			}
			comparison.Reason, comparison.CodeEvidenceIDs = extra.Reason, extra.CodeEvidenceIDs
			if !text(extra.Reason) || !validAdditionalObservation(comparison, byDoc, original) {
				return invalidf("additional document observation is invalid")
			}
		}
		if workflow := x.RelatedWorkflow; workflow != nil {
			workflows++
			if workflows > 1 || !hasAcceptedWorkflowReading(r) || !validWorkflowCitations(*workflow, d.AppliesTo, byDoc) {
				return invalidf("related workflow evidence is invalid")
			}
		}
	}
	for _, x := range code.Strengths {
		if !x.ValidWithin(byCode, limit) || !text(x.Title) || !text(x.Benefit) {
			return invalidf("strength is invalid")
		}
	}
	return nil
}

func validWorkflowCitations(workflow RelatedWorkflow, scope string, catalog map[string]record.Excerpt) bool {
	ids := workflow.evidenceIDs()
	if ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		return false
	}
	for i, kind := range []string{"code", "code", "test"} {
		e := catalog[ids[i]]
		if ids[i] == "" || e.Kind != kind || record.SourceKind(e.Path) != kind ||
			!strings.HasSuffix(e.Path, ".go") || !record.ScopeUnder(e.Path, scope) {
			return false
		}
	}
	return true
}

func hasAcceptedWorkflowReading(r Report) bool {
	version, _ := strconv.Atoi(r.Builder.Version)
	const taskKey = "review.investigate"
	decoders := []string{(WorkflowReadingDecoder{}).Key().String(), (CitedReviewDecoder{}).Key().String()}
	return version >= WorkflowReadingSince && r.CodeReview != nil &&
		slices.ContainsFunc(r.Tasks, func(t Task) bool {
			return t.Key == taskKey && t.Status == string(domain.TaskSucceeded)
		}) &&
		slices.ContainsFunc(r.CodeReview.Roles, func(t AIReviewTask) bool {
			return t.Key == taskKey && t.Result == "accepted" && deref(t.TaskStatus) == string(domain.TaskSucceeded)
		}) &&
		slices.ContainsFunc(r.Artifacts, func(a Artifact) bool {
			return deref(a.TaskKey) == taskKey && slices.Contains(decoders, deref(a.Decoder))
		})
}

var investigateFindingID = regexp.MustCompile(`^review_(s[0-9]{3}_)?investigate_[0-9]+$`)

// validateNotShipped checks the marker of a candidate that was not sent to
// verification against the finding's own evidence: an investigation candidate
// of an instruction pack that sets candidates aside carries
// candidate_not_shipped exactly when every code or test place it cites is on
// a path that is certainly not shipped code (record.PathClass). A forged
// marker would hide a finding of shipped code from the action items; a
// missing one would list a test-only candidate as an ordinary finding.
func validateNotShipped(r Report) error {
	if !builderAtLeast(r.Builder.Version, ShippedFocusSince) {
		return nil
	}
	pack := map[string]string{}
	for _, c := range r.Coverage.ReviewCalls {
		if _, ok := pack[c.TaskKey]; !ok {
			pack[c.TaskKey] = c.PackVersion
		}
	}
	for _, f := range r.Findings {
		marked := FindingNotShipped(f)
		if !investigateFindingID.MatchString(f.ID) {
			if marked {
				return invalidf("finding %s is marked as not sent to verification but is not an investigation candidate", f.ID)
			}
			continue
		}
		task := "review.investigate"
		if strings.HasPrefix(f.ID, "review_s") {
			task += "." + f.ID[len("review_"):len("review_")+4]
		}
		version, known := pack[task]
		if known && !record.PackSetsAside(version) {
			if marked {
				return invalidf("finding %s is marked as not sent to verification by an instruction pack that verifies every candidate", f.ID)
			}
			continue
		}
		cited, shipped := false, false
		for _, e := range f.Evidence {
			if e.Kind != "code" || e.Location == nil {
				continue
			}
			cited = true
			shipped = shipped || record.PathClass(e.Location.Path) == ""
		}
		// A scope without shipped code sets nothing aside.
		if want := cited && !shipped && !(r.CodeReview != nil && r.CodeReview.Subject == record.SubjectNonShipped); want != marked && (known || marked) {
			return invalidf("finding %s: the candidate_not_shipped marker does not match the places it cites", f.ID)
		}
	}
	return nil
}
