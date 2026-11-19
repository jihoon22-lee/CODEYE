package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// Offline mode and the CA bundle reach the local agent; offline seeds no
// Git host and passes no GitHub tokens. git is still given: a Git host the
// administrator approves in the web (an internal server) works without a
// restart.
func TestPersonalAgentArgvOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	personalModelEnv = func() personal.ModelEnv { return personal.ModelEnv{} }
	t.Cleanup(func() { personalModelEnv = personal.SystemModelEnv })
	raw := `{"schema_version":"0.1","state_dir":"/srv/codeye/state","database":{"mode":"external"},"offline":true,"ca_bundle":"/etc/codeye/ca.pem",
"agent":{"go":"/usr/local/go/bin/go","git":"/usr/bin/git"}}`
	cfg, err := personal.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(agentArgv(cfg, personal.Layout{Root: cfg.StateDir}, nil, false), " ")
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
	cfg, err := personal.Parse([]byte(raw), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	layout := personal.Layout{Root: cfg.StateDir}
	if args := gitHubCredentialArgs(cfg, layout); args != nil {
		t.Fatalf("credential args offline: %v", args)
	}
	if _, isPR, err := pullRequestTarget(cfg, "https://github.com/o/r/pull/7"); !isPR || err == nil || !strings.Contains(err.Error(), "offline mode does not use GitHub") {
		t.Fatalf("pull request offline: %v %v", isPR, err)
	}
	orig := personalGitHubEnv
	personalGitHubEnv = func(toolfind.Result) personal.GitHubEnv {
		return personal.GitHubEnv{Getenv: func(k string) string { t.Fatalf("%s read offline", k); return "" }}
	}
	t.Cleanup(func() { personalGitHubEnv = orig })
	var out strings.Builder
	if st := setupPersonalGitHub(context.Background(), cfg, layout, toolfind.Result{}, &out); st.Reason != personal.GitHubOffline || !strings.Contains(out.String(), "github: off (offline mode)") {
		t.Fatalf("%+v %q", st, out.String())
	}
	if g := setupGitHubOf(personal.GitHubSetup{Host: "github.com", Reason: personal.GitHubOffline}); g.Enabled || g.Reason != personal.GitHubOffline {
		t.Fatalf("setup summary %+v", g)
	}
}

// The offline example (examples/personal-offline.json, docs/OPERATIONS.md)
// is a valid configuration: offline, an internal Git host, internal
// registries and a CA bundle.
func TestPersonalOfflineExampleParses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the example uses Linux paths")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "personal-offline.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := personal.Parse(raw, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Agent.Dependencies
	if !cfg.Offline || cfg.CABundle == "" || len(cfg.GitHosts) != 1 || d == nil || d.GoProxy == "" || d.NpmRegistry == "" || d.PipIndexURL == "" {
		t.Fatalf("%+v", cfg)
	}
}
