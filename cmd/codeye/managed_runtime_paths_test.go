package main

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

func TestManagedRuntimePathChangesPreserveOtherSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installation paths")
	}
	root := t.TempDir()
	manager := managedRuntimePaths{path: filepath.Join(root, "codeye.json"), layout: instance.Layout{Root: filepath.Join(root, "state")}}
	initial := []byte(`{"schema_version":"0.1","agent":{"tools":{"ruff":"/service/ruff"},"go":"/old/go"}}`)
	if err := os.WriteFile(manager.path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]string{"go": "/automatic/go"}
	if err := manager.layout.WriteRuntimeDefaults(defaults); err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	interpreter := filepath.Join(root, "bin", "python3")
	if err := os.Symlink(python, interpreter); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Save(context.Background(), map[string]string{"python": root, "go": "relative/go"}); err == nil {
		t.Fatal("accepted a relative path")
	}
	after, err := os.ReadFile(manager.path)
	if err != nil || !bytes.Equal(after, initial) {
		t.Fatalf("invalid batch changed file: %s, %v", after, err)
	}
	var invalid *httpapi.InvalidRuntimePath
	if _, err := manager.Save(context.Background(), map[string]string{"java": interpreter}); !errors.As(err, &invalid) || invalid.Name != "java" {
		t.Fatalf("wrong executable: %v", err)
	}
	saved, err := manager.Save(context.Background(), map[string]string{"python": root, "go": ""})
	if err != nil || !saved.Editable || !saved.AutoDetect || !maps.Equal(saved.Paths, map[string]string{"python": interpreter}) || !maps.Equal(saved.AutomaticPaths, defaults) {
		t.Fatalf("save: %+v, %v", saved, err)
	}
	cfg, err := instance.LoadForStateDir(manager.path, knownTools(), manager.layout.Root)
	if err != nil || cfg.Agent.Tools["ruff"] != "/service/ruff" {
		t.Fatalf("unrelated tool lost: %v", err)
	}
}

func TestApplyWorkerRuntimePathsPreservesRunningConfiguration(t *testing.T) {
	h := &workerproto.Hello{GoExecutable: "go-old", AnalyzerTools: map[string]string{"cargo": "cargo-old", "ruff": "ruff"},
		TestPackageManagers: map[string]string{"python": "python-old", "npm": "npm"},
		Dependencies:        &workerproto.Dependencies{PackageManagers: map[string]string{"python": "python-old", "uv": "uv"}}}
	original := helloPaths(h)
	selected := map[string]string{"go": "go-new", "python": "python-new", "java": "java-new", "node": "node-new"}
	next := applyWorkerPaths(h, selected, &instance.Config{})
	if !maps.Equal(helloPaths(&next), selected) || !maps.Equal(helloPaths(h), original) {
		t.Fatal("updated runtime paths were not isolated to the next attempt")
	}
	if next.Dependencies.PackageManagers["python"] != "python-new" || h.Dependencies.PackageManagers["python"] != "python-old" || next.Dependencies.PackageManagers["uv"] != "uv" || next.TestPackageManagers["npm"] != "npm" || next.AnalyzerTools["ruff"] != "ruff" {
		t.Fatal("unrelated manager or running dependency settings changed")
	}
}
