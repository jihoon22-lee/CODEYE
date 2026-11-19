//go:build codeye_fixture

package github

import "os"

// TestAPIEnv names the fake GitHub API of test builds
// (internal/github/githubtest), so `codeye up` with a pull-request address
// runs end to end without the network.
const TestAPIEnv = "CODEYE_TEST_GITHUB_API"

// APIBase is the fake API when TestAPIEnv is set, else github.com's.
func APIBase() string {
	if v := os.Getenv(TestAPIEnv); v != "" {
		return v
	}
	return DefaultAPIURL
}
