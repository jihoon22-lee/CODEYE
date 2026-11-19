package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/doctor"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return 0
	}
	if code, ok := runNodeAdmin(ctx, args, out, stderr); ok {
		return code
	}
	if code, ok := runNode(ctx, args, out, stderr); ok {
		return code
	}
	if args[0] == "model" {
		return modelCommand(ctx, args[1:], out, stderr)
	}
	if code, ok := runPersonal(ctx, args, out, stderr); ok {
		return code
	}
	if code, ok := runControl(ctx, args, out, stderr); ok {
		return code
	}
	if args[0] == "migrate" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "invalid migrate options")
			return 2
		}
		migrationCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := postgres.Migrate(migrationCtx, os.Getenv("CODEYE_DATABASE_URL")); err != nil {
			fmt.Fprintln(stderr, "migration failed:", err)
			return 1
		}
		fmt.Fprintln(out, "CODEYE database migrations applied")
		return 0
	}
	if args[0] != "doctor" {
		fmt.Fprintln(stderr, "unsupported command; use codeye --help")
		return 2
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	// Do not echo rejected arguments: they may contain credentials.
	flags.SetOutput(io.Discard)
	format := flags.String("format", "text", "")
	work := flags.String("work-dir", ".codeye/work", "")
	timeout := flags.Duration("timeout", 5*time.Second, "")
	config := flags.String("config", "", "")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*format != "json" && *format != "text") || *timeout <= 0 || *timeout > time.Minute || *work == "" {
		fmt.Fprintln(stderr, "invalid doctor options; use codeye --help")
		return 2
	}
	opts := doctor.Options{WorkDir: *work, DatabaseURL: os.Getenv("CODEYE_DATABASE_URL"), Timeout: *timeout}
	if *config != "" {
		// --config adds the personal operations section.
		cfg, err := personal.Load(*config, knownTools())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		opts.Personal = cfg
		if cfg.Managed() {
			// The managed cluster's URL when it runs (it is not started here).
			layout := completeConfig(cfg)
			if _, err := personalDatabase(ctx, cfg, layout, dbOptions{notices: stderr}); err == nil {
				opts.DatabaseURL = os.Getenv("CODEYE_DATABASE_URL")
			}
		}
	}
	report := doctor.Collect(ctx, opts, doctor.NativeRunner{})
	if err := report.Write(out, *format); err != nil {
		fmt.Fprintln(stderr, "cannot write diagnostic report")
		return 2
	}
	return report.ExitCode()
}
