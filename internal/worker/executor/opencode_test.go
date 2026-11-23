package executor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestManagedModelEnvironment(t *testing.T) {
	// An operator's ordinary tool environment cannot inject model config, keys or
	// a different provider endpoint into the managed profile.
	for _, key := range []string{"OPENCODE_API_KEY", "OPENCODE_CONFIG_CONTENT", "OPENCODE_SERVER_PASSWORD"} {
		if _, err := validateEnv(map[string]string{key: "example"}); err == nil {
			t.Fatalf("accepted %s", key)
		}
	}
	for _, s := range []string{"opencode/", "opencode/foo\n", "model", "/model", "Open/model", "opencode/safe/../../elsewhere", "p/a//b", "p/a b", "p/./m"} {
		if ValidOpenCodeModel(s) {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, s := range []string{"opencode/big-pickle", "openai/gpt-6.2-sol", "anthropic/claude-opus-5-5", "openrouter/vendor/model:free"} {
		if !ValidOpenCodeModel(s) {
			t.Fatalf("rejected %q", s)
		}
	}
	// codeye-gateway is the relay's provider only; a key-backed provider must
	// match the model's provider and have a one-line key and an http(s) URL.
	key := &ModelProvider{ID: "openai", APIKey: "sk-test-0123456789"}
	for name, ok := range map[string]bool{
		"codeye-gateway/gpt-6.2-sol": false, "openai/gpt-6.2-sol": true, "anthropic/claude": false,
	} {
		if validOpenCode(name, key) != ok {
			t.Fatalf("%s with an openai key: want %v", name, ok)
		}
	}
	if validOpenCode("codeye-gateway/gpt-6.2-sol", nil) || !validOpenCode("openai/gpt-6.2-sol", nil) {
		t.Fatal("credential-free rule")
	}
	for _, bad := range []ModelProvider{{ID: "openai", APIKey: "a\nb"}, {ID: "openai"}, {ID: "openai", APIKey: "k", BaseURL: "ftp://x"},
		{ID: "openai", APIKey: "k", BaseURL: "http://u:p@127.0.0.1/v1"}, {ID: GatewayProvider, APIKey: "k"}} {
		if ValidProvider(&bad) {
			t.Fatalf("accepted %+v", bad)
		}
	}
	env := managedOpenCodeEnv("opencode/test", nil, 0)
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

// The QA relay's profile is pinned byte for byte. Its token limits are the
// review budget's, here the default (16384 output tokens; the 256 KiB
// request, OpenCode's overhead and the output as context); a relay
// without limits declares none. OpenCode waits for the relay's headers and
// chunks as long as the call may run (600 s here), not its 300 s default.
func TestRelayProfileUnchanged(t *testing.T) {
	bare := &LocalModelRelay{BaseURL: "http://127.0.0.1:4567/v1", Token: "tok-0123456789abcdef0123456789abcdef", Model: "gpt-6.2-sol"}
	if got := managedOpenCodeEnv("codeye-gateway/gpt-6.2-sol", RelayProvider(bare), 0)["OPENCODE_CONFIG_CONTENT"]; strings.Contains(got, `"limit"`) {
		t.Fatalf("relay without limits declared some: %s", got)
	}
	r := &LocalModelRelay{BaseURL: "http://127.0.0.1:4567/v1", Token: "tok-0123456789abcdef0123456789abcdef", Model: "gpt-6.2-sol",
		OutputTokens: 16384, ContextTokens: 262144 + 16384 + 16384}
	p := RelayProvider(r)
	if !validOpenCode("codeye-gateway/gpt-6.2-sol", p) || validOpenCode("codeye-gateway/gpt-6.1-sol", p) {
		t.Fatal("relay model rule")
	}
	want := `{"autoupdate":false,"enabled_providers":["codeye-gateway"],"formatter":false,"lsp":false,"model":"codeye-gateway/gpt-6.2-sol","permission":"deny","plugin":[],"provider":{"codeye-gateway":{"models":{"gpt-6.2-sol":{"id":"gpt-6.2-sol","limit":{"context":294912,"output":16384},"name":"gpt-6.2-sol","tool_call":false}},"name":"CODEYE budgeted QA relay","npm":"@ai-sdk/openai-compatible","options":{"apiKey":"tok-0123456789abcdef0123456789abcdef","baseURL":"http://127.0.0.1:4567/v1","chunkTimeout":600000,"headerTimeout":600000}}},"share":"disabled","small_model":"codeye-gateway/gpt-6.2-sol","snapshot":false,"tools":{"*":false}}`
	if got := managedOpenCodeEnv("codeye-gateway/gpt-6.2-sol", p, 600*time.Second)["OPENCODE_CONFIG_CONTENT"]; got != want {
		t.Fatalf("relay profile changed:\n got %s\nwant %s", got, want)
	}
	if got := managedOpenCodeEnv("opencode/big-pickle", nil, 600*time.Second)["OPENCODE_CONFIG_CONTENT"]; got != `{"autoupdate":false,"enabled_providers":["opencode"],"formatter":false,"lsp":false,"model":"opencode/big-pickle","permission":"deny","plugin":[],"share":"disabled","small_model":"opencode/big-pickle","snapshot":false,"tools":{"*":false}}` {
		t.Fatalf("credential-free profile changed: %s", got)
	}
}

// A key-backed provider declares the model (OpenCode's offline catalog may
// not list it) and passes the key and endpoint only in the profile.
func TestProviderProfile(t *testing.T) {
	p := &ModelProvider{ID: "openai", APIKey: "sk-test-0123456789", BaseURL: "http://127.0.0.1:9/v1"}
	got := managedOpenCodeEnv("openai/gpt-6.2-sol", p, 600*time.Second)["OPENCODE_CONFIG_CONTENT"]
	want := `{"autoupdate":false,"enabled_providers":["openai"],"formatter":false,"lsp":false,"model":"openai/gpt-6.2-sol","permission":"deny","plugin":[],"provider":{"openai":{"models":{"gpt-6.2-sol":{"name":"gpt-6.2-sol"}},"options":{"apiKey":"sk-test-0123456789","baseURL":"http://127.0.0.1:9/v1","chunkTimeout":600000,"headerTimeout":600000}}},"share":"disabled","small_model":"openai/gpt-6.2-sol","snapshot":false,"tools":{"*":false}}`
	if got != want {
		t.Fatalf("got %s", got)
	}
	compat := &ModelProvider{ID: "local", APIKey: "k", BaseURL: "http://127.0.0.1:9/v1", OpenAICompatible: true}
	var s struct {
		Provider map[string]struct {
			NPM    string                    `json:"npm"`
			Models map[string]map[string]any `json:"models"`
		}
	}
	if err := json.Unmarshal([]byte(managedOpenCodeEnv("local/qwen/coder", compat, 0)["OPENCODE_CONFIG_CONTENT"]), &s); err != nil {
		t.Fatal(err)
	}
	if s.Provider["local"].NPM != "@ai-sdk/openai-compatible" || s.Provider["local"].Models["qwen/coder"]["tool_call"] != false {
		t.Fatalf("%+v", s)
	}
	// The key is masked in recorded commands and output without the caller
	// listing it in Redact.
	if v := secretValues(Spec{OpenCodeProvider: p}); len(v) != 1 || v[0] != p.APIKey {
		t.Fatal(v)
	}
}

func TestNativeProviderProfileKeepsIsolationAndMasksHeaders(t *testing.T) {
	p := &ModelProvider{ID: "internal", BaseURL: "http://127.0.0.1:9000/v1", Native: &NativeModel{
		Package: "@ai-sdk/openai-compatible", ID: "upstream/model",
		Headers: map[string]string{"X-Test": "fixture", "Authorization": "Bearer private-token"},
	}}
	if !validOpenCode("internal/alias", p) || validOpenCode("unrelated/alias", p) {
		t.Fatal("native keyless provider validation")
	}
	var profile struct {
		Permission string          `json:"permission"`
		Plugin     []string        `json:"plugin"`
		Tools      map[string]bool `json:"tools"`
		Provider   map[string]struct {
			Options map[string]any            `json:"options"`
			Models  map[string]map[string]any `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(managedOpenCodeEnv("internal/alias", p, 0)["OPENCODE_CONFIG_CONTENT"]), &profile); err != nil {
		t.Fatal(err)
	}
	provider := profile.Provider["internal"]
	if profile.Permission != "deny" || len(profile.Plugin) != 0 || len(profile.Tools) != 1 || profile.Tools["*"] ||
		provider.Options["apiKey"] != nil || provider.Options["baseURL"] != p.BaseURL || provider.Models["alias"]["tool_call"] != false || provider.Models["alias"]["id"] != "upstream/model" {
		t.Fatal("native provider profile failed keyless or isolation requirements")
	}
	secrets := secretValues(Spec{OpenCodeProvider: p})
	if output := redactString("Bearer private-token, private-token, fixture", secrets); strings.Contains(output, "private-token") || strings.Contains(output, "fixture") {
		t.Fatal("native header credentials survived redaction")
	}
	// The operator's own timeouts stay; the ones it left unset wait as long
	// as the call may run.
	p.Native.Timeouts = map[string]any{"headerTimeout": 120000, "timeout": false}
	if err := json.Unmarshal([]byte(managedOpenCodeEnv("internal/alias", p, 600*time.Second)["OPENCODE_CONFIG_CONTENT"]), &profile); err != nil {
		t.Fatal(err)
	}
	if o := profile.Provider["internal"].Options; o["headerTimeout"] != float64(120000) || o["timeout"] != false || o["chunkTimeout"] != float64(600000) {
		t.Fatalf("native timeouts %v", o)
	}
}
