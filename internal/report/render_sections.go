package report

import (
	"bytes"
	"fmt"
	"html/template"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// The sectioned layout of reports built with SectionsSince or later
// (docs/REPORT.md#분석-종류별-절): the overview first, then one section per
// analysis kind, the consolidated gaps and the raw records. Both text
// formats render the same section model, so every number is read once from
// the stored blocks. A report without sections keeps the flat layout.

// secView is the translated section model shared by Markdown and HTML.
type secView struct {
	view
	Sections       []secBlock
	Appendix       []secPart
	OverviewDetail []secPart
	Decision       projectDecision
	// Summary selects the summary layout (render_summary.go): the appendix
	// is Appendix instead of the full record tables.
	Summary bool
}

// secBlock is one top-level section.
type secBlock struct {
	ID, Title string
	State     string // translated state with its icon; "" for none
	Parts     []secPart
}

// secPart is one piece of a section, rendered in field order: heading,
// paragraphs, the model-interpretation label, counts, table, list, findings.
type secPart struct {
	Sources  []sourceCitation
	Heading  string
	Anchor   string
	Paras    []string
	Model    string
	Counts   []countRow
	Table    *secTable
	List     []secItem
	Findings []Finding
	// Cards are the short finding cards of the summary layout.
	Cards []secCard
	Tone  string
	// Empty is shown instead of an empty table, list or finding list;
	// ShowEmpty is set when the part has none of them.
	Empty     string
	ShowEmpty bool
}

type secTable struct {
	Head []string
	Rows [][]secCell
	// ObservationIDs, when set, name the observation of each row (HTML row
	// id and data-observation).
	ObservationIDs []string
	// RowIDs, when set, are the HTML row ids of the summary layout's rows
	// (a finding or a detection the overview links to).
	RowIDs []string
}

// secCard is one finding of the summary layout: its title and a few lines.
type secCard struct {
	ID, Title string
	State     string
	Lines     []secItem
}

// secCell is plain text or a code span; Anchor links to an in-page id.
type secCell struct {
	Text   string
	Code   bool
	Anchor string
}

// secItem is one list entry: text followed by code spans.
type secItem struct {
	Label   string
	Text    string
	Codes   []string
	Link    string // in-page anchor of the entry's subject
	Sources []sourceCitation
}

func txt(s string) secCell  { return secCell{Text: s} }
func code(s string) secCell { return secCell{Text: s, Code: true} }
func link(s string) secCell { return secCell{Text: s, Code: true, Anchor: s} }
func num(n int) secCell     { return secCell{Text: strconv.Itoa(n)} }

// lab translates prefix.code, falling back to the bare code.
func lab(l Locale, prefix, c string) string {
	if t, ok := l.Labels[prefix+"."+c]; ok {
		return t
	}
	return c
}

func tf(l Locale, key string, args ...any) string { return fmt.Sprintf(l.Label(key), args...) }

// attentionText is the sentence of an attention item. A template without a
// verb (model_review_not_performed carries a count of 1 that means "present")
// is used as is: Sprintf would append %!(EXTRA int=1) for the unused count.
func attentionText(l Locale, code string, count int) string {
	tmpl := l.Label("attention." + code)
	if !strings.Contains(tmpl, "%") {
		return tmpl
	}
	return fmt.Sprintf(tmpl, count)
}

func optText(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func optInt(p *int, def string) string {
	if p == nil {
		return def
	}
	return strconv.Itoa(*p)
}

// reasonText is a reason code with its translation when one exists.
func reasonText(l Locale, c string) string {
	if t := l.Code(c); t != c {
		return t
	}
	if t, ok := l.Labels["model_reason."+c]; ok {
		return t
	}
	return c
}

func stageLine(l Locale, st SectionStage) string {
	s := lab(l, "state", st.State)
	if st.State == StateNotRecorded {
		return tf(l, "state_line_unstaged", s, st.Tasks)
	}
	return tf(l, "state_line", s, st.Tasks, st.Succeeded)
}

func countsOf(l Locale, section string, counts map[string]int, order []string) []countRow {
	var out []countRow
	for _, k := range order {
		if v, ok := counts[k]; ok {
			out = append(out, countRow{Name: section + "." + k, Label: lab(l, "count."+section, k), Value: strconv.Itoa(v)})
		}
	}
	return out
}

// countOrder is the display order of each section's overview counts.
var countOrder = map[string][]string{
	SectionEnvironment:    {"modules", "requirements", "satisfied", "mismatch", "unknown", "dependencies", "dependencies_prepared", "notes"},
	SectionDocuments:      {"documents", "read", "not_read", "modules_without_documents", "contracts", "conflicts", "questions", "instruction_notes"},
	SectionStaticAnalysis: {"tools", "succeeded", "not_succeeded", "observations", "findings", "actionable", "pending"},
	SectionMetrics:        {"tools", "observations", "metrics"},
	SectionSecrets:        {"tools", "detections", "files"},
	SectionCodeReview: {"calls", "files_sent_to_model", "files_not_sent_to_model", "primary_modules", "primary_files", "primary_files_sent_to_model",
		"secondary_files", "secondary_files_not_reached", "findings", "actionable", "pending", "refuted"},
	SectionGaps: {"gaps"},
}

// dependencyPart is the dependency table: one row per deps.* task with what
// it prepared, or why not.
func dependencyPart(l Locale, deps []EnvDependency) secPart {
	t := &secTable{Head: []string{l.Label("col.module"), l.Label("col.manager"), l.Label("col.state"), l.Label("col.prepared"), l.Label("col.reason"), l.Label("col.limitations")}}
	for _, d := range deps {
		module, manager, state, counts := d.TaskKey, "-", lab(l, "task_status", d.TaskStatus), "-"
		if d.Module != nil {
			module = *d.Module
		}
		if d.Manager != nil {
			manager = *d.Manager
			if d.ManagerVersion != nil {
				// go reports "go1.27.4": show "go 1.27.4", not the name twice.
				manager += " " + strings.TrimPrefix(*d.ManagerVersion, *d.Manager)
			}
		}
		if d.State != nil {
			state = lab(l, "dep_state", *d.State)
		}
		if d.Prepared != nil {
			counts = strconv.Itoa(*d.Prepared)
			if d.Declared != nil {
				counts += "/" + strconv.Itoa(*d.Declared)
			}
			if d.Failed != nil && *d.Failed > 0 {
				counts += " " + tf(l, "dep_failed", *d.Failed)
			}
		}
		reason := "-"
		switch {
		case d.Reason != nil:
			reason = reasonText(l, *d.Reason)
		case d.TaskReason != nil:
			reason = reasonText(l, *d.TaskReason)
		case d.Result != DiscoveryAccepted:
			reason = lab(l, "result", d.Result)
		}
		var lims []string
		for _, c := range d.Limitations {
			lims = append(lims, l.Code(c))
		}
		lim := "-"
		if len(lims) > 0 {
			lim = strings.Join(lims, "; ")
		}
		t.Rows = append(t.Rows, []secCell{code(module), code(manager), txt(state), txt(counts), txt(reason), txt(lim)})
	}
	return secPart{Heading: l.Label("env.dependencies"), Table: t}
}

func modelPart(l Locale, m ModelInterpretation) secPart {
	if m.State == ModelPerformed {
		return secPart{Model: tf(l, "model.performed", m.TaskKey)}
	}
	return secPart{Paras: []string{tf(l, "model.not_performed", reasonText(l, optText(m.Reason, m.State)))}}
}

func listPart(heading string, items []secItem, empty string) secPart {
	return secPart{Heading: heading, List: items, Empty: empty}
}

func newSecView(r Report, digest string, l Locale) secView {
	v := secView{view: newView(r, digest, l)}
	findings := map[string]Finding{}
	for _, f := range r.Findings {
		findings[f.ID] = f
	}
	pick := func(ids []string) []Finding {
		out := []Finding{}
		for _, f := range r.Findings {
			for _, id := range ids {
				if f.ID == id {
					out = append(out, f)
				}
			}
		}
		return out
	}
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	title := func(k string) string { return lab(l, "section", k) }
	states := map[string]string{}
	for _, s := range r.Overview.Sections {
		if s.State != nil {
			states[s.Key] = lab(l, "state", *s.State)
		}
	}

	// Overview.
	ov := secBlock{ID: "overview", Title: title("overview")}
	ov.Parts = append(ov.Parts, secPart{Paras: append([]string{tf(l, "overview_lead", lab(l, "status", r.Status), lab(l, "verdict", r.QualityVerdict))}, pullRequestParas(r, l)...), Counts: v.Counts})
	t := &secTable{Head: []string{l.Label("col.section"), l.Label("col.state"), l.Label("col.counts")}}
	for _, s := range r.Overview.Sections {
		var parts []string
		for _, c := range countsOf(l, s.Key, s.Counts, countOrder[s.Key]) {
			parts = append(parts, c.Label+" "+c.Value)
		}
		st := "-"
		if s.State != nil {
			st = lab(l, "state", *s.State)
		}
		t.Rows = append(t.Rows, []secCell{{Text: title(s.Key), Anchor: s.Key}, txt(st), txt(strings.Join(parts, " · "))})
	}
	ov.Parts = append(ov.Parts, secPart{Heading: l.Label("overview_sections"), Table: t})
	var attention []secItem
	for _, a := range r.Overview.Attention {
		attention = append(attention, secItem{Text: attentionText(l, a.Code, a.Count) + " " + tf(l, "attention_see", title(a.Section)), Link: a.Section})
	}
	ov.Parts = append(ov.Parts, listPart(l.Label("overview_attention"), attention, l.Label("overview_no_attention")))
	if len(r.Summary.Priorities) > 0 {
		var pr []secItem
		for _, id := range r.Summary.Priorities {
			f := findings[id]
			pr = append(pr, secItem{Text: f.Title + " — " + f.Validation + " / " + f.Severity, Codes: []string{id}, Link: id})
		}
		ov.Parts = append(ov.Parts, listPart(l.Label("overview_priorities"), pr, ""))
	}
	v.Sections = append(v.Sections, ov)

	// Environment.
	e := r.Environment
	env := secBlock{ID: SectionEnvironment, Title: title(SectionEnvironment), State: states[SectionEnvironment]}
	env.Parts = append(env.Parts, secPart{Paras: []string{stageLine(l, e.Stage), lab(l, "discovery", e.Discovery)}, Counts: countsOf(l, SectionEnvironment, overviewCounts(r, SectionEnvironment), countOrder[SectionEnvironment])})
	if e.Discovery == DiscoveryAccepted {
		mt := &secTable{Head: []string{l.Label("col.module"), l.Label("col.ecosystem"), l.Label("col.path"), l.Label("col.in_scope")}}
		for _, m := range e.Modules {
			mt.Rows = append(mt.Rows, []secCell{code(m.ID), code(m.Ecosystem), code(m.Path), txt(lab(l, "in_scope", strconv.FormatBool(m.InScope)))})
		}
		env.Parts = append(env.Parts, secPart{Heading: l.Label("env.modules"), Table: mt})
		qt := &secTable{Head: []string{l.Label("col.module"), l.Label("col.tool"), l.Label("col.constraint"), l.Label("col.comparison"), l.Label("col.source")}}
		for _, q := range e.Requirements {
			qt.Rows = append(qt.Rows, []secCell{code(q.Module), code(q.Tool), code(optText(q.Constraint, l.Label("constraint_withheld"))), code(q.Comparison), code(q.Path + ":" + strconv.Itoa(q.StartLine))})
		}
		env.Parts = append(env.Parts, secPart{Heading: l.Label("env.requirements"), Table: qt, Empty: l.Label("none")})
		ot := &secTable{Head: []string{l.Label("col.tool"), l.Label("col.status"), l.Label("col.version"), l.Label("col.detail")}}
		for _, o := range e.Observed {
			ot.Rows = append(ot.Rows, []secCell{code(o.Tool), txt(lab(l, "obs", o.Status)), code(optText(o.Version, "-")), code(optText(o.Detail, "-"))})
		}
		env.Parts = append(env.Parts, secPart{Heading: l.Label("env.observed"), Table: ot, Empty: l.Label("none")})
		vt := &secTable{Head: []string{l.Label("col.module"), l.Label("col.tool"), l.Label("col.requirement"), l.Label("col.status"), l.Label("col.reason")}}
		for _, x := range e.Verdicts {
			reason := "-"
			if x.Reason != nil {
				reason = *x.Reason
			}
			vt.Rows = append(vt.Rows, []secCell{code(x.Module), code(x.Tool), code(optText(x.Requirement, "-")), txt(lab(l, "verdict_env", x.Status)), code(reason)})
		}
		env.Parts = append(env.Parts, secPart{Heading: l.Label("env.verdicts"), Table: vt, Empty: l.Label("none")})
		at := &secTable{Head: []string{l.Label("col.tool"), l.Label("col.analyzers"), l.Label("col.status"), l.Label("col.version"), l.Label("col.detail")}}
		for _, a := range e.AnalyzerTools {
			at.Rows = append(at.Rows, []secCell{code(a.Tool), code(strings.Join(a.Analyzers, ", ")), txt(lab(l, "obs", a.Status)), code(optText(a.Version, "-")), code(optText(a.Detail, "-"))})
		}
		env.Parts = append(env.Parts, secPart{Heading: l.Label("env.analyzer_tools"), Table: at, Empty: l.Label("none")})
		if len(e.Limitations) > 0 {
			var items []secItem
			for _, c := range e.Limitations {
				items = append(items, codeItem(l, c))
			}
			env.Parts = append(env.Parts, listPart(l.Label("env.limitations"), items, ""))
		}
	}
	if len(e.Dependencies) > 0 {
		env.Parts = append(env.Parts, dependencyPart(l, e.Dependencies))
	}
	mp := modelPart(l, e.Model)
	if e.Model.State == ModelPerformed {
		mp.Heading = l.Label("env.notes")
		for _, n := range e.Notes {
			mp.List = append(mp.List, secItem{Text: tf(l, "env.note", n.Module, n.AffectedChecks, n.StillCheckable, n.NeededObservation),
				Sources: v.sourceCitations(n.EvidenceIDs, e.Evidence)})
		}
		mp.Empty = l.Label("none")
	}
	env.Parts = append(env.Parts, mp)
	v.Sections = append(v.Sections, env)

	// Documents.
	d := r.Documents
	docs := secBlock{ID: SectionDocuments, Title: title(SectionDocuments), State: states[SectionDocuments]}
	lead := []string{stageLine(l, d.Stage), lab(l, "discovery", d.Discovery)}
	if b := d.Budget; b != nil {
		lead = append(lead, tf(l, "docs.budget", b.PerDocumentBytes, b.TotalBytes, b.MaxDocuments, b.ReadBytes))
	}
	docs.Parts = append(docs.Parts, secPart{Paras: lead, Counts: countsOf(l, SectionDocuments, overviewCounts(r, SectionDocuments), countOrder[SectionDocuments])})
	if d.Discovery == DiscoveryAccepted {
		it := &secTable{Head: []string{l.Label("col.path"), l.Label("col.class"), l.Label("col.applies_to"), l.Label("col.status"), l.Label("col.reason"), l.Label("col.size"), l.Label("col.lines")}}
		for _, x := range d.Items {
			size := "-"
			if x.SizeBytes != nil {
				size = strconv.FormatInt(*x.SizeBytes, 10)
			}
			it.Rows = append(it.Rows, []secCell{code(x.Path), code(x.Class), txt(displayDocumentScope(x.AppliesTo)), txt(lab(l, "doc_status", x.Status)), code(optText(x.Reason, "-")), txt(size), txt(optInt(x.LineCount, "-"))})
		}
		docs.Parts = append(docs.Parts, secPart{Heading: l.Label("docs.items"), Table: it, Empty: l.Label("none")})
		dm := &secTable{Head: []string{l.Label("col.module"), l.Label("col.status"), l.Label("col.own"), l.Label("col.inherited")}}
		for _, m := range d.Modules {
			dm.Rows = append(dm.Rows, []secCell{code(m.Module), txt(lab(l, "doc_state", m.State)), code(joinOr(m.Own, "-")), code(joinOr(m.Inherited, "-"))})
		}
		docs.Parts = append(docs.Parts, secPart{Heading: l.Label("docs.modules"), Table: dm, Empty: l.Label("none")})
		if len(d.Overlaps) > 0 {
			ol := &secTable{Head: []string{l.Label("col.path"), l.Label("col.documents"), l.Label("col.assessment")}}
			for _, o := range d.Overlaps {
				assessment := lab(l, "overlap", o.Assessment)
				if d.Model.State == ModelPerformed {
					// The documents role read these guidance sets; its
					// conflicts below are the assessment.
					assessment = l.Label("overlap.read_by_model")
				}
				ol.Rows = append(ol.Rows, []secCell{code(o.Path), code(strings.Join(o.Documents, ", ")), txt(assessment)})
			}
			docs.Parts = append(docs.Parts, secPart{Heading: l.Label("docs.overlaps"), Table: ol})
		}
		if len(d.Limitations) > 0 {
			var items []secItem
			for _, c := range d.Limitations {
				items = append(items, codeItem(l, c))
			}
			docs.Parts = append(docs.Parts, listPart(l.Label("docs.limitations"), items, ""))
		}
	}
	docs.Parts = append(docs.Parts, modelPart(l, d.Model))
	if d.Model.State == ModelPerformed {
		var items []secItem
		for _, x := range d.Contracts {
			if !comparisonCoversContract(d.Comparisons, x) {
				items = append(items, secItem{Text: scopedDocumentText(x.AppliesTo, x.Statement), Sources: v.sourceCitations(x.EvidenceIDs, d.Evidence)})
			}
		}
		if len(items) > 0 {
			docs.Parts = append(docs.Parts, listPart(l.Label("docs.contracts"), items, ""))
		}
		items = nil
		for _, x := range d.Conflicts {
			items = append(items, secItem{Text: scopedDocumentText(x.Scope, tf(l, "docs.conflict_detail", x.Summary, x.Impact)),
				Sources: v.sourceCitations(append(slices.Clone(x.FirstEvidence), x.SecondEvidence...), d.Evidence)})
		}
		docs.Parts = append(docs.Parts, listPart(l.Label("docs.conflicts"), items, l.Label("none")))
		items = nil
		for _, x := range d.Questions {
			items = append(items, secItem{Text: scopedDocumentText(x.AppliesTo, x.Question), Sources: v.sourceCitations(x.EvidenceIDs, d.Evidence)})
		}
		docs.Parts = append(docs.Parts, listPart(l.Label("docs.questions"), items, l.Label("none")))
		items = nil
		for _, x := range d.InstructionNotes {
			items = append(items, secItem{Text: x.Note, Sources: v.sourceCitations(x.EvidenceIDs, d.Evidence)})
		}
		docs.Parts = append(docs.Parts, listPart(l.Label("docs.instruction_notes"), items, l.Label("none")))
	}
	v.Sections = append(v.Sections, docs)

	// Static analysis.
	s := r.StaticAnalysis
	st := secBlock{ID: SectionStaticAnalysis, Title: title(SectionStaticAnalysis), State: states[SectionStaticAnalysis]}
	st.Parts = append(st.Parts, secPart{Paras: []string{stageLine(l, s.Stage), l.Label("static.lead")}, Counts: countsOf(l, SectionStaticAnalysis, overviewCounts(r, SectionStaticAnalysis), countOrder[SectionStaticAnalysis])})
	var diag []string
	for _, lang := range s.Languages {
		heading := l.Label("static.language_unknown")
		if lang.Ecosystem != nil {
			heading = tf(l, "static.language", *lang.Ecosystem)
			if *lang.Ecosystem == "any" {
				heading = l.Label("static.language_any")
			}
		}
		tt := &secTable{Head: []string{l.Label("col.task"), l.Label("col.tool"), l.Label("col.required"), l.Label("col.status"), l.Label("col.result"), l.Label("col.version"), l.Label("col.files"), l.Label("col.diagnostics"), l.Label("col.rules")}}
		for _, x := range lang.Tools {
			status := lab(l, "task_status", x.TaskStatus)
			if x.TaskReason != nil {
				status += " · " + reasonText(l, *x.TaskReason)
			}
			result := lab(l, "result", x.Result)
			if x.ExitClass != nil {
				result += " · " + *x.ExitClass
			}
			files := optInt(x.FilesInScope, l.Label("unknown")) + " / " + optInt(x.ProcessedFiles, l.Label("unknown"))
			var rules []string
			for _, rt := range x.Rules {
				rules = append(rules, rt.RuleID+" "+strconv.Itoa(rt.Count))
			}
			diagnostics := strconv.Itoa(len(x.ObservationIDs))
			switch {
			case x.TaskStatus != string(domain.TaskSucceeded) || x.Result != DiscoveryAccepted:
				// Not run or not accepted: nothing was analyzed, never "0".
				diagnostics = l.Label("not_run")
			case optText(x.Purpose, "") == PurposeMetrics:
				diagnostics = tf(l, "static.metric_count", x.MetricObservations)
			case optText(x.Purpose, "") == PurposeSecrets:
				diagnostics = tf(l, "static.secret_count", x.SecretObservations)
			}
			tool := optText(x.Analyzer, "-")
			if x.Tool != nil {
				tool += " (" + *x.Tool + ")"
			}
			tt.Rows = append(tt.Rows, []secCell{code(x.TaskKey), code(tool), txt(lab(l, "required", strconv.FormatBool(x.Required))), txt(status), txt(result), code(optText(x.ToolVersion, "-")), txt(files), txt(diagnostics), code(joinOr(rules, "-"))})
			diag = append(diag, x.ObservationIDs...)
		}
		p := secPart{Heading: heading, Table: tt}
		if lang.Ecosystem != nil && *lang.Ecosystem == "any" {
			p.Paras = []string{l.Label("static.any_note")}
		}
		st.Parts = append(st.Parts, p)
	}
	st.Parts = append(st.Parts, v.observationsPart(l.Label("static.observations"), diag, obs, l.Label("static.no_observations")))
	st.Parts = append(st.Parts, secPart{Heading: l.Label("static.findings"), Findings: pick(StaticFindingIDs(r)), Empty: l.Label("static.no_findings")})
	v.Sections = append(v.Sections, st)

	// Metrics.
	m := r.Metrics
	mb := secBlock{ID: SectionMetrics, Title: title(SectionMetrics), State: states[SectionMetrics]}
	mb.Parts = append(mb.Parts, secPart{Paras: []string{stageLine(l, m.Stage), l.Label("metrics.lead")}, Counts: countsOf(l, SectionMetrics, overviewCounts(r, SectionMetrics), countOrder[SectionMetrics])})
	mb.Parts = append(mb.Parts, v.tasksPart(l.Label("metrics.tasks"), m.TaskKeys))
	gt := &secTable{Head: []string{l.Label("col.metric"), l.Label("col.unit"), l.Label("col.threshold"), l.Label("col.max"), l.Label("col.count")}}
	var mobs []string
	for _, g := range m.Metrics {
		var th []string
		for _, x := range g.Thresholds {
			th = append(th, strconv.Itoa(x))
		}
		gt.Rows = append(gt.Rows, []secCell{code(g.Name), code(g.Unit), txt(strings.Join(th, ", ")), num(g.MaxValue), num(len(g.ObservationIDs))})
		mobs = append(mobs, g.ObservationIDs...)
	}
	mb.Parts = append(mb.Parts, secPart{Heading: l.Label("metrics.groups"), Table: gt, Empty: l.Label("metrics.none")})
	if len(mobs) > 0 {
		mb.Parts = append(mb.Parts, v.observationsPart(l.Label("metrics.observations"), mobs, obs, ""))
	}
	v.Sections = append(v.Sections, mb)

	// Secrets.
	sc := r.Secrets
	sb := secBlock{ID: SectionSecrets, Title: title(SectionSecrets), State: states[SectionSecrets]}
	sb.Parts = append(sb.Parts, secPart{Paras: []string{stageLine(l, sc.Stage), l.Label("secrets.lead"), v.modelInputText(sc)}, Counts: countsOf(l, SectionSecrets, overviewCounts(r, SectionSecrets), countOrder[SectionSecrets])})
	sb.Parts = append(sb.Parts, v.tasksPart(l.Label("secrets.tasks"), sc.TaskKeys))
	dt := &secTable{Head: []string{l.Label("col.rule"), l.Label("col.location"), l.Label("col.evidence_state"), l.Label("col.finding")}, ObservationIDs: []string{}}
	for _, x := range sc.Detections {
		dt.ObservationIDs = append(dt.ObservationIDs, x.ObservationID)
		f := secCell{Text: "-"}
		if x.FindingID != nil {
			f = link(*x.FindingID)
		}
		dt.Rows = append(dt.Rows, []secCell{code(x.RuleID), code(detectionPlace(l, x)), code(x.EvidenceState), f})
	}
	sb.Parts = append(sb.Parts, secPart{Heading: l.Label("secrets.detections"), Table: dt, Empty: l.Label("secrets.none")})
	v.Sections = append(v.Sections, sb)

	// Code review.
	c := r.CodeReview
	cb := secBlock{ID: SectionCodeReview, Title: title(SectionCodeReview), State: states[SectionCodeReview]}
	lead = []string{stageLine(l, c.Stage)}
	if len(c.Roles) == 0 {
		lead = append(lead, l.Label("review.pack.none"))
	} else {
		lead = append(lead, tf(l, "review.pack", optText(c.PackID, "-")+"@"+optText(c.PackVersion, "-")), tf(l, "review.calls", c.Calls, c.AcceptedCalls))
		if c.RequestBytes != nil {
			lead = append(lead, tf(l, "review.request_bytes", *c.RequestBytes))
		} else {
			lead = append(lead, l.Label("review.request_bytes_unknown"))
		}
		lead = append(lead, tf(l, "review.exposed", optInt(c.ExposedFiles, l.Label("unknown")), c.InventoryFiles))
	}
	cb.Parts = append(cb.Parts, secPart{Paras: lead, Counts: countsOf(l, SectionCodeReview, overviewCounts(r, SectionCodeReview), countOrder[SectionCodeReview])})
	if len(c.Roles) > 0 {
		rt := &secTable{Head: []string{l.Label("col.role"), l.Label("col.task_status"), l.Label("col.result"), l.Label("col.reason")}}
		for _, x := range c.Roles {
			reason := optText(x.Reason, "-")
			detail := code(reason)
			if reason == "no_review_scope" {
				detail = txt(reasonText(l, reason))
			}
			rt.Rows = append(rt.Rows, []secCell{code(x.Key), txt(lab(l, "task_status", optText(x.TaskStatus, "-"))), code(x.Result), detail})
		}
		cb.Parts = append(cb.Parts, secPart{Heading: l.Label("review.roles"), Table: rt})
	}
	if c.Ledger != nil {
		cb.Parts = append(cb.Parts, ledgerParts(l, *c.Ledger)...)
	}
	cb.Parts = append(cb.Parts, secPart{Heading: l.Label("review.findings"), Findings: pick(c.FindingIDs), Empty: l.Label("review.no_findings")})
	if len(c.Limits) > 0 {
		lp := secPart{Heading: l.Label("review.limits"), Model: tf(l, "model.performed_observations", "review.interpret")}
		for _, x := range c.Limits {
			lp.List = append(lp.List, secItem{Text: x.Statement, Codes: x.ObservationIDs})
		}
		cb.Parts = append(cb.Parts, lp)
	}
	v.Sections = append(v.Sections, cb)

	// Gaps.
	gb := secBlock{ID: SectionGaps, Title: title(SectionGaps)}
	gt = &secTable{Head: []string{l.Label("col.section"), l.Label("col.gap"), l.Label("col.subject"), l.Label("col.detail")}}
	for _, g := range r.Gaps {
		detail := "-"
		if g.Detail != nil {
			detail = reasonText(l, *g.Detail)
		}
		gt.Rows = append(gt.Rows, []secCell{txt(title(g.Section)), txt(lab(l, "gap", g.Code)), code(optText(g.Subject, "-")), txt(detail)})
	}
	gb.Parts = append(gb.Parts, secPart{Paras: []string{l.Label("gaps.lead")}, Table: gt, Empty: l.Label("gaps.none")})
	v.Sections = append(v.Sections, gb)
	for i := range v.Sections {
		for j := range v.Sections[i].Parts {
			x := &v.Sections[i].Parts[j]
			x.ShowEmpty = x.Empty != "" && (x.Table == nil || len(x.Table.Rows) == 0) && len(x.List) == 0 && len(x.Findings) == 0
		}
	}
	return v
}

// ledgerParts render the code review ledger: its header, one row per module
// and the slice tasks with the modules they hold.
func ledgerParts(l Locale, x ReviewLedger) []secPart {
	unknown := l.Label("unknown")
	scope := lab(l, "task_status", optText(x.Scope.TaskStatus, "-"))
	if x.Scope.Reason != nil {
		scope += " — " + reasonText(l, *x.Scope.Reason)
	}
	head := secPart{Heading: l.Label("ledger.heading"), Paras: []string{l.Label("ledger.lead"), l.Label("ledger.status." + x.Status),
		tf(l, "ledger.scope", x.Scope.Key, scope), tf(l, "ledger.calls", optInt(x.CallsUsed, unknown), optInt(x.MaxCalls, unknown)),
		tf(l, "ledger.bytes", optInt(x.RequestBytesSent, unknown), optInt(x.MaxRequestBytes, unknown))}}
	mt := &secTable{Head: []string{l.Label("col.module"), l.Label("col.discovered"), l.Label("col.eligible"), l.Label("col.sent"), l.Label("col.bytes_sent"),
		l.Label("col.truncated"), l.Label("col.withheld"), l.Label("col.unreadable"), l.Label("col.budget_exhausted"), l.Label("col.not_planned"),
		l.Label("col.not_sent"), l.Label("col.completed")}}
	// Builder 8 classes each module and counts its secondary files: the
	// class and the secondary files left out within the ceiling get a column
	// so that each row adds up to the eligible files.
	classes := ledgerClasses(&x)
	if classes {
		mt.Head = slices.Insert(mt.Head, 1, l.Label("col.module_class"))
		mt.Head = slices.Insert(mt.Head, 11, l.Label("col.secondary_not_reached"))
	}
	var order []string
	slicesOf := map[string]LedgerSlice{}
	modulesOf := map[string][]string{}
	for _, m := range x.Modules {
		row := []secCell{code(m.ModuleID), txt(lab(l, "discovered", strconv.FormatBool(m.Discovered))), num(m.FilesEligible), num(m.FilesSentToModel), num(m.BytesSent), num(m.FilesTruncated),
			num(m.FilesWithheld), num(m.FilesUnreadable), num(m.FilesBudgetExhausted), num(m.FilesNotPlanned), num(m.FilesNotSent), num(m.FilesWithCompletedReview)}
		if classes {
			row = slices.Insert(row, 1, txt(lab(l, "class", m.Class)))
			row = slices.Insert(row, 11, num(m.FilesSecondaryNotReached))
		}
		mt.Rows = append(mt.Rows, row)
		for _, s := range m.Slices {
			if _, ok := slicesOf[s.TaskKey]; !ok {
				order = append(order, s.TaskKey)
				slicesOf[s.TaskKey] = s
			}
			modulesOf[s.TaskKey] = append(modulesOf[s.TaskKey], m.ModuleID)
		}
	}
	sort.Strings(order)
	st := &secTable{Head: []string{l.Label("col.task"), l.Label("col.status"), l.Label("col.reason"), l.Label("col.modules")}}
	for _, k := range order {
		s := slicesOf[k]
		reason := "-"
		if s.Reason != nil {
			reason = reasonText(l, *s.Reason)
		}
		st.Rows = append(st.Rows, []secCell{code(k), txt(lab(l, "task_status", s.Status)), txt(reason), code(strings.Join(modulesOf[k], ", "))})
	}
	return []secPart{head, {Heading: l.Label("ledger.modules"), Table: mt, Empty: l.Label("ledger.none")}, {Heading: l.Label("ledger.slices"), Table: st, Empty: l.Label("ledger.none")}}
}

func detectionPlace(l Locale, x SecretDetection) string {
	if x.Path == nil {
		return l.Label("none")
	}
	if x.StartLine == 0 {
		return *x.Path
	}
	return *x.Path + ":" + strconv.Itoa(x.StartLine) + "-" + strconv.Itoa(x.EndLine)
}

// codeItem is a translated code with the code itself; an untranslated code
// is shown once.
func codeItem(l Locale, c string) secItem {
	if t := l.Code(c); t != c {
		return secItem{Text: t, Codes: []string{c}}
	}
	return secItem{Codes: []string{c}}
}

func joinOr(list []string, def string) string {
	if len(list) == 0 {
		return def
	}
	return strings.Join(list, ", ")
}

// overviewCounts are the stored overview counts of one section.
func overviewCounts(r Report, key string) map[string]int {
	for _, s := range r.Overview.Sections {
		if s.Key == key {
			return s.Counts
		}
	}
	return nil
}

// modelInputText is the secrets section's sentence on how model inputs were
// screened. Inputs left unscreened because a secrets task ran but its result
// is unusable name that task with its state and reason; review then still
// ran on the read filters alone. Every other state keeps its fixed sentence.
func (v secView) modelInputText(sc *SecretsSection) string {
	l := v.L
	if sc.ModelInput == ScreenNotScreened {
		for _, k := range sc.TaskKeys {
			for _, x := range v.R.Tasks {
				if x.Key != k || x.Status == string(domain.TaskSucceeded) || x.Status == string(domain.TaskUnsupported) {
					continue
				}
				reason := "-"
				if x.Reason != nil {
					reason = reasonText(l, *x.Reason)
				}
				return tf(l, "secrets.model_input.not_screened_unusable", x.Key, lab(l, "task_status", x.Status), reason)
			}
		}
	}
	return lab(l, "secrets.model_input", sc.ModelInput)
}

// tasksPart lists the tasks of a section with their state and reason.
func (v secView) tasksPart(heading string, keys []string) secPart {
	l := v.L
	t := &secTable{Head: []string{l.Label("col.task"), l.Label("col.status"), l.Label("col.reason")}}
	for _, k := range keys {
		for _, x := range v.R.Tasks {
			if x.Key == k {
				reason := "-"
				if x.Reason != nil {
					reason = reasonText(l, *x.Reason)
				}
				t.Rows = append(t.Rows, []secCell{code(x.Key), txt(lab(l, "task_status", x.Status)), txt(reason)})
			}
		}
	}
	return secPart{Heading: heading, Table: t, Empty: l.Label("gap.not_planned")}
}

// observationsPart is the observation table of the given IDs.
func (v secView) observationsPart(heading string, ids []string, obs map[string]Observation, empty string) secPart {
	l := v.L
	t := &secTable{Head: []string{"id", l.Label("col.task"), l.Label("col.rule"), l.Label("col.location"), l.Label("col.evidence_state"), l.Label("col.message")}}
	for _, id := range ids {
		o := obs[id]
		loc := l.Label("none")
		if o.Location != nil {
			loc = sourceRange(o.Location.Path, o.Location.StartLine, o.Location.EndLine)
		}
		t.Rows = append(t.Rows, []secCell{{Text: o.ID, Code: true, Anchor: ""}, code(o.TaskKey), code(o.RuleID), code(loc), code(o.EvidenceState), txt(v.Message[o.ID])})
	}
	t.ObservationIDs = ids
	return secPart{Heading: heading, Table: t, Empty: empty}
}

// --- Markdown ----------------------------------------------------------------

func renderMarkdownSections(r Report, digest string, l Locale) []byte {
	v := withInsights(newSecView(r, digest, l))
	decision := v.projectDecision()
	var b strings.Builder
	p := func(s ...string) {
		for _, x := range s {
			b.WriteString(x)
		}
		b.WriteByte('\n')
	}
	mdHeader(p, r, v.view, l)
	for _, s := range v.Sections {
		mdSectionAnchor(p, s.ID)
		p("## ", mdText(s.Title))
		p()
		if s.ID == "overview" {
			mdDecision(p, decision, l)
		}
		for _, part := range s.Parts {
			mdComparisonAnchor(p, decision, part.Anchor)
			mdPart(p, part, l)
		}
	}
	p("## ", mdText(l.Label("section.appendix")))
	p()
	p(mdText(l.Label("appendix.lead")))
	p()
	mdAIReview(p, r, l, "### ")
	mdCoverage(p, r, v.view, l, "### ")
	mdTasks(p, r, l, "### ")
	mdAppendix(p, r, v.view, l, "### ")
	mdTechnical(p, r, v.view, l)
	p("digest sha256: ", mdCode(digest))
	return []byte(b.String())
}

func mdCell(c secCell) string {
	if c.Code {
		return mdCode(c.Text)
	}
	return mdText(c.Text)
}

func mdPart(p func(...string), part secPart, l Locale) {
	if part.Heading != "" {
		p("### ", mdText(part.Heading))
		p()
	}
	for _, x := range part.Paras {
		p(mdText(x))
		p()
	}
	if part.Model != "" {
		p("> ", mdText(part.Model))
		p()
	}
	if len(part.Counts) > 0 {
		p("| ", mdText(l.Label("col.item")), " | ", mdText(l.Label("col.count")), " |")
		p("|---|---|")
		for _, c := range part.Counts {
			p("| ", mdText(c.Label), " | ", c.Value, " |")
		}
		p()
	}
	if t := part.Table; t != nil && len(t.Rows) > 0 {
		var head, sep []string
		for _, h := range t.Head {
			head, sep = append(head, mdText(h)), append(sep, "---")
		}
		p("| ", strings.Join(head, " | "), " |")
		p("|", strings.Join(sep, "|"), "|")
		for _, row := range t.Rows {
			cells := make([]string, len(row))
			for i, c := range row {
				cells[i] = mdCell(c)
			}
			p("| ", strings.Join(cells, " | "), " |")
		}
		p()
	}
	if len(part.List) > 0 {
		for _, it := range part.List {
			mdItem(p, it)
		}
		p()
	}
	for _, f := range part.Findings {
		mdFinding(p, f, "#### ", true)
	}
	mdCitations(p, part.Sources)
	mdCards(p, part.Cards)
	if part.ShowEmpty {
		p(mdText(part.Empty))
		p()
	}
}

func mdItem(p func(...string), it secItem) {
	line := "-"
	if it.Label != "" {
		line += " **" + mdText(it.Label) + "**"
	}
	if it.Text != "" {
		line += " " + mdText(it.Text)
	}
	for _, c := range it.Codes {
		line += " " + mdCode(c)
	}
	var sources []sourceCitation
	for _, source := range it.Sources {
		if source.URL != "" && source.Text == "" {
			line += " [" + mdCode(source.Location) + "](<" + source.URL + ">)"
		} else {
			sources = append(sources, source)
		}
	}
	p(line)
	if len(sources) > 0 {
		p()
		mdCitations(func(s ...string) {
			for _, line := range strings.Split(strings.Join(s, ""), "\n") {
				if line == "" {
					p()
				} else {
					p("  ", line)
				}
			}
		}, sources)
	}
}

// --- HTML --------------------------------------------------------------------

var htmlSectionsTemplate = template.Must(template.New("sections").Funcs(htmlFuncs).Parse(`<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + savedHTMLContentSecurityPolicy + `">
<meta name="referrer" content="no-referrer">
<title>{{with .R.Project}}{{.Name}} · {{end}}{{.L.Label "title"}}</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>` + sectionedExportCSS + `</style>
</head>
<body class="review-theme report-export">
<header class="export-brand-bar"><div class="export-brand-inner report-brand">
<span class="report-mark">{{reportIcon "scan-eye"}}</span>
<strong>CODEYE</strong><span class="report-brand-label">{{.L.Label "report.document"}}</span>
</div></header>
<div class="report-layout export-page">
<nav class="report-rail" aria-label="{{.L.Label "report.navigation"}}">
<div class="export-desktop-nav"><p class="report-rail-label">{{.L.Label "report.navigation"}}</p>{{template "report_navigation" .}}</div>
<details class="export-mobile-nav">
<summary>{{reportIcon "menu-2"}} {{.L.Label "report.navigation"}} {{reportIcon "chevron-down"}}</summary>
{{template "report_navigation" .}}
</details>
</nav>
<main class="report-main">
<header class="report-hero">
{{if and .R.Project .R.Project.Name}}<h1 class="report-name">{{.R.Project.Name}}</h1>{{else}}<h1 class="report-name">{{.L.Label "title"}}</h1>{{end}}
{{with .HeaderSource}}<p class="export-target"><strong>{{$.L.Label "report.target"}}</strong><code>{{.}}</code></p>{{end}}
<div class="report-verdict">
<span class="badge">{{if .HasStatusBanner}}{{reportIcon "clock"}}{{else}}{{reportIcon "circle-check"}}{{end}} {{.ReportStatus}}</span>
</div>
{{$timing := .HeaderTiming}}{{if or $timing.Times .R.Inputs.CommitSHA}}<dl class="export-hero-meta">
{{range $timing.Times}}<div>{{reportIcon "clock"}}<dt>{{.Label}}</dt><dd><time datetime="{{.Original}}">{{.Display}}</time></dd></div>{{end}}
{{with $timing.Duration}}<div><dt>{{$.L.Label "identity.duration"}}</dt><dd>{{.}}</dd></div>{{end}}
{{with .R.Inputs.CommitSHA}}<div>{{reportIcon "git-commit"}}<dt>{{$.L.Label "identity.commit"}}</dt><dd><code title="{{.}}">{{short .}}</code></dd></div>{{end}}
</dl>{{end}}
{{if .HasStatusBanner}}<p class="report-hero-note" role="status">{{.Banner}}</p>{{end}}
{{if not .R.Export.IncludeCode}}<p class="report-hero-note">{{.L.Label "opening.code_free"}}</p>{{end}}
</header>
{{$l := .L}}{{range .Sections}}<section id="{{.ID}}" data-section="{{.ID}}" class="export-section">
<h2>{{.Title}}</h2>
{{if eq .ID "overview"}}
{{if $.Summary}}{{range .Parts}}{{template "part" (pair $l .)}}{{end}}{{end}}
{{template "decision" $}}
<details class="card report-disclosure">
<summary>{{reportIcon "chevron-down"}} {{$l.Label "opening.coverage_details"}}</summary>
{{if $.Summary}}{{range $.OverviewDetail}}{{template "part" (pair $l .)}}{{end}}{{else}}{{range .Parts}}{{template "part" (pair $l .)}}{{end}}{{end}}
</details>
{{else}}{{range .Parts}}{{template "part" (pair $l .)}}{{end}}{{end}}</section>
{{end}}<section id="appendix" class="export-section">
<h2>{{.L.Label "section.appendix"}}</h2>
{{if .Summary}}<details class="card report-disclosure"><summary>{{reportIcon "chevron-down"}} {{.L.Label "opening.run_details"}}</summary>
{{range .Appendix}}{{template "part" (pair $l .)}}{{end}}</details>
{{else}}<p>{{.L.Label "appendix.lead"}}</p>
<h3>{{.L.Label "ai_review"}}</h3>
<p>상태: <code data-field="ai_review">{{.R.Coverage.AIReview.Status}}</code> {{with .R.Coverage.AIReview.Reason}}<code>{{.}}</code>{{end}}</p>
{{if .R.Coverage.ReviewCalls}}<ul>{{range .R.Coverage.ReviewCalls}}<li>모델 호출 <code>{{.Role}}</code>{{if .Purpose}} ({{$l.Label (printf "call.%s" .Purpose)}}){{end}} · <code>{{.Model}}</code> · OpenCode <code>{{.Version}}</code> · <code>{{.Outcome}}</code> · 지침{{if .PackID}} <code>{{.PackID}}@{{.PackVersion}}</code>{{end}} <code>{{.PackDigest}}</code></li>{{end}}</ul>{{end}}
<h3>{{.L.Label "coverage"}}</h3>
<ul>
<li>scope: <code>{{.R.Coverage.ScopeMode}}</code></li>
<li>inventory_files: <span data-count="inventory_files">{{.R.Coverage.InventoryFiles}}</span></li>
<li>tool_processed_files: <span data-count="tool_processed_files">{{.Processed}}</span></li>
<li>llm_exposed_files: {{derefInt .R.Coverage.LLMExposedFiles "미확정"}}</li>
<li>test_status: <code>{{.R.Coverage.TestStatus}}</code></li>
{{range .Notes}}<li>{{.}}</li>{{end}}
</ul>
<h3>{{.L.Label "tasks"}}</h3>
<div class="scroll"><table><tr><th>task</th><th>필수</th><th>상태</th><th>사유</th></tr>
{{range .R.Tasks}}<tr><td><code>{{.Key}}</code></td><td>{{.RequiredForVerdict}}</td><td><code>{{.Status}}</code></td><td><code>{{deref .Reason "없음"}}</code></td></tr>
{{end}}</table></div>
<h3>{{.L.Label "executions"}}</h3>
<div class="scroll"><table><tr><th>실행</th><th>상태</th><th>exit</th><th>정리</th><th>argv</th><th>cwd</th><th>stdout</th><th>stderr</th></tr>
{{range .R.Executions}}<tr><td><code>{{.ID}}</code></td><td><code>{{.Status}}</code></td><td>{{derefInt .ExitCode "없음"}}</td><td><code>{{.Cleanup}}</code></td><td><code>{{join .Argv " "}}</code></td><td><code>{{.Cwd}}</code></td><td><code>{{artref .StdoutArtifactID .StdoutTruncated}}</code></td><td><code>{{artref .StderrArtifactID .StderrTruncated}}</code></td></tr>
{{end}}</table></div>
<h3>{{.L.Label "artifacts"}}</h3>
<div class="scroll"><table><tr><th>id</th><th>task</th><th>role</th><th>media</th><th>sha256</th><th>bytes</th><th>decoder</th></tr>
{{range .R.Artifacts}}<tr><td><code>{{.ID}}</code></td><td><code>{{deref .TaskKey "-"}}</code></td><td><code>{{.Role}}</code></td><td><code>{{.MediaType}}</code></td><td><code>{{.SHA256}}</code></td><td>{{i64 .SizeBytes}}</td><td><code>{{deref .Decoder "-"}}</code></td></tr>
{{end}}</table></div>
<h3>{{.L.Label "limitations"}}</h3>
<ul>{{range .Limitations}}<li>{{.}}</li>{{end}}</ul>
{{end}}</section>
<details class="card report-disclosure"><summary>{{reportIcon "chevron-down"}} {{.L.Label "identity.technical"}}</summary>
<div class="scroll"><table>
{{range .Identity}}<tr><th scope="row">{{.Label}}</th><td>{{.Value}}</td></tr>{{end}}
<tr><th>report</th><td><code>{{.R.ID}}</code> rev <span data-field="revision">{{.R.Revision}}</span></td></tr>
<tr><th>digest</th><td><code data-field="digest">{{.DigestShort}}</code></td></tr>
<tr><th>status</th><td><code data-field="status">{{.R.Status}}</code></td></tr>
<tr><th>quality_verdict</th><td><code data-field="quality_verdict">{{.R.QualityVerdict}}</code></td></tr>
<tr><th>run</th><td><code>{{.R.RunID}}</code></td></tr>
<tr><th>attempt</th><td><code>{{.R.Attempt.ID}}</code> #{{i64 .R.Attempt.Number}} epoch {{i64 .R.Attempt.LeaseEpoch}}</td></tr>
<tr><th>snapshot</th><td><code>{{.R.Inputs.SnapshotID}}</code> manifest <code>{{.ManifestShort}}</code></td></tr>
{{with .R.Inputs.CommitSHA}}<tr><th>commit</th><td><code>{{.}}</code></td></tr>{{end}}
<tr><th>profile</th><td><code>{{.R.Inputs.Profile}}</code></td></tr>
<tr><th>created_at</th><td><code>{{.R.CreatedAt}}</code></td></tr>
<tr><th>export</th><td>{{if .R.Export.IncludeCode}}{{.L.Label "export_full"}}{{else}}{{.L.Label "export_codefree"}}{{end}}</td></tr>
</table></div>
<p>digest sha256: <code>{{.Digest}}</code></p>
</details>
<details class="card report-disclosure report-licenses">
<summary>{{reportIcon "chevron-down"}} {{.L.Label "report.licenses"}}</summary>
{{range .AssetNotices}}<h3>{{.Name}}</h3><pre>{{.Text}}</pre>{{end}}
</details>
</main>
</div>
</body>
</html>
{{define "report_navigation"}}<ul class="nav nav-vertical">
{{range .Sections}}<li><a href="#{{.ID}}" class="nav-link">{{reportIcon (sectionIcon .ID)}}<span>{{.Title}}</span></a></li>{{end}}
<li><a href="#appendix" class="nav-link">{{reportIcon "list-details"}}<span>{{.L.Label "section.appendix"}}</span></a></li>
</ul>{{end}}
{{define "decision"}}{{$l := .L}}{{with .Decision}}
<div class="card report-group report-decision" data-tone="{{.Tone}}">
<div class="card-body">
<div class="report-decision-state"><span class="report-group-mark">{{reportIcon (groupIcon .Tone)}}</span><span class="decision-label">{{.State}}</span>{{if and .Review (or (and (not .DocumentClaim) (not .ComparisonRows)) .AdditionalObservation)}}<span class="report-group-note" role="note">{{$l.Label "insight.interpretation"}}</span>{{end}}</div>
<h3>{{.Title}}</h3>
{{if .ComparisonRows}}<dl class="report-comparison-rows">{{range .ComparisonRows}}<div><dt>{{.Label}}</dt><dd>
<p>{{range inlineCodeParts .Text}}{{if .Code}}<code>{{.Text}}</code>{{else}}{{.Text}}{{end}}{{end}}</p>
{{if .Sources}}<div class="report-decision-sources">{{range .Sources}}{{template "citation" .}}{{end}}</div>{{end}}
</dd></div>{{end}}</dl>{{end}}
{{if .DocumentClaim}}<p class="report-decision-copy">{{.DocumentClaim}}</p>{{end}}
{{if .Reason}}<p class="report-decision-copy">{{if .ReasonLabel}}<strong>{{.ReasonLabel}}</strong> {{end}}{{.Reason}}</p>{{end}}
{{if .Condition}}<p class="report-decision-copy"><strong>{{$l.Label "decision.option"}}</strong> {{.Condition}}</p>{{end}}
{{if .Boundary}}<p class="report-decision-copy"><strong>{{$l.Label "decision.unconfirmed"}}</strong> {{.Boundary}}</p>{{end}}
{{if .Next}}<p class="report-decision-next"><strong>{{if eq .Tone "action"}}{{$l.Label "decision.next"}}{{else}}{{$l.Label "opening.verification"}}{{end}}</strong><span>{{.Next}}</span></p>{{end}}
{{if .Scope}}<p class="report-decision-copy">{{$l.Label "col.applies_to"}} <code>{{.Scope}}</code></p>{{end}}
{{if .Followup}}<p class="report-decision-copy">{{$l.Label "decision.followup"}}</p>{{end}}
{{if .Sources}}<div class="report-decision-sources">{{range .Sources}}{{template "citation" .}}{{end}}</div>{{end}}
{{if .Action}}<p class="report-decision-next"><strong>{{$l.Label "decision.priority_action"}}</strong> {{.Action.State}}{{if .Action.Title}} · {{.Action.Title}}{{end}}</p>
<div class="report-decision-sources">{{template "citation" .Action.Source}}</div>{{end}}
<div class="report-document-assessment">
<div class="report-document-assessment-header"><strong>{{$l.Label "decision.document_assessment"}}</strong>
{{if .ComparisonCounts}}<dl>{{range .ComparisonCounts}}<div><dt>{{.Label}}</dt><dd data-comparison-status="{{.Name}}">{{.Value}}</dd></div>{{end}}</dl>
{{else}}<p>{{$l.Label "decision.no_document_assessment"}}</p>{{end}}</div>
{{if .ComparisonExample}}<p class="report-document-assessment-example"><strong>{{$l.Label "decision.document_claim"}}</strong> {{range inlineCodeParts .ComparisonExample}}{{if .Code}}<code>{{.Text}}</code>{{else}}{{.Text}}{{end}}{{end}}</p>{{end}}
<a href="#{{or .ComparisonAnchor "documents"}}" class="btn btn-sm btn-ghost-primary report-card-action">{{$l.Label "decision.documents"}}{{reportIcon "chevron-right"}}</a></div>
{{if .Glossary}}<details class="report-glossary"><summary>{{reportIcon "chevron-down"}} {{$l.Label "decision.glossary"}}</summary><dl>
{{range .Glossary}}<div><dt>{{.Name}}</dt><dd>{{.Definition}}</dd></div>{{end}}
</dl></details>{{end}}
</div>
</div>
{{end}}{{end}}
{{define "part"}}{{$l := .L}}{{with .P}}{{if .Cards}}<section class="report-group" data-tone="{{.Tone}}">
<div class="report-group-title"><span class="report-group-mark">{{reportIcon (groupIcon .Tone)}}</span><h3>{{.Heading}}</h3>{{if .Model}}<p class="report-group-note" role="note">{{.Model}}</p>{{end}}</div>
{{else if .Heading}}<h3{{if .Anchor}} id="{{.Anchor}}" tabindex="-1"{{end}}>{{.Heading}}</h3>
{{end}}{{range .Paras}}<p>{{.}}</p>
{{end}}{{if and .Model (not .Cards)}}<p class="model" role="note">{{.Model}}</p>
{{end}}{{if .Counts}}<table><tr><th>{{$l.Label "col.item"}}</th><th>{{$l.Label "col.count"}}</th></tr>
{{range .Counts}}<tr><td>{{.Label}}</td><td data-count="{{.Name}}">{{.Value}}</td></tr>
{{end}}</table>
{{end}}{{if and .Table .Table.Rows}}<div class="scroll"><table><tr>{{range .Table.Head}}<th scope="col">{{.}}</th>{{end}}</tr>
{{$t := .Table}}{{range $i, $row := $t.Rows}}<tr{{if $t.ObservationIDs}} id="{{index $t.ObservationIDs $i}}" data-observation="{{index $t.ObservationIDs $i}}"{{end}}{{if $t.RowIDs}} id="{{index $t.RowIDs $i}}"{{end}}>{{range $row}}<td>{{if .Anchor}}<a href="#{{.Anchor}}">{{end}}{{if .Code}}<code>{{.Text}}</code>{{else}}{{.Text}}{{end}}{{if .Anchor}}</a>{{end}}</td>{{end}}</tr>
{{end}}</table></div>
{{end}}{{if .List}}<ul>{{range .List}}<li>{{template "item" .}}</li>{{end}}</ul>
{{end}}{{range .Sources}}{{template "citation" .}}{{end}}{{range .Findings}}<section class="card export-full-finding" id="{{.ID}}" data-finding="{{.ID}}">
<h4>{{.Title}}</h4>
<ul>
<li>id: <code>{{.ID}}</code></li>
<li>category / severity: <code>{{.Category}}</code> / <code>{{.Severity}}</code> — {{.SeverityReason}}</li>
<li>validation / disposition: <code>{{.Validation}}</code> / <code>{{.Disposition}}</code></li>
{{range .Locations}}<li>location: <code>{{.Path}}:{{.StartLine}}-{{.EndLine}}</code></li>{{end}}
{{range .Conditions}}<li>발생 조건 (<code>{{.Status}}</code>) {{.Statement}} <code>{{join .EvidenceIDs ","}}</code></li>{{end}}
{{if .CounterevidenceIDs}}<li>반대 근거 <code>{{join .CounterevidenceIDs ","}}</code></li>{{end}}
<li>impact: {{.Impact}}</li>
{{range .Claims}}<li>claim (<code>{{.Kind}}</code>): {{.Assertion}} <code>{{join .EvidenceIDs ","}}</code></li>{{end}}
{{range .Evidence}}<li>evidence <code>{{.ID}}</code> (<code>{{.Kind}}</code>): {{.Description}} {{with .ArtifactID}}<code>{{.}}</code>{{end}} {{with .JSONPointer}}<code>{{.}}</code>{{end}}{{if .Location}}{{template "citation" (findingCitation $l .)}}{{end}}</li>{{end}}
{{range .ObservationIDs}}<li>observation <a href="#{{.}}"><code>{{.}}</code></a></li>{{end}}
<li>recommendation: {{.Recommendation.Summary}}</li>
{{range .Verification}}<li>verification: {{.}}</li>{{end}}
{{range .Limitations}}<li>limitation: {{.}}</li>{{end}}
</ul></section>
{{end}}{{if .Cards}}<div class="report-cards">{{range .Cards}}<article class="card report-card"{{if .ID}} id="{{.ID}}" data-finding="{{.ID}}"{{end}}>
<div class="card-body">
{{if .State}}<span class="badge">{{.State}}</span>{{end}}
<h4 class="card-title">{{.Title}}</h4>
{{if .Lines}}<ul class="export-card-points">{{range .Lines}}<li>{{template "item" .}}</li>{{end}}</ul>{{end}}
</div>
</article>
{{end}}</div></section>
{{end}}{{if .ShowEmpty}}<p>{{.Empty}}</p>
{{end}}{{end}}{{end}}
{{define "item"}}{{if .Label}}<strong>{{.Label}}</strong> {{end}}{{if .Link}}<a href="#{{.Link}}">{{.Text}}</a>{{else}}{{.Text}}{{end}}{{range .Codes}} <code>{{.}}</code>{{end}}{{range .Sources}}{{template "citation" .}}{{end}}{{end}}
{{define "citation"}}<div class="report-evidence">
{{if .Example}}<p class="report-document-example"><strong>{{.ExampleLabel}}</strong> <code>{{.Example.Call}}</code> → <code>{{.Example.Output}}</code></p>{{end}}
{{if .Text}}<details class="report-excerpt">
<summary class="btn btn-sm btn-ghost-primary export-evidence-control" aria-label="{{.ActionLabel}} · {{.Location}}">{{template "citation_action" .}}{{reportIcon "chevron-down"}}</summary>
{{if .URL}}<a href="{{.URL}}" class="btn btn-sm btn-ghost-primary report-source-action" target="_blank" rel="noopener noreferrer">{{reportIcon "external-link"}}{{.LinkLabel}}</a>{{end}}
<p class="report-excerpt-label">{{.StoredLabel}} <code>{{.ExcerptLocation}}</code></p>
<pre tabindex="0" aria-label="{{.ExcerptLocation}}"><code>{{.Text}}</code></pre></details>
{{else if .URL}}<a href="{{.URL}}" class="btn btn-sm btn-ghost-primary report-source-action export-evidence-control" target="_blank" rel="noopener noreferrer" aria-label="{{.ActionLabel}} · {{.Location}}">{{template "citation_action" .}}{{reportIcon "external-link"}}</a>
{{else}}{{template "citation_file" .}}{{end}}
</div>{{end}}
{{define "citation_action"}}<span class="export-evidence-label">{{.ActionLabel}}</span>{{template "citation_file" .}}{{end}}
{{define "citation_file"}}<span class="report-file" title="{{.Location}}">{{reportIcon "file-description"}}<code class="report-file-name">{{.File}}</code><code class="report-file-lines">:{{.Range}}</code></span>{{end}}`))

type partArgs struct {
	L Locale
	P secPart
}

func renderHTMLSections(r Report, digest string, l Locale) ([]byte, error) {
	var buf bytes.Buffer
	v := withInsights(newSecView(r, digest, l))
	v.Decision = v.projectDecision()
	if err := htmlSectionsTemplate.Execute(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
