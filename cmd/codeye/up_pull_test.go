package main

import (
	"flag"
	"io"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

// A pull-request address of github.com is a pull-request target (with the
// browser's /files tab too); other hosts and plain repository addresses are
// not; --comment knows whether it was given.
func TestPullRequestTargetAndCommentFlag(t *testing.T) {
	cfg := &instance.Config{GitHosts: []string{"github.com", "ghe.example"}}
	for in, want := range map[string]int{
		"https://github.com/acme/widget/pull/7":        7,
		"https://github.com/acme/widget/pull/12/files": 12,
		"github.com/acme/widget/pull/3":                3,
	} {
		tg, ok, err := pullRequestTarget(cfg, in)
		if !ok || err != nil || tg.pullRequest != want || tg.repoURL != "https://github.com/acme/widget" || tg.host != "github.com" {
			t.Fatalf("%s: %+v %v %v", in, tg, ok, err)
		}
	}
	if target, ok, err := pullRequestTarget(cfg, "https://ghe.example/acme/widget/pull/7"); !ok || err != nil || target.host != "ghe.example" {
		t.Fatalf("another host: %v %v", ok, err)
	}
	if _, ok, _ := pullRequestTarget(&instance.Config{GitHosts: []string{"ghe.example"}}, "https://github.com/acme/widget/pull/7"); !ok {
		t.Fatal("a host outside git_hosts is not refused as a pull request")
	}
	for _, in := range []string{"", "https://github.com/acme/widget", "https://github.com/acme/widget/tree/main", "./pull/7"} {
		if _, ok, _ := pullRequestTarget(cfg, in); ok {
			t.Fatalf("%q taken as a pull request", in)
		}
	}
	target, rest := splitTarget([]string{"--comment", "--comment-wait", "10m", "https://github.com/acme/widget/pull/7", "--dry-run-comment"})
	if target != "https://github.com/acme/widget/pull/7" || len(rest) != 4 {
		t.Fatalf("%q %q", target, rest)
	}
	for args, want := range map[string]optionalBool{"": {}, "--comment": {set: true, value: true}, "--comment=false": {set: true}} {
		var b optionalBool
		fs := flag.NewFlagSet("up", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Var(&b, "comment", "")
		var argv []string
		if args != "" {
			argv = []string{args}
		}
		if err := fs.Parse(argv); err != nil || b != want {
			t.Fatalf("%q: %+v %v", args, b, err)
		}
	}
}
