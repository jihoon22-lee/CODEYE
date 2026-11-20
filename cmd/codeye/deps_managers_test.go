package main

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps"
)

// The package managers are known tool names (agent.tools.npm ...
// agent.tools.uv become --npm ... --uv) and the index becomes
// --pip-index-url.
func TestInstanceAgentArgvPackageManagers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	if !slices.Equal(deps.ManagerFlags(), []string{"npm", "pnpm", "python", "uv", "yarn"}) {
		t.Fatalf("manager flags %v", deps.ManagerFlags())
	}
	for _, m := range deps.ManagerFlags() {
		if !slices.Contains(knownTools(), m) {
			t.Fatalf("known tools %v lack %s", knownTools(), m)
		}
	}
	raw := `{"schema_version":"0.1","state_dir":"/srv/codeye/state","database":{"mode":"external"},"agent":{"tools":{"python":"/usr/bin/python3","uv":"/opt/uv","pnpm":"/opt/pnpm/bin/pnpm.cjs"},"dependencies":{"pip_index_url":"https://pypi.example/simple"}}}`
	cfg, err := instance.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(agentArgv(cfg, instance.Layout{Root: cfg.StateDir}, nil, false), " ")
	for _, want := range []string{" --pip-index-url https://pypi.example/simple", " --python /usr/bin/python3", " --uv /opt/uv", " --pnpm /opt/pnpm/bin/pnpm.cjs"} {
		if !strings.Contains(got, want) {
			t.Fatalf("argv %s lacks %s", got, want)
		}
	}
}

// A credential in the index URL is refused without repeating it.
func TestPipIndexCredentialRefused(t *testing.T) {
	raw := `{"schema_version":"0.1","state_dir":` + jsonString(filepath.Join(t.TempDir(), "state")) + `,"agent":{"dependencies":{"pip_index_url":"https://user:s3cret@pypi.example/simple"}}}`
	_, err := instance.Parse([]byte(raw), knownTools())
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "pip_index_url") {
		t.Fatalf("%v", err)
	}
}

// Python packages are prepared only where mypy or pyright is configured and
// usable.
func TestPythonConsumers(t *testing.T) {
	cfg := builtin.Config{Tools: map[string]string{"ruff": "/x/ruff"}}
	if pythonConsumers(cfg, nil) {
		t.Fatal("ruff alone reads no third-party packages")
	}
	cfg.Tools["mypy"] = "/x/mypy"
	if !pythonConsumers(cfg, nil) {
		t.Fatal("configured mypy")
	}
	if pythonConsumers(cfg, []builtin.Unavailable{{Name: "mypy", Reason: "probe_failed"}}) {
		t.Fatal("unusable mypy")
	}
	cfg.Tools["pyright"] = "/x/index.js"
	if !pythonConsumers(cfg, []builtin.Unavailable{{Name: "mypy", Reason: "probe_failed"}}) {
		t.Fatal("pyright usable")
	}
}
