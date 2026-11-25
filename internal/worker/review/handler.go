package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review"
)

// The full message text of one review request (opencode.Prompt:
// instructions, contract, repair rules, input and wrapper) is bounded by
// the operator's request budget (Budget, budget.go); a repair request keeps
// room within it (limits.reserve).

// Task reasons and limitation codes of the review.
const (
	ReasonNoDocuments       = "no_documents"
	ReasonDocsNotSelected   = "documents_not_selected"
	ReasonNoToolObservation = "no_tool_observations"
	ReasonNoEnvironmentGap  = "no_environment_gap"
	reasonDependency        = "dependency_failed"
	reasonUnknownRole       = "unknown_review_role"
	reasonRecordMissing     = "record_missing"
	reasonRecordInvalid     = "record_invalid"
	limInputTrimmed         = "review_input_trimmed"
	// code_excerpts_go_only (code context of Go files only) is no longer
	// written; the report still names it for earlier records.
)

// Handler composes the static analyzer with the candidate roles of the
// review. Planning uses the manifest and bounded embedded documentation;
// every role reads
// only platform artifacts of this attempt and accepted earlier role records,
// each validated again. Model output never decides a task status, a platform
// verdict or a reproduction.
type Handler struct {
	Static analysis.Handler
	Caller Caller
	// MaxCalls is the node operator's model call ceiling per attempt
	// (codeye agent --review-max-calls); 0 means record.DefaultMaxCalls. It
	// decides which review slices are admitted at run time and never the
	// plan.
	MaxCalls int
	// Secondary is the path rule of test input, examples and third-party
	// material (docs/REVIEW.md#검토-우선순위); nil means
	// record.DefaultSecondary. It orders the overview's files and the slots.
	Secondary *record.SecondaryRule
	// Budget is the node operator's request size; the zero value is the
	// default budget.
	Budget Budget
}

var _ analysis.Handler = Handler{}

func (h Handler) secondary() record.SecondaryRule {
	if h.Secondary == nil {
		return record.DefaultSecondary
	}
	return *h.Secondary
}

func (h Handler) Info() analysis.HandlerInfo { return h.Static.Info() }

// modelSkipReason is the reason of a review task the job may not send to
// the model: the service withholding it from the user who started the run,
// or the source not approved (the project's choice or the node's grant).
func modelSkipReason(j analysis.Job) string {
	if j.ModelReviewWithheld {
		return domain.ReasonModelReviewNotOffered
	}
	return domain.ReasonModelSourceNotApproved
}

func (h Handler) maxCalls(scope domain.Scope) int {
	if h.MaxCalls == 0 {
		return record.DefaultMaxCalls
	}
	if scope.ReviewCoverage != domain.ReviewCoverageExtended {
		return min(h.MaxCalls, record.LegacyMaxCalls)
	}
	return h.MaxCalls
}

// Plan appends the whole-run review roles. If there is source in scope it
// also plans the inventory and optional investigation and verification
// pairs. Their keys are fixed here and review.scope assigns the remaining
// lines after the overview. The optional contract follow-up comes last.
func (h Handler) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, err := h.Static.Plan(ctx, j)
	if err != nil {
		return nil, err
	}
	docs := len(discovery.EligibleDocuments(j.Snapshot.Manifest, j.Scope)) > 0
	if !docs {
		docs, err = discovery.HasJavaAPIDocuments(ctx, j.Snapshot, j.Scope, j.PullRequest)
		if err != nil {
			return nil, err
		}
	}
	plan = append(plan,
		analysis.TaskSpec{Key: "review.environment", Required: false, Title: "환경 영향 검토", Stage: domain.StageEnvironment},
		analysis.TaskSpec{Key: "review.documents", Required: docs, Title: "문서 계약 검토", Stage: domain.StageDocuments},
		analysis.TaskSpec{Key: "review.investigate", Required: true, Title: "독립 코드 검토", Stage: domain.StageCodeReview},
		analysis.TaskSpec{Key: "review.interpret", Required: false, Title: "정적 진단 해석", Stage: domain.StageCodeReview},
		analysis.TaskSpec{Key: "review.verify", Required: true, Title: "후보 반례 검증", Stage: domain.StageCodeReview})
	sp := planSlicesFor(j.Snapshot.Manifest, j.Scope, h.secondary(), changedPaths(j))
	groups := codeGroupsFor(j.Snapshot.Manifest, j.Scope, h.secondary(), changedPaths(j), true)
	if groups.eligible > 0 {
		plan = append(plan, analysis.TaskSpec{Key: record.TaskScope, Required: false, Title: "모듈별 검토 범위", Stage: domain.StageCodeReview})
		slots := coverageSlots(sp)
		if j.Scope.ReviewCoverage == domain.ReviewCoverageExtended {
			planner := roleStage{ctx: ctx, j: j}
			if n, err := planner.coverageSlotPlan(groups); err == nil {
				slots = n
			}
		}
		for i := range slots {
			inv, ver := slotTaskKeys(sliceKey(i))
			plan = append(plan,
				analysis.TaskSpec{Key: inv, Required: false, Title: "모듈별 코드 검토 " + sliceKey(i), Stage: domain.StageCodeReview},
				analysis.TaskSpec{Key: ver, Required: false, Title: "모듈별 후보 검증 " + sliceKey(i), Stage: domain.StageCodeReview})
		}
	}
	isCode := func(f plannedFile) bool { return record.SourceKind(f.Path) == "code" }
	if j.AllowModelReview && h.Caller != nil && docs && (slices.ContainsFunc(sp.Overview, isCode) || slices.ContainsFunc(sp.Rest, isCode)) {
		plan = append(plan, analysis.TaskSpec{Key: record.TaskContract, Title: "문서 계약 후속 검토", Stage: domain.StageCodeReview})
	}
	// The diagnostic triage slots come last: the module slices and the
	// contract follow-up are admitted first and leave the triage a bounded
	// share of the call ceiling (triageHold, docs/REVIEW.md#호출-비용).
	for i := range triageSlotCount(groups.eligible) {
		tri, ver := "review."+record.StageTriage+"."+triageKey(i), "review."+record.StageVerify+"."+triageKey(i)
		plan = append(plan,
			analysis.TaskSpec{Key: tri, Required: false, Title: "도구 진단 분류 " + triageKey(i), Stage: domain.StageCodeReview},
			analysis.TaskSpec{Key: ver, Required: false, Title: "분류 후보 검증 " + triageKey(i), Stage: domain.StageCodeReview})
	}
	return plan, nil
}

func (h Handler) Run(ctx context.Context, j analysis.Job, t analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	role, ok := strings.CutPrefix(t.Key, "review.")
	if !ok {
		return h.Static.Run(ctx, j, t, runner)
	}
	slice, triage := "", ""
	tStage, tKey, isTriage := record.TriageTask(t.Key)
	switch stage, key, isSlice := record.SliceTask(t.Key); {
	case t.Key == record.TaskScope:
	case isSlice:
		role, slice = stage, key
	case isTriage:
		role, triage = tStage, tKey
	case !slices.Contains(reviewpack.Roles, role):
		return result(domain.TaskFailed, reasonUnknownRole)
	}
	if !j.AllowModelReview {
		return result(domain.TaskSkippedPolicy, modelSkipReason(j))
	}
	if ctx.Err() != nil {
		return result(domain.TaskCancelled, "cancelled")
	}
	// Every role, the scope and the slices follow one screen rule: without a
	// usable secrets result their inputs go through the read filters only and
	// the records say model_input_not_secret_screened (screen.go).
	s := &roleStage{h: h, ctx: ctx, j: j, runner: runner, task: t.Key, role: role, screen: screenSecrets(ctx, h.Static, j)}
	if t.Key == record.TaskScope {
		return s.scopeTask()
	}
	if slice != "" {
		return s.sliceTask(slice)
	}
	if triage != "" {
		return s.triageTask(triage)
	}
	switch role {
	case record.StageEnvironment:
		return s.environment()
	case record.StageDocuments:
		return s.documents()
	case record.StageInvestigate:
		return s.investigate()
	case record.StageInterpret:
		return s.interpret()
	case record.StageContract:
		return s.contractFollowup()
	}
	return s.verify()
}

// roleStage is one role run of the review.
type roleStage struct {
	h      Handler
	ctx    context.Context
	j      analysis.Job
	runner executor.Runner
	task   string // the task key (review.investigate.s001 for a slice)
	role   string
	pack   reviewpack.Selection
	rec    record.Record
	// screen is the secrets screen of the run's model inputs (screen.go).
	screen secretScreen
	// slice is set for a slice task: its entry of the review.scope artifact
	// and that artifact's upstream binding (slice_run.go).
	slice *record.ScopeSlice
	// triage is the slot key of a diagnostic triage task or its verify
	// (triage_run.go).
	triage       string
	sliceLimits  record.ScopeLimits
	scopeUp      record.Upstream
	sliceReserve int
	// lastChance is set by exchange while parse reads an output that gets
	// no further format repair: parse then resolves faults of form (fixes
	// or leaves out the item, docs/REVIEW.md#출력-오류-분류) and lists their
	// codes in degraded. Before that, parse names every fault and sets
	// repairable when all of them are of form.
	lastChance bool
	degraded   []string
	repairable bool
}

// limRepairNotSent: the format repair could not be sent (call budget,
// request size or its input) and the first output was kept with its faults
// of form resolved.
const limRepairNotSent = "output_repair_not_sent"

// limit is the bounds of the handler's request budget.
func (s *roleStage) limit() limits { return s.h.Budget.limits() }

// begin loads the pack and the record skeleton; a nil caller is reported
// only after the skip rules, which need no model.
func (s *roleStage) begin(upstream []record.Upstream) *analysis.TaskResult {
	if s.h.Caller == nil {
		r := result(domain.TaskUnsupported, "model_not_configured")
		return &r
	}
	sel, err := reviewpack.For(s.role, languagesInScope(s.j))
	if err != nil {
		r := result(domain.TaskFailed, "review_instructions_invalid")
		return &r
	}
	s.pack = sel
	if upstream == nil {
		upstream = []record.Upstream{}
	}
	s.rec = record.Record{Schema: record.SchemaVersionCitedLines, Pack: record.PackRef{ID: sel.ID, Version: sel.Version}, Snapshot: s.j.Snapshot.Digest,
		RunID: s.j.RunID, AttemptID: s.j.AttemptID, Stage: s.role, Outcome: record.OutcomeAccepted, Upstream: upstream,
		Evidence: []record.Excerpt{}, Observations: []record.ObservationRef{}, Limitations: []string{}, Calls: []record.Call{}}
	if s.slice != nil {
		s.rec.Slice = &record.SliceRef{Key: s.slice.Key, Modules: append([]string{}, s.slice.Modules...)}
	}
	if s.triage != "" {
		s.rec.Schema, s.rec.Slice = record.SchemaVersionTriage, record.TriageSlotRef(s.triage)
	}
	return nil
}

// screenLimitation is added when the record is written, after the model
// input was built: the record and the report say the inputs were not
// screened for secrets; the model input stays as it was.
func (s *roleStage) screenLimitation(lims []string) []string {
	if s.screen.state == screenUnscreened {
		return addLim(lims, limNotSecretScreened)
	}
	return lims
}

// languagesInScope are the source ecosystems whose language guides a role
// gets: the analyzer packs' one definition of an ecosystem in scope
// (pack.InScope, docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백) over the job's manifest and scope. Every call
// records the selected guide files in pack_files.
func languagesInScope(j analysis.Job) []string {
	return pack.InScope(j.Snapshot.Manifest, j.Scope)
}

func (s *roleStage) setEvidence(list []record.Excerpt) {
	s.rec.Evidence = make([]record.Excerpt, len(list))
	for i, e := range list {
		e.Text = ""
		s.rec.Evidence[i] = e
	}
}

func (s *roleStage) instructions(contract string) string {
	return s.pack.Text + "\n" + contract + s.requestRules()
}

// requestRules state the input, citation and prose rules of every role;
// the prose length follows the request budget.
func (s *roleStage) requestRules() string {
	return fmt.Sprintf(requestRules, s.limit().chars, 2*s.limit().chars)
}

const requestRules = "\nUse only the provided input. No code execution, test runs, reproduction or external access: never claim any. " +
	"Source excerpts show each line as \"<line>| <text>\"; the numbers are not part of the source. Cite only evidence and observation IDs given in this input. " +
	citationRule + " Add no paths or other fields. " +
	"Keep every text field under %d characters of concise Korean prose (a mechanism under %d)."

// citationRule is how every role cites code and test lines (record.MaxCitedLines,
// record.WholeCitationLines); the example has the format of a request's evidence IDs.
const citationRule = "Cite a code or test excerpt as \"<evidence id>:<start>-<end>\" (one line: \"<evidence id>:<line>\"), for example \"ev_5f0c2a9e1b7d4c3a8e6f2b10:120-134\": " +
	"the lines that show the point, inside that excerpt's start_line..end_line and at most 80 lines. " +
	"An excerpt of more than 80 lines (end_line - start_line + 1 > 80) is citable only with such a range: its ID alone is rejected. " +
	"Cite a document excerpt by its ID alone."

const repairRules = "\nFormat repair: your previous output was rejected with the platform error codes in input.repair.output_errors; " +
	"input.repair.items names each rejected item by list, 1-based index and field, with the cited evidence_id when the fault is a citation. " +
	"Return the complete result again in the same contract, citing only IDs from this input and an excerpt of more than 80 lines only as \"<evidence id>:<start>-<end>\"."

// outputRepair carries fixed codes and, for document scope failures, choices
// derived from the excerpts already sent to the model; items names each
// rejected item of a list.
type outputRepair struct {
	OutputErrors   []string             `json:"output_errors"`
	DocumentScopes []documentScopeIssue `json:"document_scopes,omitempty"`
	Items          []itemIssue          `json:"items,omitempty"`
}

// itemIssue is one fault of an item of the output: its list, 1-based
// position, field, the sent excerpt the field cites when the fault is a
// citation (an ID the request did not hold is never echoed) and the fixed
// code. No model text.
type itemIssue struct {
	List  string `json:"list"`
	Index int    `json:"index"`
	Field string `json:"field"`
	ID    string `json:"evidence_id,omitempty"`
	Error string `json:"error"`
}

// fitItems builds a repair request with as many of issues as the request
// budget leaves room for (all, or a prefix, or none).
func (s *roleStage) fitItems(repair *outputRepair, issues []itemIssue, instructions string, build func(*outputRepair) ([]byte, error)) ([]byte, error) {
	if repair == nil || len(issues) == 0 {
		return build(repair)
	}
	for n := len(issues); n > 0; n-- {
		r := *repair
		r.Items = issues[:n]
		body, err := build(&r)
		if err != nil {
			return nil, err
		}
		if _, ok := s.fits(instructions, body, false); ok {
			return body, nil
		}
	}
	return build(repair)
}

// fits reports whether the request text fits the budget, keeping room for
// one repair request when reserve is set.
func (s *roleStage) fits(instructions string, input []byte, reserve bool) (int, bool) {
	n := len(opencode.Prompt(opencode.Request{Role: s.role, Instructions: instructions + repairRules, Input: input}))
	if reserve {
		n += s.limit().reserve
	}
	return n, n <= s.limit().request
}

// exchange sends a request and handles bounded retries and format repair.
// parse validates an output and returns platform error codes; it must keep
// its result aside until it returns none.
func (s *roleStage) exchange(contract string, input func(*outputRepair) ([]byte, error), parse func(json.RawMessage) []string) *analysis.TaskResult {
	instructions := s.instructions(contract)
	var repair *outputRepair
	repaired, retried, proofRetried := false, false, false
	maxCalls := record.TaskMaxCalls(s.task)
	// fallback is the latest rejected output whose faults are all of form
	// (s.repairable after its parse) and fallbackCall its call: once no
	// further repair can arrive, it is accepted with those faults resolved
	// (docs/REVIEW.md#출력-오류-분류).
	var fallback json.RawMessage
	fallbackCall := -1
	degrade := func(lim string) bool {
		if fallback == nil {
			return false
		}
		s.lastChance, s.degraded = true, nil
		if errs := parse(fallback); len(errs) != 0 {
			return false
		}
		s.rec.Calls[fallbackCall].Outcome = callAccepted
		s.rec.Limitations = addLim(s.rec.Limitations, record.LimOutputDegraded)
		if lim != "" {
			s.rec.Limitations = addLim(s.rec.Limitations, lim)
		}
		return true
	}
	// end fails the stage unless the form-only fallback can stand.
	end := func(res analysis.TaskResult, lim string) *analysis.TaskResult {
		if res.Status != domain.TaskCancelled && res.Reason != reasonCallCapExceeded && s.ctx.Err() == nil && degrade(lim) {
			return nil
		}
		return s.fail(res)
	}
	for attempt := 1; attempt <= maxCalls; attempt++ {
		if s.ctx.Err() != nil {
			return s.fail(result(domain.TaskCancelled, "cancelled"))
		}
		query, err := input(repair)
		if err != nil && s.ctx.Err() != nil {
			return s.fail(result(domain.TaskCancelled, "cancelled"))
		}
		if err != nil {
			return end(result(domain.TaskFailed, "review_input_unavailable"), limRepairNotSent)
		}
		text := instructions
		if repair != nil {
			text += repairRules
		}
		q := opencode.Request{Role: s.role, Scratch: s.j.Scratch, SnapshotDigest: s.j.Snapshot.Digest, Instructions: text, Input: query, ProtectedRoots: []string{s.j.Snapshot.SnapshotDir}}
		size := len(opencode.Prompt(q))
		if size > s.limit().request {
			return end(result(domain.TaskFailed, reasonInputTooLarge), limRepairNotSent)
		}
		if r := s.reserveCall(); r != nil {
			return end(*r, limRepairNotSent)
		}
		reply := s.h.Caller.Review(s.ctx, s.runner, q)
		call := s.recordCall(reply, size, attempt)
		if reply.Reason != "" && reply.Reason != "model_output_invalid" {
			call.Outcome = reply.Reason
			if !record.ValidCode(call.Outcome) {
				call.Outcome = "model_unavailable"
			}
			s.rec.Calls = append(s.rec.Calls, call)
			if reply.WorkspaceProofMismatch() {
				if s.ctx.Err() != nil {
					return s.fail(result(domain.TaskCancelled, "cancelled"))
				}
				if !proofRetried && attempt < maxCalls {
					proofRetried = true
					continue
				}
			}
			return end(failure(reply), "")
		}
		notJSON := reply.Reason != ""
		errs := []string{"output_not_json"}
		if !notJSON {
			// The format repair is spent (or no call is left): parse now
			// resolves faults of form instead of naming them.
			s.lastChance, s.degraded, s.repairable = repaired || attempt == maxCalls, nil, false
			errs = parse(reply.Output)
		}
		if len(errs) == 0 {
			call.Outcome, call.OutputErrors = callAccepted, nonNil(addLim(nil, s.degraded...))
			if len(s.degraded) > 0 {
				s.rec.Limitations = addLim(s.rec.Limitations, record.LimOutputDegraded)
			}
			s.rec.Calls = append(s.rec.Calls, call)
			return nil
		}
		call.Outcome, call.OutputErrors = callRejected, addLim(nil, errs...)
		s.rec.Calls = append(s.rec.Calls, call)
		s.noteExecutionClaim(call)
		if !notJSON && s.repairable {
			fallback, fallbackCall = reply.Output, len(s.rec.Calls)-1
		}
		// One repair of a rejected output, and one retry of a reply that was
		// not JSON at all; the retry keeps the earlier repair's codes.
		if notJSON {
			if retried {
				break
			}
			retried = true
			if repair != nil {
				repair = &outputRepair{OutputErrors: addLim(append([]string{}, repair.OutputErrors...), errs...)}
				continue
			}
		} else {
			if repaired {
				break
			}
			repaired = true
		}
		repair = &outputRepair{OutputErrors: call.OutputErrors}
	}
	return end(result(domain.TaskFailed, reasonOutputInvalid), "")
}

func (s *roleStage) recordCall(reply opencode.Reply, size, attempt int) record.Call {
	s.rec.Limitations = noteVersion(s.rec.Limitations, reply)
	return record.Call{Role: s.role, Model: reply.Model, Version: reply.Version, PackID: s.pack.ID, PackVersion: s.pack.Version,
		PackDigest: s.pack.Digest, PackFiles: s.pack.Files, RequestDigest: reply.RequestDigest, RequestBytes: size,
		Usage: usage(reply.Usage), Attempt: attempt, OutputErrors: []string{}}
}

// reserveCall checks the attempt's call ceiling and records the call before
// it is made (callsBelow: the check and the record are one step, in plan
// order, also when pairs run concurrently).
func (s *roleStage) reserveCall() *analysis.TaskResult {
	reserve := 0
	if s.slice != nil && s.role == record.StageInvestigate && s.sliceReserve == record.SliceMinCalls {
		reserve = 1
	}
	// A triage retry keeps the call its verification needs.
	if s.triage != "" && s.role == record.StageTriage {
		reserve = 1
	}
	// The module slices and the contract follow-up leave the triage
	// slots their held share.
	if s.slice != nil || s.role == record.StageContract {
		reserve += s.triageHold()
	}
	ok, err := s.callsBelow(s.h.maxCalls(s.j.Scope)-reserve, true)
	switch {
	case errors.Is(err, errCallsCancelled):
		r := result(domain.TaskCancelled, "cancelled")
		return &r
	case errors.Is(err, errCallCapExceeded):
		r := result(domain.TaskFailed, reasonCallCapExceeded)
		return &r
	case err != nil:
		r := result(domain.TaskFailed, reasonCallCountFailed)
		return &r
	case !ok:
		r := result(domain.TaskBudgetExhausted, reasonBudgetExhausted)
		return &r
	}
	return nil
}

// noteExecutionClaim records at stage level that the platform rejected a
// model claim of running or reproducing something, so an evaluation can count
// it without reading every call.
func (s *roleStage) noteExecutionClaim(c record.Call) {
	if slices.Contains(c.OutputErrors, "execution_claim") {
		s.rec.Limitations = addLim(s.rec.Limitations, record.LimExecutionClaim)
	}
}

// fail keeps the attempted calls, the inputs given and the upstream states
// in a failed record; model content is quarantined (not kept).
func (s *roleStage) fail(res analysis.TaskResult) *analysis.TaskResult {
	r := s.rec
	r.Limitations = s.screenLimitation(r.Limitations)
	r.Outcome, r.FailureReason = record.OutcomeFailed, res.Reason
	if !record.ValidCode(r.FailureReason) {
		r.FailureReason = "review_failed"
	}
	r.EnvironmentNotes, r.Contracts, r.Conflicts, r.Questions, r.InstructionNotes, r.Limits = nil, nil, nil, nil, nil, nil
	r.ContractComparisons = nil
	r.PartialSupport = nil
	if r.Stage != record.StageVerify {
		r.Candidates = nil
	} else {
		r.Verifications = make([]record.Verification, len(r.Candidates))
		for i, c := range r.Candidates {
			r.Verifications[i] = unreviewed(c.ID, record.LimNotCompleted)
		}
	}
	r.Calls = append([]record.Call{}, r.Calls...)
	for i := range r.Calls {
		if r.Calls[i].Outcome == callAccepted {
			r.Calls[i].Outcome = callDiscarded
		}
	}
	if art, err := persistRecord(s.j, r); err == nil {
		res.Artifacts = append(res.Artifacts, art)
	} else {
		res.Limitations = addLim(res.Limitations, "review_record_failed")
	}
	return &res
}

// finish writes the accepted record.
func (s *roleStage) finish() analysis.TaskResult {
	s.rec.Limitations = s.screenLimitation(addLim(s.rec.Limitations))
	art, err := persistRecord(s.j, s.rec)
	if err != nil {
		return result(domain.TaskFailed, "review_record_failed")
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}, Limitations: s.rec.PublicLimitations()}
}

// recordPath is the scratch file of a role record: name is a stage, or
// stage.sNNN for a slice record.
func recordPath(j analysis.Job, name string) string {
	return filepath.Join(j.Scratch, "review."+name+".json")
}

// recordName is the recordPath name of a record.
func recordName(r record.Record) string {
	if r.Slice != nil {
		return r.Stage + "." + r.Slice.Key
	}
	return r.Stage
}

// expectRecord is expectStage for a record name (stage or stage.sNNN).
func expectRecord(j analysis.Job, name string) record.Expect {
	stage, slice, _ := strings.Cut(name, ".")
	e := expectStage(j, stage)
	e.Slice = slice
	return e
}

func expectStage(j analysis.Job, stage string) record.Expect {
	e := expect(j, stage)
	// Review manifest evidence (requirement sources) may lie outside a paths
	// scope: discovery reads a module's manifest when the scope touches it.
	index := map[string]string{}
	for _, en := range j.Snapshot.Manifest.Entries {
		if en.Kind == "file" && en.SHA256 != "" && en.ExcludedReason == "" {
			index[en.Path] = en.SHA256
		}
	}
	e.Lookup = func(p string) (string, bool) { sha, ok := index[p]; return sha, ok }
	return e
}

func persistRecord(j analysis.Job, r record.Record) (analysis.ArtifactFile, error) {
	b, err := record.Marshal(r, expectRecord(j, recordName(r)))
	if err != nil {
		return analysis.ArtifactFile{}, err
	}
	p := recordPath(j, recordName(r))
	if err = os.WriteFile(p, bytes.TrimSpace(b), 0600); err != nil {
		return analysis.ArtifactFile{}, err
	}
	return analysis.ArtifactFile{Path: p, Role: "other", MediaType: MediaType}, nil
}

func digestOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func readBounded(p string, max int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("artifact too large")
	}
	return b, nil
}

// prior reads an earlier role record of this attempt (name is a stage, or
// stage.sNNN for a slice record). A missing whole-run record is explained by
// the stage's own skip rule when it applies.
func (s *roleStage) prior(name string) (record.Record, record.Upstream) {
	u := record.Upstream{Source: "review." + name}
	b, err := readBounded(recordPath(s.j, name), record.MaxBytes)
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordMissing
		if reason := s.skipReason(name); reason != "" {
			u.State, u.Reason = record.UpstreamSkipped, reason
		}
		return record.Record{}, u
	}
	r, err := record.Decode(b, expectRecord(s.j, name))
	switch {
	case err != nil:
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordInvalid
	case !r.Accepted():
		u.State, u.Reason = record.UpstreamFailed, r.FailureReason
	default:
		u.State, u.SHA256 = record.UpstreamAccepted, digestOf(b)
		return r, u
	}
	return record.Record{}, u
}

// skipReason recomputes why a stage finished without a record, using the
// same platform inputs and rules the stage used.
func (s *roleStage) skipReason(stage string) string {
	switch stage {
	case record.StageEnvironment:
		_, reason := s.environmentFacts()
		return reason
	case record.StageDocuments:
		_, reason := s.documentFacts()
		return reason
	case record.StageInterpret:
		_, _, reason := s.toolFacts()
		return reason
	}
	return ""
}

// upstreamLimitation records a skipped or failed upstream role.
func upstreamLimitation(u record.Upstream) string {
	if u.State == record.UpstreamAccepted {
		return ""
	}
	name := strings.NewReplacer("review.", "", ".", "_", "-", "_").Replace(u.Source)
	return "upstream_" + name + "_" + u.State
}

func (s *roleStage) noteUpstream(us ...record.Upstream) {
	for _, u := range us {
		if l := upstreamLimitation(u); l != "" {
			s.rec.Limitations = addLim(s.rec.Limitations, l)
		}
	}
}

// strictList decodes a model result object whose listed keys must all be
// present (as arrays); unknown keys are rejected.
func decodeOutput(raw json.RawMessage, into any, keys ...string) []string {
	var head map[string]json.RawMessage
	if json.Unmarshal(raw, &head) != nil {
		return []string{"output_schema_invalid"}
	}
	for _, k := range keys {
		v, ok := head[k]
		if !ok || len(v) == 0 || v[0] != '[' {
			return []string{"output_schema_invalid"}
		}
	}
	if domain.DecodeStrict(raw, into) != nil {
		return []string{"output_schema_invalid"}
	}
	return nil
}

// textErrors returns the codes of invalid model prose within the budget's
// prose limit.
func (s *roleStage) textErrors(texts ...string) []string {
	return proseErrors(s.limit().text, texts...)
}

func proseErrors(limit int, texts ...string) []string {
	var errs []string
	for _, t := range texts {
		switch {
		case record.ClaimsExecution(t):
			errs = addLim(errs, "execution_claim")
		case !record.ProseText(t, limit):
			errs = addLim(errs, "text_invalid")
		}
	}
	return errs
}

// idErrors checks citations against the IDs given to this call.
func idErrors(ids []string, given map[string]record.Excerpt, max int, kinds ...string) []string {
	var errs []string
	if len(ids) == 0 {
		return []string{"evidence_missing"}
	}
	if len(ids) > max {
		errs = addLim(errs, "evidence_list_too_long")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		e, ok := given[id]
		switch {
		case !ok && rangedKnownID(id, given) != "":
			// A line range on a sent excerpt that is cited by its ID alone
			// (a document or manifest excerpt): a fault of form.
			errs = addLim(errs, errCitationInvalid)
		case !ok:
			errs = addLim(errs, "evidence_id_unknown")
		case seen[id]:
			errs = addLim(errs, "evidence_id_duplicate")
		case len(kinds) > 0 && !slices.Contains(kinds, e.Kind):
			errs = addLim(errs, "evidence_kind_invalid")
		}
		seen[id] = true
	}
	return errs
}

// rangedKnownID is the sent excerpt ID of a citation "<id>:<anything>" of
// given, or "".
func rangedKnownID(id string, given map[string]record.Excerpt) string {
	prefix, _, ranged := strings.Cut(id, ":")
	if _, ok := given[strings.TrimSpace(prefix)]; ranged && ok {
		return strings.TrimSpace(prefix)
	}
	return ""
}

// stripKnownRanges replaces each ranged citation of a sent excerpt with its
// ID (the whole excerpt, citation_range_unspecified).
func stripKnownRanges(ids []string, given map[string]record.Excerpt) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id
		if base := rangedKnownID(id, given); base != "" {
			out[i] = base
		}
	}
	return out
}

// resolvedLims names what tidying a list whose codes are all of form did:
// a stripped range (citation_range_unspecified) and repeated or surplus IDs
// (citation_list_trimmed).
func resolvedLims(codes []string) []string {
	var lims []string
	if slices.Contains(codes, errCitationInvalid) {
		lims = append(lims, limCitationUnspecified)
	}
	if slices.ContainsFunc(codes, func(c string) bool { return c != errCitationInvalid }) {
		lims = append(lims, limCitationListTrimmed)
	}
	return lims
}

func catalogOf(list []record.Excerpt) map[string]record.Excerpt {
	m := map[string]record.Excerpt{}
	for _, e := range list {
		m[e.ID] = e
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
