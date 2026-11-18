// Package analysis defines the contract between the worker and analysis
// handlers (W06 contract §8): a handler plans tasks from an immutable W04
// snapshot and runs each task only through the W05 executor.Runner it is
// given. Handlers never import os/exec, never use the network and never
// assert cleanup; the worker derives cleanup from the executor results it
// recorded itself.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// HandlerInfo identifies a handler. Capability is "analysis:<profile>".
type HandlerInfo struct{ Name, Version, Capability string }

// Job is one attempt as seen by a handler.
type Job struct {
	// AllowModelReview is set from the node operator's explicit source grant.
	AllowModelReview bool
	RunID, AttemptID string
	Epoch            int64
	Profile          string
	Scope            domain.Scope
	// Snapshot.SnapshotDir is read-only; Snapshot.WorkspaceDir is a fresh
	// writable clone for this attempt. Snapshot.Digest is the manifest digest.
	Snapshot source.Snapshot
	// Scratch is the attempt-private writable directory; every file a
	// handler returns (execution streams, artifacts) must live inside it.
	Scratch string
}

// TaskSpec is one planned task.
type TaskSpec struct {
	Key      string // stable, ^[a-z][a-z0-9_.-]{0,63}$
	Required bool   // required_for_verdict
	Title    string
	// Stage is the display stage (docs/37 §2) sent with task_created. Empty
	// means "not recorded"; the server never infers it from Key.
	Stage domain.Stage
	// Weight is the optional estimate weight (1..1000) sent with task_created
	// for the weighted progress (W22). 0 means not declared (the server counts
	// the task as 1). Handlers must not claim a weight they have not measured.
	Weight int
}

// Execution is one executor.Runner.Run call made by a handler. Name is stable
// within the task (e.g. "go.vet"); StdoutPath/StderrPath are regular files in
// Job.Scratch holding the captured (already redacted) stream bytes.
type Execution struct {
	Name                   string
	Result                 executor.Result
	StdoutPath, StderrPath string
}

// ArtifactFile is an extra file produced by the handler, inside Job.Scratch.
type ArtifactFile struct{ Path, Role, MediaType string }

// TaskResult is the handler's structured outcome for one task. Status is never
// succeeded if a required execution did not exit, its output was
// truncated/unparsed, or the parser failed. The worker may still downgrade it.
type TaskResult struct {
	Status      domain.TaskStatus
	Reason      string
	Executions  []Execution
	Artifacts   []ArtifactFile
	Limitations []string
}

// Handler plans and runs the tasks of one capability.
type Handler interface {
	Info() HandlerInfo
	// Plan derives tasks from the job (snapshot manifest, scope) without
	// executing any process.
	Plan(ctx context.Context, job Job) ([]TaskSpec, error)
	// Run executes one planned task. Processes are started only via runner.
	Run(ctx context.Context, job Job, task TaskSpec, runner executor.Runner) TaskResult
}

// Media types and artifact roles shared with the worker and W09.
const (
	MediaResultManifest = "application/vnd.codeye.result-manifest+json"
	MediaObservations   = "application/vnd.codeye.observations+json"
	RoleResultManifest  = "result_manifest"
	RoleObservations    = "observations"
	RoleStdout          = "stdout"
	RoleStderr          = "stderr"
)

// Reasons used by handlers for task results.
const (
	ReasonAnalysisUnsupported = "analysis_unsupported"
	ReasonHandlerError        = "handler_error"
)

var (
	taskKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	namePattern       = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	capabilityPattern = regexp.MustCompile(`^analysis:[a-z][a-z0-9-]{0,63}$`)
)

// ValidTaskKey reports whether k is a valid task key (also used for execution names).
func ValidTaskKey(k string) bool { return taskKeyPattern.MatchString(k) }

// ErrInvalid wraps every validation error of this package.
var ErrInvalid = errors.New("analysis_invalid")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks a HandlerInfo.
func (i HandlerInfo) Validate() error {
	if !namePattern.MatchString(i.Name) {
		return invalidf("handler name %q is invalid", i.Name)
	}
	if i.Version == "" || len(i.Version) > 64 {
		return invalidf("handler %s version is invalid", i.Name)
	}
	if !capabilityPattern.MatchString(i.Capability) {
		return invalidf("handler %s capability %q is invalid", i.Name, i.Capability)
	}
	return nil
}

// Registry is an explicit, immutable set of handlers (no init() globals).
type Registry struct {
	byCapability map[string]Handler
	ordered      []Handler
}

// NewRegistry validates handlers; a nil handler, invalid info, duplicate
// capability or duplicate name is an error.
func NewRegistry(hs ...Handler) (*Registry, error) {
	r := &Registry{byCapability: map[string]Handler{}}
	names := map[string]bool{}
	for _, h := range hs {
		if h == nil {
			return nil, invalidf("nil handler")
		}
		info := h.Info()
		if err := info.Validate(); err != nil {
			return nil, err
		}
		if _, dup := r.byCapability[info.Capability]; dup {
			return nil, invalidf("duplicate capability %s", info.Capability)
		}
		if names[info.Name] {
			return nil, invalidf("duplicate handler name %s", info.Name)
		}
		names[info.Name] = true
		r.byCapability[info.Capability] = h
		r.ordered = append(r.ordered, h)
	}
	sort.Slice(r.ordered, func(a, b int) bool { return r.ordered[a].Info().Capability < r.ordered[b].Info().Capability })
	return r, nil
}

// Handlers returns the handlers sorted by capability.
func (r *Registry) Handlers() []Handler { return append([]Handler(nil), r.ordered...) }

// Infos returns the handler infos sorted by capability (heartbeat capabilities).
func (r *Registry) Infos() []HandlerInfo {
	out := make([]HandlerInfo, 0, len(r.ordered))
	for _, h := range r.ordered {
		out = append(out, h.Info())
	}
	return out
}

// Lookup returns the handler for a capability.
func (r *Registry) Lookup(capability string) (Handler, bool) {
	h, ok := r.byCapability[capability]
	return h, ok
}

// WriteExecution stores the captured streams of r as dir/<name>.stdout and
// dir/<name>.stderr (0600, created exclusively) and returns the Execution.
// dir must be absolute; callers keep it inside Job.Scratch.
func WriteExecution(dir, name string, r executor.Result) (Execution, error) {
	if !filepath.IsAbs(dir) {
		return Execution{}, invalidf("execution dir must be absolute")
	}
	if !ValidTaskKey(name) {
		return Execution{}, invalidf("execution name %q is invalid", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Execution{}, err
	}
	e := Execution{Name: name, Result: r, StdoutPath: filepath.Join(dir, name+".stdout"), StderrPath: filepath.Join(dir, name+".stderr")}
	if err := writeExclusive(e.StdoutPath, r.Stdout.Data); err != nil {
		return Execution{}, err
	}
	if err := writeExclusive(e.StderrPath, r.Stderr.Data); err != nil {
		return Execution{}, err
	}
	return e, nil
}

// WriteFileExclusive creates path (0600) and writes data; an existing file is an error.
func WriteFileExclusive(path string, data []byte) error { return writeExclusive(path, data) }

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
