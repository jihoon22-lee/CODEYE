package report

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func staticCheckKind(id string) string {
	switch id {
	case "go.gofmt", "gofmt":
		return "format"
	case "javascript.tsc", "python.mypy", "python.pyright", "rust.cargocheck":
		return "type"
	case "python.bandit":
		return "other"
	default:
		return "lint"
	}
}

func checkIdentity(tool StaticTool) string {
	id, _, _ := strings.Cut(tool.TaskKey, ".")
	if tool.Analyzer == nil {
		if analyzer, ok := map[string]string{"go-vet": "go.vet", "staticcheck": "go.staticcheck", "gofmt": "go.gofmt",
			"tsc": "javascript.tsc", "mypy": "python.mypy", "pyright": "python.pyright", "bandit": "python.bandit",
			"cargo-check": "rust.cargocheck", "lizard": "any.lizard", "jscpd": "any.jscpd"}[id]; ok {
			return analyzer
		}
	}
	return optText(tool.Analyzer, id)
}

func checkCompleted(tool StaticTool) bool {
	return tool.TaskStatus == string(domain.TaskSucceeded) && tool.Result == DiscoveryAccepted
}

func regularStaticLanguages(r Report) []StaticLanguage {
	if r.StaticAnalysis == nil {
		return nil
	}
	specialized := map[string]bool{}
	if r.Metrics != nil {
		for _, key := range r.Metrics.TaskKeys {
			specialized[key] = true
		}
	}
	if r.Secrets != nil {
		for _, key := range r.Secrets.TaskKeys {
			specialized[key] = true
		}
	}
	var languages []StaticLanguage
	for _, language := range r.StaticAnalysis.Languages {
		tools := make([]StaticTool, 0, len(language.Tools))
		for _, tool := range language.Tools {
			if !specialized[tool.TaskKey] && optText(tool.Purpose, PurposeDiagnostics) == PurposeDiagnostics {
				tools = append(tools, tool)
			}
		}
		if len(tools) > 0 {
			language.Tools = tools
			languages = append(languages, language)
		}
	}
	return languages
}

func staticCheckCounts(languages []StaticLanguage) (planned, accepted int) {
	for _, language := range languages {
		for _, tool := range language.Tools {
			planned++
			if checkCompleted(tool) {
				accepted++
			}
		}
	}
	return planned, accepted
}

func checkOutcome(l Locale, tool StaticTool) string {
	status := lab(l, "task_status", tool.TaskStatus)
	if tool.TaskReason != nil {
		return status + " · " + reasonText(l, *tool.TaskReason)
	}
	if tool.Result != DiscoveryAccepted {
		return status + " · " + lab(l, "result", tool.Result)
	}
	return status
}

func checkFileScope(l Locale, tool StaticTool, submitted bool) string {
	if tool.ProcessedFiles == nil {
		return l.Label("inspection.files_unknown")
	}
	mode := "checked"
	if submitted {
		mode = "submitted"
	}
	if tool.FilesInScope == nil {
		return tf(l, "inspection."+mode, *tool.ProcessedFiles)
	}
	return tf(l, "inspection."+mode+"_scope", *tool.FilesInScope, *tool.ProcessedFiles)
}

type checkResultGroup struct {
	id, status, result, reason, version, rulePack string
}

func (v secView) staticInspectionParts(language StaticLanguage, obs map[string]Observation) []secPart {
	l := v.L
	heading := l.Label("static.language_unknown")
	if eco := language.Ecosystem; eco != nil {
		heading = lab(l, "inspection.language", *eco)
	}
	groups := map[checkResultGroup][]StaticTool{}
	kinds := map[string][]checkResultGroup{}
	for _, tool := range language.Tools {
		if optText(tool.Purpose, PurposeDiagnostics) != PurposeDiagnostics {
			continue
		}
		id := checkIdentity(tool)
		key := checkResultGroup{id: id, status: tool.TaskStatus, result: tool.Result, reason: optText(tool.TaskReason, ""),
			version: optText(tool.ToolVersion, ""), rulePack: optText(tool.RulePack, "")}
		if len(groups[key]) == 0 {
			kind := staticCheckKind(id)
			kinds[kind] = append(kinds[kind], key)
		}
		groups[key] = append(groups[key], tool)
	}
	if len(groups) == 0 {
		return nil
	}
	coverage := map[string]ToolCoverage{}
	for _, tool := range v.R.Coverage.Tools {
		coverage[tool.TaskKey] = tool
	}
	details := map[string]bool{}
	parts := []secPart{{Heading: heading}}
	for _, kind := range []string{"lint", "type", "format", "other"} {
		category := secPart{Heading: lab(l, "inspection.kind", kind)}
		for _, key := range kinds[kind] {
			rows := groups[key]
			tool := rows[0]
			name := optText(tool.Tool, key.id)
			if localized, ok := l.Labels["inspection.name."+key.id]; ok {
				name = localized
			}
			if key.version != "" {
				name += " " + key.version
			}
			detail := l.Labels["inspection.tool."+key.id]
			if strings.HasPrefix(key.rulePack, "codeye-js-eslint-rejection-types@") {
				detail = l.Label("inspection.js_rejection")
			}
			if details[detail] {
				detail = ""
			}
			details[detail] = true
			if len(rows) == 1 {
				part := v.staticTaskPart(tool, kind, coverage[tool.TaskKey], obs, false)
				part.List[0].Label = name
				if strings.Contains(tool.TaskKey, ".") {
					part.List[0].Codes = []string{tool.TaskKey}
				}
				if detail != "" {
					part.List[0].Text += " · " + detail
				}
				category.Parts = append(category.Parts, part)
				continue
			}
			outcome := checkOutcome(l, tool)
			if detail != "" {
				outcome += " · " + detail
			}
			fold := secPart{Fold: tf(l, "inspection.module_checks", len(rows))}
			for _, row := range rows {
				fold.Parts = append(fold.Parts, v.staticTaskPart(row, kind, coverage[row.TaskKey], obs, true))
			}
			category.Parts = append(category.Parts, secPart{List: []secItem{{Label: name, Text: outcome}}, Parts: []secPart{fold}})
		}
		if len(category.Parts) == 0 {
			if kind == "other" || language.Ecosystem == nil {
				continue
			}
			category.List = []secItem{{Text: l.Label("inspection.not_planned")}}
		}
		parts = append(parts, category)
	}
	return parts
}

func (v secView) staticTaskPart(tool StaticTool, kind string, coverage ToolCoverage, obs map[string]Observation, grouped bool) secPart {
	l := v.L
	item := secItem{}
	if grouped {
		item.Codes = []string{tool.TaskKey}
	} else {
		item.Text = checkOutcome(l, tool)
	}
	total := 0
	for _, id := range tool.ObservationIDs {
		if !obs[id].Suppressed {
			total++
		}
	}
	if checkCompleted(tool) {
		switch {
		case kind == "format" && total == 0:
			item.Text = l.Label("inspection.format_clean")
		case kind == "format":
			item.Text = tf(l, "inspection.format_hits", total)
		case total == 0:
			item.Text = l.Label("inspection.diagnostics_clean")
		default:
			item.Text = tf(l, "inspection.diagnostic_hits", total)
		}
		item.Text += " · " + checkFileScope(l, tool, false)
	} else if total > 0 {
		item.Text = strings.TrimPrefix(item.Text+" · "+tf(l, "inspection.recorded_diagnostics", total), " · ")
	}
	if n := coverage.Packages["format_input_limit"]; kind == "format" && n > 0 {
		item.Text += " · " + tf(l, "inspection.format_limit", n)
	}
	part := secPart{List: []secItem{item}}
	if paths := coverage.ProcessedPaths; len(paths) > 0 {
		part.Parts = []secPart{processedPathsPart(l, "inspection.processed_paths", paths)}
	}
	return part
}

func processedPathsPart(l Locale, label string, paths []string) secPart {
	part := secPart{Fold: tf(l, label, len(paths))}
	for _, path := range paths {
		part.List = append(part.List, secItem{Codes: []string{path}})
	}
	return part
}

func (v secView) staticResults(language StaticLanguage, obs map[string]Observation) secPart {
	l := v.L
	severity := map[string]string{}
	excerpts := map[string]*sourceexcerpt.Excerpt{}
	var findingIDs []string
	for _, tool := range language.Tools {
		findingIDs = append(findingIDs, tool.FindingIDs...)
	}
	for _, finding := range findingsOf(v.R, findingIDs) {
		for _, id := range finding.ObservationIDs {
			if sevRank(finding.Severity) > sevRank(severity[id]) {
				severity[id] = finding.Severity
			}
			o := obs[id]
			if o.EvidenceState != EvidenceVerified || o.Location == nil {
				continue
			}
			for _, e := range finding.Evidence {
				if e.Excerpt != nil && e.Location != nil && e.Location.Path == o.Location.Path && e.Location.StartLine == o.Location.StartLine {
					excerpts[id] = e.Excerpt
					break
				}
			}
		}
	}
	table := &secTable{Head: []string{l.Label("col.tool"), l.Label("col.rule"), l.Label("col.message"), l.Label("col.count"),
		l.Label("col.severity"), l.Label("col.location")}, DetailGroup: "static-detail"}
	for _, g := range v.staticGroups(language, obs, severity) {
		rank := func(o Observation) int {
			switch {
			case o.Location == nil:
				return 3
			case !productLocation(o.Location.Path, o.Location.StartLine, o.Location.EndLine):
				return 2
			case excerpts[o.ID] != nil:
				return 0
			default:
				return 1
			}
		}
		o := obs[g.ObservationIDs[0]]
		severityRank := func(observation Observation) int {
			if s := severity[observation.ID]; s != "" {
				return sevRank(s)
			}
			return sevRank(optText(observation.RawSeverity, ""))
		}
		for _, id := range g.ObservationIDs[1:] {
			next := obs[id]
			if a, b := severityRank(next), severityRank(o); a > b || a == b && rank(next) < rank(o) {
				o = next
			}
		}
		location := l.Label("none")
		message := v.Message[o.ID]
		summary := cutLine(message, summaryMessageRunes)
		if isPromiseRejectDiagnostic(g.Analyzer, g.Rule) {
			summary = l.Label("rule.promise_rejection")
		}
		detail := secTableDetail{ID: "static-detail-" + o.ID, Label: g.Rule}
		if summary != strings.TrimSpace(message) {
			detail.Text = message
		}
		if o.Location != nil {
			source := v.inspectionCitation(o.Location, excerpts[o.ID])
			location = source.Location
			detail.Label += " · " + location
			detail.Sources = []sourceCitation{source}
			if source.Text == "" {
				detail.Text = strings.TrimSpace(detail.Text + " " + l.Label("inspection.no_excerpt"))
			}
		}
		table.Rows = append(table.Rows, []secCell{code(g.Analyzer), code(g.Rule), txt(summary), num(g.Count), txt(lab(l, "severity", g.Severity)), code(location)})
		table.Details = append(table.Details, detail)
	}
	return secPart{Table: table}
}

func (v secView) measurementParts(obs map[string]Observation, detail bool) []secPart {
	l := v.L
	metrics := v.R.Metrics
	groups := map[string]MetricGroup{}
	for _, group := range metrics.Metrics {
		groups[group.Name] = group
	}
	tools := map[string]StaticTool{}
	for _, language := range v.R.StaticAnalysis.Languages {
		for _, tool := range language.Tools {
			tools[tool.TaskKey] = tool
		}
	}
	coverage := map[string]ToolCoverage{}
	for _, tool := range v.R.Coverage.Tools {
		coverage[tool.TaskKey] = tool
	}
	var parts []secPart
	for _, key := range metrics.TaskKeys {
		tool, ok := tools[key]
		if !ok {
			continue
		}
		id := checkIdentity(tool)
		name := map[string]string{"any.lizard": "cyclomatic_complexity", "any.jscpd": "duplicated_lines"}[id]
		group := groups[name]
		heading := key
		if name != "" {
			heading = l.Label("measurement.name." + name)
		}
		line := optText(tool.Tool, key)
		if tool.ToolVersion != nil {
			line += " " + *tool.ToolVersion
		}
		if checkCompleted(tool) {
			line += " · " + checkFileScope(l, tool, name == "duplicated_lines")
		} else {
			line += " · " + checkOutcome(l, tool)
		}
		units := coverage[key].Packages
		if n, ok := units["functions_measured"]; ok && checkCompleted(tool) {
			line += " · " + tf(l, "measurement.functions", n)
		}
		if n := units["excluded_third_party_directory"]; n > 0 {
			line += " · " + tf(l, "measurement.excluded_external", n)
		}
		if n := units["excluded_data_directory"]; n > 0 {
			line += " · " + tf(l, "measurement.excluded_data", n)
		}
		if name != "" && (checkCompleted(tool) || len(group.ObservationIDs) > 0) {
			line += " · " + l.Label("measurement.threshold."+name)
		}
		part := secPart{Heading: heading, Paras: []string{line}}
		if paths := coverage[key].ProcessedPaths; detail && len(paths) > 0 {
			label := "inspection.processed_paths"
			if name == "duplicated_lines" {
				label = "measurement.submitted_paths"
			}
			part.Parts = append(part.Parts, processedPathsPart(l, label, paths))
		}
		if name != "" {
			part.Paras = append(part.Paras, l.Label("measurement.meaning."+name))
		}
		if checkCompleted(tool) {
			if slices.Contains(coverage[key].Limitations, "no_file_reached_clone_thresholds") {
				part.Paras = append(part.Paras, l.Label("measurement.no_eligible_clones"))
			} else if len(group.ObservationIDs) == 0 {
				part.Paras = append(part.Paras, l.Label("measurement.clean"))
			} else if name != "" {
				part.Paras = append(part.Paras, tf(l, "measurement.hits."+name, len(group.ObservationIDs))+" "+l.Label("measurement.next."+name))
			}
		}
		var results []Observation
		legacy := 0
		for _, id := range group.ObservationIDs {
			o, ok := obs[id]
			if !ok || o.Metric == nil {
				continue
			}
			if legacyThirdParty(o) {
				legacy++
			}
			results = append(results, o)
		}
		if legacy > 0 {
			part.Paras = append(part.Paras, tf(l, "measurement.legacy_external", legacy))
		}
		sort.SliceStable(results, func(i, j int) bool {
			if a, b := legacyThirdParty(results[i]), legacyThirdParty(results[j]); a != b {
				return !a
			}
			return results[i].Metric.Value > results[j].Metric.Value
		})
		if name == "duplicated_lines" {
			part.Parts = append(part.Parts, v.cloneParts(results, detail)...)
		} else {
			first := min(summaryShown, len(results))
			if first > 0 {
				part.Table = v.measurementTable(results[:first], detail)
			}
			if rest := results[first:]; len(rest) > 0 {
				if detail {
					part.Parts = append(part.Parts, secPart{Fold: tf(l, "measurement.other_locations", len(rest)), Table: v.measurementTable(rest, true)})
				} else {
					part.Paras = append(part.Paras, tf(l, "measurement.full", len(rest)))
				}
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return []secPart{{Paras: []string{l.Label("measurement.not_planned")}}}
	}
	return parts
}

type cloneRange struct {
	path       string
	start, end int
}

func cloneRanges(o Observation) []cloneRange {
	var ranges []cloneRange
	for _, loc := range []*ObservationLocation{o.Location, o.Metric.Counterpart} {
		if loc != nil {
			ranges = append(ranges, cloneRange{loc.Path, loc.StartLine, loc.EndLine})
		}
	}
	return ranges
}

// Clone records sharing the same structured source range belong to the
// same group. Individual pair relations are kept under each group.
func groupClones(results []Observation) [][]Observation {
	parents := make([]int, len(results))
	for i := range parents {
		parents[i] = i
	}
	root := func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	firstAt := map[cloneRange]int{}
	for i, o := range results {
		for _, place := range cloneRanges(o) {
			if first, ok := firstAt[place]; ok {
				parents[root(i)] = root(first)
			} else {
				firstAt[place] = i
			}
		}
	}
	var groups [][]Observation
	groupOf := map[int]int{}
	for i, o := range results {
		parent := root(i)
		idx, ok := groupOf[parent]
		if !ok {
			idx = len(groups)
			groupOf[parent] = idx
			groups = append(groups, nil)
		}
		groups[idx] = append(groups[idx], o)
	}
	return groups
}

func (v secView) cloneParts(results []Observation, detail bool) []secPart {
	var parts []secPart
	groups := groupClones(results)
	var rest secPart
	if len(groups) > summaryShown {
		rest.Fold = tf(v.L, "measurement.other_clone_groups", len(groups)-summaryShown)
	}
	if len(results) > 0 && len(groups) == len(results) {
		first := min(len(results), summaryShown)
		parts = append(parts, secPart{Table: v.measurementTable(results[:first], detail)})
		if first < len(results) {
			if detail {
				rest.Table = v.measurementTable(results[first:], true)
				parts = append(parts, rest)
			} else {
				parts = append(parts, secPart{Paras: []string{tf(v.L, "measurement.full", len(results)-first)}})
			}
		}
		return parts
	}
	for i, rows := range groups {
		if !detail && i == summaryShown {
			parts = append(parts, secPart{Paras: []string{tf(v.L, "measurement.full", len(groups)-summaryShown)}})
			break
		}
		part := secPart{Table: v.measurementTable(rows[:1], detail)}
		if len(rows) > 1 {
			seen := map[cloneRange]bool{}
			for _, o := range rows {
				for _, loc := range cloneRanges(o) {
					seen[loc] = true
				}
			}
			part.Paras = []string{tf(v.L, "measurement.clone_group", len(rows), len(seen))}
			if detail {
				part.Parts = []secPart{{Fold: tf(v.L, "measurement.clone_details", len(rows)-1), Table: v.measurementTable(rows[1:], true)}}
			}
		}
		if i < summaryShown {
			parts = append(parts, part)
		} else {
			rest.Parts = append(rest.Parts, part)
		}
	}
	if len(rest.Parts) > 0 {
		parts = append(parts, rest)
	}
	return parts
}

func legacyThirdParty(o Observation) bool {
	return o.Location != nil && anypack.ThirdPartyDirectory(o.Location.Path) ||
		o.Metric != nil && o.Metric.Counterpart != nil && anypack.ThirdPartyDirectory(o.Metric.Counterpart.Path)
}

func (v secView) measurementTable(rows []Observation, detail bool) *secTable {
	table := &secTable{Head: []string{v.L.Label("measurement.value"), v.L.Label("col.location")}, DetailGroup: "metrics-detail"}
	clone := rows[0].Metric.Name == "duplicated_lines"
	if clone {
		table.Head = append(table.Head, v.L.Label("measurement.counterpart"))
	}
	for _, o := range rows {
		m := o.Metric
		item := v.measurementItem(o, detail)
		value := strings.TrimSpace(item.Label + " " + strings.Join(item.Codes, " "))
		location := func(loc *ObservationLocation) secCell {
			if loc == nil {
				return txt("-")
			}
			return code(sourceRange(loc.Path, loc.StartLine, loc.EndLine))
		}
		row := []secCell{txt(value), location(o.Location)}
		if clone {
			row = append(row, location(m.Counterpart))
		}
		table.ObservationIDs = append(table.ObservationIDs, o.ID)
		table.Rows = append(table.Rows, row)
		if detail {
			table.Details = append(table.Details, secTableDetail{ID: "metric-detail-" + o.ID, Label: value + " · " + row[1].Text, Text: item.Text, Sources: item.Sources})
		}
	}
	return table
}

func (v secView) measurementItem(o Observation, detail bool) secItem {
	m := o.Metric
	item := secItem{Label: tf(v.L, "measurement.value."+m.Name, m.Value), ObservationID: o.ID}
	if m.Name == "cyclomatic_complexity" && o.RawMessage != nil {
		marker := " at "
		if o.Location != nil {
			marker += o.Location.Path + ":" + strconv.Itoa(o.Location.StartLine) + ": "
		}
		if name, _, ok := strings.Cut(*o.RawMessage, marker); ok {
			item.Codes = []string{name}
		}
	}
	if detail {
		var notes []string
		for _, target := range []struct {
			loc     *ObservationLocation
			excerpt *sourceexcerpt.Excerpt
		}{{o.Location, m.Excerpt}, {m.Counterpart, m.CounterpartExcerpt}} {
			if target.loc == nil {
				continue
			}
			item.Sources = append(item.Sources, v.inspectionCitation(target.loc, target.excerpt))
			if target.excerpt == nil {
				notes = append(notes, sourceRange(target.loc.Path, target.loc.StartLine, target.loc.EndLine))
			}
		}
		if len(notes) > 0 {
			item.Text = strings.Join(notes, ", ") + " · " + v.L.Label("inspection.no_excerpt")
		}
	}
	if m.Name == "duplicated_lines" && m.Counterpart == nil {
		label := "measurement.no_counterpart"
		if o.EvidenceState == EvidenceVerified {
			label = "measurement.legacy_counterpart"
		}
		item.Text = strings.TrimSpace(item.Text + " " + v.L.Label(label))
		if detail && o.RawMessage != nil {
			item.Text += " " + *o.RawMessage
		}
	}
	return item
}

func (v secView) inspectionCitation(loc *ObservationLocation, excerpt *sourceexcerpt.Excerpt) sourceCitation {
	source := v.citeEvidence("code", loc.Path, loc.StartLine, loc.EndLine, excerpt)
	source.URL = snapshotBrowserURL(v.R, loc.Path, loc.StartLine, loc.EndLine)
	source.LinkLabel = v.L.Label("opening.open_source")
	return source
}

func (s sourceCitation) InspectionPreview() *sourcePreview {
	// A tool starts a measurement or diagnostic at its verified line anchor.
	s.EndLine = s.StartLine
	return s.Preview()
}
