package report

import (
	"cmp"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	LineCoverageSince     = 16
	lineBudgetExhausted   = "review_budget_exhausted"
	lineInputTrimmed      = "review_input_trimmed"
	lineReviewUnaccepted  = "requested_without_accepted_response"
	lineReviewUnconfirmed = "request_unconfirmed"
	lineNotRequested      = "not_requested"
	lineAccepted          = "accepted"
)

type lineCoverageRange struct {
	start, end     int
	sent, reviewed bool
	reason         string
}

type lineCoverageFile struct {
	info                record.ScopeFile
	ranges              []lineCoverageRange
	bytesSent           int
	noRequestReason     string
	candidateFollowedUp bool
}

func (c sectionContext) lineCoverage(doc *record.ScopeDoc, l *ReviewLedger) {
	rows := map[string]*LedgerModule{}
	for _, m := range doc.Modules {
		rows[m.ID] = &LedgerModule{ModuleID: m.ID, Discovered: m.Discovered, Class: m.Class, FilesEligible: m.FilesEligible,
			FilesSecondary: m.FilesSecondary, FilesRequested: new(int), FilesAccepted: new(int), Slices: []LedgerSlice{}}
	}
	files := map[string]*lineCoverageFile{}
	ensureFile := func(f record.ScopeFile) *lineCoverageFile {
		if files[f.Path] == nil {
			files[f.Path] = &lineCoverageFile{info: f, noRequestReason: lineNotRequested}
		}
		return files[f.Path]
	}
	for _, list := range [][]record.ScopeFile{doc.Overview.Files, doc.Excluded, doc.Unplanned} {
		for _, f := range list {
			ensureFile(f)
		}
	}
	for _, sl := range doc.Slices {
		investigate, accepted, why := investigationRequest(c.states[sl.InvestigateTask])
		verified := verifiedCandidatePaths(sl.VerifyTask, c.states[sl.VerifyTask])
		st := c.states[sl.InvestigateTask]
		if st != nil && st.facts.Status == string(domain.TaskBudgetExhausted) && investigate == nil {
			why = lineBudgetExhausted
			if sl.Class == record.ClassPrimary {
				l.PrimarySlotsBudgetExhausted++
			}
		} else if accepted {
			why = lineInputTrimmed
		}
		for _, w := range sl.Files {
			f := ensureFile(w)
			end := w.StartLine + w.PlannedLines - 1
			active := investigate != nil && slices.ContainsFunc(investigate.Evidence, func(e record.Excerpt) bool {
				return contextEvidence(e) && e.Path == w.Path && e.SHA256 == w.SHA256 && e.Start == w.StartLine && e.End == end
			})
			interval := lineCoverageRange{start: w.StartLine, end: end, sent: active, reviewed: active && accepted, reason: why}
			if active {
				f.bytesSent += w.PlannedBytes
				interval.reason = lineReviewUnaccepted
				f.candidateFollowedUp = f.candidateFollowedUp || (accepted && verified[w.Path])
			}
			f.ranges = append(f.ranges, interval)
		}
		for _, m := range sl.Modules {
			for _, key := range []string{sl.InvestigateTask, sl.VerifyTask} {
				if s := c.states[key]; s != nil {
					rows[m].Slices = append(rows[m].Slices, LedgerSlice{TaskKey: key, Status: s.facts.Status, Reason: s.facts.Reason})
				}
			}
		}
	}
	for _, o := range doc.Omitted {
		f := files[o.Path]
		f.ranges = append(f.ranges, lineCoverageRange{start: o.StartLine, end: o.EndLine, reason: o.Reason})
	}
	overview, accepted, overviewReason := investigationRequest(c.states["review.investigate"])
	if overview != nil {
		verified := verifiedCandidatePaths("review.verify", c.states["review.verify"])
		charged := map[string]bool{}
		for _, e := range overview.Evidence {
			if f := files[e.Path]; f != nil && contextEvidence(e) && e.SHA256 == f.info.SHA256 && e.End <= f.info.TotalLines {
				f.ranges = append(f.ranges, lineCoverageRange{start: e.Start, end: e.End, sent: true, reviewed: accepted,
					reason: lineReviewUnaccepted})
				f.candidateFollowedUp = f.candidateFollowedUp || (accepted && verified[e.Path])
				if f.info.State == record.FileCoveredByOverview && !charged[e.Path] {
					f.bytesSent += f.info.PlannedBytes
					charged[e.Path] = true
				}
			}
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		f := files[path]
		if overviewReason == lineReviewUnconfirmed {
			f.noRequestReason = lineReviewUnconfirmed
		}
		item := f.ledgerFile()
		l.Files = append(l.Files, item)
		row := rows[item.ModuleID]
		row.BytesSent += f.bytesSent
		secondary := f.info.Class == record.ClassSecondary
		switch {
		case item.SentLines > 0:
			(*row.FilesRequested)++
			row.FilesSentToModel++
			if item.SentLines < item.TotalLines {
				row.FilesTruncated++
			}
			if item.ReviewedLines > 0 {
				(*row.FilesAccepted)++
			}
			if f.candidateFollowedUp {
				row.FilesWithCompletedReview++
			}
			if secondary {
				row.FilesSecondarySent++
			}
		case f.info.State == record.FileWithheld:
			row.FilesWithheld++
		case f.info.State == record.FileUnreadable:
			row.FilesUnreadable++
		case secondary && (f.info.State == record.FileNotPlanned || item.hasReason(lineBudgetExhausted)):
			row.FilesSecondaryNotReached++
		case item.hasReason(lineBudgetExhausted):
			row.FilesBudgetExhausted++
		case f.info.State == record.FileNotPlanned:
			row.FilesNotPlanned++
		default:
			row.FilesNotSent++
		}
	}
	for _, row := range rows {
		l.Modules = append(l.Modules, *row)
	}
	slices.SortFunc(l.Modules, func(a, b LedgerModule) int { return cmp.Compare(a.ModuleID, b.ModuleID) })
}

func contextEvidence(e record.Excerpt) bool {
	return e.Origin == "context" && (e.Kind == "code" || e.Kind == "test")
}

func sliceInvestigationInScope(rec record.Record, doc *record.ScopeDoc) bool {
	i := slices.IndexFunc(doc.Slices, func(s record.ScopeSlice) bool { return s.Key == rec.Slice.Key })
	if i < 0 || !slices.Equal(doc.Slices[i].Modules, rec.Slice.Modules) {
		return false
	}
	for _, e := range rec.Evidence {
		if contextEvidence(e) && !slices.ContainsFunc(doc.Slices[i].Files, func(f record.ScopeFile) bool {
			return f.Path == e.Path && f.SHA256 == e.SHA256 && f.StartLine == e.Start && f.StartLine+f.PlannedLines-1 == e.End
		}) {
			return false
		}
	}
	return true
}

func coverageOverviewInScope(doc *record.ScopeDoc, st *taskState) bool {
	up := doc.Upstream[0]
	if up.State != record.UpstreamAccepted {
		return len(doc.Overview.Files) == 0 && !reviewAccepted(st)
	}
	if !reviewAccepted(st) || st.decodedSHA[record.MediaType] != up.SHA256 {
		return false
	}
	r, accepted, _ := investigationRequest(st)
	if !accepted {
		return len(doc.Overview.Files) == 0
	}
	files := map[string]record.ScopeFile{}
	covered := map[string]map[int]bool{}
	for _, f := range doc.Overview.Files {
		files[f.Path] = f
		covered[f.Path] = map[int]bool{}
	}
	for _, e := range r.Evidence {
		if !contextEvidence(e) {
			continue
		}
		f, ok := files[e.Path]
		if !ok || e.SHA256 != f.SHA256 || e.Start <= 0 || e.End > f.TotalLines {
			return false
		}
		for n := e.Start; n <= e.End; n++ {
			covered[e.Path][n] = true
		}
	}
	for _, f := range doc.Overview.Files {
		if len(covered[f.Path]) != f.PlannedLines {
			return false
		}
	}
	overlaps := func(path string, start, end int) bool {
		for n := range covered[path] {
			if start <= n && n <= end {
				return true
			}
		}
		return false
	}
	for _, sl := range doc.Slices {
		for _, f := range sl.Files {
			if overlaps(f.Path, f.StartLine, f.StartLine+f.PlannedLines-1) {
				return false
			}
		}
	}
	for _, o := range doc.Omitted {
		if overlaps(o.Path, o.StartLine, o.EndLine) {
			return false
		}
	}
	return true
}

func investigationRequest(st *taskState) (*record.Record, bool, string) {
	if st == nil {
		return nil, false, lineNotRequested
	}
	if st.rejected || st.reviewResult == nil || st.reviewResult.Record == nil {
		if noCallStatus(st.facts.Status) {
			return nil, false, lineNotRequested
		}
		return nil, false, lineReviewUnconfirmed
	}
	r := st.reviewResult.Record
	if !slices.ContainsFunc(r.Calls, func(c record.Call) bool { return c.RequestDigest != "" }) {
		return nil, false, lineNotRequested
	}
	accepted := reviewAccepted(st) && slices.ContainsFunc(r.Calls, func(c record.Call) bool {
		return c.RequestDigest != "" && c.Outcome == record.OutcomeAccepted
	})
	return r, accepted, lineNotRequested
}

// The verify task can succeed with no candidates and no model call. Only a
// recorded model disposition on a candidate identifies paths followed up;
// an incomplete verify task's accepted verdicts count (verifyIncomplete).
func verifiedCandidatePaths(key string, st *taskState) map[string]bool {
	if !verdictsUsable(key, st) {
		return nil
	}
	r := st.reviewResult.Record
	if !slices.ContainsFunc(r.Calls, func(c record.Call) bool {
		return c.RequestDigest != "" && c.Outcome == record.OutcomeAccepted
	}) {
		return nil
	}
	ids := map[string]bool{}
	for _, v := range r.Verifications {
		if v.Verdict != record.VerdictUnreviewed {
			for _, id := range append(slices.Clone(v.SupportingEvidence), v.Counterevidence...) {
				ids[id] = true
			}
		}
	}
	paths := map[string]bool{}
	for _, e := range r.Evidence {
		if ids[e.ID] && (e.Kind == "code" || e.Kind == "test") {
			paths[e.Path] = true
		}
	}
	return paths
}

func (f lineCoverageFile) ledgerFile() LedgerFile {
	item := LedgerFile{Path: f.info.Path, ModuleID: f.info.Module, TotalLines: f.info.TotalLines,
		Investigation: lineNotRequested, Remaining: []LedgerRange{}}
	if f.info.State == record.FileWithheld || f.info.State == record.FileUnreadable {
		item.Reason = f.info.Reason
		return item
	}
	// Sweep only boundaries of assigned, disclosed and omitted ranges. This
	// unions overlapping overview excerpts without counting a line twice.
	type boundary struct {
		line       int
		rangeIndex int
		start      bool
	}
	points := []boundary{{line: 1, rangeIndex: -1}, {line: item.TotalLines + 1, rangeIndex: -1}}
	for i, r := range f.ranges {
		points = append(points, boundary{line: r.start, rangeIndex: i, start: true}, boundary{line: r.end + 1, rangeIndex: i})
	}
	slices.SortFunc(points, func(a, b boundary) int { return cmp.Compare(a.line, b.line) })
	active := map[int]bool{}
	prev := 1
	for _, p := range points {
		if p.line > prev {
			sent, reviewed, why := false, false, f.noRequestReason
			for idx := range active {
				r := f.ranges[idx]
				sent, reviewed = sent || r.sent, reviewed || r.reviewed
				// The planned and omitted ranges cannot overlap each other.
				// A request can overlap those ranges when the overview failed.
				if !r.sent && r.reason != lineNotRequested {
					why = r.reason
				}
			}
			if sent {
				item.SentLines += p.line - prev
				why = lineReviewUnaccepted
			}
			if reviewed {
				item.ReviewedLines += p.line - prev
			} else if n := len(item.Remaining); n > 0 && item.Remaining[n-1].EndLine+1 == prev && item.Remaining[n-1].Reason == why {
				item.Remaining[n-1].EndLine = p.line - 1
			} else {
				item.Remaining = append(item.Remaining, LedgerRange{StartLine: prev, EndLine: p.line - 1, Reason: why})
			}
		}
		if p.rangeIndex >= 0 {
			if p.start {
				active[p.rangeIndex] = true
			} else {
				delete(active, p.rangeIndex)
			}
		}
		prev = p.line
	}
	switch {
	case item.ReviewedLines > 0:
		item.Investigation = lineAccepted
	case item.SentLines > 0:
		item.Investigation = lineReviewUnaccepted
	case item.hasReason(lineReviewUnconfirmed):
		item.Investigation = lineReviewUnconfirmed
	}
	return item
}

func (f LedgerFile) hasReason(reason string) bool {
	return slices.ContainsFunc(f.Remaining, func(r LedgerRange) bool { return r.Reason == reason })
}

func validateLedgerLines(l *ReviewLedger) error {
	if l.Files == nil {
		for _, row := range l.Modules {
			if row.FilesRequested != nil || row.FilesAccepted != nil {
				return invalidf("ledger request counts lack file coverage")
			}
		}
		return nil
	}
	rows := map[string]*LedgerModule{}
	for _, row := range l.Modules {
		rows[row.ModuleID] = &LedgerModule{FilesAccepted: new(int)}
	}
	prev := ""
	for _, f := range l.Files {
		row := rows[f.ModuleID]
		if row == nil || domain.ValidateEvidencePath(f.Path) != nil || record.SourceKind(f.Path) == "" || f.Path <= prev || f.Remaining == nil ||
			f.ReviewedLines < 0 || f.ReviewedLines > f.SentLines || f.SentLines > f.TotalLines || f.TotalLines > record.MaxSourceBytes ||
			(f.TotalLines == 0) != (f.Reason != "") ||
			(f.Reason != "" && !slices.Contains([]string{record.ScopeReasonWithheld, record.ScopeReasonUnreadable, record.ScopeReasonEmpty, record.ScopeReasonTooLarge}, f.Reason)) {
			return invalidf("review ledger file %s", f.Path)
		}
		prev = f.Path
		row.FilesEligible++
		if f.SentLines > 0 {
			row.FilesSentToModel++
			if f.SentLines < f.TotalLines {
				row.FilesTruncated++
			}
		}
		if f.ReviewedLines > 0 {
			(*row.FilesAccepted)++
		}
		end, missing, unverified := 0, 0, 0
		for _, r := range f.Remaining {
			if r.StartLine <= end || r.StartLine > r.EndLine || r.EndLine > f.TotalLines ||
				!slices.Contains([]string{lineReviewUnaccepted, lineReviewUnconfirmed, lineNotRequested, lineInputTrimmed, lineBudgetExhausted,
					record.ScopeReasonLineLimit, record.ScopeReasonSlotLimit, record.ScopeReasonBlankRange}, r.Reason) {
				return invalidf("review ledger range %s", f.Path)
			}
			end = r.EndLine
			missing += r.EndLine - r.StartLine + 1
			if r.Reason == lineReviewUnaccepted {
				unverified += r.EndLine - r.StartLine + 1
			}
		}
		if missing != f.TotalLines-f.ReviewedLines || unverified != f.SentLines-f.ReviewedLines {
			return invalidf("review ledger file %s lines do not add up", f.Path)
		}
		status := lineNotRequested
		switch {
		case f.ReviewedLines > 0:
			status = lineAccepted
		case f.SentLines > 0:
			status = lineReviewUnaccepted
		case f.hasReason(lineReviewUnconfirmed):
			status = lineReviewUnconfirmed
		}
		if f.Investigation != status {
			return invalidf("review ledger file %s request status differs from input", f.Path)
		}
	}
	for _, row := range l.Modules {
		got := rows[row.ModuleID]
		if got.FilesEligible != row.FilesEligible || got.FilesSentToModel != row.FilesSentToModel || got.FilesTruncated != row.FilesTruncated ||
			row.FilesRequested == nil || *row.FilesRequested != got.FilesSentToModel ||
			row.FilesAccepted == nil || *row.FilesAccepted != *got.FilesAccepted || row.FilesWithCompletedReview > *row.FilesAccepted {
			return invalidf("review ledger line counts do not match module %s", row.ModuleID)
		}
	}
	return nil
}
