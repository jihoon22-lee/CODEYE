package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github/githubtest"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// One service, many users: a user without an allowed folder is told so and
// shown none; the administrator opens a folder to everyone in the web; each
// user stores their own GitHub token, which is never answered, logged or
// audited; a pull request is resolved, previewed and commented on with the
// token of the user who starts or asks (never the project owner's for a
// member), and a user without a token reaches public repositories only; the
// administrator's switches change what other users' runs get and the setup
// screen says why.
func TestSharedServiceForOtherUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private file modes")
	}
	const tokA, tokB = "tok-alice-123456", "tok-bob-1234567"
	e := newEnv(t)
	fake := githubtest.New("", "")
	defer fake.Close()
	fake.Users = map[string]string{tokA: "alice", tokB: "bob"}
	fake.Private = map[string]string{"acme/widget": "alice"}
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 7, Title: "Fix", State: "open", BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "fix", HeadSHA: strings.Repeat("b", 40), HeadRepo: "acme/widget"})
	e.inst = &Instance{StateDir: filepath.Join(t.TempDir(), "state"), GitHubAPI: fake.URL}
	e.start(t.TempDir())
	admin := e.login(e.newAdminAccount("admin"))
	alice := e.login(e.account("alice", "", false))
	bob := e.login(e.account("bob", "", false))
	ctx := context.Background()

	// No folder yet: the path is refused with the reason and the (empty) list.
	dir := t.TempDir()
	r := e.do("POST", "/api/v1/projects", map[string]any{"local_path": dir}, bob.o()).want(t, 422, "LOCAL_PATH_NOT_ALLOWED")
	if d := r.body["error"].(map[string]any)["details"].(map[string]any); len(d["readable_folders"].([]any)) != 0 {
		t.Fatalf("%s", r.raw)
	}
	if v := e.do("GET", "/api/v1/setup", nil, bob.o()).want(t, 200, ""); v.body["available"].(map[string]any)["local_path"].(map[string]any)["reason"] != "no_allowed_folder" {
		t.Fatalf("setup %s", v.raw)
	}
	// The administrator opens the folder to everyone.
	e.do("POST", "/api/v1/admin/source-roots", map[string]any{"path": dir}, bob.o()).want(t, 403, "FORBIDDEN")
	e.do("POST", "/api/v1/admin/source-roots", map[string]any{"path": filepath.Join(dir, "missing")}, admin.o()).want(t, 422, "FOLDER_NOT_FOUND")
	// A folder inside the data directory is refused (one that contains it,
	// like a home directory, is not: its projects are checked one by one).
	if err := os.MkdirAll(filepath.Join(e.inst.StateDir, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/v1/admin/source-roots", map[string]any{"path": filepath.Join(e.inst.StateDir, "run")}, admin.o()).want(t, 422, "FOLDER_HOLDS_CODEYE_DATA")
	root := e.do("POST", "/api/v1/admin/source-roots", map[string]any{"path": dir, "everyone": true}, admin.o()).want(t, 201, "")
	if root.body["everyone"] != true || root.body["label"] == "" {
		t.Fatalf("%s", root.raw)
	}
	e.do("POST", "/api/v1/admin/source-roots", map[string]any{"path": dir}, admin.o()).want(t, 409, "CONFLICT")
	e.do("POST", "/api/v1/projects", map[string]any{"local_path": dir}, bob.o()).want(t, 201, "")
	list := e.do("GET", "/api/v1/admin/source-roots", nil, admin.o()).want(t, 200, "")
	if items := list.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["path"] != root.body["path"] || items[0].(map[string]any)["everyone"] != true {
		t.Fatalf("%s", list.raw)
	}

	// Tokens: a refused one is not stored; a good one is never answered.
	e.do("PUT", "/api/v1/account/github", map[string]string{"token": "tok-wrong-123456"}, alice.o()).want(t, 422, "GITHUB_TOKEN_REFUSED")
	if v := e.do("GET", "/api/v1/account/github", nil, alice.o()).want(t, 200, ""); v.body["connected"] != false || v.body["storable"] != true {
		t.Fatalf("%s", v.raw)
	}
	put := e.do("PUT", "/api/v1/account/github", map[string]string{"token": tokA}, alice.o()).want(t, 200, "")
	if put.body["connected"] != true || put.body["account"] != "alice" || put.body["source"] != "web" || strings.Contains(string(put.raw), tokA) {
		t.Fatalf("%s", put.raw)
	}
	e.do("PUT", "/api/v1/account/github", map[string]string{"token": tokB}, bob.o()).want(t, 200, "")

	// alice's private repository, bob a runner of it.
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
	// alice's own run: her token reads her private repository.
	n := len(fake.Requests())
	run := runPR(alice, pa, 202, "")
	only(n, "alice")
	// bob starts a run of alice's project: his own token, which GitHub does
	// not show alice's private repository - never alice's token.
	n = len(fake.Requests())
	runPR(bob, pa, 422, "PULL_REQUEST_NOT_FOUND")
	only(n, "bob")
	// bob's preview of alice's run asks with bob's token too.
	n = len(fake.Requests())
	e.do("GET", "/api/v1/runs/"+run.body["id"].(string)+"/pull-request-comment", nil, bob.o()).want(t, 200, "")
	only(n, "bob")
	// bob's own project of the same repository: bob's token.
	n = len(fake.Requests())
	runPR(bob, pb, 422, "PULL_REQUEST_NOT_FOUND")
	only(n, "bob")
	// Once the repository is public, bob's run of alice's project is pinned
	// with bob's token, and its comment would be posted as bob.
	fake.Private = nil
	n = len(fake.Requests())
	bobRun := runPR(bob, pa, 202, "")
	only(n, "bob")
	if src, err := e.store.PullRequestCommentSource(ctx, bobRun.body["id"].(string), 0); err != nil || src.RequesterID != bob.id {
		t.Fatalf("comment of bob's run acts for %q (%v)", src.RequesterID, err)
	}
	fake.Private = map[string]string{"acme/widget": "alice"}
	e.do("DELETE", "/api/v1/account/github", nil, bob.o()).want(t, 204, "")
	if v := e.do("GET", "/api/v1/account/github", nil, bob.o()).want(t, 200, ""); v.body["connected"] != false {
		t.Fatalf("%s", v.raw)
	}
	n = len(fake.Requests())
	runPR(bob, pb, 422, "PULL_REQUEST_NOT_FOUND")
	if got := logins(n); len(got) == 0 || got[0] != "" {
		t.Fatalf("bob without a token used %v", got)
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
	for _, tok := range []string{tokA, tokB, "tok-wrong-123456"} {
		if strings.Contains(e.logs.String(), tok) || strings.Contains(audits, tok) {
			t.Fatalf("token %s reached the log or the audit", tok[:8])
		}
	}
	var set, removed int
	if err := e.pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE action=$1), count(*) FILTER (WHERE action=$2) FROM audit_events", postgres.AuditGitHubTokenSet, postgres.AuditGitHubTokenRemoved).Scan(&set, &removed); err != nil || set != 2 || removed != 1 {
		t.Fatalf("token audit rows: %d set, %d removed (%v)", set, removed, err)
	}
}
