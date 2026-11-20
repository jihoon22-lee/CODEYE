package instance

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
)

// GitHub configures installation credentials. Each entry binds the name of
// an operator-supplied environment variable to one configured Git host.
type GitHub struct {
	Enabled  *bool             `json:"enabled,omitempty"`
	TokenEnv map[string]string `json:"token_env,omitempty"`
}

func (g GitHub) On() bool { return g.Enabled == nil || *g.Enabled }

var tokenEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (g GitHub) validate(hosts map[string]bool) error {
	for host, name := range g.TokenEnv {
		if !hosts[host] || !tokenEnvName.MatchString(name) {
			return domain.ErrInvalid
		}
	}
	return nil
}

// GitHubSetup describes one host's configured platform connection.
type GitHubSetup struct {
	Host   string
	Login  string
	Reason string
}

// SetupGitHub resolves an explicitly configured token for host. Public
// GitHub is skipped offline even if previously listed in the configuration.
func SetupGitHub(ctx context.Context, cfg *Config, host, apiURL string) (GitHubSetup, *github.Credential) {
	st := GitHubSetup{Host: host}
	if !cfg.GitHub.On() || !github.HostAllowed(host, cfg.Offline, cfg.GitHosts) {
		st.Reason = "disabled"
		return st, nil
	}
	name := cfg.GitHub.TokenEnv[host]
	if name == "" {
		st.Reason = "no_token"
		return st, nil
	}
	token := strings.TrimSpace(os.Getenv(name))
	if !github.ValidToken(token) {
		st.Reason = "token_invalid"
		if token == "" {
			st.Reason = "token_env_empty"
		}
		return st, nil
	}
	base, err := github.ProviderAPI(host, apiURL)
	if err != nil {
		st.Reason = "api_not_configured"
		return st, nil
	}
	if host == github.Host && apiURL == "" {
		base = github.APIBase()
	}
	client, err := github.NewWithCA(base, token, cfg.CABundle)
	if err != nil {
		st.Reason = "github_unavailable"
		return st, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	user, err := client.User(probeCtx)
	switch kind := github.KindOf(err); {
	case err == nil:
		st.Login = user.Login
	case kind == github.KindUnauthorized:
		st.Reason = "token_refused"
		return st, nil
	default:
		st.Reason = kind
	}
	return st, &github.Credential{SchemaVersion: github.CredentialSchema, Host: host, Token: token, Source: "platform", Login: st.Login}
}
