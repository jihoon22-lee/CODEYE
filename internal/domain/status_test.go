package domain

import "testing"

var allRun = []RunStatus{RunQueued, RunPreparing, RunRunning, RunFinalizing, RunCancelRequested,
	RunCancelling, RunRecoveryRequired, RunCompleted, RunPartial, RunFailed, RunCancelled}

var allTask = []TaskStatus{TaskQueued, TaskBlocked, TaskRunning, TaskSucceeded, TaskFailed,
	TaskTimedOut, TaskCancelled, TaskUnsupported, TaskSkippedPolicy, TaskBudgetExhausted}

func TestStatusValid(t *testing.T) {
	for _, s := range allRun {
		if !s.Valid() {
			t.Errorf("run %q should be valid", s)
		}
	}
	for _, s := range allTask {
		if !s.Valid() {
			t.Errorf("task %q should be valid", s)
		}
	}
	for _, bad := range []string{"", "Queued", "done", "cancel", " queued"} {
		if RunStatus(bad).Valid() || TaskStatus(bad).Valid() {
			t.Errorf("%q should be invalid", bad)
		}
	}
	// A run-only status is not a task status and vice versa.
	if TaskStatus(RunPreparing).Valid() || RunStatus(TaskBlocked).Valid() {
		t.Error("run and task enums must not overlap beyond shared names")
	}
}

func TestStatusTerminal(t *testing.T) {
	runTerminal := map[RunStatus]bool{RunCompleted: true, RunPartial: true, RunFailed: true, RunCancelled: true}
	for _, s := range allRun {
		if s.Terminal() != runTerminal[s] {
			t.Errorf("run %q Terminal()=%v", s, s.Terminal())
		}
	}
	if RunStatus("bogus").Terminal() || TaskStatus("bogus").Terminal() {
		t.Error("unknown status must not be terminal")
	}
	nonTerminal := map[TaskStatus]bool{TaskQueued: true, TaskBlocked: true, TaskRunning: true}
	for _, s := range allTask {
		if s.Terminal() == nonTerminal[s] {
			t.Errorf("task %q Terminal()=%v", s, s.Terminal())
		}
	}
}

func TestRunTransitions(t *testing.T) {
	allowed := []struct{ from, to RunStatus }{
		{RunQueued, RunPreparing},
		{RunQueued, RunFailed},    // pre-acquire failure
		{RunQueued, RunCancelled}, // service-guarded shortcut
		{RunPreparing, RunRunning},
		{RunPreparing, RunFailed},
		{RunRunning, RunFinalizing},
		{RunFinalizing, RunCompleted},
		{RunFinalizing, RunPartial},
		{RunFinalizing, RunFailed},
		{RunQueued, RunCancelRequested},
		{RunPreparing, RunCancelRequested},
		{RunRunning, RunCancelRequested},
		{RunFinalizing, RunCancelRequested},
		{RunCancelRequested, RunCancelling},
		{RunCancelling, RunCancelled},
		{RunPreparing, RunRecoveryRequired},
		{RunRunning, RunFailed},
		{RunRunning, RunRecoveryRequired},
		{RunFinalizing, RunRecoveryRequired},
		{RunCancelRequested, RunRecoveryRequired},
		{RunCancelling, RunRecoveryRequired},
		{RunRecoveryRequired, RunQueued}, // explicit guarded retry
		{RunRecoveryRequired, RunFailed},
	}
	allow := map[[2]RunStatus]bool{}
	for _, c := range allowed {
		allow[[2]RunStatus{c.from, c.to}] = true
		if !CanTransitionRun(c.from, c.to) {
			t.Errorf("run %s -> %s should be allowed", c.from, c.to)
		}
	}
	// Everything not listed is rejected, which covers same-state, terminal
	// sources, and unsupported shortcuts such as running -> completed.
	for _, from := range allRun {
		for _, to := range allRun {
			if want := allow[[2]RunStatus{from, to}]; CanTransitionRun(from, to) && !want {
				t.Errorf("run %s -> %s unexpectedly allowed", from, to)
			}
		}
	}
}

func TestRunTransitionRules(t *testing.T) {
	for _, s := range allRun {
		if CanTransitionRun(s, s) {
			t.Errorf("same-state %s must be rejected", s)
		}
		if s.Terminal() {
			for _, to := range allRun {
				if CanTransitionRun(s, to) {
					t.Errorf("terminal %s must not move to %s", s, to)
				}
			}
		}
	}
	// completed/partial only from finalizing.
	for _, to := range []RunStatus{RunCompleted, RunPartial} {
		for _, from := range allRun {
			if got, want := CanTransitionRun(from, to), from == RunFinalizing; got != want {
				t.Errorf("%s -> %s = %v, want %v", from, to, got, want)
			}
		}
	}
	// recovery_required never reaches an active worker state or success directly.
	for _, to := range []RunStatus{RunPreparing, RunRunning, RunFinalizing, RunCompleted, RunPartial, RunCancelRequested, RunCancelling, RunCancelled} {
		if CanTransitionRun(RunRecoveryRequired, to) {
			t.Errorf("recovery_required -> %s must be rejected", to)
		}
	}
	// Unknown states never transition.
	if CanTransitionRun("bogus", RunQueued) || CanTransitionRun(RunQueued, "bogus") || CanTransitionRun("", "") {
		t.Error("unknown statuses must be rejected")
	}
}

func TestTaskTransitions(t *testing.T) {
	allowed := []struct{ from, to TaskStatus }{
		{TaskQueued, TaskRunning}, {TaskQueued, TaskBlocked}, {TaskQueued, TaskCancelled},
		{TaskQueued, TaskUnsupported}, {TaskQueued, TaskSkippedPolicy}, {TaskQueued, TaskBudgetExhausted},
		{TaskBlocked, TaskQueued}, {TaskBlocked, TaskFailed}, {TaskBlocked, TaskSkippedPolicy},
		{TaskBlocked, TaskCancelled}, {TaskBlocked, TaskUnsupported}, {TaskBlocked, TaskBudgetExhausted},
		{TaskRunning, TaskSucceeded}, {TaskRunning, TaskFailed}, {TaskRunning, TaskTimedOut},
		{TaskRunning, TaskCancelled}, {TaskRunning, TaskBudgetExhausted}, {TaskRunning, TaskUnsupported}, {TaskRunning, TaskSkippedPolicy},
	}
	allow := map[[2]TaskStatus]bool{}
	for _, c := range allowed {
		allow[[2]TaskStatus{c.from, c.to}] = true
		if !CanTransitionTask(c.from, c.to) {
			t.Errorf("task %s -> %s should be allowed", c.from, c.to)
		}
	}
	for _, from := range allTask {
		for _, to := range allTask {
			if CanTransitionTask(from, to) && !allow[[2]TaskStatus{from, to}] {
				t.Errorf("task %s -> %s unexpectedly allowed", from, to)
			}
		}
	}
}

func TestTaskTransitionRules(t *testing.T) {
	for _, s := range allTask {
		if CanTransitionTask(s, s) {
			t.Errorf("same-state %s must be rejected", s)
		}
		if s.Terminal() {
			for _, to := range allTask {
				if CanTransitionTask(s, to) {
					t.Errorf("terminal %s must not move to %s", s, to)
				}
			}
		}
	}
	// A task cannot succeed without having run, and unsupported/skipped never
	// become succeeded.
	for _, from := range allTask {
		if from != TaskRunning && CanTransitionTask(from, TaskSucceeded) {
			t.Errorf("%s -> succeeded must be rejected", from)
		}
	}
	if CanTransitionTask(TaskRunning, TaskQueued) || CanTransitionTask(TaskRunning, TaskBlocked) {
		t.Error("running must not be rolled back")
	}
	if CanTransitionTask("bogus", TaskQueued) || CanTransitionTask(TaskQueued, "bogus") {
		t.Error("unknown statuses must be rejected")
	}
}
