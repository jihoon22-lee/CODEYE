package personal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/github/githubtest"
)

func gitHubLayout(t *testing.T) (*Config, Layout) {
	t.Helper()
	state := t.TempDir()
	if err := os.Chmod(state, 0o700); err != nil {
		t.Fatal(err)
	}
	return &Config{StateDir: state}, Layout{Root: state}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// Discovery order: GH_TOKEN, GITHUB_TOKEN, then gh; the stored file holds
// the token and its account, is private, and a token GitHub refuses is not
// stored. Turning discovery off or finding nothing removes the file.
func TestSetupGitHubDiscoversStoresAndRemoves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private file modes")
	}
	fake := githubtest.New("tok-from-env-1", "octo")
	defer fake.Close()
	cfg, layout := gitHubLayout(t)
	ghCalls := 0
	env := GitHubEnv{APIURL: fake.URL, GH: "/usr/bin/gh", RunGH: func(_ context.Context, gh, host string) (string, error) {
		ghCalls++
		if host != "github.com" {
			t.Fatalf("gh asked for %s", host)
		}
		return "tok-from-gh-22\n", nil
	}}
	env.Getenv = envOf(map[string]string{"GH_TOKEN": "tok-from-env-1", "GITHUB_TOKEN": "other-token-1"})
	st, err := SetupGitHub(context.Background(), cfg, layout, env)
	if err != nil || !st.Available || st.Source != TokenFromGHToken || st.Login != "octo" || ghCalls != 0 {
		t.Fatalf("GH_TOKEN: %+v %v (gh calls %d)", st, err, ghCalls)
	}
	c, err := github.ReadCredential(layout.GitHubSecret())
	if err != nil || c == nil || c.Token != "tok-from-env-1" || c.Login != "octo" || c.Host != "github.com" {
		t.Fatalf("stored %+v %v", c, err)
	}
	if fi, _ := os.Stat(layout.GitHubSecret()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}

	// gh's token, refused by GitHub: nothing is stored, the old file goes.
	env.Getenv = envOf(nil)
	st, err = SetupGitHub(context.Background(), cfg, layout, env)
	if err != nil || st.Available || st.Reason != "token_refused" || st.Source != TokenFromGH || ghCalls != 1 {
		t.Fatalf("refused: %+v %v", st, err)
	}
	if _, err := os.Stat(layout.GitHubSecret()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused token's file stays")
	}

	// gh's token accepted.
	fake.Token = "tok-from-gh-22"
	if st, err = SetupGitHub(context.Background(), cfg, layout, env); err != nil || !st.Available || st.Login != "octo" {
		t.Fatalf("gh: %+v %v", st, err)
	}
	// gh signed out: no token, the file goes.
	env.RunGH = func(context.Context, string, string) (string, error) { return "", errors.New("not signed in") }
	if st, err = SetupGitHub(context.Background(), cfg, layout, env); err != nil || st.Available || st.Reason != "no_token" {
		t.Fatalf("signed out: %+v %v", st, err)
	}
	if _, err := os.Stat(layout.GitHubSecret()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a signed-out token's file stays")
	}

	// GitHub unreachable: the token is kept, the account unknown.
	env.Getenv = envOf(map[string]string{"GITHUB_TOKEN": "tok-offline-333"})
	env.APIURL = "http://127.0.0.1:1"
	if st, err = SetupGitHub(context.Background(), cfg, layout, env); err != nil || !st.Available || st.Login != "" || st.Reason != github.KindUnavailable || st.Source != TokenFromGitHubToken {
		t.Fatalf("offline: %+v %v", st, err)
	}

	// github.enabled false: off, file removed, nothing asked.
	off := false
	cfg.GitHub.Enabled = &off
	env.RunGH = func(context.Context, string, string) (string, error) { t.Fatal("gh asked while off"); return "", nil }
	if st, err = SetupGitHub(context.Background(), cfg, layout, env); err != nil || st.Enabled || st.Available {
		t.Fatalf("off: %+v %v", st, err)
	}
	if _, err := os.Stat(layout.GitHubSecret()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the file stays when discovery is off")
	}
}

func TestGitHubConfigField(t *testing.T) {
	state := filepath.Join(t.TempDir(), "s")
	raw := `{"schema_version":"0.1","state_dir":"` + filepath.ToSlash(state) + `","github":{"enabled":false}}`
	if runtime.GOOS == "windows" {
		t.Skip("absolute Unix path")
	}
	c, err := Parse([]byte(raw), nil)
	if err != nil || c.GitHub.On() {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Parse([]byte(strings.Replace(raw, `"enabled":false`, `"token":"x"`, 1)), nil); err == nil {
		t.Fatal("a token in the configuration is accepted")
	}
}
