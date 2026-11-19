//go:build linux || windows

package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, spec Spec) Result {
	t.Helper()
	var e Executor
	res := e.Run(context.Background(), spec)
	t.Logf("status=%s reason=%s cleanup=%s note=%s exit=%v signal=%s", res.Status, res.Reason, res.Cleanup, res.CleanupNote, derefInt(res.ExitCode), res.Signal)
	return res
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func requireClean(t *testing.T, res Result, status Status) {
	t.Helper()
	if res.Status != status {
		t.Fatalf("status = %s (%s: %s), want %s; stderr=%q", res.Status, res.Reason, res.Detail, status, res.Stderr.Data)
	}
	if res.Cleanup != CleanupConfirmed || res.RecoveryRequired() {
		t.Fatalf("cleanup = %s (%s), want confirmed", res.Cleanup, res.CleanupNote)
	}
	want := ContainmentProcessGroup
	if runtime.GOOS == "windows" {
		want = ContainmentJobObject
	}
	if res.Containment != want {
		t.Fatalf("containment = %s, want %s", res.Containment, want)
	}
}

func TestArgvPassedLiterally(t *testing.T) {
	ws := newWorkspace(t)
	literal := []string{
		"with space", "한글 ✓ ünïcödé", `"quoted"`, `back\slash\`, `trailing\\`,
		"$(touch pwned)", "`touch pwned2`", "; touch pwned3", "a&b|c<d>e^f%PATH%!x!",
		"", "*", "line1\nline2", "--flag=value with space",
	}
	res := run(t, helperSpec(t, ws, "args", literal...))
	requireClean(t, res, StatusExited)
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("exit code = %v", derefInt(res.ExitCode))
	}
	var got []string
	if err := json.Unmarshal(res.Stdout.Data, &got); err != nil {
		t.Fatalf("decode %q: %v", res.Stdout.Data, err)
	}
	if !slices.Equal(got, literal) {
		t.Fatalf("argv mismatch\n got %q\nwant %q", got, literal)
	}
	for _, f := range []string{"pwned", "pwned2", "pwned3"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err == nil {
			t.Fatalf("shell interpretation created %s", f)
		}
	}
	if res.Command.Argv[0] != helperExe(t) || !slices.Equal(res.Command.Argv[3:], literal) {
		t.Fatalf("command record = %q", res.Command.Argv)
	}
}

func TestEnvIsolation(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("CODEYE_LEAK_CANARY", "parent-only")
	t.Setenv("GITHUB_TOKEN", "ghp_parent")
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "env")
	spec.Env = map[string]string{"CODEYE_VISIBLE": "yes", "LANG": "C.UTF-8"}
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	var env []string
	if err := json.Unmarshal(res.Stdout.Data, &env); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[strings.ToUpper(k)] = v
	}
	allowed := map[string]bool{"CODEYE_VISIBLE": true, "LANG": true, "HOME": true, "USERPROFILE": true, "TMPDIR": true, "TMP": true, "TEMP": true}
	if runtime.GOOS == "windows" {
		allowed["SYSTEMROOT"] = true // added by os/exec on Windows
	}
	for k := range got {
		if !allowed[k] {
			t.Errorf("unexpected inherited env key %s", k)
		}
	}
	if got["CODEYE_VISIBLE"] != "yes" {
		t.Errorf("explicit env not passed")
	}
	home := got["HOME"]
	if filepath.Dir(home) != res.Command.TempDir || got["TMP"] != filepath.Join(res.Command.TempDir, "tmp") {
		t.Errorf("home/tmp %q %q not under per-run temp %q", home, got["TMP"], res.Command.TempDir)
	}
	if _, err := os.Stat(res.Command.TempDir); !os.IsNotExist(err) || res.Command.TempRetained {
		t.Errorf("per-run temp dir not removed after confirmed cleanup")
	}
	keys := append([]string(nil), res.Command.EnvKeys...)
	if !sort.StringsAreSorted(keys) || slices.Contains(keys, "SSH_AUTH_SOCK") {
		t.Errorf("env keys record = %v", keys)
	}

	for _, key := range []string{"GITHUB_TOKEN", "ssh_auth_sock", "DATABASE_URL", "ANTHROPIC_API_KEY", "MY_PASSWORD", "AWS_PROFILE"} {
		spec := helperSpec(t, ws, "cwd")
		spec.Env = map[string]string{key: "value-not-echoed"}
		res := run(t, spec)
		if res.Status != StatusRejected || res.Reason != "sensitive_env_rejected" {
			t.Errorf("%s: status=%s reason=%s", key, res.Status, res.Reason)
		}
		if strings.Contains(res.Detail, "value-not-echoed") {
			t.Errorf("detail leaks value")
		}
	}
	spec = helperSpec(t, ws, "cwd")
	spec.Env = map[string]string{"HOME": "/root"}
	if res := run(t, spec); res.Reason != "reserved_env" {
		t.Errorf("HOME override: %s", res.Reason)
	}
}

func TestSpecGuards(t *testing.T) {
	ws := newWorkspace(t)
	sub := filepath.Join(ws, "sub dir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	snapshot := t.TempDir()
	cases := []struct {
		name   string
		mutate func(*Spec)
		reason string
	}{
		{"relative executable", func(s *Spec) { s.Executable = filepath.Base(s.Executable) }, "invalid_executable"},
		{"missing executable", func(s *Spec) { s.Executable = filepath.Join(outside, "nope") }, "executable_not_found"},
		{"directory executable", func(s *Spec) { s.Executable = outside }, "executable_not_regular"},
		{"cwd outside workspace", func(s *Spec) { s.Dir = outside }, "cwd_outside_workspace"},
		{"cwd parent traversal", func(s *Spec) { s.Dir = filepath.Join(ws, "..") }, "cwd_outside_workspace"},
		{"relative cwd", func(s *Spec) { s.Dir = "sub dir" }, "invalid_workspace"},
		{"cwd in snapshot", func(s *Spec) { s.WorkspaceRoot, s.Dir = snapshot, snapshot; s.ProtectedRoots = []string{snapshot} }, "workspace_overlaps_protected_root"},
		{"workspace inside snapshot", func(s *Spec) { s.ProtectedRoots = []string{filepath.Dir(ws)} }, "workspace_overlaps_protected_root"},
		{"snapshot inside workspace", func(s *Spec) { s.ProtectedRoots = []string{sub} }, "workspace_overlaps_protected_root"},
		{"temp in snapshot", func(s *Spec) { s.ProtectedRoots = []string{snapshot}; s.TempParent = snapshot }, "temp_overlaps_protected_root"},
		{"no timeout", func(s *Spec) { s.Timeout = 0 }, "invalid_timeout"},
		{"bad sensitive index", func(s *Spec) { s.SensitiveArgs = []int{99} }, "invalid_sensitive_arg"},
		{"negative cap", func(s *Spec) { s.StdoutLimit = -1 }, "invalid_limit"},
		{"NUL argument", func(s *Spec) { s.Args = append(s.Args, "a\x00b") }, "invalid_argument"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := helperSpec(t, ws, "cwd")
			c.mutate(&spec)
			res := run(t, spec)
			if res.Status != StatusRejected || res.Reason != c.reason {
				t.Fatalf("got %s/%s, want rejected/%s", res.Status, res.Reason, c.reason)
			}
			if res.Cleanup != CleanupNotStarted || res.Process.PID != 0 {
				t.Fatalf("process started for rejected spec")
			}
			for _, d := range []string{ws, sub, outside, snapshot} {
				if _, err := os.Stat(filepath.Join(d, "started.txt")); err == nil {
					t.Fatalf("helper ran in %s", d)
				}
			}
		})
	}

	spec := helperSpec(t, ws, "cwd")
	spec.Dir = sub
	spec.ProtectedRoots = []string{snapshot}
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	want, _ := filepath.EvalSymlinks(sub)
	if got, _ := filepath.EvalSymlinks(string(res.Stdout.Data)); !strings.EqualFold(got, want) {
		t.Fatalf("cwd = %q, want %q", res.Stdout.Data, want)
	}
}

func TestOutputCapsPerStream(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "out", "300000", "1000")
	spec.StdoutLimit, spec.StderrLimit = 1024, 4096
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	if len(res.Stdout.Data) != 1024 || !res.Stdout.Truncated || res.Stdout.TotalBytes != 300000 {
		t.Fatalf("stdout len=%d truncated=%v total=%d", len(res.Stdout.Data), res.Stdout.Truncated, res.Stdout.TotalBytes)
	}
	if !bytes.Equal(res.Stdout.Data, bytes.Repeat([]byte("o"), 1024)) {
		t.Fatalf("stdout contains other stream data")
	}
	if len(res.Stderr.Data) != 1000 || res.Stderr.Truncated || res.Stderr.TotalBytes != 1000 || bytes.Contains(res.Stderr.Data, []byte("o")) {
		t.Fatalf("stderr len=%d truncated=%v", len(res.Stderr.Data), res.Stderr.Truncated)
	}
}

func TestNonzeroExitIsNotSuccessOrCrash(t *testing.T) {
	res := run(t, helperSpec(t, newWorkspace(t), "exit", "7"))
	requireClean(t, res, StatusExited)
	if res.ExitCode == nil || *res.ExitCode != 7 {
		t.Fatalf("exit code = %v", derefInt(res.ExitCode))
	}
	if res.LeftoverKilled || res.Command.TempRetained {
		t.Fatalf("plain exit reported leftover=%v tempRetained=%v", res.LeftoverKilled, res.Command.TempRetained)
	}
	if string(res.Stderr.Data) != "failing on purpose" {
		t.Fatalf("stderr = %q", res.Stderr.Data)
	}
}

func TestStdin(t *testing.T) {
	spec := helperSpec(t, newWorkspace(t), "stdin")
	spec.Stdin = []byte("input 한글\n")
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	if string(res.Stdout.Data) != "input 한글\n" {
		t.Fatalf("stdout = %q", res.Stdout.Data)
	}
}

func treePids(t *testing.T, ws string) []int {
	return []int{readPidFile(t, filepath.Join(ws, "pid-2")), readPidFile(t, filepath.Join(ws, "pid-1")), readPidFile(t, filepath.Join(ws, "pid-0"))}
}

func TestTimeoutKillsProcessTree(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "tree", "2", "wait")
	spec.Timeout = 15 * time.Second // three process starts on slow CI runners
	spec.Grace = 500 * time.Millisecond
	startedCh := make(chan ProcessIdentity, 1)
	spec.OnStarted = func(_ context.Context, id ProcessIdentity) error { startedCh <- id; return nil }
	begin := time.Now()
	res := run(t, spec)
	requireClean(t, res, StatusTimedOut)
	if res.Reason != "wall_clock_timeout" || res.ExitCode != nil && *res.ExitCode == 0 {
		t.Fatalf("reason=%s exit=%v", res.Reason, derefInt(res.ExitCode))
	}
	if elapsed := time.Since(begin); elapsed < spec.Timeout {
		t.Fatalf("returned before timeout: %s", elapsed)
	}
	pids := treePids(t, ws)
	var started ProcessIdentity
	select {
	case started = <-startedCh:
	case <-time.After(10 * time.Second):
		t.Fatal("OnStarted not called")
	}
	if started.PID != pids[0] || res.Process.PID != pids[0] || started.StartTime == 0 {
		t.Fatalf("identity %+v / %+v, main pid %d", started, res.Process, pids[0])
	}
	for _, pid := range pids {
		assertDead(t, pid)
	}
}

func TestCancelKillsProcessTree(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "tree", "2", "wait")
	spec.Grace = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		var e Executor
		done <- e.Run(ctx, spec)
	}()
	pids := treePids(t, ws) // grandchild exists before cancelling
	cancel()
	var res Result
	select {
	case res = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	requireClean(t, res, StatusCancelled)
	if res.Reason != "context_cancelled" {
		t.Fatalf("reason = %s", res.Reason)
	}
	for _, pid := range pids {
		assertDead(t, pid)
	}
}

func TestLeftoverProcessesKilledAfterMainExit(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "tree", "2", "exit")
	spec.Grace = 500 * time.Millisecond
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	if res.ExitCode == nil || *res.ExitCode != 0 || !res.LeftoverKilled {
		t.Fatalf("exit=%v leftover=%v", derefInt(res.ExitCode), res.LeftoverKilled)
	}
	if string(res.Stdout.Data) != "main exiting" || res.Stdout.Incomplete {
		t.Fatalf("stdout = %q incomplete=%v", res.Stdout.Data, res.Stdout.Incomplete)
	}
	for _, pid := range treePids(t, ws) {
		assertDead(t, pid)
	}
}

func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s|%v\n", rel, d.IsDir())
		if !d.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestSnapshotSourceUnchanged(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	ws := newWorkspace(t)
	for _, d := range []string{source, ws} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"main.go": "package main\n", "README.md": "doc\n"} {
			if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, wsBefore := treeHash(t, source), treeHash(t, ws)

	spec := helperSpec(t, ws, "write")
	spec.ProtectedRoots = []string{source}
	res := run(t, spec)
	requireClean(t, res, StatusExited)

	spec.WorkspaceRoot, spec.Dir = source, source
	if res := run(t, spec); res.Status != StatusRejected {
		t.Fatalf("running in protected source was not rejected: %s", res.Status)
	}
	if treeHash(t, source) != before {
		t.Fatal("protected source changed")
	}
	if treeHash(t, ws) == wsBefore {
		t.Fatal("tool did not run in workspace copy")
	}
}

func TestRedaction(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "args", "--token=hunter2", "plain-secret-arg", "visible")
	spec.SensitiveArgs = []int{3}
	spec.Redact = []string{"hunter2"}
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	meta, _ := json.Marshal(res)
	for _, blob := range [][]byte{meta, res.Stdout.Data, res.Stderr.Data} {
		if bytes.Contains(blob, []byte("hunter2")) || bytes.Contains(blob, []byte("plain-secret-arg")) {
			t.Fatalf("secret leaked: %s", blob)
		}
	}
	want := []string{"--token=[REDACTED]", "[REDACTED]", "visible"}
	if !slices.Equal(res.Command.Argv[3:], want) {
		t.Fatalf("argv record = %q", res.Command.Argv)
	}
	if !bytes.Contains(res.Stdout.Data, []byte("visible")) {
		t.Fatalf("stdout = %s", res.Stdout.Data)
	}
}

// A model provider's API key reaches the process only through
// OPENCODE_CONFIG_CONTENT: never argv, and it is masked in everything the
// executor records even when the process prints its environment.
func TestProviderKeyOnlyInProfile(t *testing.T) {
	ws := newWorkspace(t)
	const key = "sk-test-provider-key-0123456789"
	spec := helperSpec(t, ws, "env")
	spec.OpenCodeModel = "openai/gpt-6.2-sol"
	spec.OpenCodeProvider = &ModelProvider{ID: "openai", APIKey: key}
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	meta, _ := json.Marshal(res)
	for _, blob := range [][]byte{meta, res.Stdout.Data, res.Stderr.Data} {
		if bytes.Contains(blob, []byte(key)) {
			t.Fatalf("key leaked: %s", blob)
		}
	}
	if !bytes.Contains(res.Stdout.Data, []byte(`OPENCODE_CONFIG_CONTENT=`)) || !bytes.Contains(res.Stdout.Data, []byte(redactedMarker)) {
		t.Fatalf("the profile did not reach the process masked: %s", res.Stdout.Data)
	}
	for _, a := range res.Command.Argv {
		if strings.Contains(a, "sk-test") {
			t.Fatal(res.Command.Argv)
		}
	}
}

func TestEnforcedResourcesUnsupported(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "cwd")
	spec.Resources = ResourceRequest{CPUMillis: 1000, MemoryBytes: 1 << 30, RequireEnforced: true}
	res := run(t, spec)
	if res.Status != StatusUnsupported || res.Reason != "resource_enforcement_unavailable" || res.Cleanup != CleanupNotStarted {
		t.Fatalf("got %s/%s/%s", res.Status, res.Reason, res.Cleanup)
	}
	if _, err := os.Stat(filepath.Join(ws, "started.txt")); err == nil {
		t.Fatal("process ran")
	}

	spec.Resources.RequireEnforced = false
	res = run(t, spec)
	requireClean(t, res, StatusExited)
	if res.Resources.Enforced || res.Resources.Requested.MemoryBytes != 1<<30 {
		t.Fatalf("resources = %+v", res.Resources)
	}
}

func TestCancelledBeforeStart(t *testing.T) {
	ws := newWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var e Executor
	res := e.Run(ctx, helperSpec(t, ws, "cwd"))
	if res.Status != StatusCancelled || res.Cleanup != CleanupNotStarted {
		t.Fatalf("got %s/%s", res.Status, res.Cleanup)
	}
	if _, err := os.Stat(filepath.Join(ws, "started.txt")); err == nil {
		t.Fatal("process ran")
	}
}

func TestOnStartedSlowCallbackDoesNotDelayRun(t *testing.T) {
	spec := helperSpec(t, newWorkspace(t), "exit", "0")
	spec.OnStarted = func(context.Context, ProcessIdentity) error {
		time.Sleep(8 * time.Second) // ignores ctx on purpose
		return nil
	}
	begin := time.Now()
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("slow callback delayed Run: %s", elapsed)
	}
}

func TestOnStartedHungCallbackDoesNotBlockTimeout(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "sleep")
	spec.Timeout = 3 * time.Second
	spec.Grace = 200 * time.Millisecond
	spec.OnStarted = func(context.Context, ProcessIdentity) error {
		time.Sleep(30 * time.Second) // hung, ignores ctx
		return nil
	}
	begin := time.Now()
	res := run(t, spec)
	requireClean(t, res, StatusTimedOut)
	if elapsed := time.Since(begin); elapsed > spec.Timeout+5*time.Second {
		t.Fatalf("hung callback delayed timeout: %s", elapsed)
	}
	assertDead(t, readPidFile(t, filepath.Join(ws, "sleep")))
}

func TestOnStartedCtxEndsWithTimeout(t *testing.T) {
	spec := helperSpec(t, newWorkspace(t), "sleep")
	spec.Timeout = 2 * time.Second
	spec.Grace = 200 * time.Millisecond
	ctxErr := make(chan error, 1)
	spec.OnStarted = func(ctx context.Context, _ ProcessIdentity) error {
		<-ctx.Done()
		ctxErr <- ctx.Err()
		return ctx.Err()
	}
	res := run(t, spec)
	requireClean(t, res, StatusTimedOut) // not on_started_failed
	select {
	case err := <-ctxErr:
		if err == nil {
			t.Fatal("callback ctx not cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("callback ctx never ended")
	}
}

func TestOnStartedErrorTerminatesTree(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "tree", "2", "wait")
	spec.Grace = 500 * time.Millisecond
	spec.OnStarted = func(ctx context.Context, _ ProcessIdentity) error {
		for ctx.Err() == nil {
			if _, err := os.Stat(filepath.Join(ws, "pid-0")); err == nil {
				return errors.New("journal unavailable")
			}
			time.Sleep(20 * time.Millisecond)
		}
		return ctx.Err()
	}
	res := run(t, spec)
	requireClean(t, res, StatusCancelled)
	if res.Reason != "on_started_failed" || strings.Contains(res.Detail, "journal unavailable") {
		t.Fatalf("reason=%s detail=%q", res.Reason, res.Detail)
	}
	for _, pid := range treePids(t, ws) {
		assertDead(t, pid)
	}
}

func TestOnStartedPanicTerminatesProcess(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "sleep")
	spec.OnStarted = func(ctx context.Context, _ ProcessIdentity) error {
		waitForFile(filepath.Join(ws, "sleep"), 60*time.Second)
		panic("callback bug")
	}
	res := run(t, spec)
	requireClean(t, res, StatusCancelled)
	if res.Reason != "on_started_panic" {
		t.Fatalf("reason = %s", res.Reason)
	}
	assertDead(t, readPidFile(t, filepath.Join(ws, "sleep")))
}

func TestRedactionOverlappingSecretsAndPaths(t *testing.T) {
	ws := newWorkspace(t)
	spec := helperSpec(t, ws, "args", "xx abcdef-secret yy", "abc-alone")
	spec.Redact = []string{"abc", "abcdef-secret", "한글"}
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	meta, _ := json.Marshal(res)
	for _, blob := range [][]byte{meta, res.Stdout.Data} {
		for _, leak := range []string{"def-secret", "abc", "한글"} {
			if bytes.Contains(blob, []byte(leak)) {
				t.Fatalf("%q leaked in %s", leak, blob)
			}
		}
	}
	if !bytes.Contains(res.Stdout.Data, []byte("xx [REDACTED] yy")) {
		t.Fatalf("stdout = %s", res.Stdout.Data)
	}
	if !strings.Contains(res.Command.Dir, "[REDACTED]") {
		t.Fatalf("dir not redacted: %q", res.Command.Dir)
	}
}

func TestRedactionSecretCutAtCap(t *testing.T) {
	prefix := strings.Repeat("p", 20)
	secret := "TOPSECRET-12345"
	spec := helperSpec(t, newWorkspace(t), "args", prefix+secret+"tail")
	spec.Redact = []string{secret}
	spec.StdoutLimit = int64(len(`["`) + len(prefix) + 7) // cut inside the secret
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	if !res.Stdout.Truncated {
		t.Fatal("expected truncation")
	}
	if got := string(res.Stdout.Data); got != `["`+prefix {
		t.Fatalf("partial secret kept: %q", got)
	}
}

func TestExecutableInProtectedRootRejected(t *testing.T) {
	ws := newWorkspace(t)
	snapshot := t.TempDir()
	copyExe := filepath.Join(snapshot, filepath.Base(helperExe(t)))
	b, err := os.ReadFile(helperExe(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyExe, b, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := helperSpec(t, ws, "cwd")
	spec.ProtectedRoots = []string{snapshot}
	spec.Executable = copyExe
	if res := run(t, spec); res.Reason != "executable_in_protected_root" {
		t.Fatalf("alias in snapshot: %s", res.Reason)
	}
	// A workspace alias whose target is inside the snapshot.
	link := filepath.Join(ws, "tool-link"+filepath.Ext(copyExe))
	if err := os.Symlink(copyExe, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	spec.Executable = link
	if res := run(t, spec); res.Reason != "executable_in_protected_root" {
		t.Fatalf("target in snapshot: %s", res.Reason)
	}
	if _, err := os.Stat(filepath.Join(ws, "started.txt")); err == nil {
		t.Fatal("process ran")
	}
}

func TestExecutesResolvedTargetKeepsAliasArgv0(t *testing.T) {
	ws := newWorkspace(t)
	link := filepath.Join(ws, "alias-tool"+filepath.Ext(helperExe(t)))
	if err := os.Symlink(helperExe(t), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	spec := helperSpec(t, ws, "argv0")
	spec.Executable = link
	res := run(t, spec)
	requireClean(t, res, StatusExited)
	real, _ := filepath.EvalSymlinks(helperExe(t))
	if string(res.Stdout.Data) != link || res.Command.Executable != link || !strings.EqualFold(res.Command.ResolvedExecutable, real) {
		t.Fatalf("argv0=%q record=%+v", res.Stdout.Data, res.Command)
	}
}
