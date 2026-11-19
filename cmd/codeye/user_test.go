package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin replaces the password input with a pipe carrying text.
func withStdin(t *testing.T, text string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(text); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	prev := passwordStdin
	passwordStdin = r
	t.Cleanup(func() { passwordStdin = prev; _ = r.Close() })
}

func TestUserAccountCommands(t *testing.T) {
	pool, _ := isolatedStore(t)
	ctx := context.Background()
	const secret = "operator chosen passphrase"
	withStdin(t, secret+"\n")
	code, out, errOut := cli(t, "user", "create", "--username", "Admin.One", "--admin", "--name", "First admin")
	if code != 0 {
		t.Fatalf("create admin: %d %s", code, errOut)
	}
	var created map[string]string
	if err := json.Unmarshal([]byte(out), &created); err != nil || created["username"] != "admin.one" {
		t.Fatalf("output %q", out)
	}
	admin := created["id"]
	if strings.Contains(out+errOut, secret) {
		t.Fatal("password echoed")
	}
	var isAdmin, hasPassword bool
	if err := pool.QueryRow(ctx, "SELECT is_admin,password_hash IS NOT NULL FROM users WHERE id=$1", admin).Scan(&isAdmin, &hasPassword); err != nil || !isAdmin || !hasPassword {
		t.Fatalf("stored admin %v %v %v", isAdmin, hasPassword, err)
	}
	var plain int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE position($1::bytea in coalesce(password_hash,'')) > 0", []byte(secret)).Scan(&plain); err != nil || plain != 0 {
		t.Fatal("password stored in clear")
	}

	// Policy, duplicates, and passwords never from flags.
	withStdin(t, "short\n")
	if code, _, _ := cli(t, "user", "create", "--username", "weak"); code != 2 {
		t.Fatal("weak password accepted")
	}
	withStdin(t, secret+"\n")
	if code, _, errOut := cli(t, "user", "create", "--username", "ADMIN.ONE"); code != 1 || !strings.Contains(errOut, "taken") {
		t.Fatalf("duplicate username: %d %s", code, errOut)
	}
	if code, _, _ := cli(t, "user", "create", "--username", "x", "--password", secret); code != 2 {
		t.Fatal("--password flag accepted")
	}

	// --password-file must not be readable by others.
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte("another long passphrase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := cli(t, "user", "create", "--username", "bob", "--password-file", loose); code != 2 || strings.Contains(errOut, "another long") {
		t.Fatalf("group-readable password file: %d %s", code, errOut)
	}
	tight := filepath.Join(dir, "tight")
	if err := os.WriteFile(tight, []byte("another long passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = cli(t, "user", "create", "--username", "bob", "--password-file", tight)
	if code != 0 {
		t.Fatal(errOut)
	}
	bob := idOf(t, out)

	// passwd revokes sessions and can force a change.
	withStdin(t, "a third long passphrase\n")
	if code, _, errOut := cli(t, "user", "passwd", "--username", "bob", "--must-change"); code != 0 {
		t.Fatal(errOut)
	}
	var must bool
	if err := pool.QueryRow(ctx, "SELECT must_change_password FROM users WHERE id=$1", bob).Scan(&must); err != nil || !must {
		t.Fatal("must-change not stored")
	}
	_, linkOnly, _ := cli(t, "user", "create")
	withStdin(t, secret+"\n")
	if code, _, _ := cli(t, "user", "passwd", "--id", idOf(t, linkOnly)); code != 1 {
		t.Fatal("password for a user without username")
	}

	// Administrator role and the last-administrator rule.
	if code, _, errOut := cli(t, "user", "admin", "--username", "admin.one", "--unset"); code != 1 || !strings.Contains(errOut, "last enabled administrator") {
		t.Fatalf("last admin demoted: %d %s", code, errOut)
	}
	if code, _, _ := cli(t, "user", "disable", "--username", "admin.one"); code != 1 {
		t.Fatal("last admin disabled")
	}
	if code, _, _ := cli(t, "user", "admin", "--username", "bob", "--set", "--unset"); code != 2 {
		t.Fatal("both --set and --unset accepted")
	}
	if code, _, errOut := cli(t, "user", "admin", "--username", "bob", "--set"); code != 0 {
		t.Fatal(errOut)
	}
	if code, _, errOut := cli(t, "user", "disable", "--username", "admin.one"); code != 0 {
		t.Fatal(errOut)
	}
	if code, _, _ := cli(t, "user", "disable", "--id", bob, "--username", "bob"); code != 2 {
		t.Fatal("both --id and --username accepted")
	}

	code, out, _ = cli(t, "user", "list")
	var list struct {
		Items []struct {
			ID       string  `json:"id"`
			Username *string `json:"username"`
			IsAdmin  bool    `json:"is_admin"`
			Disabled bool    `json:"disabled"`
		} `json:"items"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list.Items) != 3 || strings.Contains(out, "hash") {
		t.Fatalf("list %d %s", code, out)
	}

	// Every change is audited as the operator.
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_kind='operator_cli' AND action IN
		('user.created','auth.password_set','user.admin_granted','user.disabled')`).Scan(&rows); err != nil || rows != 6 {
		t.Fatalf("operator audit rows %d %v", rows, err)
	}
	var leaked int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE detail::text LIKE '%passphrase%'").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password in audit detail")
	}
}
