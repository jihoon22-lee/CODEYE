package run

import (
	"io"
	"os"
	"path/filepath"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

// Retain only source positions and outcomes of terminal tests whose sidecar
// was uploaded by this task. Subsequent tasks cannot replace this information
// by rewriting a file in the workspace or inventing a runner result.
func collectTestEvidence(job *analysis.Job, t *taskRun) {
	if job.Profile != "with-tests" || !job.AllowModelReview || t.spec.Stage != domain.StageTest || t.manifest.Status != t.status ||
		(t.status != domain.TaskSucceeded && t.status != domain.TaskFailed) || len(job.TestEvidence) >= analysis.MaxTestEvidence {
		return
	}
	var sidecar analysis.ManifestArtifact
	for _, a := range t.manifest.Artifacts {
		if a.Role == "other" && a.MediaType == resultdoc.MediaType {
			if sidecar.ArtifactID != "" {
				return
			}
			sidecar = a
		}
	}
	if sidecar.ArtifactID == "" || sidecar.SizeBytes <= 0 || sidecar.SizeBytes > resultdoc.MaxBytes {
		return
	}
	var run *executor.Result
	for i := range t.result.Executions {
		e := &t.result.Executions[i]
		if e.Name == "test.run" {
			if run != nil {
				return
			}
			run = &e.Result
		}
	}
	if run == nil || run.Status != executor.StatusExited || run.Cleanup != executor.CleanupConfirmed || run.Stdout.Truncated || run.Stderr.Truncated || run.Stdout.Incomplete || run.Stderr.Incomplete {
		return
	}
	f, err := os.OpenInRoot(job.Scratch, filepath.Join("tests", t.spec.Key, "test-results.json"))
	if err != nil {
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, resultdoc.MaxBytes+1))
	if err != nil || int64(len(body)) != sidecar.SizeBytes || domain.Digest(body) != sidecar.SHA256 {
		return
	}
	doc, err := resultdoc.Decode(body, resultdoc.Expect{RunID: job.RunID, AttemptID: job.AttemptID, Snapshot: job.Snapshot.Digest, TaskKey: t.spec.Key, StdoutSHA256: domain.Digest(run.Stdout.Data), StderrSHA256: domain.Digest(run.Stderr.Data)})
	if err != nil {
		return
	}
	for _, c := range doc.Cases {
		if c.Path != "" && len(job.TestEvidence) < analysis.MaxTestEvidence {
			job.TestEvidence = append(job.TestEvidence, analysis.ObservedTest{Path: c.Path, Line: c.Line, Status: c.Status})
		}
	}
}
