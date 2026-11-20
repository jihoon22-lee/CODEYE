package instance

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

// Discovery order: GH_TOKEN, GITHUB_TOKEN, then gh. The credential returned
// holds the token and its account; a token GitHub refuses, a signed-out gh
// and discovery turned off return none. SetupGitHub writes no file: the
// caller stores the credential as the administrator's own token.
func TestSetupGitHubDiscoversAndRefuses(t *testing.T) {
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
	st, c := SetupGitHub(context.Background(), cfg, env)
	if !st.Available || st.Source != TokenFromGHToken || st.Login != "octo" || ghCalls != 0 {
		t.Fatalf("GH_TOKEN: %+v (gh calls %d)", st, ghCalls)
	}
	if c == nil || c.Token != "tok-from-env-1" || c.Login != "octo" || c.Host != "github.com" || c.Source != TokenFromGHToken || c.SchemaVersion != github.CredentialSchema {
		t.Fatalf("credential %+v", c)
	}
	if entries, _ := os.ReadDir(layout.Root); len(entries) != 0 {
		t.Fatalf("SetupGitHub wrote %v", entries)
	}

	// gh's token, refused by GitHub: no credential.
	env.Getenv = envOf(nil)
	if st, c = SetupGitHub(context.Background(), cfg, env); st.Available || st.Reason != "token_refused" || st.Source != TokenFromGH || ghCalls != 1 || c != nil {
		t.Fatalf("refused: %+v %+v", st, c)
	}

	// gh's token accepted.
	fake.Token = "tok-from-gh-22"
	if st, c = SetupGitHub(context.Background(), cfg, env); !st.Available || st.Login != "octo" || c == nil || c.Token != "tok-from-gh-22" {
		t.Fatalf("gh: %+v %+v", st, c)
	}
	// gh signed out: no token.
	env.RunGH = func(context.Context, string, string) (string, error) { return "", errors.New("not signed in") }
	if st, c = SetupGitHub(context.Background(), cfg, env); st.Available || st.Reason != "no_token" || c != nil {
		t.Fatalf("signed out: %+v %+v", st, c)
	}

	// GitHub unreachable: the token is kept, the account unknown.
	env.Getenv = envOf(map[string]string{"GITHUB_TOKEN": "tok-offline-333"})
	env.APIURL = "http://127.0.0.1:1"
	if st, c = SetupGitHub(context.Background(), cfg, env); !st.Available || st.Login != "" || st.Reason != github.KindUnavailable || st.Source != TokenFromGitHubToken || c == nil || c.Login != "" {
		t.Fatalf("unreachable: %+v %+v", st, c)
	}

	// github.enabled false: off, nothing asked, no credential.
	off := false
	cfg.GitHub.Enabled = &off
	env.RunGH = func(context.Context, string, string) (string, error) { t.Fatal("gh asked while off"); return "", nil }
	if st, c = SetupGitHub(context.Background(), cfg, env); st.Enabled || st.Available || c != nil {
		t.Fatalf("off: %+v %+v", st, c)
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
