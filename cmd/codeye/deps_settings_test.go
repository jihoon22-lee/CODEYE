package main

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// The user's own Go download settings fill the dependency options the
// configuration leaves empty; configured values and a disabled fetch stay.
func TestDetectedGoSettingsFillDependencyOptions(t *testing.T) {
	res := toolfind.Result{GoSettings: &toolfind.GoSettings{GoProxy: "https://goproxy.example", GoPrivate: "git.corp/*", GoSumDB: "off"}}
	cfg := &personal.Config{}
	useDetectedTools(cfg, res)
	if d := cfg.Agent.Dependencies; d == nil || d.GoProxy != "https://goproxy.example" || d.GoPrivate != "git.corp/*" || d.GoSumDB != "off" {
		t.Fatalf("dependencies %+v", d)
	}
	cfg = &personal.Config{Agent: personal.Agent{Dependencies: &personal.Dependencies{GoProxy: "https://mine.example"}}}
	useDetectedTools(cfg, res)
	if d := cfg.Agent.Dependencies; d.GoProxy != "https://mine.example" || d.GoPrivate != "git.corp/*" {
		t.Fatalf("configured value replaced: %+v", d)
	}
	off := false
	cfg = &personal.Config{Agent: personal.Agent{Dependencies: &personal.Dependencies{Enabled: &off}}}
	useDetectedTools(cfg, res)
	if d := cfg.Agent.Dependencies; d.GoPrivate != "" {
		t.Fatalf("filled although fetching is off: %+v", d)
	}
}
