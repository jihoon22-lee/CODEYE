package domain

// RunStatus is the run lifecycle state (docs/ARCHITECTURE.md#상태-전이).
type RunStatus string

const (
	RunQueued           RunStatus = "queued"
	RunPreparing        RunStatus = "preparing"
	RunRunning          RunStatus = "running"
	RunFinalizing       RunStatus = "finalizing"
	RunCancelRequested  RunStatus = "cancel_requested"
	RunCancelling       RunStatus = "cancelling"
	RunRecoveryRequired RunStatus = "recovery_required"
	RunCompleted        RunStatus = "completed"
	RunPartial          RunStatus = "partial"
	RunFailed           RunStatus = "failed"
	RunCancelled        RunStatus = "cancelled"
)

// Valid reports whether s is a known run status.
func (s RunStatus) Valid() bool {
	_, ok := runTransitions[s]
	return ok
}

// Terminal reports whether s is a final state that can no longer change.
func (s RunStatus) Terminal() bool {
	switch s {
	case RunCompleted, RunPartial, RunFailed, RunCancelled:
		return true
	}
	return false
}

// runTransitions lists the allowed targets of each state. Notes:
//   - completed/partial are reachable only from finalizing.
//   - queued/preparing -> failed covers pre-acquire failures (no snapshot yet).
//   - queued -> cancelled is a shortcut valid only when the service has
//     confirmed no execution child exists; the guard is the caller's duty.
//   - recovery_required -> queued is an explicit, guarded service retry; it is
//     never an automatic requeue.
var runTransitions = map[RunStatus][]RunStatus{
	RunQueued:           {RunPreparing, RunFailed, RunCancelled, RunCancelRequested},
	RunPreparing:        {RunRunning, RunFailed, RunCancelRequested, RunRecoveryRequired},
	RunRunning:          {RunFinalizing, RunFailed, RunCancelRequested, RunRecoveryRequired},
	RunFinalizing:       {RunCompleted, RunPartial, RunFailed, RunCancelRequested, RunRecoveryRequired},
	RunCancelRequested:  {RunCancelling, RunRecoveryRequired},
	RunCancelling:       {RunCancelled, RunRecoveryRequired},
	RunRecoveryRequired: {RunQueued, RunFailed},
	RunCompleted:        nil,
	RunPartial:          nil,
	RunFailed:           nil,
	RunCancelled:        nil,
}

// CanTransitionRun reports whether a run may move from one state to another.
// Same-state, unknown-state and terminal-source transitions are rejected.
func CanTransitionRun(from, to RunStatus) bool {
	return canTransition(runTransitions, from, to)
}

// TaskStatus is the task state (docs/ARCHITECTURE.md#상태-전이).
type TaskStatus string

const (
	TaskQueued          TaskStatus = "queued"
	TaskBlocked         TaskStatus = "blocked"
	TaskRunning         TaskStatus = "running"
	TaskSucceeded       TaskStatus = "succeeded"
	TaskFailed          TaskStatus = "failed"
	TaskTimedOut        TaskStatus = "timed_out"
	TaskCancelled       TaskStatus = "cancelled"
	TaskUnsupported     TaskStatus = "unsupported"
	TaskSkippedPolicy   TaskStatus = "skipped_policy"
	TaskBudgetExhausted TaskStatus = "budget_exhausted"
)

// Valid reports whether s is a known task status.
func (s TaskStatus) Valid() bool {
	_, ok := taskTransitions[s]
	return ok
}

// Terminal reports whether s is a final state. blocked is not terminal: it
// waits for prerequisites. unsupported and skipped_policy are terminal but are
// never counted as successful checks.
func (s TaskStatus) Terminal() bool {
	switch s {
	case TaskSucceeded, TaskFailed, TaskTimedOut, TaskCancelled,
		TaskUnsupported, TaskSkippedPolicy, TaskBudgetExhausted:
		return true
	}
	return false
}

// A running task never returns to queued here: a retry after a lost attempt is
// a new execution, decided by the service, not a status rollback.
var taskTransitions = map[TaskStatus][]TaskStatus{
	TaskQueued:          {TaskRunning, TaskBlocked, TaskCancelled, TaskUnsupported, TaskSkippedPolicy, TaskBudgetExhausted},
	TaskBlocked:         {TaskQueued, TaskFailed, TaskCancelled, TaskUnsupported, TaskSkippedPolicy, TaskBudgetExhausted},
	TaskRunning:         {TaskSucceeded, TaskFailed, TaskTimedOut, TaskCancelled, TaskBudgetExhausted, TaskUnsupported, TaskSkippedPolicy},
	TaskSucceeded:       nil,
	TaskFailed:          nil,
	TaskTimedOut:        nil,
	TaskCancelled:       nil,
	TaskUnsupported:     nil,
	TaskSkippedPolicy:   nil,
	TaskBudgetExhausted: nil,
}

// CanTransitionTask reports whether a task may move from one state to another.
// Same-state, unknown-state and terminal-source transitions are rejected.
func CanTransitionTask(from, to TaskStatus) bool {
	return canTransition(taskTransitions, from, to)
}

func canTransition[S comparable](table map[S][]S, from, to S) bool {
	for _, next := range table[from] {
		if next == to {
			return true
		}
	}
	return false
}
