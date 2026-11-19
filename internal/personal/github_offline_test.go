package personal

import (
	"context"
	"os"
	"runtime"
	"testing"
)

// Offline mode asks neither the environment, gh nor GitHub, and removes a
// token stored before so nothing reads it.
func TestSetupGitHubOfflineLooksUpNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private file modes")
	}
	cfg, layout := gitHubLayout(t)
	if err := os.MkdirAll(layout.SecretsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.GitHubSecret(), []byte(`{"schema_version":"0.1","host":"github.com","token":"tok-old-1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Offline = true
	env := GitHubEnv{APIURL: "http://127.0.0.1:9", GH: "/usr/bin/gh",
		RunGH:  func(context.Context, string, string) (string, error) { t.Fatal("gh asked offline"); return "", nil },
		Getenv: func(k string) string { t.Fatalf("%s read offline", k); return "" }}
	st, err := SetupGitHub(context.Background(), cfg, layout, env)
	if err != nil || st.Enabled || st.Available || st.Reason != GitHubOffline {
		t.Fatalf("offline: %+v %v", st, err)
	}
	if _, err := os.Stat(layout.GitHubSecret()); !os.IsNotExist(err) {
		t.Fatalf("the stored token stays offline: %v", err)
	}
}
