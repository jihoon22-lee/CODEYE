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
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	body := `{"schema_version":"0.1","state_dir":` + q(state) + `,"database":{"mode":"external"},"source_roots":[{"label":"w","path":` + q(src) + `}],"agent":` + agent + `}`
	cfgPath = filepath.Join(root, "personal.json")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, state
}

// stubDetection replaces the machine detection with a canned result and
// records the explicit settings it was given.
func stubDetection(t *testing.T) *toolfind.Explicit {
	t.Helper()
	var seen toolfind.Explicit
	old := detectTools
	t.Cleanup(func() { detectTools = old })
	detectTools = func(_ context.Context, cfg *personal.Config) toolfind.Result {
		seen = explicitTools(cfg)
		res := toolfind.Result{SchemaVersion: toolfind.SchemaVersion}
		for _, name := range toolfind.Names() {
			tool := toolfind.Tool{Name: name, Status: toolfind.NotFound, Hint: "install " + name}
			if p := seen.Tools[name]; p != "" {
				tool = toolfind.Tool{Name: name, Status: toolfind.Unusable, Path: p, Source: toolfind.SourceConfig, Reason: "not usable", Hint: "install " + name}
			}
			res.Tools = append(res.Tools, tool)
		}
		return res
	}
	return &seen
}

// codeye tools prints the table, prints the same result as JSON with --json,
// passes the configuration's explicit settings to the detection and records
// the result in the state directory of the configuration.
func TestToolsCommand(t *testing.T) {
	seen := stubDetection(t)
	explicit := filepath.Join(t.TempDir(), "ruff")
	q, _ := json.Marshal(explicit)
	cfgPath, state := toolsConfig(t, `{"tools":{"ruff":`+string(q)+`}}`)
	var out, errb bytes.Buffer
	if code := toolsCommand(context.Background(), []string{"--config", cfgPath}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "TOOL") || !strings.Contains(out.String(), "ruff") || !strings.Contains(out.String(), "(config)") || !strings.Contains(out.String(), "install cppcheck") {
		t.Fatalf("table:\n%s", out.String())
	}
	if seen.Tools["ruff"] != explicit {
		t.Fatalf("explicit settings not passed: %+v", seen)
	}
	out.Reset()
	if code := toolsCommand(context.Background(), []string{"--config", cfgPath, "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var res toolfind.Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Tools) != len(toolfind.Names()) {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if ruff, _ := res.Tool("ruff"); ruff.Status != toolfind.Unusable || ruff.Path != explicit {
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
	// A configuration with detection off says so under the table.
	cfgPath3, _ := toolsConfig(t, `{"auto_detect":false}`)
	out.Reset()
	if code := toolsCommand(context.Background(), []string{"--config", cfgPath3}, &out, &errb); code != 0 || !strings.Contains(out.String(), "agent.auto_detect is false") {
		t.Fatalf("auto_detect false: exit %d\n%s", code, out.String())
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
	// agent.auto_detect false leaves the agent settings alone and writes nothing.
	off := false
	cfg2 := &personal.Config{StateDir: t.TempDir(), Agent: personal.Agent{AutoDetect: &off}}
	var out bytes.Buffer
	if res, err := prepareTools(context.Background(), cfg2, &out); err != nil || len(res.Tools) != 0 || out.Len() != 0 || cfg2.Agent.Go != "" {
		t.Fatalf("auto_detect false: %+v %v %q", res, err, out.String())
	}
	if _, err := os.Stat(filepath.Join(cfg2.StateDir, toolfind.FileName)); err == nil {
		t.Fatal("tools.json written with detection off")
	}
	// With detection on, prepareTools detects, records, prints and completes.
	stubDetection(t)
	cfg3 := &personal.Config{StateDir: t.TempDir(), Agent: personal.Agent{Tools: map[string]string{"ruff": "/mine/ruff"}}}
	out.Reset()
	res3, err := prepareTools(context.Background(), cfg3, &out)
	if err != nil || len(res3.Tools) != len(toolfind.Names()) || !strings.HasPrefix(out.String(), "TOOL") || cfg3.Agent.Tools["ruff"] != "/mine/ruff" {
		t.Fatalf("prepareTools: %v %q %+v", err, out.String(), cfg3.Agent)
	}
	if _, err := toolfind.Read(filepath.Join(cfg3.StateDir, toolfind.FileName)); err != nil {
		t.Fatal(err)
	}
}
