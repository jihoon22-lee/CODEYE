package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// personalGitHubEnv is what token discovery reads besides the
// configuration: the environment of `codeye up` and the detected gh (tests
// replace it so they never find this machine's real login).
var personalGitHubEnv = func(tools toolfind.Result) personal.GitHubEnv {
	gh := ""
	if t, ok := tools.Tool("gh"); ok && t.Status == toolfind.Found {
		gh = t.Path
	}
	return personal.SystemGitHubEnv(gh)
}

// setupPersonalGitHub is the GitHub step of `codeye up`
// (personal.SetupGitHub). It prints whether a token is available and for
// which account, never the token. A missing token is no error: public
// repositories work without one.
func setupPersonalGitHub(ctx context.Context, cfg *personal.Config, layout personal.Layout, tools toolfind.Result, out io.Writer) personal.GitHubSetup {
	st, err := personal.SetupGitHub(ctx, cfg, layout, personalGitHubEnv(tools))
	switch {
	case err != nil:
		fmt.Fprintln(out, "github:", err)
	case !st.Enabled:
		fmt.Fprintln(out, "github: off (github.enabled false) - public repositories only, no pull-request comments")
	case st.Available && st.Login != "":
		fmt.Fprintf(out, "github: signed in as %s on %s (token from %s) - private repositories and pull requests you can read\n", st.Login, st.Host, st.Source)
	case st.Available:
		fmt.Fprintf(out, "github: a token from %s is available for %s (account not confirmed: %s)\n", st.Source, st.Host, st.Reason)
	case st.Reason == "token_refused":
		fmt.Fprintf(out, "github: GitHub refused the token from %s; it is not used - public repositories only (sign in again: gh auth login)\n", st.Source)
	case st.Reason == "token_invalid":
		fmt.Fprintln(out, "github: the token found is not a single-line token; it is not used - public repositories only")
	default:
		fmt.Fprintln(out, "github: no token (sign in with `gh auth login` or set GH_TOKEN, then run codeye up again) - public repositories only")
	}
	return st
}

// gitHubCredentialArgs are the agent flags of the GitHub token: the file the
// worker reads for each Git fetch of github.com. It is passed whenever
// discovery is on, whether or not a token was found, so a token appearing or
// going away later does not restart the agent.
func gitHubCredentialArgs(cfg *personal.Config, layout personal.Layout) []string {
	if !cfg.GitHub.On() || cfg.Agent.Git == "" || !slices.Contains(cfg.GitHosts, github.Host) {
		return nil
	}
	return []string{"--git-credential-file", layout.GitHubSecret()}
}

// setupGitHubOf is the GitHub part of the setup summary (no token).
func setupGitHubOf(st personal.GitHubSetup) *httpapi.SetupGitHub {
	g := &httpapi.SetupGitHub{Enabled: st.Enabled, Host: st.Host, Token: st.Available, Account: st.Login, Source: st.Source}
	if !st.Available || st.Login == "" {
		g.Reason = st.Reason
	}
	return g
}
