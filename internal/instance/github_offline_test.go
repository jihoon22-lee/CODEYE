package instance

import (
	"context"
	"testing"
)

// Offline mode asks neither the environment, gh nor GitHub and returns no
// credential, so `codeye up` removes a token it stored before.
func TestSetupGitHubOfflineLooksUpNothing(t *testing.T) {
	cfg, _ := gitHubLayout(t)
	cfg.Offline = true
	env := GitHubEnv{APIURL: "http://127.0.0.1:9", GH: "/usr/bin/gh",
		RunGH:  func(context.Context, string, string) (string, error) { t.Fatal("gh asked offline"); return "", nil },
		Getenv: func(k string) string { t.Fatalf("%s read offline", k); return "" }}
	st, c := SetupGitHub(context.Background(), cfg, env)
	if st.Enabled || st.Available || st.Reason != GitHubOffline || c != nil {
		t.Fatalf("offline: %+v %+v", st, c)
	}
}
