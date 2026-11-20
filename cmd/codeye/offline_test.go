package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
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
	if !strings.Contains(got, " --offline --ca-bundle /etc/codeye/ca.pem ") || !strings.Contains(got, " --git /usr/bin/git ") || strings.Contains(got, "--git-host") || strings.Contains(got, "--git-credential") {
		t.Fatalf("argv %s", got)
	}
}

// Offline, GitHub is off even when github.com is written in git_hosts: the
// agent gets no credential file, a pull-request address is refused before
// any GitHub call, and up says so.
func TestOfflineTurnsGitHubOff(t *testing.T) {
	q := func(s string) string { return strconv.Quote(s) }
	state := filepath.Join(t.TempDir(), "state")
	git := filepath.Join(t.TempDir(), "git")
	raw := `{"schema_version":"0.1","state_dir":` + q(state) + `,"database":{"mode":"external"},"offline":true,"git_hosts":["github.com"],"agent":{"git":` + q(git) + `}}`
	cfg, err := instance.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := instance.Layout{Root: cfg.StateDir}
	if args := gitHubCredentialArgs(cfg, layout); args != nil {
		t.Fatalf("credential args offline: %v", args)
	}
	if _, isPR, err := pullRequestTarget(cfg, "https://github.com/o/r/pull/7"); !isPR || err == nil || !strings.Contains(err.Error(), "offline mode does not use GitHub") {
		t.Fatalf("pull request offline: %v %v", isPR, err)
	}
	orig := instanceGitHubEnv
	instanceGitHubEnv = func(toolfind.Result) instance.GitHubEnv {
		return instance.GitHubEnv{Getenv: func(k string) string { t.Fatalf("%s read offline", k); return "" }}
	}
	t.Cleanup(func() { instanceGitHubEnv = orig })
	var out strings.Builder
	if st, cred := setupInstanceGitHub(context.Background(), cfg, toolfind.Result{}, &out); cred != nil || st.Reason != instance.GitHubOffline || !strings.Contains(out.String(), "github: off (offline mode)") {
		t.Fatalf("%+v %q", st, out.String())
	}
	if g := setupGitHubOf(instance.GitHubSetup{Host: "github.com", Reason: instance.GitHubOffline}); g.Enabled || g.Reason != instance.GitHubOffline {
		t.Fatalf("setup summary %+v", g)
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
