package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	q := Request{Scratch: scratch, SnapshotDigest: strings.Repeat("1", 64), Role: "context", Instructions: "Return the sum.", Input: json.RawMessage(`{"a":2,"b":3}`)}
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

func TestEventGates(t *testing.T) {
	valid := events(`{"workspace_proof":"proof","result":{}}`)
	cases := map[string]string{
		events(`{"workspace_proof":"other","result":{}}`):                 "workspace_unreachable",
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

// Explicit opt-in. CI's offline protocol tests never submit a provider request.
func TestLiveOpenCode(t *testing.T) {
	model := os.Getenv("CODEYE_TEST_OPENCODE_MODEL")
	if model == "" {
		t.Skip("live OpenCode model not requested")
	}
	cfg := Config{Executable: os.Getenv("CODEYE_TEST_OPENCODE_BIN"), Model: model, Timeout: 75 * time.Second}
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
