package report

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Review slices (docs/REPORT.md#모듈별-검토-장부): the slice records (review
// record 0.3), the review scope artifact of review.scope and the per-module
// ledger the code review section carries from builder 6.

// LedgerSince is the first builder version that reads review slices: their
// records, findings and calls, the scope artifact and the ledger.
const LedgerSince = 6

// Ledger statuses.
const (
	// LedgerComplete: every eligible code and test file of the scope was sent
	// to the model and its review reached an accepted verify record.
	LedgerComplete = "complete"
	// LedgerOverviewOnly: no slice investigate task succeeded; only the
	// whole-run review sent files.
	LedgerOverviewOnly = "overview_only"
	// LedgerPartial: anything in between.
	LedgerPartial = "partial"
)

// SliceReviewDecoder validates a slice record (schema 0.3) attached to its
// own slice task.
type SliceReviewDecoder struct{}

func (SliceReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionSlice}
}

func (SliceReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	stage, key, ok := record.SliceTask(ctx.Task.Key)
	if !ok || art.Role != "other" {
		return Contribution{}, invalidf("review slice record attached to unexpected task or role")
	}
	r, err := record.DecodeRole(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Slice: key, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), Role: &r}, Limitations: r.Limitations}, nil
}

// ScopeDecoder validates the review scope artifact of review.scope against
// the attempt and the snapshot manifest.
type ScopeDecoder struct{}

func (ScopeDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.ScopeMediaType, SchemaVersion: record.ScopeSchemaVersion}
}

func (ScopeDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if ctx.Task.Key != record.TaskScope || art.Role != "other" {
		return Contribution{}, invalidf("review scope artifact attached to unexpected task or role")
	}
	d, err := record.DecodeScope(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest, RunID: ctx.RunID, AttemptID: ctx.AttemptID, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Scope: &d, Limitations: d.Limitations}, nil
}

// scopeAccepted reports a review.scope task whose artifact the report kept.
func scopeAccepted(st *taskState) bool {
	return st != nil && !st.rejected && st.facts.Status == string(domain.TaskSucceeded) && st.scope != nil
}

// isSliceTask reports a review slice task key (review.investigate.sNNN,
// review.verify.sNNN).
func isSliceTask(key string) bool {
	_, _, ok := record.SliceTask(key)
	return ok
}

// sliceKeys are the slice keys of the planned slice tasks, in plan order.
func sliceKeys(states map[string]*taskState) []string {
	seen := map[string]bool{}
	var out []string
	for k := range states {
		if _, key, ok := record.SliceTask(k); ok && !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// roleRecordKeys are the review tasks that may hold a role record, in the
// order their lineage is checked: the whole-run roles, then each slice's
// investigate before its verify.
func roleRecordKeys(states map[string]*taskState) []string {
	var out []string
	for _, stage := range record.RoleStages {
		out = append(out, "review."+stage)
	}
	for _, key := range sliceKeys(states) {
		out = append(out, "review."+record.StageInvestigate+"."+key, "review."+record.StageVerify+"."+key)
	}
	return out
}

// noCallStatus is a review task status that made no model call.
func noCallStatus(s string) bool {
	return s == string(domain.TaskSkippedPolicy) || s == string(domain.TaskUnsupported) || s == string(domain.TaskBudgetExhausted)
}

// sentPaths are the snapshot paths the accepted role records sent to the
// model: the evidence of every record with a call that sent a request.
func sentPaths(states map[string]*taskState, keys []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		st := states[k]
		if !roleAccepted(st) {
			continue
		}
		rec := st.reviewResult.Role
		if slices.ContainsFunc(rec.Calls, func(c record.RoleCall) bool { return c.RequestDigest != "" }) {
			for _, e := range rec.Evidence {
				out[e.Path] = true
			}
		}
	}
	return out
}

// ledger builds the code review ledger: present when review.scope was
// planned (the plan had files beyond the overview), with one row per module
// of the accepted scope artifact.
func (c sectionContext) ledger() *ReviewLedger {
	scopeState := c.states[record.TaskScope]
	if scopeState == nil {
		return nil
	}
	// The scope task holds an artifact, not a review record: it is accepted
	// when the report kept that artifact.
	scopeItem, _ := reviewItem(record.TaskScope, scopeState, true)
	if scopeAccepted(scopeState) {
		scopeItem.Result = "accepted"
	}
	l := &ReviewLedger{Scope: scopeItem, Modules: []LedgerModule{}}
	keys := roleRecordKeys(c.states)
	sent := sentPaths(c.states, keys)

	// Calls: those before the slices as the scope artifact counted them (the
	// attempt's own counter, failed whole-run calls included), else the
	// whole-run records; then every slice task's record. A task that may have
	// called without a usable record leaves the count unknown.
	calls, callsKnown := 0, true
	bytes, bytesKnown := 0, true
	doc := scopeState.scope
	if !scopeAccepted(scopeState) {
		doc = nil
	}
	for _, k := range keys {
		st := c.states[k]
		if st == nil {
			continue
		}
		whole := !isSliceTask(k)
		switch {
		case roleAccepted(st):
			for _, call := range st.reviewResult.Role.Calls {
				bytes += call.RequestBytes
				if !whole || doc == nil {
					calls++
				}
			}
		case noCallStatus(st.facts.Status):
			// A record of a call that was not made (model_not_configured)
			// still states its request size; the section sums every record.
			if !st.rejected && st.reviewResult != nil && st.reviewResult.Role != nil {
				for _, call := range st.reviewResult.Role.Calls {
					bytes += call.RequestBytes
				}
			}
		default:
			bytesKnown = false
			if !whole || doc == nil {
				callsKnown = false
			}
		}
	}
	if doc != nil {
		calls += doc.Budget.CallsBeforeSlices
		mc, mb := doc.Budget.MaxCalls, doc.Budget.MaxRequestBytes
		l.MaxCalls, l.MaxRequestBytes = &mc, &mb
	}
	if callsKnown {
		l.CallsUsed = &calls
	}
	if bytesKnown {
		l.RequestBytesSent = &bytes
	}

	if doc != nil {
		rows := map[string]*LedgerModule{}
		for _, m := range doc.Modules {
			rows[m.ID] = &LedgerModule{ModuleID: m.ID, Discovered: m.Discovered, FilesEligible: m.FilesEligible, FilesNotPlanned: m.FilesNotPlanned, Slices: []LedgerSlice{}}
		}
		count := func(f record.ScopeFile, slice string) {
			row := rows[f.Module]
			verify := "review.verify"
			if slice != "" && f.State != record.FileCoveredByOverview {
				verify = "review.verify." + slice
			}
			switch {
			case sent[f.Path]:
				row.FilesSentToModel++
				row.BytesSent += f.PlannedBytes
				if f.PlannedLines < f.TotalLines {
					row.FilesTruncated++
				}
				if roleAccepted(c.states[verify]) {
					row.FilesWithCompletedReview++
				}
			case f.State == record.FileWithheld:
				row.FilesWithheld++
			case f.State == record.FileUnreadable:
				row.FilesUnreadable++
			case slice != "" && f.State == record.FilePlanned && c.states["review.investigate."+slice] != nil &&
				c.states["review.investigate."+slice].facts.Status == string(domain.TaskBudgetExhausted):
				row.FilesBudgetExhausted++
			default:
				row.FilesNotSent++
			}
		}
		for _, f := range doc.Overview.Files {
			count(f, "")
		}
		for _, s := range doc.Slices {
			for _, f := range s.Files {
				count(f, s.Key)
			}
			for _, m := range s.Modules {
				for _, k := range []string{s.InvestigateTask, s.VerifyTask} {
					if st := c.states[k]; st != nil {
						rows[m].Slices = append(rows[m].Slices, LedgerSlice{TaskKey: k, Status: st.facts.Status, Reason: st.facts.Reason})
					}
				}
			}
		}
		for _, m := range doc.Modules {
			l.Modules = append(l.Modules, *rows[m.ID])
		}
		sort.Slice(l.Modules, func(a, b int) bool { return l.Modules[a].ModuleID < l.Modules[b].ModuleID })
	}
	l.Status = ledgerStatus(*l, c.r.Tasks)
	return l
}

// ledgerStatus derives the ledger status from its rows and the task rows.
func ledgerStatus(l ReviewLedger, tasks []Task) string {
	complete := len(l.Modules) > 0
	for _, m := range l.Modules {
		complete = complete && m.FilesWithCompletedReview == m.FilesEligible
	}
	if complete {
		return LedgerComplete
	}
	for _, t := range tasks {
		if stage, _, ok := record.SliceTask(t.Key); ok && stage == record.StageInvestigate && t.Status == string(domain.TaskSucceeded) {
			return LedgerPartial
		}
	}
	return LedgerOverviewOnly
}

// ledgerTotals sums the module rows: files sent to the model and files of
// the scope that were not.
func ledgerTotals(l *ReviewLedger) (sent, notSent int) {
	for _, m := range l.Modules {
		sent += m.FilesSentToModel
		notSent += m.FilesEligible - m.FilesSentToModel
	}
	return sent, notSent
}

// ledgerGaps are the gap entries of the scope the review did not send or
// did not finish: per module and kind, and the scope task itself.
func ledgerGaps(l *ReviewLedger, add func(section, code string, subject, detail *string)) {
	if l.Scope.Result != "accepted" {
		add(SectionCodeReview, "review_scope_"+l.Scope.Result, strPtr(l.Scope.Key), l.Scope.Reason)
	}
	for _, m := range l.Modules {
		for _, g := range []struct {
			code string
			n    int
		}{{"review_scope_budget_exhausted", m.FilesBudgetExhausted}, {"review_scope_not_planned", m.FilesNotPlanned},
			{"review_scope_withheld", m.FilesWithheld}, {"review_scope_unreadable", m.FilesUnreadable}, {"review_scope_not_sent", m.FilesNotSent}} {
			if g.n > 0 {
				add(SectionCodeReview, g.code, strPtr(m.ModuleID), nil)
			}
		}
	}
}

var (
	ledgerStatuses = []string{LedgerComplete, LedgerPartial, LedgerOverviewOnly}
	sliceFindingID = regexp.MustCompile(`^review_(s[0-9]{3})_`)
)

// validateLedger checks the ledger against the builder version and the
// report: present exactly when review.scope is a task of a builder-6 report;
// the scope item and every slice entry repeat their task rows; each module
// row's files add up to its eligible files; the status and the call count
// follow from the rows, the task rows and the review calls.
func validateLedger(r Report) error {
	tasks := map[string]Task{}
	for _, t := range r.Tasks {
		tasks[t.Key] = t
	}
	n, _ := strconv.Atoi(r.Builder.Version)
	_, planned := tasks[record.TaskScope]
	for _, c := range r.Coverage.ReviewCalls {
		if c.TaskKey == "" {
			continue
		}
		stage, _, slice := record.SliceTask(c.TaskKey)
		if !slice {
			stage = strings.TrimPrefix(c.TaskKey, "review.")
		}
		if _, ok := tasks[c.TaskKey]; n < LedgerSince || !ok || stage != c.Role {
			return invalidf("review call names task %s it does not belong to", c.TaskKey)
		}
	}
	// A slice finding (review_sNNN_<candidate>) belongs to a planned slice of
	// a builder-6 report.
	for _, f := range r.Findings {
		if m := sliceFindingID.FindStringSubmatch(f.ID); m != nil {
			if _, ok := tasks["review.investigate."+m[1]]; n < LedgerSince || !ok {
				return invalidf("finding %s of a slice that was not planned", f.ID)
			}
		}
	}
	var l *ReviewLedger
	if r.CodeReview != nil {
		l = r.CodeReview.Ledger
	}
	switch {
	case l == nil && (!planned || n < LedgerSince || r.CodeReview == nil):
		return nil
	case l == nil:
		return invalidf("builder %s report with review.scope but no ledger", r.Builder.Version)
	case n < LedgerSince || !planned:
		return invalidf("review ledger without review.scope or in a builder %s revision", r.Builder.Version)
	}
	sameTask := func(key, status string, reason *string) bool {
		t, ok := tasks[key]
		return ok && t.Status == status && deref(t.Reason) == deref(reason) && (t.Reason == nil) == (reason == nil)
	}
	if l.Scope.Key != record.TaskScope || l.Scope.TaskStatus == nil || !sameTask(record.TaskScope, *l.Scope.TaskStatus, l.Scope.Reason) ||
		!slices.Contains([]string{"accepted", "skipped", "not_succeeded", "result_rejected", "result_missing"}, l.Scope.Result) {
		return invalidf("ledger scope differs from the review.scope task")
	}
	if !slices.Contains(ledgerStatuses, l.Status) || l.Modules == nil {
		return invalidf("ledger status is invalid")
	}
	if (l.MaxCalls == nil) != (l.MaxRequestBytes == nil) || (l.MaxCalls != nil && (!record.ValidMaxCalls(*l.MaxCalls) || *l.MaxRequestBytes <= 0)) ||
		(l.MaxCalls == nil && len(l.Modules) > 0) || (l.Scope.Result == "accepted") != (l.MaxCalls != nil) {
		return invalidf("ledger budget is invalid")
	}
	// A listed call without a request (model_not_configured and the other
	// outcomes of a call that was never sent) is not one the ledger counts.
	sent := 0
	for _, c := range r.Coverage.ReviewCalls {
		if c.RequestDigest != "" {
			sent++
		}
	}
	if l.CallsUsed != nil && *l.CallsUsed < sent {
		return invalidf("ledger counts fewer calls than the report lists")
	}
	if l.RequestBytesSent != nil && (r.CodeReview.RequestBytes == nil || *l.RequestBytesSent != *r.CodeReview.RequestBytes) {
		return invalidf("ledger request bytes differ from the code review section")
	}
	prev := ""
	for _, m := range l.Modules {
		if !record.ValidModuleRef(m.ModuleID) || m.ModuleID <= prev || m.Slices == nil {
			return invalidf("ledger module %s is invalid or out of order", m.ModuleID)
		}
		prev = m.ModuleID
		counts := []int{m.FilesSentToModel, m.BytesSent, m.FilesTruncated, m.FilesWithheld, m.FilesUnreadable, m.FilesBudgetExhausted, m.FilesNotPlanned, m.FilesNotSent, m.FilesWithCompletedReview}
		if slices.Min(counts) < 0 || m.FilesEligible < 1 ||
			m.FilesSentToModel+m.FilesWithheld+m.FilesUnreadable+m.FilesBudgetExhausted+m.FilesNotPlanned+m.FilesNotSent != m.FilesEligible ||
			m.FilesTruncated > m.FilesSentToModel || m.FilesWithCompletedReview > m.FilesSentToModel || (m.FilesSentToModel == 0 && m.BytesSent != 0) {
			return invalidf("ledger module %s files do not add up", m.ModuleID)
		}
		exhausted := false
		for _, s := range m.Slices {
			if !isSliceTask(s.TaskKey) || !sameTask(s.TaskKey, s.Status, s.Reason) {
				return invalidf("ledger slice %s differs from its task", s.TaskKey)
			}
			exhausted = exhausted || s.Status == string(domain.TaskBudgetExhausted)
		}
		if m.FilesBudgetExhausted > 0 && !exhausted {
			return invalidf("ledger module %s counts budget-exhausted files without such a slice", m.ModuleID)
		}
	}
	if ledgerStatus(*l, r.Tasks) != l.Status {
		return invalidf("ledger status differs from its rows")
	}
	return nil
}
