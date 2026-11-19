//go:build linux

package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A configured CA bundle reaches every git command (GIT_SSL_CAINFO covers a
// GnuTLS build, SSL_CERT_FILE an OpenSSL one); without one neither is set.
func TestGitCommandsTrustTheConfiguredCABundle(t *testing.T) {
	for _, bundle := range []string{"/etc/codeye/ca.pem", ""} {
		_, data := fixture(t)
		envLog := filepath.Join(t.TempDir(), "env")
		exe := fakeGit(t, "echo \"$GIT_SSL_CAINFO|$SSL_CERT_FILE\" >> "+envLog+"\nexit 1\n")
		_, _ = AcquireGit(context.Background(), "https://example.invalid/repo", "main", data, DefaultLimits(), GitOptions{Executable: exe, AllowedHost: "example.invalid", Timeout: 5 * time.Second, CABundle: bundle})
		raw, err := os.ReadFile(envLog)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != bundle+"|"+bundle {
				t.Fatalf("bundle %q: the command saw %q", bundle, line)
			}
		}
		cleanupAll(t, data)
	}
}

// The CA bundle and the GitHub token both reach git, through the process
// environment only: the token is in no argument and the bundle variables do
// not displace the scoped credential configuration.
func TestGitGetsTheCABundleAndTheTokenTogether(t *testing.T) {
	_, data := fixture(t)
	envLog := filepath.Join(t.TempDir(), "env")
	exe := fakeGit(t, "echo \"$GIT_SSL_CAINFO|$SSL_CERT_FILE|$GIT_CONFIG_COUNT|$GIT_CONFIG_KEY_0|${GIT_CONFIG_VALUE_0:+set}|$*\" >> "+envLog+"\nexit 1\n")
	const token = "ghp_testtokenvalue0123456789"
	_, _ = AcquireGit(context.Background(), "https://github.com/o/r", "main", data, DefaultLimits(),
		GitOptions{Executable: exe, AllowedHost: "github.com", Timeout: 5 * time.Second, CABundle: "/etc/codeye/ca.pem", Token: token})
	raw, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.SplitN(line, "|", 6)
		if len(f) != 6 || f[0] != "/etc/codeye/ca.pem" || f[1] != "/etc/codeye/ca.pem" || f[2] != "1" || f[3] != "http.https://github.com/.extraHeader" || f[4] != "set" || strings.Contains(f[5], token) {
			t.Fatalf("the command saw %q", line)
		}
	}
	cleanupAll(t, data)
}
