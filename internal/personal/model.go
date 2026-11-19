package personal

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
	ModelSourceConfig   = "config"        // agent.model.credential_file
	ModelSourceSecret   = "state_secret"  // <state>/secrets/model.json
	ModelSourceOpenCode = "opencode_auth" // the user's OpenCode auth.json
	ModelSourceRelay    = "relay"         // agent.model.relay_file
	ModelSourceNone     = "none"          // a provider that needs no key
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
	d := e.xdg("XDG_DATA_HOME", filepath.Join(".local", "share"))
	if d == "" {
		return ""
	}
	return filepath.Join(d, "opencode", "auth.json")
}

func (e ModelEnv) openCodeConfigFile() string {
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
	RelayFile      string
	Timeout        time.Duration
	ReviewPack     string
	ReviewMaxCalls int
	// Source says where the credential comes from (ModelSource*).
	Source string
	// Notice explains a problem that will make model calls fail (for
	// example no API key for the provider); the agent still starts.
	Notice string
}

// Provider is the provider part of ID.
func (m *ResolvedModel) Provider() string {
	p, _, _ := strings.Cut(m.ID, "/")
	return p
}

// Caller is the worker-side call configuration of m.
func (m *ResolvedModel) Caller() opencode.Config {
	return opencode.Config{Executable: m.OpenCode, Model: m.ID, RelayFile: m.RelayFile, CredentialFile: m.CredentialFile, Timeout: m.Timeout}
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
	res := &ResolvedModel{RelayFile: m.RelayFile, Timeout: opencode.DefaultTimeout, ReviewPack: m.ReviewPack, ReviewMaxCalls: m.ReviewMaxCalls}
	if m.TimeoutSeconds > 0 {
		res.Timeout = time.Duration(m.TimeoutSeconds) * time.Second
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
			return nil, fmt.Errorf("agent.model.credential_file: %w", err)
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
		if res.CredentialFile == "" {
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
	if provider == "" {
		return nil, errors.New("agent.model.name must be PROVIDER/MODEL when no API key is configured")
	}
	if model == "" {
		model = openCodeConfigModel(env.openCodeConfigFile(), provider)
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
	res.OpenCode = m.OpenCode
	if res.OpenCode == "" {
		res.OpenCode = findOpenCode(env)
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

// findOpenCode looks in OpenCode's install location, then PATH.
func findOpenCode(env ModelEnv) string {
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

func (m *ResolvedModel) fingerprint() string {
	parts := []string{m.ID, m.OpenCode, m.CredentialFile, m.RelayFile}
	if st, err := os.Stat(m.CredentialFile); m.CredentialFile != "" && err == nil {
		parts = append(parts, st.ModTime().UTC().Format(time.RFC3339Nano), fmt.Sprint(st.Size()))
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
	return json.Unmarshal(raw, &p) != nil || p.Fingerprint != m.fingerprint()
}

// RecordModelProbe stores the outcome of a probe of m.
func RecordModelProbe(layout Layout, m *ResolvedModel, verified bool, reason string) error {
	data, err := json.Marshal(modelProbe{SchemaVersion: recordSchema, Fingerprint: m.fingerprint(), Verified: verified, Reason: reason})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(layout.RunDir(), 0o700); err != nil {
		return err
	}
	return writePrivate(layout.ModelProbeRecord(), data)
}
