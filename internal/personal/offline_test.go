package personal

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Offline seeds no Git host; an explicit list stays as written. ca_bundle
// must be an absolute path.
func TestOfflineConfiguration(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	c, err := Parse([]byte(cfg(state, `,"offline":true`)), tools)
	if err != nil || !c.Offline || len(c.GitHosts) != 0 || c.GitHostsExplicit() {
		t.Fatalf("offline default hosts %v explicit %v: %v", c.GitHosts, c.GitHostsExplicit(), err)
	}
	c, err = Parse([]byte(cfg(state, `,"offline":true,"git_hosts":["git.internal:8443"]`)), tools)
	if err != nil || !slices.Equal(c.GitHosts, []string{"git.internal:8443"}) || !c.GitHostsExplicit() {
		t.Fatalf("explicit hosts %v: %v", c.GitHosts, err)
	}
	c, err = Parse([]byte(cfg(state, "")), tools)
	if err != nil || !slices.Equal(c.GitHosts, DefaultGitHosts) || c.GitHostsExplicit() {
		t.Fatalf("online default hosts %v: %v", c.GitHosts, err)
	}
	if _, err := Parse([]byte(cfg(state, `,"ca_bundle":"relative/ca.pem"`)), tools); err == nil || !strings.Contains(err.Error(), "ca_bundle") {
		t.Fatalf("relative ca_bundle: %v", err)
	}
}

// codeye up --offline saves the setting in the file.
func TestEditFileSetsOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "personal.json")
	if err := os.WriteFile(path, []byte(cfg(filepath.Join(dir, "state"), "")), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := EditFile(path, tools, ConfigEdit{Offline: true})
	if err != nil || !c.Offline || len(c.GitHosts) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	again, err := Load(path, tools)
	if err != nil || !again.Offline {
		t.Fatalf("reloaded offline=%v: %v", again != nil && again.Offline, err)
	}
}

// Offline, the model is reached only through an endpoint the operator
// named: a key file with base_url or the relay. OpenCode's own login, a key
// file without base_url and a keyless model are refused.
func TestOfflineModelNeedsAnEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	home := fakeHome(t, `{"openai":{"type":"api","key":"sk-auth"}}`, "")
	c, layout := modelConfig(t, "")
	c.Offline = true
	if m, err := ResolveModel(c, layout, home); m != nil || err != nil {
		t.Fatalf("auth.json must not be used offline: %+v %v", m, err)
	}
	write := func(body string) {
		if err := os.MkdirAll(layout.SecretsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layout.ModelSecret(), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"schema_version":"0.1","provider":"openai","api_key":"sk-pub"}`)
	if _, err := ResolveModel(c, layout, home); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("key file without base_url: %v", err)
	}
	write(`{"schema_version":"0.1","provider":"openai","api_key":"sk-in","base_url":"https://llm.internal/v1"}`)
	c.CABundle = "/etc/codeye/ca.pem"
	m, err := ResolveModel(c, layout, home)
	if err != nil || m.Source != ModelSourceSecret || !m.Caller().Offline || m.Caller().CABundle != "/etc/codeye/ca.pem" {
		t.Fatalf("internal endpoint: %+v %v", m, err)
	}
	keyless, layout2 := modelConfig(t, `{"name":"opencode/free-model"}`)
	keyless.Offline = true
	if _, err := ResolveModel(keyless, layout2, home); err == nil {
		t.Fatal("a keyless model answers from a public endpoint")
	}
}
