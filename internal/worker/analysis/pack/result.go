package pack

import (
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Failed is a failed task result with reason and a copy of limitations.
func Failed(reason string, limitations ...string) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason, Limitations: append([]string{}, limitations...)}
}

// Stoppable reports a result after which a task starts no further
// execution: nothing ran (nil), cleanup is uncertain, or the process was
// cancelled, timed out, rejected, unsupported or never started.
func Stoppable(r *executor.Result) bool {
	return r == nil || r.Cleanup == executor.CleanupUncertain || r.Status == executor.StatusCancelled || r.Status == executor.StatusTimedOut ||
		r.Status == executor.StatusRejected || r.Status == executor.StatusUnsupported || r.Status == executor.StatusStartFailed
}

// ExitedZero reports a clean exit 0.
func ExitedZero(r *executor.Result) bool {
	return r != nil && r.Status == executor.StatusExited && r.ExitCode != nil && *r.ExitCode == 0
}

// ExitCode is the exit code of an exited process, else -1.
func ExitCode(r *executor.Result) int {
	if r != nil && r.Status == executor.StatusExited && r.ExitCode != nil {
		return *r.ExitCode
	}
	return -1
}

// StreamsComplete reports both streams read to EOF within their limits.
func StreamsComplete(r *executor.Result) bool {
	return r != nil && !r.Stdout.Truncated && !r.Stdout.Incomplete && !r.Stderr.Truncated && !r.Stderr.Incomplete
}
