package govet

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Go telemetry is off for every CODEYE go execution (docs/ANALYZERS.md#go-공통-환경과-빌드-문맥): the mode
// file "off" in the XDG config dir, on the platforms whose os.UserConfigDir
// reads XDG_CONFIG_HOME. Windows needs nothing (no AppData in the executor
// environment); darwin has no override.
func TestDisableTelemetry(t *testing.T) {
	env := map[string]string{"PATH": "x"}
	dir := filepath.Join(t.TempDir(), "config")
	if err := DisableTelemetry(env, dir); err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "plan9":
		if _, ok := env["XDG_CONFIG_HOME"]; ok || len(env) != 1 {
			t.Fatalf("env %v", env)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("config dir created: %v", err)
		}
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, "go", "telemetry", "mode"))
	if err != nil || strings.TrimSpace(string(b)) != "off" || env["XDG_CONFIG_HOME"] != dir {
		t.Fatalf("mode %q %v env %v", b, err, env)
	}
}

// With the real go command the telemetry directory holds only the mode file
// after a go vet task: no counter files were opened, so the detached upload
// child that used to write into the executor's removed per-run HOME after
// cleanup was confirmed is not started either.
func TestRealGoVetTelemetryOff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the XDG_CONFIG_HOME override was measured on Linux")
	}
	h := newHandler(t, Config{})
	fx := newFixture(t, "", map[string]string{"go.mod": "module example.test/tel\n\ngo 1.22\n", "a.go": "package a\n\nfunc A() int { return 1 }\n"}, domain.Scope{})
	res := runAll(t, h, fx.Job, &executor.Executor{})
	r, ok := res["go-vet"]
	if !ok || r.Status != domain.TaskSucceeded || len(r.Executions) == 0 {
		t.Fatalf("go vet %+v", res)
	}
	for _, e := range r.Executions {
		if !slices.Contains(e.Result.Command.EnvKeys, "XDG_CONFIG_HOME") {
			t.Fatalf("%s env keys %v", e.Name, e.Result.Command.EnvKeys)
		}
	}
	entries, err := os.ReadDir(filepath.Join(fx.Job.Scratch, "go-vet", "go-vet", "config", "go", "telemetry"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "mode" {
		t.Fatalf("telemetry dir %v %v", entries, err)
	}
}
