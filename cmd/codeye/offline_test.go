package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
)

// Offline mode and the CA bundle reach the local agent; offline seeds no
// Git host, so git is not given either.
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
	if !strings.Contains(got, " --offline --ca-bundle /etc/codeye/ca.pem ") || strings.Contains(got, "--git") {
		t.Fatalf("argv %s", got)
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
