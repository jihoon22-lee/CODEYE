package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// The user's own Go download settings fill the dependency options the
// configuration leaves empty; configured values and a disabled fetch stay.
func TestDetectedGoSettingsFillDependencyOptions(t *testing.T) {
	res := toolfind.Result{GoSettings: &toolfind.GoSettings{GoProxy: "https://goproxy.example", GoPrivate: "git.corp/*", GoSumDB: "off"}}
	cfg := &instance.Config{}
	useDetectedTools(cfg, res)
	if d := cfg.Agent.Dependencies; d == nil || d.GoProxy != "https://goproxy.example" || d.GoPrivate != "git.corp/*" || d.GoSumDB != "off" {
		t.Fatalf("dependencies %+v", d)
	}
	cfg = &instance.Config{Agent: instance.Agent{Dependencies: &instance.Dependencies{GoProxy: "https://mine.example"}}}
	useDetectedTools(cfg, res)
	if d := cfg.Agent.Dependencies; d.GoProxy != "https://mine.example" || d.GoPrivate != "git.corp/*" {
		t.Fatalf("configured value replaced: %+v", d)
	}
	off := false
	cfg = &instance.Config{Agent: instance.Agent{Dependencies: &instance.Dependencies{Enabled: &off}}}
	useDetectedTools(cfg, res)
	if d := cfg.Agent.Dependencies; d.GoPrivate != "" {
		t.Fatalf("filled although fetching is off: %+v", d)
	}
}

// A proxy URL with a credential is refused in the configuration, without
// repeating the value.
func TestDependencyProxyCredentialRefused(t *testing.T) {
	for _, field := range []string{"go_proxy", "go_sumdb"} {
		raw := `{"schema_version":"0.1","state_dir":` + jsonString(filepath.Join(t.TempDir(), "state")) + `,"agent":{"dependencies":{"` + field + `":"https://user:s3cret@proxy.example"}}}`
		_, err := instance.Parse([]byte(raw), knownTools())
		if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), field) {
			t.Fatalf("%s: %v", field, err)
		}
	}
}
