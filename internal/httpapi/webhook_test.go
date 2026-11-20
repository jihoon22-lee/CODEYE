package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
)

// TC-28 verifies the actual HTTP endpoint, durable queue, and pinned run
// against an in-process GitHub API fixture and real PostgreSQL.
func TestGitHubWebhookDelivery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	const repo, token = "acme/project", "fixture-api-token-for-webhook"
	var permission atomic.Value
	permission.Store("write")
	var beforeFiles atomic.Pointer[func()]
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/repos/"+repo+"/collaborators/developer/permission" {
			_ = json.NewEncoder(w).Encode(map[string]any{"permission": permission.Load().(string), "user": map[string]int{"id": 123}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			if f := beforeFiles.Swap(nil); f != nil {
				(*f)()
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		if r.URL.Path != "/repos/"+repo+"/pulls/7" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		repository := map[string]any{"id": 1234, "full_name": repo}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 7, "state": "open", "title": "Review", "changed_files": 0,
			"base": map[string]any{"ref": "main", "sha": base, "repo": repository}, "head": map[string]any{"ref": "feature", "sha": head, "repo": repository}})
	}))
	t.Cleanup(remote.Close)
	state := t.TempDir()
	if err := github.WritePlatformCredentials(github.PlatformCredentialFile(state), github.PlatformCredentials{"github.com": {
		SchemaVersion: github.CredentialSchema, Token: token, Login: "codeye-bot", Host: "github.com",
	}}); err != nil {
		t.Fatal(err)
	}
	e.inst = &Instance{StateDir: state, GitHubAPI: remote.URL}
	e.start(t.TempDir())
	owner := e.login(e.account("hook-owner", "a fixture password", false))
	provider, _, err := e.store.EnsureProvider(ctx, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	create := map[string]any{"source": domain.Source{Kind: domain.SourceGit, ProviderID: provider.ID, RepositoryURL: "https://github.com/" + repo},
		"analysis": map[string]bool{"comment_on_pull_request": true, "send_source_to_model": false, "run_build_scripts": false}}
	project := e.do("POST", "/api/v1/projects", create, owner.o()).want(t, 201, "").body["id"].(string)
	path := e.hs.URL + "/api/v1/github/webhooks/" + project
	const secret = "locally-generated-fixture-signature-value"
	cfg := webhookConfig{Host: "github.com", Repository: repo, RepositoryID: 1234, Actor: owner.id, Secret: secret, PullRequests: true, Mentions: true}
	confDir := filepath.Join(state, "secrets", "github-webhooks")
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeConfig := func(mode os.FileMode) {
		t.Helper()
		p := filepath.Join(confDir, project+".json")
		raw, _ := json.Marshal(cfg)
		if err := os.WriteFile(p, raw, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	send := func(event, delivery, signingKey, origin string, payload any, want int) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		mac := hmac.New(sha256.New, []byte(signingKey))
		mac.Write(raw)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := e.hs.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			var detail json.RawMessage
			_ = json.NewDecoder(res.Body).Decode(&detail)
			t.Fatalf("delivery status %d, want %d: %s", res.StatusCode, want, detail)
		}
	}
	repository := map[string]any{"id": 1234, "full_name": repo}
	pull := func(headID int, sha string) map[string]any {
		return map[string]any{"action": "opened", "number": 7, "repository": repository, "pull_request": map[string]any{
			"head": map[string]any{"sha": sha, "repo": map[string]any{"id": headID}}, "base": map[string]any{"repo": repository},
		}}
	}
	writeConfig(0o644)
	send("pull_request", domain.NewID(), secret, "", pull(1234, head), 404)
	writeConfig(0o600)
	send("pull_request", domain.NewID(), secret, e.hs.URL, pull(1234, head), 403)
	send("pull_request", domain.NewID(), "wrong", "", pull(1234, head), 401)
	wrongRepo := pull(1234, head)
	wrongRepo["repository"] = map[string]any{"id": 1235, "full_name": repo}
	send("pull_request", domain.NewID(), secret, "", wrongRepo, 422)
	const auto = "pull_request"
	delivery := domain.NewID()
	send(auto, delivery, secret, "", pull(1234, head), 202)
	send(auto, delivery, secret, "", pull(1234, head), 202)
	send(auto, delivery, secret, "", pull(1234, base), 409)
	countRuns := func(want int) {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(ctx, "SELECT count(*) FROM runs WHERE project_id=$1", project).Scan(&n); err != nil || n != want {
			t.Fatalf("runs %d, want %d: %v", n, want, err)
		}
	}
	countRuns(0)
	stale, err := e.store.ClaimWebhookDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE github_webhook_deliveries SET next_at=now() WHERE project_id=$1 AND delivery_id=$2", project, delivery); err != nil {
		t.Fatal(err)
	}
	due, err := e.store.ClaimWebhookDelivery(ctx)
	if err != nil || due.Attempts != stale.Attempts+1 {
		t.Fatalf("reclaimed delivery = %+v, %v", due, err)
	}
	if reason, _ := e.srv.handleWebhookDelivery(ctx, stale); reason == "" {
		t.Fatal("expired claim enqueued a run")
	}
	countRuns(0)
	if reason, _ := e.srv.handleWebhookDelivery(ctx, due); reason != "" {
		t.Fatalf("reclaimed delivery did not enqueue: %s", reason)
	}
	send(auto, domain.NewID(), secret, "", pull(1235, head), 202) // fork ignored before API calls
	send(auto, domain.NewID(), secret, "", pull(1234, base), 202) // superseded event ignored after the API call
	send(auto, domain.NewID(), secret, "", pull(1234, head), 202) // same head deduped after pinning
	for e.srv.processWebhook(ctx) {
	}
	countRuns(1)
	var runID string
	if err := e.pool.QueryRow(ctx, "SELECT run_id::text FROM github_webhook_deliveries WHERE project_id=$1 AND delivery_id=$2", project, delivery).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	run := e.do("GET", "/api/v1/runs/"+runID, nil, owner.o()).want(t, 200, "").body
	if run["trigger"].(map[string]any)["kind"] != "github_webhook" || run["pull_request"].(map[string]any)["head_sha"] != head ||
		run["pull_request"].(map[string]any)["comment"].(map[string]any)["requested"] != true {
		t.Fatalf("webhook run is not pinned or does not use the comment setting: %+v", run)
	}
	comment := func(id int, text, login, kind string) map[string]any {
		who := map[string]any{"id": 123, "login": login, "type": kind}
		return map[string]any{"action": "created", "repository": repository, "sender": who,
			"issue":   map[string]any{"number": 7, "pull_request": map[string]any{}},
			"comment": map[string]any{"id": id, "body": text, "user": who}}
	}
	const mention = "issue_comment"
	send(mention, domain.NewID(), secret, "", comment(11, "> @codeye-bot", "developer", "User"), 202)
	send(mention, domain.NewID(), secret, "", comment(12, "@codeye-bot", "codeye-bot", "User"), 202)
	send(mention, domain.NewID(), secret, "", comment(13, "@codeye-bot", "external-bot", "Bot"), 202)
	permission.Store("read")
	send(mention, domain.NewID(), secret, "", comment(14, "@codeye-bot", "developer", "User"), 202)
	for e.srv.processWebhook(ctx) {
	}
	countRuns(1)
	permission.Store("write")
	accountRenamed := comment(14, "@codeye-bot", "developer", "User")
	accountRenamed["sender"].(map[string]any)["id"] = 456
	send(mention, domain.NewID(), secret, "", accountRenamed, 202)
	for e.srv.processWebhook(ctx) {
	}
	countRuns(1)
	updated := make(chan error, 1)
	disableComment := func() {
		choices := domain.DefaultAnalysisChoices(domain.SourceGit)
		_, err := e.store.SetProjectAnalysis(ctx, owner.id, project, choices)
		updated <- err
	}
	beforeFiles.Store(&disableComment)
	mentionDelivery := domain.NewID()
	send(mention, mentionDelivery, secret, "", comment(15, "@CODEYE-bot", "developer", "User"), 202)
	send(mention, domain.NewID(), secret, "", comment(15, "@CODEYE-bot", "developer", "User"), 202)
	for e.srv.processWebhook(ctx) {
	}
	countRuns(2)
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("the comment choice did not change during PR resolution")
	}
	var commentRequested bool
	if err := e.pool.QueryRow(ctx, `SELECT (r.pull_request->>'comment')::boolean FROM runs r
		JOIN github_webhook_deliveries d ON d.run_id=r.id WHERE d.project_id=$1 AND d.delivery_id=$2`, project, mentionDelivery).Scan(&commentRequested); err != nil || commentRequested {
		t.Fatalf("webhook used obsolete comment choice: %v, %v", commentRequested, err)
	}
	send(mention, domain.NewID(), secret, "", comment(16, "@codeye-bot", "developer", "User"), 202)
	if _, err := e.pool.Exec(ctx, "UPDATE users SET disabled_at=now() WHERE id=$1", owner.id); err != nil {
		t.Fatal(err)
	}
	for e.srv.processWebhook(ctx) {
	}
	countRuns(2)
	if strings.Contains(e.logs.String(), secret) || strings.Contains(e.logs.String(), token) {
		t.Fatal("GitHub credential logged")
	}
}
