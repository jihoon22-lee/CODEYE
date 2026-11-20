package instance

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/github"
)

func TestSetupGitHubUsesExplicitPlatformSource(t *testing.T) {
	cfg := &Config{GitHosts: []string{github.Host}}
	t.Setenv("GH_TOKEN", "personal-token-123")
	t.Setenv("GITHUB_TOKEN", "personal-token-456")
	if st, c := SetupGitHub(context.Background(), cfg, github.Host, ""); st.Reason != "no_token" || c != nil {
		t.Fatalf("implicit personal token: %q", st.Reason)
	}
	cfg.GitHub.TokenEnv = map[string]string{github.Host: "CODEYE_TEST_PLATFORM_TOKEN"}
	t.Setenv("CODEYE_TEST_PLATFORM_TOKEN", "")
	if st, c := SetupGitHub(context.Background(), cfg, github.Host, ""); st.Reason != "token_env_empty" || c != nil {
		t.Fatalf("empty explicit token: %q", st.Reason)
	}
	cfg.Offline = true
	t.Setenv("CODEYE_TEST_PLATFORM_TOKEN", "platform-token-123")
	if st, c := SetupGitHub(context.Background(), cfg, github.Host, ""); st.Reason != "disabled" || c != nil {
		t.Fatalf("public host offline: %q", st.Reason)
	}
}

func TestGitHubConfigField(t *testing.T) {
	state := filepath.Join(t.TempDir(), "s")
	raw := `{"schema_version":"0.1","state_dir":"` + filepath.ToSlash(state) + `","github":{"enabled":false}}`
	if runtime.GOOS == "windows" {
		t.Skip("absolute Unix path")
	}
	c, err := Parse([]byte(raw), nil)
	if err != nil || c.GitHub.On() {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Parse([]byte(strings.Replace(raw, `"enabled":false`, `"token":"x"`, 1)), nil); err == nil {
		t.Fatal("a token in the configuration is accepted")
	}
}
