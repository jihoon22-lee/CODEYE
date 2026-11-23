package report

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type sectionReading struct {
	Model, IndexLabel, SourcesLabel string
	Groups                          []*readingGroup
}

type readingGroup struct {
	ID, Title  string
	Categories []readingCategory
	Sources    []sourceCitation
}

type readingCategory struct {
	Title string
	Rows  []readingRow
}

type readingRow struct {
	ID, Title, Type, Status, SharedPath, Location string
	Statement                                     string
	Aliases                                       []string
	Details                                       []secItem
	Sources                                       []sourceCitation
	Workflow                                      *workflowReadingView
	Preview                                       *sourcePreview
}

type readingDocumentSource struct {
	Evidence SectionEvidence
	Anchor   string
}

func (g *readingGroup) add(category string, reading readingRow) {
	for i := range g.Categories {
		if g.Categories[i].Title == category {
			g.Categories[i].Rows = append(g.Categories[i].Rows, reading)
			return
		}
	}
	g.Categories = append(g.Categories, readingCategory{Title: category, Rows: []readingRow{reading}})
}

func (v secView) documentReading(doc *DocumentsSection) *sectionReading {
	l := v.L
	reading := &sectionReading{
		Model: l.Label("insight.interpretation"), IndexLabel: l.Label("docs.reading_index"),
		SourcesLabel: l.Label("docs.reading_sources"),
	}
	evidenceByID := make(map[string]SectionEvidence, len(doc.Evidence))
	for _, e := range doc.Evidence {
		evidenceByID[e.ID] = e
	}
	groups := map[string]*readingGroup{}
	getGroup := func(path string) *readingGroup {
		if g := groups[path]; g != nil {
			return g
		}
		title := path
		if title == "" {
			title = l.Label("docs.reading_unlocated")
		}
		g := &readingGroup{ID: "document-path-" + strconv.Itoa(len(groups)+1), Title: title}
		groups[path] = g
		reading.Groups = append(reading.Groups, g)
		return g
	}
	used := map[string]bool{}
	add := func(row readingRow, category string, ids []string) {
		var paths []string
		for _, id := range ids {
			e, ok := evidenceByID[id]
			if !ok || e.Kind != "document" {
				continue
			}
			used[id] = true
			if row.Location == "" {
				row.Location = tf(l, "docs.reading_line_range", e.StartLine, e.EndLine)
			}
			if row.Preview == nil && v.R.Export.IncludeCode {
				row.Preview = v.citeSectionEvidence(e).previewFor(row.Statement)
			}
			if !slices.Contains(paths, e.Path) {
				paths = append(paths, e.Path)
			}
		}
		if len(paths) == 0 {
			paths = []string{""}
		}
		getGroup(paths[0]).add(category, row)
		for _, path := range paths[1:] {
			getGroup(path).add(category, readingRow{ID: row.ID, Title: row.Title, SharedPath: paths[0]})
		}
	}
	// Rank the recorded state while keeping the original comparison anchors.
	for _, status := range []string{"mismatch", "partial", "match", "unknown"} {
		for i, c := range doc.Comparisons {
			if c.Status != status {
				continue
			}
			title := openingText(c.Statement, tf(l, "docs.withheld_behavior", i+1))
			continueAt := v.comparisonDetails(c, doc.Evidence)
			var preview *sourcePreview
			if status != "unknown" && v.R.Export.IncludeCode {
				for _, id := range c.CodeEvidenceIDs {
					if evidence := evidenceByID[id]; evidence.Kind == "code" {
						preview = v.citeSectionEvidence(evidence).previewFor(c.Statement)
						if preview != nil {
							break
						}
					}
				}
			}
			add(readingRow{ID: comparisonAnchor(i), Title: title, Statement: c.Statement, Status: l.Label("decision.comparison." + c.Status), Details: continueAt,
				Workflow: v.workflowCitations(c.RelatedWorkflow, doc.Evidence), Preview: preview}, l.Label("docs.reading_comparisons"), c.DocumentEvidenceIDs)
		}
	}
	for i, q := range doc.Quality {
		add(readingRow{ID: "document-quality-" + strconv.Itoa(i+1), Title: l.Label("insight." + q.Aspect), Statement: q.Statement, Status: l.Label("insight." + q.Assessment),
			Details: readingScopeAndText(l, "", q.Statement), Sources: v.sourceCitations(q.EvidenceIDs, doc.Evidence)}, l.Label("docs.reading_quality"), q.EvidenceIDs)
	}
	if doc.Model.State == ModelPerformed {
		category := l.Label("docs.reading_notes")
		for i, c := range doc.Contracts {
			if comparisonCoversContract(doc.Comparisons, c) {
				continue
			}
			add(readingRow{ID: "document-contract-" + strconv.Itoa(i+1), Title: openingText(c.Statement, tf(l, "docs.withheld_contract", i+1)),
				Type: l.Label("docs.contracts"), Details: readingScopeAndText(l, c.AppliesTo, ""), Sources: v.sourceCitations(c.EvidenceIDs, doc.Evidence)}, category, c.EvidenceIDs)
		}
		for i, c := range doc.Conflicts {
			ids := append(slices.Clone(c.FirstEvidence), c.SecondEvidence...)
			add(readingRow{ID: "document-conflict-" + strconv.Itoa(i+1), Title: openingText(c.Summary, l.Label("docs.conflicts")),
				Type: l.Label("docs.conflicts"), Details: readingScopeAndText(l, c.Scope, c.Impact), Sources: v.sourceCitations(ids, doc.Evidence)}, category, ids)
		}
		for i, q := range doc.Questions {
			add(readingRow{ID: "document-question-" + strconv.Itoa(i+1), Title: openingText(q.Question, l.Label("docs.questions")),
				Type: l.Label("docs.questions"), Details: readingScopeAndText(l, q.AppliesTo, ""), Sources: v.sourceCitations(q.EvidenceIDs, doc.Evidence)}, category, q.EvidenceIDs)
		}
		for i, n := range doc.InstructionNotes {
			add(readingRow{ID: "document-note-" + strconv.Itoa(i+1), Title: openingText(n.Note, l.Label("docs.instruction_notes")),
				Type: l.Label("docs.instruction_notes"), Sources: v.sourceCitations(n.EvidenceIDs, doc.Evidence)}, category, n.EvidenceIDs)
		}
	}
	if len(reading.Groups) == 0 {
		return nil
	}
	if v.R.Export.IncludeCode {
		v.canonicalDocumentSources(doc.Evidence, used, groups)
	}
	return reading
}

func readingScopeAndText(l Locale, scope, text string) []secItem {
	var details []secItem
	if scope := displayDocumentScope(scope); scope != "" {
		details = append(details, secItem{Label: l.Label("col.applies_to"), Text: scope})
	}
	if text := openingText(text, ""); text != "" {
		details = append(details, secItem{Text: text})
	}
	return details
}

func (v secView) canonicalDocumentSources(evidence []SectionEvidence, used map[string]bool, groups map[string]*readingGroup) {
	var windows []SectionEvidence
	documentIDs := map[string]bool{}
	for _, e := range evidence {
		if used[e.ID] && e.Excerpt != nil {
			windows = append(windows, e)
			documentIDs[e.ID] = true
		}
	}
	slices.SortStableFunc(windows, func(a, b SectionEvidence) int {
		return cmp.Compare(b.Excerpt.EndLine-b.Excerpt.StartLine, a.Excerpt.EndLine-a.Excerpt.StartLine)
	})
	canonical := map[string][]readingDocumentSource{}
	mapped := map[string]string{}
	for _, e := range windows {
		for _, window := range canonical[e.Path] {
			if sameSnapshotWindow(window.Evidence, e) {
				mapped[e.ID] = window.Anchor
				break
			}
		}
		if mapped[e.ID] == "" {
			anchor := "document-source-" + strconv.Itoa(len(mapped)+1)
			mapped[e.ID] = anchor
			canonical[e.Path] = append(canonical[e.Path], readingDocumentSource{Evidence: e, Anchor: anchor})
		}
	}
	for path, windows := range canonical {
		slices.SortStableFunc(windows, func(a, b readingDocumentSource) int {
			return cmp.Compare(a.Evidence.Excerpt.StartLine, b.Evidence.Excerpt.StartLine)
		})
		for _, window := range windows {
			e := window.Evidence
			e.StartLine, e.EndLine = e.Excerpt.StartLine, e.Excerpt.EndLine
			citation := v.citeSectionEvidence(e)
			citation.Anchor = window.Anchor
			groups[path].Sources = append(groups[path].Sources, citation)
		}
	}
	compact := func(sources []sourceCitation) {
		for i := range sources {
			source := &sources[i]
			if documentIDs[source.ID] {
				source.Jump = mapped[source.ID]
				source.Text = ""
			}
		}
	}
	for _, group := range groups {
		for _, category := range group.Categories {
			for _, row := range category.Rows {
				compact(row.Sources)
				for _, detail := range row.Details {
					compact(detail.Sources)
				}
				if row.Workflow != nil {
					for _, item := range row.Workflow.Items {
						compact(item.Sources)
					}
				}
			}
		}
	}
}

func sameSnapshotWindow(container, inside SectionEvidence) bool {
	outer, inner := container.Excerpt, inside.Excerpt
	if container.BlobSHA256 != inside.BlobSHA256 || outer.StartLine > inner.StartLine || outer.EndLine < inner.EndLine {
		return false
	}
	lines := strings.Split(outer.Text, "\n")
	start, end := inner.StartLine-outer.StartLine, inner.EndLine-outer.StartLine+1
	return end <= len(lines) && strings.Join(lines[start:end], "\n") == inner.Text
}

func (v secView) strengthReading(code *CodeReviewSection) *sectionReading {
	if len(code.Strengths) == 0 {
		return nil
	}
	l := v.L
	reading := &sectionReading{Model: l.Label("insight.interpretation")}
	groups := map[string]*readingGroup{}
	type observationKey struct{ scope, title, benefit string }
	seen := map[observationKey]int{}
	for i, strength := range code.Strengths {
		scope := displayDocumentScope(strength.Scope)
		group := groups[scope]
		if group == nil {
			title := scope
			if title == "" {
				title = l.Label("review.reading_repository")
			}
			group = &readingGroup{Title: title}
			groups[scope] = group
			reading.Groups = append(reading.Groups, group)
		}
		status := ""
		if !hasProductSectionEvidence(strength.EvidenceIDs, code.Evidence, "code") {
			status = l.Label("review.reading_secondary")
		}
		title := openingText(strength.Title, tf(l, "review.withheld_strength", i+1))
		key := observationKey{scope, strings.Join(strings.Fields(title), " "), strings.Join(strings.Fields(strength.Benefit), " ")}
		anchor := strengthAnchor(i)
		sources := v.sourceCitations(strength.EvidenceIDs, code.Evidence)
		if rowIndex, ok := seen[key]; ok {
			row := &group.Categories[0].Rows[rowIndex]
			row.Aliases = append(row.Aliases, anchor)
			for _, source := range sources {
				if !slices.ContainsFunc(row.Sources, func(current sourceCitation) bool { return current.Location == source.Location }) {
					row.Sources = append(row.Sources, source)
				}
			}
			if status == "" {
				row.Status = ""
			}
			continue
		}
		group.add("", readingRow{ID: anchor, Title: title, Status: status,
			Details: readingScopeAndText(l, "", strength.Benefit), Sources: sources})
		seen[key] = len(group.Categories[0].Rows) - 1
	}
	return reading
}

func (v secView) codeReviewReadingOrder(parts []secPart) []secPart {
	l := v.L
	investigation, verification := 0, 0
	for _, call := range v.R.Coverage.ReviewCalls {
		if call.RequestDigest == "" || call.Purpose != "" {
			continue
		}
		switch call.Role {
		case record.StageInvestigate:
			investigation++
		case record.StageVerify:
			verification++
		}
	}
	outcomes := []secPart{{Paras: []string{tf(l, "review.reading_requests", investigation, verification)}}}
	var details, additional, execution []secPart
	for _, part := range parts {
		switch part.Heading {
		case l.Label("review.findings"):
			groups := v.reviewFindingParts(part.Findings)
			for i, group := range groups {
				if i == 0 || len(group.Cards) > 0 {
					outcomes = append(outcomes, group)
				} else {
					additional = append(additional, group)
				}
			}
		case l.Label("review.reading_actions"), l.Label("review.reading_limited"), l.Label("review.reading_secondary_findings"):
			outcomes = append(outcomes, part)
		case l.Label("review.reading_refuted"), l.Label("review.reading_disposition"), l.Label("review.reading_related_records"):
			additional = append(additional, part)
		case l.Label("review.limits"):
			details = append(details, part)
		default:
			execution = append(execution, part)
		}
	}
	if observations := v.strengthReading(v.R.CodeReview); observations != nil {
		outcomes = append(outcomes, secPart{Fold: l.Label("review.reading_strengths"), Reading: observations})
	}
	for _, part := range additional {
		part.Fold, part.Heading = part.Heading, ""
		outcomes = append(outcomes, part)
	}
	outcomes = append(outcomes, details...)
	return append(outcomes, secPart{Fold: l.Label("review.reading_execution"), Parts: execution})
}

func (v secView) reviewFindingParts(findings []Finding) []secPart {
	l := v.L
	sorted := slices.Clone(findings)
	slices.SortStableFunc(sorted, func(a, b Finding) int {
		return cmp.Compare(sevRank(b.Severity), sevRank(a.Severity))
	})
	headings := []string{"review.reading_actions", "review.reading_limited", "review.reading_secondary_findings",
		"review.reading_refuted", "review.reading_disposition", "review.reading_related_records"}
	var duplicates map[string]bool
	if v.Summary {
		duplicates = duplicateReadingFindings(sorted, v.R.Observations)
	}
	groups := make([]secPart, len(headings))
	for i, heading := range headings {
		groups[i].Heading = l.Label(heading)
	}
	for _, f := range sorted {
		group := 0
		switch {
		case duplicates[f.ID]:
			group = 5
		case f.Validation == ValidationRefuted:
			group = 3
		case hiddenDisposition[f.Disposition]:
			group = 4
		case findingSecondary(f):
			group = 2
		case f.Validation != ValidationSupported && f.Validation != ValidationReproduced:
			group = 1
		}
		part := &groups[group]
		if !v.Summary {
			part.Findings = append(part.Findings, f)
			continue
		}
		if group <= 2 {
			part.Cards = append(part.Cards, v.findingCard(f))
			continue
		}
		if part.Table == nil {
			part.Table = &secTable{Head: []string{l.Label("col.finding"), l.Label("col.title"), l.Label("col.validation"), l.Label("col.severity"), l.Label("col.location")}}
		}
		var locations []string
		for _, loc := range f.Locations {
			locations = append(locations, sourceRange(loc.Path, loc.StartLine, loc.EndLine))
		}
		part.Table.RowIDs = append(part.Table.RowIDs, f.ID)
		part.Table.Rows = append(part.Table.Rows, []secCell{code(f.ID), txt(f.Title), txt(lab(l, "validation", f.Validation)), txt(lab(l, "severity", f.Severity)), code(joinOr(locations, "-"))})
	}
	if p := &groups[0]; len(p.Findings)+len(p.Cards) == 0 {
		p.Paras = []string{l.Label("review.reading_none")}
		if !hasCodeReviewEvidence(v.R.CodeReview) {
			p.Paras = []string{l.Label("review.reading_no_evidence")}
		}
	}
	return slices.DeleteFunc(groups, func(part secPart) bool {
		return part.Table == nil && len(part.Findings)+len(part.Cards)+len(part.Paras) == 0
	})
}

func hasCodeReviewEvidence(section *CodeReviewSection) bool {
	return len(section.FindingIDs)+len(section.Strengths) > 0 || slices.ContainsFunc(section.Roles, func(role AIReviewTask) bool {
		return (role.Result == "accepted" && role.Key == "review.investigate") ||
			((role.Result == "accepted" || role.Result == ResultAcceptedIncomplete) && role.Key == "review.verify")
	}) || section.Ledger != nil && slices.ContainsFunc(section.Ledger.Modules, func(module LedgerModule) bool {
		return module.FilesWithCompletedReview > 0
	})
}

func mdReading(p func(...string), reading *sectionReading, l Locale) {
	p("> ", mdText(reading.Model))
	p()
	if reading.IndexLabel != "" && len(reading.Groups) > 1 {
		p("**", mdText(reading.IndexLabel), "**")
		p()
		for _, group := range reading.Groups {
			p("- [", mdCode(group.Title), "](#", group.ID, ")")
		}
		p()
	}
	for _, group := range reading.Groups {
		mdReadingAnchor(p, group.ID)
		p("### ", mdCode(group.Title))
		p()
		for _, category := range group.Categories {
			if category.Title != "" {
				p("**", mdText(category.Title), "**")
				p()
			}
			for _, row := range category.Rows {
				if row.SharedPath != "" {
					p("- [", mdText(row.Title), "](#", row.ID, ") · ", mdText(tf(l, "docs.reading_shared", row.SharedPath)))
					p()
					continue
				}
				mdReadingAnchor(p, row.ID)
				for _, alias := range row.Aliases {
					mdReadingAnchor(p, alias)
				}
				p("##### ", mdText(row.Title))
				p()
				for _, text := range []string{row.Status, row.Type} {
					if text != "" {
						p("**", mdText(text), "**")
						p()
					}
				}
				for _, detail := range row.Details {
					mdItem(p, detail)
				}
				if len(row.Details) > 0 {
					p()
				}
				mdCitations(p, row.Sources)
				mdWorkflowCitations(p, row.Workflow)
			}
		}
		if len(group.Sources) > 0 {
			p("**", mdText(reading.SourcesLabel), "**")
			p()
			mdCitations(p, group.Sources)
		}
	}
}

func mdReadingAnchor(p func(...string), id string) {
	if id != "" {
		p("<a id=\"", id, "\"></a>")
		p()
	}
}

type overviewReadingRow struct{ Section, Label, Value, Status, Tone, Count string }

var reportResultNumber = regexp.MustCompile(`[0-9]+(?:[.,/][0-9]+)*`)

func (r overviewReadingRow) ValueParts() []secCell {
	var parts []secCell
	from := 0
	for _, span := range reportResultNumber.FindAllStringIndex(r.Value, -1) {
		parts = append(parts, txt(r.Value[from:span[0]]), code(r.Value[span[0]:span[1]]))
		from = span[1]
	}
	return append(parts, txt(r.Value[from:]))
}

func reportStageTone(state string) string {
	switch state {
	case "succeeded":
		return "ready"
	case "partial":
		return "partial"
	case "failed", "cancelled":
		return "failed"
	default:
		return "neutral"
	}
}

func reportStageLabel(l Locale, state string) string {
	text := lab(l, "state", state)
	for _, prefix := range []string{"○ ", "· ", "◷ ", "◉ ", "✓ ", "– ", "✕ ", "■ ", "△ "} {
		text = strings.TrimPrefix(text, prefix)
	}
	return text
}

func groupedCount(value int) string {
	digits := strconv.Itoa(value)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

func (v secView) ReadingOverview() []overviewReadingRow {
	var rows []overviewReadingRow
	states := make(map[string]string, len(v.R.Overview.Sections))
	for _, state := range v.R.Overview.Sections {
		if state.State != nil {
			states[state.Key] = *state.State
		}
	}
	add := func(section, label, value string) *overviewReadingRow {
		state := states[section]
		row := overviewReadingRow{Section: section, Label: v.L.Label(label), Value: value, Tone: reportStageTone(state)}
		if state != "" {
			row.Status = reportStageLabel(v.L, state)
		}
		rows = append(rows, row)
		return &rows[len(rows)-1]
	}

	primaryFindings, secondaryFindings := v.actionableFindings()
	primary, secondary := len(primaryFindings), len(secondaryFindings)
	eligible, sent, accepted := 0, 0, 0
	reviewedLines, totalLines := 0, 0
	knownAccepted := true
	if ledger := v.R.CodeReview.Ledger; ledger != nil {
		for _, file := range ledger.Files {
			reviewedLines += file.ReviewedLines
			totalLines += file.TotalLines
		}
		for _, module := range ledger.Modules {
			eligible += module.FilesEligible
			sent += module.FilesSentToModel
			if module.FilesAccepted == nil {
				knownAccepted = false
			} else {
				accepted += *module.FilesAccepted
			}
		}
	}
	if secondary > 0 {
		add(SectionCodeReview, "reading.code_outcome", tf(v.L, "reading.action_counts", primary, secondary)).Count = groupedCount(primary)
	} else if primary > 0 || knownAccepted && accepted > 0 {
		add(SectionCodeReview, "reading.code_outcome", tf(v.L, "reading.action_primary", primary)).Count = groupedCount(primary)
	}
	if eligible > 0 {
		if knownAccepted {
			value := tf(v.L, "reading.model_scope", groupedCount(accepted), groupedCount(eligible))
			if totalLines > 0 {
				value += " · " + tf(v.L, "reading.model_lines", groupedCount(reviewedLines), groupedCount(totalLines))
			}
			row := add(SectionCodeReview, "reading.model_scope_label", value)
			row.Count = groupedCount(accepted) + "/" + groupedCount(eligible)
			row.Status, row.Tone = v.L.Label("reading.model_partial"), "partial"
			if accepted == eligible {
				row.Status, row.Tone = v.L.Label("reading.model_available"), "ready"
			}
		} else {
			row := add(SectionCodeReview, "reading.model_scope_label", tf(v.L, "reading.model_sent", groupedCount(sent), groupedCount(eligible)))
			row.Count = groupedCount(sent) + "/" + groupedCount(eligible)
			row.Status, row.Tone = v.L.Label("reading.model_requested"), "neutral"
		}
	}
	languages := regularStaticLanguages(v.R)
	planned, accepted := staticCheckCounts(languages)
	observed := map[string]bool{}
	for _, language := range languages {
		for _, tool := range language.Tools {
			if checkCompleted(tool) {
				for _, id := range tool.ObservationIDs {
					observed[id] = true
				}
			}
		}
	}
	if planned > 0 {
		primary := 0
		for _, observation := range v.R.Observations {
			if loc := observation.Location; observed[observation.ID] && !observation.Suppressed && observation.Metric == nil &&
				observation.EvidenceState == EvidenceVerified && loc != nil && productLocation(loc.Path, loc.StartLine, loc.EndLine) {
				primary++
			}
		}
		row := add(SectionStaticAnalysis, "section.static_analysis", tf(v.L, "reading.static_results", accepted, planned)+" · "+tf(v.L, "reading.static_diagnostics", primary))
		row.Count = groupedCount(accepted) + "/" + groupedCount(planned)
		if accepted == planned {
			row.Status, row.Tone = reportStageLabel(v.L, "succeeded"), "ready"
		} else if accepted > 0 {
			row.Status, row.Tone = reportStageLabel(v.L, "partial"), "partial"
		}
	}
	if test := v.TestRunPart(); test.Fold != "" {
		status, _, _ := strings.Cut(test.Fold, " · ")
		add("test-results", "reading.tests_outcome", status)
	}
	statusCounts := map[string]int{}
	for _, task := range v.R.Tasks {
		if task.RequiredForVerdict && !testTaskKey(task.Key) {
			statusCounts[task.Status]++
		}
	}
	var outcomes []string
	totalIncomplete := 0
	for _, status := range []string{"failed", "timed_out", "unsupported", "skipped_policy"} {
		if count := statusCounts[status]; count > 0 {
			outcomes = append(outcomes, tf(v.L, "reading.outcome."+status, count))
			totalIncomplete += count
		}
	}
	if len(outcomes) > 0 {
		row := add(SectionGaps, "reading.required_outcome", strings.Join(outcomes, " · "))
		row.Status, row.Tone, row.Count = v.L.Label("reading.incomplete_state"), "attention", groupedCount(totalIncomplete)
		if statusCounts["failed"] > 0 {
			row.Tone = "failed"
		}
	}
	if d := v.R.Documents; len(d.Items)+len(d.Comparisons) > 0 {
		read, assessed := 0, 0
		for _, item := range d.Items {
			if item.Status == "selected" {
				read++
			}
		}
		for _, comparison := range d.Comparisons {
			if comparison.Status != "unknown" {
				assessed++
			}
		}
		unknown := len(d.Comparisons) - assessed
		values := []string{tf(v.L, "reading.documents_count", read)}
		if assessed > 0 {
			values = append(values, tf(v.L, "reading.documents_assessed", assessed))
		}
		if unknown > 0 {
			values = append(values, tf(v.L, "reading.documents_unknown", unknown))
		}
		row := add(SectionDocuments, "section.documents", strings.Join(values, " · "))
		row.Count = groupedCount(read)
		if unknown > 0 {
			row.Status, row.Tone = v.L.Label("reading.documents_pending"), "attention"
		} else if read > 0 {
			row.Status = v.L.Label("reading.documents_read")
		}
	}
	if environment := v.R.Environment; len(environment.Modules) > 0 {
		mismatches := 0
		for _, verdict := range environment.Verdicts {
			if verdict.Status == "mismatch" {
				mismatches++
			}
		}
		row := add(SectionEnvironment, "section.environment", tf(v.L, "reading.environment_outcome", len(environment.Modules), mismatches))
		row.Count = groupedCount(len(environment.Modules))
		if mismatches > 0 {
			row.Status, row.Tone = v.L.Label("reading.environment_mismatch"), "attention"
		}
	}
	if metrics := v.R.Metrics; len(metrics.TaskKeys) > 0 {
		for _, metric := range metrics.Metrics {
			count := groupedCount(len(metric.ObservationIDs))
			label, value := metric.Name, tf(v.L, "reading.metric_outcome", metric.Name, count)
			switch metric.Name {
			case "cyclomatic_complexity":
				label, value = "reading.complexity_label", tf(v.L, "reading.complexity_outcome", count)
			case "duplicated_lines":
				label, value = "reading.duplication_label", tf(v.L, "reading.duplication_outcome", count)
			}
			row := add(SectionMetrics, label, value)
			row.Count = count
			if row.Tone == "ready" {
				row.Status = v.L.Label("reading.measured_state")
			}
		}
		if len(metrics.Metrics) == 0 {
			add(SectionMetrics, "section.metrics", v.L.Label("reading.metrics_empty"))
		}
	}
	if graphs := v.R.Metrics.GoDependencies; len(graphs) > 0 {
		complete, available, selected := true, false, false
		packages := 0
		for _, graph := range graphs {
			complete = complete && graph.Status == "complete"
			available = available || graph.Status != "unavailable"
			selected = selected || graph.Scope == "selected_packages"
			packages += len(graph.Nodes)
		}
		value := tf(v.L, "reading.graph_outcome", groupedCount(len(graphs)), groupedCount(packages))
		if selected {
			value += " · " + v.L.Label("reading.graph_selected")
		}
		row := add(SectionMetrics, "graph.title", value)
		row.Count = groupedCount(len(graphs))
		switch {
		case complete:
			row.Status, row.Tone = v.L.Label("reading.graph_ready"), "ready"
		case available:
			row.Status, row.Tone = v.L.Label("reading.graph_partial"), "partial"
		default:
			row.Status, row.Tone = v.L.Label("reading.graph_unavailable"), "neutral"
		}
	}
	if secrets := v.R.Secrets; len(secrets.TaskKeys) > 0 {
		count, state := len(secrets.Detections), secrets.Stage.State
		complete := state == "succeeded"
		status, tone := reportStageLabel(v.L, state), reportStageTone(state)
		values := []string{v.L.Label("reading.secrets_undetermined")}
		if complete || count > 0 {
			values[0] = tf(v.L, "reading.secrets_outcome", count)
		}
		if count > 0 {
			if !complete && state != "" {
				values = append(values, status)
			}
			status, tone = v.L.Label("reading.secrets_detected"), "attention"
		} else if complete {
			status = v.L.Label("reading.secrets_clear")
		}
		if slices.ContainsFunc(v.R.Gaps, func(gap Gap) bool {
			return gap.Section == SectionSecrets && gap.Code == "history_not_scanned"
		}) {
			values = append(values, v.L.Label("reading.secrets_workspace_scope"))
		}
		row := add(SectionSecrets, "section.secrets", strings.Join(values, " · "))
		row.Status, row.Tone, row.Count = status, tone, groupedCount(count)
	}
	flow := []string{SectionEnvironment, SectionDocuments, SectionStaticAnalysis, SectionMetrics, SectionSecrets, SectionCodeReview, "test-results", SectionGaps}
	slices.SortStableFunc(rows, func(a, b overviewReadingRow) int {
		return cmp.Compare(slices.Index(flow, a.Section), slices.Index(flow, b.Section))
	})
	return rows
}

func (v secView) CodeIndexCount() int {
	total := 0
	for _, section := range v.Sections {
		if section.ID != SectionCodeReview {
			continue
		}
		for _, part := range section.Parts {
			total += len(part.Cards) + len(part.Findings)
		}
	}
	return total
}

func mdReadingOverview(p func(...string), v secView) {
	l := v.L
	if rows := v.ReadingOverview(); len(rows) > 0 {
		mdReadingAnchor(p, "coverage")
		p("### ", mdText(l.Label("reading.outcome")))
		p()
		p("| ", mdText(l.Label("reading.area")), " | ", mdText(l.Label("reading.result")), " | ", mdText(l.Label("reading.details")), " |")
		p("|---|---|---|")
		for _, row := range rows {
			label := row.Label
			if row.Status != "" {
				label += " · " + row.Status
			}
			p("| ", mdText(label), " | ", mdText(row.Value), " | [", mdText(l.Label("reading.view")), "](#", row.Section, ") |")
		}
		p()
	}
	if v.R.Inputs.Profile == "with-tests" {
		mdReadingAnchor(p, "test-results")
		mdPart(p, v.TestRunPart(), l)
	}
	p("<details><summary>", mdText(l.Label("reading.context_details")), "</summary>")
	p()
	mdDecision(p, v.Decision, l)
	mdBehaviors(p, v.Behaviors, l)
	p("</details>")
	p()
}
