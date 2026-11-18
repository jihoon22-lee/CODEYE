package gitleaks

import (
	"os"
	"strings"
	"testing"
)

// Fixture sources: every secret is generated at test time
// (anypacktest.Secret) and written only into the test's temp snapshot; the
// replay tests use the same files with anypacktest.Placeholder (same
// length, no secret shape) so the captured line and column ranges in
// testdata/ still resolve. Nothing realistic is committed.

// fixtureFiles places secret where gitleaks must find it, where CODEYE's
// rule pack overrides or disables a repository's way to hide it, and where
// gitleaks' default allowlist and the repository .gitleaksignore hide it.
func fixtureFiles(secret string) map[string][]byte {
	return map[string][]byte{
		"main.go":              []byte("package main\n\nfunc main() {}\n"),
		"src/app.py":           []byte("import os\n\nAWS_KEY = \"" + secret + "\"\n"),
		"config/settings.yaml": []byte("service: demo\naws_access_key_id: " + secret + "\n"),
		// inline allow is disabled: still reported.
		"src/allow.py": []byte("KEY = \"" + secret + "\"  # gitleaks:allow\n"),
		// the repository .gitleaksignore names this finding: honored.
		"src/ignored.py":     []byte("KEY = \"" + secret + "\"\n"),
		".gitleaksignore":    []byte("src/ignored.py:aws-access-token:1\n"),
		".gitleaks.toml":     []byte("[allowlist]\npaths = [\"src/app.py\"]\n"),
		"go.sum":             []byte("example.test/x v1.0.0 h1:" + secret + "=\n"),
		"package-lock.json":  []byte("{\"token\": \"" + secret + "\"}\n"),
		"vendor/modules.txt": []byte("# example.test/x v1.0.0 " + secret + "\n"),
		"assets/blob.dat":    append([]byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}, []byte("k="+secret+"\n")...),
		"notes/empty.txt":    {},
		"README.md":          []byte("# fixture\n\nNo credentials here.\n"),
	}
}

// cleanFiles hold no secret.
func cleanFiles() map[string][]byte {
	return map[string][]byte{"main.go": []byte("package main\n\nfunc main() {}\n"), "README.md": []byte("# clean\n"), "config/app.yaml": []byte("name: demo\n")}
}

// testGitleaks returns the operator-installed gitleaks named by
// CODEYE_TEST_GITLEAKS, or skips.
func testGitleaks(t *testing.T) string {
	t.Helper()
	p := os.Getenv("CODEYE_TEST_" + strings.ToUpper(Name))
	if p == "" {
		t.Skip("set CODEYE_TEST_GITLEAKS to the gitleaks executable (8.30.x) to run real-tool tests")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("gitleaks not found: %v", err)
	}
	return p
}
