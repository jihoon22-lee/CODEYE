package review

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	rolepack "github.com/jihoon22-lee/CODEYE/prompts/review-r1"
)

// Running the review slices (docs/REVIEW.md#모듈-단위-검토-분할).
//
// The call budget is attempt-local: one attempt is one worker process that
// runs its tasks one after another, and every role-pack model call of the
// attempt appends one line to a counter file in the attempt scratch before
// the call is made (countCall). A slice investigate is admitted, in plan
// order, only while the ceiling minus the calls made so far is at least
// record.SliceReserveCalls (the worst case of its investigate and verify);
// the decision is written next to the counter and the slice's verify follows
// it. The whole-run roles run before every slice, so they are never cut and
// the attempt never makes more than the ceiling. Running slices in parallel
// inside one attempt would need one shared counter for those tasks.
const (
	reasonBudgetExhausted   = "review_budget_exhausted"
	reasonScreenUnavailable = "secret_screen_unavailable"
	reasonCallCountFailed   = "review_call_count_failed"

	decisionAdmitted  = "admitted"
	decisionExhausted = "budget_exhausted"
)

// DefaultMaxCalls is the model call ceiling per attempt when the node
// operator sets none (codeye agent --review-max-calls).
const DefaultMaxCalls = record.DefaultMaxCalls

// ValidMaxCalls reports whether n is an accepted call ceiling: at least the
// whole-run roles' worst case, at most what every slice could use.
func ValidMaxCalls(n int) bool { return record.ValidMaxCalls(n) }

func callsPath(j analysis.Job) string { return filepath.Join(j.Scratch, "review.r1.calls") }
func scopePath(j analysis.Job) string { return filepath.Join(j.Scratch, "review.r1.scope.json") }
func admissionPath(j analysis.Job, key string) string {
	return filepath.Join(j.Scratch, "review.r1.admission."+key)
}

// countCall records one model call of this attempt before it is made. A
// call that cannot be counted is not made.
func (s *roleStage) countCall() error {
	f, err := os.OpenFile(callsPath(s.j), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(s.task + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
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

// scopeTask is review.scope: no model call. It reads the overview
// investigate's record (the files it actually sent), checks every file of
// the plan against the secrets screen and the read filters, and writes the
// scope artifact the slices run from.
func (s *roleStage) scopeTask() analysis.TaskResult {
	sel, err := rolepack.For(record.StageInvestigate, languagesInScope(s.j))
	if err != nil {
		return result(domain.TaskFailed, "review_pack_invalid")
	}
	used, err := callsUsed(s.j)
	if err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	plan := planSlices(s.j.Snapshot.Manifest, s.j.Scope)
	offered, up := s.overviewOffered()
	sent := map[string]bool{}
	for _, p := range offered {
		sent[p] = true
	}
	files := s.entries()
	inspect := func(f plannedFile, state string) (record.ScopeFile, error) {
		sf := record.ScopeFile{Path: f.Path, SHA256: f.SHA256, Module: f.Module, BoundBytes: f.Bound, State: state}
		e, ok := files[f.Path]
		if !ok || e.SHA256 != f.SHA256 {
			return sf, errSnapshotIntegrity
		}
		if s.secretFiles()[f.Path] {
			sf.State = record.FileWithheld
			return sf, nil
		}
		x, total, ok, err := s.excerpt(e, windowKind(f.Path), "context", 1, 50, roleMaxCodeFile)
		if err != nil {
			return sf, err
		}
		switch {
		case ok:
			sf.PlannedLines, sf.PlannedBytes, sf.TotalLines = x.End, record.JSONLen(x.Text), total
		case !sent[f.Path]:
			// Not UTF-8, NUL bytes, a sensitive pattern or no text.
			sf.State = record.FileUnreadable
		}
		if sent[f.Path] {
			sf.State = record.FileCoveredByOverview
		}
		return sf, nil
	}
	doc := record.ScopeDoc{Schema: record.ScopeSchemaVersion, Kind: record.ScopeKind, Pack: record.PackRef{ID: sel.ID, Version: sel.Version}, Snapshot: s.j.Snapshot.Digest,
		RunID: s.j.RunID, AttemptID: s.j.AttemptID, Screen: record.ScreenApplied,
		Budget: record.ScopeBudget{MaxCalls: s.h.maxCalls(), MaxRequestBytes: PromptBudget, WholeRunMaxCalls: record.WholeRunMaxCalls, SliceReserveCalls: record.SliceReserveCalls,
			CallsBeforeSlices: min(used, record.WholeRunMaxCalls), Admission: record.AdmissionSequentialReserve},
		Limits:   record.ScopeLimits{MaxFiles: sliceMaxFiles, MaxBytes: sliceMaxBytes, MaxFileBytes: roleMaxCodeFile, MaxFileLines: 50, MaxSlices: MaxSlices},
		Upstream: []record.Upstream{up}, Modules: []record.ScopeModule{}, Overview: record.ScopeOverview{Files: []record.ScopeFile{}, Offered: nonNilList(offered)},
		Slices: []record.ScopeSlice{}, Limitations: []string{}}
	for _, f := range plan.Overview {
		sf, err := inspect(f, record.FileOverviewNotSent)
		if err != nil {
			return result(domain.TaskFailed, "review_input_unavailable")
		}
		doc.Overview.Files = append(doc.Overview.Files, sf)
	}
	for _, sl := range plan.Slices {
		inv, ver := sl.TaskKeys()
		out := record.ScopeSlice{Key: sl.Key, InvestigateTask: inv, VerifyTask: ver, Modules: sl.Modules, Files: []record.ScopeFile{}}
		for _, f := range sl.Files {
			sf, err := inspect(f, record.FilePlanned)
			if err != nil {
				return result(domain.TaskFailed, "review_input_unavailable")
			}
			out.Files = append(out.Files, sf)
		}
		doc.Slices = append(doc.Slices, out)
	}
	// A file past the slice limit the overview sent after all is covered
	// by the overview; the rest are counted per module as not planned.
	notPlanned := map[string]int{}
	for _, f := range plan.NotPlanned {
		if !sent[f.Path] {
			notPlanned[f.Module]++
			continue
		}
		sf, err := inspect(f, record.FileCoveredByOverview)
		if err != nil {
			return result(domain.TaskFailed, "review_input_unavailable")
		}
		doc.Overview.Files = append(doc.Overview.Files, sf)
	}
	discovered := map[string]bool{}
	mods, _ := discovery.DetectModules(s.j.Snapshot.Manifest, envdoc.Scope{Kind: "full", Paths: []string{}})
	for _, m := range mods {
		discovered[m.ID] = true
	}
	for _, g := range plan.Groups {
		doc.Modules = append(doc.Modules, record.ScopeModule{ID: g.Module, Discovered: discovered[g.Module], FilesEligible: g.Eligible, FilesNotPlanned: notPlanned[g.Module]})
		if notPlanned[g.Module] > 0 {
			doc.Limitations = addLim(doc.Limitations, limSliceLimit)
		}
	}
	art, err := persistScope(s.j, doc)
	if err != nil {
		return result(domain.TaskFailed, "review_record_failed")
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}, Limitations: doc.Limitations}
}

// overviewOffered returns the code and test files the overview investigate
// sent to the model (the context excerpts of its record, accepted or failed,
// when it made a call) and the upstream binding of that record.
func (s *roleStage) overviewOffered() ([]string, record.Upstream) {
	u := record.Upstream{Source: "review." + record.StageInvestigate}
	b, err := readBounded(rolePath(s.j, record.StageInvestigate), record.MaxBytes)
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordMissing
		return []string{}, u
	}
	r, err := record.DecodeRole(b, expectRole(s.j, record.StageInvestigate))
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordInvalid
		return []string{}, u
	}
	if r.Accepted() {
		u.State, u.SHA256 = record.UpstreamAccepted, digestOf(b)
	} else {
		u.State, u.Reason = record.UpstreamFailed, r.FailureReason
	}
	sent := slices.ContainsFunc(r.Calls, func(c record.RoleCall) bool { return c.RequestDigest != "" })
	out := []string{}
	for _, e := range r.Evidence {
		if sent && e.Origin == "context" && (e.Kind == "code" || e.Kind == "test") && !slices.Contains(out, e.Path) {
			out = append(out, e.Path)
		}
	}
	sort.Strings(out)
	return out, u
}

func scopeExpect(j analysis.Job) record.Expect {
	e := expectRole(j, "")
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
		return result(domain.TaskSkippedPolicy, reasonDependency)
	}
	s.slice, s.scopeUp = &doc.Slices[i], up
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
	used, err := callsUsed(s.j)
	if err != nil {
		return result(domain.TaskFailed, reasonCallCountFailed)
	}
	decision := decisionAdmitted
	if s.h.maxCalls()-used < record.SliceReserveCalls {
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

// sliceCorpus builds the slice's code and test excerpts the way codeCorpus
// does (first 50 lines, at most 2,400 bytes each, 12,000 in total), over the
// slice's files still planned in the scope artifact. The read filters and
// the secrets screen apply again; a file that no longer reads is skipped.
func (s *roleStage) sliceCorpus() ([]record.Excerpt, []string, error) {
	files := s.entries()
	var out []record.Excerpt
	// A slice is part of the repository by definition.
	lims := []string{"files_outside_review_context"}
	remain := roleMaxCodeBytes
	for _, f := range s.slice.Files {
		if f.State != record.FilePlanned {
			continue
		}
		e, ok := files[f.Path]
		if !ok || e.SHA256 != f.SHA256 {
			return nil, nil, errSnapshotIntegrity
		}
		if len(out) >= roleMaxCodeFiles || remain < 128 {
			break
		}
		x, total, ok, err := s.excerpt(e, windowKind(f.Path), "context", 1, 50, min(roleMaxCodeFile, remain))
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
	return out, addLim(nil, lims...), nil
}
