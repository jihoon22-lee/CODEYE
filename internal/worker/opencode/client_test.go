package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

type runnerFunc func(context.Context, executor.Spec) executor.Result

func (f runnerFunc) Run(ctx context.Context, s executor.Spec) executor.Result { return f(ctx, s) }

func successful(stdout string) executor.Result {
	code := 0
	return executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed, Stdout: executor.Stream{Data: []byte(stdout)}}
}

func events(text string) string {
	parts := []map[string]any{{"type": "step_start"}, {"type": "reasoning", "text": "private reasoning"}, {"type": "text", "text": text}, {"type": "step_finish", "reason": "stop", "tokens": map[string]int{"input": 10, "output": 5}}}
	var lines strings.Builder
	for _, part := range parts {
		kind := part["type"]
		part["sessionID"], part["messageID"] = "session", "msg"
		line, _ := json.Marshal(map[string]any{"type": kind, "sessionID": "session", "part": part})
		lines.Write(line)
		lines.WriteByte('\n')
	}
	return lines.String()
}

func TestBoundWorkspaceRequest(t *testing.T) {
	scratch := t.TempDir()
	cfg := Config{Executable: filepath.Join(scratch, "approved-cli"), Model: "opencode/fixture", Timeout: time.Minute}
	q := Request{Scratch: scratch, SnapshotDigest: strings.Repeat("1", 64), Role: "probe", Instructions: "Return the sum.", Input: json.RawMessage(`{"a":2,"b":3}`)}
	var workdir string
	calls := 0
	reply := cfg.Review(t.Context(), runnerFunc(func(_ context.Context, s executor.Spec) executor.Result {
		calls++
		if reflect.DeepEqual(s.Args, []string{"--version"}) {
			return successful(Version + "\n")
		}
		workdir = s.Dir
		if s.WorkspaceRoot != workdir || workdir == scratch || filepath.Dir(workdir) != scratch || strings.Contains(strings.Join(s.Args, " "), string(q.Input)) || !strings.Contains(string(s.Stdin), string(q.Input)) {
			t.Fatal("unsafe workspace or prompt transport")
		}
		if s.OpenCodeModel != cfg.Model {
			t.Fatal("wrong managed profile")
		}
		proof, err := os.ReadFile(s.Args[len(s.Args)-1])
		if err != nil {
			t.Fatal(err)
		}
		return successful(events(`{"workspace_proof":"` + strings.TrimSpace(string(proof)) + `","result":{"sum":5}}`))
	}), q)
	if reply.Reason != "" || calls != 2 || string(reply.Output) != `{"sum":5}` || reply.Usage.Output != 5 || reply.RequestDigest == "" {
		t.Fatalf("unexpected outcome %+v", reply)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Fatalf("retained workdir: %v", err)
	}
}

func TestMissingModelAndUnsupportedVersion(t *testing.T) {
	cfg := Config{Executable: filepath.Join(t.TempDir(), "opencode"), Timeout: time.Second}
	q := Request{Scratch: t.TempDir(), SnapshotDigest: strings.Repeat("0", 64), Role: "verify", Input: json.RawMessage(`{}`)}
	calls := 0
	runner := runnerFunc(func(context.Context, executor.Spec) executor.Result { calls++; return successful("99.0") })
	if reply := cfg.Review(t.Context(), runner, q); reply.Reason != "model_not_configured" || calls != 0 {
		t.Fatal(reply)
	}
	cfg.Model = "opencode/free"
	if reply := cfg.Review(t.Context(), runner, q); reply.Reason != "opencode_version_unsupported" || calls != 1 {
		t.Fatal(reply)
	}
}

func TestReviewSeparatesVersionProbeAndModelTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name, reason, version string
		failedSpec            int
		status                executor.Status
		cleanup               executor.Cleanup
		limit, probeLimit     time.Duration
	}{
		{name: "version timeout", reason: ReasonVersionTimeout, failedSpec: 1, limit: time.Minute, probeLimit: 30 * time.Second, status: executor.StatusTimedOut, cleanup: executor.CleanupConfirmed},
		{name: "short configured limit", reason: ReasonVersionTimeout, failedSpec: 1, limit: 12 * time.Second, probeLimit: 12 * time.Second, status: executor.StatusTimedOut, cleanup: executor.CleanupConfirmed},
		{name: "model timeout", reason: "model_timeout", version: Version, failedSpec: 2, limit: time.Minute, probeLimit: 30 * time.Second, status: executor.StatusTimedOut, cleanup: executor.CleanupConfirmed},
		{name: "cancelled version probe", reason: "cancelled", failedSpec: 1, limit: time.Minute, probeLimit: 30 * time.Second, status: executor.StatusCancelled, cleanup: executor.CleanupConfirmed},
		{name: "version executable cannot start", reason: "model_unavailable", failedSpec: 1, limit: time.Minute, probeLimit: 30 * time.Second, status: executor.StatusStartFailed, cleanup: executor.CleanupNotStarted},
		{name: "unconfirmed timeout cleanup", reason: "recovery_required", failedSpec: 1, limit: time.Minute, probeLimit: 30 * time.Second, status: executor.StatusTimedOut, cleanup: executor.CleanupUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := t.TempDir()
			cfg := Config{Executable: filepath.Join(scratch, "opencode"), Model: "opencode/fixture", Timeout: tc.limit}
			q := Request{Scratch: scratch, SnapshotDigest: strings.Repeat("0", 64), Role: "investigate", Input: json.RawMessage(`{}`)}
			var workdir string
			calls := 0
			reply := cfg.Review(t.Context(), runnerFunc(func(_ context.Context, spec executor.Spec) executor.Result {
				calls++
				workdir = spec.Dir
				switch calls {
				case 1:
					if !reflect.DeepEqual(spec.Args, []string{"--version"}) || spec.Timeout != tc.probeLimit || len(spec.Stdin) != 0 {
						t.Fatalf("unexpected version probe: %v, timeout %v", spec.Args, spec.Timeout)
					}
					if tc.failedSpec == 2 {
						return successful(Version + "\n")
					}
				case 2:
					if spec.Args[0] != "run" || spec.Timeout != tc.limit || len(spec.Stdin) == 0 {
						t.Fatalf("unexpected model invocation: %v, timeout %v", spec.Args, spec.Timeout)
					}
				}
				return executor.Result{Status: tc.status, Cleanup: tc.cleanup}
			}), q)
			if reply.Reason != tc.reason || reply.Version != tc.version || reply.Cleanup != tc.cleanup || reply.Usage != nil || calls != tc.failedSpec {
				t.Fatalf("%d process invocations, reply %+v", calls, reply)
			}
			if _, err := os.Stat(workdir); (tc.cleanup == executor.CleanupUncertain && err != nil) ||
				(tc.cleanup != executor.CleanupUncertain && !os.IsNotExist(err)) {
				t.Fatalf("unexpected workspace state for %s: %v", tc.cleanup, err)
			}
		})
	}
}

func TestReviewVersionProbeDeadlineKeepsCallerCancellationSeparate(t *testing.T) {
	for _, tc := range []struct {
		name, runnerReason, reason string
		callerDeadline             bool
	}{
		{name: "review deadline during probe", runnerReason: "context_deadline", reason: ReasonVersionTimeout},
		{name: "review deadline before probe", runnerReason: "cancelled_before_start", reason: "cancelled"},
		{name: "caller deadline", runnerReason: "context_deadline", reason: "cancelled", callerDeadline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const shortLimit = 30 * time.Millisecond
			ctx := t.Context()
			scratch := t.TempDir()
			cfg := Config{Executable: filepath.Join(scratch, "opencode"), Model: "opencode/fixture", Timeout: shortLimit}
			if tc.callerDeadline {
				cfg.Timeout = time.Minute
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, shortLimit)
				defer cancel()
			}
			q := Request{Scratch: scratch, SnapshotDigest: strings.Repeat("0", 64), Role: "probe", Input: json.RawMessage(`{}`)}
			reply := cfg.Review(ctx, runnerFunc(func(ctx context.Context, spec executor.Spec) executor.Result {
				if !reflect.DeepEqual(spec.Args, []string{"--version"}) {
					t.Fatalf("unexpected call after version probe failed: %v", spec.Args)
				}
				<-ctx.Done()
				return executor.Result{Status: executor.StatusCancelled, Reason: tc.runnerReason, Cleanup: executor.CleanupConfirmed}
			}), q)
			if reply.Reason != tc.reason || reply.Version != "" || reply.Usage != nil {
				t.Fatalf("unexpected reply: %+v", reply)
			}
		})
	}
}

func TestEventGates(t *testing.T) {
	valid := events(`{"workspace_proof":"proof","result":{}}`)
	cases := map[string]string{
		events(`{"workspace_proof":"other","result":{}}`):                 reasonProofMismatch,
		events(`{"workspace_proof":"proof","result":{"copy":"proof"}}`):   "model_output_invalid",
		events(`{"workspace_proof":"proof","result":"ok"}`):               "model_output_invalid",
		events(`{"workspace_proof":"proof","result":{},"result":{}}`):     "model_output_invalid",
		strings.Replace(valid, `"reason":"stop"`, `"reason":"length"`, 1): "model_output_incomplete",
		strings.Replace(valid, `"type":"text"`, `"type":"tool_use"`, 2):   "policy_blocked",
		valid + "oops": "model_output_invalid",
		"":             "model_output_incomplete",
	}
	for stream, want := range cases {
		out, _, got := collect([]byte(stream), "proof")
		if got != want || out != nil {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func writeCredential(t *testing.T, dir string, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, "model.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// The credential is read at every call, from either a CODEYE credential or
// an OpenCode auth.json entry of type api; a missing, shared or mismatched
// file makes no request.
func TestCredentialReadAtCallTime(t *testing.T) {
	scratch := t.TempDir()
	dir := t.TempDir()
	var seen []*executor.ModelProvider
	runner := runnerFunc(func(_ context.Context, s executor.Spec) executor.Result {
		if reflect.DeepEqual(s.Args, []string{"--version"}) {
			seen = append(seen, s.OpenCodeProvider)
			if s.OpenCodeProvider == nil || !slicesContain(s.Redact, s.OpenCodeProvider.APIKey) {
				t.Fatal("key not masked")
			}
			return successful(Version + "\n")
		}
		proof, _ := os.ReadFile(s.Args[len(s.Args)-1])
		return successful(events(`{"workspace_proof":"` + strings.TrimSpace(string(proof)) + `","result":{"sum":5}}`))
	})
	q := Request{Scratch: scratch, SnapshotDigest: strings.Repeat("1", 64), Role: "probe", Input: json.RawMessage(`{}`)}
	path := writeCredential(t, dir, `{"schema_version":"0.1","provider":"openai","api_key":"sk-one","base_url":"http://127.0.0.1:9/v1"}`, 0o600)
	cfg := Config{Executable: filepath.Join(dir, "opencode"), Model: "openai/gpt-6.2-sol", CredentialFile: path, Timeout: time.Minute}
	if r := cfg.Review(t.Context(), runner, q); r.Reason != "" || seen[0].APIKey != "sk-one" || seen[0].BaseURL != "http://127.0.0.1:9/v1" {
		t.Fatalf("%+v", r)
	}
	writeCredential(t, dir, `{"schema_version":"0.1","provider":"openai","api_key":"sk-two"}`, 0o600)
	if r := cfg.Review(t.Context(), runner, q); r.Reason != "" || seen[1].APIKey != "sk-two" {
		t.Fatal("key not re-read")
	}
	auth := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(auth, []byte(`{"openai":{"type":"api","key":"sk-auth"},"anthropic":{"type":"oauth","access":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.CredentialFile = auth
	if r := cfg.Review(t.Context(), runner, q); r.Reason != "" || seen[2].APIKey != "sk-auth" {
		t.Fatal("auth.json entry")
	}
	before := len(seen)
	for name, c := range map[string]Config{
		"oauth entry":      {Executable: cfg.Executable, Model: "anthropic/claude-opus-5-5", CredentialFile: auth, Timeout: time.Minute},
		"missing file":     {Executable: cfg.Executable, Model: cfg.Model, CredentialFile: filepath.Join(dir, "none.json"), Timeout: time.Minute},
		"other provider":   {Executable: cfg.Executable, Model: "anthropic/claude-opus-5-5", CredentialFile: path, Timeout: time.Minute},
		"readable by all":  {Executable: cfg.Executable, Model: cfg.Model, CredentialFile: writeCredential(t, t.TempDir(), `{"schema_version":"0.1","provider":"openai","api_key":"k"}`, 0o644), Timeout: time.Minute},
		"multi-line key":   {Executable: cfg.Executable, Model: cfg.Model, CredentialFile: writeCredential(t, t.TempDir(), `{"schema_version":"0.1","provider":"openai","api_key":"a\nb"}`, 0o600), Timeout: time.Minute},
		"unknown field":    {Executable: cfg.Executable, Model: cfg.Model, CredentialFile: writeCredential(t, t.TempDir(), `{"schema_version":"0.1","provider":"openai","api_key":"k","x":1}`, 0o600), Timeout: time.Minute},
		"gateway with key": {Executable: cfg.Executable, Model: "codeye-gateway/gpt-6.2-sol", CredentialFile: path, Timeout: time.Minute},
	} {
		want := ReasonCredentialUnavailable
		if name == "gateway with key" {
			want = "model_not_configured"
		}
		if runtime.GOOS == "windows" && name == "readable by all" {
			continue
		}
		if r := c.Review(t.Context(), runner, q); r.Reason != want {
			t.Errorf("%s: %s", name, r.Reason)
		}
	}
	if len(seen) != before {
		t.Fatal("a request was made without a usable credential")
	}
}

func slicesContain(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Another OpenCode release runs and is marked untested; an answer that is
// not a release version is still refused.
func TestUntestedVersion(t *testing.T) {
	dir := t.TempDir()
	for version, want := range map[string]string{"1.20.0": "", "2.0.0-beta.1": "", Version: "", "99.0": "opencode_version_unsupported", "opencode": "opencode_version_unsupported"} {
		runner := runnerFunc(func(_ context.Context, s executor.Spec) executor.Result {
			if reflect.DeepEqual(s.Args, []string{"--version"}) {
				return successful(version + "\n")
			}
			proof, _ := os.ReadFile(s.Args[len(s.Args)-1])
			return successful(events(`{"workspace_proof":"` + strings.TrimSpace(string(proof)) + `","result":{}}`))
		})
		cfg := Config{Executable: filepath.Join(dir, "opencode"), Model: "opencode/free", Timeout: time.Minute}
		r := cfg.Review(t.Context(), runner, Request{Scratch: t.TempDir(), SnapshotDigest: strings.Repeat("0", 64), Role: "probe", Input: json.RawMessage(`{}`)})
		if r.Reason != want {
			t.Fatalf("%s: %q", version, r.Reason)
		}
		if want == "" && (r.Version != version || VersionUntested(r.Version) != (version != Version)) {
			t.Fatalf("%s: version %q", version, r.Version)
		}
	}
}

// A failed `opencode run` is classified by its JSON error event.
func TestProviderFailureReasons(t *testing.T) {
	for status, want := range map[int]string{401: ReasonAuthFailed, 403: ReasonAuthFailed, 404: ReasonModelNotFound, 429: "model_rate_limited", 500: "model_unavailable"} {
		line := `{"type":"error","sessionID":"s","error":{"name":"APIError","data":{"message":"x","statusCode":` + strconv.Itoa(status) + `}}}` + "\n"
		if got := providerFailure([]byte(`{"type":"step_start"}` + "\n" + line)); got != want {
			t.Errorf("%d: %s", status, got)
		}
	}
	if got := providerFailure([]byte(`{"type":"error","error":{"name":"ProviderModelNotFoundError","data":{}}}`)); got != ReasonModelNotFound {
		t.Fatal(got)
	}
	if got := providerFailure([]byte("not json")); got != "model_unavailable" {
		t.Fatal(got)
	}
	dir := t.TempDir()
	code := 1
	runner := runnerFunc(func(_ context.Context, s executor.Spec) executor.Result {
		if reflect.DeepEqual(s.Args, []string{"--version"}) {
			return successful(Version + "\n")
		}
		return executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
			Stdout: executor.Stream{Data: []byte(`{"type":"error","sessionID":"s","error":{"name":"APIError","data":{"statusCode":401}}}` + "\n")}}
	})
	cfg := Config{Executable: filepath.Join(dir, "opencode"), Model: "opencode/free", Timeout: time.Minute}
	if r := cfg.Review(t.Context(), runner, Request{Scratch: t.TempDir(), SnapshotDigest: strings.Repeat("0", 64), Role: "probe", Input: json.RawMessage(`{}`)}); r.Reason != ReasonAuthFailed || r.Version != Version {
		t.Fatalf("%+v", r)
	}
}

// Explicit opt-in. CI's offline protocol tests never submit a provider request.
func TestLiveOpenCode(t *testing.T) {
	model := os.Getenv("CODEYE_TEST_OPENCODE_MODEL")
	if model == "" {
		t.Skip("live OpenCode model not requested")
	}
	cfg := Config{Executable: os.Getenv("CODEYE_TEST_OPENCODE_BIN"), Model: model, CredentialFile: os.Getenv("CODEYE_TEST_MODEL_CREDENTIAL"), Timeout: DefaultTimeout}
	q := Request{Scratch: t.TempDir(), SnapshotDigest: strings.Repeat("0", 64), Role: "probe", Instructions: "For this synthetic fixture return the integer sum in result.sum.", Input: json.RawMessage(`{"a":2,"b":3}`)}
	reply := cfg.Review(t.Context(), &executor.Executor{}, q)
	if reply.Reason != "" {
		t.Fatalf("version=%s model=%s reason=%s cleanup=%s", reply.Version, reply.Model, reply.Reason, reply.Cleanup)
	}
	var result struct{ Sum int }
	if err := json.Unmarshal(reply.Output, &result); err != nil || result.Sum != 5 {
		t.Fatal("incorrect synthetic fixture response")
	}
	t.Logf("version=%s model=%s workspace verified, cleanup=%s", reply.Version, reply.Model, reply.Cleanup)
}

// A relay answer that arrives later than OpenCode's own 300 s header and
// chunk waits still reaches the review when the call timeout is longer:
// the managed profile carries the call timeout as both waits. Run it with
// the relay in front of scripts/fake_model_upstream.py --delay-seconds
// (for example 320) and CODEYE_TEST_MODEL_TIMEOUT above the delay (400s).
func TestLiveRelaySlowAnswer(t *testing.T) {
	descriptor, limit := os.Getenv("CODEYE_TEST_REVIEW_RELAY"), os.Getenv("CODEYE_TEST_MODEL_TIMEOUT")
	if descriptor == "" || limit == "" {
		t.Skip("slow relay answer not requested")
	}
	timeout, err := time.ParseDuration(limit)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Executable: os.Getenv("CODEYE_TEST_OPENCODE_BIN"), Model: "codeye-gateway/gpt-6.2-sol", RelayFile: descriptor, Timeout: timeout}
	started := time.Now()
	reply := cfg.Review(t.Context(), &executor.Executor{}, Request{Scratch: t.TempDir(), Role: "probe", SnapshotDigest: strings.Repeat("0", 64),
		Input: json.RawMessage(`{"a":2,"b":3}`), Instructions: "Return the integer sum in result.sum."})
	t.Logf("answered after %s: reason=%q cleanup=%s", time.Since(started).Round(time.Second), reply.Reason, reply.Cleanup)
	var result struct{ Sum int }
	if reply.Reason != "" || json.Unmarshal(reply.Output, &result) != nil || result.Sum != 5 {
		t.Fatalf("slow answer lost: reason=%s output=%s", reply.Reason, reply.Output)
	}
}

// Once the relay has reserved a real upstream request, cancel the client and
// require OS-confirmed cleanup. The relay still accounts for the upstream call.
func TestLiveInFlightCancel(t *testing.T) {
	ledger, descriptor := os.Getenv("CODEYE_TEST_REVIEW_LEDGER"), os.Getenv("CODEYE_TEST_REVIEW_RELAY")
	if ledger == "" || descriptor == "" {
		t.Skip("explicit real in-flight cancellation not enabled")
	}
	before, err := os.Stat(ledger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	submitted := make(chan bool, 1)
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				submitted <- false
				return
			case <-tick.C:
				if st, err := os.Stat(ledger); err == nil && st.Size() > before.Size() {
					submitted <- true
					cancel()
					return
				}
			}
		}
	}()
	cfg := Config{Executable: os.Getenv("CODEYE_TEST_OPENCODE_BIN"), Model: "codeye-gateway/gpt-6.2-sol", RelayFile: descriptor, Timeout: 75 * time.Second}
	reply := cfg.Review(ctx, &executor.Executor{}, Request{Scratch: t.TempDir(), Role: "probe", SnapshotDigest: strings.Repeat("0", 64), Input: json.RawMessage(`{"a":2,"b":3}`), Instructions: "Return a sum."})
	cancel()
	if !<-submitted || reply.Reason != "cancelled" || reply.Cleanup != executor.CleanupConfirmed {
		t.Fatalf("cancel: reason=%s cleanup=%s", reply.Reason, reply.Cleanup)
	}
}
