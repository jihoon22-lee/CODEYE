package main

import (
	"context"
	"fmt"
	"io"

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
	case st.Reason == personal.GitHubOffline:
		fmt.Fprintln(out, "github: off (offline mode) - no token is looked up or used, github.com is not a Git host, no pull requests")
	case !st.Enabled:
		fmt.Fprintln(out, "github: your token is not looked up (github.enabled false) - your projects reach public repositories only unless you connect a token under account > GitHub connection")
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

// gitHubCredentialArgs are the agent flags of the users' GitHub tokens: the
// directory of per-user token files; the worker reads the project owner's
// file for each Git fetch of github.com. It is passed whenever the service
// is not offline, whether or not anyone stored a token, so tokens added or
// removed in the web do not restart the agent.
func gitHubCredentialArgs(cfg *personal.Config, layout personal.Layout) []string {
	if cfg.Offline || cfg.Agent.Git == "" {
		return nil
	}
	return []string{"--git-credential-dir", string(github.UserTokensDir(layout.Root))}
}

// setupGitHubOf is the GitHub part of the setup summary (no token).
func setupGitHubOf(st personal.GitHubSetup) *httpapi.SetupGitHub {
	g := &httpapi.SetupGitHub{Enabled: st.Enabled, Host: st.Host, Token: st.Available, Account: st.Login, Source: st.Source}
	if !st.Available || st.Login == "" {
		g.Reason = st.Reason
	}
	return g
}
