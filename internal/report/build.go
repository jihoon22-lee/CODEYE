package report

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// RequiredReviewTasks are the model review task keys every current profile
// requires (W11/W12 plan them). The requirement is met only when the DB task
// row succeeded and a registered decoder accepted that task's result bytes.
var RequiredReviewTasks = []string{"review.context", "review.investigate", "review.verify"}

var profiles = map[string]bool{"deep-review": true, "with-tests": true, "focused": true}

var (
	hex64       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:[.:][a-z0-9_.-]+)*$`)
)

// Limitation and note codes. Renderers translate known codes; JSON keeps codes.
const (
	LimAIReviewNotPerformed   = "ai_review_not_performed"
	LimAIReviewPartial        = "ai_review_partial"
	LimResultManifestMissing  = "result_manifest_unavailable"
	LimResultManifestRejected = "result_manifest_rejected"
	LimRequiredTaskIncomplete = "required_task_not_succeeded"
	LimCleanupUncertain       = "cleanup_uncertain"
	LimArtifactRejected       = "artifact_rejected"
	LimArtifactMissing        = "analysis_artifact_missing"
	LimUnrecognizedArtifact   = "unrecognized_artifact"
	LimNoCodeExcerpts         = "controller_holds_no_snapshot_bytes"
	LimTaskNotInManifest      = "task_not_in_result_manifest"
	LimTaskStatusMismatch     = "artifact_status_differs_from_accepted_task"
	LimRunCancelled           = "run_cancelled"
	LimRunFailed              = "run_failed"
	LimTestsNotPerformed      = "required_tests_not_performed"
	// LimStaticAnalysisGap (":<ecosystem>"): source of a language ecosystem
	// was in scope and the worker had no analyzer for it (a pack-gap task,
	// docs/39 §2.8). The report is partial; the gap is not a required task.
	LimStaticAnalysisGap = "static_analysis_gap"
	// LimStaticAnalysisNotPlanned: the run planned no analyzer task at all
	// (no supported language source in scope). The report is partial.
	LimStaticAnalysisNotPlanned = "static_analysis_not_planned"
	NoteInventoryAllLanguages   = "inventory_counts_all_included_files"
	NoteToolFilesUnknown        = "tool_processed_files_unknown"
	NoteNoTestsPlanned          = "tests_not_planned"
	NoteToolZeroIsNotClean      = "tool_reported_zero_is_not_clean"
)

type taskState struct {
	facts        TaskFacts
	manifest     *analysis.ManifestTask
	rejected     bool
	decoded      bool
	review       bool
	reviewResult *ReviewResult
	// decodedSHA is the SHA-256 of each accepted artifact by media type; an
	// R1 record binds its upstream inputs to these digests.
	decodedSHA map[string]string
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
	if r1Planned(states) {
		composeR1Reviews(&r, states, lim)
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
		if record.AnalyzerTaskKey(k) {
			analyzerPlanned = true
		}
		// A static analysis gap is visible and makes the report partial
		// without being counted as a required task (docs/39 §2.8).
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
	r.Coverage.Notes = notes.list()
	r.Limitations = lim.list()
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
	if in.ResultManifest != nil && !seenArt[in.ResultManifest.ID] {
		return invalidf("result manifest is not an artifact of this attempt")
	}
	return nil
}

// acceptResultManifest decodes and cross-checks the result manifest. A
// non-empty reason rejects it; the report then shows DB task rows only.
func acceptResultManifest(in Input, arts map[string]ArtifactRef, states map[string]*taskState) (analysis.ResultManifest, string) {
	var m analysis.ResultManifest
	a := arts[in.ResultManifest.ID]
	body, ok := in.Bodies[a.ID]
	if !ok || a.Role != "result_manifest" || a.MediaType != analysis.MediaResultManifest {
		return m, "unreadable"
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.More() || m.Validate() != nil {
		return m, "invalid"
	}
	if canon, err := domain.CanonicalJSON(body); err != nil || !bytes.Equal(canon, body) {
		return m, "not_canonical"
	}
	if m.RunID != in.Run.ID || m.AttemptID != in.Attempt.ID || m.LeaseEpoch != in.Attempt.Epoch {
		return m, "attempt_mismatch"
	}
	if m.Snapshot.ID != in.Snapshot.ID || m.Snapshot.ManifestDigest != in.Snapshot.ManifestDigest {
		return m, "snapshot_mismatch"
	}
	if m.CleanupState == "confirmed" && in.Attempt.Cleanup != "confirmed" {
		return m, "cleanup_mismatch"
	}
	for _, t := range m.Tasks {
		st, ok := states[t.Key]
		if !ok || st.facts.ID != t.TaskID || st.facts.Required != t.Required {
			return m, "task_mismatch"
		}
		// The accepted DB status is the authority; an artifact may never claim
		// success for a task the controller recorded otherwise.
		if t.Status == domain.TaskSucceeded && st.facts.Status != string(domain.TaskSucceeded) {
			return m, "task_status_upgrade"
		}
		if st.facts.ExecutionOrdinal < 1 && (len(t.Executions) > 0 || len(t.Artifacts) > 0) {
			return m, "task_execution_missing"
		}
		for _, e := range t.Executions {
			for role, id := range map[string]*string{"stdout": e.StdoutArtifactID, "stderr": e.StderrArtifactID} {
				if id == nil {
					continue
				}
				if x, ok := arts[*id]; !ok || x.Role != role {
					return m, "execution_artifact_unbound"
				}
			}
		}
		for _, ma := range t.Artifacts {
			x, ok := arts[ma.ArtifactID]
			if !ok || x.SHA256 != ma.SHA256 || x.SizeBytes != ma.SizeBytes || x.Role != ma.Role || x.MediaType != ma.MediaType {
				return m, "task_artifact_unbound"
			}
		}
	}
	return m, ""
}

func executionOf(ref ExecutionRef, key, texec string) Execution {
	e := ref.Manifest
	x := Execution{ID: ref.ID, TaskKey: key, TaskExecutionID: texec, Name: e.Name, Status: string(e.Status), ExitCode: e.ExitCode, Cleanup: string(e.Cleanup),
		Argv: append([]string{}, e.Command.Argv...), Cwd: e.Command.Dir, EnvKeys: append([]string{}, e.Command.EnvKeys...),
		StdoutArtifactID: e.StdoutArtifactID, StderrArtifactID: e.StderrArtifactID, StdoutTruncated: e.StdoutTruncated, StderrTruncated: e.StderrTruncated}
	if e.Reason != "" {
		reason := e.Reason
		x.Reason = &reason
	}
	if e.StartedAt != "" {
		s := e.StartedAt
		x.StartedAt = &s
	}
	if e.EndedAt != "" {
		s := e.EndedAt
		x.EndedAt = &s
	}
	return x
}

// checkContribution enforces the evidence rules on decoder output before it
// may enter the report.
func checkContribution(c Contribution, obsSeen, findSeen map[string]bool, arts map[string]ArtifactRef, index *SourceIndex, snapshot string, ctx DecodeContext) error {
	local := map[string]bool{}
	for _, o := range c.Observations {
		if o.ID == "" || obsSeen[o.ID] || local[o.ID] || o.TaskKey != ctx.Task.Key {
			return invalidf("observation id %q is empty or duplicated", o.ID)
		}
		if _, ok := arts[o.RawRef.ArtifactID]; !ok {
			return invalidf("observation raw_ref is not an attempt artifact")
		}
		if o.Location != nil && o.Location.SnapshotID != snapshot {
			return invalidf("observation location snapshot mismatch")
		}
		local[o.ID] = true
	}
	artIDs := map[string]bool{}
	for id := range arts {
		artIDs[id] = true
	}
	localFind := map[string]bool{}
	for _, f := range c.Findings {
		if f.ID == "" || findSeen[f.ID] || localFind[f.ID] {
			return invalidf("finding id %q is empty or duplicated", f.ID)
		}
		localFind[f.ID] = true
		if err := validateFinding(f, snapshot, local, artIDs); err != nil {
			return err
		}
		// Only a review decoder may produce anything but unreviewed findings.
		if c.Review == nil && f.Validation != "unreviewed" {
			return invalidf("tool-only finding must be unreviewed")
		}
		for _, id := range f.ObservationIDs {
			if !local[id] {
				return invalidf("finding references an unknown observation")
			}
		}
		for _, e := range f.Evidence {
			switch e.Kind {
			case "code", "document":
				if e.Location == nil || e.Location.SnapshotID != snapshot {
					return invalidf("code evidence without a snapshot location")
				}
				if sha, ok := index.Lookup(e.Location.Path); !ok || sha != e.BlobSHA256 {
					return invalidf("code evidence path/hash is not in the source manifest")
				}
			case "tool", "execution":
				if _, ok := arts[e.ArtifactID]; !ok || e.TaskExecutionID != ctx.TaskExecutionID {
					return invalidf("tool evidence is not bound to an attempt artifact")
				}
			}
		}
		for _, l := range f.Locations {
			if l.SnapshotID != snapshot {
				return invalidf("finding location snapshot mismatch")
			}
			if _, ok := index.Lookup(l.Path); !ok {
				return invalidf("finding location is not in the source manifest")
			}
		}
	}
	for _, l := range c.Limitations {
		if !codePattern.MatchString(l) {
			return invalidf("limitation must be a code")
		}
	}
	return nil
}

// requiredReviewKeys derives the review tasks the verdict needs from the
// planned tasks. A plan with a review-r1 role key requires exactly its
// review.* tasks with required_for_verdict; the others are optional. Any
// other plan (R0/review-v1 records, or no review task) is read with the three
// roles of the time, listing the ones not planned.
func requiredReviewKeys(states map[string]*taskState) (required, optional []string, r1 bool) {
	if !r1Planned(states) {
		return RequiredReviewTasks, nil, false
	}
	var keys []string
	for k := range states {
		if strings.HasPrefix(k, "review.") {
			keys = append(keys, k)
		}
	}
	order := map[string]int{}
	for i, stage := range record.R1Stages {
		order["review."+stage] = i
	}
	sort.Slice(keys, func(a, b int) bool {
		x, okx := order[keys[a]]
		y, oky := order[keys[b]]
		if okx != oky {
			return okx
		}
		if x != y {
			return x < y
		}
		return keys[a] < keys[b]
	})
	for _, k := range keys {
		if states[k].facts.Required {
			required = append(required, k)
		} else {
			optional = append(optional, k)
		}
	}
	return required, optional, true
}

// r1Planned reports a plan with a review-r1 role that review-v1 never plans.
func r1Planned(states map[string]*taskState) bool {
	for _, k := range []string{"review.environment", "review.documents", "review.interpret"} {
		if states[k] != nil {
			return true
		}
	}
	return false
}

func reviewItem(k string, st *taskState, r1 bool) (AIReviewTask, bool) {
	item := AIReviewTask{Key: k}
	if st == nil {
		item.Result = "not_planned"
		return item, false
	}
	s := st.facts.Status
	item.TaskStatus = &s
	if r1 && st.facts.Reason != nil {
		reason := *st.facts.Reason
		item.Reason = &reason
	}
	switch {
	case r1 && st.facts.Status == string(domain.TaskSkippedPolicy):
		item.Result = "skipped"
	case st.facts.Status != string(domain.TaskSucceeded):
		item.Result = "not_succeeded"
	case st.rejected:
		item.Result = "result_rejected"
	case !st.review:
		item.Result = "result_missing"
	default:
		item.Result = "accepted"
		return item, true
	}
	return item, false
}

func decideAIReview(states map[string]*taskState) AIReview {
	out := AIReview{RequiredTasks: []AIReviewTask{}}
	required, optional, r1 := requiredReviewKeys(states)
	accepted, planned := 0, 0
	for _, k := range required {
		item, ok := reviewItem(k, states[k], r1)
		if states[k] != nil {
			planned++
		}
		if ok {
			accepted++
		}
		out.RequiredTasks = append(out.RequiredTasks, item)
	}
	for _, k := range optional {
		item, _ := reviewItem(k, states[k], r1)
		out.OptionalTasks = append(out.OptionalTasks, item)
	}
	reason := func(s string) *string { return &s }
	switch {
	case len(required) > 0 && accepted == len(required):
		out.Status = AIReviewPerformed
	case accepted > 0:
		out.Status, out.Reason = AIReviewPartial, reason("required_review_incomplete")
	case planned == 0:
		out.Status, out.Reason = AIReviewNotPerformed, reason("review_tasks_not_planned")
	default:
		out.Status, out.Reason = AIReviewNotPerformed, reason("review_results_not_accepted")
	}
	return out
}

// decideStatus maps structured facts to the report status. final is the run's
// terminal status for a report-only publication (failed/cancelled).
func decideStatus(final string, readable, incomplete bool, ai string) string {
	switch {
	case final == StatusCancelled:
		return StatusCancelled
	case final == StatusFailed, !readable:
		return StatusFailed
	case incomplete || ai != AIReviewPerformed:
		return StatusPartial
	}
	return StatusCompleted
}

// DecideVerdict: the first matching rule wins. A clean verdict needs a
// completed report, performed AI review and no pending candidate.
func DecideVerdict(status string, s Summary, ai string) string {
	switch {
	case s.ActionableFindings > 0:
		return VerdictAttention
	case status != StatusCompleted, ai != AIReviewPerformed, s.PendingCandidates > 0, s.UnresolvedObservations > 0:
		return VerdictInconclusive
	}
	return VerdictNoActionable
}

var hiddenDisposition = map[string]bool{"false_positive": true, "suppressed": true, "accepted_risk": true, "fixed_by_user": true}
var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

// ComputeSummary is the single source of every count a format shows.
func ComputeSummary(findings []Finding, obs []Observation) Summary {
	s := Summary{TotalFindings: len(findings), RawObservations: len(obs), Priorities: []string{}}
	for _, o := range obs {
		if o.EvidenceState != EvidenceVerified {
			s.UnresolvedObservations++
		}
	}
	type cand struct {
		f    Finding
		rank int
	}
	var cands []cand
	for _, f := range findings {
		if hiddenDisposition[f.Disposition] {
			s.HiddenFindings++
		}
		switch f.Validation {
		case "supported", "reproduced":
			if !hiddenDisposition[f.Disposition] {
				s.ActionableFindings++
				cands = append(cands, cand{f, 0})
			}
		case "unreviewed", "inconclusive":
			s.PendingCandidates++
			if !hiddenDisposition[f.Disposition] {
				cands = append(cands, cand{f, 1})
			}
		case "refuted":
			s.RefutedFindings++
		}
	}
	sort.SliceStable(cands, func(a, b int) bool {
		x, y := cands[a], cands[b]
		if x.rank != y.rank {
			return x.rank < y.rank
		}
		if severityRank[x.f.Severity] != severityRank[y.f.Severity] {
			return severityRank[x.f.Severity] < severityRank[y.f.Severity]
		}
		return findingKey(x.f) < findingKey(y.f)
	})
	for i := 0; i < len(cands) && i < 5; i++ {
		s.Priorities = append(s.Priorities, cands[i].f.ID)
	}
	return s
}

func findingKey(f Finding) string {
	if len(f.Locations) == 0 {
		return "\xff" + f.ID
	}
	l := f.Locations[0]
	return l.Path + "\x00" + pad(l.StartLine) + "\x00" + f.ID
}

func sortReport(r *Report) {
	sort.SliceStable(r.Executions, func(a, b int) bool { return r.Executions[a].ID < r.Executions[b].ID })
	sort.SliceStable(r.Observations, func(a, b int) bool { return obsKey(r.Observations[a]) < obsKey(r.Observations[b]) })
	sort.SliceStable(r.Findings, func(a, b int) bool { return findingKey(r.Findings[a]) < findingKey(r.Findings[b]) })
	sort.SliceStable(r.Coverage.Tools, func(a, b int) bool { return r.Coverage.Tools[a].TaskKey < r.Coverage.Tools[b].TaskKey })
}

func obsKey(o Observation) string {
	p, line := "\xff", 0
	if o.Location != nil {
		p, line = o.Location.Path, o.Location.StartLine
	}
	return o.TaskKey + "\x00" + p + "\x00" + pad(line) + "\x00" + o.RuleID + "\x00" + o.ID
}

func sortedArtifacts(in []ArtifactRef) []ArtifactRef {
	out := append([]ArtifactRef(nil), in...)
	order := map[string]int{"source_manifest": 0, "result_manifest": 1, "observations": 2, "stdout": 3, "stderr": 4, "raw_output": 5, "other": 6}
	sort.SliceStable(out, func(a, b int) bool {
		if order[out[a].Role] != order[out[b].Role] {
			return order[out[a].Role] < order[out[b].Role]
		}
		return out[a].ID < out[b].ID
	})
	return out
}

func pad(n int) string {
	s := "000000000000" + itoa(n)
	return s[len(s)-12:]
}

func itoa(n int) string {
	if n < 0 {
		return "-"
	}
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
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

type stringSet struct {
	m map[string]bool
}

func newSet() *stringSet               { return &stringSet{m: map[string]bool{}} }
func (s *stringSet) add(v string)      { s.m[v] = true }
func (s *stringSet) has(v string) bool { return s.m[v] }
func (s *stringSet) list() []string {
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
