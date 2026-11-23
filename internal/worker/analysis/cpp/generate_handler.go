package cpp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// GenerationHandler puts the compile DB generation task in front of the
// inner plan when the inner plan has a C/C++ analyzer task and the job has
// a CMake project without a compile DB. Without the repository-code grant
// the task is still planned (the gap stays visible) and ends unsupported
// without starting anything.
type GenerationHandler struct {
	inner analysis.Handler
	cfg   GenerationHandlerConfig
}

// GenerationHandlerConfig configures the wrapper.
type GenerationHandlerConfig struct {
	Generate GenerateConfig
	// Consumer: a C/C++ analyzer whose tool is usable reads the database.
	// Without one the task is not planned: configuring a project nobody
	// reads would run repository code for nothing.
	Consumer bool
}

// WrapGeneration wraps inner (the analyzer composite or a handler around it).
func WrapGeneration(inner analysis.Handler, cfg GenerationHandlerConfig) (*GenerationHandler, error) {
	if inner == nil {
		return nil, errors.New("cpp: nil inner handler")
	}
	return &GenerationHandler{inner: inner, cfg: cfg}, nil
}

// Info returns the inner handler identity.
func (h *GenerationHandler) Info() analysis.HandlerInfo { return h.inner.Info() }

func isCppAnalyzerTask(key string) bool {
	return key == CppcheckName || key == ClangTidyName || key == GCCAnalyzerName
}

// generationSpec is the planned generation task.
func generationSpec() analysis.TaskSpec {
	return analysis.TaskSpec{Key: GenerateTaskKey, Title: "C/C++ compile DB generation (CMake configure)", Stage: domain.StageBuild}
}

func (h *GenerationHandler) planned(job analysis.Job, inner []analysis.TaskSpec) (bool, error) {
	if !h.cfg.Consumer {
		return false, nil
	}
	consumer := false
	for _, t := range inner {
		if t.Key == GenerateTaskKey {
			return false, errors.New("cpp: inner handler planned the generation task key")
		}
		consumer = consumer || isCppAnalyzerTask(t.Key)
	}
	if !consumer {
		return false, nil
	}
	projects, err := GenerationProjects(job)
	return len(projects) > 0, err
}

// Plan reads only the manifest and scope.
func (h *GenerationHandler) Plan(ctx context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	inner, err := h.inner.Plan(ctx, job)
	if err != nil {
		return nil, err
	}
	ok, err := h.planned(job, inner)
	if err != nil || !ok {
		return inner, err
	}
	return append([]analysis.TaskSpec{generationSpec()}, inner...), nil
}

// Run executes the generation task or delegates to the inner handler.
func (h *GenerationHandler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	if task.Key != GenerateTaskKey {
		return h.inner.Run(ctx, job, task, runner)
	}
	inner, err := h.inner.Plan(ctx, job)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	if ok, err := h.planned(job, inner); err != nil || !ok {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	if !job.AllowRepositoryCode {
		reason := pack.ReasonRepositoryCodeNotAllowed
		if job.RepositoryCodeWithheld {
			reason = pack.ReasonRepositoryCodeNotOffered
		}
		return notAllowed(job, reason)
	}
	r := Generate(ctx, job, runner, h.cfg.Generate)
	if len(r.Executions) > 0 {
		r.Limitations = append(r.Limitations, pack.LimRepositoryCodeExecuted)
	}
	return r
}

// notAllowed records that the generation did not run for want of the grant,
// so the analyzers can say why they stayed in reduced mode.
func notAllowed(job analysis.Job, reason string) analysis.TaskResult {
	lims := []string{reason, LimGenerationNotAllowed}
	rec := Generation{Kind: generateKind, SchemaVersion: generateVersion, Snapshot: job.Snapshot.Digest, AttemptID: job.AttemptID,
		Status: domain.TaskUnsupported, Reason: reason, Generator: "cmake", Projects: []GeneratedProject{}, Limitations: lims}
	dir := filepath.Join(job.Scratch, generateDir)
	b, err := json.Marshal(rec)
	if err != nil || !filepath.IsAbs(job.Scratch) || os.Mkdir(dir, 0o700) != nil || analysis.WriteFileExclusive(generationPath(job.Scratch), b) != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Limitations: []string{"record_write_failed"}}
	}
	return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: reason, Limitations: lims}
}
