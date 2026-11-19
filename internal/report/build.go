package report

import (
	"regexp"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// RequiredReviewTasks are the model review task keys every current profile
// requires (the legacy review handler plans them). The requirement is met only when the DB task
// row succeeded and a registered decoder accepted that task's result bytes.
var RequiredReviewTasks = []string{"review.context", "review.investigate", "review.verify"}

var profiles = map[string]bool{"deep-review": true, "with-tests": true, "focused": true}

var (
	hex64       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:[.:][a-z0-9_.-]+)*$`)
)

type taskState struct {
	facts        TaskFacts
	manifest     *analysis.ManifestTask
	rejected     bool
	decoded      bool
	review       bool
	reviewResult *ReviewResult
	// decodedSHA is the SHA-256 of each accepted artifact by media type; an
	// role record binds its upstream inputs to these digests.
	decodedSHA map[string]string
	// What the sections read of the accepted contributions: the analyzer
	// identity, the tool row, the discovery document and the IDs of the tool
	// findings this task's artifacts produced.
	analyzer     *AnalyzerIdentity
	tool         *ToolCoverage
	discovery    *envdoc.Document
	toolFindings []string
	// scope is the accepted review scope artifact (review.scope).
	scope *record.ScopeDoc
	// dependencies is the accepted record of a deps.* task.
	dependencies *depdoc.Record
}

// Build validates the input and produces the report. Every refusal wraps
// ErrInvalidInput. An invalid result manifest is not a refusal: the report is
// built from DB task rows only and records the rejection.
func Build(in Input, reg *Registry) (Report, error) {
	if err := checkInput(in); err != nil {
		return Report{}, err
	}
	manifest, err := source.DecodeManifest(in.SourceManifest)
	if err != nil {
		return Report{}, invalidf("source manifest is invalid")
	}
	index := &SourceIndex{entries: map[string]string{}}
	for _, e := range manifest.Entries {
		if (e.Kind == "file" || e.Kind == "lfs_pointer") && e.ExcludedReason == "" {
			index.entries[e.Path] = e.SHA256
			index.files++
		}
	}
	lim := newSet()
	notes := newSet()
	r := Report{
		SchemaVersion: SchemaVersion, ID: in.ReportID, RunID: in.Run.ID, Revision: in.Revision, CreatedAt: formatTime(in.Now),
		Builder: Builder{ID: BuilderID, Version: BuilderVersion, InputDigest: InputDigest(in)},
		Export:  Export{IncludeCode: true},
		Inputs: Inputs{SnapshotID: in.Snapshot.ID, ManifestSHA256: in.Snapshot.ManifestDigest, CommitSHA: in.Snapshot.SourceSHA, Profile: in.Run.Profile,
			SourceKind: sourceKind(in.Snapshot.Consistency), Description: sourceDescription(in.Snapshot.Consistency)},
		Attempt: Attempt{ID: in.Attempt.ID, Number: in.Attempt.Number, LeaseEpoch: in.Attempt.Epoch, CompletionStatus: in.Attempt.CompletionStatus, CleanupState: in.Attempt.Cleanup, Reason: in.Attempt.Reason},
		Tasks:   []Task{}, Executions: []Execution{}, Artifacts: []Artifact{}, Observations: []Observation{}, Findings: []Finding{},
	}
	r.Coverage = Coverage{ScopeMode: in.Run.ScopeKind, InventoryFiles: index.files, TestStatus: "not_run", Tools: []ToolCoverage{}}
	notes.add(NoteInventoryAllLanguages)
	notes.add(NoteNoTestsPlanned)
	lim.add(LimNoCodeExcerpts)

	states := map[string]*taskState{}
	keys := []string{}
	for _, t := range in.Tasks {
		states[t.Key] = &taskState{facts: t}
		keys = append(keys, t.Key)
	}
	sort.Strings(keys)
	artByID := map[string]ArtifactRef{}
	for _, a := range in.Artifacts {
		artByID[a.ID] = a
	}

	var rm *analysis.ResultManifest
	if in.ResultManifest == nil {
		lim.add(LimResultManifestMissing)
	} else if m, reason := acceptResultManifest(in, artByID, states); reason != "" {
		lim.add(LimResultManifestRejected + ":" + reason)
	} else {
		rm = &m
	}

	artifactTask := map[string]string{}
	if rm != nil {
		for i := range rm.Tasks {
			mt := &rm.Tasks[i]
			st := states[mt.Key]
			st.manifest = mt
			if string(mt.Status) != st.facts.Status {
				lim.add(LimTaskStatusMismatch + ":" + mt.Key)
			}
			for _, e := range mt.Executions {
				for _, id := range []*string{e.StdoutArtifactID, e.StderrArtifactID} {
					if id != nil {
						artifactTask[*id] = mt.Key
					}
				}
			}
			for _, a := range mt.Artifacts {
				artifactTask[a.ArtifactID] = mt.Key
			}
		}
		for _, k := range keys {
			if states[k].manifest == nil {
				lim.add(LimTaskNotInManifest + ":" + k)
			}
		}
	}

	for _, k := range keys {
		t := states[k].facts
		r.Tasks = append(r.Tasks, Task{ID: t.ID, Key: t.Key, Status: t.Status, RequiredForVerdict: t.Required, Reason: t.Reason})
	}

	// Artifacts of the attempt, bound by id/sha/size/role (never embedded).
	decoderOf := map[string]string{}
	processedUnion, unionComplete := map[string]bool{}, true
	obsSeen := map[string]bool{}
	findSeen := map[string]bool{}
	if rm != nil {
		for _, k := range keys {
			st := states[k]
			if st.manifest == nil {
				continue
			}
			ctx := DecodeContext{RunID: in.Run.ID, AttemptID: in.Attempt.ID, Snapshot: in.Snapshot, Task: st.facts, TaskExecutionID: TaskExecutionID(in.Attempt.ID, st.facts.ID, st.facts.ExecutionOrdinal), Executions: map[string]ExecutionRef{}, Source: index}
			for i, e := range st.manifest.Executions {
				id := k + "/" + e.Name
				if e.Name == "unreported" {
					id += "/" + itoa(i+1)
				}
				ref := ExecutionRef{ID: id, Manifest: e}
				if e.StdoutArtifactID != nil {
					a := artByID[*e.StdoutArtifactID]
					ref.Stdout = &a
				}
				if e.StderrArtifactID != nil {
					a := artByID[*e.StderrArtifactID]
					ref.Stderr = &a
				}
				if e.Name != "unreported" {
					ctx.Executions[e.Name] = ref
				}
				r.Executions = append(r.Executions, executionOf(ref, k, ctx.TaskExecutionID))
			}
			for _, ma := range st.manifest.Artifacts {
				art := artByID[ma.ArtifactID]
				body := in.Bodies[art.ID]
				d, ok := reg.lookup(art, body)
				if !ok {
					if strings.HasSuffix(art.MediaType, "+json") {
						if body == nil && reg.Wants(art.MediaType) {
							lim.add(LimArtifactMissing + ":" + k)
						} else {
							lim.add(LimUnrecognizedArtifact + ":" + k)
						}
					}
					continue
				}
				c, err := d.Decode(ctx, art, body)
				if err == nil {
					err = checkContribution(c, obsSeen, findSeen, artByID, index, in.Snapshot.ID, ctx)
				}
				if err != nil {
					st.rejected = true
					lim.add(LimArtifactRejected + ":" + k)
					continue
				}
				decoderOf[art.ID] = d.Key().String()
				st.decoded = true
				if st.decodedSHA == nil {
					st.decodedSHA = map[string]string{}
				}
				st.decodedSHA[art.MediaType] = art.SHA256
				if c.Review != nil {
					if st.reviewResult != nil {
						st.rejected = true
						lim.add(LimArtifactRejected + ":" + k)
					}
					st.reviewResult = c.Review
					st.review = c.Review.Accepted
				}
				for _, o := range c.Observations {
					obsSeen[o.ID] = true
				}
				for _, f := range c.Findings {
					findSeen[f.ID] = true
					st.toolFindings = append(st.toolFindings, f.ID)
				}
				if c.Analyzer != nil {
					st.analyzer = c.Analyzer
				}
				if c.Tool != nil {
					tool := *c.Tool
					st.tool = &tool
				}
				if c.Discovery != nil {
					st.discovery = c.Discovery
				}
				if c.Scope != nil {
					st.scope = c.Scope
				}
				if c.Dependencies != nil {
					st.dependencies = c.Dependencies
				}
				r.Observations = append(r.Observations, c.Observations...)
				r.Findings = append(r.Findings, c.Findings...)
				if c.Tool != nil {
					r.Coverage.Tools = append(r.Coverage.Tools, *c.Tool)
					// tool_processed_files is the union of the analyzers'
					// processed paths when every counted task gave them (a
					// known zero needs none); otherwise the sum.
					switch pf, pp := c.Tool.ProcessedFiles, c.Tool.ProcessedPaths; {
					case pf == nil || *pf == 0 && len(pp) == 0:
					case len(pp) != *pf:
						unionComplete = false
					default:
						for _, p := range pp {
							processedUnion[p] = true
						}
					}
				}
				for _, l := range c.Limitations {
					lim.add(l)
				}
			}
		}
	}
	for _, a := range sortedArtifacts(in.Artifacts) {
		item := Artifact{ID: a.ID, Role: a.Role, MediaType: a.MediaType, SHA256: a.SHA256, SizeBytes: a.SizeBytes}
		if k, ok := artifactTask[a.ID]; ok {
			kk := k
			item.TaskKey = &kk
		}
		if d, ok := decoderOf[a.ID]; ok {
			dd := d
			item.Decoder = &dd
		}
		r.Artifacts = append(r.Artifacts, item)
	}
	if rolePackPlanned(states) {
		composeRoleReviews(&r, states, lim)
	} else {
		composeReviews(&r, states, lim)
	}
	applyDispositions(&r, in.Reviews, in.Now)
	sortReport(&r)

	// Coverage: processed files only when every analyzer task produced a known count.
	processed, known := 0, len(r.Coverage.Tools) > 0
	for _, tc := range r.Coverage.Tools {
		if tc.ProcessedFiles == nil {
			known = false
			break
		}
		processed += *tc.ProcessedFiles
	}
	for _, k := range keys {
		st := states[k]
		if !strings.HasPrefix(k, "review.") && !st.decoded {
			known = false
		}
	}
	if known && unionComplete {
		// Several analyzers read the same files: count each snapshot file once.
		processed = len(processedUnion)
	}
	if known {
		r.Coverage.ToolProcessedFiles = &processed
	} else {
		notes.add(NoteToolFilesUnknown)
	}
	for _, tc := range r.Coverage.Tools {
		if tc.ExitClass == "success_without_findings" {
			notes.add(NoteToolZeroIsNotClean)
		}
	}

	r.Coverage.AIReview = decideAIReview(states)
	reviewPlanned := false
	for _, k := range keys {
		if strings.HasPrefix(k, "review.") {
			reviewPlanned = true
		}
	}
	if !reviewPlanned {
		zero := 0
		r.Coverage.LLMExposedFiles = &zero // no model task was planned in this run
	}
	switch r.Coverage.AIReview.Status {
	case AIReviewNotPerformed:
		lim.add(LimAIReviewNotPerformed)
	case AIReviewPartial:
		lim.add(LimAIReviewPartial)
	}

	// Status from structured facts only.
	readable, requiredMissing, incompleteResult := false, false, false
	analyzerPlanned := false
	for _, k := range keys {
		st := states[k]
		// The secrets task screens model inputs and is planned for a
		// snapshot of documents alone; it is no static analysis of the code.
		if record.AnalyzerTaskKey(k) && !obsdoc.IsSecretTask(k) {
			analyzerPlanned = true
		}
		// A static analysis gap is visible and makes the report partial
		// without being counted as a required task (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백).
		if pack.Owns(pack.GapName, k) && st.facts.Reason != nil && *st.facts.Reason == pack.ReasonAnalyzerPackMissing {
			lim.add(LimStaticAnalysisGap + ":" + strings.TrimPrefix(k, pack.GapName+"."))
			incompleteResult = true
		}
		if st.facts.Status == string(domain.TaskSucceeded) && rm != nil && !st.rejected {
			readable = true
		}
		if st.facts.Required && st.facts.Status != string(domain.TaskSucceeded) {
			requiredMissing = true
		}
		if st.facts.Required && st.facts.Status == string(domain.TaskSucceeded) && !st.decoded {
			lim.add(LimArtifactMissing + ":" + k)
			incompleteResult = true
		}
		if st.rejected {
			incompleteResult = true
		}
	}
	if requiredMissing {
		lim.add(LimRequiredTaskIncomplete)
	}
	if !analyzerPlanned {
		lim.add(LimStaticAnalysisNotPlanned)
		incompleteResult = true
	}
	if in.Attempt.Cleanup != "confirmed" || (rm != nil && rm.CleanupState != "confirmed") {
		lim.add(LimCleanupUncertain)
	}
	if in.Run.Profile == "with-tests" && r.Coverage.TestStatus == "not_run" {
		lim.add(LimTestsNotPerformed)
		incompleteResult = true
	}
	r.Status = decideStatus(in.Run.FinalStatus, readable, requiredMissing || incompleteResult || lim.has(LimCleanupUncertain), r.Coverage.AIReview.Status)
	switch r.Status {
	case StatusCancelled:
		lim.add(LimRunCancelled)
	case StatusFailed:
		lim.add(LimRunFailed)
	}
	r.Summary = ComputeSummary(r.Findings, r.Observations)
	r.QualityVerdict = DecideVerdict(r.Status, r.Summary, r.Coverage.AIReview.Status)
	if in.PullRequest != nil {
		notes.add(NotePullRequestFocus)
	}
	r.Coverage.Notes = notes.list()
	r.Limitations = lim.list()
	// The sections read the finished report and the task states; they never
	// change a limitation, note, count, status or verdict.
	composeSections(&r, in, states, keys, rm != nil)
	r.PullRequest = pullRequestSection(&r, in.PullRequest, index.entries, sentPaths(states, roleRecordKeys(states)), reviewPlanned)
	if err := ValidateReport(r); err != nil {
		return Report{}, err
	}
	return r, nil
}

func checkInput(in Input) error {
	if !domain.ValidID(in.ReportID) || !domain.ValidID(in.Run.ID) || !domain.ValidID(in.Snapshot.ID) || !domain.ValidID(in.Attempt.ID) || in.Revision < 1 || in.Now.IsZero() {
		return invalidf("report, run, snapshot and attempt ids, revision and time are required")
	}
	if !profiles[in.Run.Profile] {
		return invalidf("unknown profile")
	}
	if in.Run.ScopeKind != "full" && in.Run.ScopeKind != "paths" {
		return invalidf("unknown scope kind")
	}
	switch in.Run.FinalStatus {
	case "", StatusFailed, StatusCancelled:
	default:
		return invalidf("final run status must be empty, failed or cancelled")
	}
	if !hex64.MatchString(in.Snapshot.ManifestDigest) || domain.Digest(in.SourceManifest) != in.Snapshot.ManifestDigest {
		return invalidf("source manifest bytes do not match the snapshot digest")
	}
	if in.Attempt.Epoch < 1 || in.Attempt.Number < 1 || (in.Attempt.Cleanup != "confirmed" && in.Attempt.Cleanup != "uncertain") {
		return invalidf("attempt facts are incomplete")
	}
	seenID, seenKey := map[string]bool{}, map[string]bool{}
	for _, t := range in.Tasks {
		if !domain.ValidID(t.ID) || !analysis.ValidTaskKey(t.Key) || seenID[t.ID] || seenKey[t.Key] {
			return invalidf("task %q is invalid or duplicated", t.Key)
		}
		seenID[t.ID], seenKey[t.Key] = true, true
		if st := domain.TaskStatus(t.Status); !st.Valid() || !st.Terminal() {
			return invalidf("task %s is not terminal", t.Key)
		}
		if t.Stage != "" && !domain.Stage(t.Stage).Valid() {
			return invalidf("task %s stage is unknown", t.Key)
		}
	}
	seenArt := map[string]bool{}
	for _, a := range in.Artifacts {
		if !domain.ValidID(a.ID) || seenArt[a.ID] || a.RunID != in.Run.ID || a.AttemptID != in.Attempt.ID || !hex64.MatchString(a.SHA256) || a.SizeBytes < 0 || a.Role == "" || a.MediaType == "" {
			return invalidf("artifact %s is not a finalized artifact of this attempt", a.ID)
		}
		seenArt[a.ID] = true
		if body, ok := in.Bodies[a.ID]; ok && (domain.Digest(body) != a.SHA256 || int64(len(body)) != a.SizeBytes) {
			return invalidf("artifact %s bytes do not match", a.ID)
		}
	}
	for id := range in.Bodies {
		if !seenArt[id] {
			return invalidf("body for an unknown artifact")
		}
	}
	if err := checkReviews(in.Reviews); err != nil {
		return err
	}
	if in.PullRequest != nil && in.PullRequest.Validate() != nil {
		return invalidf("pull request pin is invalid")
	}
	if in.ResultManifest != nil && !seenArt[in.ResultManifest.ID] {
		return invalidf("result manifest is not an artifact of this attempt")
	}
	return nil
}

func sourceKind(consistency string) string {
	if consistency == "git_commit" {
		return "git"
	}
	return "local"
}

func sourceDescription(consistency string) string {
	if consistency == "git_commit" {
		return "Git 커밋으로 고정한 snapshot이다."
	}
	return "승인된 로컬 경로를 복사해 고정한 snapshot이다(안정 복사 best effort)."
}
