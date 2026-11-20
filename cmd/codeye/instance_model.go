package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// instanceModelEnv is what the service reads to find the model besides
// the configuration (tests replace it).
var instanceModelEnv = instance.SystemModelEnv

// modelAgentArgs are the agent's model flags in the service: the model
// resolved from agent.model and the credential files
// (instance.ResolveModel). Which projects' source the model may read is each
// project's choice (--project-analysis-policy). Without a usable model it
// returns no flags; review tasks are then unsupported (model_not_configured)
// and static analysis runs as usual.
func modelAgentArgs(cfg *instance.Config, layout instance.Layout) []string {
	m, err := instance.ResolveModel(cfg, layout, instanceModelEnv())
	if err != nil || m == nil {
		return nil
	}
	args := []string{"--opencode", m.OpenCode, "--model", m.ID}
	switch {
	case m.RelayFile != "":
		args = append(args, "--model-relay-file", m.RelayFile)
	case m.CredentialFile != "":
		args = append(args, "--model-credential-file", m.CredentialFile)
	}
	if m.Timeout != opencode.DefaultTimeout {
		args = append(args, "--model-timeout", m.Timeout.String())
	}
	if m.ReviewMaxCalls != 0 {
		args = append(args, "--review-max-calls", strconv.Itoa(m.ReviewMaxCalls))
	}
	return args
}

// setupInstanceModel is the model step of `codeye up`: it writes
// <state>/secrets/model.json from OPENAI_API_KEY, ANTHROPIC_API_KEY or
// OPENCODE_API_KEY when one is set, resolves the model like `codeye start`
// and, unless noProbe, probes it once when the model configuration differs
// from the last probe. It prints what it did to out and never prints the
// key. A failed probe is reported and does not stop `up`: static analysis
// runs, and the report says why model review was not performed.
func setupInstanceModel(ctx context.Context, cfg *instance.Config, layout instance.Layout, noProbe bool, out io.Writer) *instance.ResolvedModel {
	// Offline, a key from the environment is for a provider's public endpoint:
	// it is neither stored nor used, and the key file stays as it is.
	var setup instance.ModelSetup
	var err error
	if !cfg.Offline {
		setup, err = instance.WriteModelSecretFromEnv(layout, os.Getenv)
	}
	switch {
	case err != nil:
		fmt.Fprintln(out, "model: cannot store the API key:", err)
	case setup.Changed:
		fmt.Fprintf(out, "model: stored the %s API key from %s in %s (mode 0600)\n", setup.Provider, setup.EnvVar, layout.ModelSecret())
	}
	m, err := instance.ResolveModel(cfg, layout, instanceModelEnv())
	switch {
	case err != nil:
		fmt.Fprintln(out, "model: not configured -", err, "- model review is skipped, static analysis runs")
		return nil
	case m == nil && cfg.Offline:
		fmt.Fprintf(out, "model: none (offline mode: write %s with an internal endpoint in base_url, docs/OPERATIONS.md) - static analysis only\n", layout.ModelSecret())
		return nil
	case m == nil:
		fmt.Fprintln(out, "model: none (set OPENAI_API_KEY, ANTHROPIC_API_KEY or OPENCODE_API_KEY and run codeye up again) - static analysis only")
		return nil
	}
	fmt.Fprintf(out, "model: %s (key: %s)\n", m.ID, modelSourceText(m, layout))
	if m.Notice != "" {
		fmt.Fprintln(out, "model:", m.Notice)
	}
	if noProbe || !instance.ModelProbeDue(layout, m) {
		return m
	}
	r := probeModel(ctx, m.Caller())
	fmt.Fprint(out, r.text())
	if err := instance.RecordModelProbe(layout, m, r.Verified, r.Reason); err != nil {
		fmt.Fprintln(out, "model: cannot record the probe result (the next codeye up probes again):", err)
	}
	return m
}

func modelSourceText(m *instance.ResolvedModel, layout instance.Layout) string {
	switch m.Source {
	case instance.ModelSourceSecret:
		return layout.ModelSecret()
	case instance.ModelSourceConfig:
		return "agent.model.credential_file " + m.CredentialFile
	case instance.ModelSourceOpenCode:
		return "OpenCode " + m.CredentialFile
	case instance.ModelSourceRelay:
		return "relay " + m.RelayFile
	}
	return "none"
}
