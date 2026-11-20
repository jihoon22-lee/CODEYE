package main

import (
	"context"
	"fmt"
	"io"

	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// configurePlatformGitHub replaces credentials using only this installation's
// explicit host-to-variable configuration. User token files are never read.
func configurePlatformGitHub(ctx context.Context, s *postgres.Store, cfg *instance.Config, layout instance.Layout, out io.Writer) ([]httpapi.SetupGitHub, error) {
	creds := github.PlatformCredentials{}
	connections := []httpapi.SetupGitHub{}
	for _, host := range cfg.GitHosts {
		if !github.HostAllowed(host, cfg.Offline, cfg.GitHosts) {
			continue
		}
		provider, err := s.ProviderForHost(ctx, host)
		if err != nil {
			return nil, err
		}
		st, cred := instance.SetupGitHub(ctx, cfg, host, provider.APIURL)
		if cred != nil {
			creds[host] = *cred
		}
		connections = append(connections, httpapi.SetupGitHub{Enabled: cfg.GitHub.On(), Host: host, Token: cred != nil, Account: st.Login, Reason: st.Reason})
		switch {
		case st.Login != "":
			fmt.Fprintf(out, "github: platform account %s on %s\n", st.Login, host)
		case st.Reason != "":
			fmt.Fprintf(out, "github: %s (%s)\n", host, st.Reason)
		}
	}
	if err := github.WritePlatformCredentials(github.PlatformCredentialFile(layout.Root), creds); err != nil {
		return nil, err
	}
	return connections, nil
}

func platformGitHub(cfg *instance.Config, layout instance.Layout) github.Platform {
	return github.Platform{CredentialFile: github.PlatformCredentialFile(layout.Root), CABundle: cfg.CABundle, Offline: cfg.Offline, Hosts: cfg.GitHosts, PublicAPI: github.APIBase()}
}
