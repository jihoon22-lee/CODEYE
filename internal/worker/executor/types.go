// Package executor runs one analyzer/tool process natively with explicit
// argv, cwd, env and limits, and reports whether the whole process tree was
// cleaned up. Native execution is not a security sandbox.
package executor

import (
	"context"
	"time"
)

// Status is the execution outcome of one Run. It never says "success":
// callers interpret ExitCode for exited processes.
type Status string

const (
	// StatusExited: the main process exited on its own before timeout/cancel.
	StatusExited Status = "exited"
	// StatusTimedOut: Spec.Timeout elapsed and the process tree was terminated.
	StatusTimedOut Status = "timed_out"
	// StatusCancelled: ctx was cancelled (or its deadline passed).
	StatusCancelled Status = "cancelled"
	// StatusRejected: Spec failed validation; nothing was started.
	StatusRejected Status = "rejected"
	// StatusStartFailed: the OS refused to start the executable.
	StatusStartFailed Status = "start_failed"
	// StatusUnsupported: a required capability (containment, enforced limits,
	// platform) is unavailable; the process was not run (or was terminated
	// before resuming).
	StatusUnsupported Status = "unsupported"
)

// Cleanup follows the cleanup_state vocabulary.
type Cleanup string

const (
	// CleanupConfirmed: the containment unit (Linux process group/session,
	// Windows Job Object) was observed empty after the run.
	CleanupConfirmed Cleanup = "confirmed"
	// CleanupUncertain: processes may still be alive; the caller must record
	// recovery_required and must not report the task as cleanly finished.
	CleanupUncertain Cleanup = "uncertain"
	// CleanupNotStarted: no process was created.
	CleanupNotStarted Cleanup = "not_started"
)

// Containment names the OS mechanism used to track the process tree.
type Containment string

const (
	ContainmentNone         Containment = "none"
	ContainmentProcessGroup Containment = "linux_session_process_group"
	ContainmentJobObject    Containment = "windows_job_object"
)

// ResourceRequest is the scheduler reservation/estimate for this execution.
// The executor never enforces it; see ResourceReport.Enforced.
type ResourceRequest struct {
	CPUMillis   int64 `json:"cpu_millis,omitempty"`
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
	// RequireEnforced makes Run return StatusUnsupported instead of running
	// with advisory-only limits.
	RequireEnforced bool `json:"require_enforced,omitempty"`
}

// Spec describes one process execution. All paths must be absolute.
type Spec struct {
	// TaskID/AttemptEpoch are copied into the Result for correlation only.
	TaskID       string `json:"task_id,omitempty"`
	AttemptEpoch int64  `json:"attempt_epoch,omitempty"`

	// Executable is an absolute path to a regular executable file. PATH is
	// never searched. Windows .bat/.cmd are rejected (implicit cmd.exe).
	Executable string `json:"executable"`
	// Args are argv[1:]; each element is passed literally without a shell.
	Args []string `json:"args"`

	// WorkspaceRoot is the approved writable working copy for this task.
	WorkspaceRoot string `json:"workspace_root"`
	// Dir is the cwd; after symlink resolution it must be WorkspaceRoot or
	// inside it.
	Dir string `json:"dir"`
	// ProtectedRoots are read-only snapshot/original source paths. Run is
	// rejected if Dir or WorkspaceRoot overlaps any of them.
	ProtectedRoots []string `json:"protected_roots,omitempty"`
	// TempParent is where the per-run temp/home directory is created
	// (default os.TempDir()). It must not overlap ProtectedRoots.
	TempParent string `json:"temp_parent,omitempty"`

	// Env is the complete explicit environment besides HOME/TMP variables
	// set by the executor. Nothing is inherited from the agent process. On
	// Windows callers normally pass SystemRoot (and PATH if needed). Keys
	// that look like credentials (tokens, SSH agent, DB URLs, model API keys)
	// are rejected.
	Env map[string]string `json:"-"`

	// OpenCodeModel selects the managed, tool-free OpenCode provider profile.
	// Other commands leave this empty. No provider credentials are inherited.
	OpenCodeModel string `json:"-"`
	// OpenCodeProvider is the provider the model is reached through (its
	// credential and endpoint, or the QA relay); nil for providers that
	// need no credential. Its key is masked like Redact values.
	OpenCodeProvider *ModelProvider `json:"-"`

	// Stdin bytes; nil means the null device.
	Stdin []byte `json:"-"`

	// Timeout is the wall-clock limit (required, > 0).
	Timeout time.Duration `json:"timeout"`
	// Grace is the SIGTERM→SIGKILL interval on Linux (default 2s). Windows
	// has no graceful phase: the Job is terminated immediately.
	Grace time.Duration `json:"grace,omitempty"`

	// StdoutLimit/StderrLimit cap captured bytes per stream (default 1 MiB).
	// Output beyond the cap is drained and counted but not kept.
	StdoutLimit int64 `json:"stdout_limit,omitempty"`
	StderrLimit int64 `json:"stderr_limit,omitempty"`

	// SensitiveArgs are indexes into Args replaced by "[REDACTED]" in the
	// recorded command. Redact values (and sensitive arg values) are masked,
	// longest first, in the recorded executable/argv/dir/temp paths and in
	// captured stdout/stderr. A cut stream drops a trailing partial secret.
	SensitiveArgs []int    `json:"-"`
	Redact        []string `json:"-"`

	Resources ResourceRequest `json:"resources"`

	// OnStarted, if set, is a notification sent after the process has been
	// started inside its containment unit (Windows: after resume). The
	// process may already have run code when it is called. It runs on its
	// own goroutine after the timeout is armed, so a slow or hung callback
	// never delays timeout/cancel handling. Its ctx is cancelled when Run
	// returns or the timeout/run ctx ends; callbacks must honour it,
	// otherwise the goroutine outlives Run. A non-nil error or a panic
	// terminates the process tree (Status cancelled, Reason
	// on_started_failed / on_started_panic).
	OnStarted func(context.Context, ProcessIdentity) error `json:"-"`
}

// ProcessIdentity identifies the started main process for journals.
type ProcessIdentity struct {
	PID int `json:"pid"`
	// StartTime is an OS-specific start marker (Linux: /proc stat
	// starttime clock ticks; Windows: creation FILETIME). Zero if unknown.
	StartTime uint64 `json:"start_time"`
}

// CommandRecord is safe-to-store metadata about what was executed.
type CommandRecord struct {
	// Executable is the alias given in Spec; ResolvedExecutable is the
	// symlink-resolved file that was actually executed (argv[0] keeps the
	// alias for multi-call tools).
	Executable         string   `json:"executable"`
	ResolvedExecutable string   `json:"resolved_executable,omitempty"`
	Argv               []string `json:"argv"` // redacted argv[0..]
	Dir                string   `json:"dir"`
	EnvKeys            []string `json:"env_keys"` // names only, sorted
	TempDir            string   `json:"temp_dir,omitempty"`
	// TempRetained: the per-run temp dir was kept (cleanup uncertain or
	// removal failed).
	TempRetained bool `json:"temp_retained,omitempty"`
}

// Stream is one captured output stream.
type Stream struct {
	Data       []byte `json:"-"`
	TotalBytes int64  `json:"total_bytes"`
	Truncated  bool   `json:"truncated"`
	// Incomplete is true when the reader did not reach EOF (pipe held open
	// by an escaped process).
	Incomplete bool `json:"incomplete,omitempty"`
}

// ResourceReport separates the request from what the OS enforced and what
// was observed. The executor enforces nothing: Enforced is always false.
type ResourceReport struct {
	Requested ResourceRequest `json:"requested"`
	Enforced  bool            `json:"enforced"`
	// Observed values; zero means not measured.
	UserCPU          time.Duration `json:"user_cpu"`
	SystemCPU        time.Duration `json:"system_cpu"`
	PeakMemoryBytes  int64         `json:"peak_memory_bytes"`
	PeakMemoryScope  string        `json:"peak_memory_scope,omitempty"` // main_process_rusage | job
	CPUScope         string        `json:"cpu_scope,omitempty"`
	GracefulStopUsed bool          `json:"graceful_stop_used"`
}

// Result of Run. Exactly one result channel: inspect Status and Cleanup.
type Result struct {
	TaskID       string      `json:"task_id,omitempty"`
	AttemptEpoch int64       `json:"attempt_epoch,omitempty"`
	Status       Status      `json:"status"`
	Reason       string      `json:"reason,omitempty"` // stable machine code
	Detail       string      `json:"detail,omitempty"` // human detail, no secrets
	ExitCode     *int        `json:"exit_code,omitempty"`
	Signal       string      `json:"signal,omitempty"`
	Cleanup      Cleanup     `json:"cleanup"`
	CleanupNote  string      `json:"cleanup_note,omitempty"`
	Containment  Containment `json:"containment"`
	// LeftoverKilled: the main process exited but other processes remained
	// in the containment unit and were terminated by the executor.
	LeftoverKilled bool `json:"leftover_killed,omitempty"`

	Process   ProcessIdentity `json:"process"`
	Command   CommandRecord   `json:"command"`
	Stdout    Stream          `json:"stdout"`
	Stderr    Stream          `json:"stderr"`
	Resources ResourceReport  `json:"resources"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at"`
}

// RecoveryRequired reports whether the caller must mark the attempt
// recovery_required instead of releasing it.
func (r Result) RecoveryRequired() bool { return r.Cleanup == CleanupUncertain }

// Executor runs Specs. The zero value is ready to use and safe for
// concurrent Runs.
type Executor struct {
	// KillWait bounds how long cleanup waits for the containment unit to
	// become empty after a forced kill (default 10s).
	KillWait time.Duration
}

// Runner is the interface analyzers should depend on.
type Runner interface {
	Run(ctx context.Context, spec Spec) Result
}

var _ Runner = (*Executor)(nil)
