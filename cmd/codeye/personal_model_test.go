package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// With only a stored API key (no agent.model), codeye start gives the agent
// the provider's default model, the credential file path (never the key)
// and model review for every root whose model_review is not false.
func TestPersonalModelFromSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	base := t.TempDir()
	state := filepath.Join(base, "state")
	home := filepath.Join(base, "home")
	exe := filepath.Join(home, ".opencode", "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":"0.1","state_dir":"` + state + `","database":{"mode":"external"},
"source_roots":[{"label":"work","path":"/srv/src"},{"label":"other","path":"/srv/other","model_review":false}]}`
	cfg, err := personal.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := personal.Layout{Root: state}
	personalModelEnv = func() personal.ModelEnv { return personal.ModelEnv{Home: home} }
	t.Cleanup(func() { personalModelEnv = personal.SystemModelEnv })
	roots := []rootGrant{{SourceRoot: cfg.SourceRoots[0], ID: "root-1"}, {SourceRoot: cfg.SourceRoots[1], ID: "root-2"}}
	if got := strings.Join(modelAgentArgs(cfg, layout, roots), " "); got != "" {
		t.Fatalf("no key, no model flags: %s", got)
	}
	const key = "sk-test-start-0123456789"
	if _, err := personal.WriteModelSecretFromEnv(layout, func(k string) string {
		if k == "OPENAI_API_KEY" {
			return key
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(agentArgv(cfg, layout, roots, false), " ")
	want := " --opencode " + exe + " --model openai/gpt-6.2-sol --model-credential-file " + layout.ModelSecret() + " --model-source-root root-1"
	if !strings.Contains(argv, want) || strings.Contains(argv, "root-2 --") || strings.HasSuffix(argv, "root-2") || strings.Contains(argv, key) {
		t.Fatalf("argv %s\nwant %s", argv, want)
	}
}

// The probe names the provider, model and OpenCode release and, when it
// fails, the stage and reason; an untested release is said so.
func TestModelProbeOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell script stands in for OpenCode")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.20.0; exit 0; fi\n" +
		"echo '{\"type\":\"error\",\"sessionID\":\"s\",\"error\":{\"name\":\"APIError\",\"data\":{\"statusCode\":401}}}'\nexit 1\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(dir, "model.json")
	if err := os.WriteFile(cred, []byte(`{"schema_version":"0.1","provider":"openai","api_key":"sk-probe-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := modelCommand(context.Background(), []string{"probe", "--executable", exe, "--model", "openai/gpt-6.2-sol", "--credential-file", cred, "--timeout", "20s"}, &out, &errb)
	text := out.String()
	if code != 1 || !strings.Contains(text, "provider openai, model openai/gpt-6.2-sol, OpenCode 1.20.0 (not the tested "+opencode.Version) ||
		!strings.Contains(text, "FAILED at request: model_auth_failed") || strings.Contains(text+errb.String(), "sk-probe-key") {
		t.Fatalf("code %d\n%s%s", code, text, errb.String())
	}
	out.Reset()
	if code := modelCommand(context.Background(), []string{"probe", "--json", "--executable", exe, "--model", "openai/gpt-6.2-sol", "--credential-file", filepath.Join(dir, "none.json")}, &out, &errb); code != 1 ||
		!strings.Contains(out.String(), `"stage":"credential"`) || !strings.Contains(out.String(), `"reason":"model_credential_unavailable"`) {
		t.Fatalf("%d %s", code, out.String())
	}
}
