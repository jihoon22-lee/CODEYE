package report

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/faildoc"
)

const testFailureFixturePath = "pkg/arrival_test.go"

type testFailureFixture struct {
	*fixture
	manifest            analysis.ResultManifest
	doc                 faildoc.Document
	log, other, sidecar ArtifactRef
}

func newTestFailureFixture(t *testing.T) *testFailureFixture {
	t.Helper()
	f := newFixture(t, taskSpec{key: "test.go", status: "failed", required: true})
	f.in.Run.Profile = "with-tests"
	f.in.Tasks[0].Stage = string(domain.StageTest)
	f.in.Tasks[0].Reason = new(goTestFailureReason)
	var manifest analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &manifest); err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(f.in.SourceManifest, &snapshot); err != nil {
		t.Fatal(err)
	}
	source := []byte("package pkg\nimport \"testing\"\nfunc TestArrival(t *testing.T) { t.Fatal(\"failed\") }\n")
	blob := domain.Digest(source)
	entries := append(snapshot["entries"].([]any),
		map[string]any{"path": testFailureFixturePath, "kind": "file", "size_bytes": len(source), "mode": 420, "sha256": blob, "encoding": "utf-8"},
		map[string]any{"path": "elsewhere/out_test.go", "kind": "file", "size_bytes": len(source), "mode": 420, "sha256": blob, "encoding": "utf-8"},
	)
	slices.SortFunc(entries, func(a, b any) int {
		return strings.Compare(a.(map[string]any)["path"].(string), b.(map[string]any)["path"].(string))
	})
	snapshot["entries"] = entries
	f.in.SourceManifest = canonical(t, snapshot)
	snapshotSHA := domain.Digest(f.in.SourceManifest)
	f.in.Snapshot.ManifestDigest, manifest.Snapshot.ManifestDigest = snapshotSHA, snapshotSHA
	f.in.Artifacts[0].SHA256, f.in.Artifacts[0].SizeBytes = snapshotSHA, int64(len(f.in.SourceManifest))
	add := func(role, media string, data []byte) ArtifactRef {
		a := ArtifactRef{ID: domain.NewID(), RunID: f.in.Run.ID, AttemptID: f.in.Attempt.ID, Role: role, MediaType: media,
			SHA256: domain.Digest(data), SizeBytes: int64(len(data))}
		f.in.Bodies[a.ID] = data
		f.in.Artifacts = append(f.in.Artifacts, a)
		return a
	}
	const namedFailure = `{"Action":"fail","Package":"example.test/shipment/pkg","Test":"TestArrival"}`
	const before = `{"Action":"run","Package":"example.test/shipment/pkg","Test":"TestArrival"}` + "\n" +
		`{"Action":"output","Package":"example.test/shipment/pkg","Test":"TestArrival","Output":"private transcript sentinel"}` + "\n"
	stdout := []byte(before + namedFailure + "\n" + `{"Action":"fail","Package":"example.test/shipment/pkg"}` + "\n")
	log := add("stdout", "text/plain", stdout)
	other := add("stdout", "text/plain", []byte("unrelated execution output\n"))
	doc := faildoc.Document{SchemaVersion: faildoc.Version, RunID: f.in.Run.ID, AttemptID: f.in.Attempt.ID, Snapshot: snapshotSHA,
		TaskKey: "test.go", StdoutSHA256: log.SHA256, Failures: []faildoc.Failure{{Package: "example.test/shipment/pkg", Test: "TestArrival",
			Path: testFailureFixturePath, Line: 3, BlobSHA256: blob, ByteStart: len(before), ByteEnd: len(before) + len(namedFailure)}}}
	sidecar := add("other", faildoc.MediaType, canonical(t, doc))
	manifest.Tasks[0].Reason = f.in.Tasks[0].Reason
	manifest.Tasks[0].Artifacts = []analysis.ManifestArtifact{{ArtifactID: sidecar.ID, Role: sidecar.Role, MediaType: sidecar.MediaType, SHA256: sidecar.SHA256, SizeBytes: sidecar.SizeBytes}}
	manifest.Tasks[0].Executions = []analysis.ManifestExecution{
		{Name: "test.isolation", Status: executor.StatusExited, ExitCode: new(0), Cleanup: executor.CleanupConfirmed,
			Command: analysis.ManifestCommand{Argv: []string{"unshare", "--net"}, Dir: ".", EnvKeys: []string{}}, StdoutArtifactID: &other.ID},
		{Name: "test.run", Status: executor.StatusExited, ExitCode: new(1), Cleanup: executor.CleanupConfirmed,
			Command:          analysis.ManifestCommand{Argv: []string{"unshare", "[REDACTED]", "test", "-json", "./..."}, Dir: ".", EnvKeys: []string{}},
			StdoutArtifactID: &log.ID, StartedAt: "2026-11-18T00:00:00Z", EndedAt: "2026-11-18T00:00:01Z"},
	}
	f.setManifest(t, manifest)
	return &testFailureFixture{fixture: f, manifest: manifest, doc: doc, log: log, other: other, sidecar: sidecar}
}

func (f *testFailureFixture) rebindSidecar(t *testing.T) {
	t.Helper()
	body := canonical(t, f.doc)
	digest, size := domain.Digest(body), int64(len(body))
	for i := range f.in.Artifacts {
		if f.in.Artifacts[i].ID == f.sidecar.ID {
			f.in.Artifacts[i].SHA256, f.in.Artifacts[i].SizeBytes = digest, size
		}
	}
	f.in.Bodies[f.sidecar.ID] = body
	f.manifest.Tasks[0].Artifacts[0].SHA256, f.manifest.Tasks[0].Artifacts[0].SizeBytes = digest, size
	f.setManifest(t, f.manifest)
}

func TestReportBindsNamedGoTestFailuresToAcceptedExecution(t *testing.T) {
	f := newTestFailureFixture(t)
	available := map[string][]byte{f.log.ID: f.in.Bodies[f.log.ID], f.sidecar.ID: f.in.Bodies[f.sidecar.ID]}
	for _, ref := range []ArtifactRef{f.log, f.other, f.sidecar} {
		delete(f.in.Bodies, ref.ID)
	}
	var opened []string
	f.in.ReadGoTestFailureBodies(func(ref ArtifactRef) ([]byte, bool) {
		opened = append(opened, ref.ID)
		body, found := available[ref.ID]
		return body, found
	})
	if !slices.Equal(opened, []string{f.sidecar.ID, f.log.ID}) {
		t.Fatalf("unexpected artifact reads: %v", opened)
	}
	r := build(t, f.in, DefaultRegistry())
	want := []GoTestFailure{{Name: "TestArrival", Path: testFailureFixturePath, Line: 3}}
	if got := testExecutions(r.Executions)["test.go"].GoTestFailures; !slices.Equal(got, want) || r.Coverage.TestStatus != TestFailed {
		t.Fatalf("failed test evidence = %+v, status = %q", got, r.Coverage.TestStatus)
	}
	if len(r.Findings) != 0 || len(r.Observations) != 0 {
		t.Fatal("test evidence changed analysis findings or observations")
	}
	for _, includeCode := range []bool{true, false} {
		projected := Project(r, includeCode)
		body, err := Marshal(projected)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Contains(text, "private transcript sentinel") || strings.Contains(text, "example.test/shipment/pkg") ||
			strings.Contains(text, `"name":"TestArrival"`) != includeCode || !strings.Contains(text, `"line":3`) ||
			!strings.Contains(text, testFailureFixturePath) || projected.Coverage.TestStatus != TestFailed {
			t.Fatalf("bad projection include_code=%t: %s", includeCode, text)
		}
		if _, err := Unmarshal(body); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(testExecutions(r.Executions)["test.go"].GoTestFailures, want) {
		t.Fatal("code-free projection changed original evidence")
	}
	r.Builder.Version = "17"
	if err := ValidateReport(r); err == nil {
		t.Fatal("a previous builder accepted new Go test failure evidence")
	}
}

func TestReportBindsGoTestFailureInSelectedPackage(t *testing.T) {
	f := newTestFailureFixture(t)
	f.in.Run.ScopeKind, f.in.Run.ScopePaths = "paths", []string{"pkg"}
	// A real failed test remains visible when another package lacks dependencies.
	f.in.Tasks[0].Reason = new("test_dependencies_unavailable")
	f.manifest.Tasks[0].Reason = f.in.Tasks[0].Reason
	f.manifest.Tasks[0].Executions[1].Command.Argv[4] = "./pkg/..."
	f.setManifest(t, f.manifest)
	r := build(t, f.in, DefaultRegistry())
	want := []GoTestFailure{{Name: "TestArrival", Path: testFailureFixturePath, Line: 3}}
	if !slices.Equal(testExecutions(r.Executions)["test.go"].GoTestFailures, want) {
		t.Fatalf("selected Go package failure is absent: %+v", r.Executions)
	}
}

func TestReportOmitsUnboundGoTestFailureEvidence(t *testing.T) {
	for _, name := range []string{"missing sidecar", "corrupt stream", "unrelated stream", "mutated event identity", "wrong blob digest", "source outside run scope", "truncated stdout", "different command", "unsupported task", "zero exit", "wrong attempt", "dependency-only failure"} {
		t.Run(name, func(t *testing.T) {
			f := newTestFailureFixture(t)
			task := &f.manifest.Tasks[0]
			execution := &task.Executions[1]
			want := TestFailed
			switch name {
			case "unrelated stream":
				execution.StdoutArtifactID, task.Executions[0].StdoutArtifactID = &f.other.ID, &f.log.ID
			case "mutated event identity":
				f.doc.Failures[0].Test = "TestDeparted"
			case "wrong blob digest":
				f.doc.Failures[0].BlobSHA256 = strings.Repeat("0", 64)
			case "source outside run scope":
				f.in.Run.ScopeKind, f.in.Run.ScopePaths = "paths", []string{"elsewhere"}
				execution.Command.Argv[4] = "./elsewhere/..."
			case "truncated stdout":
				execution.StdoutTruncated = true
			case "different command":
				execution.Command.Argv[2] = "list"
			case "unsupported task":
				f.in.Tasks[0].Status, task.Status, want = "unsupported", domain.TaskUnsupported, TestNotRun
			case "zero exit":
				execution.ExitCode, want = new(0), TestNotRun
			case "wrong attempt":
				f.doc.AttemptID = domain.NewID()
			case "dependency-only failure":
				f.in.Tasks[0].Reason = new("test_dependencies_unavailable")
				task.Reason = f.in.Tasks[0].Reason
				stdout := []byte("{\"Action\":\"fail\",\"Package\":\"example.test/shipment/pkg\"}\n")
				sha := domain.Digest(stdout)
				f.in.Bodies[f.log.ID] = stdout
				f.doc.StdoutSHA256 = sha
				for i := range f.in.Artifacts {
					if f.in.Artifacts[i].ID == f.log.ID {
						f.in.Artifacts[i].SHA256, f.in.Artifacts[i].SizeBytes = sha, int64(len(stdout))
					}
				}
			}
			f.rebindSidecar(t)
			if name == "missing sidecar" {
				delete(f.in.Bodies, f.sidecar.ID)
			}
			if name == "corrupt stream" {
				stdout := f.in.Bodies[f.log.ID]
				delete(f.in.Bodies, f.log.ID)
				stdout[0] = ' '
				f.in.ReadGoTestFailureBodies(func(ref ArtifactRef) ([]byte, bool) {
					if ref.ID == f.log.ID {
						return stdout, true
					}
					body, ok := f.in.Bodies[ref.ID]
					return body, ok
				})
			}
			r := build(t, f.in, DefaultRegistry())
			if len(testExecutions(r.Executions)["test.go"].GoTestFailures) != 0 || r.Coverage.TestStatus != want {
				t.Fatalf("unbound Go test evidence changed the existing test status: %+v", r.Executions)
			}
		})
	}
}
