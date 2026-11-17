package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/doctor"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "CODEYE foundation\nUsage: codeye doctor [--format text|json] [--work-dir .codeye/work] [--timeout 5s]\nDatabase: CODEYE_DATABASE_URL (optional). start/stop/agent are not implemented.")
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
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*format != "json" && *format != "text") || *timeout <= 0 || *timeout > time.Minute || *work == "" {
		fmt.Fprintln(stderr, "invalid doctor options; use codeye --help")
		return 2
	}
	report := doctor.Collect(ctx, doctor.Options{WorkDir: *work, DatabaseURL: os.Getenv("CODEYE_DATABASE_URL"), Timeout: *timeout}, doctor.NativeRunner{})
	if err := report.Write(out, *format); err != nil {
		fmt.Fprintln(stderr, "cannot write diagnostic report")
		return 2
	}
	return report.ExitCode()
}
