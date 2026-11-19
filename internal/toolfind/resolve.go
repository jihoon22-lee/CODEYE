package toolfind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// goModCache asks the Go toolchain for its module cache with the user's real
// environment (a probe environment would answer with a temporary directory).
// go vet runs with GOPROXY=off, so a module that has dependencies can only be
// analyzed when this cache holds them. Falls back to ~/go/pkg/mod.
func goModCache(ctx context.Context, env Env, goExe, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goExe, "env", "GOMODCACHE")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=")
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		if p := strings.TrimSpace(out.String()); p != "" && filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
	}
	if env.Home != "" {
		return filepath.Join(env.Home, "go", "pkg", "mod")
	}
	return ""
}

// Resolved is the tool configuration of the agent: the explicit settings
// where there are any, otherwise what detection found. It maps one-to-one onto
// the agent flags and the personal configuration fields.
type Resolved struct {
	// Go is --go (agent.go).
	Go string
	// GoModCache is --go-mod-cache (agent.go_mod_cache).
	GoModCache string
	// Tools are the analyzer tool flags and the shared toolchain "node"
	// (agent.tools).
	Tools map[string]string
	// PythonBothTypeCheckers is --python-both-type-checkers; true exactly when
	// Tools holds both mypy and pyright (the agent refuses any other
	// combination).
	PythonBothTypeCheckers bool
	// Git (--git, agent.git) and OpenCode (--opencode with --model,
	// agent.model.opencode) are not analyzer arguments: the caller passes them
	// where it handles git sources and the model.
	Git      string
	OpenCode string
	// Sources tells per tool name whether the value is SourceConfig or the
	// detected location.
	Sources map[string]string
}

// Resolve merges a detection result with the explicit configuration. An
// explicit value always wins, even when it is unusable; a detected tool is
// used when it was found (also with an untested version) and is otherwise left
// out, so the agent reports the analyzer as not configured.
func Resolve(res Result, ex Explicit) Resolved {
	r := Resolved{Tools: map[string]string{}, Sources: map[string]string{}}
	pick := func(name, explicit string) string {
		if explicit != "" {
			r.Sources[name] = SourceConfig
			return explicit
		}
		if t, ok := res.Tool(name); ok && t.Status == Found {
			r.Sources[name] = t.Source
			return t.Path
		}
		return ""
	}
	r.Go = pick("go", ex.Go)
	r.Git = pick("git", ex.Git)
	r.OpenCode = pick("opencode", ex.OpenCode)
	for _, name := range Names() {
		switch name {
		case "go", "git", "opencode":
			continue
		}
		if p := pick(name, ex.Tools[name]); p != "" {
			r.Tools[name] = p
		}
	}
	// A tool name this build does not know stays: the personal
	// configuration rejects those before they get here.
	for name, p := range ex.Tools {
		if _, known := r.Tools[name]; !known && p != "" {
			r.Tools[name], r.Sources[name] = p, SourceConfig
		}
	}
	switch {
	case ex.GoModCache != "":
		r.GoModCache = ex.GoModCache
	case r.Go != "":
		r.GoModCache = res.GoModCache
	}
	r.PythonBothTypeCheckers = r.Tools["mypy"] != "" && r.Tools["pyright"] != ""
	return r
}

// Args are the agent flags of the analyzer tooling: --go, --go-mod-cache,
// the tool flags in name order and --python-both-type-checkers.
func (r Resolved) Args() []string {
	var args []string
	if r.Go != "" {
		args = append(args, "--go", r.Go)
	}
	if r.GoModCache != "" {
		args = append(args, "--go-mod-cache", r.GoModCache)
	}
	for _, name := range sortedKeys(r.Tools) {
		args = append(args, "--"+name, r.Tools[name])
	}
	if r.PythonBothTypeCheckers {
		args = append(args, "--python-both-type-checkers")
	}
	return args
}

// Write stores res as path (0600, replaced atomically).
func Write(path string, res Result) error {
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tools-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(raw, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o600)); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Read loads a result written by Write. Unknown fields and other schema
// versions are refused.
func Read(path string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var res Result
	if err := dec.Decode(&res); err != nil {
		return Result{}, errors.New("tools file is not valid")
	}
	if res.SchemaVersion != SchemaVersion {
		return Result{}, errors.New("tools file has an unsupported schema_version")
	}
	return res, nil
}
