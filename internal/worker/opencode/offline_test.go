package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Offline, a call goes only to an endpoint the operator named: a key file
// without base_url, an OpenCode auth.json entry and a keyless model make no
// request. A configured CA bundle reaches the OpenCode process.
func TestOfflineNeedsAConfiguredEndpoint(t *testing.T) {
	scratch, dir := t.TempDir(), t.TempDir()
	bundle := filepath.Join(dir, "ca.pem")
	calls := 0
	runner := runnerFunc(func(_ context.Context, s executor.Spec) executor.Result {
		calls++
		for k, v := range executor.CAEnv(bundle) {
			if s.Env[k] != v {
				t.Fatalf("%s = %q, want the CA bundle", k, s.Env[k])
			}
		}
		if reflect.DeepEqual(s.Args, []string{"--version"}) {
			return successful(Version + "\n")
		}
		proof, _ := os.ReadFile(s.Args[len(s.Args)-1])
		return successful(events(`{"workspace_proof":"` + strings.TrimSpace(string(proof)) + `","result":{"ok":true}}`))
	})
	q := Request{Scratch: scratch, SnapshotDigest: strings.Repeat("1", 64), Role: "probe", Input: json.RawMessage(`{}`)}
	exe := filepath.Join(dir, "opencode")
	internal := writeCredential(t, t.TempDir(), `{"schema_version":"0.1","provider":"openai","api_key":"sk-in","base_url":"https://llm.internal/v1"}`, 0o600)
	ok := Config{Executable: exe, Model: "openai/gpt-6.2-sol", CredentialFile: internal, Timeout: time.Minute, Offline: true, CABundle: bundle}
	if r := ok.Review(t.Context(), runner, q); r.Reason != "" || calls != 2 {
		t.Fatalf("internal endpoint: %+v (calls %d)", r, calls)
	}
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"openai":{"type":"api","key":"sk-auth"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	public := writeCredential(t, t.TempDir(), `{"schema_version":"0.1","provider":"openai","api_key":"sk-pub"}`, 0o600)
	for name, c := range map[string]Config{
		"key file without base_url": {Executable: exe, Model: "openai/gpt-6.2-sol", CredentialFile: public, Timeout: time.Minute, Offline: true},
		"OpenCode auth.json":        {Executable: exe, Model: "openai/gpt-6.2-sol", CredentialFile: auth, Timeout: time.Minute, Offline: true},
		"keyless model":             {Executable: exe, Model: "opencode/free-model", Timeout: time.Minute, Offline: true},
	} {
		before := calls
		if r := c.Review(t.Context(), runner, q); r.Reason != ReasonEndpointNotConfigured || calls != before {
			t.Errorf("%s: reason %q, %d calls", name, r.Reason, calls-before)
		}
	}
}
