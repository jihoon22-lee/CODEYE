package review

import (
	"context"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// packStub is a composite-like static handler: the go vet stub, then
// staticcheck tasks writing generic envelopes (docs/39): "staticcheck" with
// one observation on gateway.go, "staticcheck.m01" not run (tool not
// configured) and "staticcheck.m02" that writes nothing.
type packStub struct{}

func (packStub) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: pack.CompositeName, Version: "stub", Capability: pack.Capability}
}

func (packStub) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, _ := vetStub{}.Plan(ctx, j)
	for _, k := range []string{"staticcheck", "staticcheck.m01", "staticcheck.m02"} {
		plan = append(plan, analysis.TaskSpec{Key: k, Title: k, Stage: domain.StageStaticAnalysis})
	}
	return plan, nil
}

func (packStub) Run(ctx context.Context, j analysis.Job, t analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	if pack.Owns("go-vet", t.Key) {
		return vetStub{obs: gatewayObservation}.Run(ctx, j, t, r)
	}
	if t.Key == "staticcheck.m02" {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: "go.staticcheck", Ecosystem: "go"}, obsdoc.Component{ID: "staticcheck", Version: "1"}, obsdoc.Component{ID: "staticcheck-json-v1", Version: "1"}, t.Key, j.Snapshot.Digest)
	doc.Module.Dir, doc.Tool.Name = ".", "staticcheck"
	doc.RulePack = obsdoc.RulePack{ID: "codeye-go-staticcheck", Version: "2", Selection: []string{"-checks", "SA*", "-show-ignored"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{"U1000"}}
	if t.Key == "staticcheck.m01" {
		reason := pack.ReasonToolNotConfigured
		doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskUnsupported, Reason: &reason, ExitClass: obsdoc.ExitNotRun}
		doc.Coverage.ProcessedPaths = []string{}
		doc.Limitations = []string{reason}
	} else {
		first := `{"code":"SA4006","message":"this value of err is never used"}` + "\n"
		stdout := []byte(first + `{"code":"SA4006","severity":"ignored","message":"this value of x is never used"}` + "\n")
		code := 1
		version := "2026.2.2 (0.8.2)"
		doc.Tool.Version, doc.Tool.VersionTested = &version, true
		doc.Executions = []obsdoc.Execution{{Name: "staticcheck.check", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
			Command: analysis.ManifestCommand{Argv: []string{"staticcheck", "-f", "json"}, Dir: ".", EnvKeys: []string{}},
			Stdout:  obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(stdout), CapturedBytes: int64(len(stdout)), TotalBytes: int64(len(stdout))}, Stderr: obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}}}
		var sha string
		for _, e := range j.Snapshot.Manifest.Entries {
			if e.Path == "gateway.go" {
				sha = e.SHA256
			}
		}
		p := "gateway.go"
		severity := "error"
		doc.Observations = []obsdoc.Observation{{ID: "obs_aaaaaaaaaaaaaaaaaaaa", RuleID: "SA4006", Category: "correctness", RawSeverity: &severity, RawMessage: "this value of err is never used (synthetic fixture)",
			Path: &p, Location: &obsdoc.Location{Path: p, StartLine: 11, StartCol: 2, EndLine: 11, EndCol: 9, ColumnUnit: "byte", BlobSHA256: sha}, LocationState: obsdoc.LocationResolved,
			Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: "staticcheck.check", Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex(stdout), ByteStart: 0, ByteEnd: int64(len(first))}, Unit: "example.test/w15"}}
		// A diagnostic hidden by //lint:ignore, reported with -show-ignored.
		ignored := "ignored"
		doc.Observations = append(doc.Observations, obsdoc.Observation{ID: "obs_bbbbbbbbbbbbbbbbbbbb", RuleID: "SA4006", Category: "correctness", RawSeverity: &ignored, RawMessage: "this value of x is never used (synthetic fixture)",
			Path: &p, Location: &obsdoc.Location{Path: p, StartLine: 12, StartCol: 2, EndLine: 12, EndCol: 9, ColumnUnit: "byte", BlobSHA256: sha}, LocationState: obsdoc.LocationResolved,
			Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: "staticcheck.check", Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex(stdout), ByteStart: int64(len(first)), ByteEnd: int64(len(stdout))}, Unit: "example.test/w15", Suppressed: true})
		doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithFindings}
		doc.Coverage.ProcessedPaths = []string{"gateway.go"}
		doc.Parse.OK = true
		doc.Limitations = []string{"cgo_disabled", "source_suppressed_observations"}
	}
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: doc.Outcome.TaskStatus, Artifacts: []analysis.ArtifactFile{art}}
}

// review.interpret reads the generic envelope next to go vet's v0.1
// document: both analyzers' observations reach the model input with their
// evidence windows, a not-run analyzer is a tool row with zero observations,
// a missing envelope is an unavailable upstream, never zero findings, and a
// diagnostic the tool reported as suppressed in source never reaches the
// model (only the limitation that some were left out does).
func TestR1InterpretReadsGenericEnvelope(t *testing.T) {
	j := w15Job(t, nil)
	script := validScript(t)
	script["interpret"] = func(int, opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []any{}, "limits": []any{}})
	}
	f := &r1Caller{script: script}
	_, res := runR1(t, j, packStub{}, f, "1.27.4")
	if res["staticcheck"].Status != domain.TaskSucceeded || res["staticcheck.m01"].Status != domain.TaskUnsupported {
		t.Fatalf("static tasks %+v %+v", res["staticcheck"], res["staticcheck.m01"])
	}
	var in interpretInput
	for _, q := range f.requests {
		if q.Role == "interpret" {
			in = decodeAs[interpretInput](t, q)
		}
	}
	tools := map[string]toolInput{}
	for _, ti := range in.Tools {
		tools[ti.TaskKey] = ti
	}
	if sc := tools["staticcheck"]; sc.Tool != "go.staticcheck" || sc.Observations != 1 || sc.TaskStatus != "succeeded" || sc.ToolVersion == nil ||
		!slices.Equal(sc.Limitations, []string{"cgo_disabled", "source_suppressed_observations"}) {
		t.Fatalf("staticcheck tool row %+v", sc)
	}
	if nr := tools["staticcheck.m01"]; nr.Tool != "go.staticcheck" || nr.Observations != 0 || nr.TaskStatus != "unsupported" || nr.ExitClass != obsdoc.ExitNotRun {
		t.Fatalf("not-run tool row %+v", nr)
	}
	if _, ok := tools["staticcheck.m02"]; ok || tools["go-vet"].Tool != "go.vet" {
		t.Fatalf("tools %+v", in.Tools)
	}
	var keys []string
	for _, o := range in.Observations {
		keys = append(keys, o.TaskKey+"/"+o.RuleID)
		if len(o.EvidenceIDs) != 1 || o.Path != "gateway.go" {
			t.Fatalf("observation %+v", o)
		}
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"go-vet/unusedresult", "staticcheck/SA4006"}) {
		t.Fatalf("observations %v", keys)
	}
	rec := readR1(t, j, "interpret")
	states := map[string]string{}
	for _, u := range rec.Upstream {
		states[u.Source] = u.State + "/" + u.Reason
	}
	if states["staticcheck"] != record.UpstreamAccepted+"/" || states["staticcheck.m01"] != record.UpstreamAccepted+"/" || states["staticcheck.m02"] != record.UpstreamUnavailable+"/"+reasonRecordMissing {
		t.Fatalf("upstream %v", states)
	}
}
