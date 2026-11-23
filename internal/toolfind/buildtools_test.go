package toolfind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookToolchainPath(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	for _, name := range []string{"cmake", "ninja", "cc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "make"), []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	tp := LookToolchainPath("relative/dir" + string(os.PathListSeparator) + dir)
	if !tp.CompileDBGenerationReady() {
		t.Fatalf("not ready: %+v", tp)
	}
	if p, _ := tp.Program("make"); p.Status != NotFound {
		t.Fatalf("a non-executable make counted: %+v", p)
	}
	if p, _ := tp.Program("ninja"); p.Status != Found || p.Path != filepath.Join(dir, "ninja") || len(p.UsedFor) == 0 {
		t.Fatalf("ninja %+v", p)
	}
	if err := os.Remove(filepath.Join(dir, "cmake")); err != nil {
		t.Fatal(err)
	}
	missing := LookToolchainPath(dir)
	if missing.CompileDBGenerationReady() {
		t.Fatal("ready without cmake")
	}
	var b strings.Builder
	if err := WriteTable(&b, Result{ToolchainPath: missing}, ""); err != nil || !strings.Contains(b.String(), "cmake, make") || !strings.Contains(b.String(), "compile DB generation needs") {
		t.Fatalf("table %q %v", b.String(), err)
	}
	if def := LookToolchainPath(""); def.Path != DefaultToolchainPath {
		t.Fatalf("default %+v", def)
	}
}
