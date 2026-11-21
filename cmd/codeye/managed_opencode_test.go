package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

func TestManagedOpenCodeRejectsInvalidUpdateWithoutReplacingConnection(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	settings := filepath.Join(home, "codeye.json")
	profile := filepath.Join(home, "opencode.jsonc")
	broken := filepath.Join(home, "broken.jsonc")
	write := func(path string, contents []byte) {
		t.Helper()
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config, err := json.Marshal(map[string]any{
		"schema_version": "0.1", "state_dir": state,
		"agent": map[string]any{"model": map[string]any{"review_max_calls": 33}},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(settings, config)
	write(profile, []byte(`{// Internal model fixture
  "model":"lab/reviewer",
  "provider":{"lab":{"options":{"baseURL":"http://127.0.0.1:9000/v1"}}}
}`))
	write(broken, []byte(`{"provider": [`))
	publicKey := filepath.Join(home, "public.json")
	write(publicKey, []byte(`{"schema_version":"0.1","provider":"openai","api_key":"fixture"}`))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	previousEnv := instanceModelEnv
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{Home: home} }
	t.Cleanup(func() { instanceModelEnv = previousEnv })
	connection := newManagedOpenCode(settings, state, true, "")
	valid := httpapi.OpenCodeFields{Executable: executable, ConfigFile: "~/opencode.jsonc"}
	saved, err := connection.Save(valid)
	if err != nil || !saved.Editable || !saved.Configured || saved.ConfigFile != profile || saved.EffectiveModel == nil || *saved.EffectiveModel != "lab/reviewer" {
		t.Fatalf("saving a readable native profile: %#v, %v", saved, err)
	}
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		fields httpapi.OpenCodeFields
		reason string
	}{
		{"missing executable", httpapi.OpenCodeFields{Executable: filepath.Join(home, "missing"), ConfigFile: profile}, "opencode_unavailable"},
		{"malformed native profile", httpapi.OpenCodeFields{Executable: executable, ConfigFile: broken}, "opencode_config_invalid"},
		{"offline endpoint missing", httpapi.OpenCodeFields{Executable: executable, CredentialFile: publicKey, Model: "openai/model"}, "model_endpoint_not_configured"},
		{"missing credential", httpapi.OpenCodeFields{Executable: executable, ConfigFile: profile, CredentialFile: "~/missing-auth.json"}, "model_credential_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := connection.Save(tc.fields)
			if !errors.Is(err, httpapi.ErrOpenCodeValidation) || candidate.Reason != tc.reason {
				t.Fatalf("rejection = %v (%q), want validation error with %q", err, candidate.Reason, tc.reason)
			}
			after, err := os.ReadFile(settings)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected save replaced the working configuration: %v", err)
			}
			current, err := connection.Get()
			if err != nil || !current.Configured || current.ConfigFile != profile {
				t.Fatalf("active connection after rejection: %#v, %v", current, err)
			}
		})
	}
}
