package agent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The package managers and the index travel in the hello only while the
// operator's switch is on; empty paths are left out.
func TestHelloCarriesPythonPackageManagers(t *testing.T) {
	py := filepath.Join(t.TempDir(), "python3")
	ag := Agent{cfg: Config{StateDir: t.TempDir(), DependencyFetch: true, PackageManagers: map[string]string{"python": py, "uv": ""}, PipIndexURL: "https://pypi.example/simple"}}
	d := ag.dependencies()
	if d == nil || len(d.PackageManagers) != 1 || d.PackageManagers["python"] != py || d.PipIndexURL != "https://pypi.example/simple" {
		t.Fatalf("hello dependencies %+v", d)
	}
	ag.cfg.DependencyFetch = false
	if ag.dependencies() != nil {
		t.Fatal("switch off still sends dependencies")
	}
}

// An index URL with a credential, or a relative package manager path, is a
// usage error that does not repeat the value.
func TestPythonDependencyFlagsRefused(t *testing.T) {
	for _, cfg := range []Config{{PipIndexURL: "https://user:s3cret@pypi.example/simple"}, {PipIndexURL: "file:///s3cret"}, {PackageManagers: map[string]string{"python": "s3cret/python3"}}} {
		cfg.ControllerURL, cfg.StateDir, cfg.Workers, cfg.DependencyFetch = "http://127.0.0.1:1", t.TempDir(), 1, true
		_, err := New(cfg)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != ExitUsage || strings.Contains(exit.Msg, "s3cret") {
			t.Fatalf("%+v: %v", cfg, err)
		}
	}
}
