package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// fakeRust writes a toolchain bin directory whose tools answer the version
// commands (a shell script, so Linux and macOS only).
func fakeRust(t *testing.T, withRustup bool) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script toolchain")
	}
	dir = t.TempDir()
	script := func(name, version string) {
		body := "#!/bin/sh\ncase \"$1\" in\n--version) echo '" + version + "';;\n-vV) printf 'rustc 1.99.1 (58cf8f9c3 2026-10-13)\\nhost: x86_64-unknown-linux-gnu\\nrelease: 1.99.1\\n';;\nesac\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("cargo", "cargo 1.99.1 (5f94df478 2026-08-27)")
	script("rustc", "")
	script("cargo-clippy", "clippy 0.1.99 (58cf8f9c35 2026-10-13)")
	script("clippy-driver", "")
	if withRustup {
		script("rustup", "rustup 1.28.0")
	}
	return dir
}

// Rust pack (docs/ANALYZERS.md#rust-팩): cargo is the Rust baseline analyzer, so a node with only
// --cargo (no --go) advertises deep-review. Its Go and clippy members are
// visible unsupported stand-ins.
func TestCargoAloneAdvertisesDeepReview(t *testing.T) {
	dir := fakeRust(t, false)
	cfg := Config{Tools: map[string]string{"cargo": filepath.Join(dir, "cargo")}, ProbeDir: t.TempDir()}
	hs, missing := Handlers(context.Background(), cfg, &executor.Executor{})
	if len(hs) != 1 || !slices.Equal(reasons(missing), slices.Concat([]string{"go-vet:not_configured", "staticcheck:not_configured", "gofmt:not_configured"}, cppUnconfigured, pythonUnconfigured, jsUnconfigured, []string{"clippy:not_configured"}, anyMissing)) {
		t.Fatalf("handlers %d missing %v", len(hs), reasons(missing))
	}
	c, ok := hs[0].(*pack.Composite)
	if !ok {
		t.Fatalf("handler %T", hs[0])
	}
	var ids []string
	for _, d := range c.Descriptors() {
		ids = append(ids, d.ID)
	}
	if !slices.Contains(ids, "rust.cargocheck") || !slices.Contains(ids, "rust.clippy") {
		t.Fatalf("members %v", ids)
	}
	if entries, _ := os.ReadDir(cfg.ProbeDir); len(entries) != 0 {
		t.Fatalf("probe left files: %v", entries)
	}
	// clippy needs the toolchain's cargo.
	_, missing = Handlers(context.Background(), Config{Tools: map[string]string{"clippy": filepath.Join(dir, "cargo-clippy")}, ProbeDir: t.TempDir()}, &executor.Executor{})
	if !slices.Contains(reasons(missing), "clippy:toolchain_not_configured") {
		t.Fatalf("clippy without cargo: %v", reasons(missing))
	}
	// Both with cargo: no Rust member is missing.
	hs, missing = Handlers(context.Background(), Config{Tools: map[string]string{"cargo": filepath.Join(dir, "cargo"), "clippy": filepath.Join(dir, "cargo-clippy")}, ProbeDir: t.TempDir()}, &executor.Executor{})
	if len(hs) != 1 || slices.Contains(reasons(missing), "clippy:probe_failed") || slices.Contains(reasons(missing), "cargo-check:probe_failed") {
		t.Fatalf("both tools: %d %v", len(hs), reasons(missing))
	}
}

// A rustup proxy directory is refused: the node does not advertise the
// capability for it, and env.discover never executes it (the probe path is
// replaced by a non-absolute marker, so the observation is probe_failed with
// nothing started).
func TestRustProxyIsNeitherUsedNorProbed(t *testing.T) {
	dir := fakeRust(t, true)
	cfg := Config{Tools: map[string]string{"cargo": filepath.Join(dir, "cargo"), "clippy": filepath.Join(dir, "cargo-clippy")}, ProbeDir: t.TempDir()}
	hs, missing := Handlers(context.Background(), cfg, &executor.Executor{})
	if len(hs) != 0 || !slices.Contains(reasons(missing), "cargo-check:invalid_config") || !slices.Contains(reasons(missing), "clippy:invalid_config") {
		t.Fatalf("proxy accepted: %d %v", len(hs), reasons(missing))
	}
	for _, p := range Probes(cfg) {
		if (p.Tool == "cargo" || p.Tool == "clippy") && filepath.IsAbs(p.Executable) {
			t.Fatalf("a refused Rust tool would be executed by env.discover: %+v", p)
		}
	}
	// A valid toolchain is probed as configured.
	good := fakeRust(t, false)
	for _, p := range Probes(Config{Tools: map[string]string{"cargo": filepath.Join(good, "cargo"), "clippy": filepath.Join(good, "cargo-clippy")}}) {
		if (p.Tool == "cargo" || p.Tool == "clippy") && !filepath.IsAbs(p.Executable) {
			t.Fatalf("a valid Rust tool is not probed: %+v", p)
		}
	}
}

// With the real toolchain (CODEYE_TEST_CARGO, CODEYE_TEST_CLIPPY) the startup
// probe makes both analyzers members, and the probe leaves nothing in the
// probe directory (no detached child writes after the cleanup was confirmed,
// the failure mode docs/ANALYZERS.md#go-공통-환경과-빌드-문맥 measured for go).
func TestHandlersProbeRealRustToolchain(t *testing.T) {
	cargo, clippy := os.Getenv("CODEYE_TEST_CARGO"), os.Getenv("CODEYE_TEST_CLIPPY")
	if cargo == "" || clippy == "" {
		t.Skip("set CODEYE_TEST_CARGO and CODEYE_TEST_CLIPPY")
	}
	probe := t.TempDir()
	hs, missing := Handlers(context.Background(), Config{Tools: map[string]string{"cargo": cargo, "clippy": clippy}, ProbeDir: probe}, &executor.Executor{})
	if len(hs) != 1 || slices.Contains(reasons(missing), "cargo-check:probe_failed") || slices.Contains(reasons(missing), "clippy:probe_failed") {
		t.Fatalf("real toolchain: %d %v", len(hs), reasons(missing))
	}
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probe left files: %v", entries)
	}
}
