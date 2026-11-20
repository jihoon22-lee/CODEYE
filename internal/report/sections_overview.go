package report

import (
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

var gapSectionOrder = map[string]int{SectionEnvironment: 0, SectionDocuments: 1, SectionStaticAnalysis: 2, SectionMetrics: 3, SectionSecrets: 4, SectionCodeReview: 5, SectionReport: 6}

// gaps is the consolidated list of what was not analyzed or only partly,
// derived from the sections and the task rows.
func (c sectionContext) gaps() []Gap {
	var out []Gap
	add := func(section, code string, subject, detail *string) {
		out = append(out, Gap{Section: section, Code: code, Subject: subject, Detail: detail})
	}
	r := c.r
	for _, l := range r.Limitations {
		switch code, detail := splitCode(l); code {
		case LimResultManifestMissing, LimResultManifestRejected:
			add(SectionReport, code, nil, detail)
		}
	}
	if r.Coverage.TestStatus == "not_run" {
		add(SectionReport, "tests_not_run", nil, nil)
	}
	modelGap := func(section string, m ModelInterpretation) {
		if m.State != ModelPerformed && !(m.Reason != nil && domain.NothingToReview(*m.Reason)) {
			add(section, "model_not_performed", strPtr(m.TaskKey), m.Reason)
		}
	}
	if e := r.Environment; e != nil {
		if e.Discovery != DiscoveryAccepted {
			add(SectionEnvironment, "discovery_"+e.Discovery, strPtr(envdoc.TaskEnvironment), nil)
		}
		for _, v := range e.Verdicts {
			if v.Status == envdoc.VerdictUnknown {
				add(SectionEnvironment, "verdict_unknown", strPtr(v.Module+" "+v.Tool), v.Reason)
			}
		}
		for _, l := range e.Limitations {
			add(SectionEnvironment, "discovery_limitation", nil, strPtr(l))
		}
		c.dependencyGaps(add)
		modelGap(SectionEnvironment, e.Model)
	}
	if d := r.Documents; d != nil {
		if d.Discovery != DiscoveryAccepted {
			add(SectionDocuments, "discovery_"+d.Discovery, strPtr(envdoc.TaskDocuments), nil)
		}
		budget := false
		for _, x := range d.Items {
			if x.Status != envdoc.DocSelected {
				add(SectionDocuments, "document_"+x.Status, strPtr(x.Path), x.Reason)
				budget = budget || x.Status == envdoc.DocBudgetExceeded
			}
		}
		for _, l := range d.Limitations {
			// A limitation the list already shows per document, or one the
			// performed documents role answered (discovery itself never
			// compares guidance), is no further gap.
			if (l == "document_budget_exceeded" && budget) || (l == "semantic_conflicts_not_assessed" && d.Model.State == ModelPerformed) {
				continue
			}
			add(SectionDocuments, "discovery_limitation", nil, strPtr(l))
		}
		if d.Model.State != ModelPerformed {
			for _, o := range d.Overlaps {
				add(SectionDocuments, "overlap_not_assessed", strPtr(o.Path), nil)
			}
		}
		modelGap(SectionDocuments, d.Model)
	}
	if s := r.StaticAnalysis; s != nil {
		tools := 0
		for _, l := range s.Languages {
			for _, t := range l.Tools {
				tools++
				section := SectionStaticAnalysis
				switch deref(t.Purpose) {
				case PurposeMetrics:
					section = SectionMetrics
				case PurposeSecrets:
					section = SectionSecrets
				}
				subject := strPtr(t.TaskKey)
				switch {
				case t.TaskStatus != string(domain.TaskSucceeded):
					add(section, "task_"+t.TaskStatus, subject, t.TaskReason)
				case t.Result != DiscoveryAccepted:
					add(section, t.Result, subject, nil)
				default:
					if t.PartialParse {
						add(section, "partial_parse", subject, nil)
					}
					if t.ProcessedFiles == nil {
						add(section, "processed_files_unknown", subject, nil)
					}
				}
			}
		}
		if tools == 0 {
			add(SectionStaticAnalysis, "not_planned", nil, nil)
		}
	}
	if m := r.Metrics; m != nil && len(m.TaskKeys) == 0 && len(m.Metrics) == 0 {
		add(SectionMetrics, "not_planned", nil, nil)
	}
	if s := r.Secrets; s != nil {
		if len(s.TaskKeys) == 0 {
			add(SectionSecrets, "not_planned", nil, nil)
		}
		for _, k := range s.TaskKeys {
			if tc := c.states[k].tool; tc != nil {
				for _, l := range tc.Limitations {
					if l == "history_not_scanned" {
						add(SectionSecrets, l, strPtr(k), nil)
					}
				}
			}
		}
		switch s.ModelInput {
		case ScreenNotScreened, ScreenFailed:
			add(SectionSecrets, "model_input_"+s.ModelInput, nil, nil)
		}
	}
	if cr := r.CodeReview; cr != nil {
		if len(cr.Roles) == 0 {
			add(SectionCodeReview, "review_tasks_not_planned", nil, nil)
		}
		for _, t := range cr.Roles {
			if t.Result == "accepted" || (t.Reason != nil && domain.NothingToReview(*t.Reason)) {
				continue
			}
			add(SectionCodeReview, "role_"+t.Result, strPtr(t.Key), t.Reason)
		}
		if len(cr.Roles) > 0 && cr.ExposedFiles == nil {
			add(SectionCodeReview, "exposure_unknown", nil, nil)
		}
		if cr.Ledger != nil {
			ledgerGaps(cr.Ledger, add)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if gapSectionOrder[x.Section] != gapSectionOrder[y.Section] {
			return gapSectionOrder[x.Section] < gapSectionOrder[y.Section]
		}
		if x.Code != y.Code {
			return x.Code < y.Code
		}
		return deref(x.Subject) < deref(y.Subject)
	})
	return out
}

func splitCode(c string) (string, *string) {
	for i := 0; i < len(c); i++ {
		if c[i] == ':' {
			return c[:i], strPtr(c[i+1:])
		}
	}
	return c, nil
}

// findingsOf returns the findings of ids.
func findingsOf(r Report, ids []string) []Finding {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Finding
	for _, f := range r.Findings {
		if want[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

// StaticFindingIDs are the tool findings the static analysis section lists.
func StaticFindingIDs(r Report) []string {
	var out []string
	if r.StaticAnalysis != nil {
		for _, l := range r.StaticAnalysis.Languages {
			for _, t := range l.Tools {
				out = append(out, t.FindingIDs...)
			}
		}
	}
	return out
}

// overviewOf derives the 종합 section from the other sections. It is pure,
// so the validator recomputes it and every renderer shows the same numbers.
func overviewOf(r Report) Overview {
	o := Overview{Sections: []OverviewSection{}, Attention: []Attention{}}
	line := func(key string, st *SectionStage, counts map[string]int) {
		var state *string
		if st != nil {
			state = strPtr(st.State)
		}
		o.Sections = append(o.Sections, OverviewSection{Key: key, State: state, Counts: counts})
	}
	attention := map[string][]Attention{}
	need := func(rank, section, code string, n int) {
		if n > 0 {
			attention[rank] = append(attention[rank], Attention{Section: section, Code: code, Count: n})
		}
	}
	if e := r.Environment; e != nil {
		n := map[string]int{"modules": 0, "requirements": len(e.Requirements), "satisfied": 0, "mismatch": 0, "unknown": 0, "notes": len(e.Notes)}
		for _, m := range e.Modules {
			if m.InScope {
				n["modules"]++
			}
		}
		for _, v := range e.Verdicts {
			switch v.Status {
			case envdoc.VerdictSatisfied:
				n["satisfied"]++
			case envdoc.VerdictMismatch:
				n["mismatch"]++
			default:
				n["unknown"]++
			}
		}
		if len(e.Dependencies) > 0 {
			n["dependencies"], n["dependencies_prepared"] = len(e.Dependencies), 0
			for _, d := range e.Dependencies {
				if d.TaskStatus == "succeeded" {
					n["dependencies_prepared"]++
				}
			}
		}
		line(SectionEnvironment, &e.Stage, n)
		need("3", SectionEnvironment, "environment_mismatch", n["mismatch"])
	}
	if d := r.Documents; d != nil {
		n := map[string]int{"documents": len(d.Items), "read": 0, "not_read": 0, "modules_without_documents": 0,
			"contracts": len(d.Contracts), "conflicts": len(d.Conflicts), "questions": len(d.Questions), "instruction_notes": len(d.InstructionNotes)}
		for _, x := range d.Items {
			if x.Status == envdoc.DocSelected {
				n["read"]++
			} else {
				n["not_read"]++
			}
		}
		for _, m := range d.Modules {
			if m.State == envdoc.StateNone {
				n["modules_without_documents"]++
			}
		}
		line(SectionDocuments, &d.Stage, n)
		need("4", SectionDocuments, "document_conflicts", n["conflicts"])
	}
	if s := r.StaticAnalysis; s != nil {
		n := map[string]int{"tools": 0, "succeeded": 0, "not_succeeded": 0, "observations": 0, "findings": 0, "actionable": 0, "pending": 0}
		requiredFailed := 0
		for _, l := range s.Languages {
			for _, t := range l.Tools {
				n["tools"]++
				if t.TaskStatus == string(domain.TaskSucceeded) {
					n["succeeded"]++
				} else {
					n["not_succeeded"]++
					if t.Required {
						requiredFailed++
					}
				}
				n["observations"] += len(t.ObservationIDs)
			}
		}
		fs := ComputeSummary(findingsOf(r, StaticFindingIDs(r)), nil)
		n["findings"], n["actionable"], n["pending"] = fs.TotalFindings, fs.ActionableFindings, fs.PendingCandidates
		line(SectionStaticAnalysis, &s.Stage, n)
		need("0", SectionStaticAnalysis, "actionable_findings", fs.ActionableFindings)
		need("2", SectionStaticAnalysis, "required_tools_not_succeeded", requiredFailed)
		need("6", SectionStaticAnalysis, "pending_candidates", fs.PendingCandidates)
	}
	if m := r.Metrics; m != nil {
		n := map[string]int{"tools": len(m.TaskKeys), "observations": 0, "metrics": len(m.Metrics)}
		for _, g := range m.Metrics {
			n["observations"] += len(g.ObservationIDs)
		}
		line(SectionMetrics, &m.Stage, n)
	}
	if s := r.Secrets; s != nil {
		files := map[string]bool{}
		for _, d := range s.Detections {
			if d.Path != nil {
				files[*d.Path] = true
			}
		}
		n := map[string]int{"tools": len(s.TaskKeys), "detections": len(s.Detections), "files": len(files)}
		line(SectionSecrets, &s.Stage, n)
		need("1", SectionSecrets, "secrets_detected", len(s.Detections))
	}
	if cr := r.CodeReview; cr != nil {
		fs := ComputeSummary(findingsOf(r, cr.FindingIDs), nil)
		n := map[string]int{"calls": cr.Calls, "findings": fs.TotalFindings, "actionable": fs.ActionableFindings, "pending": fs.PendingCandidates, "refuted": fs.RefutedFindings}
		notSent, primaryNotSent := 0, -1
		if l := cr.Ledger; l != nil && len(l.Modules) > 0 {
			// The reviewed scope is what was sent to the model, never verified
			// scope; the rest of the eligible files was not sent.
			n["files_sent_to_model"], notSent = ledgerTotals(l)
			n["files_not_sent_to_model"] = notSent
			if t := ledgerClassTotals(l); ledgerClasses(l) && t.SecondaryFiles > 0 {
				// The rows say which files are the project's own: the
				// overview counts those apart from test input, examples and
				// generated files. Without secondary files the totals above
				// already are the primary ones.
				n["primary_modules"], n["primary_files"], n["primary_files_sent_to_model"] = t.PrimaryModules, t.PrimaryFiles, t.PrimarySent
				n["secondary_files"], n["secondary_files_not_reached"] = t.SecondaryFiles, t.SecondaryNotReached
				primaryNotSent = t.PrimaryFiles - t.PrimarySent
			}
		}
		line(SectionCodeReview, &cr.Stage, n)
		need("0", SectionCodeReview, "actionable_findings", fs.ActionableFindings)
		if r.Coverage.AIReview.Status != AIReviewPerformed {
			need("5", SectionCodeReview, "model_review_not_performed", 1)
		}
		if primaryNotSent >= 0 {
			need("5", SectionCodeReview, "review_primary_not_sent", primaryNotSent)
		} else {
			need("5", SectionCodeReview, "review_scope_not_sent", notSent)
		}
		need("6", SectionCodeReview, "pending_candidates", fs.PendingCandidates)
	}
	line(SectionGaps, nil, map[string]int{"gaps": len(r.Gaps)})
	need("7", SectionGaps, "analysis_gaps", len(r.Gaps))
	for _, rank := range []string{"0", "1", "2", "3", "4", "5", "6", "7"} {
		o.Attention = append(o.Attention, attention[rank]...)
	}
	return o
}
