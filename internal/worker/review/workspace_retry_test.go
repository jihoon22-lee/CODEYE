package review

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type proofCaller func(context.Context, executor.Runner, opencode.Request) opencode.Reply

func (f proofCaller) Review(ctx context.Context, runner executor.Runner, q opencode.Request) opencode.Reply {
	return f(ctx, runner, q)
}

type proofRunner func(context.Context, executor.Spec) executor.Result

func (f proofRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	return f(ctx, spec)
}

type proofFixture struct {
	t                     *testing.T
	dirs, proofs, prompts []string
	cancel                context.CancelFunc
}

// reply exercises the actual workspace setup, event parser and cleanup without
// starting a process or contacting a provider.
func (f *proofFixture) reply(ctx context.Context, q opencode.Request, output json.RawMessage, mode string) opencode.Reply {
	f.t.Helper()
	cfg := opencode.Config{Executable: filepath.Join(q.Scratch, "opencode"), Model: "opencode/fixture", Timeout: time.Second}
	if mode == "local" {
		q.Scratch = filepath.Join(q.Scratch, "missing")
	}
	runner := proofRunner(func(_ context.Context, spec executor.Spec) executor.Result {
		code := 0
		success := func(stdout string) executor.Result {
			return executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, ExitCode: &code,
				Stdout: executor.Stream{Data: []byte(stdout)}}
		}
		if slices.Equal(spec.Args, []string{"--version"}) {
			return success(opencode.Version)
		}
		if spec.WorkspaceRoot != spec.Dir || filepath.Dir(spec.Dir) != q.Scratch || spec.OpenCodeModel != cfg.Model ||
			!slices.Equal(spec.ProtectedRoots, q.ProtectedRoots) || spec.Args[len(spec.Args)-1] != filepath.Join(spec.Dir, "identity.txt") {
			f.t.Fatal("workspace policy changed")
		}
		proof := strings.TrimSpace(string(mustRead(f.t, filepath.Join(spec.Dir, "identity.txt"))))
		if slices.Contains(f.proofs, proof) || slices.Contains(f.dirs, spec.Dir) ||
			bytes.Contains(spec.Stdin, []byte(proof)) || strings.Contains(strings.Join(spec.Args, " "), proof) {
			f.t.Fatal("workspace or proof was reused or disclosed")
		}
		for _, prior := range f.dirs {
			if _, err := os.Stat(prior); !os.IsNotExist(err) {
				f.t.Fatal("previous workspace still exists")
			}
		}
		f.dirs = append(f.dirs, spec.Dir)
		f.proofs = append(f.proofs, proof)
		f.prompts = append(f.prompts, string(spec.Stdin))
		if mode == "provider" {
			return success(`{"type":"error","sessionID":"session","error":{"data":{"statusCode":503}}}`)
		}
		if mode == "cancelled" {
			f.cancel()
			return executor.Result{Status: executor.StatusCancelled, Cleanup: executor.CleanupConfirmed}
		}
		if mode == "mismatch" {
			proof = "different proof"
		}
		var events bytes.Buffer
		enc := json.NewEncoder(&events)
		part := func(kind string, fields map[string]any) {
			fields["sessionID"], fields["messageID"] = "session", "message"
			if err := enc.Encode(map[string]any{"type": kind, "sessionID": "session", "part": fields}); err != nil {
				f.t.Fatal(err)
			}
		}
		part("step_start", map[string]any{})
		packet := asJSON(map[string]any{"workspace_proof": proof, "result": output})
		part("text", map[string]any{"text": string(packet.Output)})
		part("step_finish", map[string]any{"reason": "stop", "tokens": map[string]int{"input": 20, "output": 10}})
		return success(events.String())
	})
	reply := cfg.Review(ctx, runner, q)
	if mode == "mismatch" && (!reply.WorkspaceProofMismatch() || reply.Output != nil || reply.Usage == nil) {
		f.t.Fatal("unverified output was retained or usage was lost")
	}
	if mode != "mismatch" && reply.WorkspaceProofMismatch() {
		f.t.Fatal("unrelated failure misclassified as a mismatched proof")
	}
	return reply
}

func TestContractWorkspaceProofRetry(t *testing.T) {
	for _, test := range []struct {
		name, mode, reason string
		remaining, calls   int
		status             domain.TaskStatus
	}{
		{"recover", "mismatch", "", 2, 2, domain.TaskSucceeded},
		{"persistent", "mismatch", "workspace_unreachable", 3, 2, domain.TaskFailed},
		{"no budget", "mismatch", reasonBudgetExhausted, 1, 1, domain.TaskBudgetExhausted},
		{"local failure", "local", "workspace_unreachable", 2, 1, domain.TaskFailed},
		{"provider failure", "provider", "model_unavailable", 2, 1, domain.TaskFailed},
		{"cancel after mismatch", "mismatch", "cancelled", 2, 1, domain.TaskCancelled},
		{"cancel during retry", "mismatch", "cancelled", 2, 2, domain.TaskCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			j := contractExampleJob(t, nil)
			for _, stage := range []string{record.StageDocuments, record.StageInvestigate} {
				var compact bytes.Buffer
				path := filepath.Join(exampleDir, "contract", stage+".json")
				if err := json.Compact(&compact, mustRead(t, path)); err != nil {
					t.Fatal(err)
				}
				writeFile(t, j.Scratch, filepath.Base(recordPath(j, stage)), compact.String())
			}
			usedBefore := record.WholeRunMaxCalls - test.remaining
			writeFile(t, j.Scratch, "review.calls", strings.Repeat("review.verify\n", usedBefore))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture := proofFixture{t: t, cancel: cancel}
			calls := 0
			caller := proofCaller(func(ctx context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
				calls++
				comparison := exampleContractReply(t, q)
				mode := test.mode
				if calls == 2 && test.name == "recover" {
					mode = ""
				}
				if calls == 2 && test.name == "cancel during retry" {
					mode = "cancelled"
				}
				if mode == "mismatch" {
					comparison.Reason = "검증하지 않은 첫 응답이다."
				}
				out := asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{comparison}})
				reply := fixture.reply(ctx, q, out.Output, mode)
				if test.name == "cancel after mismatch" {
					cancel()
				}
				return reply
			})
			h := Handler{Static: vetStub{}, Caller: caller, MaxCalls: record.WholeRunMaxCalls}
			res := h.Run(ctx, j, analysis.TaskSpec{Key: record.TaskContract}, nil)
			used, err := callsUsed(j)
			if res.Status != test.status || res.Reason != test.reason || calls != test.calls || used != usedBefore+test.calls || err != nil {
				t.Fatalf("status %s reason %q calls %d used %d err %v", res.Status, res.Reason, calls, used, err)
			}
			if len(fixture.prompts) == 2 && fixture.prompts[0] != fixture.prompts[1] {
				t.Fatal("proof retry changed the pending request")
			}
			r := readRecord(t, j, record.StageContract)
			if len(r.Calls) != test.calls || r.Accepted() != (test.status == domain.TaskSucceeded) {
				t.Fatal("invalid call record")
			}
			if test.mode == "mismatch" && (r.Calls[0].Outcome != "workspace_unreachable" || r.Calls[0].Usage == nil || r.Calls[0].Usage.Output != 10) {
				t.Fatal("mismatched reply was not accounted")
			}
			if test.name == "recover" && (len(r.ContractComparisons) != 1 || r.Calls[1].Outcome != callAccepted ||
				r.Calls[0].RequestDigest != r.Calls[1].RequestDigest) {
				t.Fatal("verified reply did not replace the discarded output")
			}
			raw := string(mustRead(t, recordPath(j, record.StageContract)))
			if strings.Contains(raw, "검증하지 않은 첫 응답") || slices.ContainsFunc(fixture.proofs, func(proof string) bool { return strings.Contains(raw, proof) }) {
				t.Fatal("discarded result or workspace proof was stored")
			}
		})
	}
}

func TestVerifyWorkspaceProofRetryKeepsPending(t *testing.T) {
	j := modulesJob(t, nil)
	f := &scriptedCaller{script: validScript(t)}
	fixture := proofFixture{t: t}
	f.script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []map[string]any
		for _, c := range in.Candidates {
			if n == 1 && c.Origin == "interpret" {
				out = append(out, inconclusiveFor(c, "ev_000000000000000000000000"))
			} else {
				out = append(out, inconclusiveFor(c, c.Evidence[0]))
			}
		}
		if n > 1 && (len(in.Candidates) != 1 || in.Candidates[0].Origin != "interpret" || in.Repair == nil) {
			t.Fatal("verifier did not preserve the pending candidates and repair")
		}
		mode := ""
		if n == 2 {
			mode = "mismatch"
		}
		return fixture.reply(t.Context(), q, asJSON(map[string]any{"verifications": out}).Output, mode)
	}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	r := readRecord(t, j, record.StageVerify)
	if res["review.verify"].Status != domain.TaskSucceeded || f.calls("verify") != 3 || len(r.Calls) != 3 ||
		r.Calls[0].Outcome != callAccepted || r.Calls[1].Outcome != "workspace_unreachable" || r.Calls[2].Outcome != callAccepted ||
		!reflect.DeepEqual(r.Calls[1].Usage, &record.Usage{Input: 20, Output: 10}) || fixture.prompts[1] != fixture.prompts[2] {
		t.Fatalf("verification failed to keep the first verdict and retry the pending one: %+v", r.Calls)
	}
	for _, v := range r.Verifications {
		if v.Verdict != record.VerdictInconclusive {
			t.Fatal("verification lost an accepted verdict")
		}
	}
}
