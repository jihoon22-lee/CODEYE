package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// model probe is an explicit live request. doctor stays local and does not
// consume the model quota.
func modelCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "probe" {
		fmt.Fprintln(stderr, "expected model probe --executable PATH --model PROVIDER/MODEL")
		return 2
	}
	flags := flag.NewFlagSet("model probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg opencode.Config
	flags.StringVar(&cfg.Executable, "executable", "", "")
	flags.StringVar(&cfg.Model, "model", "", "")
	flags.StringVar(&cfg.RelayFile, "relay-file", "", "")
	flags.DurationVar(&cfg.Timeout, "timeout", 75*time.Second, "")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || cfg.Executable == "" || cfg.Model == "" {
		fmt.Fprintln(stderr, "invalid model probe options")
		return 2
	}
	dir, err := os.MkdirTemp("", "codeye-model-probe-")
	if err != nil {
		fmt.Fprintln(stderr, "cannot prepare model probe directory")
		return 1
	}
	// An uncertain run may still own its private scratch directory.
	reply := cfg.Review(ctx, &executor.Executor{}, opencode.Request{Scratch: dir, SnapshotDigest: strings.Repeat("0", 64), Role: "probe",
		Instructions: "Return result.sum as the integer sum of a and b.", Input: json.RawMessage(`{"a":2,"b":3}`)})
	if reply.Cleanup != executor.CleanupUncertain {
		if err := os.RemoveAll(dir); err != nil {
			reply.Reason = "recovery_required"
			reply.Cleanup = executor.CleanupUncertain
		}
	}
	var result struct{ Sum int }
	if reply.Reason == "" && (json.Unmarshal(reply.Output, &result) != nil || result.Sum != 5) {
		reply.Reason = "model_output_invalid"
	}
	summary := struct {
		Model    string           `json:"model"`
		Version  string           `json:"opencode_version,omitempty"`
		Verified bool             `json:"verified"`
		Reason   string           `json:"reason,omitempty"`
		Cleanup  executor.Cleanup `json:"cleanup"`
	}{cfg.Model, reply.Version, reply.Reason == "", reply.Reason, reply.Cleanup}
	if err := json.NewEncoder(out).Encode(summary); err != nil || !summary.Verified {
		return 1
	}
	return 0
}
