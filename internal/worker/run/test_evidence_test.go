package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

func TestWorkerPassesOnlyAcceptedScopedTestFactsToReview(t *testing.T) {
	scratch := t.TempDir()
	job := analysis.Job{Profile: "with-tests", AllowModelReview: true, RunID: domain.NewID(), AttemptID: domain.NewID(), Scratch: scratch, Snapshot: source.Snapshot{Digest: domain.Digest([]byte("snapshot"))}}
	key := "test.python"
	empty := domain.Digest(nil)
	want := resultdoc.Expect{RunID: job.RunID, AttemptID: job.AttemptID, Snapshot: job.Snapshot.Digest, TaskKey: key, StdoutSHA256: empty, StderrSHA256: empty}
	res := resultdoc.Document{RunID: want.RunID, AttemptID: want.AttemptID, Snapshot: want.Snapshot, TaskKey: key, SchemaVersion: resultdoc.Version, Runner: "pytest", StdoutSHA256: empty, StderrSHA256: empty, Counts: resultdoc.Counts{Failed: 1}, Cases: []resultdoc.Case{{Name: "test_private_label", Status: "failed", Path: "tests/store.py", Line: 13}}}
	body, err := resultdoc.Encode(res, want)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(scratch, "tests", key)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test-results.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	tr := taskRun{spec: analysis.TaskSpec{Key: key, Stage: domain.StageTest}, status: domain.TaskFailed, manifest: analysis.ManifestTask{Status: domain.TaskFailed, Artifacts: []analysis.ManifestArtifact{{ArtifactID: domain.NewID(), MediaType: resultdoc.MediaType, Role: "other", SHA256: domain.Digest(body), SizeBytes: int64(len(body))}}}, result: analysis.TaskResult{Executions: []analysis.Execution{{Name: "test.run", Result: executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, ExitCode: new(1)}}}}}
	collectTestEvidence(&job, &tr)
	if len(job.TestEvidence) != 1 || job.TestEvidence[0] != (analysis.ObservedTest{Path: "tests/store.py", Line: 13, Status: "failed"}) {
		t.Fatalf("observed %+v", job.TestEvidence)
	}
	job.TestEvidence = nil
	tr.manifest.Artifacts[0].SHA256 = domain.Digest([]byte("different"))
	collectTestEvidence(&job, &tr)
	if len(job.TestEvidence) != 0 {
		t.Fatal("modified sidecar reached verifier")
	}
}
