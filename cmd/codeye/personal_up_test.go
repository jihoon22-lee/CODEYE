package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
)

func TestUpArguments(t *testing.T) {
	for _, c := range []struct {
		args   []string
		target string
		rest   []string
	}{
		{[]string{"proj"}, "proj", nil},
		{[]string{"--no-open", "proj", "--config", "c.json"}, "proj", []string{"--no-open", "--config", "c.json"}},
		{[]string{"--config", "c.json", "--postgres-bin-dir", "/pg", "proj"}, "proj", []string{"--config", "c.json", "--postgres-bin-dir", "/pg"}},
		{[]string{"--config=c.json"}, "", []string{"--config=c.json"}},
		{[]string{"--", "-odd"}, "-odd", nil},
		{[]string{"a", "b"}, "a", []string{"b"}},
	} {
		target, rest := splitTarget(c.args)
		if target != c.target || !slices.Equal(rest, c.rest) {
			t.Errorf("%v: %q %v", c.args, target, rest)
		}
	}
	for s, want := range map[string]bool{"https://github.com/o/r": true, "git@github.com:o/r.git": true, "./repo": false, "/home/u/src": false} {
		if isGitURL(s) != want {
			t.Errorf("isGitURL(%q)", s)
		}
	}
}

// A Git URL is refused before anything is written or started, with the
// way that works today.
func TestUpRefusesGitURL(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := personalUp(context.Background(), []string{"--config", filepath.Join(dir, "p.json"), "https://github.com/o/r"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "git clone https://github.com/o/r DIR && codeye up DIR") {
		t.Fatal(code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "p.json")); err == nil {
		t.Fatal("a configuration was written")
	}
}

// PATH becomes a new source root (unique label, config file updated), or
// reuses the configured root it is in; the data directories are refused.
func TestUpResolvesTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	base := t.TempDir()
	state := filepath.Join(base, "state")
	work := filepath.Join(base, "work")
	for _, d := range []string{filepath.Join(work, "svc", "api"), filepath.Join(base, "other", "work"), state} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(base, "personal.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"0.1","state_dir":`+jsonString(state)+`,"source_roots":[{"label":"work","path":`+jsonString(work)+`}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := personal.Load(path, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	tg, next, err := resolveTarget(path, cfg, filepath.Join(work, "svc", "api"), &out)
	if err != nil || tg.label != "work" || tg.relative != "svc/api" || next != cfg {
		t.Fatalf("inside a root: %+v %v", tg, err)
	}
	tg, next, err = resolveTarget(path, cfg, filepath.Join(base, "other", "work"), &out)
	if err != nil || tg.label != "work-2" || tg.relative != "." || len(next.SourceRoots) != 2 {
		t.Fatalf("new root: %+v %v", tg, err)
	}
	if reloaded, err := personal.Load(path, knownTools()); err != nil || len(reloaded.SourceRoots) != 2 || reloaded.SourceRoots[1].Path != filepath.Join(base, "other", "work") {
		t.Fatalf("config not updated: %v", err)
	}
	for _, bad := range []string{base, filepath.Join(state, "x"), filepath.Join(base, "missing")} {
		_ = os.MkdirAll(filepath.Join(state, "x"), 0o700)
		if _, _, err := resolveTarget(path, next, bad, &out); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func jsonString(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

func TestBrowserCommands(t *testing.T) {
	t.Setenv("BROWSER", "")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	got := browserCommands()
	if len(got) != 3 || got[0][0] != "wslview" || got[1][0] != "explorer.exe" || got[2][0] != "xdg-open" {
		t.Fatal(got)
	}
	t.Setenv("BROWSER", "firefox")
	if got := browserCommands(); got[0][0] != "firefox" {
		t.Fatal(got)
	}
}
