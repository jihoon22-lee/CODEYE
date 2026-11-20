package github_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
)

// A user's token is one file named by the user ID, private to the service
// account, replaced on a second write and gone after Remove; an ID that is
// not a CODEYE ID never names a file.
func TestUserTokensStoreReplaceAndRemove(t *testing.T) {
	state := t.TempDir()
	d := github.UserTokensDir(state)
	user := domain.NewID()
	if c, err := d.Read(user); c != nil || err != nil {
		t.Fatalf("no file yet: %v %v", c, err)
	}
	if err := d.Write(user, github.Credential{Host: "github.com", Token: "ghp_first-token", Source: github.TokenFromWeb, Login: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Write(user, github.Credential{Host: "github.com", Token: "ghp_second-token", Source: github.TokenFromWeb}); err != nil {
		t.Fatal(err)
	}
	c, err := d.Read(user)
	if err != nil || c == nil || c.Token != "ghp_second-token" || c.Login != "" || c.SchemaVersion != github.CredentialSchema {
		t.Fatalf("read back %+v %v", c, err)
	}
	p := filepath.Join(state, "secrets", "github-users", user+".json")
	if d.Path(user) != p {
		t.Fatalf("path %q", d.Path(user))
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory holds %d entries: %v", len(entries), err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		di, _ := os.Stat(filepath.Dir(p))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	if err := d.Write(user, github.Credential{Host: "github.com", Token: "short"}); err == nil {
		t.Fatal("wrote an invalid token")
	}
	for _, bad := range []string{"../x", "", "alice"} {
		if d.Path(bad) != "" {
			t.Fatalf("%q names a file", bad)
		}
		if err := d.Write(bad, github.Credential{Host: "github.com", Token: "ghp_some-token"}); err == nil {
			t.Fatalf("wrote for %q", bad)
		}
	}
	if err := d.Remove(user); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove(user); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if c, err := d.Read(user); c != nil || err != nil {
		t.Fatalf("after remove: %v %v", c, err)
	}
	if github.UserTokensDir("").Path(user) != "" {
		t.Fatal("no state directory names a file")
	}
}
