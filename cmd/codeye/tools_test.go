package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

func toolsConfig(t *testing.T, agent string) (cfgPath, state string) {
	t.Helper()
	root := t.TempDir()
	state = filepath.Join(root, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":"0.1","state_dir":"` + state + `","database":{"mode":"external"},"source_roots":[{"label":"w","path":"` + src + `"}],"agent":` + agent + `}`
	cfgPath = filepath.Join(root, "personal.json")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, state
}

// codeye tools prints the table, prints the same result as JSON with --json,
// and records it in the state directory of the configuration.
func TestToolsCommand(t *testing.T) {
	cfgPath, state := toolsConfig(t, `{"tools":{"ruff":"/nonexistent/ruff"}}`)
	var out, errb bytes.Buffer
	if code := toolsCommand(context.Background(), []string{"--config", cfgPath}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "TOOL") || !strings.Contains(out.String(), "ruff") || !strings.Contains(out.String(), "(config)") {
		t.Fatalf("table:\n%s", out.String())
	}
	out.Reset()
	if code := toolsCommand(context.Background(), []string{"--config", cfgPath, "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var res toolfind.Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Tools) != len(toolfind.Names()) {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	// The explicit ruff path wins even though it does not exist.
	if ruff, _ := res.Tool("ruff"); ruff.Status != toolfind.Unusable || ruff.Source != toolfind.SourceConfig || ruff.Path != "/nonexistent/ruff" {
		t.Fatalf("ruff %+v", ruff)
	}
	back, err := toolfind.Read(filepath.Join(state, toolfind.FileName))
	if err != nil || len(back.Tools) != len(res.Tools) {
		t.Fatalf("tools.json: %v", err)
	}
	// Nothing is created for a missing state directory.
	cfgPath2, state2 := toolsConfig(t, `{}`)
	if err := os.Remove(state2); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := toolsCommand(context.Background(), []string{"--config", cfgPath2}, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(state2); err == nil {
		t.Fatal("tools created the state directory")
	}
	if code := toolsCommand(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "absent.json")}, &out, &errb); code != 2 {
		t.Fatalf("missing config: exit %d", code)
	}
	if code := toolsCommand(context.Background(), []string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("extra argument: exit %d", code)
	}
}

// useDetectedTools completes the agent settings: explicit values stay, the
// rest comes from the result, and both type checkers switch the flag on.
func TestUseDetectedTools(t *testing.T) {
	res := toolfind.Result{GoModCache: "/h/mod", Tools: []toolfind.Tool{
		{Name: "go", Status: toolfind.Found, Path: "/d/go"},
		{Name: "node", Status: toolfind.Found, Path: "/d/node"},
		{Name: "mypy", Status: toolfind.Found, Path: "/d/mypy"},
		{Name: "pyright", Status: toolfind.Found, Path: "/d/pyright/index.js"},
		{Name: "ruff", Status: toolfind.Found, Path: "/d/ruff"},
		{Name: "lizard", Status: toolfind.NotFound},
	}}
	cfg := &personal.Config{Agent: personal.Agent{Tools: map[string]string{"ruff": "/mine/ruff"}}}
	useDetectedTools(cfg, res)
	a := cfg.Agent
	if a.Go != "/d/go" || a.GoModCache != "/h/mod" || a.Tools["ruff"] != "/mine/ruff" || a.Tools["node"] != "/d/node" || !a.PythonBothTypeCheckers {
		t.Fatalf("%+v", a)
	}
	if _, ok := a.Tools["lizard"]; ok {
		t.Fatal("a missing tool must not be set")
	}
	// The flags built from it satisfy the agent's own consistency rule.
	args := agentArgv(&personal.Config{Agent: a, Listen: "127.0.0.1:7331", StateDir: "/s"}, personal.Layout{Root: "/s"}, nil, false)
	if !slices.Contains(args, "--python-both-type-checkers") || !slices.Contains(args, "--mypy") || !slices.Contains(args, "--pyright") {
		t.Fatalf("%q", args)
	}
	// agent.auto_detect false leaves the agent settings alone.
	off := false
	cfg2 := &personal.Config{StateDir: t.TempDir(), Agent: personal.Agent{AutoDetect: &off}}
	var out bytes.Buffer
	if res, err := prepareTools(context.Background(), cfg2, &out); err != nil || len(res.Tools) != 0 || out.Len() != 0 || cfg2.Agent.Go != "" {
		t.Fatalf("auto_detect false: %+v %v %q", res, err, out.String())
	}
	if _, err := os.Stat(filepath.Join(cfg2.StateDir, toolfind.FileName)); err == nil {
		t.Fatal("tools.json written with detection off")
	}
}
