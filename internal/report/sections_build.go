package report

import (
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// composeSections derives the per-analysis sections (docs/REPORT.md#분석-종류별-절) from the
// finished report and the task states. It is read-only towards everything
// Build decided before it: no limitation, note, count, rejection, status or
// verdict depends on a section.
func composeSections(r *Report, in Input, states map[string]*taskState, keys []string, manifest bool) {
	c := sectionContext{r: r, states: states, keys: keys, manifest: manifest, fixed: len(in.Tasks) > 0, recorded: true}
	for _, t := range in.Tasks {
		if t.Stage == "" {
			c.recorded = false
		}
	}
	c.reviewPlanned = slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, "review.") })
	r.Environment = c.environment()
	r.Documents = c.documents()
	r.StaticAnalysis = c.staticAnalysis()
	r.Metrics = c.metrics()
	r.Secrets = c.secrets()
	r.CodeReview = c.codeReview()
	r.Gaps = c.gaps()
	o := overviewOf(*r)
	r.Overview = &o
}

type sectionContext struct {
	r               *Report
	states          map[string]*taskState
	keys            []string
	manifest, fixed bool
	recorded        bool
	reviewPlanned   bool
}

func strPtr(s string) *string { return &s }

// stage classifies the tasks of a task stage that pass keep with the run
// screen's classifier.
func (c sectionContext) stage(stage domain.Stage, keep func(string) bool) SectionStage {
	out := SectionStage{Stage: string(stage), Counts: map[string]int{}, Reasons: []string{}}
	if !c.recorded {
		out.State = StateNotRecorded
		return out
	}
	var tasks []progress.Task
	for _, k := range c.keys {
		t := c.states[k].facts
		if t.Stage != string(stage) || (keep != nil && !keep(k)) {
			continue
		}
		reason := ""
		if t.Reason != nil {
			reason = *t.Reason
		}
		tasks = append(tasks, progress.Task{Stage: stage, Status: domain.TaskStatus(t.Status), Reason: reason})
	}
	ps := progress.TaskStage(string(stage), tasks, c.fixed)
	out.State, out.Tasks, out.Succeeded = string(ps.State), len(tasks), ps.Succeeded
	for k, v := range ps.Counts {
		out.Counts[k] = v
	}
	out.Reasons = append(out.Reasons, ps.Reasons...)
	return out
}

// discovery is whether the platform discovery document of a task reached
// the report.
func (c sectionContext) discovery(key string) (string, *envdoc.Document) {
	st := c.states[key]
	switch {
	case st == nil:
		return DiscoveryNotPlanned, nil
	case st.rejected:
		return DiscoveryRejected, nil
	case st.discovery != nil:
		return DiscoveryAccepted, st.discovery
	case st.facts.Status != string(domain.TaskSucceeded):
		return DiscoveryNotSucceeded, nil
	}
	return DiscoveryMissing, nil
}

// model tells whether the review role key ran and its record was kept.
func (c sectionContext) model(key string) (ModelInterpretation, *record.Record) {
	m := ModelInterpretation{TaskKey: key, State: ModelNotPerformed}
	st := c.states[key]
	if st != nil {
		m.TaskStatus = strPtr(st.facts.Status)
	}
	switch {
	case !c.reviewPlanned:
		m.Reason = strPtr("review_tasks_not_planned")
	case st == nil:
		m.Reason = strPtr("role_not_planned")
	case st.facts.Status != string(domain.TaskSucceeded):
		reason := "not_succeeded"
		if st.facts.Reason != nil {
			reason = *st.facts.Reason
		}
		m.Reason = strPtr(reason)
	case st.rejected:
		m.Reason = strPtr("result_rejected")
	case !reviewAccepted(st):
		m.Reason = strPtr("result_missing")
	default:
		m.State = ModelPerformed
		return m, st.reviewResult.Record
	}
	return m, nil
}

// evidenceOf carries the catalog entries the section's statements cite.
func evidenceOf(rec *record.Record, lists ...[]string) []SectionEvidence {
	out := []SectionEvidence{}
	if rec == nil {
		return out
	}
	want := map[string]bool{}
	for _, l := range lists {
		for _, id := range l {
			want[id] = true
		}
	}
	for _, e := range rec.Evidence {
		if want[e.ID] {
			out = append(out, SectionEvidence{ID: e.ID, Kind: e.Kind, Path: e.Path, StartLine: e.Start, EndLine: e.End,
				BlobSHA256: e.SHA256, DirectivePath: e.DirectivePath, PackageReference: e.PackageReference})
			delete(want, e.ID)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func (c sectionContext) environment() *EnvironmentSection {
	s := &EnvironmentSection{Stage: c.stage(domain.StageEnvironment, nil), Modules: []EnvModule{}, Manifests: []EnvManifest{}, Requirements: []EnvRequirement{},
		Observed: []EnvObserved{}, Verdicts: []EnvVerdict{}, AnalyzerTools: []EnvAnalyzerTool{}, Limitations: []string{}, Notes: []EnvNote{}}
	var doc *envdoc.Document
	s.Discovery, doc = c.discovery(envdoc.TaskEnvironment)
	if doc != nil && doc.Environment != nil {
		for _, m := range doc.Modules {
			s.Modules = append(s.Modules, EnvModule{ID: m.ID, Ecosystem: m.Ecosystem, Path: m.Path, InScope: m.InScope, BuildSystem: m.BuildSystem, BuildRoot: m.BuildRoot})
		}
		e := doc.Environment
		for _, m := range e.Manifests {
			s.Manifests = append(s.Manifests, EnvManifest{Path: m.Path, Status: m.Status})
		}
		for _, q := range e.Requirements {
			s.Requirements = append(s.Requirements, EnvRequirement{ID: q.ID, Module: q.Module, Tool: q.Tool, Kind: q.Kind, Constraint: strPtr(q.Constraint), Comparison: q.Comparison,
				Path: q.Source.Path, StartLine: q.Source.StartLine, EndLine: q.Source.EndLine})
		}
		for _, o := range e.Observations {
			s.Observed = append(s.Observed, EnvObserved{Tool: o.Tool, Status: o.Status, Version: o.Version, Detail: o.Detail})
		}
		for _, v := range e.Verdicts {
			s.Verdicts = append(s.Verdicts, EnvVerdict{Module: v.Module, Tool: v.Tool, Requirement: v.Requirement, Status: v.Status, Reason: v.Reason})
		}
		for _, t := range e.AnalyzerTools {
			s.AnalyzerTools = append(s.AnalyzerTools, EnvAnalyzerTool{Tool: t.Tool, Analyzers: append([]string{}, t.Analyzers...), Status: t.Status, Version: t.Version, Detail: t.Detail})
		}
		s.Limitations = append(s.Limitations, doc.Limitations...)
	}
	var rec *record.Record
	if deps := c.dependencies(); len(deps) > 0 {
		s.Dependencies = deps
	}
	s.Model, rec = c.model("review.environment")
	var cited []string
	if rec != nil {
		for _, n := range rec.EnvironmentNotes {
			s.Notes = append(s.Notes, EnvNote{Module: n.Module, RequirementIDs: append([]string{}, n.RequirementIDs...), EvidenceIDs: append([]string{}, n.EvidenceIDs...),
				AffectedChecks: n.AffectedChecks, StillCheckable: n.StillCheckable, NeededObservation: n.NeededObservation})
			cited = append(cited, n.EvidenceIDs...)
		}
	}
	s.Evidence = evidenceOf(rec, cited)
	return s
}

func (c sectionContext) documents() *DocumentsSection {
	s := &DocumentsSection{Stage: c.stage(domain.StageDocuments, nil), Items: []DocItem{}, Modules: []DocModule{}, Overlaps: []DocOverlap{}, Limitations: []string{},
		Contracts: []DocContract{}, Conflicts: []DocConflict{}, Questions: []DocQuestion{}, InstructionNotes: []DocInstructionNote{}}
	var doc *envdoc.Document
	s.Discovery, doc = c.discovery(envdoc.TaskDocuments)
	if doc != nil && doc.Documents != nil {
		d := doc.Documents
		s.Budget = &DocBudget{TotalBytes: d.Budget.TotalBytes, PerDocumentBytes: d.Budget.PerDocumentBytes, MaxDocuments: d.Budget.MaxDocuments, ReadBytes: d.Budget.ReadBytes}
		for _, x := range d.Items {
			s.Items = append(s.Items, DocItem{Path: x.Path, Class: x.Class, AppliesTo: x.AppliesTo, Status: x.Status, Reason: x.Reason, SizeBytes: x.SizeBytes, LineCount: x.LineCount})
		}
		if len(d.DocumentationGaps) != 0 {
			byPath := make(map[string]int, len(d.Items))
			for _, item := range d.Items {
				if item.LineCount != nil {
					byPath[item.Path] = *item.LineCount
				}
			}
			for _, gap := range d.DocumentationGaps {
				x := DocCoverageGap{Kind: gap.Kind, Symbol: gap.Symbol, Default: gap.Default, Scope: gap.Scope,
					Source: DocCoverageLocation{gap.Source.Path, gap.Source.StartLine, gap.Source.EndLine}}
				for _, p := range gap.Searched {
					x.Searched = append(x.Searched, DocCoverageSearch{p, byPath[p]})
				}
				s.DocumentationGaps = append(s.DocumentationGaps, x)
			}
		}
		for _, m := range d.Modules {
			s.Modules = append(s.Modules, DocModule{Module: m.Module, State: m.State, Own: append([]string{}, m.Own...), Inherited: append([]string{}, m.Inherited...)})
		}
		for _, o := range d.OverlappingGuidance {
			s.Overlaps = append(s.Overlaps, DocOverlap{Path: o.Path, Documents: append([]string{}, o.Documents...), Assessment: o.Assessment})
		}
		s.Limitations = append(s.Limitations, doc.Limitations...)
	}
	var rec *record.Record
	s.Model, rec = c.model("review.documents")
	var cited [][]string
	if rec != nil {
		for _, x := range rec.Contracts {
			s.Contracts = append(s.Contracts, DocContract{Statement: x.Statement, AppliesTo: x.AppliesTo, EvidenceIDs: append([]string{}, x.EvidenceIDs...)})
			cited = append(cited, x.EvidenceIDs)
		}
		for _, x := range rec.Conflicts {
			s.Conflicts = append(s.Conflicts, DocConflict{Scope: x.Scope, Summary: x.Summary, Impact: x.Impact, FirstEvidence: append([]string{}, x.FirstEvidence...), SecondEvidence: append([]string{}, x.SecondEvidence...)})
			cited = append(cited, x.FirstEvidence, x.SecondEvidence)
		}
		for _, x := range rec.Questions {
			s.Questions = append(s.Questions, DocQuestion{Question: x.Question, AppliesTo: x.AppliesTo, EvidenceIDs: append([]string{}, x.EvidenceIDs...)})
			cited = append(cited, x.EvidenceIDs)
		}
		s.Quality = append(s.Quality, rec.DocumentQuality...)
		for _, x := range s.Quality {
			cited = append(cited, x.EvidenceIDs)
		}
		for _, x := range rec.InstructionNotes {
			s.InstructionNotes = append(s.InstructionNotes, DocInstructionNote{Note: x.Note, EvidenceIDs: append([]string{}, x.EvidenceIDs...)})
			cited = append(cited, x.EvidenceIDs)
		}
	}
	s.Evidence = evidenceOf(rec, cited...)
	if rec != nil {
		c.addComparisons(s, rec)
	}
	return s
}

// analyzerKeys are the analyzer tasks of the run in key order.
func (c sectionContext) analyzerKeys() []string {
	var out []string
	for _, k := range c.keys {
		if analyzerTask(k) {
			out = append(out, k)
		}
	}
	return out
}

// purposeOf is the analyzer purpose of a task's accepted envelope ("" when
// none reached the report).
func (c sectionContext) purposeOf(key string) string {
	st := c.states[key]
	if st == nil || st.rejected || st.analyzer == nil {
		return ""
	}
	return st.analyzer.Purpose
}

// toolResult says whether a task's analyzer document reached the report.
func (c sectionContext) toolResult(st *taskState) string {
	switch {
	case st.rejected:
		return DiscoveryRejected
	case st.tool != nil:
		return DiscoveryAccepted
	case !c.manifest || st.manifest == nil:
		return "not_in_result_manifest"
	}
	return DiscoveryMissing
}

var ecosystemOrder = map[string]int{envdoc.EcoCpp: 0, envdoc.EcoGo: 1, envdoc.EcoJavaScript: 2, envdoc.EcoPython: 3, envdoc.EcoRust: 4, "any": 5}

func (c sectionContext) staticAnalysis() *StaticAnalysisSection {
	s := &StaticAnalysisSection{Stage: c.stage(domain.StageStaticAnalysis, nil), Languages: []StaticLanguage{}}
	groups := map[string][]StaticTool{}
	for _, k := range c.analyzerKeys() {
		st := c.states[k]
		t := StaticTool{TaskKey: k, Required: st.facts.Required, TaskStatus: st.facts.Status, TaskReason: st.facts.Reason, Result: c.toolResult(st),
			ObservationIDs: []string{}, FindingIDs: []string{}, Rules: []RuleTally{}}
		group := ""
		purpose := c.purposeOf(k)
		if a := st.analyzer; a != nil && !st.rejected {
			group = a.Ecosystem
			t.Analyzer, t.Purpose, t.Tool, t.RulePack = strPtr(a.ID), strPtr(a.Purpose), strPtr(a.Tool), strPtr(a.RulePack)
		}
		if tc := st.tool; tc != nil && !st.rejected {
			t.ToolVersion, t.ExitClass, t.PartialParse, t.ProcessedFiles = tc.ToolVersion, strPtr(tc.ExitClass), tc.PartialParse, tc.ProcessedFiles
			n := tc.FilesInScope
			if !slices.Contains(tc.Limitations, "files_in_scope_unknown") {
				t.FilesInScope = &n
			}
		}
		tally := map[[2]string]int{}
		for _, o := range c.r.Observations {
			switch {
			case o.TaskKey != k:
				continue
			case o.Metric != nil:
				t.MetricObservations++
				continue
			case purpose == PurposeSecrets:
				t.SecretObservations++
				continue
			}
			t.ObservationIDs = append(t.ObservationIDs, o.ID)
			if o.Suppressed {
				t.Suppressed++
			}
			if o.EvidenceState != EvidenceVerified {
				t.Unresolved++
			}
			sev := "\x00"
			if o.RawSeverity != nil {
				sev = *o.RawSeverity
			}
			tally[[2]string{o.RuleID, sev}]++
		}
		for k, n := range tally {
			rt := RuleTally{RuleID: k[0], Count: n}
			if k[1] != "\x00" {
				rt.RawSeverity = strPtr(k[1])
			}
			t.Rules = append(t.Rules, rt)
		}
		sort.Slice(t.Rules, func(a, b int) bool {
			x, y := t.Rules[a], t.Rules[b]
			if x.Count != y.Count {
				return x.Count > y.Count
			}
			if x.RuleID != y.RuleID {
				return x.RuleID < y.RuleID
			}
			return deref(x.RawSeverity) < deref(y.RawSeverity)
		})
		// Findings of the task stay listed even when a later artifact of it
		// was rejected: they are in the report.
		t.FindingIDs = c.sortedFindings(st.toolFindings)
		groups[group] = append(groups[group], t)
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Slice(names, func(a, b int) bool {
		x, okx := ecosystemOrder[names[a]]
		y, oky := ecosystemOrder[names[b]]
		if okx != oky {
			return okx
		}
		if x != y {
			return x < y
		}
		return names[a] < names[b]
	})
	for _, g := range names {
		l := StaticLanguage{Tools: groups[g]}
		if g != "" {
			l.Ecosystem = strPtr(g)
		}
		s.Languages = append(s.Languages, l)
	}
	return s
}

// sortedFindings orders finding IDs as the report lists findings.
func (c sectionContext) sortedFindings(ids []string) []string {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := []string{}
	for _, f := range c.r.Findings {
		if want[f.ID] {
			out = append(out, f.ID)
		}
	}
	return out
}

func (c sectionContext) purposeKeys(purpose string) []string {
	out := []string{}
	for _, k := range c.analyzerKeys() {
		if c.purposeOf(k) == purpose {
			out = append(out, k)
		}
	}
	return out
}

func (c sectionContext) metrics() *MetricsSection {
	keys := c.purposeKeys(PurposeMetrics)
	s := &MetricsSection{Stage: c.stage(domain.StageStaticAnalysis, func(k string) bool { return slices.Contains(keys, k) }), TaskKeys: keys, Metrics: []MetricGroup{}}
	byName := map[string]int{}
	for _, o := range c.r.Observations {
		m := o.Metric
		if m == nil {
			continue
		}
		i, ok := byName[m.Name]
		if !ok {
			i = len(s.Metrics)
			byName[m.Name] = i
			s.Metrics = append(s.Metrics, MetricGroup{Name: m.Name, Unit: m.Unit, Thresholds: []int{}, ObservationIDs: []string{}})
		}
		g := &s.Metrics[i]
		g.ObservationIDs = append(g.ObservationIDs, o.ID)
		if !slices.Contains(g.Thresholds, m.Threshold) {
			g.Thresholds = append(g.Thresholds, m.Threshold)
		}
		if m.Value > g.MaxValue {
			g.MaxValue = m.Value
		}
	}
	for i := range s.Metrics {
		sort.Ints(s.Metrics[i].Thresholds)
	}
	sort.Slice(s.Metrics, func(a, b int) bool { return s.Metrics[a].Name < s.Metrics[b].Name })
	return s
}

func (c sectionContext) secrets() *SecretsSection {
	keys := c.purposeKeys(PurposeSecrets)
	s := &SecretsSection{Stage: c.stage(domain.StageStaticAnalysis, func(k string) bool { return slices.Contains(keys, k) }), TaskKeys: keys, Detections: []SecretDetection{}}
	findingOf := map[string]string{}
	for _, k := range keys {
		mine := map[string]bool{}
		for _, id := range c.states[k].toolFindings {
			mine[id] = true
		}
		for _, f := range c.r.Findings {
			if mine[f.ID] {
				for _, o := range f.ObservationIDs {
					findingOf[o] = f.ID
				}
			}
		}
	}
	for _, o := range c.r.Observations {
		if !slices.Contains(keys, o.TaskKey) || o.Metric != nil {
			continue
		}
		d := SecretDetection{ObservationID: o.ID, TaskKey: o.TaskKey, RuleID: o.RuleID, EvidenceState: o.EvidenceState}
		if l := o.Location; l != nil {
			d.Path, d.StartLine, d.EndLine = strPtr(l.Path), l.StartLine, l.EndLine
		}
		if f, ok := findingOf[o.ID]; ok {
			d.FindingID = strPtr(f)
		}
		s.Detections = append(s.Detections, d)
	}
	s.ModelInput = c.screen()
	return s
}

// screen reads how the review roles screened their inputs, from the task
// reasons and the limitations of the review records the report decoded.
func (c sectionContext) screen() string {
	withheld, unscreened, decoded := false, false, false
	for _, k := range c.keys {
		st := c.states[k]
		if !strings.HasPrefix(k, "review.") {
			continue
		}
		if st.facts.Reason != nil && *st.facts.Reason == "secret_screen_failed" {
			// Runs of earlier workers skipped review when the secrets result
			// was unusable; current workers review unscreened instead.
			return ScreenFailed
		}
		var lims []string
		switch rr := st.reviewResult; {
		case rr == nil || st.rejected:
			continue
		case rr.Record != nil:
			lims, decoded = rr.Record.Limitations, decoded || len(rr.Record.Calls) > 0
		}
		withheld = withheld || slices.Contains(lims, "secret_files_withheld")
		unscreened = unscreened || slices.Contains(lims, "model_input_not_secret_screened")
	}
	switch {
	case unscreened:
		// Not screened wins over withheld: a failed or partial secrets
		// result withholds what it reported but screened nothing else.
		return ScreenNotScreened
	case withheld:
		return ScreenWithheld
	case !decoded:
		return ScreenNoModel
	}
	// A completed secrets scan the report accepted, with nothing withheld,
	// screened the inputs (builder 6; earlier builders read this case as
	// not screened).
	for _, k := range c.purposeKeys(PurposeSecrets) {
		if st := c.states[k]; st.facts.Status == string(domain.TaskSucceeded) && !st.rejected && st.tool != nil && !st.tool.PartialParse {
			return ScreenScreened
		}
	}
	// The records sent inputs and recorded no screening state: the run had no
	// secrets task to screen with and predates the limitation code.
	return ScreenNotScreened
}

func (c sectionContext) codeReview() *CodeReviewSection {
	s := &CodeReviewSection{Stage: c.stage(domain.StageCodeReview, nil), Roles: []AIReviewTask{}, InventoryFiles: c.r.Coverage.InventoryFiles,
		ExposedFiles: c.r.Coverage.LLMExposedFiles, FindingIDs: []string{}, Limits: []InterpretationLimit{}}
	ai := c.r.Coverage.AIReview
	for _, t := range append(append([]AIReviewTask{}, ai.RequiredTasks...), ai.OptionalTasks...) {
		// The scope and slice tasks are the ledger's rows, not roles.
		if t.Key == record.TaskScope || isSliceTask(t.Key) {
			continue
		}
		if t.Key != "review.environment" && t.Key != "review.documents" && (c.reviewPlanned || t.Result != "not_planned") {
			s.Roles = append(s.Roles, t)
		}
	}
	sort.SliceStable(s.Roles, func(a, b int) bool { return roleOrder(s.Roles[a].Key) < roleOrder(s.Roles[b].Key) })
	for _, call := range c.r.Coverage.ReviewCalls {
		s.Calls++
		if call.Outcome == record.OutcomeAccepted {
			s.AcceptedCalls++
		}
		if call.PackID != "" && s.PackID == nil {
			s.PackID, s.PackVersion = strPtr(call.PackID), strPtr(call.PackVersion)
		}
	}
	if c.reviewPlanned {
		bytes := 0
		for _, k := range c.keys {
			st := c.states[k]
			if !strings.HasPrefix(k, "review.") || (st.rejected && !st.contractLineageRejected) ||
				st.reviewResult == nil || st.reviewResult.Record == nil {
				continue
			}
			for _, call := range st.reviewResult.Record.Calls {
				if k == record.TaskContract && call.RequestDigest == "" {
					continue
				}
				bytes += call.RequestBytes
			}
		}
		s.RequestBytes = &bytes
	}
	tool := map[string]bool{}
	for _, st := range c.states {
		for _, id := range st.toolFindings {
			tool[id] = true
		}
	}
	for _, f := range c.r.Findings {
		if !tool[f.ID] {
			s.FindingIDs = append(s.FindingIDs, f.ID)
		}
	}
	if st := c.states["review.interpret"]; reviewAccepted(st) {
		for _, l := range st.reviewResult.Record.Limits {
			s.Limits = append(s.Limits, InterpretationLimit{ObservationIDs: append([]string{}, l.ObservationIDs...), Statement: l.Statement})
		}
	}
	if c.reviewPlanned {
		s.Ledger = c.ledger()
	}
	c.addStrengths(s)
	return s
}

func roleOrder(key string) int {
	for i, k := range []string{"review.investigate", "review.interpret", "review.verify", record.TaskContract} {
		if k == key {
			return i
		}
	}
	return 99
}
