package personal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/github"
)

// GitHub configures personal mode's GitHub connection (docs/GITHUB.md).
// Absent or enabled means `codeye up` looks for a token of the signed-in
// user; false turns that off (public repositories only, no pull-request
// comments).
type GitHub struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// On reports whether token discovery is on.
func (g GitHub) On() bool { return g.Enabled == nil || *g.Enabled }

// GitHubSecret is the file `codeye up` writes the discovered token to
// (0600 in <state>/secrets). The controller reads it for each GitHub API
// call and the agent's worker for each Git fetch of Host; nothing else
// sees the token.
func (l Layout) GitHubSecret() string { return filepath.Join(l.SecretsDir(), "github.json") }

// Token sources (github.Credential.Source).
const (
	TokenFromGHToken     = "GH_TOKEN"
	TokenFromGitHubToken = "GITHUB_TOKEN"
	TokenFromGH          = "gh"
)

// GitHubEnv is what token discovery reads besides the configuration; tests
// replace it so they never see the machine's real login.
type GitHubEnv struct {
	Getenv func(string) string
	// GH is the GitHub CLI tool detection found ("" when none).
	GH string
	// RunGH runs `gh auth token --hostname HOST` and returns its output.
	RunGH func(ctx context.Context, gh, host string) (string, error)
	// APIURL is the REST API asked for the token's account.
	APIURL string
}

// SystemGitHubEnv asks the real environment and gh (found at gh).
func SystemGitHubEnv(gh string) GitHubEnv {
	return GitHubEnv{Getenv: os.Getenv, GH: gh, RunGH: runGHAuthToken, APIURL: github.APIBase()}
}

// runGHAuthToken runs gh with the user's environment (it needs HOME and its
// configuration directory to find the login) minus the token variables
// discovery already read, for at most 15 seconds.
func runGHAuthToken(ctx context.Context, gh, host string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "token", "--hostname", host)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k != "GH_TOKEN" && k != "GITHUB_TOKEN" && k != "GH_ENTERPRISE_TOKEN" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	var out bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &out, left: 4096}
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", errors.New("gh is not signed in for " + host)
	}
	return out.String(), nil
}

type limitedBuffer struct {
	buf  *bytes.Buffer
	left int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.left {
		p = p[:w.left]
	}
	w.left -= len(p)
	w.buf.Write(p)
	return n, nil
}

// GitHubSetup is what SetupGitHub found. It never holds the token.
type GitHubSetup struct {
	Enabled bool
	// Available: a token is stored for Host.
	Available bool
	Host      string
	Source    string
	Login     string
	// Reason says why no token is stored (enabled but none found, refused)
	// or why the account is unknown.
	Reason string
}

// SetupGitHub is the GitHub step of `codeye up`: with discovery on it
// takes GH_TOKEN, else GITHUB_TOKEN, else `gh auth token` for github.com,
// asks GitHub for the token's account and stores the token in
// GitHubSecret. A token GitHub refuses (401) is not stored. With discovery
// off, or no token found, the file is removed so a token the user signed out
// of is not used any more.
func SetupGitHub(ctx context.Context, cfg *Config, layout Layout, env GitHubEnv) (GitHubSetup, error) {
	st := GitHubSetup{Enabled: cfg.GitHub.On(), Host: github.Host}
	remove := func() error {
		if err := os.Remove(layout.GitHubSecret()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot remove " + layout.GitHubSecret())
		}
		return nil
	}
	if !st.Enabled {
		return st, remove()
	}
	get := env.Getenv
	if get == nil {
		get = func(string) string { return "" }
	}
	token, source := "", ""
	switch {
	case strings.TrimSpace(get("GH_TOKEN")) != "":
		token, source = strings.TrimSpace(get("GH_TOKEN")), TokenFromGHToken
	case strings.TrimSpace(get("GITHUB_TOKEN")) != "":
		token, source = strings.TrimSpace(get("GITHUB_TOKEN")), TokenFromGitHubToken
	case env.GH != "" && env.RunGH != nil:
		if out, err := env.RunGH(ctx, env.GH, github.Host); err == nil {
			token, source = strings.TrimSpace(out), TokenFromGH
		}
	}
	if token == "" {
		st.Reason = "no_token"
		return st, remove()
	}
	if !github.ValidToken(token) {
		st.Reason = "token_invalid"
		return st, remove()
	}
	st.Source = source
	idCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	user, err := github.New(env.APIURL, token).User(idCtx)
	switch kind := github.KindOf(err); {
	case err == nil:
		st.Login = user.Login
	case kind == github.KindUnauthorized:
		st.Reason = "token_refused"
		return st, remove()
	default:
		// Forbidden (a token without the user scope), rate limited or no
		// network: the token is kept and the account stays unknown.
		st.Reason = kind
	}
	data, err := json.Marshal(github.Credential{SchemaVersion: github.CredentialSchema, Host: github.Host, Token: token, Source: source, Login: st.Login})
	if err != nil {
		return st, err
	}
	data = append(data, '\n')
	if old, err := os.ReadFile(layout.GitHubSecret()); err != nil || !bytes.Equal(old, data) {
		if err := os.MkdirAll(layout.SecretsDir(), 0o700); err != nil {
			return st, errors.New("cannot create the secrets directory")
		}
		if err := checkPrivateDir(layout.SecretsDir()); err != nil {
			return st, err
		}
		if err := writePrivate(layout.GitHubSecret(), data); err != nil {
			return st, errors.New("cannot write the GitHub credential")
		}
	}
	st.Available = true
	return st, nil
}
