package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/github/githubtest"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

const testGitHubToken = "tok-httpapi-123456"

// pullRequestRun is the isolation world's pull-request run: a github.com
// project of the same owner with the same members, and a run pinned
// directly (server mode resolves no pull request itself).
func (w *isoWorld) pullRequestRun() string {
	if w.prRun != "" {
		return w.prRun
	}
	ctx := context.Background()
	e := w.e
	prov, _, err := e.store.EnsureProvider(ctx, "github.com")
	if err != nil {
		e.t.Fatal(err)
	}
	p, err := e.store.RegisterProject(ctx, w.f.user.id, postgres.ProjectRegistration{Name: w.name("pr"), Source: domain.Source{Kind: domain.SourceGit, ProviderID: prov.ID, RepositoryURL: "https://github.com/acme/widget"}})
	if err != nil {
		e.t.Fatal(err)
	}
	for name, role := range map[string]domain.Role{"viewer": domain.RoleViewer, "runner": domain.RoleRunner, "manager": domain.RoleManager} {
		if err := e.store.SetMember(ctx, w.f.user.id, p.ID, w.actors[name].id, role); err != nil {
			e.t.Fatal(err)
		}
	}
	pin := domain.PullRequest{Host: "github.com", Repository: "acme/widget", Number: 7, URL: "https://github.com/acme/widget/pull/7", State: "open", BaseRef: "main",
		BaseSHA: strings.Repeat("1", 40), HeadRef: "fix", HeadSHA: strings.Repeat("2", 40), ResolvedAt: time.Now().UTC(), Files: []domain.PullRequestFile{}}
	r, err := e.store.EnqueuePullRequest(ctx, w.f.user.id, p.ID, "iso-pr", json.RawMessage(`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"},"pull_request":{"number":7}}`), pin)
	if err != nil {
		e.t.Fatal(err)
	}
	w.prRun = r.ID
	return r.ID
}

// writeGitHubCredential stores the token like `codeye up` does.
func writeGitHubCredential(t *testing.T, state, token, login string) {
	t.Helper()
	dir := filepath.Join(state, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(github.Credential{SchemaVersion: github.CredentialSchema, Host: "github.com", Token: token, Source: "GH_TOKEN", Login: login})
	if err := os.WriteFile(filepath.Join(dir, "github.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func patch(s string) *string { return &s }

// Pull-request runs in personal mode: the controller resolves the pull
// request with the stored token, pins it on the run and shows it in the run
// view and the comment preview; GitHub's refusals have their own codes;
// server mode, a folder project and schedules refuse pull requests; the
// remembered comment choice applies when the request leaves it out; the
// token never appears in a response or the log.
func TestPullRequestRunsOverHTTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private file modes")
	}
	e := newEnv(t)
	fake := githubtest.New(testGitHubToken, "octo")
	defer fake.Close()
	head := strings.Repeat("b", 40)
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 7, Title: "Fix the parser", State: "open", BaseRef: "main", BaseSHA: strings.Repeat("a", 40),
		HeadRef: "fix", HeadSHA: head, HeadRepo: "someone/widget", Files: []githubtest.File{
			{Filename: "parse.go", Status: "modified", Patch: patch("@@ -10,2 +10,3 @@\n a\n+b\n c")},
			{Filename: "old.go", Status: "removed", Patch: patch("@@ -1 +0,0 @@\n-x")},
			{Filename: "big.bin", Status: "added"},
		}})
	u := e.newUser()
	ctx := context.Background()
	prov, _, err := e.store.EnsureProvider(ctx, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	project := func(u user, provider, repo string) string {
		return e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "git", "provider_id": provider, "repository_url": repo}}, u.o()).want(t, 201, "").body["id"].(string)
	}
	runBody := func(pr map[string]any) map[string]any {
		return map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}, "pull_request": pr}
	}
	run := func(u user, p string, body any, status int, code string) resp {
		return e.do("POST", "/api/v1/projects/"+p+"/runs", body, opts{cookie: u.cookie, csrf: u.csrf, idem: domain.NewID()}).want(t, status, code)
	}
	// Server mode: no GitHub connection.
	serverProject := project(u, prov.ID, "https://github.com/acme/widget")
	run(u, serverProject, runBody(map[string]any{"number": 7}), 422, "PULL_REQUEST_UNSUPPORTED")

	state := filepath.Join(t.TempDir(), "state")
	writeGitHubCredential(t, state, testGitHubToken, "octo")
	e.personal = &Personal{StateDir: state, GitHubAPI: fake.URL}
	e.start(t.TempDir())
	u = e.login(u.id)
	p := serverProject
	r := run(u, p, runBody(map[string]any{"number": 7}), 202, "")
	v := e.do("GET", "/api/v1/runs/"+r.body["id"].(string), nil, u.o()).want(t, 200, "")
	pr := v.body["pull_request"].(map[string]any)
	c := pr["comment"].(map[string]any)
	if pr["number"] != float64(7) || pr["head_sha"] != head || pr["head_repository"] != "someone/widget" || pr["changed_files"] != float64(3) || pr["repository"] != "github.com/acme/widget" ||
		c["requested"] != false || c["state"] != "not_requested" {
		t.Fatalf("run view %s", v.raw)
	}
	// The preview before a report: no report yet, nothing posted.
	pv := e.do("GET", "/api/v1/runs/"+r.body["id"].(string)+"/pull-request-comment", nil, u.o()).want(t, 200, "")
	if body := pv.body["body"].(string); !strings.Contains(body, "아직 보고서가 없다") || pv.body["current_head_sha"] != head || pv.body["head_moved"] != false || pv.body["report_revision"] != nil {
		t.Fatalf("preview %s", pv.raw)
	}
	// A branch run has no pull-request block and no preview.
	plain := run(u, p, map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, 202, "")
	if pv := e.do("GET", "/api/v1/runs/"+plain.body["id"].(string), nil, u.o()).want(t, 200, ""); pv.body["pull_request"] != nil {
		t.Fatalf("branch run %s", pv.raw)
	}
	e.do("GET", "/api/v1/runs/"+plain.body["id"].(string)+"/pull-request-comment", nil, u.o()).want(t, 404, "NOT_A_PULL_REQUEST_RUN")

	// The remembered choice applies; the request's own choice wins.
	e.do("PUT", "/api/v1/projects/"+p+"/analysis", map[string]bool{"send_source_to_model": true, "run_build_scripts": false, "comment_on_pull_request": true}, u.o()).want(t, 200, "")
	on := run(u, p, runBody(map[string]any{"number": 7}), 202, "")
	off := run(u, p, runBody(map[string]any{"number": 7, "comment": false}), 202, "")
	for id, want := range map[string]bool{on.body["id"].(string): true, off.body["id"].(string): false} {
		c := e.do("GET", "/api/v1/runs/"+id, nil, u.o()).want(t, 200, "").body["pull_request"].(map[string]any)["comment"].(map[string]any)
		if c["requested"] != want {
			t.Fatalf("comment choice %v: %v", want, c)
		}
	}
	if fake.Writes() != 0 {
		t.Fatalf("%d GitHub writes before any report", fake.Writes())
	}

	// GitHub's answers.
	run(u, p, runBody(map[string]any{"number": 8}), 422, "PULL_REQUEST_NOT_FOUND")
	fake.FailPrefix, fake.FailStatus = "/repos/acme/widget/pulls/7", 429
	if r := run(u, p, runBody(map[string]any{"number": 7}), 503, "GITHUB_RATE_LIMITED"); r.body["error"].(map[string]any)["retryable"] != true {
		t.Fatalf("%s", r.raw)
	}
	fake.FailStatus = 403
	run(u, p, runBody(map[string]any{"number": 7}), 422, "GITHUB_FORBIDDEN")
	fake.FailStatus = 401
	run(u, p, runBody(map[string]any{"number": 7}), 422, "GITHUB_TOKEN_REFUSED")
	fake.FailPrefix = ""
	run(u, p, runBody(map[string]any{"number": 0}), 422, "VALIDATION_FAILED")
	lab, _, err := e.store.EnsureProvider(ctx, "gitlab.example")
	if err != nil {
		t.Fatal(err)
	}
	other := project(u, lab.ID, "https://gitlab.example/acme/widget")
	run(u, other, runBody(map[string]any{"number": 7}), 422, "PULL_REQUEST_UNSUPPORTED")
	sched := scheduleBody()
	sched["run"].(map[string]any)["pull_request"] = map[string]any{"number": 7}
	e.do("POST", "/api/v1/projects/"+p+"/schedules", sched, opts{cookie: u.cookie, csrf: u.csrf, idem: domain.NewID()}).want(t, 422, "VALIDATION_FAILED")

	// Every API call carried the stored token; no answer or log line did.
	for _, rq := range fake.Requests() {
		if !rq.Authorized {
			t.Fatalf("anonymous call %+v", rq)
		}
	}
	if strings.Contains(e.logs.String(), testGitHubToken) {
		t.Fatal("the token reached the log")
	}
	setup := e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, "")
	if strings.Contains(string(setup.raw), testGitHubToken) {
		t.Fatal("the token reached GET /setup")
	}
}
