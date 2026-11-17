package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunNodeDispatchAndUsage(t *testing.T) {
	ctx := context.Background()
	if _, ok := runNode(ctx, []string{"doctor"}, &bytes.Buffer{}, &bytes.Buffer{}); ok {
		t.Fatal("doctor must not be a node command")
	}
	cases := [][]string{
		{"agent"},
		{"agent", "--controller", "http://127.0.0.1:1"},
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--source-root", "not-a-mapping"},
		{"agent", "--controller", "http://example.com", "--state-dir", t.TempDir()},
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--workers", "17"},
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--git", "git"},
		{"worker", "--anything"},
		// W07: --autoscale excludes an explicit --workers; --max-workers needs --autoscale and 1..16.
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--autoscale", "--workers", "1"},
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--max-workers", "2"},
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--autoscale", "--max-workers", "17"},
		{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir(), "--autoscale", "--max-workers", "0"},
	}
	for _, args := range cases {
		var stderr bytes.Buffer
		code, ok := runNode(ctx, args, &bytes.Buffer{}, &stderr)
		if !ok || code != 2 {
			t.Errorf("%v: code %d ok %v (%s)", args, code, ok, stderr.String())
		}
	}
	// A secret-looking rejected flag value is never echoed.
	var stderr bytes.Buffer
	runNode(ctx, []string{"agent", "--bogus=hunter2-SECRET"}, &bytes.Buffer{}, &stderr)
	if strings.Contains(stderr.String(), "hunter2") {
		t.Fatal("rejected argument echoed")
	}
}

func TestAgentWithoutEnrollmentTokenExitsUsage(t *testing.T) {
	var stderr bytes.Buffer
	code, _ := runNode(context.Background(), []string{"agent", "--controller", "http://127.0.0.1:1", "--state-dir", t.TempDir()}, &bytes.Buffer{}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "enroll-token-file") {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
}
