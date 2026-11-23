package executor

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// openCodeModel is a PROVIDER/MODEL identifier. The model part may hold
// further slashes (routers name models by vendor/model) but no "." or ".."
// segment and no whitespace.
var openCodeModel = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]{0,63})/([A-Za-z0-9][A-Za-z0-9._:@+/-]{0,127})$`)

// GatewayProvider is the provider ID of the budgeted QA relay; only a relay
// descriptor may use it.
const GatewayProvider = "codeye-gateway"

// ValidOpenCodeModel reports whether id is a PROVIDER/MODEL identifier the
// managed profile accepts.
func ValidOpenCodeModel(id string) bool {
	m := openCodeModel.FindStringSubmatch(id)
	if m == nil {
		return false
	}
	for _, seg := range strings.Split(m[2], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// LocalModelRelay is the operator-launched metered loopback service. Its
// token is a short-lived local capability, never the upstream API key.
type LocalModelRelay struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
	Model   string `json:"model"`
	// OutputTokens and ContextTokens are the limits the profile declares
	// for the model, set by the adapter from the review budget (never read
	// from the descriptor).
	OutputTokens  int `json:"-"`
	ContextTokens int `json:"-"`
}

// ModelProvider is the one provider of the managed OpenCode profile: the
// credential read at call time and an optional endpoint. The QA relay is the
// instance built by RelayProvider.
type ModelProvider struct {
	// ID is the provider part of the model ID (openai, anthropic, opencode, ...).
	ID string
	// APIKey goes only into OPENCODE_CONFIG_CONTENT and is masked in the
	// recorded command and captured output.
	APIKey string
	// BaseURL overrides the provider's endpoint (an http or https URL).
	BaseURL string
	// OpenAICompatible selects OpenCode's OpenAI-compatible (chat
	// completions) provider package, for endpoints that are not one of
	// OpenCode's built-in providers.
	OpenAICompatible bool
	// Native holds the selected provider and model settings projected from
	// an explicit OpenCode configuration file.
	Native *NativeModel
	relay  *LocalModelRelay
}

// NativeModel carries the subset of OpenCode settings used by the managed
// adapter. It does not carry instructions, plugins or local module paths.
type NativeModel struct {
	Package      string
	ID           string
	Name         string
	Limit        map[string]int
	Headers      map[string]string
	Timeouts     map[string]any
	IncludeUsage *bool
}

// RelayProvider is the QA relay as a model provider.
func RelayProvider(r *LocalModelRelay) *ModelProvider {
	return &ModelProvider{ID: GatewayProvider, APIKey: r.Token, BaseURL: r.BaseURL, OpenAICompatible: true, relay: r}
}

// Relay reports whether p is the QA relay.
func (p *ModelProvider) Relay() bool { return p != nil && p.relay != nil }

// MeteredModel reports whether the budget relay accepts this upstream model.
func MeteredModel(id string) bool {
	switch id {
	case "gpt-6.2-sol", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-luna":
		return true
	}
	return false
}

func (r *LocalModelRelay) valid(name string) bool {
	u, err := url.Parse(r.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil ||
		u.Port() == "" || u.Path != "/v1" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	n, err := strconv.Atoi(u.Port())
	return err == nil && n > 0 && n <= 65535 && MeteredModel(r.Model) &&
		name == GatewayProvider+"/"+r.Model && len(r.Token) >= 32 && len(r.Token) <= 256 && !strings.ContainsAny(r.Token, "\r\n\x00")
}

// validOpenCode checks the model ID and the provider it is sent through.
func validOpenCode(name string, p *ModelProvider) bool {
	if !ValidOpenCodeModel(name) {
		return false
	}
	provider, _, _ := strings.Cut(name, "/")
	if p == nil {
		// Without a credential only providers that need none (OpenCode Zen's
		// free models) can answer; the relay's provider needs its descriptor.
		return provider != GatewayProvider
	}
	if p.relay != nil {
		return p.ID == GatewayProvider && p.relay.valid(name)
	}
	if p.ID != provider {
		return false
	}
	if p.Native != nil && p.APIKey == "" {
		return provider != GatewayProvider && (p.BaseURL == "" || ValidBaseURL(p.BaseURL))
	}
	return ValidProvider(p)
}

// ValidProvider checks a credential-backed provider (not the relay): an ID
// that may prefix a model ID, a single-line API key and an optional endpoint.
func ValidProvider(p *ModelProvider) bool {
	if p == nil || p.relay != nil || p.ID == GatewayProvider || !ValidOpenCodeModel(p.ID+"/m") ||
		p.APIKey == "" || len(p.APIKey) > 8192 || strings.ContainsAny(p.APIKey, "\r\n\t\x00 ") {
		return false
	}
	return p.BaseURL == "" || ValidBaseURL(p.BaseURL)
}

// ValidBaseURL accepts an absolute http(s) endpoint without credentials,
// query or fragment.
func ValidBaseURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(s, " \t\r\n\x00")
}

// Arbitrary OPENCODE_* values in Spec.Env still follow the credential filter.
// timeout is the call's wall-clock limit (Spec.Timeout).
func managedOpenCodeEnv(model string, p *ModelProvider, timeout time.Duration) map[string]string {
	provider, name, _ := strings.Cut(model, "/")
	// OpenCode aborts a provider request that gets no response headers, or
	// no next stream chunk, within headerTimeout or chunkTimeout (300,000 ms
	// each unless set; OpenCode 1.19.5). The relay sends its headers only
	// once the upstream answer is complete, so the provider profiles CODEYE
	// writes wait as long as the call may run.
	waits := map[string]any{}
	if ms := timeout.Milliseconds(); ms > 0 {
		waits["headerTimeout"], waits["chunkTimeout"] = ms, ms
	}
	settings := map[string]any{
		"enabled_providers": []string{provider}, "model": model, "small_model": model,
		"permission": "deny", "tools": map[string]bool{"*": false}, "share": "disabled", "snapshot": false,
		"autoupdate": false, "lsp": false, "formatter": false, "plugin": []string{},
	}
	switch {
	case p.Relay():
		r := p.relay
		settings["provider"] = map[string]any{provider: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "CODEYE budgeted QA relay",
			"options": withWaits(map[string]any{"apiKey": r.Token, "baseURL": r.BaseURL}, waits),
			"models":  map[string]any{r.Model: relayModel(r)},
		}}
	case p != nil && p.Native != nil:
		n := p.Native
		// The operator's own timeouts stay as set.
		options := withWaits(map[string]any{}, waits)
		for key, value := range n.Timeouts {
			options[key] = value
		}
		if n.IncludeUsage != nil {
			options["includeUsage"] = *n.IncludeUsage
		}
		if p.APIKey != "" {
			options["apiKey"] = p.APIKey
		}
		if p.BaseURL != "" {
			options["baseURL"] = p.BaseURL
		}
		if len(n.Headers) > 0 {
			options["headers"] = n.Headers
		}
		entry := map[string]any{"tool_call": false}
		if n.ID != "" {
			entry["id"] = n.ID
		}
		if n.Name != "" {
			entry["name"] = n.Name
		}
		if len(n.Limit) > 0 {
			entry["limit"] = n.Limit
		}
		block := map[string]any{"options": options, "models": map[string]any{name: entry}}
		if n.Package != "" {
			block["npm"] = n.Package
		}
		settings["provider"] = map[string]any{provider: block}
	case p != nil:
		options := withWaits(map[string]any{"apiKey": p.APIKey}, waits)
		if p.BaseURL != "" {
			options["baseURL"] = p.BaseURL
		}
		// The model is always declared: OpenCode's offline catalog does not
		// know every model a provider serves, and a declared catalog model
		// keeps its catalog metadata.
		block := map[string]any{"options": options, "models": map[string]any{name: map[string]any{"name": name}}}
		if p.OpenAICompatible {
			block["npm"] = "@ai-sdk/openai-compatible"
			block["models"] = map[string]any{name: map[string]any{"name": name, "tool_call": false}}
		}
		settings["provider"] = map[string]any{provider: block}
	}
	data, _ := json.Marshal(settings)
	return map[string]string{
		"OPENCODE_CONFIG_CONTENT": string(data), "DO_NOT_TRACK": "1",
		"OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_DEFAULT_PLUGINS": "1",
		"OPENCODE_DISABLE_LSP_DOWNLOAD": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "1", "OPENCODE_DISABLE_EXTERNAL_SKILLS": "1",
		"OPENCODE_DISABLE_CLAUDE_CODE": "1",
		// OpenCode asks the npm registry for its plugin package on every
		// start, plugins off or not (measured with 1.19.5). The managed
		// profile needs no package, so the lookup goes to a closed loopback
		// port instead of registry.npmjs.org or an internal mirror.
		"npm_config_registry": OpenCodeNpmRegistry,
	}
}

func withWaits(options, waits map[string]any) map[string]any {
	for key, value := range waits {
		options[key] = value
	}
	return options
}

// OpenCodeNpmRegistry is the npm registry of the managed OpenCode profile: a
// loopback port nothing listens on (discard), so the lookup fails at once.
const OpenCodeNpmRegistry = "http://127.0.0.1:9/"

// relayModel is the relay's one model entry. Its token limits are the
// review budget's (the adapter sets them); a relay without them declares
// none.
func relayModel(r *LocalModelRelay) map[string]any {
	m := map[string]any{"id": r.Model, "name": r.Model, "tool_call": false}
	if r.OutputTokens > 0 && r.ContextTokens > 0 {
		m["limit"] = map[string]int{"context": r.ContextTokens, "output": r.OutputTokens}
	}
	return m
}
