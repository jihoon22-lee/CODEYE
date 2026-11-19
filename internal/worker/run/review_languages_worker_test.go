//go:build linux

package run

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// legacyScripted answers the three legacy-pack roles from the request input alone
// (first excerpt, first code excerpt, every candidate). It never starts a
// process or contacts a model.
type legacyScripted struct{}

func (legacyScripted) Review(_ context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
	reply := opencode.Reply{Model: "fake/scripted", Version: opencode.Version, RequestDigest: domain.Digest([]byte(opencode.Prompt(q))), Usage: &opencode.Usage{Input: int64(len(q.Input)), Output: 40}}
	var in struct {
		Scope struct {
			Excerpts []struct{ ID, Kind string } `json:"excerpts"`
		} `json:"scope"`
		Candidates []struct {
			ID       string   `json:"id"`
			Evidence []string `json:"evidence_ids"`
		} `json:"candidates"`
	}
	if json.Unmarshal(q.Input, &in) != nil || len(in.Scope.Excerpts) == 0 {
		reply.Reason = "invalid_review_input"
		return reply
	}
	code := ""
	for _, e := range in.Scope.Excerpts {
		if code == "" && (e.Kind == "code" || e.Kind == "test") {
			code = e.ID
		}
	}
	var body any
	switch q.Role {
	case "context":
		body = map[string]any{"facts": []any{map[string]any{"kind": "observed", "statement": "저장소는 JSON 파일에 값을 쓴다.", "evidence_ids": []string{in.Scope.Excerpts[0].ID}}}}
	case "investigate":
		body = map[string]any{"candidates": []any{map[string]any{"title": "없는 키 조회가 예외를 낸다", "condition": "없는 키를 조회하는 경우", "impact": "문서와 달리 None 대신 예외가 난다.",
			"improvement": "조회 계약을 문서와 맞춘다.", "counterpoint": "호출자가 예외를 기대할 수 있다.", "evidence_ids": []string{code}}}}
	default:
		var out []any
		for _, c := range in.Candidates {
			out = append(out, map[string]any{"candidate_id": c.ID, "verdict": "inconclusive", "category": "api_contract", "severity": "low", "severity_reason": "영향이 조회 경로에 한정된다.",
				"impact": "호출자가 예외를 처리하지 않으면 중단된다.", "valid_conditions": []string{}, "invalid_conditions": []string{}, "supporting_evidence_ids": c.Evidence[:1], "counterevidence_ids": []string{},
				"unresolved_assumptions": []string{"호출자의 예외 처리를 확인하지 못했다."}, "next_verification": []string{"조회 호출자를 읽는다."}})
		}
		body = map[string]any{"verifications": out}
	}
	reply.Output, _ = json.Marshal(body)
	return reply
}

// Review inputs of other languages through the real worker and the report
// builder: a Python-only
// snapshot (fixtures/review-inputs/python) with the production analyzer
// composite (no tool configured) and a scripted model. Both packs send
// Python code to investigate and verify, and the report built from the
// uploaded artifacts with the default registry accepts every review record
// (no lineage or decode limitation) and cites Python evidence.
func TestReviewInputsOfPythonThroughTheWorkerAndReport(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	for _, packName := range []string{review.PackLegacy, review.PackRole} {
		t.Run(packName, func(t *testing.T) {
			e := newEnv(t)
			if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../fixtures/review-inputs/python")
			err := filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(fixture, p)
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(filepath.Join(e.src, rel)), 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(e.src, rel), b, 0o600)
			})
			if err != nil {
				t.Fatal(err)
			}
			cfg := builtin.Config{GoExecutable: goExe, Tools: map[string]string{}, ProbeDir: t.TempDir()}
			static, _, err := builtin.Composite(context.Background(), cfg, &executor.Executor{})
			if err != nil {
				t.Fatal(err)
			}
			var inner analysis.Handler = review.Handler{Static: static, Caller: legacyScripted{}}
			if packName == review.PackRole {
				inner = review.RoleHandler{Static: static, Caller: &roleScripted{}}
			}
			h, err := discovery.Wrap(inner, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe, Analyzers: builtin.Probes(cfg)}})
			if err != nil {
				t.Fatal(err)
			}
			fa, in, done, _ := startWorker(t, h)
			defer func() {
				in.Close()
				if code := <-done; code != 0 {
					t.Errorf("worker exit %d", code)
				}
			}()
			stop := make(chan struct{})
			defer close(stop)
			go func() {
				for {
					select {
					case <-fa.started:
					case <-stop:
						return
					}
				}
			}()
			fa.hello(e)
			job := localJob(e)
			job.AllowModelReview = true
			if err := fa.conn.Send(job); err != nil {
				t.Fatal(err)
			}
			var c *workerproto.Complete
			select {
			case c = <-fa.complete:
			case <-time.After(90 * time.Second):
				t.Fatalf("no complete; messages %v", fa.kinds())
			}
			if c.ResultManifestClientArtifact == nil || c.CleanupState != "confirmed" {
				t.Fatalf("completion: %+v", c)
			}
			fa.mu.Lock()
			defer fa.mu.Unlock()
			var m analysis.ResultManifest
			if err := json.Unmarshal(fa.artifacts[*c.ResultManifestClientArtifact], &m); err != nil {
				t.Fatal(err)
			}
			status := map[string]string{}
			for _, task := range m.Tasks {
				status[task.Key] = string(task.Status)
			}
			for _, key := range []string{"review.investigate", "review.verify"} {
				if status[key] != "succeeded" {
					t.Fatalf("%s %s; tasks %v", key, status[key], status)
				}
			}
			r, err := report.Build(reportInput(t, fa, m, *c.ResultManifestClientArtifact), report.DefaultRegistry())
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range r.Limitations {
				if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, report.LimReviewPackMismatch) || strings.HasPrefix(l, "analysis_artifact_") {
					t.Fatalf("limitation %s in %v", l, r.Limitations)
				}
			}
			if r.Coverage.AIReview.Status != report.AIReviewPerformed {
				t.Fatalf("ai review %+v limitations %v", r.Coverage.AIReview, r.Limitations)
			}
			python := false
			for _, f := range r.Findings {
				if !strings.HasPrefix(f.ID, "review_") && !strings.HasPrefix(f.ID, "candidate") {
					continue
				}
				for _, ev := range f.Evidence {
					python = python || (ev.Location != nil && strings.HasSuffix(ev.Location.Path, ".py"))
				}
			}
			if !python {
				t.Fatalf("no review finding cites Python evidence: %+v", r.Findings)
			}
		})
	}
}
