package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// With only a stored API key (no agent.model), codeye start gives the agent
// the provider's default model, the credential file path (never the key)
// and model review for every root whose model_review is not false.
func TestInstanceModelFromSecret(t *testing.T) {
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
	cfg, err := instance.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := instance.Layout{Root: state}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{Home: home} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	roots := []sourceRootMapping{{SourceRoot: cfg.SourceRoots[0], ID: "root-1"}, {SourceRoot: cfg.SourceRoots[1], ID: "root-2"}}
	if got := strings.Join(modelAgentArgs(cfg, layout), " "); got != "" {
		t.Fatalf("no key, no model flags: %s", got)
	}
	const key = "sk-test-start-0123456789"
	if _, err := instance.WriteModelSecretFromEnv(layout, func(k string) string {
		if k == "OPENAI_API_KEY" {
			return key
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(agentArgv(cfg, layout, roots, false), " ")
	want := " --opencode " + exe + " --model openai/gpt-6.2-sol --model-credential-file " + layout.ModelSecret()
	// Each project decides whether the model reads its source; no per-root
	// grant is passed, and the root-level model_review is no longer read.
	if !strings.Contains(argv, want) || !strings.Contains(argv, " --project-analysis-policy") || strings.Contains(argv, "--model-source-root") ||
		strings.Contains(argv, "--repo-code-source-root") || strings.Contains(argv, key) {
		t.Fatalf("argv %s\nwant %s", argv, want)
	}
}

// The model step of `codeye up` stores the key from the environment, probes
// once, and does not probe again (no second paid call) until the
// configuration changes, even before the state tree exists.
func TestSetupInstanceModelProbesOnce(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell script stands in for OpenCode")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	exe := filepath.Join(home, ".opencode", "bin", "opencode")
	calls := filepath.Join(base, "calls")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho x >> " + calls + "\nif [ \"$1\" = --version ]; then echo " + opencode.Version + "; exit 0; fi\n" +
		"echo '{\"type\":\"error\",\"sessionID\":\"s\",\"error\":{\"name\":\"APIError\",\"data\":{\"statusCode\":401}}}'\nexit 1\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state")
	cfg, err := instance.Parse([]byte(`{"schema_version":"0.1","state_dir":"`+state+`","database":{"mode":"external"},"source_roots":[{"label":"w","path":"/srv/src"}]}`), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := instance.Layout{Root: state}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{Home: home} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	const key = "sk-test-up-0123456789"
	t.Setenv("OPENAI_API_KEY", key)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENCODE_API_KEY", "")
	var out bytes.Buffer
	m := setupInstanceModel(context.Background(), cfg, layout, false, &out)
	first, _ := os.ReadFile(calls)
	if m == nil || m.ID != "openai/gpt-6.2-sol" || !strings.Contains(out.String(), "stored the openai API key from OPENAI_API_KEY") ||
		!strings.Contains(out.String(), "FAILED at request: model_auth_failed") || len(first) == 0 || strings.Contains(out.String(), key) {
		t.Fatalf("%v\n%s", m, out.String())
	}
	out.Reset()
	setupInstanceModel(context.Background(), cfg, layout, false, &out)
	if again, _ := os.ReadFile(calls); len(again) != len(first) || strings.Contains(out.String(), "model probe") || strings.Contains(out.String(), "stored") {
		t.Fatalf("probed again without a change:\n%s", out.String())
	}
}

// Offline, an API key in the environment does not replace the key file (or
// create one) and is not used.
func TestSetupInstanceModelOfflineKeepsKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	base := t.TempDir()
	state := filepath.Join(base, "state")
	cfg, err := instance.Parse([]byte(`{"schema_version":"0.1","state_dir":"`+state+`","offline":true,"database":{"mode":"external"},"source_roots":[{"label":"w","path":"/srv/src"}]}`), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := instance.Layout{Root: state}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{Home: filepath.Join(base, "home")} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	t.Setenv("OPENAI_API_KEY", "sk-test-env-0123456789")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENCODE_API_KEY", "")
	var out bytes.Buffer
	if m := setupInstanceModel(context.Background(), cfg, layout, true, &out); m != nil {
		t.Fatalf("offline with no key file: %v", m)
	}
	if _, err := os.Stat(layout.ModelSecret()); !os.IsNotExist(err) || strings.Contains(out.String(), "stored") || !strings.Contains(out.String(), "offline mode") {
		t.Fatalf("stat %v\n%s", err, out.String())
	}
	if err := os.MkdirAll(filepath.Dir(layout.ModelSecret()), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"schema_version":"0.1","provider":"openai","api_key":"sk-file-0123456789"}` + "\n")
	if err := os.WriteFile(layout.ModelSecret(), before, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if m := setupInstanceModel(context.Background(), cfg, layout, true, &out); m != nil {
		t.Fatalf("a key file without base_url is used offline: %v", m)
	}
	if after, _ := os.ReadFile(layout.ModelSecret()); !bytes.Equal(after, before) || !strings.Contains(out.String(), "no base_url") {
		t.Fatalf("key file changed or wrong message:\n%s\n%s", after, out.String())
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
	if code != 1 || !strings.Contains(text, "provider openai, model openai/gpt-6.2-sol, OpenCode 1.20.0 (not a tested release: "+opencode.TestedVersionList()) ||
		!strings.Contains(text, "FAILED at request: model_auth_failed") || strings.Contains(text+errb.String(), "sk-probe-key") {
		t.Fatalf("code %d\n%s%s", code, text, errb.String())
	}
	out.Reset()
	if code := modelCommand(context.Background(), []string{"probe", "--json", "--executable", exe, "--model", "openai/gpt-6.2-sol", "--credential-file", filepath.Join(dir, "none.json")}, &out, &errb); code != 1 ||
		!strings.Contains(out.String(), `"stage":"credential"`) || !strings.Contains(out.String(), `"reason":"model_credential_unavailable"`) {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestManagedWorkerLoadsModelBeforeSourceAndProtectsFiles(t *testing.T) {
	root := t.TempDir()
	state, service := filepath.Join(root, "state"), filepath.Join(root, "service.json")
	profile := filepath.Join(root, "settings", "custom-name.jsonc")
	credential := filepath.Join(root, "auth", "existing.json")
	privateHeader := filepath.Join(root, "tokens", "private-header")
	for _, dir := range []string{filepath.Dir(profile), filepath.Dir(credential), filepath.Dir(privateHeader), filepath.Join(root, "project")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// state_dir is deliberately omitted, as in a default installation.
	const config = `{"schema_version":"0.1","database":{"mode":"external"}`
	write(service, config+`}`)
	worker := &instanceModelCaller{settings: localModelSettings(service, state, instance.ModelEnv{}), offline: true}
	first, next := filepath.Join(root, "first"), filepath.Join(root, "next")
	worker.prepareAttempt(first)
	if worker.caller.Model != "" {
		t.Fatal("the initial configuration unexpectedly enables model review")
	}
	write(service, config+fmt.Sprintf(`,"agent":{"model":{"opencode":%q,"config_file":%q,"credential_file":%q}}}`, filepath.Join(root, "opencode"), profile, credential))
	write(profile, `{"model":"corp/default","provider":{"corp":{"options":{"baseURL":"http://127.0.0.1:9000/v1","headers":{"X-Token":"{file:../tokens/private-header}"}}}}}`)
	write(credential, `{"corp":{"type":"api","key":"credential-fixture"}}`)
	write(privateHeader, "header-fixture\n")
	worker.prepareAttempt(first)
	if worker.caller.Model != "" {
		t.Fatal("saved settings changed an active attempt")
	}
	protected := worker.prepareAttempt(next)
	if worker.caller.Model != "corp/default" || worker.caller.NativeConfigDigest == "" || worker.caller.CredentialFile != credential || !worker.caller.Offline {
		t.Fatal("the next worker attempt did not resolve the saved offline configuration")
	}
	for _, name := range []string{"settings", "auth", "tokens"} {
		if err := source.CheckProtected(root, name, protected); !errors.Is(err, source.ErrHoldsProtected) {
			t.Fatalf("source containing selected %s not protected: %v", name, err)
		}
	}
	if err := source.CheckProtected(root, "project", protected); err != nil {
		t.Fatalf("sibling project unexpectedly protected: %v", err)
	}
	write(profile, `{invalid native file`)
	protected = worker.prepareAttempt(filepath.Join(root, "after"))
	if worker.caller.Model != "" {
		t.Fatal("an unreadable native configuration left a stale caller active")
	}
	for _, name := range []string{"settings", "auth", "tokens"} {
		if err := source.CheckProtected(root, name, protected); !errors.Is(err, source.ErrHoldsProtected) {
			t.Fatalf("invalid native config cleared %s protection: %v", name, err)
		}
	}
}

// TC-313: in the service the agent has no model flags; the attempt resolves
// agent.model from the configuration file. An attempt whose model is a
// budget relay runs its review pairs one at a time, the next attempt with a
// key file runs them at the configured degree, and an attempt the caller did
// not prepare counts as serial.
func TestServiceRelayRunsReviewPairsOneAtATime(t *testing.T) {
	root := t.TempDir()
	state, service := filepath.Join(root, "state"), filepath.Join(root, "service.json")
	credential := filepath.Join(root, "model.json")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const config = `{"schema_version":"0.1","database":{"mode":"external"}`
	opencodeExe := filepath.Join(root, "opencode")
	write(service, config+fmt.Sprintf(`,"agent":{"model":{"opencode":%q,"name":"codeye-gateway/gpt-6.2-sol","relay_file":%q,"review_parallel":4}}}`, opencodeExe, filepath.Join(root, "relay.json")))
	cfg, err := instance.LoadForStateDir(service, knownTools(), state)
	if err != nil {
		t.Fatal(err)
	}
	if args := modelAgentArgs(cfg, instance.Layout{Root: state}); slices.Contains(args, "--model-relay-file") || !slices.Contains(args, "--model-instance-config") {
		t.Fatalf("service agent args: %v", args)
	}
	worker := &instanceModelCaller{settings: localModelSettings(service, state, instance.ModelEnv{})}
	first, next := filepath.Join(root, "first"), filepath.Join(root, "next")
	worker.prepareAttempt(first)
	if worker.caller.RelayFile == "" || !worker.servesOneAtATime(first) {
		t.Fatalf("relay attempt: caller relay %q, serial %v", worker.caller.RelayFile, worker.servesOneAtATime(first))
	}
	write(credential, `{"schema_version":"0.1","provider":"openai","api_key":"sk-fixture","base_url":"http://127.0.0.1:9/v1"}`)
	write(service, config+fmt.Sprintf(`,"agent":{"model":{"opencode":%q,"credential_file":%q,"review_parallel":4}}}`, opencodeExe, credential))
	worker.prepareAttempt(next)
	if worker.caller.CredentialFile != credential || worker.servesOneAtATime(next) {
		t.Fatalf("key attempt: caller %q, serial %v", worker.caller.CredentialFile, worker.servesOneAtATime(next))
	}
	if !worker.servesOneAtATime(first) {
		t.Fatal("an attempt the caller does not hold counts as parallel")
	}
}
