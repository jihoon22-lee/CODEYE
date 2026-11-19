package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/github/githubtest"
)

func ptr(s string) *string { return &s }

const (
	baseSHA = "1111111111111111111111111111111111111111"
	headSHA = "2222222222222222222222222222222222222222"
)

func TestChangedLines(t *testing.T) {
	patch := "@@ -1,3 +1,4 @@\n package a\n-old\n+new\n+more\n keep\n@@ -20,2 +21,3 @@ func x() {\n a\n+b\n c\n\\ No newline at end of file"
	got, ok := github.ChangedLines(patch)
	if !ok || !reflect.DeepEqual(got, [][2]int{{2, 3}, {22, 22}}) {
		t.Fatalf("ranges %v %v", got, ok)
	}
	if got, ok := github.ChangedLines("@@ -1,2 +0,0 @@\n-a\n-b"); !ok || len(got) != 0 {
		t.Fatalf("pure deletion: %v %v", got, ok)
	}
	if got, ok := github.ChangedLines("@@ -0,0 +1,2 @@\n+a\n+b"); !ok || !reflect.DeepEqual(got, [][2]int{{1, 2}}) {
		t.Fatalf("new file: %v %v", got, ok)
	}
	if _, ok := github.ChangedLines("not a patch"); ok {
		t.Fatal("garbage parsed")
	}
}

func TestResolvePaginatesAndKeepsTheForkAndTheFileStatuses(t *testing.T) {
	fake := githubtest.New("tok", "octo")
	defer fake.Close()
	fake.PerPage = 2
	files := []githubtest.File{
		{Filename: "a/one.go", Status: "modified", Patch: ptr("@@ -3,1 +3,2 @@\n x\n+y")},
		{Filename: "a/two.go", Status: "added", Patch: ptr("@@ -0,0 +1,1 @@\n+z")},
		{Filename: "old.go", Status: "removed", Patch: ptr("@@ -1,1 +0,0 @@\n-gone")},
		{Filename: "img.png", Status: "modified"},
		{Filename: "b/new.go", Status: "renamed", PreviousFilename: "b/old.go", Patch: ptr("@@ -1,1 +1,1 @@\n-a\n+b")},
	}
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 7, Title: "Fix it", BaseRef: "main", BaseSHA: baseSHA, HeadRef: "fix", HeadSHA: headSHA, HeadRepo: "someone/widget", Files: files})
	c := github.New(fake.URL, "tok")
	p, err := github.Resolve(context.Background(), c, "github.com", "acme/widget", 7, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if p.HeadRepository != "someone/widget" || p.HeadSHA != headSHA || p.BaseSHA != baseSHA || p.URL != "https://github.com/acme/widget/pull/7" || p.FilesTruncated || len(p.Files) != 5 {
		t.Fatalf("pin %+v", p)
	}
	want := []domain.PullRequestFile{
		{Path: "a/one.go", Status: "modified", LinesKnown: true, Lines: [][2]int{{4, 4}}},
		{Path: "a/two.go", Status: "added", LinesKnown: true, Lines: [][2]int{{1, 1}}},
		{Path: "old.go", Status: "removed"},
		{Path: "img.png", Status: "modified"},
		{Path: "b/new.go", Status: "renamed", PreviousPath: "b/old.go", LinesKnown: true, Lines: [][2]int{{1, 1}}},
	}
	if !reflect.DeepEqual(p.Files, want) {
		t.Fatalf("files %+v", p.Files)
	}
	pages := 0
	for _, r := range fake.Requests() {
		if strings.HasSuffix(r.Path, "/files") {
			pages++
			if !r.Authorized {
				t.Fatal("unauthorized page request")
			}
		}
	}
	if pages != 3 {
		t.Fatalf("%d file pages", pages)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveMarksAShortListingTruncated(t *testing.T) {
	fake := githubtest.New("", "")
	defer fake.Close()
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 8, BaseSHA: baseSHA, HeadSHA: headSHA, ChangedFiles: 3500,
		Files: []githubtest.File{{Filename: "x.go", Status: "modified"}, {Filename: "../evil", Status: "added"}}})
	p, err := github.Resolve(context.Background(), github.New(fake.URL, ""), "github.com", "acme/widget", 8, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !p.FilesTruncated || len(p.Files) != 1 || p.ChangedFiles != 3500 {
		t.Fatalf("pin %+v", p)
	}
}

func TestErrorKinds(t *testing.T) {
	fake := githubtest.New("tok", "octo")
	defer fake.Close()
	ctx := context.Background()
	if _, err := github.New(fake.URL, "tok").PullRequest(ctx, "acme/none", 1); github.KindOf(err) != github.KindNotFound {
		t.Fatalf("404: %v", err)
	}
	if _, err := github.New(fake.URL, "wrong").User(ctx); github.KindOf(err) != github.KindUnauthorized {
		t.Fatalf("401: %v", err)
	}
	fake.FailPrefix, fake.FailStatus, fake.FailBody = "/repos/", http.StatusForbidden, "API rate limit exceeded"
	_, err := github.New(fake.URL, "tok").PullRequest(ctx, "acme/x", 1)
	var e *github.Error
	if github.KindOf(err) != github.KindRateLimited || !asError(err, &e) || e.RetryAfter != time.Minute {
		t.Fatalf("rate limit: %v", err)
	}
	fake.FailBody = "Resource not accessible"
	if _, err := github.New(fake.URL, "tok").PullRequest(ctx, "acme/x", 1); github.KindOf(err) != github.KindForbidden {
		t.Fatalf("403: %v", err)
	}
	fake.FailStatus, fake.FailBody = http.StatusTooManyRequests, ""
	if _, err := github.New(fake.URL, "tok").PullRequest(ctx, "acme/x", 1); github.KindOf(err) != github.KindRateLimited {
		t.Fatalf("429: %v", err)
	}
	if u, err := github.New(fake.URL, "tok").User(ctx); err != nil || u.Login != "octo" {
		t.Fatalf("user: %v", err)
	}
}

func asError(err error, target **github.Error) bool {
	e, ok := err.(*github.Error)
	if ok {
		*target = e
	}
	return ok
}

// The token goes to the configured API host only: a pagination link or a
// redirect to another host is not followed.
func TestTokenStaysOnTheAPIHost(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		fmt.Fprint(w, "[]")
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("Link", "<"+other.URL+"/next>; rel=\"next\"")
			fmt.Fprint(w, "[]")
		default:
			http.Redirect(w, r, other.URL+"/user", http.StatusFound)
		}
	}))
	defer api.Close()
	c := github.New(api.URL, "secret")
	if _, err := c.PullRequestFiles(context.Background(), "a/b", 1); github.KindOf(err) != github.KindInvalid {
		t.Fatalf("foreign next link: %v", err)
	}
	if _, err := c.User(context.Background()); err == nil {
		t.Fatal("redirect followed")
	}
	if leaked {
		t.Fatal("the token reached another host")
	}
}
