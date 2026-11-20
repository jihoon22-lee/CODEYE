//go:build linux

package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Only the model and agent transport are scripted. The worker acquires a
// snapshot, runs the actual Go toolchain and emits its ordinary result
// manifest. Every role answers as roleScripted does; verify times out.
type timeoutVerifier struct{ roleScripted }

func (v *timeoutVerifier) Review(ctx context.Context, r executor.Runner, q opencode.Request) opencode.Reply {
	if q.Role != "verify" {
		return v.roleScripted.Review(ctx, r, q)
	}
	return opencode.Reply{Model: "fake/scripted", Version: opencode.Version, RequestDigest: domain.Digest([]byte(opencode.Prompt(q))), Reason: "model_timeout"}
}

func TestReviewTimeoutPreservesStaticObservationsInPartialManifest(t *testing.T) {
	e := newEnv(t)
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(e.src, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.invalid/sum\n\ngo 1.23\n")
	write("main.go", "package main\n\nfunc Sum(a,b int) int { return a+b }\nfunc main() {}\n")
	h, err := govet.New(govet.Config{GoExecutable: filepath.Join(runtime.GOROOT(), "bin", "go")})
	if err != nil {
		t.Fatal(err)
	}
	fa, in, done, _ := startWorker(t, review.Handler{Static: h, Caller: &timeoutVerifier{}})
	defer func() {
		in.Close()
		if code := <-done; code != 0 {
			t.Errorf("worker exit %d", code)
		}
	}()
	fa.hello(e)
	job := localJob(e)
	job.AllowModelReview = true
	if err := fa.conn.Send(job); err != nil {
		t.Fatal(err)
	}
	c := waitComplete(t, fa)
	if c.Outcome != "partial" || c.ResultManifestClientArtifact == nil || c.CleanupState != "confirmed" {
		t.Fatalf("completion: %+v", c)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(fa.artifacts[*c.ResultManifestClientArtifact], &m); err != nil {
		t.Fatal(err)
	}
	status := map[string]analysis.ManifestTask{}
	for _, task := range m.Tasks {
		status[task.Key] = task
	}
	if vet, inv, ver := status["go-vet"], status["review.investigate"], status["review.verify"]; vet.Status != "succeeded" || inv.Status != "succeeded" ||
		ver.Status != "timed_out" || ver.Reason == nil || *ver.Reason != "model_timeout" {
		t.Fatalf("task statuses: %+v", m.Tasks)
	}
	var static, failed bool
	for id, body := range fa.artifacts {
		if fa.roles[id] == "observations" {
			var o struct {
				Outcome struct {
					Status string `json:"task_status"`
				} `json:"outcome"`
			}
			static = json.Unmarshal(body, &o) == nil && o.Outcome.Status == "succeeded"
		}
		if fa.roles[id] == "other" {
			var r record.Record
			if json.Unmarshal(body, &r) == nil && r.Stage == "verify" {
				failed = !r.Accepted() && r.FailureReason == "model_timeout" && len(r.Verifications) == len(r.Candidates) && len(r.Verifications) > 0 &&
					r.Verifications[0].Verdict == record.VerdictUnreviewed
			}
		}
	}
	if !static || !failed {
		t.Fatalf("uploaded static observations=%v and timeout record=%v", static, failed)
	}
}
