package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

// The token `codeye up` found is stored once, as the administrator's own
// token file: written when found, rewritten when it changes, removed when
// it is gone, and never over a token the administrator pasted in the web.
// No other token file is left in the secrets directory.
func TestSyncAdminGitHubTokenStoresOneFile(t *testing.T) {
	layout := instance.Layout{Root: t.TempDir()}
	const admin = "0b0e8f8a-6a43-4c1e-9a52-1c7b1f0f3a11"
	dir := github.UserTokensDir(layout.Root)
	found := &github.Credential{SchemaVersion: github.CredentialSchema, Host: github.Host, Token: "tok-from-env-1", Source: instance.TokenFromGHToken, Login: "octo"}
	if err := syncAdminGitHubToken(layout, admin, found); err != nil {
		t.Fatal(err)
	}
	if c, err := dir.Read(admin); err != nil || c == nil || *c != *found {
		t.Fatalf("stored %+v %v", c, err)
	}
	entries, _ := os.ReadDir(filepath.Join(layout.Root, "secrets"))
	if len(entries) != 1 || entries[0].Name() != "github-users" {
		t.Fatalf("secrets holds %v", entries)
	}

	changed := *found
	changed.Token, changed.Source = "tok-from-gh-22", instance.TokenFromGH
	if err := syncAdminGitHubToken(layout, admin, &changed); err != nil {
		t.Fatal(err)
	}
	if c, _ := dir.Read(admin); c == nil || c.Token != "tok-from-gh-22" {
		t.Fatalf("not replaced: %+v", c)
	}

	if err := syncAdminGitHubToken(layout, admin, nil); err != nil {
		t.Fatal(err)
	}
	if c, _ := dir.Read(admin); c != nil {
		t.Fatalf("a token that is gone stays: %+v", c)
	}

	web := github.Credential{Host: github.Host, Token: "ghp_pasted-token", Source: github.TokenFromWeb}
	if err := dir.Write(admin, web); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*github.Credential{found, nil} {
		if err := syncAdminGitHubToken(layout, admin, f); err != nil {
			t.Fatal(err)
		}
		if c, _ := dir.Read(admin); c == nil || c.Token != "ghp_pasted-token" {
			t.Fatalf("the web token was replaced: %+v", c)
		}
	}
}
