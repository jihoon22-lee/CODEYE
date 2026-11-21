package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNativeConfigSelectsAliasesAndProtectsPrivateReferences(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "token")
	if err := os.WriteFile(key, []byte("fixture-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.jsonc")
	raw := `{
	  // JSONC leaves the actual URL and strings alone.
	  "model":"local/default",
	  "plugin":["file:///must-not-load"],
	  "permission":"allow",
	  "provider":{
	    "ignored":{"npm":"file:///must-not-load"},
	    "local":{"npm":"@ai-sdk/openai-compatible", "options": {
	      "baseURL":"http://127.0.0.1:9000/v1", "apiKey":"{file:token}",
	      "headers":{"X-Extra":"literal // comment", "Authorization":"Bearer {file:~/token}"},
	      "timeout":5000, "includeUsage":false,
	    }, "models":{"alias":{"id":"upstream/model", "name":"A name", "limit":{"context":16000,"output":1024}}}},
	  },
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	name, p, err := NativeSelection(path, "alias", "", true, dir)
	if err != nil || name != "local/alias" || p.ID != "local" || p.BaseURL != "http://127.0.0.1:9000/v1" || p.APIKey != "fixture-secret" ||
		p.Native.Package != "@ai-sdk/openai-compatible" || p.Native.ID != "upstream/model" || p.Native.Limit["context"] != 16000 ||
		p.Native.Headers["X-Extra"] != "literal // comment" || p.Native.Headers["Authorization"] != "Bearer fixture-secret" ||
		p.Native.Timeouts["timeout"] != 5000 || p.Native.IncludeUsage == nil || *p.Native.IncludeUsage {
		t.Fatalf("native profile selection is wrong: %q, %v", name, err)
	}
	digest, paths := PinNativeConfig(path, "local/alias", dir)
	if digest == "" || !slices.Contains(paths, key) {
		t.Fatalf("private reference not protected: %v", paths)
	}
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ConfigFile: path, Model: name, Offline: true, NativeConfigDigest: digest}
	if provider, reason := cfg.provider(); provider != nil || reason != "model_not_configured" {
		t.Fatal("a running attempt used a native config that changed after source protection")
	}
}

func TestNativeConfigRequiresConfiguredOfflineTransport(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      error
	}{
		{"keyless internal", `{"model":"corp/m","provider":{"corp":{"options":{"baseURL":"http://127.0.0.1:9000/v1"}}}}`, nil},
		{"offline public", `{"model":"openai/m","provider":{"openai":{"options":{"apiKey":"fixture"}}}}`, ErrNativeEndpoint},
		{"runtime module", `{"model":"corp/m","provider":{"corp":{"npm":"file:///outside","options":{"baseURL":"http://127.0.0.1:9000/v1"}}}}`, errNativeProvider},
		{"unknown connection setting", `{"model":"openai/m","provider":{"openai":{"options":{"baseUrl":"http://127.0.0.1:9000/v1"}}}}`, errNativeProvider},
		{"missing private key", `{"model":"corp/m","provider":{"corp":{"options":{"baseURL":"http://127.0.0.1:9000/v1","apiKey":"{file:no-such-file}"}}}}`, errNativeFile},
		{"unterminated comment", `{"model":"corp/m"} /*`, errNativeConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.jsonc")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			name, p, err := NativeSelection(path, "", "", true, "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("resolve error %v, want %v", err, tc.want)
			}
			if tc.want == nil && (name != "corp/m" || p.APIKey != "" || p.Native.Package != "@ai-sdk/openai-compatible") {
				t.Fatal("keyless custom endpoint did not select the compatible transport")
			}
		})
	}
}
