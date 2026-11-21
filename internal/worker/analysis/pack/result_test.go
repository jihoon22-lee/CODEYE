package pack

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestExecutionOutcomeAcrossProcesses(t *testing.T) {
	refused := &executor.Result{Status: executor.StatusRejected}
	missing := &executor.Result{Status: executor.StatusStartFailed}
	timeout := &executor.Result{Status: executor.StatusTimedOut}
	cancel := &executor.Result{Status: executor.StatusCancelled}
	uncertain := &executor.Result{Cleanup: executor.CleanupUncertain}
	for _, tc := range []struct {
		name          string
		results       []*executor.Result
		cancelled     bool
		status        domain.TaskStatus
		reason, class string
	}{
		{"analyzer handles missing result", []*executor.Result{nil}, false, domain.TaskSucceeded, "", ""},
		{"first process refusal", []*executor.Result{nil, refused, missing}, false, domain.TaskFailed, ReasonExecutionRejected, obsdoc.ExitNotRun},
		{"later timeout", []*executor.Result{missing, timeout}, false, domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout},
		{"cancelled attempt", []*executor.Result{timeout}, true, domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled},
		{"cancelled process", []*executor.Result{timeout, cancel}, false, domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled},
		{"unconfirmed cleanup", []*executor.Result{cancel, uncertain}, true, domain.TaskFailed, ReasonCleanupUncertain, obsdoc.ExitExecutionError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, class := ExecutionOutcome(tc.results, tc.cancelled)
			if status != tc.status || reason != tc.reason || class != tc.class {
				t.Fatalf("got (%s, %q, %q), want (%s, %q, %q)", status, reason, class, tc.status, tc.reason, tc.class)
			}
		})
	}
}
