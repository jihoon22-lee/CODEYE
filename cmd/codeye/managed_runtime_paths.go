package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

type managedRuntimePaths struct {
	path   string
	layout instance.Layout
}

func (m managedRuntimePaths) view(cfg *instance.Config) (httpapi.RuntimePathSettings, error) {
	if cfg.StateDir != m.layout.Root {
		return httpapi.RuntimePathSettings{}, errors.New("installation directory changed")
	}
	defaults, err := m.layout.RuntimeDefaults()
	if err != nil {
		return httpapi.RuntimePathSettings{}, err
	}
	if !cfg.Agent.AutoDetectEnabled() {
		clear(defaults)
	}
	return httpapi.RuntimePathSettings{Editable: true, AutoDetect: cfg.Agent.AutoDetectEnabled(), Paths: cfg.RuntimePaths(), AutomaticPaths: defaults}, nil
}

func (m managedRuntimePaths) Get() (httpapi.RuntimePathSettings, error) {
	cfg, err := instance.LoadForStateDir(m.path, knownTools(), m.layout.Root)
	if err != nil {
		return httpapi.RuntimePathSettings{}, err
	}
	return m.view(cfg)
}

// Installations and virtual environments can be selected by their root folder.
func runtimeInDirectory(root, name string) string {
	bin := filepath.Join(root, "bin", name)
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{bin + ".exe", filepath.Join(root, name+".exe"), filepath.Join(root, "Scripts", name+".exe")} {
			if usableExecutable(candidate) {
				return candidate
			}
		}
	}
	if name == "python" && !usableExecutable(bin) {
		return filepath.Join(root, "bin", "python3")
	}
	return bin
}

func (m managedRuntimePaths) Save(ctx context.Context, changes map[string]string) (httpapi.RuntimePathSettings, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	home, _ := os.UserHomeDir()
	edits := map[string]string{}
	for name, raw := range changes {
		p, err := servicePath(raw, home)
		if err != nil {
			return httpapi.RuntimePathSettings{}, &httpapi.InvalidRuntimePath{Name: name}
		}
		if p != "" {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				p = runtimeInDirectory(p, name)
			}
			if !usableExecutable(p) || !toolfind.ValidRuntimeExecutable(ctx, name, p) {
				return httpapi.RuntimePathSettings{}, &httpapi.InvalidRuntimePath{Name: name}
			}
		}
		edits[name] = p
	}
	var view httpapi.RuntimePathSettings
	_, err := instance.EditFileForStateDir(m.path, knownTools(), instance.ConfigEdit{RuntimePaths: edits}, m.layout.Root, func(cfg *instance.Config) error {
		var err error
		view, err = m.view(cfg)
		return err
	})
	return view, err
}
