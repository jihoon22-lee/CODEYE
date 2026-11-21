package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

type managedOpenCode struct {
	path     string
	layout   instance.Layout
	offline  bool
	caBundle string
	mutating sync.Mutex
}

func newManagedOpenCode(path, stateDir string, offline bool, caBundle string) *managedOpenCode {
	return &managedOpenCode{path: path, layout: instance.Layout{Root: filepath.Clean(stateDir)}, offline: offline, caBundle: caBundle}
}

func (c *managedOpenCode) load() (*instance.Config, error) {
	cfg, err := instance.LoadForStateDir(c.path, knownTools(), c.layout.Root)
	if err != nil {
		return nil, err
	}
	if cfg.StateDir != c.layout.Root {
		return nil, errors.New("the service state directory changed")
	}
	return cfg, nil
}

func (c *managedOpenCode) current(cfg *instance.Config) (httpapi.OpenCodeSettings, *instance.ResolvedModel) {
	view := httpapi.OpenCodeSettings{Editable: true}
	if m := cfg.Agent.Model; m != nil {
		view.OpenCodeFields = httpapi.OpenCodeFields{Executable: m.OpenCode, ConfigFile: m.ConfigFile, CredentialFile: m.CredentialFile, Model: m.Name}
	}
	cfg.Offline, cfg.CABundle = c.offline, c.caBundle
	m, err := instance.ResolveModel(cfg, c.layout, instanceModelEnv())
	var keyRef *opencode.NativeKeyReference
	switch {
	case view.Executable != "" && !usableExecutable(view.Executable):
		view.Reason = "opencode_unavailable"
	case errors.Is(err, opencode.ErrNativeEndpoint):
		view.Reason = "model_endpoint_not_configured"
	case errors.Is(err, opencode.ErrNativeKeyMissing), errors.Is(err, opencode.ErrCredentialUnavailable), errors.As(err, &keyRef):
		view.Reason = "model_credential_unavailable"
	case err != nil && view.ConfigFile != "":
		view.Reason = "opencode_config_invalid"
	case err != nil && view.CredentialFile != "":
		view.Reason = "model_credential_unavailable"
	case err != nil || m == nil:
		view.Reason = "model_not_configured"
	case m.Notice != "":
		view.Reason = "model_credential_unavailable"
	case !usableExecutable(m.OpenCode):
		view.Reason = "opencode_unavailable"
	default:
		status, reason := instance.ModelProbeStatus(c.layout, m)
		view.Configured, view.EffectiveModel, view.Probe, view.Reason = true, &m.ID, &status, reason
		return view, m
	}
	return view, nil
}

func usableExecutable(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	_, err = exec.LookPath(path)
	return err == nil
}

func (c *managedOpenCode) Get() (httpapi.OpenCodeSettings, error) {
	cfg, err := c.load()
	if err != nil {
		return httpapi.OpenCodeSettings{}, err
	}
	view, _ := c.current(cfg)
	return view, nil
}

func servicePath(path, home string) (string, error) {
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~/") && filepath.IsAbs(home) {
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return "", httpapi.ErrOpenCodeValidation
	}
	return filepath.Clean(path), nil
}

func (c *managedOpenCode) Save(fields httpapi.OpenCodeFields) (httpapi.OpenCodeSettings, error) {
	if !c.mutating.TryLock() {
		return httpapi.OpenCodeSettings{}, httpapi.ErrOpenCodeBusy
	}
	defer c.mutating.Unlock()
	home := instanceModelEnv().Home
	edit := instance.OpenCodeEdit{Model: fields.Model}
	for _, field := range []struct {
		raw  string
		dest *string
	}{
		{fields.Executable, &edit.Executable}, {fields.ConfigFile, &edit.ConfigFile}, {fields.CredentialFile, &edit.CredentialFile},
	} {
		path, err := servicePath(field.raw, home)
		if err != nil {
			return httpapi.OpenCodeSettings{}, err
		}
		*field.dest = path
	}
	var view httpapi.OpenCodeSettings
	cfg, err := instance.EditFileChecked(c.path, knownTools(), instance.ConfigEdit{OpenCode: &edit}, func(cfg *instance.Config) error {
		if cfg.StateDir != c.layout.Root {
			return httpapi.ErrOpenCodeValidation
		}
		view, _ = c.current(cfg)
		if edit.Executable != "" && !usableExecutable(edit.Executable) ||
			(edit.CredentialFile != "" || edit.ConfigFile != "") && !view.Configured {
			return httpapi.ErrOpenCodeValidation
		}
		return nil
	})
	if err != nil {
		// The instance validator may describe operator-entered values. The
		// HTTP adapter returns only stable reason codes.
		return view, httpapi.ErrOpenCodeValidation
	}
	view, _ = c.current(cfg)
	return view, nil
}

func (c *managedOpenCode) Probe(ctx context.Context) (httpapi.OpenCodeSettings, error) {
	if !c.mutating.TryLock() {
		return httpapi.OpenCodeSettings{}, httpapi.ErrOpenCodeBusy
	}
	defer c.mutating.Unlock()
	cfg, err := c.load()
	if err != nil {
		return httpapi.OpenCodeSettings{}, err
	}
	view, model := c.current(cfg)
	if model == nil {
		status := httpapi.ProbeFailed
		view.Probe = &status
		return view, nil
	}
	identity := model.ProbeFingerprint()
	result := probeModel(ctx, model.Caller())
	if err := instance.RecordModelProbeFor(c.layout, identity, result.Verified, result.Reason); err != nil {
		return httpapi.OpenCodeSettings{}, err
	}
	return c.Get()
}
