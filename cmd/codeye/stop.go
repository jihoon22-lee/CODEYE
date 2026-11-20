package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Stop outcomes.
const (
	stopStopped          = "stopped"
	stopNotRunning       = "not_running"
	stopRecoveryRequired = "recovery_required"
	stopFailed           = "failed"
)

type procReport struct {
	Role   string `json:"role"`
	PID    int    `json:"pid,omitempty"`
	Before string `json:"state_before"`
	Action string `json:"action"`
	After  string `json:"state_after"`
}

type attemptReport struct {
	ID        string `json:"attempt_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Cleanup   string `json:"cleanup_state"`
	RunStatus string `json:"run_status"`
}

type stopReport struct {
	Outcome      string          `json:"outcome"`
	Processes    []procReport    `json:"processes"`
	InFlight     []attemptReport `json:"in_flight_at_stop"`
	AttemptsNow  []attemptReport `json:"attempts_after_stop"`
	AgentCleanup string          `json:"agent_cleanup"`
	// Database is what happened to the managed PostgreSQL: stopped,
	// not_running, left_running (a CODEYE process still runs), stop_failed;
	// external for database.mode external.
	Database string   `json:"database"`
	Notes    []string `json:"notes"`
}

func attemptReports(in []postgres.AttemptState) []attemptReport {
	out := []attemptReport{}
	for _, a := range in {
		out = append(out, attemptReport{ID: a.ID, RunID: a.RunID, Status: a.Status, Cleanup: a.Cleanup, RunStatus: a.RunStatus})
	}
	return out
}

func cmdStop(ctx context.Context, args []string, out, stderr io.Writer) int {
	var format string
	cfg, layout, ok := loadInstanceConfig("stop", args, stderr, func(fs *flag.FlagSet) { fs.StringVar(&format, "format", "text", "") })
	if !ok || (format != "text" && format != "json") {
		return 2
	}
	if err := instance.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := layout.Lock()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	rep := stopReport{InFlight: []attemptReport{}, AttemptsNow: []attemptReport{}, Notes: []string{}, AgentCleanup: "not_applicable", Database: "external"}
	state := layout.Inspect(probedListen(cfg), true)
	for _, issue := range state.Issues {
		switch issue {
		case instance.IssueAgentWithoutRecord:
			rep.Notes = append(rep.Notes, "an agent without a start record holds the agent state lock; it was not signalled")
		case instance.IssueListenWithoutRecord:
			rep.Notes = append(rep.Notes, "the listen address is used by a process without a start record; it was not signalled")
		}
	}
	// The database is optional for stop: without it the in-flight work is
	// unknown, which the report says.
	var store *postgres.Store
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	db, dberr := instanceDatabase(dctx, cfg, layout, dbOptions{notices: stderr})
	dcancel()
	if dberr != nil {
		rep.Notes = append(rep.Notes, "database: "+dberr.Error())
	}
	if os.Getenv("CODEYE_DATABASE_URL") != "" {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		store, err = openStore(sctx)
		cancel()
		if err != nil {
			store = nil
		}
	}
	if store != nil {
		defer func() {
			if store != nil {
				store.Close()
			}
		}()
	} else {
		rep.Notes = append(rep.Notes, "database unavailable: in-flight attempts were not read")
	}
	node, _ := layout.AgentNodeID()
	readAttempts := func(also []string) ([]postgres.AttemptState, bool) {
		if store == nil || node == "" {
			return nil, false
		}
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		a, err := store.NodeAttempts(qctx, node, also)
		return a, err == nil
	}

	agentStopped := true
	var agentRec, ctrlRec *instance.Record
	for _, p := range state.Processes {
		switch p.Role {
		case instance.RoleAgent:
			agentRec = p.Record
		case instance.RoleController:
			ctrlRec = p.Record
		}
	}
	anyRunning := false
	// 1. Drain the agent, then cancel on timeout; never kill it.
	ap := procReport{Role: instance.RoleAgent, Before: stateOf(agentRec), Action: "none"}
	if agentRec != nil {
		ap.PID = agentRec.PID
	}
	if ap.Before == instance.ProcRunning {
		anyRunning = true
		inflight, _ := readAttempts(nil)
		rep.InFlight = attemptReports(inflight)
		if format == "text" {
			fmt.Fprintf(out, "draining the agent (%d attempt(s) in flight; up to %ds)\n", len(inflight), cfg.Stop.DrainTimeoutSeconds)
		}
		ap.Action = signalAction(agentRec.Drain(), "drain_requested")
		if ap.Action == "drain_requested" && !waitGoneCtx(ctx, agentRec, time.Duration(cfg.Stop.DrainTimeoutSeconds)*time.Second) {
			if format == "text" {
				fmt.Fprintf(out, "drain timeout: cancelling running work (up to %ds)\n", cfg.Stop.CancelTimeoutSeconds)
			}
			ap.Action = signalAction(agentRec.Terminate(), "cancelled_after_drain_timeout")
			waitGoneCtx(context.Background(), agentRec, time.Duration(cfg.Stop.CancelTimeoutSeconds)*time.Second)
		}
		ap.After = agentRec.State()
		if ap.After == instance.ProcRunning {
			agentStopped = false
			rep.Notes = append(rep.Notes, "the agent is still running; it was not killed and the controller was left running so the agent can still report cleanup")
		} else {
			// The verdict comes from the agent's own journal, not its log.
			problem := journalProblem(layout)
			if problem == "" {
				rep.AgentCleanup = "confirmed"
			} else {
				rep.AgentCleanup = "uncertain"
				rep.Notes = append(rep.Notes, "agent journal "+problem)
			}
		}
	} else {
		ap.After = ap.Before
	}
	if agentStopped && agentRec != nil && ap.After != instance.ProcRunning && ap.After != instance.ProcUnknown {
		_ = layout.RemoveRecord(instance.RoleAgent)
	}
	rep.Processes = append(rep.Processes, ap)

	// 2. The controller, only after the agent is gone.
	cp := procReport{Role: instance.RoleController, Before: stateOf(ctrlRec), Action: "none"}
	if ctrlRec != nil {
		cp.PID = ctrlRec.PID
	}
	if ids := attemptIDs(rep.InFlight); agentStopped {
		if now, ok := readAttempts(ids); ok {
			rep.AttemptsNow = attemptReports(now)
		}
	}
	if cp.Before == instance.ProcRunning {
		anyRunning = true
		if agentStopped {
			cp.Action = signalAction(ctrlRec.Terminate(), "terminated")
			waitGoneCtx(context.Background(), ctrlRec, instanceTimings.controllerStop)
		} else {
			cp.Action = "left_running"
		}
	}
	cp.After = stateOf(ctrlRec)
	if ctrlRec != nil && cp.After != instance.ProcRunning && cp.After != instance.ProcUnknown {
		_ = layout.RemoveRecord(instance.RoleController)
	}
	rep.Processes = append(rep.Processes, cp)

	// 3. The managed PostgreSQL, only when no CODEYE process is left.
	pgFailed := false
	if db != nil && db.managed {
		switch {
		case !db.running:
			rep.Database = "not_running"
		case !agentStopped || cp.After == instance.ProcRunning:
			rep.Database = "left_running"
		default:
			if store != nil {
				store.Close()
				store = nil
			}
			pctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if err := db.stop(pctx); err != nil {
				rep.Database = "stop_failed"
				rep.Notes = append(rep.Notes, "PostgreSQL did not stop: "+err.Error())
				pgFailed = true
			} else {
				rep.Database = "stopped"
				anyRunning = true
			}
			cancel()
		}
	}

	switch {
	case !agentStopped:
		rep.Outcome = stopRecoveryRequired
	case cp.After == instance.ProcRunning:
		rep.Outcome = stopFailed
		rep.Notes = append(rep.Notes, "the controller did not stop; it was not killed")
	case pgFailed:
		rep.Outcome = stopFailed
	case rep.AgentCleanup == "uncertain":
		rep.Outcome = stopRecoveryRequired
	case activeAttempts(rep.AttemptsNow):
		rep.Outcome = stopRecoveryRequired
		rep.Notes = append(rep.Notes, "attempts of this node are still leased or recovery_required in the database")
	case !anyRunning:
		rep.Outcome = stopNotRunning
	default:
		rep.Outcome = stopStopped
	}
	if format == "json" {
		printJSON(out, rep)
	} else {
		for _, p := range rep.Processes {
			fmt.Fprintf(out, "%-10s %-12s -> %-12s action=%s\n", p.Role, p.Before, p.After, p.Action)
		}
		if rep.Database != "external" {
			fmt.Fprintf(out, "%-10s %s\n", "postgres", rep.Database)
		}
		for _, a := range rep.AttemptsNow {
			fmt.Fprintf(out, "attempt %s run %s: %s cleanup=%s run=%s\n", a.ID, a.RunID, a.Status, a.Cleanup, a.RunStatus)
		}
		for _, n := range rep.Notes {
			fmt.Fprintln(out, "note:", n)
		}
		fmt.Fprintln(out, "outcome:", rep.Outcome)
	}
	if rep.Outcome == stopStopped || rep.Outcome == stopNotRunning {
		return 0
	}
	return 1
}

func stateOf(r *instance.Record) string {
	if r == nil {
		return instance.ProcNone
	}
	return r.State()
}

func attemptIDs(in []attemptReport) []string {
	out := []string{}
	for _, a := range in {
		out = append(out, a.ID)
	}
	return out
}

func activeAttempts(in []attemptReport) bool {
	for _, a := range in {
		if a.Status == "leased" || a.Status == "recovery_required" {
			return true
		}
	}
	return false
}

// signalAction names the result of a verified signal.
func signalAction(err error, sent string) string {
	switch {
	case err == nil:
		return sent
	case errors.Is(err, instance.ErrProcessGone):
		return "already_exited"
	case errors.Is(err, instance.ErrProcessReused):
		return "not_signalled_pid_reused"
	case errors.Is(err, instance.ErrSignalUnsupported):
		return "not_signalled_unsupported"
	}
	return "signal_failed"
}

func waitGoneCtx(ctx context.Context, r *instance.Record, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if r.State() != instance.ProcRunning {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(instanceTimings.poll)
	}
}
