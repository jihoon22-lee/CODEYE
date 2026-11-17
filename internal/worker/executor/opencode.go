package executor

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var openCodeModel = regexp.MustCompile(`^opencode/[a-z0-9][a-z0-9.-]{0,100}$`)

// LocalModelRelay is the operator-launched metered loopback service. Its
// token is a short-lived local capability, never the upstream API key.
type LocalModelRelay struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
	Model   string `json:"model"`
}

func (r *LocalModelRelay) valid(name string) bool {
	if r == nil {
		return openCodeModel.MatchString(name)
	}
	u, err := url.Parse(r.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil ||
		u.Port() == "" || u.Path != "/v1" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	n, err := strconv.Atoi(u.Port())
	return err == nil && n > 0 && n <= 65535 && (r.Model == "gpt-6.2-sol" || r.Model == "gpt-6.1-sol") &&
		name == "codeye-gateway/"+r.Model && len(r.Token) >= 32 && len(r.Token) <= 256 && !strings.ContainsAny(r.Token, "\r\n\x00")
}

// Arbitrary OPENCODE_* values in Spec.Env still follow the credential filter.
func managedOpenCodeEnv(model string, relay *LocalModelRelay) map[string]string {
	provider, _, _ := strings.Cut(model, "/")
	settings := map[string]any{
		"enabled_providers": []string{provider}, "model": model, "small_model": model,
		"permission": "deny", "tools": map[string]bool{"*": false}, "share": "disabled", "snapshot": false,
		"autoupdate": false, "lsp": false, "formatter": false, "plugin": []string{},
	}
	if relay != nil {
		settings["provider"] = map[string]any{provider: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "CODEYE budgeted QA relay",
			"options": map[string]any{"apiKey": relay.Token, "baseURL": relay.BaseURL},
			"models":  map[string]any{relay.Model: map[string]any{"id": relay.Model, "name": relay.Model, "tool_call": false, "limit": map[string]int{"context": 64000, "output": 1536}}},
		}}
	}
	data, _ := json.Marshal(settings)
	return map[string]string{
		"OPENCODE_CONFIG_CONTENT": string(data), "DO_NOT_TRACK": "1",
		"OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_DEFAULT_PLUGINS": "1",
		"OPENCODE_DISABLE_LSP_DOWNLOAD": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "1", "OPENCODE_DISABLE_EXTERNAL_SKILLS": "1",
		"OPENCODE_DISABLE_CLAUDE_CODE": "1",
	}
}
