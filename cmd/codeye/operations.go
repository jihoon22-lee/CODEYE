package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// operationsUsage documents the operator commands for recovery, retention
// and the user service.
const operationsUsage = `  codeye run recover --id RUN_ID --confirm-cleanup [--config PATH]   (a recovery_required run: you checked that none of its processes is left on the node)
  codeye cleanup [--config PATH | --artifact-dir DIR] [--agent-state-dir DIR] [--older-than 30d] [--apply] [--format text|json]   (dry run unless --apply)
  codeye service install|uninstall|print [--config PATH] [--name codeye]   (systemd user unit running codeye start/stop, Linux)
`

// runOperations handles run recover, cleanup and service. ok is false for
// other commands.
func runOperations(ctx context.Context, args []string, out, stderr io.Writer) (code int, ok bool) {
	switch {
	case len(args) >= 2 && args[0] == "run" && args[1] == "recover":
		return runRecover(ctx, args[2:], out, stderr), true
	case len(args) >= 1 && args[0] == "run":
		fmt.Fprintln(stderr, "missing run subcommand; use codeye --help")
		return 2, true
	case len(args) >= 1 && args[0] == "cleanup":
		return cleanupCommand(ctx, args[1:], out, stderr), true
	case len(args) >= 1 && args[0] == "service":
		return serviceCommand(ctx, args[1:], out, stderr), true
	}
	return 0, false
}

// runRecover is the operator CLI form of POST /runs/{id}/recover. The
// confirmation flag is required: CODEYE cannot see the node's processes, so
// the operator's statement is the evidence (audit run.recovery_confirmed,
// actor operator_cli). The work is queued again on behalf of the run's
// requester.
func runRecover(ctx context.Context, args []string, out, stderr io.Writer) int {
	var id, config string
	var confirmed bool
	if !parse("run recover", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&id, "id", "", "")
		fs.StringVar(&config, "config", "", "")
		fs.BoolVar(&confirmed, "confirm-cleanup", false, "")
	}) || id == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	if !domain.ValidID(id) {
		fmt.Fprintln(stderr, "--id must be a run ID")
		return 2
	}
	if !confirmed {
		fmt.Fprintln(stderr, "--confirm-cleanup is required: first check on the run's node that no process of its attempt is left (codeye status, ps), then confirm")
		return 2
	}
	with := withStore
	if config != "" || os.Getenv("CODEYE_DATABASE_URL") == "" {
		// Personal mode (--config, or the default configuration when no
		// database URL is set): the installation's own database.
		with = func(ctx context.Context, stderr io.Writer, f func(context.Context, *postgres.Store) int) int {
			args := []string{}
			if config != "" {
				args = append(args, "--config", config)
			}
			cfg, layout, ok := loadPersonalConfig("run recover", args, stderr, nil)
			if !ok {
				return 2
			}
			store, db, err := personalStore(ctx, cfg, layout, stderr)
			if err != nil {
				fmt.Fprintln(stderr, "database unavailable:", err)
				return 1
			}
			defer stopStartedDB(db)
			defer store.Close()
			return f(ctx, store)
		}
	}
	return with(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		rec, err := s.ConfirmRecovery(ctx, postgres.OperatorPrincipal(), id)
		switch {
		case errors.Is(err, domain.ErrConflict):
			fmt.Fprintln(stderr, "the run is not recovery_required; nothing to confirm")
			return 1
		case errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid):
			fmt.Fprintln(stderr, "run not found")
			return 1
		case err != nil:
			fmt.Fprintln(stderr, "cannot confirm recovery:", err)
			return 1
		}
		result := map[string]any{"confirmed_run": rec.Confirmed.ID, "attempt_id": rec.AttemptID, "epoch": rec.Epoch, "status": string(rec.Confirmed.Status), "retry": "not_queued"}
		if rec.Requeued != nil {
			result["retry"], result["run"], result["run_status"] = rec.How, rec.Requeued.ID, string(rec.Requeued.Status)
		} else {
			result["not_queued_reason"] = requeueReason(rec.RequeueError)
		}
		printJSON(out, result)
		return 0
	})
}

// requeueReason names why the confirmed run was not queued again.
func requeueReason(err error) string {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		return "requester_cannot_run_project"
	case errors.Is(err, domain.ErrConflict):
		return "run_cannot_be_repeated"
	default:
		return "store_unavailable"
	}
}
