package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/github/githubtest"
)

// Web accounts share platform source access and keep project-specific permissions.
func TestSharedServiceForOtherUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private file modes")
	}
	const platformToken, legacyToken = "tok-platform-123456", "tok-private-1234567"
	e := newEnv(t)
	fake := githubtest.New(platformToken, "platform")
	defer fake.Close()
	fake.Private = map[string]string{"acme/widget": "platform"}
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 7, Title: "Fix", State: "open", BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "fix", HeadSHA: strings.Repeat("b", 40), HeadRepo: "acme/widget"})
	e.inst = &Instance{StateDir: filepath.Join(t.TempDir(), "state"), GitHubAPI: fake.URL}
	e.start(t.TempDir())
	admin := e.login(e.newAdminAccount("admin"))
	alice := e.login(e.account("alice", "", false))
	bob := e.login(e.account("bob", "", false))
	ctx := context.Background()

	// A readable folder is available without a root or per-user grant.
	dir := t.TempDir()
	local := e.do("POST", "/api/v1/projects", map[string]any{"local_path": dir}, bob.o()).want(t, 201, "")
	id := local.body["id"].(string)
	e.do("GET", "/api/v1/projects/"+id, nil, alice.o()).want(t, 404, "NOT_FOUND")
	if v := e.do("GET", "/api/v1/setup", nil, bob.o()).want(t, 200, ""); v.body["available"].(map[string]any)["local_path"].(map[string]any)["available"] != true {
		t.Fatalf("setup %s", v.raw)
	}
	// Legacy personal tokens cannot be promoted to service credentials.
	legacyDir := filepath.Join(e.inst.StateDir, "secrets", "github-users")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(github.Credential{SchemaVersion: github.CredentialSchema, Host: github.Host, Token: legacyToken, Source: "web"})
	if err := os.WriteFile(filepath.Join(legacyDir, alice.id+".json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	e.do("PUT", "/api/v1/account/github", map[string]string{"token": legacyToken}, alice.o()).want(t, 404, "NOT_FOUND")

	prov, _, err := e.store.EnsureProvider(ctx, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	gitProject := func(u user) string {
		return e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "git", "provider_id": prov.ID, "repository_url": "https://github.com/acme/widget"}}, u.o()).want(t, 201, "").body["id"].(string)
	}
	pa := gitProject(alice)
	if err := e.store.SetMember(ctx, alice.id, pa, bob.id, domain.RoleRunner); err != nil {
		t.Fatal(err)
	}
	pb := gitProject(bob)
	runPR := func(u user, p string, status int, code string) resp {
		body := map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}, "pull_request": map[string]any{"number": 7}}
		return e.do("POST", "/api/v1/projects/"+p+"/runs", body, opts{cookie: u.cookie, csrf: u.csrf, idem: domain.NewID()}).want(t, status, code)
	}
	logins := func(from int) []string {
		var out []string
		for _, rq := range fake.Requests()[from:] {
			out = append(out, rq.Login)
		}
		return out
	}
	only := func(from int, login string) {
		t.Helper()
		got := logins(from)
		if len(got) == 0 {
			t.Fatal("GitHub was not asked")
		}
		for _, l := range got {
			if l != login {
				t.Fatalf("calls carried %v, want only %q", got, login)
			}
		}
	}
	runPR(alice, pa, 422, "PULL_REQUEST_NOT_FOUND")
	only(0, "")
	writeGitHubCredential(t, e.inst.StateDir, github.Host, platformToken, "platform")
	n := len(fake.Requests())
	run := runPR(alice, pa, 202, "")
	only(n, "platform")
	n = len(fake.Requests())
	bobRun := runPR(bob, pa, 202, "")
	only(n, "platform")
	n = len(fake.Requests())
	e.do("GET", "/api/v1/runs/"+run.body["id"].(string)+"/pull-request-comment", nil, bob.o()).want(t, 200, "")
	only(n, "platform")
	n = len(fake.Requests())
	runPR(bob, pb, 202, "")
	only(n, "platform")
	if src, err := e.store.PullRequestCommentSource(ctx, bobRun.body["id"].(string), 0); err != nil || src.RequesterID != bob.id {
		t.Fatalf("analysis requester %q (%v)", src.RequesterID, err)
	}

	// The administrator's switches.
	e.do("PUT", "/api/v1/admin/settings", map[string]bool{"others_model_review": false}, bob.o()).want(t, 403, "FORBIDDEN")
	st := e.do("PUT", "/api/v1/admin/settings", map[string]bool{"others_model_review": false, "others_build_scripts": true}, admin.o()).want(t, 200, "")
	if st.body["others_model_review"] != false || st.body["others_build_scripts"] != true {
		t.Fatalf("%s", st.raw)
	}
	for u, want := range map[user]any{bob: "not_offered", admin: nil} {
		v := e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, "")
		a := v.body["available"].(map[string]any)
		if a["model_review"].(map[string]any)["reason"] == "not_offered" != (want == "not_offered") || a["build_scripts"].(map[string]any)["available"] != true {
			t.Fatalf("setup of %s: %s", u.id, v.raw)
		}
	}
	if o, err := e.store.OfferFor(ctx, bob.id); err != nil || o.ModelReview || !o.BuildScripts {
		t.Fatalf("offer %+v %v", o, err)
	}

	// No token in a log line or an audit row.
	var audits string
	if err := e.pool.QueryRow(ctx, "SELECT coalesce(string_agg(detail::text, ' '), '') FROM audit_events").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{platformToken, legacyToken} {
		if strings.Contains(e.logs.String(), tok) || strings.Contains(audits, tok) {
			t.Fatalf("token %s reached the log or the audit", tok[:8])
		}
	}
}
