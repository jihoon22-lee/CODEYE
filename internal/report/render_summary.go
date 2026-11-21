package report

import (
	"bytes"
	"cmp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Detail levels of the Markdown and HTML exports
// (docs/REPORT.md#내보내기-상세도). The summary opens with selected evidence,
// then groups long lists in each analysis section. The full level shows every
// row. Both read the same stored report as the JSON export.
const (
	DetailSummary = "summary"
	DetailFull    = "full"
)

// Finding validations (docs/REPORT.md#검증-상태).
const (
	ValidationUnreviewed   = "unreviewed"
	ValidationSupported    = "supported"
	ValidationReproduced   = "reproduced"
	ValidationRefuted      = "refuted"
	ValidationInconclusive = "inconclusive"
)

// ValidDetail reports a detail level the exports accept.
func ValidDetail(d string) bool { return d == DetailSummary || d == DetailFull }

// RenderMarkdownAt renders r at a detail level. A report without sections
// (builder 4 and earlier) has one layout for both levels.
func RenderMarkdownAt(r Report, digest string, l Locale, detail string) []byte {
	if detail == DetailFull || !r.hasSections() {
		return RenderMarkdown(r, digest, l)
	}
	return renderMarkdownSummary(r, digest, l)
}

// RenderHTMLAt is RenderMarkdownAt for the self-contained HTML page.
func RenderHTMLAt(r Report, digest string, l Locale, detail string) ([]byte, error) {
	if detail == DetailFull || !r.hasSections() {
		return RenderHTML(r, digest, l)
	}
	var buf bytes.Buffer
	if err := htmlSectionsTemplate.Execute(&buf, newSummaryView(r, digest, l)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (v secView) ReportStatus() string  { return lab(v.L, "report.status", v.R.Status) }
func (v secView) HasStatusBanner() bool { return v.R.Status != StatusCompleted }

// summaryShown is how many entries a grouped list shows before "외 N".
const summaryShown = 5

// summaryMessageRunes cuts a one-line description.
const summaryMessageRunes = 100

// secondaryPath reports a path below a secondary path segment of the
// default review priority rule (docs/REVIEW.md#검토-우선순위). The report
// side is path-only: it does not see generated-file headers.
func secondaryPath(p string) bool { return record.DefaultSecondary.Match(p) != "" }

// secondaryDir reports a module or document directory below a secondary
// path ("." never is).
func secondaryDir(dir string) bool {
	return dir != "" && dir != "." && secondaryPath(strings.TrimSuffix(dir, "/")+"/x")
}

// moduleDir is the directory of a module ID <ecosystem>:<dir>.
func moduleDir(id string) string {
	_, dir, ok := strings.Cut(id, ":")
	if !ok {
		return id
	}
	return dir
}

// cutLine is s on one line, cut to n runes.
func cutLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// shortList is the first summaryShown entries of list and "외 N" for the
// rest; the whole list when it is that short.
func shortList(l Locale, list []string) string {
	if len(list) <= summaryShown {
		return strings.Join(list, ", ")
	}
	return strings.Join(list[:summaryShown], ", ") + " " + tf(l, "summary.more", len(list)-summaryShown)
}

// placesText lists locations primary first: every location when there are
// at most summaryShown, else the first primary ones (or, without any, the
// first two secondary ones) and how many more there are, of which how many
// are below a secondary path.
func placesText(l Locale, primary, secondary []string) string {
	if len(primary)+len(secondary) <= summaryShown {
		return strings.Join(append(append([]string{}, primary...), secondary...), ", ")
	}
	shown := primary[:min(summaryShown, len(primary))]
	if len(shown) == 0 {
		shown = secondary[:min(2, len(secondary))]
	}
	rest := len(primary) + len(secondary) - len(shown)
	restSecondary := len(secondary)
	if len(primary) == 0 {
		restSecondary -= len(shown)
	}
	out := strings.Join(shown, ", ") + " " + tf(l, "summary.more_places", rest)
	if restSecondary > 0 {
		out += " " + tf(l, "summary.of_secondary", restSecondary)
	}
	return out
}

// sevRank orders finding severities; an unknown value ranks lowest.
func sevRank(s string) int {
	switch s {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "info":
		return 1
	}
	return 0
}

// ruleGroup is the diagnostics of one rule of one analyzer in one language.
type ruleGroup struct {
	Analyzer, Rule     string
	Count              int
	Severity           string
	sev                int
	Message            string
	Primary, Secondary []string
	order              int
}

// staticGroups groups the located and unlocated diagnostics of a language's
// diagnostics tools by analyzer and rule (the .mNN tasks of one analyzer
// merge). Suppressed diagnostics and metrics are not in a group. Groups
// come by analyzer (first appearance), then those with a primary location,
// highest severity, most diagnostics, rule.
func (v secView) staticGroups(lang StaticLanguage, obs map[string]Observation, sevOf map[string]string) []ruleGroup {
	byKey := map[string]*ruleGroup{}
	var groups []*ruleGroup
	analyzers := map[string]int{}
	for _, x := range lang.Tools {
		if p := optText(x.Purpose, PurposeDiagnostics); p != PurposeDiagnostics {
			continue
		}
		analyzer := optText(x.Analyzer, x.TaskKey)
		if _, ok := analyzers[analyzer]; !ok {
			analyzers[analyzer] = len(analyzers)
		}
		for _, id := range x.ObservationIDs {
			o, ok := obs[id]
			if !ok || o.Suppressed || o.Metric != nil {
				continue
			}
			k := analyzer + "\x00" + o.RuleID
			g := byKey[k]
			if g == nil {
				g = &ruleGroup{Analyzer: analyzer, Rule: o.RuleID, Message: cutLine(v.Message[o.ID], summaryMessageRunes), order: analyzers[analyzer]}
				byKey[k] = g
				groups = append(groups, g)
			}
			g.Count++
			sev := sevOf[o.ID]
			if sev == "" && o.RawSeverity != nil {
				sev = *o.RawSeverity
			}
			if rank := sevRank(sev); g.Severity == "" || rank > g.sev {
				g.Severity, g.sev = sev, rank
			}
			loc := l0(v.L)
			if o.Location != nil {
				loc = sourceRange(o.Location.Path, o.Location.StartLine, o.Location.EndLine)
			}
			if o.Location != nil && secondaryPath(o.Location.Path) {
				g.Secondary = append(g.Secondary, loc)
			} else {
				if o.Location != nil && len(g.Primary) == 0 {
					g.Message = cutLine(v.Message[o.ID], summaryMessageRunes)
				}
				g.Primary = append(g.Primary, loc)
			}
		}
	}
	out := make([]ruleGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a], out[b]
		switch {
		case x.order != y.order:
			return x.order < y.order
		case (len(x.Primary) > 0) != (len(y.Primary) > 0):
			return len(x.Primary) > 0
		case x.sev != y.sev:
			return x.sev > y.sev
		case x.Count != y.Count:
			return x.Count > y.Count
		}
		return x.Rule < y.Rule
	})
	return out
}

// l0 is the location text of an unlocated diagnostic.
func l0(l Locale) string { return l.Label("none") }

// newSummaryView is the summary layout's section model.
func newSummaryView(r Report, digest string, l Locale) secView {
	full := newSecView(r, digest, l)
	v := secView{view: full.view, Summary: true}
	byID := map[string]secBlock{}
	for _, s := range full.Sections {
		byID[s.ID] = s
	}
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	findings := map[string]Finding{}
	for _, f := range r.Findings {
		findings[f.ID] = f
	}
	order := SectionKeys

	// Separate the opening review from the structured coverage and totals.
	ov := byID["overview"]
	v.buildProjectOpening()
	ov.Parts = nil
	if r.PullRequest != nil {
		ov.Parts = []secPart{{Paras: pullRequestParas(r, l)[:1]}}
	}
	fullOverview := byID["overview"].Parts
	detail := fullOverview[0]
	detail.Paras = append(pullRequestParas(r, l), summarySentences(r, l, obs)...)
	table := *fullOverview[1].Table
	rows := map[string][]secCell{}
	for i, s := range r.Overview.Sections {
		rows[s.Key] = table.Rows[i]
	}
	table.Rows = nil
	for _, k := range order {
		if row, ok := rows[k]; ok {
			table.Rows = append(table.Rows, row)
		}
	}
	if limits := v.openingLimits(); len(limits.Paras)+len(limits.List) > 0 {
		v.OverviewDetail = append(v.OverviewDetail, limits)
	}
	v.OverviewDetail = append(v.OverviewDetail, detail, secPart{Heading: fullOverview[1].Heading, Table: &table})
	v.OverviewDetail = append(v.OverviewDetail, fullOverview[2:]...)
	v.Sections = append(v.Sections, ov)

	for _, k := range order {
		b, ok := byID[k]
		if !ok {
			continue
		}
		switch k {
		case SectionCodeReview:
			b = v.summaryCodeReview(b, findings)
		case SectionStaticAnalysis:
			b = v.summaryStatic(b, obs, findings)
		case SectionMetrics:
			b = v.summaryMetrics(b, obs)
		case SectionSecrets:
			b = v.summarySecrets(b)
		case SectionEnvironment:
			b = v.summaryEnvironment(b)
		case SectionDocuments:
			b = v.summaryDocuments(b)
		case SectionGaps:
			b = v.summaryGaps(b)
		}
		v.Sections = append(v.Sections, b)
	}
	v.Appendix = v.summaryAppendix()
	for i := range v.Sections {
		for j := range v.Sections[i].Parts {
			x := &v.Sections[i].Parts[j]
			x.ShowEmpty = x.Empty != "" && (x.Table == nil || len(x.Table.Rows) == 0) && len(x.List) == 0 && len(x.Findings) == 0 && len(x.Cards) == 0
		}
	}
	v = withInsights(v)
	v.dropDanglingLinks()
	return v
}

// summarySentences say in words what the run found and covered, from the
// stored sections only.
func summarySentences(r Report, l Locale, obs map[string]Observation) []string {
	var out []string
	if c := r.CodeReview; c != nil {
		var by = map[string]int{}
		for _, id := range c.FindingIDs {
			for _, f := range r.Findings {
				if f.ID == id {
					by[f.Validation]++
				}
			}
		}
		if n := len(c.FindingIDs); n > 0 {
			s := tf(l, "summary.model_findings", n, by[ValidationSupported]+by[ValidationReproduced])
			var rest []string
			for _, x := range []string{ValidationReproduced, ValidationInconclusive, ValidationRefuted, ValidationUnreviewed} {
				if by[x] > 0 {
					rest = append(rest, tf(l, "summary.validation."+x, by[x]))
				}
			}
			if len(rest) > 0 {
				s += " (" + strings.Join(rest, ", ") + ")"
			}
			out = append(out, s+".")
		} else if len(c.Roles) > 0 {
			out = append(out, l.Label("summary.model_none"))
		}
		if x := c.Ledger; x != nil && len(x.Modules) > 0 {
			var primary, primarySent, secondary, secondarySent int
			for _, m := range x.Modules {
				sec := m.FilesSecondary
				secSent := m.FilesSecondarySent
				if m.Class == "" && secondaryDir(moduleDir(m.ModuleID)) {
					sec, secSent = m.FilesEligible, m.FilesSentToModel
				}
				primary += m.FilesEligible - sec
				primarySent += m.FilesSentToModel - secSent
				secondary += sec
				secondarySent += secSent
			}
			s := tf(l, "summary.review_scope", primary, primarySent)
			if secondary > 0 {
				s += " " + tf(l, "summary.review_scope_secondary", secondary, secondarySent)
			}
			out = append(out, s)
		}
	}
	if s := r.StaticAnalysis; s != nil {
		diag, suppressed, secondary, required := 0, 0, 0, 0
		rules, tools := map[string]bool{}, map[string]bool{}
		for _, lang := range s.Languages {
			for _, x := range lang.Tools {
				if x.Required && x.TaskStatus != string(domain.TaskSucceeded) {
					required++
				}
				if optText(x.Purpose, PurposeDiagnostics) != PurposeDiagnostics {
					continue
				}
				for _, id := range x.ObservationIDs {
					o, ok := obs[id]
					if !ok || o.Metric != nil {
						continue
					}
					diag++
					if o.Suppressed {
						suppressed++
						continue
					}
					analyzer := optText(x.Analyzer, x.TaskKey)
					rules[analyzer+"\x00"+o.RuleID], tools[analyzer] = true, true
					if o.Location != nil && secondaryPath(o.Location.Path) {
						secondary++
					}
				}
			}
		}
		line := tf(l, "summary.static", diag, len(rules), len(tools))
		if suppressed > 0 {
			line += " " + tf(l, "summary.static_suppressed", suppressed)
		}
		if secondary > 0 {
			line += " " + tf(l, "summary.static_secondary", secondary)
		}
		if required > 0 {
			line += " " + tf(l, "summary.required_not_succeeded", required)
		}
		out = append(out, line)
	}
	if m := r.Metrics; m != nil && len(m.Metrics) > 0 {
		n := 0
		for _, g := range m.Metrics {
			n += len(g.ObservationIDs)
		}
		out = append(out, tf(l, "summary.metrics", n, len(m.Metrics)))
	}
	if s := r.Secrets; s != nil && len(s.Detections) > 0 {
		files := map[string]bool{}
		for _, d := range s.Detections {
			if d.Path != nil {
				files[*d.Path] = true
			}
		}
		out = append(out, tf(l, "summary.secrets", len(s.Detections), len(files)))
	}
	if len(r.Gaps) > 0 {
		reasons := map[string]bool{}
		for _, g := range r.Gaps {
			reasons[g.Section+"\x00"+g.Code] = true
		}
		out = append(out, tf(l, "summary.gaps", len(r.Gaps), len(reasons)))
	}
	return out
}

// summaryCodeReview keeps the lead, counts and roles, folds the ledger and
// shows the supported and reproduced findings as cards, the others as one
// row each.
func (v secView) summaryCodeReview(b secBlock, findings map[string]Finding) secBlock {
	l := v.L
	c := v.R.CodeReview
	parts := []secPart{b.Parts[0]}
	var ordered []Finding
	for _, id := range c.FindingIDs {
		if finding, ok := findings[id]; ok {
			ordered = append(ordered, finding)
		}
	}
	parts = append(parts, v.reviewFindingParts(ordered)...)
	for _, p := range b.Parts[1:] {
		switch {
		case p.Heading == l.Label("ledger.heading"), p.Heading == l.Label("ledger.modules"), p.Heading == l.Label("ledger.slices"):
			continue
		case p.Heading == l.Label("review.findings"):
			continue
		}
		parts = append(parts, p)
	}
	if c.Ledger != nil {
		parts = append(parts, v.summaryLedger(*c.Ledger)...)
	}
	b.Parts = parts
	return b
}

func (v secView) findingCard(f Finding) secCard {
	l := v.L
	c := secCard{ID: f.ID, Title: f.Title, State: lab(l, "validation", f.Validation) + " · " + lab(l, "severity", f.Severity)}
	c.Lines = append(c.Lines, secItem{Label: l.Label("opening.disposition"), Text: lab(l, "disposition", f.Disposition)})
	seen := make(map[string]bool, len(f.Evidence))
	add := func(label, text string, refs []string) {
		item := secItem{Label: l.Label(label), Text: openingText(text, "")}
		for _, e := range f.Evidence {
			if e.Location == nil || !slices.Contains(refs, e.ID) {
				continue
			}
			x := v.citeEvidence(e.Kind, e.Location.Path, e.Location.StartLine, e.Location.EndLine, e.Excerpt)
			if !seen[x.Location] {
				item.Sources = append(item.Sources, x)
				seen[x.Location] = true
			}
		}
		if item.Text != "" || len(item.Codes) > 0 || len(item.Sources) > 0 {
			c.Lines = append(c.Lines, item)
		}
	}
	for _, x := range f.Conditions {
		add("opening.condition."+x.Status, x.Statement, x.EvidenceIDs)
	}
	var impactRefs []string
	for _, x := range f.Claims {
		if x.Assertion == f.Impact {
			impactRefs = append(impactRefs, x.EvidenceIDs...)
			continue
		}
		if x.Assertion != f.Title || len(f.Conditions) == 0 {
			add("opening.claim."+x.Kind, x.Assertion, x.EvidenceIDs)
		}
	}
	add("opening.impact", f.Impact, impactRefs)
	add("opening.recommendation", f.Recommendation.Summary, nil)
	for _, x := range f.Verification {
		if !nonempty(x) {
			continue
		}
		add("opening.verification", x, nil)
	}
	return c
}

// findingSecondary reports a finding supported only by secondary source.
// If no source evidence has line ranges, use the recorded finding locations.
func findingSecondary(f Finding) bool {
	hasSource := false
	for _, e := range f.Evidence {
		loc := e.Location
		if (e.Kind != "code" && e.Kind != "test") || loc == nil || loc.Path == "" || loc.StartLine < 1 || loc.EndLine < loc.StartLine {
			continue
		}
		hasSource = true
		if e.Kind == "code" && productLocation(loc.Path, loc.StartLine, loc.EndLine) {
			return false
		}
	}
	if hasSource {
		return true
	}
	for _, x := range f.Locations {
		if !secondaryPath(x.Path) {
			return false
		}
	}
	return len(f.Locations) > 0
}

// summaryLedger is the ledger with one row per primary module and one sum
// row for the modules below a secondary path, and the slots folded into
// runs of the same state.
func (v secView) summaryLedger(x ReviewLedger) []secPart {
	l := v.L
	parts := ledgerParts(l, x)
	head := parts[0]
	if x.PrimarySlotsBudgetExhausted > 0 {
		head.Paras = append(head.Paras, tf(l, "summary.ledger_refused_slots", x.PrimarySlotsBudgetExhausted))
	}
	mt := *parts[1].Table
	mt.Rows = nil
	var sum LedgerModule
	secondary := 0
	accepted := 0
	for i, m := range x.Modules {
		if m.Class == record.ClassSecondary || (m.Class == "" && secondaryDir(moduleDir(m.ModuleID))) {
			secondary++
			sum.FilesEligible += m.FilesEligible
			sum.FilesSentToModel += m.FilesSentToModel
			sum.BytesSent += m.BytesSent
			sum.FilesTruncated += m.FilesTruncated
			sum.FilesWithheld += m.FilesWithheld
			sum.FilesUnreadable += m.FilesUnreadable
			sum.FilesBudgetExhausted += m.FilesBudgetExhausted
			sum.FilesSecondaryNotReached += m.FilesSecondaryNotReached
			sum.FilesNotPlanned += m.FilesNotPlanned
			sum.FilesNotSent += m.FilesNotSent
			sum.FilesWithCompletedReview += m.FilesWithCompletedReview
			if m.FilesAccepted != nil {
				accepted += *m.FilesAccepted
			}
			continue
		}
		mt.Rows = append(mt.Rows, parts[1].Table.Rows[i])
	}
	if secondary > 0 {
		row := []secCell{txt(tf(l, "summary.ledger_secondary_modules", secondary)), txt("-"), num(sum.FilesEligible), num(sum.FilesSentToModel), num(sum.BytesSent),
			num(sum.FilesTruncated), num(sum.FilesWithheld), num(sum.FilesUnreadable), num(sum.FilesBudgetExhausted), num(sum.FilesNotPlanned), num(sum.FilesNotSent), num(sum.FilesWithCompletedReview)}
		if ledgerClasses(&x) {
			row = slices.Insert(row, 1, txt(lab(l, "class", record.ClassSecondary)))
			row = slices.Insert(row, 11, num(sum.FilesSecondaryNotReached))
		}
		if x.Files != nil {
			column := 4
			if ledgerClasses(&x) {
				column++
			}
			row = slices.Insert(row, column, num(accepted))
		}
		mt.Rows = append(mt.Rows, row)
	}
	// Slots: consecutive slots of one state and reason share a row.
	type run struct {
		first, last, status, reason string
		n                           int
		modules                     []string
	}
	var runs []run
	seen := map[string]bool{}
	slotOf := map[string]LedgerSlice{}
	modulesOf := map[string][]string{}
	var keys []string
	for _, m := range x.Modules {
		for _, s := range m.Slices {
			if !seen[s.TaskKey] {
				seen[s.TaskKey] = true
				keys = append(keys, s.TaskKey)
				slotOf[s.TaskKey] = s
			}
			modulesOf[s.TaskKey] = append(modulesOf[s.TaskKey], m.ModuleID)
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		_, ka, _ := record.SliceTask(a)
		_, kb, _ := record.SliceTask(b)
		if c := cmp.Compare(ka, kb); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	for _, k := range keys {
		stage, slot, _ := record.SliceTask(k)
		if stage != record.StageInvestigate {
			continue
		}
		s := slotOf[k]
		reason := optText(s.Reason, "")
		if n := len(runs); n > 0 && runs[n-1].status == s.Status && runs[n-1].reason == reason {
			runs[n-1].last, runs[n-1].n = slot, runs[n-1].n+1
		} else {
			runs = append(runs, run{first: slot, last: slot, status: s.Status, reason: reason, n: 1})
		}
		r := &runs[len(runs)-1]
		for _, m := range modulesOf[k] {
			if !slices.Contains(r.modules, m) {
				r.modules = append(r.modules, m)
			}
		}
	}
	st := &secTable{Head: []string{l.Label("col.slots"), l.Label("col.status"), l.Label("col.reason"), l.Label("col.modules")}}
	for _, r := range runs {
		span := r.first
		if r.last != r.first {
			span += "–" + r.last
		}
		reason := "-"
		if r.reason != "" {
			reason = reasonText(l, r.reason)
		}
		st.Rows = append(st.Rows, []secCell{txt(tf(l, "summary.slot_span", span, r.n)), txt(lab(l, "task_status", r.status)), txt(reason), txt(shortList(l, r.modules))})
	}
	out := []secPart{head, {Heading: parts[1].Heading, Table: &mt, Empty: l.Label("ledger.none")}, {Heading: parts[2].Heading, Paras: []string{l.Label("summary.slots_lead")}, Table: st, Empty: l.Label("ledger.none")}}
	return append(out, parts[3:]...)
}

// summaryStatic is one tool table per language with one row per analyzer
// and one table of rule groups.
func (v secView) summaryStatic(b secBlock, obs map[string]Observation, findings map[string]Finding) secBlock {
	l := v.L
	s := v.R.StaticAnalysis
	sevOf := map[string]string{}
	for _, id := range StaticFindingIDs(v.R) {
		if f, ok := findings[id]; ok {
			for _, o := range f.ObservationIDs {
				if sevRank(f.Severity) > sevRank(sevOf[o]) {
					sevOf[o] = f.Severity
				}
			}
		}
	}
	parts := []secPart{b.Parts[0]}
	for _, lang := range s.Languages {
		reading := v.staticInspectionParts(lang, obs)
		parts = append(parts, reading...)
		heading := l.Label("static.language_unknown")
		if len(reading) > 0 {
			heading = reading[0].Heading
		}
		groups := v.staticGroups(lang, obs, sevOf)
		if len(groups) == 0 {
			continue
		}
		gt := &secTable{Head: []string{l.Label("col.tool"), l.Label("col.rule"), l.Label("col.count"), l.Label("col.severity"), l.Label("col.message"), l.Label("col.location")}}
		for _, g := range groups {
			message := g.Message
			if isPromiseRejectDiagnostic(g.Analyzer, g.Rule) {
				if description := l.Labels["rule.promise_rejection"]; description != "" {
					message = description
				}
			}
			gt.Rows = append(gt.Rows, []secCell{code(g.Analyzer), code(g.Rule), num(g.Count), code(joinOr([]string{g.Severity}, "-")), txt(message), txt(placesText(l, g.Primary, g.Secondary))})
		}
		parts = append(parts, secPart{Heading: tf(l, "summary.static_rules", heading), Table: gt})
	}
	suppressed := 0
	for _, lang := range s.Languages {
		for _, x := range lang.Tools {
			for _, id := range x.ObservationIDs {
				if o, ok := obs[id]; ok && o.Suppressed && o.Metric == nil {
					suppressed++
				}
			}
		}
	}
	tail := []string{tf(l, "summary.static_full", len(StaticFindingIDs(v.R)))}
	if suppressed > 0 {
		tail = append(tail, tf(l, "summary.static_suppressed_rows", suppressed))
	}
	parts = append(parts, secPart{Paras: tail})
	b.Parts = parts
	return b
}

func (v secView) summaryMetrics(b secBlock, obs map[string]Observation) secBlock {
	b.Parts = append([]secPart{b.Parts[0]}, v.measurementParts(obs, false)...)
	return b
}

// summarySecrets lists the detections on primary paths and counts the
// others by rule.
func (v secView) summarySecrets(b secBlock) secBlock {
	l := v.L
	sc := v.R.Secrets
	parts := []secPart{b.Parts[0], b.Parts[1]}
	dt := &secTable{Head: []string{l.Label("col.rule"), l.Label("col.location"), l.Label("col.evidence_state"), l.Label("col.finding")}, RowIDs: []string{}}
	rules := map[string]int{}
	var ruleOrder []string
	for _, x := range sc.Detections {
		if x.Path != nil && secondaryPath(*x.Path) {
			if rules[x.RuleID] == 0 {
				ruleOrder = append(ruleOrder, x.RuleID)
			}
			rules[x.RuleID]++
			continue
		}
		f := secCell{Text: "-"}
		if x.FindingID != nil {
			f = link(*x.FindingID)
		}
		dt.RowIDs = append(dt.RowIDs, x.ObservationID)
		dt.Rows = append(dt.Rows, []secCell{code(x.RuleID), code(detectionPlace(l, x)), code(x.EvidenceState), f})
	}
	p := secPart{Heading: l.Label("secrets.detections"), Table: dt, Empty: l.Label("secrets.none")}
	if len(ruleOrder) > 0 {
		var counts []string
		n := 0
		for _, r := range ruleOrder {
			counts = append(counts, r+" "+strconv.Itoa(rules[r]))
			n += rules[r]
		}
		p.Paras = append(p.Paras, tf(l, "summary.secrets_secondary", n, strings.Join(counts, ", ")))
		if len(dt.Rows) == 0 {
			p.Empty = ""
		}
	}
	parts = append(parts, p)
	b.Parts = parts
	return b
}

// summaryEnvironment keeps the environment section but lists only primary
// modules: the modules, requirements and verdicts of modules below a
// secondary path are counted, and verdicts that are not satisfied or
// mismatched are grouped by tool and reason.
func (v secView) summaryEnvironment(b secBlock) secBlock {
	l := v.L
	e := v.R.Environment
	if e.Discovery != DiscoveryAccepted {
		return b
	}
	secondaryModule := map[string]bool{}
	var secondaryIDs []string
	for _, m := range e.Modules {
		if secondaryDir(m.Path) {
			secondaryModule[m.ID] = true
			secondaryIDs = append(secondaryIDs, m.ID)
		}
	}
	var parts []secPart
	for _, p := range b.Parts {
		switch p.Heading {
		case l.Label("env.modules"):
			t := &secTable{Head: p.Table.Head}
			for i, m := range e.Modules {
				if !secondaryModule[m.ID] {
					t.Rows = append(t.Rows, p.Table.Rows[i])
				}
			}
			p.Table = t
			if len(secondaryIDs) > 0 {
				p.Paras = append(p.Paras, tf(l, "summary.env_secondary_modules", len(secondaryIDs), shortList(l, secondaryIDs)))
			}
		case l.Label("env.requirements"):
			t := &secTable{Head: p.Table.Head}
			n := 0
			for i, q := range e.Requirements {
				if secondaryModule[q.Module] {
					n++
					continue
				}
				t.Rows = append(t.Rows, p.Table.Rows[i])
			}
			p.Table = t
			if n > 0 {
				p.Paras = append(p.Paras, tf(l, "summary.env_secondary_requirements", n))
			}
		case l.Label("env.verdicts"):
			t := &secTable{Head: []string{l.Label("col.module"), l.Label("col.tool"), l.Label("col.requirement"), l.Label("col.status"), l.Label("col.reason")}}
			type key struct{ tool, status, reason string }
			grouped := map[key][]string{}
			var keys []key
			secondary := map[string]int{}
			var secondaryMismatch [][]secCell
			for i, x := range e.Verdicts {
				if secondaryModule[x.Module] {
					// A mismatch stays visible (the overview points to it);
					// the other verdicts of secondary modules are counted.
					if x.Status == "mismatch" {
						secondaryMismatch = append(secondaryMismatch, p.Table.Rows[i])
						continue
					}
					secondary[x.Status]++
					continue
				}
				if x.Status == "satisfied" || x.Status == "mismatch" {
					t.Rows = append(t.Rows, p.Table.Rows[i])
					continue
				}
				k := key{x.Tool, x.Status, optText(x.Reason, "-")}
				if _, ok := grouped[k]; !ok {
					keys = append(keys, k)
				}
				grouped[k] = append(grouped[k], x.Module)
			}
			for _, k := range keys {
				t.Rows = append(t.Rows, []secCell{txt(shortList(l, grouped[k])), code(k.tool), code("-"), txt(lab(l, "verdict_env", k.status)), code(k.reason)})
			}
			t.Rows = append(t.Rows, secondaryMismatch...)
			p.Table = t
			if len(secondary) > 0 {
				var counts []string
				total := 0
				for _, s := range []string{"satisfied", "unknown"} {
					if secondary[s] > 0 {
						counts = append(counts, lab(l, "verdict_env", s)+" "+strconv.Itoa(secondary[s]))
						total += secondary[s]
					}
				}
				p.Paras = append(p.Paras, tf(l, "summary.env_secondary_verdicts", total, strings.Join(counts, ", ")))
			}
		}
		parts = append(parts, p)
	}
	b.Parts = parts
	return b
}

// summaryDocuments lists the documents that were read on primary paths and
// counts the others by state; the module table keeps primary modules.
func (v secView) summaryDocuments(b secBlock) secBlock {
	l := v.L
	d := v.R.Documents
	if d.Discovery != DiscoveryAccepted {
		return b
	}
	var parts []secPart
	for _, p := range b.Parts {
		switch p.Heading {
		case l.Label("docs.items"):
			t := &secTable{Head: p.Table.Head}
			notRead := map[string][]string{}
			var states []string
			secondary := 0
			for i, x := range d.Items {
				if secondaryPath(x.Path) {
					secondary++
					continue
				}
				if x.Status == "selected" {
					t.Rows = append(t.Rows, p.Table.Rows[i])
					continue
				}
				if _, ok := notRead[x.Status]; !ok {
					states = append(states, x.Status)
				}
				notRead[x.Status] = append(notRead[x.Status], x.Path)
			}
			p.Table = t
			for _, s := range states {
				p.Paras = append(p.Paras, tf(l, "summary.docs_state", lab(l, "doc_status", s), len(notRead[s]), shortList(l, notRead[s])))
			}
			if secondary > 0 {
				p.Paras = append(p.Paras, tf(l, "summary.docs_secondary", secondary))
			}
		case l.Label("docs.modules"):
			t := &secTable{Head: p.Table.Head}
			n := 0
			for i, m := range d.Modules {
				if secondaryDir(moduleDir(m.Module)) {
					n++
					continue
				}
				t.Rows = append(t.Rows, p.Table.Rows[i])
			}
			p.Table = t
			if n > 0 {
				p.Paras = append(p.Paras, tf(l, "summary.docs_secondary_modules", n))
			}
		case l.Label("docs.overlaps"):
			t := &secTable{Head: p.Table.Head}
			n := 0
			for i, o := range d.Overlaps {
				if secondaryDir(o.Path) {
					n++
					continue
				}
				t.Rows = append(t.Rows, p.Table.Rows[i])
			}
			p.Table = t
			if n > 0 {
				p.Paras = append(p.Paras, tf(l, "summary.docs_secondary_overlaps", n))
			}
		}
		parts = append(parts, p)
	}
	b.Parts = parts
	return b
}

// summaryGaps groups the gaps by section, gap and detail.
func (v secView) summaryGaps(b secBlock) secBlock {
	l := v.L
	type key struct{ section, code, detail string }
	grouped := map[key][]string{}
	var keys []key
	for _, g := range v.R.Gaps {
		k := key{g.Section, g.Code, optText(g.Detail, "")}
		if _, ok := grouped[k]; !ok {
			keys = append(keys, k)
		}
		grouped[k] = append(grouped[k], optText(g.Subject, "-"))
	}
	t := &secTable{Head: []string{l.Label("col.section"), l.Label("col.gap"), l.Label("col.detail"), l.Label("col.count"), l.Label("col.subject")}}
	for _, k := range keys {
		detail := "-"
		if k.detail != "" {
			detail = reasonText(l, k.detail)
		}
		t.Rows = append(t.Rows, []secCell{txt(lab(l, "section", k.section)), txt(lab(l, "gap", k.code)), txt(detail), num(len(grouped[k])), code(shortList(l, grouped[k]))})
	}
	b.Parts = []secPart{{Paras: []string{l.Label("gaps.lead")}, Table: t, Empty: l.Label("gaps.none")}}
	return b
}

// summaryAppendix is the appendix in counts: the AI review state, the
// coverage figures, the tasks that did not succeed grouped by state and
// reason, executions and artifacts by count, and the limitations.
func (v secView) summaryAppendix() []secPart {
	l := v.L
	r := v.R
	ai := r.Coverage.AIReview
	aiText := tf(l, "summary.ai_review", ai.Status, optText(ai.Reason, "-"), len(r.Coverage.ReviewCalls))
	files := func(p *int) string {
		if p == nil {
			return l.Label("unknown")
		}
		return tf(l, "summary.files", *p)
	}
	cov := []string{
		tf(l, "summary.coverage", r.Coverage.ScopeMode, tf(l, "summary.files", r.Coverage.InventoryFiles), files(r.Coverage.ToolProcessedFiles), files(r.Coverage.LLMExposedFiles), r.Coverage.TestStatus),
	}
	cov = append(cov, v.Notes...)
	out := []secPart{{Heading: l.Label("ai_review"), Paras: []string{aiText}}, {Heading: l.Label("coverage"), Paras: cov}}
	// Tasks.
	type key struct{ status, reason string }
	grouped := map[key][]string{}
	var keys []key
	byStatus := map[string]int{}
	for _, t := range r.Tasks {
		byStatus[t.Status]++
		reason := optText(t.Reason, "")
		if t.Status == string(domain.TaskSucceeded) || (t.Status == string(domain.TaskSkippedPolicy) && domain.NothingToDo(reason)) {
			continue
		}
		k := key{t.Status, reason}
		if _, ok := grouped[k]; !ok {
			keys = append(keys, k)
		}
		grouped[k] = append(grouped[k], t.Key)
	}
	var states []string
	for s, n := range byStatus {
		states = append(states, s+" "+strconv.Itoa(n))
	}
	sort.Strings(states)
	tt := &secTable{Head: []string{l.Label("col.status"), l.Label("col.reason"), l.Label("col.count"), l.Label("col.task")}}
	for _, k := range keys {
		reason := "-"
		if k.reason != "" {
			reason = reasonText(l, k.reason)
		}
		tt.Rows = append(tt.Rows, []secCell{txt(lab(l, "task_status", k.status)), txt(reason), num(len(grouped[k])), code(shortList(l, grouped[k]))})
	}
	out = append(out, secPart{Heading: l.Label("tasks"), Paras: []string{tf(l, "summary.tasks", len(r.Tasks), strings.Join(states, ", "))}, Table: tt})
	execs := map[string]int{}
	for _, e := range r.Executions {
		execs[e.Status]++
	}
	var es []string
	for s, n := range execs {
		es = append(es, s+" "+strconv.Itoa(n))
	}
	sort.Strings(es)
	var bytes int64
	for _, a := range r.Artifacts {
		bytes += a.SizeBytes
	}
	out = append(out, secPart{Heading: l.Label("summary.records"), Paras: []string{
		tf(l, "summary.executions", len(r.Executions), joinOr(es, "-")),
		tf(l, "summary.artifacts", len(r.Artifacts), bytes),
		l.Label("summary.full_hint"),
	}})
	var lims []secItem
	for _, x := range v.Limitations {
		lims = append(lims, secItem{Text: x})
	}
	out = append(out, listPart(l.Label("limitations"), lims, l.Label("none")))
	for i := range out {
		x := &out[i]
		x.ShowEmpty = x.Empty != "" && (x.Table == nil || len(x.Table.Rows) == 0) && len(x.List) == 0
	}
	return out
}

// dropDanglingLinks removes in-page links whose target the summary does not
// show (a tool finding among the priorities, an observation row).
func (v *secView) dropDanglingLinks() {
	ids := map[string]bool{"appendix": true}
	var visit func([]secPart)
	visit = func(parts []secPart) {
		for _, p := range parts {
			if p.Anchor != "" {
				ids[p.Anchor] = true
			}
			for _, c := range p.Cards {
				ids[c.ID] = true
			}
			if t := p.Table; t != nil {
				for _, id := range t.RowIDs {
					ids[id] = true
				}
				for _, id := range t.ObservationIDs {
					ids[id] = true
				}
			}
			for _, f := range p.Findings {
				ids[f.ID] = true
			}
			visit(p.Parts)
		}
	}
	for _, s := range v.Sections {
		ids[s.ID] = true
		visit(s.Parts)
	}
	visit(v.Appendix)
	visit(v.OverviewDetail)
	var fix func([]secPart)
	fix = func(parts []secPart) {
		for i := range parts {
			p := &parts[i]
			for j := range p.List {
				if p.List[j].Link != "" && !ids[p.List[j].Link] {
					p.List[j].Link = ""
				}
			}
			if t := p.Table; t != nil {
				for _, row := range t.Rows {
					for k := range row {
						if row[k].Anchor != "" && !ids[row[k].Anchor] {
							row[k].Anchor = ""
						}
					}
				}
			}
			fix(p.Parts)
		}
	}
	for i := range v.Sections {
		fix(v.Sections[i].Parts)
	}
	fix(v.Appendix)
	fix(v.OverviewDetail)
}

// --- Markdown ----------------------------------------------------------------

func renderMarkdownSummary(r Report, digest string, l Locale) []byte {
	v := newSummaryView(r, digest, l)
	var b strings.Builder
	p := func(s ...string) {
		for _, x := range s {
			b.WriteString(x)
		}
		b.WriteByte('\n')
	}
	if r.Project != nil {
		p("# ", mdText(r.Project.Name))
		p()
		p(mdText(l.Label("title")))
	} else {
		p("# ", mdText(l.Label("title")))
	}
	p()
	p("**", mdText(v.ReportStatus()), "**")
	if source := v.HeaderSource(); source != "" {
		p(mdText(l.Label("report.target")), " · ", mdCode(source))
	}
	timing := v.HeaderTiming()
	for _, at := range timing.Times {
		p(mdText(at.Label), " · ", mdText(at.Display))
	}
	if timing.Duration != "" {
		p(mdText(l.Label("identity.duration")), " · ", mdText(timing.Duration))
	}
	p()
	if v.HasStatusBanner() {
		p("> ", mdText(v.Banner))
		p()
	}
	if !r.Export.IncludeCode {
		p(mdText(l.Label("opening.code_free")))
		p()
	}

	for _, s := range v.Sections {
		mdSectionAnchor(p, s.ID)
		p("## ", mdText(s.Title))
		p()
		for _, part := range s.Parts {
			mdComparisonAnchor(p, v.Decision, v.Behaviors, part.Anchor)
			mdPart(p, part, l)
		}
		if s.ID == "overview" {
			for _, signal := range v.ReadingSignals() {
				p(mdText(signal))
				p()
			}
			mdPart(p, v.TestRunPart(), l)
			mdBehaviors(p, v.Behaviors, l)
			mdDecision(p, v.Decision, l)
			p("### ", mdText(l.Label("opening.coverage_details")))
			p()
			for _, part := range v.OverviewDetail {
				mdPart(p, part, l)
			}
		}
	}
	p("## ", mdText(l.Label("section.appendix")))
	p()
	for _, part := range v.Appendix {
		mdPart(p, part, l)
	}
	identity := &secTable{Head: []string{l.Label("col.item"), l.Label("opening.value")}}
	for _, field := range v.Identity() {
		identity.Rows = append(identity.Rows, []secCell{txt(field.Label), txt(field.Value)})
	}
	mdPart(p, secPart{Heading: l.Label("opening.source_details"), Table: identity}, l)
	mdTechnical(p, r, v.view, l)
	p("digest sha256: ", mdCode(digest))
	return []byte(b.String())
}

func mdDecision(p func(...string), d projectDecision, l Locale) {
	p("### ", mdText(d.Title))
	p()
	p(mdText(d.State))
	p()
	if d.Review && (d.DocumentClaim == "" && len(d.ComparisonRows) == 0 || d.AdditionalObservation) {
		p("> ", mdText(l.Label("insight.interpretation")))
		p()
	}
	for _, row := range d.ComparisonRows {
		p("**", mdText(row.Label), "** ", mdInlineComparison(row.Text))
		p()
		for _, source := range row.Sources {
			mdCitations(p, []sourceCitation{source.Brief()})
		}
	}
	if d.DocumentClaim != "" {
		p(mdText(d.DocumentClaim))
		p()
	}
	if d.Reason != "" {
		if d.ReasonLabel != "" {
			p("**", mdText(d.ReasonLabel), "** ", mdText(d.Reason))
		} else {
			p(mdText(d.Reason))
		}
		p()
	}
	if d.Condition != "" {
		p("**", mdText(l.Label("decision.option")), "** ", mdText(d.Condition))
		p()
	}
	if d.Boundary != "" {
		p("**", mdText(l.Label("decision.unconfirmed")), "** ", mdText(d.Boundary))
		p()
	}
	if d.Limitation != "" {
		p("**", mdText(l.Label("decision.finding_limitation")), "** ", mdText(d.Limitation))
		p()
	}
	if d.Next != "" {
		label := "opening.verification"
		if d.Tone == "action" {
			label = "decision.next"
			if d.Limitation != "" {
				label = "decision.conditional_action"
			}
		}
		p("**", mdText(l.Label(label)), "** ", mdText(d.Next))
		p()
	}
	if d.Scope != "" {
		p(mdText(l.Label("col.applies_to")), " ", mdCode(d.Scope))
		p()
	}
	if d.Followup {
		p(mdText(l.Label("decision.followup")))
		p()
	}
	for _, source := range d.Sources {
		mdCitations(p, []sourceCitation{source.Brief()})
	}
	if d.Action != nil {
		line := d.Action.State
		if d.Action.Title != "" {
			line += " · " + d.Action.Title
		}
		label := "decision.priority_action"
		if d.Action.Limitation != "" {
			label = "decision.priority_review"
		}
		p("**", mdText(l.Label(label)), "** ", mdText(line))
		p()
		if d.Action.Limitation != "" {
			p("**", mdText(l.Label("decision.finding_limitation")), "** ", mdText(d.Action.Limitation))
			p()
		}
		mdCitations(p, []sourceCitation{d.Action.Source.Brief()})
	}
	if len(d.ComparisonCounts) == 0 {
		return
	}
	counts := make([]string, 0, len(d.ComparisonCounts))
	for _, row := range d.ComparisonCounts {
		counts = append(counts, mdText(row.Label)+" "+row.Value)
	}
	p("**", mdText(d.ComparisonHeading), "** · ", strings.Join(counts, " · "))
	p()
	if d.ComparisonExample != "" {
		p("**", mdText(l.Label("decision.document_claim")), "** ", mdInlineComparison(d.ComparisonExample))
		p()
	}
	anchor := d.ComparisonAnchor
	if anchor == "" {
		anchor = "documents"
	}
	p("[", mdText(l.Label("decision.documents")), "](#", anchor, ")")
	p()
}

func mdInlineComparison(s string) string {
	var prose strings.Builder
	for _, part := range inlineCodeParts(s) {
		if part.Code {
			prose.WriteString(mdCodeSpan(oneLine(part.Text)))
		} else {
			prose.WriteString(mdText(part.Text))
		}
	}
	return prose.String()
}

func mdSectionAnchor(p func(...string), section string) {
	if section == "documents" {
		p("<a id=\"documents\"></a>")
		p()
	}
}

func mdComparisonAnchor(p func(...string), d projectDecision, behaviors projectBehaviors, anchor string) {
	if anchor != "" && (anchor == d.ComparisonAnchor || slices.ContainsFunc(behaviors.Rows, func(row projectBehavior) bool {
		return row.Anchor == anchor
	})) {
		p("<a id=\"", anchor, "\"></a>")
		p()
	}
}

// mdCards renders the finding cards of a part.
func mdCards(p func(...string), cards []secCard) {
	for _, c := range cards {
		p("#### ", mdText(c.Title))
		p()
		if c.State != "" {
			p(mdText(c.State))
			p()
		}
		for _, it := range c.Lines {
			mdItem(p, it)
		}
		p()
	}
}
