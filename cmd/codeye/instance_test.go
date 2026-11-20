package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

// The documented example (examples/config.json, docs/GUIDE.md) is a valid
// configuration for this build's tool names.
func TestInstanceExampleConfigParses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the example uses Linux paths")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := instance.Parse(raw, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quota.Limit("artifacts") == 0 || len(cfg.Quota.NodeLimits()) != 4 || cfg.BackupDir == "" {
		t.Fatalf("%+v", cfg.Quota)
	}
}

// The service (docs/OPERATIONS.md#up-open-start-stop-status): codeye start turns the configuration into the agent's
// argv: autoscale, roots, the per-project analysis policy, git, tools,
// model and storage quota. The root-level repository_code and model_review
// of older configurations no longer produce per-root grants. No
// database URL or token value is ever in it (only the token file path).
func TestInstanceAgentArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	raw := `{"schema_version":"0.1","state_dir":"/srv/codeye/state","database":{"mode":"external"},
"source_roots":[{"label":"work","path":"/srv/src","repository_code":true,"model_review":true}],
"agent":{"max_workers":3,"go":"/usr/local/go/bin/go","git":"/usr/bin/git","tools":{"staticcheck":"/opt/sc"},
"model":{"opencode":"/opt/opencode","name":"provider/model-a","relay_file":"/srv/relay.json","review_max_calls":50}},
"quota":{"logs_bytes":100,"workspace_bytes":200,"artifacts_bytes":300}}`
	cfg, err := instance.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := instance.Layout{Root: cfg.StateDir}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	argv := agentArgv(cfg, layout, []rootGrant{{SourceRoot: cfg.SourceRoots[0], ID: "root-1"}}, true)
	got := strings.Join(argv, " ")
	want := "agent --controller http://127.0.0.1:7331 --state-dir /srv/codeye/state/agent --name local --autoscale --max-workers 3 --toolchain-path /usr/bin:/bin --advertise-without-baseline" +
		" --enroll-token-file /srv/codeye/state/run/enroll-token --source-root root-1=/srv/src --project-analysis-policy --controller-sources --protected-dir /srv/codeye/state --protected-dir /srv/codeye/state-backups --git /usr/bin/git --git-host github.com" +
		" --git-credential-dir /srv/codeye/state/secrets/github-users --go /usr/local/go/bin/go --dependency-fetch --dependency-cache-dir /srv/codeye/state/cache/deps --staticcheck /opt/sc --opencode /opt/opencode --model provider/model-a --model-relay-file /srv/relay.json --review-max-calls 50" +
		" --storage-quota logs=100 --storage-quota workspace=200 --storage-log-dir /srv/codeye/state/logs"
	if got != want {
		t.Fatalf("argv\n got %s\nwant %s", got, want)
	}
	// Re-start of an enrolled agent: no token file; no artifacts quota on the node.
	again := strings.Join(agentArgv(cfg, layout, nil, false), " ")
	if strings.Contains(again, "enroll-token") || strings.Contains(again, "artifacts=") {
		t.Fatal(again)
	}
	// github.enabled false only stops looking up the administrator's token:
	// the users' own tokens still reach the worker.
	off := false
	cfg.GitHub.Enabled = &off
	if off := strings.Join(agentArgv(cfg, layout, nil, false), " "); !strings.Contains(off, "--git-credential-dir") || !strings.Contains(off, "--git-host github.com") {
		t.Fatal(off)
	}
}

// agent.dependencies: on by default; its options become the agent's flags,
// and enabled=false removes the operator switch (no project fetches).
func TestInstanceAgentArgvDependencies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	for _, tc := range []struct{ deps, want string }{
		{`{"cache_dir":"/srv/deps","go_proxy":"https://goproxy.example","timeout_seconds":60,"total_timeout_seconds":120,"max_download_bytes":1000}`,
			" --dependency-fetch --dependency-cache-dir /srv/deps --go-proxy https://goproxy.example --dependency-timeout 60s --dependency-total-timeout 120s --dependency-max-bytes 1000"},
		{`{"enabled":false,"cache_dir":"/srv/deps"}`, ""},
	} {
		raw := `{"schema_version":"0.1","state_dir":"/srv/codeye/state","database":{"mode":"external"},"agent":{"go":"/usr/local/go/bin/go","dependencies":` + tc.deps + `}}`
		cfg, err := instance.Parse([]byte(raw), knownTools())
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(agentArgv(cfg, instance.Layout{Root: cfg.StateDir}, nil, false), " ")
		if !strings.Contains(got, " --go /usr/local/go/bin/go"+tc.want) || (tc.want == "" && strings.Contains(got, "--dependency")) {
			t.Fatalf("argv %s\nwant ...%s", got, tc.want)
		}
	}
}
