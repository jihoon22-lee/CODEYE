package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Type checker choice (docs/ANALYZERS.md#타입-검사기-선택): mypy and pyright are alternative type checkers. One of
// them runs when its path is given; both run only with the explicit
// --python-both-type-checkers choice, which itself needs both paths.
func TestPythonTypeCheckerChoice(t *testing.T) {
	base := func() []string {
		return []string{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir()}
	}
	mypy, pyright := filepath.Join(t.TempDir(), "mypy"), filepath.Join(t.TempDir(), "index.js")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"both paths without the choice", []string{"--mypy", mypy, "--pyright", pyright}, "add --python-both-type-checkers"},
		{"choice without pyright", []string{"--mypy", mypy, "--python-both-type-checkers"}, "needs both --mypy and --pyright"},
		{"choice without any path", []string{"--python-both-type-checkers"}, "needs both --mypy and --pyright"},
		// Past the choice the agent stops at the next check (no enrollment
		// token), so the flags themselves were accepted.
		{"mypy only", []string{"--mypy", mypy}, "enroll-token-file"},
		{"pyright only", []string{"--pyright", pyright}, "enroll-token-file"},
		{"both with the choice", []string{"--mypy", mypy, "--pyright", pyright, "--python-both-type-checkers"}, "enroll-token-file"},
	}
	for _, c := range cases {
		var stderr bytes.Buffer
		code, ok := runNode(context.Background(), append(base(), c.args...), &bytes.Buffer{}, &stderr)
		if !ok || code != 2 || !strings.Contains(stderr.String(), c.want) {
			t.Errorf("%s: code %d ok %v stderr %q", c.name, code, ok, stderr.String())
		}
	}
}
