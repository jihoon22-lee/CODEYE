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
