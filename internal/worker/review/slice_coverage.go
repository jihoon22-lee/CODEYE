package review

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review"
)

// Keep the ordinary slot estimate stable for pinned retries. The controller
// requeues the original task keys, so reducing this estimate can leave older
// tasks pending after the worker finishes. This is a manifest-only estimate.
func coverageSlots(plan slicePlan) int {
	if len(plan.Groups) == 0 {
		return 0
	}
	var additionalWindows int64
	for _, list := range [][]plannedFile{plan.Overview, plan.Rest} {
		for _, f := range list {
			// Each extra line costs at least one source byte. Estimate room
			// for short lines that reach the line cap before the byte cap.
			additionalWindows += max(0, (f.Size-1)/roleMaxCodeLines)
		}
	}
	return min(MaxSlices, max(2, plan.Slots, (len(plan.Groups)+4)/5)+(int(additionalWindows)+3)/4)
}

// Budget changes affect admission, so plan keys stay stable across worker
// retries. Count windows from verified text before tool diagnostics or
// model results can reorder them. The smallest possible fit of windows
// bounds slots irrespective of that order. Each overview excerpt may
// split one window, and each tier/class may occur in two scheduling passes.
func (s *roleStage) coverageSlotPlan(groups codeGroups) (int, error) {
	type windowGroup struct{ count, maxBytes int }
	var counts [3][2]windowGroup
	totalWindows := 0
	for _, key := range groups.order {
		for _, e := range groups.files[key] {
			if err := s.ctx.Err(); err != nil {
				return 0, err
			}
			text, ok, err := s.readSource(e)
			if err != nil {
				return 0, err
			}
			if !ok || strings.TrimSpace(text) == "" {
				continue
			}
			class := 0
			if groups.secondary[e.Path] != "" || record.GeneratedHeader([]byte(text)) {
				class = 1
			}
			ranges := missingCoverageWindows(record.ScopeFile{}, strings.Split(text, "\n"), nil, record.MaxSlices, nil)
			if len(ranges) == 0 {
				continue
			}
			totalWindows += len(ranges)
			if totalWindows >= record.MaxSlices*sliceMaxFiles {
				return record.MaxSlices, nil
			}
			count := &counts[groups.tierOf(e.Path)][class]
			count.count += len(ranges)
			count.maxBytes = max(count.maxBytes, min(roleMaxCodeFile, record.JSONLen(text)))
		}
	}
	total := 0
	for _, tier := range counts {
		for _, c := range tier {
			if c.count == 0 {
				continue
			}
			perSlot := min(sliceMaxFiles, sliceMaxBytes/c.maxBytes)
			windows := c.count + min(c.count, roleMaxCodeFiles)
			total += (windows + perSlot - 1) / perSlot
			if tier[0].count > 0 && tier[1].count > 0 {
				total++
			}
		}
	}
	return min(record.MaxSlices, total), nil
}

type coverageCursor struct {
	file     record.ScopeFile
	tier     int
	severity int
	assigned bool
	windows  []record.ScopeFile
}

// scopeTask reads only snapshot content allowed by the input filters. It
// packs verified ranges using their actual encoded size and persists paths,
// line counts and assignments, never source text.
func (s *roleStage) scopeTask() analysis.TaskResult {
	sel, err := reviewpack.For(record.StageInvestigate, languagesInScope(s.j))
	if err != nil {
		return result(domain.TaskFailed, "review_instructions_invalid")
	}
	used, err := callsUsed(s.j)
	if err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	rule := s.h.secondary()
	changed := changedPaths(s.j)
	groups := codeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, rule, changed, true)
	slots := coverageSlots(planSlicesFor(s.j.Snapshot.Manifest, s.j.Scope, rule, changed))
	maxSlots := record.LegacyMaxSlices
	if s.j.Scope.ReviewCoverage == domain.ReviewCoverageExtended {
		// The plan was made before the secrets screen and must keep the
		// same keys if any file has since been withheld.
		planner := roleStage{ctx: s.ctx, j: s.j}
		slots, err = planner.coverageSlotPlan(groups)
		if err != nil {
			return result(domain.TaskFailed, "review_input_unavailable")
		}
		maxSlots = record.MaxSlices
	}
	activeSlots := slots
	maxCalls := s.h.maxCalls(s.j.Scope)
	if maxCalls <= record.LegacyMaxCalls {
		activeSlots = min(activeSlots, record.LegacyMaxSlices)
	}
	sent, up := s.overviewOffered()
	facts := s.priorityFacts(s.entries())
	doc := record.ScopeDoc{Schema: record.ScopeSchemaVersion, Kind: record.ScopeKind, Pack: record.PackRef{ID: sel.ID, Version: sel.Version}, Snapshot: s.j.Snapshot.Digest,
		RunID: s.j.RunID, AttemptID: s.j.AttemptID, Screen: screenOf(s.screen),
		Budget: record.ScopeBudget{MaxCalls: maxCalls, MaxRequestBytes: s.limit().request, WholeRunMaxCalls: record.WholeRunMaxCalls, SliceReserveCalls: record.SliceMinCalls,
			CallsBeforeSlices: used, Admission: record.AdmissionReserveVerification},
		// The limits are those of this node's request budget: a slot holds
		// at most one request of units (slice_run.go reads them back).
		Limits:   record.ScopeLimits{MaxFiles: s.limit().excerpts, MaxBytes: s.limit().source, MaxFileBytes: s.limit().unit, MaxFileLines: s.limit().unit, MaxSlices: maxSlots, Slots: activeSlots},
		Priority: record.ScopePriority{Order: priorityOrder(s.j), Secondary: rule}, Upstream: []record.Upstream{up},
		Modules: []record.ScopeModule{}, Overview: record.ScopeOverview{Files: []record.ScopeFile{}, Offered: []string{}},
		Slices: []record.ScopeSlice{}, Excluded: []record.ScopeFile{}, Limitations: s.screenLimitation(addLim([]string{}, groups.lims...))}
	discovered := map[string]bool{}
	mods, _ := discovery.DetectModules(s.j.Snapshot.Manifest, envdoc.Scope{Kind: "full", Paths: []string{}})
	for _, m := range mods {
		discovered[m.ID] = true
	}
	var cursors []*coverageCursor
	costs := map[string]int{}
	modules := map[string]*record.ScopeModule{}
	for _, key := range groups.order {
		id := moduleID(key)
		module := &record.ScopeModule{ID: id, Discovered: discovered[id], Class: rule.ModuleClass(id)}
		modules[id] = module
		for _, e := range groups.files[key] {
			if s.ctx.Err() != nil {
				return result(domain.TaskCancelled, "cancelled")
			}
			// Paths that cannot be validated as evidence cannot be published.
			if domain.ValidateEvidencePath(e.Path) != nil {
				doc.Limitations = addLim(doc.Limitations, limExcludedSource)
				continue
			}
			f := record.ScopeFile{Path: e.Path, SHA256: e.SHA256, Module: id, Class: record.ClassPrimary,
				Rank: []string{record.RankEntryPoint, record.RankSource, record.RankTest}[groups.rank[e.Path]], PullRequest: groups.change(e.Path)}
			text, reason, err := s.reviewSource(e)
			if err != nil {
				return result(domain.TaskFailed, "review_input_unavailable")
			}
			if seg := groups.secondary[e.Path]; seg != "" {
				f.Class, f.Secondary = record.ClassSecondary, record.ReasonSecondaryPath+":"+seg
			} else if record.GeneratedHeader([]byte(text)) {
				f.Class, f.Secondary = record.ClassSecondary, record.ReasonGeneratedFile
			}
			if n := facts.observations[e.Path]; n > 0 {
				f.Rank, f.Observations, f.Severity = record.RankToolObservations, n, record.SeverityNames[facts.severity[e.Path]]
			} else if facts.failed[id] {
				f.Rank = record.RankAnalyzerNotSucceeded
			}
			module.FilesEligible++
			if f.Class == record.ClassSecondary {
				module.FilesSecondary++
			}
			if reason == "" && strings.TrimSpace(text) == "" {
				reason = record.ScopeReasonEmpty
			}
			if reason != "" {
				f.State, f.Reason = record.FileUnreadable, reason
				if reason == record.ScopeReasonWithheld {
					f.State = record.FileWithheld
				}
				doc.Excluded = append(doc.Excluded, f)
				continue
			}
			lines := strings.Split(text, "\n")
			f.TotalLines = len(lines)
			prior := sent[e.Path]
			covered := make([]bool, len(lines))
			if len(prior) > 0 {
				row := f
				row.State = record.FileCoveredByOverview
				for k, w := range prior {
					if w.Start < 1 || w.End > len(lines) {
						return result(domain.TaskFailed, "review_input_unavailable")
					}
					// A window inside another travels as a reference
					// (requestExcerpts) and adds no bytes.
					if !slices.ContainsFunc(prior[:k], func(x record.Excerpt) bool { return x.Start <= w.Start && w.End <= x.End }) &&
						!slices.ContainsFunc(prior[k+1:], func(x record.Excerpt) bool {
							return x.Start <= w.Start && w.End <= x.End && (x.Start != w.Start || x.End != w.End)
						}) {
						row.PlannedBytes += record.JSONLen(strings.Join(lines[w.Start-1:w.End], "\n"))
					}
					for i := w.Start - 1; i < w.End; i++ {
						if !covered[i] {
							covered[i] = true
							row.PlannedLines++
						}
					}
				}
				row.BoundBytes = row.PlannedBytes
				doc.Overview.Files = append(doc.Overview.Files, row)
			}
			windows := s.unitCoverageWindows(f, lines, covered, activeSlots, &doc.Omitted, costs)
			prioritizeCoverageWindow(windows, text, e.Path, facts.line[e.Path])
			cursors = append(cursors, &coverageCursor{file: f, tier: groups.tierOf(e.Path), severity: facts.severity[e.Path],
				windows: windows})
		}
	}
	slices.SortFunc(doc.Overview.Files, func(a, b record.ScopeFile) int { return cmp.Compare(a.Path, b.Path) })
	for _, f := range doc.Overview.Files {
		doc.Overview.Offered = append(doc.Overview.Offered, f.Path)
	}
	scheduleCoverageWithin(&doc, cursors, sent, costs)
	doc.Limits.Slots = slots
	slotOmissions := map[string]bool{}
	for _, omission := range doc.Omitted {
		if omission.Reason == record.ScopeReasonSlotLimit {
			slotOmissions[omission.Path] = true
		}
	}
	for _, c := range cursors {
		for _, w := range c.windows {
			addScopeOmission(&doc.Omitted, w.Path, w.StartLine, w.StartLine+w.PlannedLines-1, record.ScopeReasonSlotLimit)
			slotOmissions[w.Path] = true
		}
		if len(sent[c.file.Path]) != 0 || c.assigned {
			continue
		}
		f := c.file
		f.State, f.Reason = record.FileNotPlanned, record.ScopeReasonSlotLimit
		if !slotOmissions[f.Path] {
			f.Reason = record.ScopeReasonLineLimit
		}
		doc.Unplanned = append(doc.Unplanned, f)
		modules[f.Module].FilesNotPlanned++
		if f.Class == record.ClassSecondary {
			modules[f.Module].FilesSecondaryNotPlanned++
		}
	}
	for _, key := range groups.order {
		if m := modules[moduleID(key)]; m.FilesEligible > 0 {
			doc.Modules = append(doc.Modules, *m)
		}
	}
	for _, omission := range doc.Omitted {
		doc.Limitations = addLim(doc.Limitations, omission.Reason)
	}
	art, err := persistScope(s.j, doc)
	if err != nil {
		return result(domain.TaskFailed, "review_record_failed")
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}, Limitations: doc.Limitations}
}

func prioritizeCoverageWindow(windows []record.ScopeFile, text, path string, diagnosticLine int) {
	if len(windows) < 2 {
		return
	}
	windowAt := func(line int) int {
		return slices.IndexFunc(windows, func(w record.ScopeFile) bool {
			return line >= w.StartLine && line < w.StartLine+w.PlannedLines
		})
	}
	i := windowAt(diagnosticLine)
	if i < 0 {
		contractExecutableLines(text, path, func(line int, _ bool) bool {
			i = windowAt(line)
			return i >= 0
		})
	}
	if i > 0 {
		window := windows[i]
		copy(windows[1:i+1], windows[:i])
		windows[0] = window
	}
}

func addScopeOmission(list *[]record.ScopeOmission, path string, start, end int, reason string) {
	if list == nil {
		return
	}
	if n := len(*list); n > 0 {
		last := &(*list)[n-1]
		if last.Path == path && last.EndLine == start-1 && last.Reason == reason {
			last.EndLine = end
			return
		}
	}
	*list = append(*list, record.ScopeOmission{Path: path, StartLine: start, EndLine: end, Reason: reason})
}

// Slice requests carry consecutive whole lines. An unsendably long line is
// counted separately and does not hide the remaining lines of that file.
func missingCoverageWindows(f record.ScopeFile, lines []string, covered []bool, slots int, omissions *[]record.ScopeOmission) []record.ScopeFile {
	var out []record.ScopeFile
	isCovered := func(i int) bool { return covered != nil && covered[i] }
	for i := 0; i < len(lines); {
		if isCovered(i) {
			i++
			continue
		}
		start, size := i, 0
		for i < len(lines) && !isCovered(i) && i-start < roleMaxCodeLines {
			cost := record.JSONLen(lines[i])
			if size+cost > roleMaxCodeFile {
				break
			}
			size += cost
			i++
		}
		if i == start {
			addScopeOmission(omissions, f.Path, i+1, i+1, record.ScopeReasonLineLimit)
			i++
			continue
		}
		if !slices.ContainsFunc(lines[start:i], func(line string) bool { return strings.TrimSpace(line) != "" }) {
			addScopeOmission(omissions, f.Path, start+1, i, record.ScopeReasonBlankRange)
			continue
		}
		if len(out) >= slots*sliceMaxFiles {
			addScopeOmission(omissions, f.Path, start+1, i, record.ScopeReasonSlotLimit)
			continue
		}
		f.StartLine, f.PlannedLines, f.PlannedBytes, f.BoundBytes, f.State = start+1, i-start, size, size, record.FilePlanned
		out = append(out, f)
	}
	return out
}

// unitCoverageWindows cuts the lines of f the overview did not send into
// review units (unitsOf) of at most one unit of the request budget each. A
// line longer than a unit is omitted (review_line_input_limit), a blank
// unit too (source_blank_range), and windows beyond what the active slots
// can hold are omitted (review_slice_limit). costs receives each window's
// numbered size plus that of the header it travels with.
func (s *roleStage) unitCoverageWindows(f record.ScopeFile, lines []string, covered []bool, slots int, omissions *[]record.ScopeOmission,
	costs map[string]int) []record.ScopeFile {
	var out []record.ScopeFile
	header := headerEnd(f.Path, lines)
	for i := 0; i < len(lines); {
		if covered != nil && covered[i] {
			i++
			continue
		}
		start := i
		for i < len(lines) && (covered == nil || !covered[i]) {
			i++
		}
		fu, long := unitsOf(f.Path, lines, start+1, i, s.limit().unit)
		type piece struct {
			span lineSpan
			long bool
		}
		var pieces []piece
		for _, u := range fu.units {
			pieces = append(pieces, piece{span: u})
		}
		for _, n := range long {
			pieces = append(pieces, piece{span: lineSpan{n, n}, long: true})
		}
		slices.SortFunc(pieces, func(a, b piece) int { return cmp.Compare(a.span.start, b.span.start) })
		for _, p := range pieces {
			a, b := p.span.start, p.span.end
			switch {
			case p.long:
				addScopeOmission(omissions, f.Path, a, b, record.ScopeReasonLineLimit)
				continue
			case !slices.ContainsFunc(lines[a-1:b], func(line string) bool { return strings.TrimSpace(line) != "" }):
				addScopeOmission(omissions, f.Path, a, b, record.ScopeReasonBlankRange)
				continue
			case len(out) >= slots*s.limit().excerpts:
				addScopeOmission(omissions, f.Path, a, b, record.ScopeReasonSlotLimit)
				continue
			}
			size := record.JSONLen(strings.Join(lines[a-1:b], "\n"))
			w := f
			w.StartLine, w.PlannedLines, w.PlannedBytes, w.BoundBytes, w.State = a, b-a+1, size, size, record.FilePlanned
			costs[windowKey(w)] = numberedSize(lines, a, b)
			if h := min(header, a-1); h > 0 {
				costs[headerKey(w)] = numberedSize(lines, 1, h)
			}
			out = append(out, w)
		}
	}
	return out
}

// unitArtifact reports whether a scope artifact's windows are review units
// (unitCoverageWindows): its line bound is its byte bound, so a window that
// does not start its file was planned with room for the file's header.
// Earlier artifacts planned 50-line windows without one.
func unitArtifact(l record.ScopeLimits) bool { return l.MaxFileLines == l.MaxFileBytes }

func windowKey(f record.ScopeFile) string { return f.Path + "\x00" + strconv.Itoa(f.StartLine) }

// headerKey keys the header cost of a window that does not start its file.
func headerKey(f record.ScopeFile) string { return windowKey(f) + "\x00header" }

// scheduleCoverageWithin fills the slots in priority order. Each
// unrepresented module first gets the units of its best file, then every
// other file follows with all its units together, so a file split into
// units travels in one slot or consecutive slots and a slot holds whole
// directories in path order. Never mix path classes or pull-request tiers
// in one request. Each window costs its numbered size (costs, by
// windowKey; its planned bytes without one) plus its file's header once
// per slot, within the artifact's limits.
func scheduleCoverageWithin(doc *record.ScopeDoc, cursors []*coverageCursor, offered map[string][]record.Excerpt, costs map[string]int) {
	slices.SortFunc(cursors, func(a, b *coverageCursor) int {
		return cmp.Or(cmp.Compare(a.tier, b.tier), cmp.Compare(a.file.Class, b.file.Class),
			cmp.Compare(record.RankIndex(a.file.Rank), record.RankIndex(b.file.Rank)), cmp.Compare(b.severity, a.severity),
			cmp.Compare(b.file.Observations, a.file.Observations), cmp.Compare(a.file.Path, b.file.Path))
	})
	represented := map[string]bool{}
	for _, f := range doc.Overview.Files {
		represented[f.Module] = true
	}
	writer := coverageSlotWriter{doc: doc, costs: costs}
	for first := 0; first < len(cursors); {
		last := first + 1
		for last < len(cursors) && cursors[last].tier == cursors[first].tier {
			last++
		}
		tier := cursors[first:last]
		for _, c := range tier {
			if !represented[c.file.Module] && writer.add(c) {
				represented[c.file.Module] = true
				for writer.add(c) {
				}
			}
		}
		for _, class := range []string{record.ClassPrimary, record.ClassSecondary} {
			fillCoverageClass(&writer, tier, offered, class)
		}
		first = last
	}
}

// fillCoverageClass adds the files of one class, the files the overview did
// not show before the rest of those it showed in part, each with all its
// remaining units.
func fillCoverageClass(writer *coverageSlotWriter, tier []*coverageCursor, offered map[string][]record.Excerpt, class string) {
	for _, shown := range []bool{false, true} {
		for _, c := range tier {
			if c.file.Class != class || (len(offered[c.file.Path]) > 0) != shown {
				continue
			}
			for writer.add(c) {
			}
		}
	}
}

type coverageSlotWriter struct {
	doc   *record.ScopeDoc
	slot  *record.ScopeSlice
	bytes int
	costs map[string]int
	// headers are the files whose header the current slot already carries.
	headers map[string]bool
}

func (w *coverageSlotWriter) add(c *coverageCursor) bool {
	if len(c.windows) == 0 {
		return false
	}
	f := c.windows[0]
	s := w.slot
	cost, ok := w.costs[windowKey(f)]
	if !ok {
		cost = f.PlannedBytes
	}
	header := w.costs[headerKey(f)]
	if s != nil && w.headers[f.Path] {
		header = 0
	}
	cost += header
	l := w.doc.Limits
	newModule := s != nil && !slices.Contains(s.Modules, f.Module)
	if s == nil || s.Class != f.Class || s.PullRequest != f.PullRequest || len(s.Files) >= l.MaxFiles || w.bytes+cost > l.MaxBytes ||
		(newModule && len(s.Modules) >= record.MaxSliceModules) {
		if len(w.doc.Slices) >= w.doc.Limits.Slots {
			return false
		}
		key := sliceKey(len(w.doc.Slices))
		i, v := slotTaskKeys(key)
		w.doc.Slices = append(w.doc.Slices, record.ScopeSlice{Key: key, InvestigateTask: i, VerifyTask: v,
			Class: f.Class, Rank: f.Rank, PullRequest: f.PullRequest})
		w.slot = &w.doc.Slices[len(w.doc.Slices)-1]
		w.bytes, w.headers = 0, map[string]bool{}
		s = w.slot
		cost = cost - header + w.costs[headerKey(f)]
	}
	s.Files = append(s.Files, f)
	if !slices.Contains(s.Modules, f.Module) {
		s.Modules = append(s.Modules, f.Module)
		slices.Sort(s.Modules)
	}
	w.bytes += cost
	if w.costs[headerKey(f)] > 0 {
		w.headers[f.Path] = true
	}
	c.windows = c.windows[1:]
	c.assigned = true
	return true
}
