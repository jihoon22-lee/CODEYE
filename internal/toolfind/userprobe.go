package toolfind

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The settings probes (`go env`, `npm config get registry`) run in the user's
// real environment, because their answer is the user's own configuration: the
// go env file, ~/.npmrc and the variables. They must still write nothing in
// the user's home. npm writes a debug log (~/.npm/_logs) and its cache for
// every command, and the go command writes telemetry counters below the user
// configuration directory (docs/ANALYZERS.md#go-공통-환경과-빌드-문맥). The
// functions here keep the reading and move the writing into a probe
// directory.

// npmProbeEnv is the environment of `npm config get registry`: the user's
// environment with HOME, the npm cache and the log directory in dir, log
// files off and the update notifier off. The user configuration file is
// still the user's: their npm_config_userconfig when set, otherwise
// <home>/.npmrc, which npm would have read with the real HOME.
func npmProbeEnv(environ []string, home, dir string) []string {
	override := map[string]string{
		"cache":           filepath.Join(dir, "cache"),
		"logs_dir":        filepath.Join(dir, "logs"),
		"logs_max":        "0",
		"update_notifier": "false",
	}
	userconfig := ""
	out := make([]string, 0, len(environ)+7)
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if key, ok := npmConfigKey(k); ok {
			if key == "userconfig" {
				userconfig = v
				continue
			}
			if _, replaced := override[key]; replaced {
				continue
			}
		}
		if n := envKey(k); n == "HOME" || n == "NO_UPDATE_NOTIFIER" {
			continue
		}
		out = append(out, kv)
	}
	if userconfig == "" && home != "" {
		userconfig = filepath.Join(home, ".npmrc")
	}
	out = append(out, "HOME="+filepath.Join(dir, "home"), "NO_UPDATE_NOTIFIER=1")
	for _, key := range []string{"cache", "logs_dir", "logs_max", "update_notifier"} {
		out = append(out, "npm_config_"+key+"="+override[key])
	}
	if userconfig != "" {
		out = append(out, "npm_config_userconfig="+userconfig)
	}
	return out
}

// npmConfigKey returns the npm setting an environment variable sets: npm
// reads npm_config_* case-insensitively and treats - and _ alike.
func npmConfigKey(name string) (string, bool) {
	const prefix = "npm_config_"
	if len(name) <= len(prefix) || !strings.EqualFold(name[:len(prefix)], prefix) {
		return "", false
	}
	return strings.ReplaceAll(strings.ToLower(name[len(prefix):]), "-", "_"), true
}

// envKey compares variable names the way the OS does.
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// goProbeEnv is the environment of the `go env` probes: the user's
// environment (HOME stays: the default GOPATH and module cache are below
// it) with GOTOOLCHAIN=local, GOWORK=off and an empty GOFLAGS. On the systems
// where the go command finds its configuration directory through
// XDG_CONFIG_HOME, that variable points at dir with telemetry off, and GOENV
// keeps the go env file the user's: their GOENV when set, otherwise the file
// below their configuration directory that go would have read. When dir
// cannot hold the telemetry setting the user's configuration directory stays.
func goProbeEnv(environ []string, dir string) []string {
	config, userGoEnv := "", ""
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "plan9":
		// The configuration directory is not XDG_CONFIG_HOME there.
	default:
		if goTelemetryOff(dir) == nil {
			config = dir
		}
	}
	out := make([]string, 0, len(environ)+5)
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		switch envKey(k) {
		case "GOTOOLCHAIN", "GOWORK", "GOFLAGS":
			continue
		case "GOENV":
			userGoEnv = v
		case "XDG_CONFIG_HOME":
			if config != "" {
				continue
			}
		}
		out = append(out, kv)
	}
	out = append(out, "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=")
	if config == "" {
		return out
	}
	if userGoEnv == "" {
		if cfg, err := os.UserConfigDir(); err == nil {
			out = append(out, "GOENV="+filepath.Join(cfg, "go", "env"))
		}
	}
	return append(out, "XDG_CONFIG_HOME="+config)
}

// goTelemetryOff writes the go command's telemetry mode file below dir.
func goTelemetryOff(dir string) error {
	if dir == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Join(dir, "go", "telemetry"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "go", "telemetry", "mode"), []byte("off\n"), 0o600)
}

// probePath is the directory of one settings probe below the probe
// directory, which Detect removes; "" without a probe directory.
func probePath(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}
