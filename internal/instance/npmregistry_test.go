package instance

import (
	"path/filepath"
	"testing"
)

// agent.dependencies.npm_registry is an http(s) URL without credentials:
// it reaches the agent's arguments and the worker hello.
func TestNpmRegistrySetting(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	c, err := Parse([]byte(cfg(state, `,"agent":{"dependencies":{"npm_registry":"https://npm.example/repo/"}}`)), tools)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.Dependencies.NpmRegistry != "https://npm.example/repo/" {
		t.Fatalf("registry %q", c.Agent.Dependencies.NpmRegistry)
	}
	for _, v := range []string{"https://user:tok@npm.example/", "ftp://npm.example/", "npm.example", "https://npm.example/a b"} {
		if _, err := Parse([]byte(cfg(state, `,"agent":{"dependencies":{"npm_registry":`+q(v)+`}}`)), tools); err == nil {
			t.Errorf("%s accepted", v)
		}
	}
}
