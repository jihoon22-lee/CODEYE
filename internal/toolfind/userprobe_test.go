package toolfind

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// The npm registry probe reads the user's configuration but writes its
// cache and logs in the probe directory, never in HOME.
func TestNpmProbeEnvMovesWritesOutOfHome(t *testing.T) {
	home, dir := "/home/u", "/probe/npm-config"
	got := envMap(npmProbeEnv([]string{"HOME=/home/u", "PATH=/bin", "npm_config_registry=https://npm.example/",
		"NPM_CONFIG_CACHE=/home/u/.npm", "npm_config_logs-dir=/home/u/logs", "NO_UPDATE_NOTIFIER=0"}, home, dir))
	want := map[string]string{
		"HOME": filepath.Join(dir, "home"), "PATH": "/bin", "npm_config_registry": "https://npm.example/",
		"npm_config_cache": filepath.Join(dir, "cache"), "npm_config_logs_dir": filepath.Join(dir, "logs"),
		"npm_config_logs_max": "0", "npm_config_update_notifier": "false", "NO_UPDATE_NOTIFIER": "1",
		"npm_config_userconfig": filepath.Join(home, ".npmrc"),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"NPM_CONFIG_CACHE", "npm_config_logs-dir"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s kept: npm would write there", k)
		}
	}
	// A user configuration file the user chose stays theirs.
	got = envMap(npmProbeEnv([]string{"npm_config_userconfig=/etc/u.npmrc"}, home, dir))
	if got["npm_config_userconfig"] != "/etc/u.npmrc" {
		t.Errorf("userconfig = %q", got["npm_config_userconfig"])
	}
}

// The go env probes keep HOME and the user's go env file, and point the
// configuration directory (telemetry) at the probe directory.
func TestGoProbeEnvTurnsTelemetryOffOutsideHome(t *testing.T) {
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "plan9":
		t.Skip("the go command does not read XDG_CONFIG_HOME here")
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := filepath.Join(t.TempDir(), "go-config")
	got := envMap(goProbeEnv([]string{"HOME=/home/u", "XDG_CONFIG_HOME=" + cfg, "GOFLAGS=-mod=mod", "GOWORK=/w"}, dir))
	want := map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": dir, "GOENV": filepath.Join(cfg, "go", "env"),
		"GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "go", "telemetry", "mode")); err != nil || string(b) != "off\n" {
		t.Fatalf("telemetry mode file: %q %v", b, err)
	}
	if got := envMap(goProbeEnv([]string{"GOENV=off"}, dir)); got["GOENV"] != "off" {
		t.Errorf("the user's GOENV = %q", got["GOENV"])
	}
	// Without a probe directory nothing changes but the three fixed values.
	got = envMap(goProbeEnv([]string{"XDG_CONFIG_HOME=" + cfg}, ""))
	if got["XDG_CONFIG_HOME"] != cfg || got["GOENV"] != "" {
		t.Errorf("without a probe directory: %v", got)
	}
}

// The real probe runs node with the isolated environment: a fake node that
// records its environment shows HOME, cache and logs below the probe
// directory, and the registry of the user's ~/.npmrc still comes back.
func TestNpmRegistryProbeRunsIsolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script node")
	}
	home, probe := t.TempDir(), t.TempDir()
	record := filepath.Join(t.TempDir(), "env")
	node := write(t, filepath.Join(t.TempDir(), "node"), "#!/bin/sh\nenv > "+record+"\nprintf 'https://npm.example/repo/\\n'\n", 0o755)
	npm := write(t, filepath.Join(t.TempDir(), "npm-cli.js"), "", 0o644)
	if got := npmRegistry(context.Background(), node, npm, probe, home); got != "https://npm.example/repo/" {
		t.Fatalf("registry = %q", got)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	env := envMap(strings.Split(strings.TrimSpace(string(raw)), "\n"))
	for _, k := range []string{"HOME", "npm_config_cache", "npm_config_logs_dir"} {
		if !strings.HasPrefix(env[k], probe+string(filepath.Separator)) {
			t.Errorf("%s = %q, not below the probe directory", k, env[k])
		}
	}
	if env["npm_config_userconfig"] != filepath.Join(home, ".npmrc") || env["npm_config_logs_max"] != "0" {
		t.Errorf("env = %v", env)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("the probe wrote in HOME: %v", entries)
	}
	if got := npmRegistry(context.Background(), node, npm, "", home); got != "" {
		t.Errorf("without a probe directory = %q", got)
	}
}
