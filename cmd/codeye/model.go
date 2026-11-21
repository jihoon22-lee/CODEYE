package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// Probe stages: where a failed probe stopped.
const (
	probeStageConfig     = "configuration" // no usable executable, model ID or timeout
	probeStageCredential = "credential"    // the credential or relay file
	probeStageOpenCode   = "opencode"      // the executable or its --version answer
	probeStageRequest    = "request"       // the provider request
	probeStageOutput     = "output"        // the reply did not pass the output checks
)

// modelProbeResult is what `codeye model probe` prints (never the key).
type modelProbeResult struct {
	Provider      string           `json:"provider"`
	Model         string           `json:"model"`
	Version       string           `json:"opencode_version,omitempty"`
	VersionTested bool             `json:"opencode_version_tested"`
	Verified      bool             `json:"verified"`
	Stage         string           `json:"stage,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	Cleanup       executor.Cleanup `json:"cleanup"`
}

// probeModel makes one live request with a synthetic input (2 + 3) and
// checks the answer. It is the same call a review task makes.
func probeModel(ctx context.Context, cfg opencode.Config) modelProbeResult {
	r := modelProbeResult{Provider: cfg.Provider(), Model: cfg.Model}
	dir, err := os.MkdirTemp("", "codeye-model-probe-")
	if err != nil {
		r.Stage, r.Reason, r.Cleanup = probeStageConfig, "workspace_unreachable", executor.CleanupNotStarted
		return r
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
	r.Version, r.VersionTested = reply.Version, reply.Version == opencode.Version
	r.Verified, r.Reason, r.Cleanup = reply.Reason == "", reply.Reason, reply.Cleanup
	if !r.Verified {
		r.Stage = probeStage(reply)
	}
	return r
}

func probeStage(reply opencode.Reply) string {
	switch reply.Reason {
	case "model_not_configured":
		return probeStageConfig
	case opencode.ReasonCredentialUnavailable, "model_relay_unavailable":
		return probeStageCredential
	case "opencode_version_unsupported", opencode.ReasonVersionTimeout:
		return probeStageOpenCode
	case "model_output_invalid", "model_output_incomplete", "workspace_unreachable", "policy_blocked":
		if reply.Version != "" {
			return probeStageOutput
		}
	}
	if reply.Version == "" {
		// The probe did not obtain an OpenCode version.
		return probeStageOpenCode
	}
	return probeStageRequest
}

// probeHints explain a failed probe in one line.
var probeHints = map[string]string{
	"model_not_configured":               "the OpenCode path, PROVIDER/MODEL or timeout is not usable",
	opencode.ReasonCredentialUnavailable: "the API key file is missing, readable by others (needs mode 0600) or has no key for this provider",
	"model_relay_unavailable":            "the relay ready file is missing or does not match the model",
	"opencode_version_unsupported":       "the executable did not answer --version like an OpenCode release",
	opencode.ReasonVersionTimeout:        "OpenCode did not answer --version within its startup limit",
	opencode.ReasonAuthFailed:            "the provider rejected the API key",
	opencode.ReasonModelNotFound:         "the provider does not serve this model (check agent.model.name)",
	"model_rate_limited":                 "the provider's rate or usage limit was reached",
	"model_timeout":                      "no answer within the per-call timeout (agent.model.timeout_seconds)",
	"model_unavailable":                  "OpenCode or the provider failed (network, endpoint or provider error)",
	"model_output_invalid":               "the model answered, but not with the requested JSON",
	"model_output_incomplete":            "the answer was cut off",
	"workspace_unreachable":              "the answer did not prove it read the request's workspace file",
	"policy_blocked":                     "the model tried to use a tool",
	"recovery_required":                  "the OpenCode process could not be confirmed stopped; its scratch directory is kept",
	"cancelled":                          "the probe was interrupted",
	"invalid_review_input":               "internal error: probe input rejected",
	"invalid_review_role":                "internal error: probe role rejected",
}

// text is the human form of r.
func (r modelProbeResult) text() string {
	var b strings.Builder
	version := r.Version
	switch {
	case version == "":
		version = "unknown version"
	case !r.VersionTested:
		version += " (not the tested " + opencode.Version + "; results are marked opencode_version_untested)"
	}
	fmt.Fprintf(&b, "model probe: provider %s, model %s, OpenCode %s\n", r.Provider, r.Model, version)
	if r.Verified {
		b.WriteString("model probe: OK - the model answered the test request correctly\n")
		return b.String()
	}
	hint := probeHints[r.Reason]
	if hint == "" {
		hint = "see docs/REVIEW.md#실패와-task-결과"
	}
	fmt.Fprintf(&b, "model probe: FAILED at %s: %s - %s\n", r.Stage, r.Reason, hint)
	b.WriteString("model probe: analysis still runs; model review is reported as not performed with this reason\n")
	return b.String()
}

// model probe is an explicit live request. doctor stays local and does not
// consume the model quota. With --executable/--model it probes exactly that
// configuration; otherwise the service configuration's model (as codeye
// start passes it to the agent).
func modelCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "probe" {
		fmt.Fprintln(stderr, "expected model probe [--config PATH] or model probe --executable PATH --model PROVIDER/MODEL [--credential-file PATH | --relay-file PATH]")
		return 2
	}
	flags := flag.NewFlagSet("model probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg opencode.Config
	var configPath string
	var asJSON bool
	flags.StringVar(&cfg.Executable, "executable", "", "")
	flags.StringVar(&cfg.Model, "model", "", "")
	flags.StringVar(&cfg.RelayFile, "relay-file", "", "")
	flags.StringVar(&cfg.CredentialFile, "credential-file", "", "")
	flags.DurationVar(&cfg.Timeout, "timeout", 0, "")
	flags.StringVar(&configPath, "config", "", "")
	flags.BoolVar(&asJSON, "json", false, "")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "invalid model probe options")
		return 2
	}
	explicit := cfg.Executable != "" || cfg.Model != "" || cfg.RelayFile != "" || cfg.CredentialFile != ""
	switch {
	case explicit && (cfg.Executable == "" || cfg.Model == "" || configPath != ""):
		fmt.Fprintln(stderr, "model probe needs both --executable and --model (or neither, to probe the service configuration)")
		return 2
	case !explicit:
		pcfg, layout, ok := loadInstanceConfig("model probe", configArgs(configPath), stderr, nil)
		if !ok {
			return 2
		}
		m, err := instance.ResolveModel(pcfg, layout, instanceModelEnv())
		if err != nil {
			fmt.Fprintln(stderr, "model not configured:", err)
			return 1
		}
		if m == nil {
			fmt.Fprintln(stderr, "no model configured: set OPENAI_API_KEY (or ANTHROPIC_API_KEY, OPENCODE_API_KEY) and run codeye up, or set agent.model")
			return 1
		}
		if m.Notice != "" {
			fmt.Fprintln(stderr, "model:", m.Notice)
		}
		timeout := cfg.Timeout
		cfg = m.Caller()
		if timeout != 0 {
			cfg.Timeout = timeout
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = opencode.DefaultTimeout
	}
	r := probeModel(ctx, cfg)
	if asJSON {
		if err := json.NewEncoder(out).Encode(r); err != nil {
			return 1
		}
	} else {
		fmt.Fprint(out, r.text())
	}
	if !r.Verified {
		return 1
	}
	return 0
}

func configArgs(path string) []string {
	if path == "" {
		return nil
	}
	return []string{"--config", path}
}
