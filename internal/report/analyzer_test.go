package report

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The generic analyzer envelope (docs/39) decoded from the real staticcheck
// 2026.2.2 document (examples/observations-staticcheck.json) next to the real
// go vet document. Only snapshot bindings are re-pointed to a manifest made
// for the test.

func readStaticcheckDoc(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../examples/observations-staticcheck.json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// notRunDoc is the envelope pack.NotConfigured writes for staticcheck when
// --staticcheck is not given.
func notRunDoc(t *testing.T) map[string]any {
	t.Helper()
	d := readStaticcheckDoc(t)
	d["adapter"] = map[string]any{"id": "codeye-not-run", "version": "1"}
	d["parser"] = map[string]any{"id": "codeye-not-run", "version": "1"}
	d["outcome"] = map[string]any{"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": "not_run", "partial_parse": false}
	d["executions"], d["observations"], d["tool_errors"] = []any{}, []any{}, []any{}
	d["tool"] = map[string]any{"name": "staticcheck", "version": nil, "version_tested": false}
	d["target"] = map[string]any{"platform": nil, "properties": map[string]any{}}
	d["rule_pack"] = map[string]any{"id": "none", "version": "1", "selection": []any{}, "repository_config": "none", "source_suppressions": "none"}
	cov := d["coverage"].(map[string]any)
	cov["processed_paths"], cov["files_skipped"], cov["units"] = []any{}, 0, map[string]any{}
	d["parse"] = map[string]any{"ok": false, "records": 0, "unparsed_bytes": 0, "shape_errors": 0, "observations": 0, "unresolved_observations": 0, "tool_errors": 0}
	d["limitations"] = []any{"tool_not_configured"}
	return d
}

func goVetProcessed(doc map[string]any) []string {
	var out []string
	for _, p := range doc["inventory"].([]any) {
		pkg := p.(map[string]any)
		if s := pkg["state"]; s == "analyzed_clean" || s == "analyzed_with_findings" {
			for _, f := range pkg["go_files"].([]any) {
				out = append(out, f.(string))
			}
		}
	}
	return out
}

// unionManifest lists every path the documents locate or claim processed.
func unionManifest(t *testing.T, docs ...map[string]any) []byte {
	t.Helper()
	paths := map[string]string{"go.mod": strings.Repeat("a", 64)}
	addLoc := func(v any) {
		if loc, ok := v.(map[string]any); ok && loc != nil {
			paths[loc["path"].(string)] = loc["blob_sha256"].(string)
		}
	}
	for _, d := range docs {
		for _, o := range d["observations"].([]any) {
			om := o.(map[string]any)
			addLoc(om["location"])
			if rel, ok := om["related"].([]any); ok {
				for _, r := range rel {
					addLoc(r.(map[string]any)["location"])
				}
			}
		}
		if cov, ok := d["coverage"].(map[string]any); ok {
			if pp, ok := cov["processed_paths"].([]any); ok {
				for _, p := range pp {
					if _, seen := paths[p.(string)]; !seen {
						paths[p.(string)] = strings.Repeat("b", 64)
					}
				}
			}
		}
		if _, ok := d["inventory"]; ok {
			for _, p := range goVetProcessed(d) {
				if _, seen := paths[p]; !seen {
					paths[p] = strings.Repeat("c", 64)
				}
			}
		}
	}
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

// analyzerFixture is newFixture over several documents with one source
// manifest covering all of them. execs edits a task's recorded executions.
func analyzerFixture(t *testing.T, editExec func(key string, e *analysis.ManifestExecution), tasks ...taskSpec) *fixture {
	t.Helper()
	f := &fixture{runID: domain.NewID(), attempt: domain.NewID(), stdout: map[string]string{}}
	var docs []map[string]any
	for _, ts := range tasks {
		if ts.doc != nil {
			docs = append(docs, ts.doc)
		}
	}
	src := unionManifest(t, docs...)
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
		Handlers: []analysis.ManifestHandler{{Name: "analyzers", Version: "1", Capability: "analysis:deep-review"}}, CleanupState: "confirmed", Limitations: []string{}}
	for _, ts := range tasks {
		id := domain.NewID()
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
				f.stdout[ts.key+"/"+name] = sout.ID
				code := int(ex["exit_code"].(float64))
				cmd := ex["command"].(map[string]any)
				argv, envKeys := []string{}, []string{}
				for _, a := range cmd["argv"].([]any) {
					argv = append(argv, a.(string))
				}
				for _, a := range cmd["env_keys"].([]any) {
					envKeys = append(envKeys, a.(string))
				}
				me := analysis.ManifestExecution{Name: name, Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
					Command: analysis.ManifestCommand{Argv: argv, Dir: ".", EnvKeys: envKeys}, StdoutArtifactID: &sout.ID, StderrArtifactID: &serr.ID,
					StartedAt: "2026-11-18T00:00:00Z", EndedAt: "2026-11-18T00:00:01Z"}
				if editExec != nil {
					editExec(ts.key, &me)
				}
				mt.Executions = append(mt.Executions, me)
			}
			body := canonical(t, ts.doc)
			d := art("observations", analysis.MediaObservations, body, "", 0)
			mt.Artifacts = append(mt.Artifacts, analysis.ManifestArtifact{ArtifactID: d.ID, Role: d.Role, MediaType: d.MediaType, SHA256: d.SHA256, SizeBytes: d.SizeBytes})
		}
		if f.taskID == "" {
			f.taskID = id
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

func staticcheckTask(t *testing.T) taskSpec {
	return taskSpec{key: "staticcheck", status: "succeeded", required: false, doc: readStaticcheckDoc(t)}
}

func toolRow(r Report, key string) *ToolCoverage {
	for i := range r.Coverage.Tools {
		if r.Coverage.Tools[i].TaskKey == key {
			return &r.Coverage.Tools[i]
		}
	}
	return nil
}

// TC-09/TC-13: staticcheck observations become bound observations and
// unreviewed tool findings; two diagnostics on the same line with different
// messages stay two findings, an identical repeat joins its finding, and the
// summary counts raw observations and findings separately.
func TestAnalyzerDecoderStaticcheckFindings(t *testing.T) {
	sc := staticcheckTask(t)
	obs := sc.doc["observations"].([]any)
	dup := map[string]any{}
	for k, v := range obs[0].(map[string]any) {
		dup[k] = v
	}
	dup["id"] = "obs_00000000000000000001"
	sc.doc["observations"] = append(obs, dup)
	sc.doc["parse"].(map[string]any)["observations"] = float64(len(obs) + 1)
	f := analyzerFixture(t, nil, goVet(t), sc)
	r := build(t, f.in, DefaultRegistry())

	if r.Summary.RawObservations != 5+8 || r.Summary.TotalFindings != 5+7 || r.Summary.PendingCandidates != 12 || r.Summary.ActionableFindings != 0 {
		t.Fatalf("summary %+v", r.Summary)
	}
	sa4006 := 0
	for _, x := range r.Findings {
		if x.Validation != "unreviewed" || x.Severity != "low" || x.Disposition != "open" || !ValidFingerprint(x.Fingerprint) || x.FingerprintBasis == nil {
			t.Fatalf("tool finding %+v", x)
		}
		if strings.HasPrefix(x.Title, "go.staticcheck") {
			// W23 identity: the shared ToolFingerprint, never a decoder hash.
			want, basis := ToolFingerprint("go.staticcheck", x.FingerprintBasis.Rule, x.Locations[0].Path, "", strings.TrimPrefix(x.Title, "go.staticcheck "+x.FingerprintBasis.Rule+": "))
			if len(x.Title) < 160 && (x.Fingerprint != want || x.FingerprintBasis.Analyzer != "go.staticcheck" || x.FingerprintBasis.Producer != ProducerTool || x.FingerprintBasis.MoveKey != basis.MoveKey) {
				t.Fatalf("staticcheck fingerprint %s %+v", x.Fingerprint, x.FingerprintBasis)
			}
		}
		if strings.HasPrefix(x.Title, "go.staticcheck SA4006") {
			sa4006++
			if len(x.ObservationIDs) != 1 {
				t.Fatalf("different SA4006 messages grouped: %+v", x.ObservationIDs)
			}
		}
		if x.ObservationIDs[0] == obs[0].(map[string]any)["id"] && len(x.ObservationIDs) != 2 {
			t.Fatalf("identical repeat not grouped: %+v", x.ObservationIDs)
		}
	}
	if sa4006 != 2 {
		t.Fatalf("SA4006 findings %d", sa4006)
	}
	scObs := 0
	for _, o := range r.Observations {
		if o.Tool == "go.staticcheck" {
			scObs++
			if o.EvidenceState != EvidenceVerified || o.RawRef.ArtifactID != f.stdout["staticcheck/staticcheck.check"] || o.ToolVersion == nil || *o.ToolVersion != "2026.2.2 (0.8.2)" {
				t.Fatalf("observation %+v", o)
			}
		}
	}
	row := toolRow(r, "staticcheck")
	if scObs != 8 || row == nil || row.Analyzer != "go.staticcheck" || row.ExitClass != "success_with_findings" || row.ProcessedFiles == nil || *row.ProcessedFiles != 3 || row.FilesInScope != 4 ||
		!slices.Equal(row.ProcessedPaths, []string{"clean/c.go", "find/find.go", "find/find_test.go"}) {
		t.Fatalf("staticcheck observations %d row %+v", scObs, row)
	}
	// go vet (4 files) and staticcheck (3 files, clean/c.go is in both): the
	// union is 6, not the sum 7.
	if r.Coverage.ToolProcessedFiles == nil || *r.Coverage.ToolProcessedFiles != 6 || len(r.Coverage.Tools) != 2 {
		t.Fatalf("processed %v tools %d", r.Coverage.ToolProcessedFiles, len(r.Coverage.Tools))
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	// Code-free export: no staticcheck message, the generic templates stay.
	p := Project(r, false)
	b, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Observations {
		if strings.Contains(string(b), *o.RawMessage) {
			t.Fatalf("code-free export carries %q", *o.RawMessage)
		}
	}
	if !strings.Contains(string(b), textToolCondition) || !strings.Contains(string(b), textToolVerification) {
		t.Fatal("generic tool templates were omitted from the code-free export")
	}
	md := RenderMarkdown(r, Digest(b), Korean)
	if !strings.Contains(string(md), "go.staticcheck") {
		t.Fatal("markdown lacks the staticcheck row")
	}
	if _, err := RenderHTML(r, Digest(b), Korean); err != nil {
		t.Fatal(err)
	}
}

// A file processed by both analyzers counts once.
func TestAnalyzerDecoderProcessedUnion(t *testing.T) {
	vet := goVet(t)
	shared := goVetProcessed(vet.doc)[0]
	sc := staticcheckTask(t)
	cov := sc.doc["coverage"].(map[string]any)
	pp := []string{shared}
	for _, p := range cov["processed_paths"].([]any) {
		pp = append(pp, p.(string))
	}
	sortStrings(pp)
	anyPaths := []any{}
	for _, p := range pp {
		anyPaths = append(anyPaths, p)
	}
	cov["processed_paths"] = anyPaths
	r := build(t, analyzerFixture(t, nil, vet, sc).in, DefaultRegistry())
	if r.Coverage.ToolProcessedFiles == nil || *r.Coverage.ToolProcessedFiles != 6 || *toolRow(r, "staticcheck").ProcessedFiles != 4 {
		t.Fatalf("processed %v", r.Coverage.ToolProcessedFiles)
	}
}

// An analyzer without its tool is visible: an unsupported row with a known
// zero, and the go vet result is still reported.
func TestAnalyzerDecoderNotConfigured(t *testing.T) {
	r := build(t, analyzerFixture(t, nil, goVet(t), taskSpec{key: "staticcheck", status: "unsupported", required: false, doc: notRunDoc(t)}).in, DefaultRegistry())
	row := toolRow(r, "staticcheck")
	if row == nil || row.ExitClass != "not_run" || row.ProcessedFiles == nil || *row.ProcessedFiles != 0 || row.ProcessedPaths != nil || !has(row.Limitations, "tool_not_configured") {
		t.Fatalf("row %+v", row)
	}
	if r.Summary.RawObservations != 5 || r.Coverage.ToolProcessedFiles == nil || *r.Coverage.ToolProcessedFiles != 4 {
		t.Fatalf("summary %+v processed %v", r.Summary, r.Coverage.ToolProcessedFiles)
	}
	var task *Task
	for i := range r.Tasks {
		if r.Tasks[i].Key == "staticcheck" {
			task = &r.Tasks[i]
		}
	}
	if task == nil || task.Status != "unsupported" || task.RequiredForVerdict {
		t.Fatalf("task %+v", task)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
}

// The decoder refuses documents that claim more than the worker recorded:
// the artifact is rejected, its task counts as not decoded and nothing of it
// reaches the report.
func TestAnalyzerDecoderRejects(t *testing.T) {
	reject := func(name string, f *fixture) {
		t.Helper()
		r := build(t, f.in, DefaultRegistry())
		if !has(r.Limitations, LimArtifactRejected+":staticcheck") || toolRow(r, "staticcheck") != nil || r.Coverage.ToolProcessedFiles != nil {
			t.Fatalf("%s accepted: %v", name, r.Limitations)
		}
		for _, o := range r.Observations {
			if o.Tool == "go.staticcheck" {
				t.Fatalf("%s: observation of a rejected document", name)
			}
		}
	}
	sc := staticcheckTask(t)
	sc.required = true
	reject("required mismatch", analyzerFixture(t, nil, goVet(t), sc))
	reject("succeeded row with a not-run document", analyzerFixture(t, nil, goVet(t), taskSpec{key: "staticcheck", status: "succeeded", required: false, doc: notRunDoc(t)}))
	reject("recorded exit code differs", analyzerFixture(t, func(key string, e *analysis.ManifestExecution) {
		if key == "staticcheck" && e.Name == "staticcheck.check" {
			two := 2
			e.ExitCode = &two
		}
	}, goVet(t), staticcheckTask(t)))
	reject("unrecorded execution", analyzerFixture(t, func(key string, e *analysis.ManifestExecution) {
		if key == "staticcheck" && e.Name == "go.list" {
			e.Name = "go.list2"
		}
	}, goVet(t), staticcheckTask(t)))
	sc = staticcheckTask(t)
	sc.doc["observations"].([]any)[0].(map[string]any)["raw_ref"].(map[string]any)["byte_end"] = float64(1 << 30)
	reject("raw range outside the stream", analyzerFixture(t, nil, goVet(t), sc))
	// The first find/find.go observation names another blob than the
	// manifest (unionManifest keeps the last one).
	sc = staticcheckTask(t)
	sc.doc["observations"].([]any)[0].(map[string]any)["location"].(map[string]any)["blob_sha256"] = strings.Repeat("0", 64)
	reject("location blob differs from the manifest", analyzerFixture(t, nil, goVet(t), sc))
}
