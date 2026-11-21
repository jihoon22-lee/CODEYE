package report

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// fixture builds builder input around the real go vet observations
// document (examples/observations-go-vet.json, captured from go1.27.4). Only
// the snapshot binding is re-pointed to a source manifest made for the test.
type fixture struct {
	in      Input
	doc     map[string]any
	docArt  string
	stdout  map[string]string // exec name -> artifact id
	taskID  string
	runID   string
	attempt string
}

func readDoc(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../examples/observations-go-vet.json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func canonical(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sourceManifest(t *testing.T, doc map[string]any, mismatch string) []byte {
	t.Helper()
	paths := map[string]string{}
	for _, o := range doc["observations"].([]any) {
		loc, _ := o.(map[string]any)["location"].(map[string]any)
		if loc != nil {
			sha := loc["blob_sha256"].(string)
			if loc["path"] == mismatch {
				sha = strings.Repeat("0", 64)
			}
			paths[loc["path"].(string)] = sha
		}
	}
	paths["go.mod"] = strings.Repeat("a", 64)
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sortStrings(keys)
	entries := []any{}
	for _, k := range keys {
		entries = append(entries, map[string]any{"path": k, "kind": "file", "size_bytes": 10, "mode": 420, "sha256": paths[k], "encoding": "utf-8"})
	}
	return canonical(t, map[string]any{"schema_version": "0.1", "kind": "local", "consistency": "stable_copy_best_effort", "source_fixed_at": "2026-11-18T00:00:00Z", "entries": entries, "limitations": []any{"local_copy_is_not_atomic"}})
}

type taskSpec struct {
	key, status string
	required    bool
	doc         map[string]any // nil: no observations artifact
}

func newFixture(t *testing.T, tasks ...taskSpec) *fixture {
	t.Helper()
	f := &fixture{runID: domain.NewID(), attempt: domain.NewID(), stdout: map[string]string{}}
	src := sourceManifest(t, readDoc(t), "")
	digest := domain.Digest(src)
	snap := domain.NewID()
	f.in = Input{ReportID: domain.NewID(), Revision: 1, Now: time.Date(2026, 11, 18, 1, 2, 3, 0, time.UTC),
		Run:            RunFacts{ID: f.runID, ProjectID: domain.NewID(), Profile: "deep-review", ScopeKind: "full"},
		Snapshot:       SnapshotFacts{ID: snap, ManifestDigest: digest, Consistency: "stable_copy_best_effort"},
		Attempt:        AttemptFacts{ID: f.attempt, Number: 1, Epoch: 1, CompletionStatus: "finalizing", Cleanup: "confirmed"},
		SourceManifest: src, Bodies: map[string][]byte{}}
	art := func(role, media string, body []byte, sha string, size int64) ArtifactRef {
		a := ArtifactRef{ID: domain.NewID(), RunID: f.runID, AttemptID: f.attempt, Role: role, MediaType: media, SHA256: sha, SizeBytes: size}
		if body != nil {
			a.SHA256, a.SizeBytes = domain.Digest(body), int64(len(body))
			f.in.Bodies[a.ID] = body
		}
		f.in.Artifacts = append(f.in.Artifacts, a)
		return a
	}
	art("source_manifest", "application/vnd.codeye.snapshot-manifest+json", src, "", 0)
	delete(f.in.Bodies, f.in.Artifacts[0].ID)
	rm := analysis.ResultManifest{SchemaVersion: "0.1", RunID: f.runID, AttemptID: f.attempt, LeaseEpoch: 1, Snapshot: analysis.ManifestSnapshot{ID: snap, ManifestDigest: digest},
		Handlers: []analysis.ManifestHandler{{Name: "go-vet", Version: "1", Capability: "analysis:deep-review"}}, CleanupState: "confirmed", Limitations: []string{}}
	for _, ts := range tasks {
		id := domain.NewID()
		if f.taskID == "" {
			f.taskID = id
		}
		f.in.Tasks = append(f.in.Tasks, TaskFacts{ID: id, Key: ts.key, Status: ts.status, Required: ts.required, ExecutionOrdinal: 1})
		mt := analysis.ManifestTask{TaskID: id, Key: ts.key, Required: ts.required, Status: domain.TaskStatus(ts.status), Executions: []analysis.ManifestExecution{}, Artifacts: []analysis.ManifestArtifact{}, Limitations: []string{}}
		if ts.doc != nil {
			ts.doc["task_key"] = ts.key
			ts.doc["snapshot_manifest_digest"] = digest
			for _, e := range ts.doc["executions"].([]any) {
				ex := e.(map[string]any)
				name := ex["name"].(string)
				so, se := ex["stdout"].(map[string]any), ex["stderr"].(map[string]any)
				sout := art("stdout", "text/plain", nil, so["sha256"].(string), int64(so["captured_bytes"].(float64)))
				serr := art("stderr", "text/plain", nil, se["sha256"].(string), int64(se["captured_bytes"].(float64)))
				if ts.key == tasks[0].key {
					f.stdout[name] = sout.ID
				}
				code := int(ex["exit_code"].(float64))
				cmd := ex["command"].(map[string]any)
				argv := []string{}
				for _, a := range cmd["argv"].([]any) {
					argv = append(argv, a.(string))
				}
				envKeys := []string{}
				for _, a := range cmd["env_keys"].([]any) {
					envKeys = append(envKeys, a.(string))
				}
				mt.Executions = append(mt.Executions, analysis.ManifestExecution{Name: name, Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
					Command: analysis.ManifestCommand{Argv: argv, Dir: ".", EnvKeys: envKeys}, StdoutArtifactID: &sout.ID, StderrArtifactID: &serr.ID,
					StartedAt: "2026-11-18T00:00:00Z", EndedAt: "2026-11-18T00:00:01Z"})
			}
			body := canonical(t, ts.doc)
			d := art("observations", analysis.MediaObservations, body, "", 0)
			if ts.key == tasks[0].key {
				f.docArt, f.doc = d.ID, ts.doc
			}
			mt.Artifacts = append(mt.Artifacts, analysis.ManifestArtifact{ArtifactID: d.ID, Role: d.Role, MediaType: d.MediaType, SHA256: d.SHA256, SizeBytes: d.SizeBytes})
		}
		rm.Tasks = append(rm.Tasks, mt)
	}
	body, err := rm.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	m := art("result_manifest", analysis.MediaResultManifest, body, "", 0)
	f.in.ResultManifest = &m
	return f
}

// rewriteDoc replaces the first task's observations document bytes and the
// manifest entry that binds them.
func (f *fixture) rewriteDoc(t *testing.T, edit func(map[string]any)) {
	t.Helper()
	edit(f.doc)
	body := canonical(t, f.doc)
	for i := range f.in.Artifacts {
		if f.in.Artifacts[i].ID == f.docArt {
			f.in.Artifacts[i].SHA256, f.in.Artifacts[i].SizeBytes = domain.Digest(body), int64(len(body))
		}
	}
	f.in.Bodies[f.docArt] = body
	var rm analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm); err != nil {
		t.Fatal(err)
	}
	for i := range rm.Tasks[0].Artifacts {
		if rm.Tasks[0].Artifacts[i].ArtifactID == f.docArt {
			rm.Tasks[0].Artifacts[i].SHA256, rm.Tasks[0].Artifacts[i].SizeBytes = domain.Digest(body), int64(len(body))
		}
	}
	f.setManifest(t, rm)
}

func (f *fixture) setManifest(t *testing.T, rm analysis.ResultManifest) {
	t.Helper()
	raw, err := json.Marshal(rm)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := domain.CanonicalJSON(raw)
	for i := range f.in.Artifacts {
		if f.in.Artifacts[i].ID == f.in.ResultManifest.ID {
			f.in.Artifacts[i].SHA256, f.in.Artifacts[i].SizeBytes = domain.Digest(body), int64(len(body))
			*f.in.ResultManifest = f.in.Artifacts[i]
		}
	}
	f.in.Bodies[f.in.ResultManifest.ID] = body
}

func goVet(t *testing.T) taskSpec {
	return taskSpec{key: "go-vet", status: "succeeded", required: true, doc: readDoc(t)}
}

func build(t *testing.T, in Input, reg *Registry) Report {
	t.Helper()
	r, err := Build(in, reg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v || strings.HasPrefix(x, v+":") {
			return true
		}
	}
	return false
}

// TC-80: real go vet observations become bound observations and unreviewed
// findings; the report is partial and inconclusive because AI review is absent.
func TestBuildFromRealGoVetDocument(t *testing.T) {
	f := newFixture(t, goVet(t))
	r := build(t, f.in, DefaultRegistry())
	if r.Status != StatusPartial || r.QualityVerdict != VerdictInconclusive || r.Coverage.AIReview.Status != AIReviewNotPerformed {
		t.Fatalf("status %s verdict %s ai %s", r.Status, r.QualityVerdict, r.Coverage.AIReview.Status)
	}
	if *r.Coverage.AIReview.Reason != "review_tasks_not_planned" || !has(r.Limitations, LimAIReviewNotPerformed) {
		t.Fatalf("ai review reason %v limitations %v", *r.Coverage.AIReview.Reason, r.Limitations)
	}
	if r.Summary.RawObservations != 5 || r.Summary.UnresolvedObservations != 0 || r.Summary.TotalFindings != 5 || r.Summary.PendingCandidates != 5 || r.Summary.ActionableFindings != 0 {
		t.Fatalf("summary %+v", r.Summary)
	}
	for _, o := range r.Observations {
		if o.RawRef.ArtifactID != f.stdout["go.vet"] || o.EvidenceState != EvidenceVerified || o.TaskExecutionID != TaskExecutionID(f.attempt, f.taskID, 1) {
			t.Fatalf("observation not bound to the task's go.vet stdout: %+v", o)
		}
	}
	for _, x := range r.Findings {
		if x.Validation != "unreviewed" || x.Severity != "low" || len(x.ObservationIDs) == 0 {
			t.Fatalf("tool finding %+v", x)
		}
	}
	if r.Coverage.InventoryFiles != 4 || r.Coverage.ToolProcessedFiles == nil || *r.Coverage.ToolProcessedFiles != 4 || r.Coverage.LLMExposedFiles == nil || *r.Coverage.LLMExposedFiles != 0 {
		t.Fatalf("coverage %+v", r.Coverage)
	}
	if len(r.Executions) != 4 || len(r.Artifacts) != 11 {
		t.Fatalf("executions %d artifacts %d", len(r.Executions), len(r.Artifacts))
	}
	for _, a := range r.Artifacts {
		if a.ID == f.docArt && (a.Decoder == nil || a.TaskKey == nil || *a.TaskKey != "go-vet") {
			t.Fatalf("observations artifact %+v", a)
		}
	}
	if _, err := Marshal(r); err != nil {
		t.Fatal(err)
	}
}

// TC-82: same input -> same bytes; the input digest ignores id/revision/time.
func TestBuildDeterministic(t *testing.T) {
	f := newFixture(t, goVet(t))
	a, _ := Marshal(build(t, f.in, DefaultRegistry()))
	b, _ := Marshal(build(t, f.in, DefaultRegistry()))
	if string(a) != string(b) {
		t.Fatal("same input produced different bytes")
	}
	other := f.in
	other.ReportID, other.Revision, other.Now = domain.NewID(), 2, f.in.Now.Add(time.Hour)
	if InputDigest(other) != InputDigest(f.in) {
		t.Fatal("input digest depends on report id, revision or time")
	}
	back, err := Unmarshal(a)
	if err != nil || !reflect.DeepEqual(back, build(t, f.in, DefaultRegistry())) {
		t.Fatalf("round trip: %v", err)
	}
}

type fakeReview struct{ accept bool }

func (fakeReview) Key() DecoderKey {
	return DecoderKey{MediaType: "application/vnd.codeye.review+json", SchemaVersion: "0.2"}
}
func (d fakeReview) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if !d.accept {
		return Contribution{}, errors.New("rejected")
	}
	return Contribution{Review: &ReviewResult{Accepted: true}}, nil
}

func withReview(t *testing.T, statuses map[string]string) *fixture {
	t.Helper()
	specs := []taskSpec{goVet(t)}
	for _, k := range []string{"review.investigate", "review.verify"} {
		if s, ok := statuses[k]; ok {
			specs = append(specs, taskSpec{key: k, status: s, required: true})
		}
	}
	f := newFixture(t, specs...)
	// Attach a review document to each review task.
	var rm analysis.ResultManifest
	_ = json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm)
	for i := range rm.Tasks {
		if !strings.HasPrefix(rm.Tasks[i].Key, "review.") {
			continue
		}
		body := canonical(t, map[string]any{"schema_version": "0.2", "snapshot_manifest_digest": f.in.Snapshot.ManifestDigest})
		a := ArtifactRef{ID: domain.NewID(), RunID: f.runID, AttemptID: f.attempt, Role: "other", MediaType: "application/vnd.codeye.review+json", SHA256: domain.Digest(body), SizeBytes: int64(len(body))}
		f.in.Artifacts = append(f.in.Artifacts, a)
		f.in.Bodies[a.ID] = body
		rm.Tasks[i].Artifacts = append(rm.Tasks[i].Artifacts, analysis.ManifestArtifact{ArtifactID: a.ID, Role: a.Role, MediaType: a.MediaType, SHA256: a.SHA256, SizeBytes: a.SizeBytes})
	}
	f.setManifest(t, rm)
	return f
}

// TC-69 (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백): a report that would be completed is partial
// when the run had a static analysis gap (a pack-gap task, not required) or
// planned no analyzer task at all. Neither is counted as a required task.
func TestStaticAnalysisGapMakesTheReportPartial(t *testing.T) {
	reg, _ := NewRegistry(GoVetDecoder{}, fakeReview{accept: true})
	all := map[string]string{"review.investigate": "succeeded", "review.verify": "succeeded"}
	if r := build(t, withReview(t, all).in, reg); r.Status != StatusCompleted {
		t.Fatalf("baseline %s %v", r.Status, r.Limitations)
	}
	f := withReview(t, all)
	var rm analysis.ResultManifest
	_ = json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm)
	reason, id := "analyzer_pack_missing", domain.NewID()
	rm.Tasks = append(rm.Tasks, analysis.ManifestTask{TaskID: id, Key: "pack-gap.python", Status: domain.TaskUnsupported, Reason: &reason,
		Executions: []analysis.ManifestExecution{}, Artifacts: []analysis.ManifestArtifact{}, Limitations: []string{reason}})
	f.in.Tasks = append(f.in.Tasks, TaskFacts{ID: id, Key: "pack-gap.python", Status: "unsupported", Reason: &reason, ExecutionOrdinal: 1})
	f.setManifest(t, rm)
	r := build(t, f.in, reg)
	if r.Status != StatusPartial || r.QualityVerdict != VerdictInconclusive || !has(r.Limitations, LimStaticAnalysisGap+":python") || has(r.Limitations, LimRequiredTaskIncomplete) {
		t.Fatalf("gap: %s %s %v", r.Status, r.QualityVerdict, r.Limitations)
	}
	// The same reason on a key outside the gap namespace is not a gap.
	f = withReview(t, all)
	_ = json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm)
	for i := range f.in.Tasks {
		if f.in.Tasks[i].Key == "go-vet" {
			f.in.Tasks[i].Reason = &reason
		}
	}
	if r := build(t, f.in, reg); has(r.Limitations, LimStaticAnalysisGap+":go-vet") {
		t.Fatalf("non-gap key read as gap: %v", r.Limitations)
	}
	// No analyzer task at all: the static analysis did not run.
	f = withReview(t, all)
	_ = json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm)
	var kept []analysis.ManifestTask
	for _, mt := range rm.Tasks {
		if mt.Key != "go-vet" {
			kept = append(kept, mt)
		}
	}
	rm.Tasks = kept
	var facts []TaskFacts
	for _, tf := range f.in.Tasks {
		if tf.Key != "go-vet" {
			facts = append(facts, tf)
		}
	}
	f.in.Tasks = facts
	f.setManifest(t, rm)
	if r := build(t, f.in, reg); r.Status != StatusPartial || !has(r.Limitations, LimStaticAnalysisNotPlanned) || has(r.Limitations, LimRequiredTaskIncomplete) {
		t.Fatalf("no analyzer task: %s %v", r.Status, r.Limitations)
	}
}

// TC-81: completion and a clean verdict need succeeded review tasks whose
// bytes a registered decoder accepted; a tools-only registry has none.
func TestAIReviewGate(t *testing.T) {
	all := map[string]string{"review.investigate": "succeeded", "review.verify": "succeeded"}
	f := withReview(t, all)
	toolsOnly, _ := NewRegistry(GoVetDecoder{})
	r := build(t, f.in, toolsOnly)
	if r.Status != StatusPartial || r.Coverage.AIReview.Status != AIReviewNotPerformed || r.Coverage.AIReview.RequiredTasks[0].Result != "result_missing" || r.Coverage.LLMExposedFiles != nil {
		t.Fatalf("succeeded review tasks without a decoder: %s %+v", r.Status, r.Coverage.AIReview)
	}
	if !has(r.Limitations, LimUnrecognizedArtifact) {
		t.Fatalf("unknown review document not reported: %v", r.Limitations)
	}
	reg, _ := NewRegistry(GoVetDecoder{}, fakeReview{accept: true})
	r = build(t, f.in, reg)
	if r.Status != StatusCompleted || r.Coverage.AIReview.Status != AIReviewPerformed || r.QualityVerdict != VerdictInconclusive {
		t.Fatalf("accepted review: %s %s %s", r.Status, r.Coverage.AIReview.Status, r.QualityVerdict)
	}
	reg, _ = NewRegistry(GoVetDecoder{}, fakeReview{accept: false})
	if r = build(t, f.in, reg); r.Status != StatusPartial || r.Coverage.AIReview.RequiredTasks[0].Result != "result_rejected" {
		t.Fatalf("rejected review: %s %+v", r.Status, r.Coverage.AIReview)
	}
	f = withReview(t, map[string]string{"review.investigate": "failed", "review.verify": "succeeded"})
	reg, _ = NewRegistry(GoVetDecoder{}, fakeReview{accept: true})
	if r = build(t, f.in, reg); r.Status != StatusPartial || r.Coverage.AIReview.Status != AIReviewPartial {
		t.Fatalf("one failed review task: %s %s", r.Status, r.Coverage.AIReview.Status)
	}
}

func TestVerdictRules(t *testing.T) {
	cases := []struct {
		status string
		s      Summary
		ai     string
		want   string
	}{
		{StatusPartial, Summary{ActionableFindings: 1}, AIReviewNotPerformed, VerdictAttention},
		{StatusCompleted, Summary{}, AIReviewPerformed, VerdictNoActionable},
		{StatusCompleted, Summary{PendingCandidates: 1}, AIReviewPerformed, VerdictInconclusive},
		{StatusCompleted, Summary{}, AIReviewNotPerformed, VerdictInconclusive},
		{StatusCompleted, Summary{UnresolvedObservations: 1}, AIReviewPerformed, VerdictInconclusive},
		{StatusPartial, Summary{}, AIReviewPerformed, VerdictInconclusive},
		{StatusFailed, Summary{}, AIReviewNotPerformed, VerdictInconclusive},
	}
	for _, c := range cases {
		if got := DecideVerdict(c.status, c.s, c.ai); got != c.want {
			t.Fatalf("%+v: %s", c, got)
		}
	}
	if decideStatus("", true, false, AIReviewNotPerformed) != StatusPartial || decideStatus("", false, false, AIReviewPerformed) != StatusFailed ||
		decideStatus(StatusCancelled, true, false, AIReviewPerformed) != StatusCancelled || decideStatus("", true, false, AIReviewPerformed) != StatusCompleted {
		t.Fatal("status rules")
	}
}

// TC-80: raw references bind per task even when two tasks have byte-identical stdout.
func TestRawRefsBindPerTask(t *testing.T) {
	second := readDoc(t)
	// Distinct observation ids (another module); same stdout bytes and hash.
	for _, o := range second["observations"].([]any) {
		m := o.(map[string]any)
		m["id"] = "obs_" + strings.Repeat("b", 16) + m["id"].(string)[len(m["id"].(string))-4:]
	}
	f := newFixture(t, goVet(t), taskSpec{key: "go-vet.m01", status: "succeeded", required: true, doc: second})
	r := build(t, f.in, DefaultRegistry())
	byTask := map[string]map[string]bool{}
	for _, o := range r.Observations {
		if byTask[o.TaskKey] == nil {
			byTask[o.TaskKey] = map[string]bool{}
		}
		byTask[o.TaskKey][o.RawRef.ArtifactID] = true
	}
	if len(byTask) != 2 || len(byTask["go-vet"]) != 1 || len(byTask["go-vet.m01"]) != 1 || byTask["go-vet"][f.stdout["go.vet"]] != true {
		t.Fatalf("raw refs: %v", byTask)
	}
	for id := range byTask["go-vet.m01"] {
		if id == f.stdout["go.vet"] {
			t.Fatal("second task bound to the first task's artifact")
		}
	}
	// Colliding observation ids across tasks reject the later contribution.
	f = newFixture(t, goVet(t), taskSpec{key: "go-vet.m01", status: "succeeded", required: true, doc: readDoc(t)})
	r = build(t, f.in, DefaultRegistry())
	if !has(r.Limitations, LimArtifactRejected) || r.Status != StatusPartial {
		t.Fatalf("duplicate ids: %s %v", r.Status, r.Limitations)
	}
}

// TC-80/TC-08: a rejected or missing document is never zero findings.
func TestRejectedDocumentIsNotClean(t *testing.T) {
	f := newFixture(t, goVet(t))
	f.rewriteDoc(t, func(d map[string]any) { d["snapshot_manifest_digest"] = strings.Repeat("f", 64) })
	r := build(t, f.in, DefaultRegistry())
	if r.Status != StatusFailed || r.QualityVerdict != VerdictInconclusive || len(r.Observations) != 0 || !has(r.Limitations, LimArtifactRejected) || r.Coverage.ToolProcessedFiles != nil {
		t.Fatalf("rejected document: %s %v %+v", r.Status, r.Limitations, r.Coverage)
	}
	f = newFixture(t, goVet(t))
	delete(f.in.Bodies, f.docArt)
	r = build(t, f.in, DefaultRegistry())
	if !has(r.Limitations, LimArtifactMissing) || r.Coverage.ToolProcessedFiles != nil || r.Status == StatusCompleted {
		t.Fatalf("missing document: %s %v", r.Status, r.Limitations)
	}
}

// A location whose path/hash is not in the source manifest stays an
// observation and never becomes a finding.
func TestSourceManifestMismatchIsolatesLocation(t *testing.T) {
	f := newFixture(t, goVet(t))
	f.in.SourceManifest = sourceManifest(t, readDoc(t), "bad/bad.go")
	f.in.Snapshot.ManifestDigest = domain.Digest(f.in.SourceManifest)
	f.rewriteDoc(t, func(d map[string]any) { d["snapshot_manifest_digest"] = f.in.Snapshot.ManifestDigest })
	var rm analysis.ResultManifest
	_ = json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm)
	rm.Snapshot.ManifestDigest = f.in.Snapshot.ManifestDigest
	f.setManifest(t, rm)
	r := build(t, f.in, DefaultRegistry())
	mismatched := 0
	for _, o := range r.Observations {
		if o.Location.Path == "bad/bad.go" {
			if o.EvidenceState != EvidenceManifestMismatch {
				t.Fatalf("observation %+v", o)
			}
			mismatched++
		}
	}
	if mismatched == 0 || r.Summary.UnresolvedObservations != mismatched || r.Summary.TotalFindings != 5-mismatched {
		t.Fatalf("mismatched %d summary %+v", mismatched, r.Summary)
	}
	for _, x := range r.Findings {
		if x.Locations[0].Path == "bad/bad.go" {
			t.Fatal("finding cited an unverified location")
		}
	}
}

// The DB task status is the authority; artifacts may not upgrade it.
func TestManifestCannotUpgradeTaskStatus(t *testing.T) {
	f := newFixture(t, goVet(t))
	f.in.Tasks[0].Status = "failed"
	r := build(t, f.in, DefaultRegistry())
	if !has(r.Limitations, LimResultManifestRejected) || len(r.Observations) != 0 || r.Status != StatusFailed || r.Tasks[0].Status != "failed" {
		t.Fatalf("upgrade accepted: %s %v", r.Status, r.Limitations)
	}
}

// TC-83 builder side: a failed run without a result manifest still reports
// its DB task rows and never looks clean.
func TestFailedRunWithoutManifest(t *testing.T) {
	f := newFixture(t, taskSpec{key: "go-vet", status: "failed", required: true})
	f.in.ResultManifest = nil
	f.in.Run.FinalStatus = StatusFailed
	f.in.Attempt.CompletionStatus = "failed"
	reason := "dependency_unavailable"
	f.in.Tasks[0].Reason = &reason
	for id := range f.in.Bodies {
		delete(f.in.Bodies, id)
	}
	r := build(t, f.in, DefaultRegistry())
	if r.Status != StatusFailed || r.QualityVerdict != VerdictInconclusive || r.Builder.ResultManifestArtifactID != nil || !has(r.Limitations, LimResultManifestMissing) || r.Tasks[0].Reason == nil {
		t.Fatalf("failed run report %+v", r)
	}
	if r.Coverage.ToolProcessedFiles != nil {
		t.Fatal("processed files must be unknown")
	}
}

func TestBuildRefusesTamperedInput(t *testing.T) {
	f := newFixture(t, goVet(t))
	f.in.Bodies[f.docArt] = append([]byte{}, f.in.Bodies[f.docArt]...)
	f.in.Bodies[f.docArt][10] ^= 1
	if _, err := Build(f.in, DefaultRegistry()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("tampered body: %v", err)
	}
	f = newFixture(t, goVet(t))
	f.in.SourceManifest = append([]byte(" "), f.in.SourceManifest...)
	if _, err := Build(f.in, DefaultRegistry()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("source manifest mismatch: %v", err)
	}
	f = newFixture(t, goVet(t))
	f.in.Tasks[0].Status = "running"
	if _, err := Build(f.in, DefaultRegistry()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-terminal task: %v", err)
	}
	f = newFixture(t, goVet(t))
	f.in.Artifacts[1].AttemptID = domain.NewID()
	if _, err := Build(f.in, DefaultRegistry()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("foreign artifact: %v", err)
	}
	// Result manifest from another epoch is rejected (DB rows only).
	f = newFixture(t, goVet(t))
	f.in.Attempt.Epoch = 2
	if r := build(t, f.in, DefaultRegistry()); !has(r.Limitations, LimResultManifestRejected) || len(r.Observations) != 0 {
		t.Fatalf("stale manifest accepted: %v", r.Limitations)
	}
}

// TC-84: the code-free projection carries no tool message text.
func TestCodeFreeProjection(t *testing.T) {
	f := newFixture(t, goVet(t))
	r := build(t, f.in, DefaultRegistry())
	p := Project(r, false)
	b, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Observations {
		msg := *o.RawMessage
		if strings.Contains(string(b), msg) || strings.Contains(string(b), msg[:20]) {
			t.Fatalf("code-free export carries %q", msg)
		}
	}
	if !reflect.DeepEqual(p.Summary, r.Summary) {
		t.Fatal("projection changed counts")
	}
	md := RenderMarkdown(p, Digest(b), Korean)
	html, _ := RenderHTML(p, Digest(b), Korean)
	for _, o := range r.Observations {
		if strings.Contains(string(md), (*o.RawMessage)[:20]) || strings.Contains(string(html), (*o.RawMessage)[:20]) {
			t.Fatal("code-free rendering carries a tool message")
		}
	}
	if !strings.Contains(string(html), Korean.Label("export_codefree")) {
		t.Fatal("code-free label missing")
	}
}

var dataCount = regexp.MustCompile(`data-count="([a-z_]+)">([0-9]+|미확정)<`)

// TC-82: the three formats show the same numbers, ids and digest.
func TestFormatsShowSameCounts(t *testing.T) {
	f := newFixture(t, goVet(t))
	r := build(t, f.in, DefaultRegistry())
	b, _ := Marshal(r)
	digest := Digest(b)
	html, err := RenderHTML(r, digest, Korean)
	if err != nil {
		t.Fatal(err)
	}
	md := string(RenderMarkdown(r, digest, Korean))
	var back Report
	_ = json.Unmarshal(b, &back)
	want := map[string]int{"raw_observations": back.Summary.RawObservations, "unresolved_observations": back.Summary.UnresolvedObservations, "total_findings": back.Summary.TotalFindings,
		"actionable_findings": back.Summary.ActionableFindings, "pending_candidates": back.Summary.PendingCandidates, "refuted_findings": back.Summary.RefutedFindings, "inventory_files": back.Coverage.InventoryFiles,
		"tool_processed_files": *back.Coverage.ToolProcessedFiles}
	got := map[string]int{}
	for _, m := range dataCount.FindAllStringSubmatch(string(html), -1) {
		got[m[1]], _ = strconv.Atoi(m[2])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html counts %v want %v", got, want)
	}
	if strings.Count(string(html), `data-finding="`) != len(r.Findings) || strings.Count(string(html), `data-observation="`) != len(r.Observations) {
		t.Fatal("html list length differs from counts")
	}
	for _, row := range newView(r, digest, Korean).Counts {
		if !strings.Contains(md, "| "+mdText(row.Label)+" | "+row.Value+" |") {
			t.Fatalf("markdown count %s missing", row.Name)
		}
	}
	if strings.Count(md, "\n#### ") != len(r.Findings) {
		t.Fatal("markdown finding count differs")
	}
	// Every section count of the overview is the same number in HTML and
	// Markdown (docs/REPORT.md#분석-종류별-절).
	sectionCount := regexp.MustCompile(`data-count="([a-z_]+\.[a-z_]+)">([0-9]+)<`)
	shown := map[string]string{}
	for _, m := range sectionCount.FindAllStringSubmatch(string(html), -1) {
		shown[m[1]] = m[2]
	}
	sections := 0
	for _, s := range back.Overview.Sections {
		for k, n := range s.Counts {
			if s.Key == SectionGaps {
				continue
			}
			sections++
			if shown[s.Key+"."+k] != strconv.Itoa(n) {
				t.Fatalf("html section count %s.%s = %q, report %d", s.Key, k, shown[s.Key+"."+k], n)
			}
			if !strings.Contains(md, "| "+mdText(Korean.Label("count."+s.Key+"."+k))+" | "+strconv.Itoa(n)+" |") {
				t.Fatalf("markdown section count %s.%s missing", s.Key, k)
			}
		}
	}
	if sections == 0 {
		t.Fatal("no section count rendered")
	}
	for _, s := range []string{digest, r.ID, r.Inputs.SnapshotID} {
		if !strings.Contains(md, s) || !strings.Contains(string(html), s) {
			t.Fatalf("%s missing from a format", s)
		}
	}
}

// TC-84: hostile tool text cannot create markup, links or table structure.
func TestRenderingEscapesHostileText(t *testing.T) {
	f := newFixture(t, goVet(t))
	hostile := "<script>alert(1)</script> </pre> ](javascript:alert(1)) ![x](http://evil.invalid/a.png) | col | `tick` ``x`` \n# heading <img src=x onerror=1> 한글"
	f.rewriteDoc(t, func(d map[string]any) {
		for _, o := range d["observations"].([]any) {
			o.(map[string]any)["raw_message"] = hostile
		}
	})
	r := build(t, f.in, DefaultRegistry())
	b, _ := Marshal(r)
	html, _ := RenderHTML(r, Digest(b), Korean)
	font := `url("` + reportFontURL + `")`
	h := string(html)
	if !strings.Contains(h, font) {
		t.Fatal("sectioned HTML lacks its offline font")
	}
	for _, notice := range []string{
		"Copyright (c) 2018-2026 The Tabler Authors",
		"Copyright (c) 2020-2026 Paweł Kuna",
		"Copyright (c) 2011-2025 The Bootstrap Authors",
		"Permission is hereby granted",
		"SIL OPEN FONT LICENSE",
		"Reserved Font Name",
	} {
		if !strings.Contains(h, notice) {
			t.Fatalf("saved HTML lacks asset notice %q", notice)
		}
	}
	h = strings.Replace(h, font, "", 1)
	navigation := `<script id="report-export-navigation">` + reportExportNavigation + `</script>`
	if strings.Count(h, navigation) != 1 || !strings.Contains(h, `script-src 'sha256-`+exportNavigationHash()+`'`) {
		t.Fatal("saved HTML does not authorize exactly the embedded navigation script")
	}
	h = strings.Replace(h, navigation, "", 1)
	for _, bad := range []string{"<script", "<img", "<iframe", "<link", "<object", "url("} {
		if strings.Contains(strings.ToLower(h), bad) {
			t.Fatalf("html contains %q", bad)
		}
	}
	if regexp.MustCompile(`<[^>]*\s(?:src|on[a-z]+|style)=`).MatchString(h) {
		t.Fatal("html tag carries src/on*/style attributes")
	}
	if !strings.Contains(h, "Content-Security-Policy") || !strings.Contains(h, "&lt;script&gt;") {
		t.Fatal("html policy or escaping missing")
	}
	for _, m := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(h, -1) {
		if !strings.HasPrefix(m[1], "#") {
			t.Fatalf("non-fragment link %s", m[1])
		}
	}
	// The sectioned layout (6 observation columns) and the flat layout of
	// older revisions (7 columns) both keep their table structure.
	flat := r
	flat.Builder.Version = "4"
	flat.Overview, flat.Environment, flat.Documents, flat.StaticAnalysis, flat.Metrics, flat.Secrets, flat.CodeReview, flat.Gaps = nil, nil, nil, nil, nil, nil, nil, nil
	for rep, pipes := range map[*Report]int{&r: 7, &flat: 8} {
		md := string(RenderMarkdown(*rep, Digest(b), Korean))
		outside := strings.ReplaceAll(stripCodeSpans(md), "[문서 분석에서 보기](#documents)", "")
		for _, section := range SectionKeys {
			outside = strings.ReplaceAll(outside, "[보기](#"+section+")", "")
		}
		outside = regexp.MustCompile(`\[상세 보기\]\(#static-detail-obs_[0-9a-f]{20}\)`).ReplaceAllString(outside, "")
		if strings.Contains(outside, "<script") || strings.Contains(outside, "<img") || strings.Contains(outside, "](") || strings.Contains(outside, "![") || strings.Contains(outside, "\n# heading") {
			t.Fatalf("markdown structure broken:\n%s", md)
		}
		rows := 0
		for _, line := range strings.Split(md, "\n") {
			if strings.HasPrefix(line, "| `obs_") {
				rows++
				if strings.Count(strings.ReplaceAll(line, "\\|", ""), "|") != pipes {
					t.Fatalf("table row structure changed: %s", line)
				}
			}
		}
		if rows == 0 {
			t.Fatal("no observation row rendered")
		}
	}
}

// TC-81: no completion claim in any format unless AI review was performed.
func TestNoForbiddenClaims(t *testing.T) {
	f := newFixture(t, goVet(t))
	r := build(t, f.in, DefaultRegistry())
	for _, inc := range []bool{true, false} {
		p := Project(r, inc)
		b, _ := Marshal(p)
		html, _ := RenderHTML(p, Digest(b), Korean)
		for _, out := range []string{string(b), string(html), string(RenderMarkdown(p, Digest(b), Korean))} {
			if containsForbidden(out) || strings.Contains(out, Korean.Label("banner_completed")) {
				t.Fatal("forbidden completion claim rendered")
			}
		}
	}
}

// stripCodeSpans removes CommonMark code spans (a backtick run closed by a
// run of the same length) so a test can inspect the text Markdown interprets.
func stripCodeSpans(md string) string {
	var out strings.Builder
	for _, line := range strings.Split(md, "\n") {
		i := 0
		for i < len(line) {
			if line[i] != '`' {
				out.WriteByte(line[i])
				i++
				continue
			}
			n := 0
			for i+n < len(line) && line[i+n] == '`' {
				n++
			}
			j := i + n
			closed := -1
			for j < len(line) {
				if line[j] == '`' {
					m := 0
					for j+m < len(line) && line[j+m] == '`' {
						m++
					}
					if m == n {
						closed = j + m
						break
					}
					j += m
					continue
				}
				j++
			}
			if closed < 0 {
				out.WriteString(line[i : i+n])
				i += n
				continue
			}
			i = closed
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// TC-81: accepted model records cannot hide a missing required tool result.
func TestMissingAnalyzerResultWithAcceptedAIReview(t *testing.T) {
	f := withReview(t, map[string]string{"review.investigate": "succeeded", "review.verify": "succeeded"})
	delete(f.in.Bodies, f.docArt)
	reg, _ := NewRegistry(GoVetDecoder{}, fakeReview{accept: true})
	got := build(t, f.in, reg)
	if got.Status != StatusPartial || got.QualityVerdict != VerdictInconclusive || got.Coverage.AIReview.Status != AIReviewPerformed {
		t.Fatalf("missing tool result: %s %s %+v", got.Status, got.QualityVerdict, got.Coverage.AIReview)
	}
}

// TC-80: worker privacy mode may record several hidden commands for one task.
func TestUnreportedExecutionsHaveDistinctIDs(t *testing.T) {
	f := newFixture(t, goVet(t))
	var rm analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm); err != nil {
		t.Fatal(err)
	}
	hidden := rm.Tasks[0].Executions[0]
	hidden.Name = "unreported"
	hidden.Command = analysis.ManifestCommand{Dir: analysis.RedactedMarker, Argv: []string{}, EnvKeys: []string{}}
	hidden.StdoutArtifactID, hidden.StderrArtifactID = nil, nil
	rm.Tasks[0].Executions = append(rm.Tasks[0].Executions, hidden, hidden)
	f.setManifest(t, rm)
	got := build(t, f.in, DefaultRegistry())
	if len(got.Executions) != len(rm.Tasks[0].Executions) {
		t.Fatalf("lost hidden executions: %d", len(got.Executions))
	}
	f.in.Tasks[0].ExecutionOrdinal = 0
	got = build(t, f.in, DefaultRegistry())
	if len(got.Executions) != 0 || !has(got.Limitations, LimResultManifestRejected) {
		t.Fatalf("invented execution row: %+v", got.Limitations)
	}
}

// A succeeded task and matching hashes do not attest to an analysis when
// the analyzer's own parse or coverage contradicts success.
func TestRejectedGoVetCoverageCannotClaimClean(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"unparsed":        func(d map[string]any) { d["parse"].(map[string]any)["ok"] = false },
		"missing_command": func(d map[string]any) { d["executions"] = d["executions"].([]any)[:3] },
		"wrong_count": func(d map[string]any) {
			d["coverage"].(map[string]any)["packages"] = map[string]int{"analyzed_clean": 94}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, goVet(t))
			f.rewriteDoc(t, change)
			got := build(t, f.in, DefaultRegistry())
			if !has(got.Limitations, LimArtifactRejected) || len(got.Observations) != 0 || got.Coverage.ToolProcessedFiles != nil {
				t.Fatalf("contradictory artifact accepted: %+v", got.Limitations)
			}
		})
	}
}

func TestWithTestsProfileRequiresTestResults(t *testing.T) {
	f := withReview(t, map[string]string{"review.investigate": "succeeded", "review.verify": "succeeded"})
	f.in.Run.Profile = "with-tests"
	reg, _ := NewRegistry(GoVetDecoder{}, fakeReview{accept: true})
	got := build(t, f.in, reg)
	if got.Status != StatusPartial || !has(got.Limitations, LimTestsNotPerformed) {
		t.Fatalf("with-tests without tests: %s %+v", got.Status, got.Limitations)
	}
}
