package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task reasons.
const (
	ReasonScopeUnsupported    = "scope_unsupported"
	ReasonIntegrity           = "snapshot_integrity_failed"
	ReasonProbeIncomplete     = "environment_probe_incomplete"
	ReasonArtifactFailed      = "discovery_artifact_failed"
	ReasonSnapshotUnavailable = "snapshot_unavailable"
	ReasonCancelled           = "cancelled"
)

// Config selects how the node is observed and how much documentation is read.
type Config struct {
	// Observer reports the node's tools. Nil observes nothing: every tool is
	// not_probed.
	Observer Observer
	// Budget is the document read budget; zero means DefaultBudget.
	Budget Budget
}

// Handler adds the platform discovery tasks env.discover (stage environment)
// and docs.discover (stage documents) in front of an inner handler's plan.
// Both are required, run first, and never stop the inner tasks: a failed
// discovery task is reported with its reason while static analysis and code
// review continue (the worker runs every planned task).
type Handler struct {
	inner analysis.Handler
	cfg   Config
}

var _ analysis.Handler = (*Handler)(nil)

// Wrap returns inner with discovery tasks. The handler identity is inner's.
func Wrap(inner analysis.Handler, cfg Config) (*Handler, error) {
	if inner == nil {
		return nil, errors.New("discovery: nil inner handler")
	}
	if cfg.Budget == (Budget{}) {
		cfg.Budget = DefaultBudget
	}
	if !cfg.Budget.valid() {
		return nil, errors.New("discovery: invalid document budget")
	}
	return &Handler{inner: inner, cfg: cfg}, nil
}

// Tasks are the discovery task specs, in run order.
func Tasks() []analysis.TaskSpec {
	return []analysis.TaskSpec{
		{Key: envdoc.TaskEnvironment, Required: true, Title: "모듈·환경 탐색", Stage: domain.StageEnvironment},
		{Key: envdoc.TaskDocuments, Required: true, Title: "문서 탐색", Stage: domain.StageDocuments},
	}
}

// Info returns the inner handler identity.
func (h *Handler) Info() analysis.HandlerInfo { return h.inner.Info() }

// Plan prepends the discovery tasks. It reads only the manifest through the
// inner handler and never repository content, so no document can alter it.
func (h *Handler) Plan(ctx context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	inner, err := h.inner.Plan(ctx, job)
	if err != nil {
		return nil, err
	}
	for _, t := range inner {
		if t.Key == envdoc.TaskEnvironment || t.Key == envdoc.TaskDocuments {
			return nil, errors.New("discovery: inner handler planned a discovery task key")
		}
	}
	return append(Tasks(), inner...), nil
}

// Run executes a discovery task or delegates to the inner handler.
func (h *Handler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	switch task.Key {
	case envdoc.TaskEnvironment:
		return h.runEnvironment(ctx, job, runner)
	case envdoc.TaskDocuments:
		return h.runDocuments(ctx, job)
	}
	return h.inner.Run(ctx, job, task, runner)
}

func failed(reason string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason}
}

func input(job analysis.Job) Input {
	return Input{Manifest: job.Snapshot.Manifest, Digest: job.Snapshot.Digest, Scope: job.Scope, RunID: job.RunID, AttemptID: job.AttemptID}
}

func taskError(err error) analysis.TaskResult {
	switch {
	case errors.Is(err, errScope):
		return failed(ReasonScopeUnsupported)
	case errors.Is(err, errIntegrity):
		return failed(ReasonIntegrity)
	}
	return failed(analysis.ReasonHandlerError)
}

// verifiedReader reads included files from the read-only snapshot and
// rejects any byte that does not match the manifest.
func verifiedReader(root *os.Root) readFunc {
	return func(e source.Entry) ([]byte, error) {
		if e.Size == nil {
			return nil, errIntegrity
		}
		f, err := root.Open(filepath.FromSlash(e.Path))
		if err != nil {
			return nil, errIntegrity
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, *e.Size+1))
		if err != nil {
			return nil, errIntegrity
		}
		sum := sha256.Sum256(b)
		if int64(len(b)) != *e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
			return nil, errIntegrity
		}
		return b, nil
	}
}

func openSnapshot(job analysis.Job) (*os.Root, error) {
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.SnapshotDir) {
		return nil, errors.New("invalid job paths")
	}
	return os.OpenRoot(job.Snapshot.SnapshotDir)
}

// ArtifactPath is where the discovery task of kind writes its artifact in
// attempt scratch. Later roles of the same attempt (review) read it from
// there and validate it again with envdoc.Decode.
func ArtifactPath(scratch, kind string) string {
	return filepath.Join(scratch, "discovery", kind+".json")
}

// write stores a validated document in attempt scratch.
func write(job analysis.Job, doc envdoc.Document) (analysis.ArtifactFile, error) {
	b, err := envdoc.Marshal(doc, input(job).Expect(doc.Kind))
	if err != nil {
		return analysis.ArtifactFile{}, err
	}
	p := ArtifactPath(job.Scratch, doc.Kind)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return analysis.ArtifactFile{}, err
	}
	if err := analysis.WriteFileExclusive(p, b); err != nil {
		return analysis.ArtifactFile{}, err
	}
	return analysis.ArtifactFile{Path: p, Role: "other", MediaType: envdoc.MediaType}, nil
}

func (h *Handler) runEnvironment(ctx context.Context, job analysis.Job, runner executor.Runner) analysis.TaskResult {
	if ctx.Err() != nil {
		return analysis.TaskResult{Status: domain.TaskCancelled, Reason: ReasonCancelled}
	}
	root, err := openSnapshot(job)
	if err != nil {
		return failed(ReasonSnapshotUnavailable)
	}
	defer root.Close()
	observe := func(tools []string) Observed {
		if h.cfg.Observer == nil {
			return StaticObserver{}.Observe(ctx, job, tools, runner)
		}
		return h.cfg.Observer.Observe(ctx, job, tools, runner)
	}
	doc, observed, err := Environment(input(job), verifiedReader(root), observe)
	res := analysis.TaskResult{Executions: observed.Executions}
	if err != nil {
		r := taskError(err)
		r.Executions = res.Executions
		return r
	}
	if observed.Cancelled || ctx.Err() != nil {
		res.Status, res.Reason = domain.TaskCancelled, ReasonCancelled
		return res
	}
	art, err := write(job, doc)
	if err != nil {
		res.Status, res.Reason = domain.TaskFailed, ReasonArtifactFailed
		return res
	}
	res.Artifacts, res.Limitations = []analysis.ArtifactFile{art}, doc.Limitations
	res.Status = domain.TaskSucceeded
	if observed.Incomplete {
		// The worker rejects a succeeded task whose execution did not exit
		// cleanly; report the probe gap explicitly and keep the artifact.
		res.Status, res.Reason = domain.TaskFailed, ReasonProbeIncomplete
	}
	return res
}

func (h *Handler) runDocuments(ctx context.Context, job analysis.Job) analysis.TaskResult {
	if ctx.Err() != nil {
		return analysis.TaskResult{Status: domain.TaskCancelled, Reason: ReasonCancelled}
	}
	root, err := openSnapshot(job)
	if err != nil {
		return failed(ReasonSnapshotUnavailable)
	}
	defer root.Close()
	doc, err := Documents(input(job), h.cfg.Budget, verifiedReader(root))
	if err != nil {
		return taskError(err)
	}
	art, err := write(job, doc)
	if err != nil {
		return failed(ReasonArtifactFailed)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}, Limitations: doc.Limitations}
}
