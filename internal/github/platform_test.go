package github

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlatformCredentialsReplaceStaleHosts(t *testing.T) {
	path := PlatformCredentialFile(t.TempDir())
	const internal = "ghe.corp.example:8443"
	creds := PlatformCredentials{
		Host:     {SchemaVersion: CredentialSchema, Host: Host, Token: "tok-fixture-public"},
		internal: {SchemaVersion: CredentialSchema, Host: internal, Token: "tok-fixture-enterprise"},
	}
	if err := WritePlatformCredentials(path, creds); err != nil {
		t.Fatal(err)
	}
	if actual, err := ReadPlatformCredentials(path); err != nil || len(actual) != 2 {
		t.Fatalf("platform credential read failed: %v", err)
	}
	if runtime.GOOS != "windows" {
		for file, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
			if info, err := os.Stat(file); err != nil || info.Mode().Perm() != mode {
				t.Fatalf("platform credential permissions: %v", err)
			}
		}
	}
	delete(creds, Host)
	if err := WritePlatformCredentials(path, creds); err != nil {
		t.Fatal(err)
	}
	if actual, err := ReadPlatformCredentials(path); err != nil || len(actual) != 1 || actual[internal].Host != internal {
		t.Fatalf("platform credential replacement failed: %v", err)
	}
	p := Platform{CredentialFile: path, Hosts: []string{Host, internal}, Offline: true}
	if public, _ := p.Client(Host, ""); public != nil {
		t.Fatal("public API is available offline")
	}
	if enterprise, _ := p.Client(internal, ""); enterprise == nil || enterprise.BaseURL != "https://"+internal+"/api/v3" || enterprise.Token != creds[internal].Token {
		t.Fatal("enterprise platform credential is unavailable offline")
	}
	if wrong, _ := p.Client(internal, "https://another.corp.example/api/v3"); wrong != nil {
		t.Fatal("enterprise credential is available to another origin")
	}
}
