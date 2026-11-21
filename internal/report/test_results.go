package report

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const (
	TestNotRun  = "not_run"
	TestPassed  = "passed"
	TestFailed  = "failed"
	TestPartial = "partial"
)

func testTaskKey(key string) bool { return strings.HasPrefix(key, "test.") }

func testExecutions(executions []Execution) map[string]Execution {
	out := map[string]Execution{}
	for _, execution := range executions {
		if testTaskKey(execution.TaskKey) && execution.Name == "test.run" {
			out[execution.TaskKey] = execution
		}
	}
	return out
}

func confirmedTestExit(execution Execution) bool {
	return execution.Status == string(executor.StatusExited) && execution.Cleanup == string(executor.CleanupConfirmed) &&
		execution.ExitCode != nil && *execution.ExitCode >= 0
}

func testResult(task Task, execution Execution) string {
	if !confirmedTestExit(execution) {
		return TestNotRun
	}
	switch {
	case task.Status == string(domain.TaskSucceeded) && *execution.ExitCode == 0:
		return TestPassed
	case task.Status == string(domain.TaskFailed) && *execution.ExitCode != 0 && deref(task.Reason) != "no_tests_executed":
		return TestFailed
	default:
		return TestNotRun
	}
}

func overallTestStatus(tasks []Task, executions map[string]Execution) string {
	passed, selected := 0, 0
	for _, task := range tasks {
		if !testTaskKey(task.Key) {
			continue
		}
		selected++
		switch testResult(task, executions[task.Key]) {
		case TestFailed:
			return TestFailed
		case TestPassed:
			passed++
		}
	}
	switch {
	case passed == 0:
		return TestNotRun
	case passed == selected:
		return TestPassed
	default:
		return TestPartial
	}
}

func (v secView) TestRunPart() secPart {
	r := v.R
	if r.Inputs.Profile != "with-tests" {
		return secPart{}
	}
	l := v.L
	executions := testExecutions(r.Executions)
	status := lab(l, "tests.summary", r.Coverage.TestStatus)
	notStarted := len(executions) == 0
	reasons := map[string]bool{}
	table := &secTable{Head: []string{l.Label("tests.suite"), l.Label("tests.directory"), l.Label("col.status"), l.Label("tests.exit"), l.Label("col.reason")}}
	for _, task := range r.Tasks {
		if !testTaskKey(task.Key) {
			continue
		}
		switch domain.TaskStatus(task.Status) {
		case domain.TaskSucceeded, domain.TaskFailed, domain.TaskTimedOut, domain.TaskCancelled:
			notStarted = false
		}
		execution := executions[task.Key]
		status := testResult(task, execution)
		label := lab(l, "tests", status)
		if status == TestNotRun {
			label = lab(l, "task_status", task.Status)
			if task.Status == string(domain.TaskSucceeded) {
				label = l.Label("tests.execution_unverified")
			}
		}
		reason := "-"
		if task.Reason != nil {
			reason = reasonText(l, *task.Reason)
		}
		reasons[reason] = true
		kind, _, _ := strings.Cut(strings.TrimPrefix(task.Key, "test."), ".")
		suite := lab(l, "tests.suite", kind) + " (" + task.Key + ")"
		directory := execution.Cwd
		if directory == "[REDACTED]" || directory != "." && domain.ValidateEvidencePath(directory) != nil {
			directory = "-"
		}
		table.Rows = append(table.Rows, []secCell{txt(suite), code(directory), txt(label), txt(optInt(execution.ExitCode, "-")), txt(reason)})
	}
	if r.Coverage.TestStatus == TestNotRun {
		if notStarted {
			status = l.Label("tests.not_started")
		}
		if len(reasons) == 1 && !reasons["-"] {
			for reason := range reasons {
				status += " · " + reason
			}
		}
	}
	return secPart{Fold: status, Table: table}
}
