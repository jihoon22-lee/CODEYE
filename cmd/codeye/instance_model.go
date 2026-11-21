package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
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
	if path := cfg.FilePath(); path != "" {
		args := []string{"--model-instance-config", path}
		if m := cfg.Agent.Model; m != nil && m.ReviewMaxCalls != 0 {
			args = append(args, "--review-max-calls", strconv.Itoa(m.ReviewMaxCalls))
		}
		return args
	}
	m, err := instance.ResolveModel(cfg, layout, instanceModelEnv())
	if err != nil || m == nil {
		return nil
	}
	args := []string{"--opencode", m.OpenCode, "--model", m.ID}
	if m.ConfigFile != "" {
		args = append(args, "--model-config-file", m.ConfigFile)
	}
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

func localModelSettings(path, root string, env instance.ModelEnv) *workerproto.ModelSettings {
	return &workerproto.ModelSettings{InstancePath: path, StateDir: root, Home: env.Home,
		Executable: env.FindOpenCode(), OpenCodeConfig: env.OpenCodeConfigFile(), OpenCodeAuth: env.OpenCodeAuthFile()}
}

// A worker serves multiple attempts. Review calls in one attempt share the
// same selection, while a new attempt reads the current atomic config file.
type instanceModelCaller struct {
	settings   *workerproto.ModelSettings
	static     opencode.Config
	offline    bool
	caBundle   string
	mu         sync.Mutex
	scratch    string
	caller     opencode.Config
	protected  []string
	nativePath string
	nativeRefs []string
}

func (c *instanceModelCaller) Review(ctx context.Context, runner executor.Runner, q opencode.Request) opencode.Reply {
	c.mu.Lock()
	var caller opencode.Config
	if q.Scratch == c.scratch {
		caller = c.caller
	}
	c.mu.Unlock()
	return caller.Review(ctx, runner, q)
}

func (c *instanceModelCaller) prepareAttempt(scratch string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scratch == scratch {
		return c.protected
	}
	c.scratch, c.caller = scratch, opencode.Config{}
	c.protected = nil
	if c.settings == nil {
		c.protected = append(c.protected, c.static.ConfigFile, c.static.CredentialFile, c.static.RelayFile)
		c.caller = c.static
		if c.static.ConfigFile != "" {
			if name, _, err := opencode.NativeSelection(c.static.ConfigFile, c.static.Model, c.static.CredentialFile, c.static.Offline, c.static.NativeHome); err == nil {
				c.caller.Model = name
			}
		}
		c.pinNative(c.static.ConfigFile, c.caller.Model, c.static.NativeHome)
		return c.protected
	}
	c.protected = append(c.protected, c.settings.InstancePath, c.settings.OpenCodeAuth)
	cfg, err := instance.LoadForStateDir(c.settings.InstancePath, knownTools(), c.settings.StateDir)
	if err != nil || filepath.Clean(cfg.StateDir) != c.settings.StateDir {
		c.protected = append(c.protected, c.nativePath)
		c.protected = append(c.protected, c.nativeRefs...)
		return c.protected
	}
	// The running agent's offline and CA policy applies until it is restarted.
	cfg.Offline, cfg.CABundle = c.offline, c.caBundle
	env := instance.ModelEnv{Home: c.settings.Home, Executable: c.settings.Executable,
		ConfigFile: c.settings.OpenCodeConfig, AuthFile: c.settings.OpenCodeAuth}
	if m := cfg.Agent.Model; m != nil {
		c.protected = append(c.protected, m.ConfigFile, m.CredentialFile, m.RelayFile)
	}
	model, err := instance.ResolveModel(cfg, instance.Layout{Root: c.settings.StateDir}, env)
	if err == nil && model != nil {
		c.caller = model.Caller()
		c.protected = append(c.protected, model.CredentialFile)
		c.pinNative(model.ConfigFile, model.ID, env.Home)
	} else if m := cfg.Agent.Model; m != nil {
		c.pinNative(m.ConfigFile, m.Name, env.Home)
	} else {
		c.nativePath, c.nativeRefs = "", nil
	}
	return c.protected
}

func (c *instanceModelCaller) pinNative(path, model, home string) {
	if path != c.nativePath {
		c.nativePath, c.nativeRefs = path, nil
	}
	if path == "" {
		return
	}
	digest, paths := opencode.PinNativeConfig(path, model, home)
	if digest == "" {
		c.protected = append(c.protected, c.nativeRefs...)
		c.caller = opencode.Config{}
		return
	}
	c.nativeRefs = paths
	c.protected = append(c.protected, paths...)
	c.caller.NativeConfigDigest = digest
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
		fmt.Fprintf(out, "model: none (offline mode: use an internal endpoint in Admin → OpenCode connection or in base_url in %s) - applies to the next analysis\n", layout.ModelSecret())
		return nil
	case m == nil:
		fmt.Fprintln(out, "model: none (set up Admin → OpenCode connection for the next analysis) - static analysis only")
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
	case instance.ModelSourceOpenCodeConfig:
		return "OpenCode settings in " + m.ConfigFile
	case instance.ModelSourceRelay:
		return "relay " + m.RelayFile
	}
	return "none"
}
