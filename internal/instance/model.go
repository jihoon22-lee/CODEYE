package instance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// ProviderDefault is one provider CODEYE can set up from an API key in the
// environment (docs/REVIEW.md#모델-설정).
type ProviderDefault struct {
	Provider string // OpenCode provider ID
	EnvVar   string // the variable `up` reads the key from
	Model    string // the model used when agent.model.name is not set
}

// DefaultModels is the per-provider preference, in the order the key
// variables are read. agent.model.name overrides the model.
var DefaultModels = []ProviderDefault{
	{Provider: "openai", EnvVar: "OPENAI_API_KEY", Model: "gpt-6.2-sol"},
	{Provider: "anthropic", EnvVar: "ANTHROPIC_API_KEY", Model: "claude-opus-5-5"},
	{Provider: "opencode", EnvVar: "OPENCODE_API_KEY", Model: "gpt-6.1-sol"},
}

func defaultModel(provider string) string {
	for _, d := range DefaultModels {
		if d.Provider == provider {
			return d.Model
		}
	}
	return ""
}

// Credential sources (ResolvedModel.Source).
const (
	ModelSourceConfig         = "config"          // agent.model.credential_file
	ModelSourceSecret         = "state_secret"    // <state>/secrets/model.json
	ModelSourceOpenCode       = "opencode_auth"   // the user's OpenCode auth.json
	ModelSourceOpenCodeConfig = "opencode_config" // explicit OpenCode config_file
	ModelSourceRelay          = "relay"           // agent.model.relay_file
	ModelSourceNone           = "none"            // a provider that needs no key
)

// SecretsDir holds files only the worker's model call reads (0700).
func (l Layout) SecretsDir() string { return filepath.Join(l.Root, "secrets") }

// ModelSecret is the model credential `up` writes from the environment.
func (l Layout) ModelSecret() string { return filepath.Join(l.SecretsDir(), "model.json") }

// ModelProbeRecord is the last probe `up` ran (no key-derived data).
func (l Layout) ModelProbeRecord() string { return filepath.Join(l.RunDir(), "model-probe.json") }

// ModelEnv is what model resolution reads besides the configuration; tests
// replace it so they never read the real home directory.
type ModelEnv struct {
	Getenv   func(string) string
	Home     string
	LookPath func(string) (string, error)
	// Model defaults passed to a managed worker without the host environment.
	Executable, AuthFile, ConfigFile string
}

// SystemModelEnv is the process environment and the user's home.
func SystemModelEnv() ModelEnv {
	home, _ := os.UserHomeDir()
	return ModelEnv{Getenv: os.Getenv, Home: home, LookPath: exec.LookPath}
}

func (e ModelEnv) get(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// xdg returns $VAR or ~/fallback.
func (e ModelEnv) xdg(variable, fallback string) string {
	if v := e.get(variable); filepath.IsAbs(v) {
		return v
	}
	if e.Home == "" {
		return ""
	}
	return filepath.Join(e.Home, fallback)
}

// OpenCodeAuthFile is the user's OpenCode credential store.
func (e ModelEnv) OpenCodeAuthFile() string {
	if e.AuthFile != "" {
		return e.AuthFile
	}
	d := e.xdg("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if d == "" {
		return ""
	}
	return filepath.Join(d, "opencode", "auth.json")
}

func (e ModelEnv) OpenCodeConfigFile() string {
	if e.ConfigFile != "" {
		return e.ConfigFile
	}
	d := e.xdg("XDG_CONFIG_HOME", ".config")
	if d == "" {
		return ""
	}
	return filepath.Join(d, "opencode", "opencode.json")
}

// ResolvedModel is the model the local agent is started with.
type ResolvedModel struct {
	OpenCode string
	// ID is PROVIDER/MODEL.
	ID             string
	CredentialFile string
	ConfigFile     string
	NativeEndpoint string // endpoint for operator diagnostics; contains no authentication fields
	nativeHome     string
	RelayFile      string
	Timeout        time.Duration
	ReviewMaxCalls int
	// Source says where the credential comes from (ModelSource*).
	Source string
	// Notice explains a problem that will make model calls fail (for
	// example no API key for the provider); the agent still starts.
	Notice string
	// Offline and CABundle are the configuration's offline mode and CA
	// bundle; model calls follow them (opencode.Config).
	Offline  bool
	CABundle string
}

// Provider is the provider part of ID.
func (m *ResolvedModel) Provider() string {
	p, _, _ := strings.Cut(m.ID, "/")
	return p
}

// Caller is the worker-side call configuration of m.
func (m *ResolvedModel) Caller() opencode.Config {
	return opencode.Config{Executable: m.OpenCode, Model: m.ID, RelayFile: m.RelayFile, CredentialFile: m.CredentialFile, ConfigFile: m.ConfigFile, NativeHome: m.nativeHome, Timeout: m.Timeout,
		Offline: m.Offline, CABundle: m.CABundle}
}

// ResolveModel decides the local agent's model. It returns (nil, nil) when
// no model is configured: no agent.model and no credential file or OpenCode
// API key entry for a provider of DefaultModels. An error means agent.model
// or a credential cannot be used; the caller starts without a model.
//
// Credentials, in order: agent.model.credential_file, <state>/secrets/model.json,
// the OpenCode auth.json entry of type "api" for the provider. The model:
// agent.model.name (PROVIDER/MODEL, or a model name for the credential's
// provider), else the OpenCode config's "model" of that provider, else
// DefaultModels.
func ResolveModel(cfg *Config, layout Layout, env ModelEnv) (*ResolvedModel, error) {
	m := cfg.Agent.Model
	if m == nil {
		m = &Model{}
	}
	res := &ResolvedModel{RelayFile: m.RelayFile, Timeout: opencode.DefaultTimeout, ReviewMaxCalls: m.ReviewMaxCalls,
		Offline: cfg.Offline, CABundle: cfg.CABundle}
	if m.TimeoutSeconds > 0 {
		res.Timeout = time.Duration(m.TimeoutSeconds) * time.Second
	}
	if m.ConfigFile != "" {
		return resolveNativeModel(cfg, m, res, layout, env)
	}
	provider, model, _ := strings.Cut(m.Name, "/")
	if !strings.Contains(m.Name, "/") {
		provider, model = "", m.Name
	}
	switch {
	case m.RelayFile != "":
		res.Source = ModelSourceRelay
		if provider == "" {
			provider = executor.GatewayProvider
		}
	case m.CredentialFile != "":
		p, err := credentialProvider(m.CredentialFile)
		if err != nil {
			p = authProvider(m.CredentialFile, provider)
			if p == "" {
				return nil, fmt.Errorf("agent.model.credential_file: %w", err)
			}
		}
		res.CredentialFile, res.Source = m.CredentialFile, ModelSourceConfig
		if provider != "" && provider != p {
			return nil, fmt.Errorf("agent.model.credential_file holds a key for %q, not for the model's provider %q", p, provider)
		}
		provider = p
	default:
		p, err := credentialProvider(layout.ModelSecret())
		switch {
		case err == nil && (provider == "" || provider == p):
			res.CredentialFile, res.Source, provider = layout.ModelSecret(), ModelSourceSecret, p
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s: %w", layout.ModelSecret(), err)
		}
		// Offline, OpenCode's own login is not read: its entries name no
		// endpoint, so they would reach the provider's public one.
		if res.CredentialFile == "" && !cfg.Offline {
			auth := env.OpenCodeAuthFile()
			if found := authProvider(auth, provider); found != "" {
				res.CredentialFile, res.Source, provider = auth, ModelSourceOpenCode, found
			}
		}
	}
	if res.Source == "" {
		if cfg.Agent.Model == nil {
			return nil, nil
		}
		res.Source = ModelSourceNone
	}
	if cfg.Offline {
		switch res.Source {
		case ModelSourceNone:
			return nil, opencode.ErrNativeEndpoint
		case ModelSourceConfig, ModelSourceSecret:
			if !credentialHasEndpoint(res.CredentialFile, provider) {
				return nil, fmt.Errorf("the model key file has no base_url: %w", opencode.ErrNativeEndpoint)
			}
		}
	}
	if provider == "" {
		return nil, errors.New("agent.model.name must be PROVIDER/MODEL when no API key is configured")
	}
	if model == "" {
		model = openCodeConfigModel(env.OpenCodeConfigFile(), provider)
	}
	if model == "" {
		model = defaultModel(provider)
	}
	if model == "" {
		return nil, fmt.Errorf("no default model for provider %q; set agent.model.name", provider)
	}
	res.ID = provider + "/" + model
	if !executor.ValidOpenCodeModel(res.ID) {
		return nil, fmt.Errorf("model %q is not a PROVIDER/MODEL identifier", res.ID)
	}
	if res.Source == ModelSourceNone && provider != "opencode" {
		res.Notice = fmt.Sprintf("no API key found for provider %q: set %s and run codeye up, or write %s", provider, envVarOf(provider), layout.ModelSecret())
	}
	return resolveModelExecutable(m, res, env)
}

func resolveNativeModel(cfg *Config, m *Model, res *ResolvedModel, layout Layout, env ModelEnv) (*ResolvedModel, error) {
	res.ConfigFile, res.CredentialFile, res.nativeHome = m.ConfigFile, m.CredentialFile, env.Home
	res.Source = ModelSourceOpenCodeConfig
	if res.CredentialFile != "" {
		res.Source = ModelSourceConfig
	}
	name, selected, err := opencode.NativeSelection(res.ConfigFile, m.Name, res.CredentialFile, cfg.Offline, env.Home)
	provider, _, _ := strings.Cut(name, "/")
	keyMissing := selected != nil && selected.APIKey == "" || errors.Is(err, opencode.ErrNativeKeyMissing) || errors.Is(err, opencode.ErrNativeEndpoint)
	var ref *opencode.NativeKeyReference
	if errors.As(err, &ref) && ref.APIKey {
		for _, d := range DefaultModels {
			keyMissing = keyMissing || d.Provider == provider && d.EnvVar == ref.Variable
		}
	}
	if keyMissing && res.CredentialFile == "" {
		for _, candidate := range []struct{ path, source string }{
			{layout.ModelSecret(), ModelSourceSecret}, {env.OpenCodeAuthFile(), ModelSourceOpenCode},
		} {
			if _, readErr := opencode.ReadCredential(candidate.path, provider); readErr != nil {
				continue
			}
			resolvedName, p, readErr := opencode.NativeSelection(res.ConfigFile, m.Name, candidate.path, cfg.Offline, env.Home)
			if readErr == nil {
				res.CredentialFile, res.Source = candidate.path, candidate.source
				name, selected, err = resolvedName, p, nil
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("agent.model.config_file: %w", err)
	}
	res.ID, res.NativeEndpoint = name, selected.BaseURL
	return resolveModelExecutable(m, res, env)
}

func resolveModelExecutable(m *Model, res *ResolvedModel, env ModelEnv) (*ResolvedModel, error) {
	res.OpenCode = m.OpenCode
	if res.OpenCode == "" {
		res.OpenCode = env.FindOpenCode()
	}
	if res.OpenCode == "" {
		return nil, errors.New("OpenCode not found (looked in ~/.opencode/bin and PATH); install it or set agent.model.opencode")
	}
	return res, nil
}

func envVarOf(provider string) string {
	for _, d := range DefaultModels {
		if d.Provider == provider {
			return d.EnvVar
		}
	}
	return "the provider's API key variable"
}

// FindOpenCode looks in OpenCode's install location, then PATH.
func (env ModelEnv) FindOpenCode() string {
	if env.Executable != "" {
		return env.Executable
	}
	if env.Home != "" {
		p := filepath.Join(env.Home, ".opencode", "bin", "opencode")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	if env.LookPath != nil {
		if p, err := env.LookPath("opencode"); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
		}
	}
	return ""
}

// credentialProvider reads the provider of a CODEYE model credential and
// checks that the worker will accept the file (the key is not kept).
func credentialProvider(path string) (string, error) {
	if _, err := os.Lstat(path); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("cannot read the model credential")
	}
	var c struct {
		Provider string `json:"provider"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Provider == "" {
		return "", errors.New(`the model credential needs "schema_version", "provider" and "api_key"`)
	}
	if _, err := opencode.ReadCredential(path, c.Provider); err != nil {
		return "", errors.New("the model credential is not usable (mode 0600, schema_version \"0.1\", a one-line api_key, an http(s) base_url)")
	}
	return c.Provider, nil
}

// credentialHasEndpoint reports whether the credential at path names its
// endpoint (base_url) for provider.
func credentialHasEndpoint(path, provider string) bool {
	p, err := opencode.ReadCredential(path, provider)
	return err == nil && p.BaseURL != ""
}

// authProvider is the provider whose OpenCode auth.json entry is an API
// key: want itself, or with want empty the first of DefaultModels.
func authProvider(path, want string) string {
	if path == "" {
		return ""
	}
	if want != "" {
		if _, err := opencode.ReadCredential(path, want); err == nil {
			return want
		}
		return ""
	}
	for _, d := range DefaultModels {
		if _, err := opencode.ReadCredential(path, d.Provider); err == nil {
			return d.Provider
		}
	}
	return ""
}

// openCodeConfigModel is the "model" of the user's OpenCode config when it
// belongs to provider.
func openCodeConfigModel(path, provider string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var c struct {
		Model string `json:"model"`
	}
	// opencode.json may be JSONC; a file that is not plain JSON is skipped.
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	p, name, ok := strings.Cut(c.Model, "/")
	if !ok || p != provider || !executor.ValidOpenCodeModel(c.Model) {
		return ""
	}
	return name
}

// ModelSetup is what WriteModelSecretFromEnv did.
type ModelSetup struct {
	// Provider and EnvVar are "" when no key variable was set.
	Provider, EnvVar string
	// Changed: the secret file was created or its content replaced.
	Changed bool
}

// WriteModelSecretFromEnv writes <state>/secrets/model.json (0600, in a
// 0700 directory) from the first of OPENAI_API_KEY, ANTHROPIC_API_KEY and
// OPENCODE_API_KEY that is set. An unchanged key leaves the file as it is,
// so its modification time tells when the configuration last changed. The
// key is not returned or printed.
func WriteModelSecretFromEnv(layout Layout, getenv func(string) string) (ModelSetup, error) {
	for _, d := range DefaultModels {
		key := strings.TrimSpace(getenv(d.EnvVar))
		if key == "" {
			continue
		}
		setup := ModelSetup{Provider: d.Provider, EnvVar: d.EnvVar}
		c := opencode.Credential{SchemaVersion: opencode.CredentialSchema, Provider: d.Provider, APIKey: key}
		if !executor.ValidProvider(&executor.ModelProvider{ID: c.Provider, APIKey: c.APIKey}) {
			return setup, fmt.Errorf("%s is not a single-line API key", d.EnvVar)
		}
		data, err := json.Marshal(c)
		if err != nil {
			return setup, err
		}
		data = append(data, '\n')
		if old, err := os.ReadFile(layout.ModelSecret()); err == nil && bytes.Equal(old, data) {
			return setup, nil
		}
		if err := os.MkdirAll(layout.SecretsDir(), 0o700); err != nil {
			return setup, errors.New("cannot create the secrets directory")
		}
		if err := checkPrivateDir(layout.SecretsDir()); err != nil {
			return setup, err
		}
		if err := writePrivate(layout.ModelSecret(), data); err != nil {
			return setup, errors.New("cannot write the model credential")
		}
		setup.Changed = true
		return setup, nil
	}
	return ModelSetup{}, nil
}

// modelProbe is the record of the last probe: the model configuration it
// tested (paths, model ID, credential file time and size) and its result.
type modelProbe struct {
	SchemaVersion string `json:"schema_version"`
	Fingerprint   string `json:"fingerprint"`
	Verified      bool   `json:"verified"`
	Reason        string `json:"reason,omitempty"`
}

// ProbeFingerprint identifies selected model inputs without storing credential values.
func (m *ResolvedModel) ProbeFingerprint() string {
	parts := []string{m.ID, m.OpenCode, m.CredentialFile, m.RelayFile}
	if st, err := os.Stat(m.CredentialFile); m.CredentialFile != "" && err == nil {
		parts = append(parts, st.ModTime().UTC().Format(time.RFC3339Nano), fmt.Sprint(st.Size()))
	}
	if m.ConfigFile != "" {
		parts = append(parts, m.ConfigFile)
		if st, err := os.Stat(m.ConfigFile); err == nil {
			parts = append(parts, st.ModTime().UTC().Format(time.RFC3339Nano), fmt.Sprint(st.Size()))
		}
	}
	return strings.Join(parts, "\x00")
}

// ModelProbeDue reports whether m differs from the configuration of the
// last recorded probe (or no probe was recorded).
func ModelProbeDue(layout Layout, m *ResolvedModel) bool {
	raw, err := os.ReadFile(layout.ModelProbeRecord())
	if err != nil {
		return true
	}
	var p modelProbe
	return json.Unmarshal(raw, &p) != nil || p.Fingerprint != m.ProbeFingerprint()
}

// ModelProbeResult is the recorded probe outcome of m's configuration:
// "verified", "failed", or "not_probed" when none matches it.
func ModelProbeResult(layout Layout, m *ResolvedModel) string {
	status, _ := ModelProbeStatus(layout, m)
	return status
}

// ModelProbeStatus includes the adapter's reason for a failed saved probe.
func ModelProbeStatus(layout Layout, m *ResolvedModel) (string, string) {
	raw, err := os.ReadFile(layout.ModelProbeRecord())
	var p modelProbe
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Fingerprint != m.ProbeFingerprint() {
		return "not_probed", ""
	}
	if p.Verified {
		return "verified", ""
	}
	return "failed", p.Reason
}

// RecordModelProbe stores the outcome of a probe of m.
func RecordModelProbe(layout Layout, m *ResolvedModel, verified bool, reason string) error {
	return RecordModelProbeFor(layout, m.ProbeFingerprint(), verified, reason)
}

// RecordModelProbeFor records the inputs captured before the call. A profile
// edited while the provider is responding will still need its own check.
func RecordModelProbeFor(layout Layout, fingerprint string, verified bool, reason string) error {
	data, err := json.Marshal(modelProbe{SchemaVersion: recordSchema, Fingerprint: fingerprint, Verified: verified, Reason: reason})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(layout.RunDir(), 0o700); err != nil {
		return err
	}
	return writePrivate(layout.ModelProbeRecord(), data)
}
