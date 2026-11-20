package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/hostaddr"
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

// The controller of a service other machines reach gets only the
// configured public origins: on 0.0.0.0 it answers on this machine's own
// addresses by itself, so no single computed address is pinned.
func TestControllerListenArgs(t *testing.T) {
	wild := &instance.Config{Listen: "0.0.0.0:7331", PlainHTTP: true}
	if got := controllerListenArgs(wild); !slices.Equal(got, []string{"--plain-http", "--local-listen", "127.0.0.1:7332"}) {
		t.Fatalf("%v", got)
	}
	wild.PublicOrigins = []string{"http://codeye.lan:7331", "http://codeye.vpn:7331"}
	if got := controllerListenArgs(wild); !slices.Equal(got, []string{"--plain-http", "--public-origin", "http://codeye.lan:7331", "--public-origin", "http://codeye.vpn:7331", "--local-listen", "127.0.0.1:7332"}) {
		t.Fatalf("%v", got)
	}
	if got := controllerListenArgs(&instance.Config{Listen: "127.0.0.1:7331"}); len(got) != 0 {
		t.Fatalf("loopback %v", got)
	}
}

func TestPrintAddressesGroups(t *testing.T) {
	var b strings.Builder
	printAddresses(&b, []hostaddr.Address{{Origin: "http://127.0.0.1:7331", Kind: hostaddr.KindThisMachine}, {Origin: "http://main:7331", Kind: hostaddr.KindName},
		{Origin: "http://172.24.157.79:7331", Kind: hostaddr.KindLAN}, {Origin: "http://100.108.34.23:7331", Kind: hostaddr.KindVPN}, {Origin: "http://localhost:7331", Kind: hostaddr.KindThisMachine}})
	want := "The service answers on:\n  LAN           http://172.24.157.79:7331\n  VPN           http://100.108.34.23:7331\n  name          http://main:7331\n  this machine  http://127.0.0.1:7331  http://localhost:7331\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
}
