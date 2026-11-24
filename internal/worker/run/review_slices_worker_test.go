//go:build linux

package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// cleanScreen is a static composite with one secrets task that completes a
// scan with no detection, so the review slices may run.
type cleanScreen struct{}

func (cleanScreen) Descriptors() []pack.Descriptor {
	return []pack.Descriptor{{ID: "any.gitl" + "eaks", Name: "secrets", Purpose: obsdoc.PurposeSecrets}}
}

func (cleanScreen) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: pack.CompositeName, Version: "stub", Capability: pack.Capability}
}

func (cleanScreen) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "secrets", Title: "secrets", Stage: domain.StageStaticAnalysis}}, nil
}

func (cleanScreen) Run(_ context.Context, j analysis.Job, t analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	id := "any.gitl" + "eaks"
	doc := obsdoc.New(obsdoc.Analyzer{ID: id, Ecosystem: "any", Purpose: obsdoc.PurposeSecrets}, obsdoc.Component{ID: t.Key, Version: "1"}, obsdoc.Component{ID: t.Key + "-stub", Version: "1"}, t.Key, j.Snapshot.Digest)
	doc.Module.Dir, doc.Tool.Name = ".", t.Key
	version := "1.0.0"
	doc.Tool.Version, doc.Tool.VersionTested = &version, true
	doc.RulePack = obsdoc.RulePack{ID: "stub", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored,
		ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	stdout := []byte("[]\n")
	code := 0
	doc.Executions = []obsdoc.Execution{{Name: t.Key + ".run", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Command: analysis.ManifestCommand{Argv: []string{t.Key}, Dir: ".", EnvKeys: []string{}},
		Stdout:  obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(stdout), CapturedBytes: int64(len(stdout)), TotalBytes: int64(len(stdout))}, Stderr: obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}}}
	doc.Observations, doc.Coverage.ProcessedPaths = []obsdoc.Observation{}, []string{}
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" {
			doc.Coverage.ProcessedPaths = append(doc.Coverage.ProcessedPaths, e.Path)
		}
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithoutFindings}
	doc.Parse.OK = true
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}}
}

// sliceScripted answers environment (as roleScripted), documents, investigate (citing its first excerpt)
// and verify validly. With block, the second investigate request (the
// first slice) waits for cancellation.
type sliceScripted struct {
	mu          sync.Mutex
	roles       []string
	investigate int
	block       bool
	blocked     chan struct{}
}

func (f *sliceScripted) Review(ctx context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
	f.mu.Lock()
	f.roles = append(f.roles, q.Role)
	if q.Role == "investigate" {
		f.investigate++
	}
	n := f.investigate
	f.mu.Unlock()
	reply := opencode.Reply{Model: "fake/scripted", Version: opencode.Version, RequestDigest: domain.Digest([]byte(opencode.Prompt(q))), Usage: &opencode.Usage{Input: int64(len(q.Input)), Output: 40}}
	if f.block && q.Role == "investigate" && n == 2 {
		close(f.blocked)
		<-ctx.Done()
		reply.Reason = "cancelled"
		return reply
	}
	if q.Role == "environment" {
		return (&roleScripted{}).Review(ctx, nil, q)
	}
	var in struct {
		Scope struct {
			Excerpts []struct {
				ID         string
				Start, End int
			} `json:"excerpts"`
		} `json:"scope"`
		Candidates []struct {
			ID       string   `json:"id"`
			Evidence []string `json:"evidence_ids"`
		} `json:"candidates"`
	}
	if json.Unmarshal(q.Input, &in) != nil {
		reply.Reason = "invalid_review_input"
		return reply
	}
	var body any
	switch q.Role {
	case "documents":
		body = map[string]any{"contracts": []any{}, "conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}}
	case "investigate":
		e := in.Scope.Excerpts[0]
		id := e.ID
		if e.End-e.Start+1 > 80 {
			id = fmt.Sprintf("%s:%d-%d", e.ID, e.Start, e.Start+79)
		}
		body = map[string]any{"candidates": []map[string]any{{"title": "입력 검증 경계가 불분명함", "condition": "빈 입력이 들어오는 경우", "mechanism": "빈 입력이 검사 없이 계산에 쓰인다.", "impact": "잘못된 결과를 반환할 수 있다.",
			"improvement": "입력 계약을 확인한다.", "counterpoint": "호출자가 미리 검사할 수 있다.", "evidence_ids": []string{id}}}}
	case "verify":
		var out []map[string]any
		for _, c := range in.Candidates {
			out = append(out, map[string]any{"candidate_id": c.ID, "verdict": "inconclusive", "category": "error_handling", "severity": "low", "severity_reason": "영향 범위가 제한적이다.",
				"impact": "잘못된 결과가 나올 수 있다.", "valid_conditions": []string{}, "invalid_conditions": []string{}, "supporting_evidence_ids": c.Evidence[:1], "counterevidence_ids": []string{},
				"unresolved_assumptions": []string{"호출자를 발췌에서 확인하지 못했다."}, "next_verification": []string{"호출자를 읽는다."}})
		}
		body = map[string]any{"verifications": out}
	default:
		reply.Reason = "model_unavailable"
		return reply
	}
	reply.Output, _ = json.Marshal(body)
	return reply
}

func copyCoverageFixture(t *testing.T, dst string) {
	t.Helper()
	fixture, _ := filepath.Abs("../../../fixtures/modules/slices/input")
	err := filepath.WalkDir(fixture, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// runSlices runs the coverage fixture through the real worker with the
// review at the given ceiling and returns the completion, the result
// manifest's task rows and the agent.
func runSlices(t *testing.T, caller *sliceScripted, maxCalls int, cancelOnBlock bool) (*workerproto.Complete, []string, *fakeAgent, analysis.ResultManifest) {
	t.Helper()
	e := newEnv(t)
	if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
		t.Fatal(err)
	}
	copyCoverageFixture(t, e.src)
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	// The smallest request budget: the fixture then needs slices.
	small := review.Budget{RequestBytes: review.MinRequestBytes, OutputTokens: review.MinOutputTokens}
	h, err := discovery.Wrap(review.Handler{Static: cleanScreen{}, Caller: caller, MaxCalls: maxCalls, Budget: small}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe}})
	if err != nil {
		t.Fatal(err)
	}
	fa, in, done, _ := startWorker(t, h)
	t.Cleanup(func() {
		in.Close()
		if code := <-done; code != 0 {
			t.Errorf("worker exit %d", code)
		}
	})
	fa.hello(e)
	job := localJob(e)
	job.AllowModelReview = true
	if err := fa.conn.Send(job); err != nil {
		t.Fatal(err)
	}
	if cancelOnBlock {
		select {
		case <-caller.blocked:
		case <-time.After(60 * time.Second):
			t.Fatalf("slice request not made; messages %v", fa.kinds())
		}
		if err := fa.conn.Send(&workerproto.Cancel{Reason: workerproto.CancelRunRequested}); err != nil {
			t.Fatal(err)
		}
	}
	var c *workerproto.Complete
	select {
	case c = <-fa.complete:
	case <-time.After(90 * time.Second):
		t.Fatalf("no complete; messages %v", fa.kinds())
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var rows []string
	var m analysis.ResultManifest
	if c.ResultManifestClientArtifact != nil {
		if err := json.Unmarshal(fa.artifacts[*c.ResultManifestClientArtifact], &m); err != nil {
			t.Fatal(err)
		}
		for _, task := range m.Tasks {
			row := fmt.Sprintf("%s=%s/%v", task.Key, task.Status, task.Required)
			if task.Reason != nil {
				row += ":" + *task.Reason
			}
			rows = append(rows, row)
		}
	}
	return c, rows, fa, m
}

// TC-168 (docs/REVIEW.md#모듈-단위-검토-분할): the slices through the real
// worker. At the minimum ceiling the attempt makes at most 15 calls; the
// slots it could not admit end budget_exhausted, slots review.scope left
// empty end skipped_policy (no_review_scope), the attempt completes, the
// slices are optional so the required review tasks alone decide the AI
// review, and the report builds and validates. Cancelling during a slice
// request cancels that slice and every later task and confirms cleanup.
func TestReviewSlicesThroughTheWorker(t *testing.T) {
	t.Run("ceiling", func(t *testing.T) {
		caller := &sliceScripted{}
		c, rows, fa, m := runSlices(t, caller, 15, false)
		if c.Outcome != "succeeded" || c.CleanupState != "confirmed" {
			t.Fatalf("completion %+v rows %v", c, rows)
		}
		joined := strings.Join(rows, " ")
		for _, want := range []string{"review.investigate=succeeded/true", "review.verify=succeeded/true", "review.scope=succeeded/false",
			"review.investigate.s001=succeeded/false", "review.verify.s001=succeeded/false",
			"review.investigate.s006=budget_exhausted/false:review_budget_exhausted", "review.verify.s006=budget_exhausted/false:review_budget_exhausted",
			"review.investigate.s010=skipped_policy/false:no_review_scope", "review.verify.s010=skipped_policy/false:no_review_scope"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %s in %v", want, rows)
			}
		}
		caller.mu.Lock()
		calls := len(caller.roles)
		caller.mu.Unlock()
		if calls > 15 {
			t.Fatalf("%d calls", calls)
		}
		r, err := report.Build(reportInput(t, fa, m, *c.ResultManifestClientArtifact), report.DefaultRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if err := report.ValidateReport(r); err != nil {
			t.Fatal(err)
		}
		ledger := r.CodeReview.Ledger
		if ledger == nil || len(ledger.Files) == 0 || ledger.PrimarySlotsBudgetExhausted == 0 {
			t.Fatalf("missing file or budget information in ledger: %+v", ledger)
		}
		t.Logf("%d calls; report %s, ai review %s, limitations %v", calls, r.Status, r.Coverage.AIReview.Status, r.Limitations)
		if r.Coverage.AIReview.Status != report.AIReviewPerformed {
			t.Fatalf("ai review %s", r.Coverage.AIReview.Status)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		caller := &sliceScripted{block: true, blocked: make(chan struct{})}
		c, rows, _, _ := runSlices(t, caller, 33, true)
		if c.Outcome != "cancelled" || c.CleanupState != "confirmed" {
			t.Fatalf("completion %+v rows %v", c, rows)
		}
		caller.mu.Lock()
		defer caller.mu.Unlock()
		if caller.investigate != 2 {
			t.Fatalf("investigate calls %d after cancel", caller.investigate)
		}
	})
}
