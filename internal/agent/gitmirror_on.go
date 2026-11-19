//go:build codeye_fixture

package agent

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// TestGitMirrorEnv names a directory of bare repositories laid out as
// HOST/OWNER/REPO.git. In test builds an approved HTTPS Git URL is fetched
// from there over file://, so the Git path runs end to end without a network.
const TestGitMirrorEnv = "CODEYE_TEST_GIT_MIRROR"

func testGitMirror(raw, host string) (string, string) {
	dir := os.Getenv(TestGitMirrorEnv)
	u, err := url.Parse(raw)
	if dir == "" || !filepath.IsAbs(dir) || host == "" || err != nil || !strings.EqualFold(u.Host, host) {
		return raw, ""
	}
	p := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	local := filepath.Join(dir, strings.ToLower(u.Host), filepath.FromSlash(strings.TrimPrefix(p, "/"))+".git")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(local)}).String(), dir
}
