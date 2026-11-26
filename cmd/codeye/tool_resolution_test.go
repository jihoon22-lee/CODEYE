package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// toolResolutionCases is fixtures/tool-resolution/cases.json; the evaluation
// runner's test (scripts/test_quality_eval.py) reads the same file.
type toolResolutionCases struct {
	Cases []struct {
		Name            string `json:"name"`
		DependencyFetch bool   `json:"dependency_fetch"`
		Explicit        struct {
			Go         string            `json:"go"`
			GoModCache string            `json:"go_mod_cache"`
			Tools      map[string]string `json:"tools"`
		} `json:"explicit"`
		ToolsJSON toolfind.Result `json:"tools_json"`
		Want      [][]string      `json:"want"`
	} `json:"cases"`
}

// fetchSettingFlags are the dependency fetch flags taken from the detection
// result.
var fetchSettingFlags = []string{"--npm-registry", "--go-proxy", "--go-sumdb", "--go-private", "--go-noproxy", "--go-nosumdb"}

// analyzerToolPairs picks the analyzer tool arguments out of an agent argv as
// [flag] or [flag, value], sorted.
func analyzerToolPairs(argv []string) [][]string {
	valued := map[string]bool{"--go": true, "--go-mod-cache": true}
	for _, f := range fetchSettingFlags {
		valued[f] = true
	}
	for _, name := range knownTools() {
		valued["--"+name] = true
	}
	var out [][]string
	for i := 0; i < len(argv); i++ {
		switch {
		case argv[i] == "--python-both-type-checkers":
			out = append(out, []string{argv[i]})
		case valued[argv[i]] && i+1 < len(argv):
			out = append(out, []string{argv[i], argv[i+1]})
			i++
		}
	}
	slices.SortFunc(out, func(a, b []string) int { return strings.Compare(strings.Join(a, "\x00"), strings.Join(b, "\x00")) })
	return out
}

// The service turns a detection result into the agent's analyzer tool
// arguments exactly as the fixture says; the evaluation runner is held to the
// same file, so the two cannot drift apart unnoticed.
func TestDetectedToolsBecomeTheFixtureAgentArguments(t *testing.T) {
	instanceModelEnv = func() instance.ModelEnv { return instance.ModelEnv{} }
	t.Cleanup(func() { instanceModelEnv = instance.SystemModelEnv })
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "tool-resolution", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases toolResolutionCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases.Cases {
		t.Run(c.Name, func(t *testing.T) {
			enabled := c.DependencyFetch
			cfg := &instance.Config{StateDir: "/srv/codeye/state", Agent: instance.Agent{
				Go: c.Explicit.Go, GoModCache: c.Explicit.GoModCache, Tools: c.Explicit.Tools,
				Dependencies: &instance.Dependencies{Enabled: &enabled}}}
			useDetectedTools(cfg, c.ToolsJSON)
			got := analyzerToolPairs(agentArgv(cfg, instance.Layout{Root: cfg.StateDir}, nil, false))
			want := c.Want
			if want == nil {
				want = [][]string{}
			}
			if got == nil {
				got = [][]string{}
			}
			if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
				t.Fatalf("got  %q\nwant %q", got, want)
			}
		})
	}
}
