package instance

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeHome is a home with an executable ~/.opencode/bin/opencode and
// optional OpenCode auth.json and opencode.json; the real home is never read.
func fakeHome(t *testing.T, auth, ocConfig string) ModelEnv {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".opencode", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		p := filepath.Join(home, ".local", "share", "opencode")
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "auth.json"), []byte(auth), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if ocConfig != "" {
		p := filepath.Join(home, ".config", "opencode")
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "opencode.json"), []byte(ocConfig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return ModelEnv{Getenv: func(string) string { return "" }, Home: home}
}

func modelConfig(t *testing.T, model string) (*Config, Layout) {
	t.Helper()
	base := t.TempDir()
	state := filepath.Join(base, "state")
	extra := ""
	if model != "" {
		extra = `,"agent":{"model":` + model + `}`
	}
	c, err := Parse([]byte(cfg(state, extra)), tools)
	if err != nil {
		t.Fatal(err)
	}
	return c, Layout{Root: state}
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// up stores the first key variable that is set; the file is private and
// rewritten only when the key changes. Resolution then picks the
// provider's default model and the user's OpenCode install.
func TestModelSecretFromEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	c, layout := modelConfig(t, "")
	home := fakeHome(t, "", "")
	if m, err := ResolveModel(c, layout, home); m != nil || err != nil {
		t.Fatalf("no key: %+v %v", m, err)
	}
	if s, err := WriteModelSecretFromEnv(layout, env(nil)); err != nil || s.Changed || s.Provider != "" {
		t.Fatalf("%+v %v", s, err)
	}
	const key = "sk-test-from-env-0123456789"
	s, err := WriteModelSecretFromEnv(layout, env(map[string]string{"ANTHROPIC_API_KEY": "sk-ant", "OPENAI_API_KEY": key}))
	if err != nil || !s.Changed || s.Provider != "openai" || s.EnvVar != "OPENAI_API_KEY" {
		t.Fatalf("%+v %v", s, err)
	}
	st, err := os.Stat(layout.ModelSecret())
	dir, derr := os.Stat(layout.SecretsDir())
	if err != nil || derr != nil || st.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatalf("modes %v %v", st, dir)
	}
	raw, _ := os.ReadFile(layout.ModelSecret())
	if !bytes.Contains(raw, []byte(key)) || !bytes.Contains(raw, []byte(`"provider":"openai"`)) {
		t.Fatal("secret content")
	}
	again, err := WriteModelSecretFromEnv(layout, env(map[string]string{"OPENAI_API_KEY": key}))
	if err != nil || again.Changed {
		t.Fatal("unchanged key rewritten")
	}
	m, err := ResolveModel(c, layout, home)
	if err != nil || m.ID != "openai/gpt-6.2-sol" || m.CredentialFile != layout.ModelSecret() || m.Source != ModelSourceSecret ||
		m.OpenCode != filepath.Join(home.Home, ".opencode", "bin", "opencode") || m.Timeout != 180*time.Second || m.Notice != "" {
		t.Fatalf("%+v %v", m, err)
	}
	if !ModelProbeDue(layout, m) {
		t.Fatal("first probe not due")
	}
	if err := os.MkdirAll(layout.RunDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RecordModelProbe(layout, m, true, ""); err != nil || ModelProbeDue(layout, m) {
		t.Fatal("probe record")
	}
	probe, _ := os.ReadFile(layout.ModelProbeRecord())
	if bytes.Contains(probe, []byte(key)) {
		t.Fatal("probe record holds the key")
	}
	// A new key changes the configuration: the probe is due again.
	time.Sleep(10 * time.Millisecond)
	if s, _ := WriteModelSecretFromEnv(layout, env(map[string]string{"OPENAI_API_KEY": key + "x"})); !s.Changed {
		t.Fatal("new key not stored")
	}
	if !ModelProbeDue(layout, m) {
		t.Fatal("probe not due after a key change")
	}
	if _, err := WriteModelSecretFromEnv(layout, env(map[string]string{"OPENAI_API_KEY": "two\nlines"})); err == nil || strings.Contains(err.Error(), "two") {
		t.Fatalf("multi-line key: %v", err)
	}
}

// Credential sources in order: agent.model.credential_file, the state
// secret, OpenCode's auth.json (type api only). The model: agent.model.name,
// the OpenCode config's model of that provider, the default table.
func TestModelResolutionOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	auth := `{"anthropic":{"type":"api","key":"sk-ant-0123"},"openai":{"type":"oauth","access":"x"}}`
	home := fakeHome(t, auth, `{"model":"anthropic/claude-sonnet-5-5"}`)

	// auth.json only: the first DefaultModels provider with an api entry,
	// the OpenCode config's model for it.
	c, layout := modelConfig(t, "")
	m, err := ResolveModel(c, layout, home)
	if err != nil || m.ID != "anthropic/claude-sonnet-5-5" || m.Source != ModelSourceOpenCode || m.CredentialFile != home.OpenCodeAuthFile() {
		t.Fatalf("auth: %+v %v", m, err)
	}
	// The state secret wins over auth.json; a bare name keeps its provider.
	if _, err := WriteModelSecretFromEnv(layout, env(map[string]string{"OPENAI_API_KEY": "sk-openai-0123"})); err != nil {
		t.Fatal(err)
	}
	c.Agent.Model = &Model{Name: "gpt-6.1-sol"}
	if m, err = ResolveModel(c, layout, home); err != nil || m.ID != "openai/gpt-6.1-sol" || m.Source != ModelSourceSecret {
		t.Fatalf("secret: %+v %v", m, err)
	}
	// A name of another provider falls through to auth.json for it.
	c.Agent.Model = &Model{Name: "anthropic/claude-opus-5-5", TimeoutSeconds: 30}
	if m, err = ResolveModel(c, layout, home); err != nil || m.ID != "anthropic/claude-opus-5-5" || m.Source != ModelSourceOpenCode || m.Timeout != 30*time.Second {
		t.Fatalf("named provider: %+v %v", m, err)
	}
	// An explicit credential file wins over both.
	explicit := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(explicit, []byte(`{"schema_version":"0.1","provider":"local","api_key":"k","base_url":"http://127.0.0.1:1/v1","openai_compatible":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c.Agent.Model = &Model{CredentialFile: explicit, Name: "local/qwen/coder", OpenCode: "/opt/oc"}
	if m, err = ResolveModel(c, layout, home); err != nil || m.ID != "local/qwen/coder" || m.Source != ModelSourceConfig || m.OpenCode != "/opt/oc" {
		t.Fatalf("explicit: %+v %v", m, err)
	}
	// A name with a slash is PROVIDER/MODEL; it must match the key's provider.
	c.Agent.Model = &Model{CredentialFile: explicit, Name: "qwen/coder"}
	if _, err = ResolveModel(c, layout, home); err == nil {
		t.Fatal("credential of another provider accepted")
	}
	// No default for an unknown provider without a name.
	c.Agent.Model = &Model{CredentialFile: explicit}
	if _, err = ResolveModel(c, layout, home); err == nil {
		t.Fatal("unknown provider without a model name")
	}
	// A provider with no key anywhere still starts, with a notice.
	c.Agent.Model = &Model{Name: "mistral/large"}
	if m, err = ResolveModel(c, layout, home); err != nil || m.Source != ModelSourceNone || m.CredentialFile != "" || m.Notice == "" {
		t.Fatalf("keyless: %+v %v", m, err)
	}
	// The relay keeps its own model ID and no credential file.
	c.Agent.Model = &Model{Name: "codeye-gateway/gpt-6.2-sol", RelayFile: "/srv/relay.json"}
	if m, err = ResolveModel(c, layout, home); err != nil || m.ID != "codeye-gateway/gpt-6.2-sol" || m.CredentialFile != "" || m.RelayFile != "/srv/relay.json" {
		t.Fatalf("relay: %+v %v", m, err)
	}
	// A shared secret file is reported, not silently skipped.
	if err := os.Chmod(layout.ModelSecret(), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Agent.Model = nil
	if _, err = ResolveModel(c, layout, home); err == nil {
		t.Fatal("secret readable by others accepted")
	}
	// No OpenCode anywhere.
	if err := os.Chmod(layout.ModelSecret(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveModel(c, layout, ModelEnv{Home: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "OpenCode not found") {
		t.Fatalf("no opencode: %v", err)
	}
}
