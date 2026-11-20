package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the per-user state and configuration directories at a
// temporary directory, so a command under test that falls back to its
// defaults never creates files in the home directory of whoever runs the
// tests. Tests that need specific directories still set their own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codeye-cmd-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, name := range []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME"} {
		os.Setenv(name, dir)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
