package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

// Offline mode and the CA bundle reach the local agent; offline seeds no
// Git host and passes no GitHub tokens. git is still given: a Git host the
// administrator approves in the web (an internal server) works without a
// restart.
func TestInstanceAgentArgvOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	raw := `{"schema_version":"0.1","state_dir":"/srv/codeye/state","database":{"mode":"external"},"offline":true,"ca_bundle":"/etc/codeye/ca.pem",
"agent":{"go":"/usr/local/go/bin/go","git":"/usr/bin/git"}}`
	cfg, err := instance.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(agentArgv(cfg, instance.Layout{Root: cfg.StateDir}, nil, false), " ")
	if !strings.Contains(got, " --offline --ca-bundle /etc/codeye/ca.pem ") || !strings.Contains(got, " --git /usr/bin/git ") || strings.Contains(got, "--git-host") || !strings.Contains(got, "--git-credential-file /srv/codeye/state/secrets/github-platform.json") {
		t.Fatalf("argv %s", got)
	}
}

func TestOfflinePullRequestHost(t *testing.T) {
	cfg := &instance.Config{Offline: true, GitHosts: []string{"github.com", "ghe.corp.example"}}
	if _, isPR, err := pullRequestTarget(cfg, "https://github.com/o/r/pull/7"); !isPR || err == nil {
		t.Fatalf("public pull request offline: %v %v", isPR, err)
	}
	if target, isPR, err := pullRequestTarget(cfg, "https://ghe.corp.example/o/r/pull/7"); !isPR || err != nil || target.pullRequest != 7 {
		t.Fatalf("enterprise pull request offline: %v %v", isPR, err)
	}
}

// The offline example (examples/config-offline.json, docs/OPERATIONS.md)
// is a valid configuration: offline, an internal Git host, internal
// registries and a CA bundle.
func TestInstanceOfflineExampleParses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the example uses Linux paths")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "config-offline.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := instance.Parse(raw, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Agent.Dependencies
	if !cfg.Offline || cfg.CABundle == "" || len(cfg.GitHosts) != 1 || d == nil || d.GoProxy == "" || d.NpmRegistry == "" || d.PipIndexURL == "" {
		t.Fatalf("%+v", cfg)
	}
}
