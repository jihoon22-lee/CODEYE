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
	for s, want := range map[string]bool{"https://github.com/o/r": true, "git@github.com:o/r.git": true, "github.com/o/r": true, "./repo": false, "/home/u/src": false, "src/a/b": false} {
		if isGitURL(s) != want {
			t.Errorf("isGitURL(%q)", s)
		}
	}
}

// A repository argument is validated before anything is written or started:
// HTTPS only, an approved host, a /tree/BRANCH address names the branch.
func TestUpGitTarget(t *testing.T) {
	dir := t.TempDir()
	cfg, err := personal.Parse([]byte(`{"schema_version":"0.1","state_dir":`+jsonString(filepath.Join(dir, "s"))+`}`), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	tg, err := gitTarget(cfg, "https://github.com/o/r/tree/release/1.2")
	if err != nil || tg.repoURL != "https://github.com/o/r" || tg.branch != "release/1.2" || tg.host != "github.com" || !tg.git() {
		t.Fatalf("%+v %v", tg, err)
	}
	if tg, err := gitTarget(cfg, "github.com/o/r"); err != nil || tg.repoURL != "https://github.com/o/r" || tg.branch != "" {
		t.Fatalf("%+v %v", tg, err)
	}
	for in, want := range map[string]string{
		"git@github.com:o/r.git":         "only HTTPS",
		"https://gitlab.example.com/o/r": "not in git_hosts",
		"https://github.com/o":           "not a repository URL",
	} {
		if _, err := gitTarget(cfg, in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
	var out, errb bytes.Buffer
	code := personalUp(context.Background(), []string{"--config", filepath.Join(dir, "p.json"), "git@github.com:o/r.git"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "only HTTPS") {
		t.Fatal(code, errb.String())
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
	if err != nil || tg.rootPath != work || tg.relative != "svc/api" || next != cfg {
		t.Fatalf("inside a root: %+v %v", tg, err)
	}
	tg, next, err = resolveTarget(path, cfg, filepath.Join(base, "other", "work"), &out)
	if err != nil || tg.rootPath != filepath.Join(base, "other", "work") || tg.relative != "." || len(next.SourceRoots) != 2 || next.SourceRoots[1].Label != "work-2" {
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

// $HOME is a readable folder unless a configured root holds it; the root is
// never written to the file, and a folder under it maps onto it.
func TestHomeRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	state := filepath.Join(home, ".local", "state", "codeye")
	if err := os.MkdirAll(filepath.Join(home, "src", "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = os.UserHomeDir })
	path := filepath.Join(base, "personal.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"0.1","state_dir":`+jsonString(state)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := personal.Load(path, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	completeConfig(cfg)
	completeConfig(cfg)
	if len(cfg.SourceRoots) != 1 || cfg.SourceRoots[0].Path != home || cfg.SourceRoots[0].Label != "home" || !cfg.SourceRoots[0].Implicit {
		t.Fatalf("%+v", cfg.SourceRoots)
	}
	var out bytes.Buffer
	tg, next, err := resolveTarget(path, cfg, filepath.Join(home, "src", "app"), &out)
	if err != nil || tg.rootPath != home || tg.relative != "src/app" || next != cfg || out.Len() != 0 {
		t.Fatalf("%+v %v %s", tg, err, out.String())
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "source_roots") {
		t.Fatalf("the home root was written: %s", raw)
	}
	// The home directory itself holds the state directory: refused.
	if _, _, err := resolveTarget(path, cfg, home, &out); err == nil {
		t.Fatal("home accepted")
	}
	// A configured root that holds $HOME replaces it.
	cfg2, err := personal.Parse([]byte(`{"schema_version":"0.1","state_dir":`+jsonString(state)+`,"source_roots":[{"label":"all","path":`+jsonString(base)+`}]}`), knownTools())
	if err != nil {
		t.Fatal(err)
	}
	completeConfig(cfg2)
	if len(cfg2.SourceRoots) != 1 {
		t.Fatalf("%+v", cfg2.SourceRoots)
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
