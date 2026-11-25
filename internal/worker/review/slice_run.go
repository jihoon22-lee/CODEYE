package review

import (
	"bytes"
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Running the review slices (docs/REVIEW.md#모듈-단위-검토-분할).
//
// The call budget is attempt-local: one attempt is one worker process, and
// every review model call of the attempt appends one line to a counter file
// in the attempt scratch before the call is made (callsBelow). A slice
// investigate is admitted, in plan order, when a call for investigation and
// one for candidate verification remain. An investigation retry preserves
// the second call. The decision is written next to the counter and the
// slice's verify follows it. The whole-run roles run before every slice and
// share the same ceiling. Pairs may run concurrently (schedule.go); each
// decision still counts only the calls before the pair in plan order.
const (
	reasonBudgetExhausted = "review_budget_exhausted"
	reasonCallCountFailed = "review_call_count_failed"
	// reasonCallCapExceeded: a task asked for a call past its own bound
	// (record.TaskMaxCalls); the call was not made.
	reasonCallCapExceeded = "review_call_cap_exceeded"

	decisionAdmitted  = "admitted"
	decisionExhausted = "budget_exhausted"
)

// DefaultMaxCalls is the model call ceiling per attempt when the node
// operator sets none (codeye agent --review-max-calls).
const DefaultMaxCalls = record.DefaultMaxCalls

// ValidMaxCalls reports whether n is an accepted call ceiling: at least the
// whole-run roles' worst case, at most what every slice could use.
func ValidMaxCalls(n int) bool { return record.ValidMaxCalls(n) }

func callsPath(j analysis.Job) string { return filepath.Join(j.Scratch, "review.calls") }
func scopePath(j analysis.Job) string { return filepath.Join(j.Scratch, "review.scope.json") }
func admissionPath(j analysis.Job, key string) string {
	return filepath.Join(j.Scratch, "review.admission."+key)
}

// callsUsed counts the model calls this attempt has made. An unreadable
// counter admits nothing more.
func callsUsed(j analysis.Job) (int, error) {
	b, err := readBounded(callsPath(j), 64<<10)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return bytes.Count(b, []byte("\n")), nil
}

// wholeRunCallsUsed counts the calls of the whole-run roles: every counted
// call except those of the diagnostic triage slots, whose records the
// report's ledger counts like a slice's. It is the scope artifact's
// calls_before_slices, which the whole-run call bound still limits.
func wholeRunCallsUsed(j analysis.Job) (int, error) {
	b, err := readBounded(callsPath(j), 64<<10)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for line := range strings.SplitSeq(string(b), "\n") {
		if _, _, triage := record.TriageTask(line); line != "" && !triage {
			n++
		}
	}
	return n, nil
}

// priorityInputs are what the analyzers say about the files: per path the
// non-metric diagnostics located in it and their highest severity rank, and
// the modules (<ecosystem>:<dir>) of required analyzer tasks that did not
// succeed.
type priorityInputs struct {
	observations map[string]int
	severity     map[string]int
	line         map[string]int
	failed       map[string]bool
}

// priorityFacts reads every planned analyzer task's observations artifact
// (the same validated read as the interpret role; an unreadable artifact
// adds nothing). Suppressed diagnostics and metrics never count.
func (s *roleStage) priorityFacts(files map[string]source.Entry) priorityInputs {
	in := priorityInputs{observations: map[string]int{}, severity: map[string]int{}, line: map[string]int{}, failed: map[string]bool{}}
	plan, err := s.h.Static.Plan(s.ctx, s.j)
	if err != nil {
		return in
	}
	for _, t := range plan {
		if !record.AnalyzerTaskKey(t.Key) {
			continue
		}
		sum, _, reason := s.readToolDoc(t.Key, files)
		if reason != "" {
			continue
		}
		if t.Required && sum.TaskStatus != string(domain.TaskSucceeded) && sum.Ecosystem != "" && sum.Ecosystem != "any" {
			in.failed[sum.Ecosystem+":"+sum.Module] = true
		}
		for _, o := range sum.Observations {
			if o.Location == nil {
				continue
			}
			p := o.Location.Path
			rank := record.SeverityRank(o.Severity)
			if line := o.Location.StartLine; line > 0 && (in.line[p] == 0 || rank > in.severity[p] || (rank == in.severity[p] && line < in.line[p])) {
				in.line[p] = line
			}
			in.observations[p]++
			in.severity[p] = max(in.severity[p], rank)
		}
	}
	return in
}

// screenOf is the scope artifact's screen state.
func screenOf(sc secretScreen) string {
	if sc.state == screenApplied {
		return record.ScreenApplied
	}
	return record.ScreenNotScreened
}

// overviewOffered returns the accepted investigation's code and test
// excerpts and the upstream binding. Other windows remain eligible.
func (s *roleStage) overviewOffered() (map[string][]record.Excerpt, record.Upstream) {
	u := record.Upstream{Source: "review." + record.StageInvestigate}
	b, err := readBounded(recordPath(s.j, record.StageInvestigate), record.MaxBytes)
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordMissing
		return nil, u
	}
	r, err := record.Decode(b, expectStage(s.j, record.StageInvestigate))
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordInvalid
		return nil, u
	}
	if r.Accepted() {
		u.State, u.SHA256 = record.UpstreamAccepted, digestOf(b)
	} else {
		u.State, u.Reason = record.UpstreamFailed, r.FailureReason
	}
	sent := r.Accepted() && slices.ContainsFunc(r.Calls, func(c record.Call) bool { return c.RequestDigest != "" && c.Outcome == record.OutcomeAccepted })
	out := map[string][]record.Excerpt{}
	for _, e := range r.Evidence {
		if sent && e.Origin == "context" && (e.Kind == "code" || e.Kind == "test") {
			out[e.Path] = append(out[e.Path], e)
		}
	}
	return out, u
}

func scopeExpect(j analysis.Job) record.Expect {
	e := expectStage(j, "")
	e.Stage = ""
	return e
}

func persistScope(j analysis.Job, d record.ScopeDoc) (analysis.ArtifactFile, error) {
	b, err := record.MarshalScope(d, scopeExpect(j))
	if err != nil {
		return analysis.ArtifactFile{}, err
	}
	p := scopePath(j)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return analysis.ArtifactFile{}, err
	}
	return analysis.ArtifactFile{Path: p, Role: "other", MediaType: record.ScopeMediaType}, nil
}

// readScope reads and validates this attempt's scope artifact.
func (s *roleStage) readScope() (record.ScopeDoc, record.Upstream) {
	u := record.Upstream{Source: record.TaskScope}
	b, err := readBounded(scopePath(s.j), record.ScopeMaxBytes)
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordMissing
		return record.ScopeDoc{}, u
	}
	d, err := record.DecodeScope(b, scopeExpect(s.j))
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordInvalid
		return record.ScopeDoc{}, u
	}
	u.State, u.SHA256 = record.UpstreamAccepted, digestOf(b)
	return d, u
}

// sliceTask runs review.investigate.sNNN or review.verify.sNNN. Gates in
// order: the run's policy and the secrets screen (Run), the scope artifact,
// a slice with files left to send (no_review_scope), then the call budget.
func (s *roleStage) sliceTask(key string) analysis.TaskResult {
	doc, up := s.readScope()
	if up.State != record.UpstreamAccepted {
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	i := slices.IndexFunc(doc.Slices, func(sl record.ScopeSlice) bool { return sl.Key == key })
	if i < 0 {
		// A planned slot review.scope did not fill: every file found a
		// place in an earlier slot, the overview or the excluded list.
		if n, err := strconv.Atoi(strings.TrimPrefix(key, "s")); err == nil && record.ValidSliceKey(key) && n <= doc.Limits.Slots {
			return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
		}
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	s.slice, s.sliceLimits, s.scopeUp = &doc.Slices[i], doc.Limits, up
	s.sliceReserve = doc.Budget.SliceReserveCalls
	pending := slices.ContainsFunc(s.slice.Files, func(f record.ScopeFile) bool { return f.State == record.FilePlanned })
	if s.role == record.StageVerify {
		b, err := readBounded(admissionPath(s.j, key), 64)
		switch {
		case err == nil && string(b) == decisionExhausted:
			return result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
		case err == nil && string(b) == decisionAdmitted:
			return s.verify()
		case !pending:
			return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
		case s.h.Caller == nil:
			return result(domain.TaskUnsupported, "model_not_configured")
		}
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	if !pending {
		return result(domain.TaskSkippedPolicy, domain.ReasonNoReviewScope)
	}
	// Admitted while max - used >= reserve + hold.
	admitted, err := s.callsBelow(s.h.maxCalls(s.j.Scope)-s.sliceReserve-s.triageHold()+1, false)
	if errors.Is(err, errCallsCancelled) {
		return result(domain.TaskCancelled, "cancelled")
	}
	if err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	decision := decisionAdmitted
	if !admitted {
		decision = decisionExhausted
	}
	if err := os.WriteFile(admissionPath(s.j, key), []byte(decision), 0o600); err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	if decision == decisionExhausted {
		return result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
	}
	return s.investigate()
}

// sliceCorpus verifies the assigned line windows against the snapshot
// again, within the limits the scope artifact recorded. A window that does
// not start its file travels after the file's header (package clause and
// imports, origin header). Older scope artifacts assigned only the first
// window per file.
func (s *roleStage) sliceCorpus() ([]record.Excerpt, []string, error) {
	files := s.entries()
	cached := map[string][]string{}
	var out []record.Excerpt
	// A slice is part of the repository by definition.
	lims := []string{"files_outside_review_context"}
	maxFiles, remain := roleMaxCodeFiles, roleMaxCodeBytes
	if s.sliceLimits.MaxFiles > 0 {
		maxFiles, remain = s.sliceLimits.MaxFiles, s.sliceLimits.MaxBytes
	}
	headers := map[string]bool{}
	// A file whose first unit this slot sends gets no separate header: that
	// unit carries the header lines and may be exactly the header (the same
	// evidence ID twice in one request and record).
	planned := 0
	for _, f := range s.slice.Files {
		if f.State == record.FilePlanned && planned < maxFiles {
			planned++
			headers[f.Path] = headers[f.Path] || f.StartLine == 1
		}
	}
	windows := 0
	for _, f := range s.slice.Files {
		if f.State != record.FilePlanned {
			continue
		}
		e, ok := files[f.Path]
		if !ok || e.SHA256 != f.SHA256 {
			return nil, nil, errSnapshotIntegrity
		}
		if windows >= maxFiles {
			break
		}
		windows++
		if f.StartLine > 0 {
			end := f.StartLine + f.PlannedLines - 1
			lines, ok, err := s.sourceLines(e, cached)
			if err != nil || !ok || len(lines) != f.TotalLines || end > len(lines) || f.PlannedBytes > remain {
				return nil, nil, errSnapshotIntegrity
			}
			text := strings.Join(lines[f.StartLine-1:end], "\n")
			if record.JSONLen(text) != f.PlannedBytes {
				return nil, nil, errSnapshotIntegrity
			}
			if h := min(headerEnd(e.Path, lines), f.StartLine-1); h > 0 && !headers[e.Path] && unitArtifact(s.sliceLimits) {
				headers[e.Path] = true
				out = append(out, record.Excerpt{ID: record.ExcerptID(s.j.Snapshot.Digest, e.Path, e.SHA256, 1, h), Path: e.Path, SHA256: e.SHA256,
					Start: 1, End: h, Kind: windowKind(e.Path), Origin: record.OriginHeader, Text: strings.Join(lines[:h], "\n")})
			}
			out = append(out, record.Excerpt{ID: record.ExcerptID(s.j.Snapshot.Digest, e.Path, e.SHA256, f.StartLine, end),
				Path: e.Path, SHA256: e.SHA256, Start: f.StartLine, End: end, Kind: windowKind(e.Path), Origin: "context", Text: text})
			remain -= f.PlannedBytes
			if f.StartLine > 1 || end < len(lines) {
				lims = append(lims, "excerpt_only")
			}
			continue
		}
		x, total, ok, err := s.excerpt(e, windowKind(f.Path), "context", 1, roleMaxCodeLines, min(roleMaxCodeFile, remain))
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		if x.End < total {
			lims = append(lims, "excerpt_only")
		}
		out = append(out, x)
		remain -= record.JSONLen(x.Text)
	}
	// A file's header and windows travel together, files in path order.
	slices.SortStableFunc(out, func(a, b record.Excerpt) int {
		return cmp.Or(cmp.Compare(path.Dir(a.Path), path.Dir(b.Path)), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Start, b.Start))
	})
	return out, addLim(nil, lims...), nil
}

// changedPaths are the files a pull-request run changes that exist at its
// head commit (sorted), nil for any other run: the review takes them first
// (planCodeGroupsFor).
func changedPaths(j analysis.Job) []string {
	if j.PullRequest == nil {
		return nil
	}
	return append([]string{}, j.PullRequest.Changed...)
}

// priorityOrder is the order the scope artifact records.
func priorityOrder(j analysis.Job) string {
	if j.PullRequest != nil {
		return record.PriorityChangedCoverageFirst
	}
	return record.PriorityCoverageFirst
}
