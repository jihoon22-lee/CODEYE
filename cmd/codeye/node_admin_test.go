package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
)

func admin(t *testing.T, args ...string) (int, bool, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code, ok := runNodeAdmin(context.Background(), args, &out, &stderr)
	return code, ok, out.String(), stderr.String()
}

func TestNodeAdminDispatchAndValidation(t *testing.T) {
	for _, args := range [][]string{{"node"}, {"node", "agent"}, {"doctor"}, {"agent", "run"}} {
		if _, ok, _, _ := admin(t, args...); ok {
			t.Fatalf("claimed %v", args)
		}
	}
	for _, args := range [][]string{
		{"node", "token"},
		{"node", "token", "create"},
		{"node", "token", "create", "--pool", "default", "--ttl", "1s"},
		{"node", "token", "create", "--pool", "default", "--secret=leak"},
		{"node", "revoke"},
		{"node", "token", "revoke"},
		{"scheduler", "set"},
		{"scheduler", "set", "--project-max-running", "0"},
		{"scheduler", "set", "--global-max-running", "-1"},
		{"pool", "set"},
		{"pool", "set", "--min-workers", "1"},
		{"pool", "list", "extra"},
	} {
		code, ok, out, stderr := admin(t, args...)
		if !ok || code != 2 || strings.Contains(out+stderr, "leak") {
			t.Fatalf("%v: ok=%v code=%d %s", args, ok, code, stderr)
		}
	}
}

func TestNodeTokenCreateWritesPrivateFile(t *testing.T) {
	pool, _ := isolatedStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "src")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := cli(t, "source-root", "add", "--label", "work", "--path", root)
	if code != 0 {
		t.Fatal(stderr)
	}
	rootID := idOf(t, out)
	tokenFile := filepath.Join(dir, "state", "enroll-token")
	code, ok, out, stderr := admin(t, "node", "token", "create", "--pool", "default", "--source-root", rootID, "--output", tokenFile)
	if !ok || code != 0 {
		t.Fatal(stderr)
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	if len(token) != 43 || strings.Contains(out+stderr, token) || strings.Contains(out, root) {
		t.Fatalf("token printed or malformed: %s", out)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(tokenFile); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode())
		}
	}
	var info struct {
		TokenID       string   `json:"token_id"`
		SourceRootIDs []string `json:"source_root_ids"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil || len(info.SourceRootIDs) != 1 || info.SourceRootIDs[0] != rootID {
		t.Fatalf("output %s %v", out, err)
	}
	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM node_enrollment_tokens WHERE token_sha256=$1 AND used_at IS NULL", httpapi.TokenHash(token)).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("hash not stored: %d %v", stored, err)
	}
	// A symlink at the output path is replaced, not followed.
	if runtime.GOOS != "windows" {
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if code, _, _, stderr := admin(t, "node", "token", "create", "--pool", "default", "--output", link); code != 1 || !strings.Contains(stderr, "not a regular file") {
			t.Fatalf("symlink output: %d %s", code, stderr)
		}
		if b, _ := os.ReadFile(target); string(b) != "keep" {
			t.Fatal("symlink target overwritten")
		}
	}
	// Unknown root is refused; revoke and list work; scheduler limit changes.
	if code, _, _, _ := admin(t, "node", "token", "create", "--pool", "default", "--source-root", "00000000-0000-4000-8000-000000000000", "--output", filepath.Join(dir, "x")); code != 1 {
		t.Fatal("unknown root accepted")
	}
	if code, _, _, stderr := admin(t, "node", "token", "revoke", "--id", info.TokenID); code != 0 {
		t.Fatal(stderr)
	}
	code, _, out, stderr = admin(t, "node", "list")
	if code != 0 || !strings.Contains(out, `"items":[]`) {
		t.Fatalf("list %d %s %s", code, out, stderr)
	}
	if code, _, _, stderr := admin(t, "scheduler", "set", "--global-max-running", "3"); code != 0 {
		t.Fatal(stderr)
	}
	var limit int
	if err := pool.QueryRow(ctx, "SELECT global_max_running FROM scheduler_limits").Scan(&limit); err != nil || limit != 3 {
		t.Fatalf("limit %d %v", limit, err)
	}
	// W07: the per-project limit alone; the global limit is kept.
	if code, _, out, stderr := admin(t, "scheduler", "set", "--project-max-running", "2"); code != 0 || !strings.Contains(out, `"project_max_running":2`) || !strings.Contains(out, `"global_max_running":3`) {
		t.Fatal(out, stderr)
	}
	// W07 pool policy: unspecified fields keep their value; min>max refused;
	// stale --expect-revision refused; list shows the policy and scaler state.
	code, _, out, stderr = admin(t, "pool", "set", "--name", "default", "--max-workers", "3", "--idle-timeout-seconds", "30")
	if code != 0 || !strings.Contains(out, `"revision":2`) || !strings.Contains(out, `"max_workers":3`) || !strings.Contains(out, `"min_workers":0`) || !strings.Contains(out, `"idle_timeout_seconds":30`) {
		t.Fatalf("pool set %d %s %s", code, out, stderr)
	}
	if code, _, _, _ := admin(t, "pool", "set", "--name", "default", "--min-workers", "4"); code != 1 {
		t.Fatal("min > max accepted")
	}
	if code, _, _, _ := admin(t, "pool", "set", "--name", "default", "--max-workers", "4", "--expect-revision", "1"); code != 1 {
		t.Fatal("stale revision accepted")
	}
	code, _, out, stderr = admin(t, "pool", "list")
	if code != 0 || !strings.Contains(out, `"pool":"default"`) || !strings.Contains(out, `"capacity_state":"healthy"`) || !strings.Contains(out, `"max_workers":3`) {
		t.Fatalf("pool list %d %s %s", code, out, stderr)
	}
}
