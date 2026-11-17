package executor

import (
	"encoding/json"
	"testing"
)

func TestManagedModelEnvironment(t *testing.T) {
	// An operator's ordinary tool environment cannot inject model config, keys or
	// a different provider endpoint into the managed profile.
	for _, key := range []string{"OPENCODE_API_KEY", "OPENCODE_CONFIG_CONTENT", "OPENCODE_SERVER_PASSWORD"} {
		if _, err := validateEnv(map[string]string{key: "example"}); err == nil {
			t.Fatalf("accepted %s", key)
		}
	}
	for _, s := range []string{"opencode/", "opencode/foo\n", "other/model", "opencode/safe/../../elsewhere"} {
		if openCodeModel.MatchString(s) {
			t.Fatalf("accepted %q", s)
		}
	}
	env := managedOpenCodeEnv("opencode/test", nil)
	var settings struct {
		Providers  []string `json:"enabled_providers"`
		Model      string
		Small      string `json:"small_model"`
		Permission string
		Share      string
		Tools      map[string]bool
	}
	if err := json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Providers) != 1 || settings.Providers[0] != "opencode" || settings.Model != "opencode/test" || settings.Small != settings.Model ||
		settings.Permission != "deny" || settings.Share != "disabled" || len(settings.Tools) != 1 || settings.Tools["*"] {
		t.Fatal("unexpected model/tool permission policy")
	}
	for _, key := range []string{"OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_DEFAULT_PLUGINS"} {
		if env[key] != "1" {
			t.Fatalf("missing %s", key)
		}
	}
}
