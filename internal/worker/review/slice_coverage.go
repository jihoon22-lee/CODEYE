package review

import (
	"cmp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review"
)

func hasCoverageSources(j analysis.Job, rule record.SecondaryRule) bool {
	return codeGroupsFor(j.Snapshot.Manifest, j.Scope, rule, changedPaths(j), true).eligible > 0
}

// The number of task keys depends only on the snapshot. The ordinary slot
// estimate covers unseen files; add room for code after the first request
// window or displaced by document evidence. The ceiling is enforced during
// admission because repairs determine the calls actually available.
func coverageSlots(plan slicePlan) int {
	if len(plan.Groups) == 0 {
		return 0
	}
	var additionalWindows int64
	for _, list := range [][]plannedFile{plan.Overview, plan.Rest} {
		for _, f := range list {
			// Each extra line costs at least one source byte. Estimate
			// room even for short lines that hit the line cap before the
			// encoded byte cap.
			additionalWindows += max(0, (f.Size-1)/roleMaxCodeLines)
		}
	}
	return min(MaxSlices, max(2, plan.Slots, (len(plan.Groups)+4)/5)+(int(additionalWindows)+3)/4)
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
	slots := coverageSlots(planSlicesFor(s.j.Snapshot.Manifest, s.j.Scope, rule, changed))
	groups := codeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, rule, changed, true)
	sent, up := s.overviewOffered()
	facts := s.priorityFacts(s.entries())
	doc := record.ScopeDoc{Schema: record.ScopeSchemaVersion, Kind: record.ScopeKind, Pack: record.PackRef{ID: sel.ID, Version: sel.Version}, Snapshot: s.j.Snapshot.Digest,
		RunID: s.j.RunID, AttemptID: s.j.AttemptID, Screen: screenOf(s.screen),
		Budget: record.ScopeBudget{MaxCalls: s.h.maxCalls(), MaxRequestBytes: PromptBudget, WholeRunMaxCalls: record.WholeRunMaxCalls, SliceReserveCalls: record.SliceMinCalls,
			CallsBeforeSlices: used, Admission: record.AdmissionReserveVerification},
		Limits:   record.ScopeLimits{MaxFiles: sliceMaxFiles, MaxBytes: sliceMaxBytes, MaxFileBytes: roleMaxCodeFile, MaxFileLines: roleMaxCodeLines, MaxSlices: MaxSlices, Slots: slots},
		Priority: record.ScopePriority{Order: priorityOrder(s.j), Secondary: rule}, Upstream: []record.Upstream{up},
		Modules: []record.ScopeModule{}, Overview: record.ScopeOverview{Files: []record.ScopeFile{}, Offered: []string{}},
		Slices: []record.ScopeSlice{}, Excluded: []record.ScopeFile{}, Limitations: s.screenLimitation(addLim([]string{}, groups.lims...))}
	discovered := map[string]bool{}
	mods, _ := discovery.DetectModules(s.j.Snapshot.Manifest, envdoc.Scope{Kind: "full", Paths: []string{}})
	for _, m := range mods {
		discovered[m.ID] = true
	}
	var cursors []*coverageCursor
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
				for _, w := range prior {
					if w.Start < 1 || w.End > len(lines) {
						return result(domain.TaskFailed, "review_input_unavailable")
					}
					row.PlannedBytes += record.JSONLen(strings.Join(lines[w.Start-1:w.End], "\n"))
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
			windows := missingCoverageWindows(f, lines, covered, slots, &doc.Omitted)
			prioritizeCoverageWindow(windows, text, e.Path, facts.line[e.Path])
			cursors = append(cursors, &coverageCursor{file: f, tier: groups.tierOf(e.Path), severity: facts.severity[e.Path],
				windows: windows})
		}
	}
	slices.SortFunc(doc.Overview.Files, func(a, b record.ScopeFile) int { return cmp.Compare(a.Path, b.Path) })
	for _, f := range doc.Overview.Files {
		doc.Overview.Offered = append(doc.Overview.Offered, f.Path)
	}
	scheduleCoverage(&doc, cursors, sent)
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
	for i := 0; i < len(lines); {
		if covered[i] {
			i++
			continue
		}
		start, size := i, 0
		for i < len(lines) && !covered[i] && i-start < roleMaxCodeLines {
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

// Give each unrepresented module a useful source window before filling in
// the other files. Then complete remaining windows round by round. Never
// mix path classes or pull-request tiers in one request.
func scheduleCoverage(doc *record.ScopeDoc, cursors []*coverageCursor, offered map[string][]record.Excerpt) {
	slices.SortFunc(cursors, func(a, b *coverageCursor) int {
		return cmp.Or(cmp.Compare(a.tier, b.tier), cmp.Compare(a.file.Class, b.file.Class),
			cmp.Compare(record.RankIndex(a.file.Rank), record.RankIndex(b.file.Rank)), cmp.Compare(b.severity, a.severity),
			cmp.Compare(b.file.Observations, a.file.Observations), cmp.Compare(a.file.Path, b.file.Path))
	})
	represented := map[string]bool{}
	for _, f := range doc.Overview.Files {
		represented[f.Module] = true
	}
	writer := coverageSlotWriter{doc: doc}
	for first := 0; first < len(cursors); {
		last := first + 1
		for last < len(cursors) && cursors[last].tier == cursors[first].tier {
			last++
		}
		tier := cursors[first:last]
		for _, c := range tier {
			if !represented[c.file.Module] && writer.add(c) {
				represented[c.file.Module] = true
			}
		}
		for _, class := range []string{record.ClassPrimary, record.ClassSecondary} {
			fillCoverageClass(&writer, tier, offered, class)
		}
		first = last
	}
}

func fillCoverageClass(writer *coverageSlotWriter, tier []*coverageCursor, offered map[string][]record.Excerpt, class string) {
	for _, c := range tier {
		if c.file.Class == class && !c.assigned && len(offered[c.file.Path]) == 0 {
			writer.add(c)
		}
	}
	for {
		added := false
		for _, c := range tier {
			if c.file.Class == class && writer.add(c) {
				added = true
			}
		}
		if !added {
			return
		}
	}
}

type coverageSlotWriter struct {
	doc   *record.ScopeDoc
	slot  *record.ScopeSlice
	bytes int
}

func (w *coverageSlotWriter) add(c *coverageCursor) bool {
	if len(c.windows) == 0 {
		return false
	}
	f := c.windows[0]
	s := w.slot
	if s == nil || s.Class != f.Class || s.PullRequest != f.PullRequest || len(s.Files) >= sliceMaxFiles || w.bytes+f.PlannedBytes > sliceMaxBytes {
		if len(w.doc.Slices) >= w.doc.Limits.Slots {
			return false
		}
		key := sliceKey(len(w.doc.Slices))
		i, v := slotTaskKeys(key)
		w.doc.Slices = append(w.doc.Slices, record.ScopeSlice{Key: key, InvestigateTask: i, VerifyTask: v,
			Class: f.Class, Rank: f.Rank, PullRequest: f.PullRequest})
		w.slot = &w.doc.Slices[len(w.doc.Slices)-1]
		w.bytes = 0
		s = w.slot
	}
	s.Files = append(s.Files, f)
	if !slices.Contains(s.Modules, f.Module) {
		s.Modules = append(s.Modules, f.Module)
		slices.Sort(s.Modules)
	}
	w.bytes += f.PlannedBytes
	c.windows = c.windows[1:]
	c.assigned = true
	return true
}
