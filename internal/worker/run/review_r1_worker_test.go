//go:build linux

package run

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// r1Scripted answers every review-r1 role from the request input alone
// (first gap requirement, first code excerpt, every candidate). Only the
// model is scripted; it never starts a process or contacts a model.
type r1Scripted struct {
	mu    sync.Mutex
	roles []string
}

func (f *r1Scripted) Review(_ context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
	f.mu.Lock()
	f.roles = append(f.roles, q.Role)
	f.mu.Unlock()
	reply := opencode.Reply{Model: "fake/scripted", Version: opencode.Version, RequestDigest: domain.Digest([]byte(opencode.Prompt(q))), Usage: &opencode.Usage{Input: int64(len(q.Input)), Output: 40}}
	var in struct {
		GapModules   []string `json:"gap_modules"`
		Requirements []struct {
			ID, Module string
			EvidenceID string `json:"evidence_id"`
		} `json:"requirements"`
		Scope struct {
			Excerpts []struct{ ID, Kind string } `json:"excerpts"`
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
	case "environment":
		var ids, ev []string
		for _, r := range in.Requirements {
			if r.Module == in.GapModules[0] {
				ids, ev = append(ids, r.ID), append(ev, r.EvidenceID)
			}
		}
		body = map[string]any{"notes": []map[string]any{{"module": in.GapModules[0], "requirement_ids": ids, "evidence_ids": ev,
			"affected_checks": "이 모듈의 go vet은 노드 toolchain으로 수행할 수 없다.", "still_checkable": "다른 모듈의 검사는 가능하다.", "needed_observation": "선언 버전 이상의 toolchain 관측"}}}
	case "documents":
		body = map[string]any{"contracts": []any{}, "conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}}
	case "investigate":
		code := ""
		for _, e := range in.Scope.Excerpts {
			if e.Kind == "code" && code == "" {
				code = e.ID
			}
		}
		body = map[string]any{"candidates": []map[string]any{{"title": "재시도 경계가 불분명함", "condition": "하위 호출이 계속 실패하는 경우", "impact": "호출이 반복될 수 있다.",
			"improvement": "재시도 한도를 문서와 맞춘다.", "counterpoint": "호출자가 한도를 보장할 수 있다.", "evidence_ids": []string{code}}}}
	case "verify":
		var out []map[string]any
		for _, c := range in.Candidates {
			out = append(out, map[string]any{"candidate_id": c.ID, "verdict": "inconclusive", "category": "error_handling", "severity": "low", "severity_reason": "영향 범위가 제한적이다.",
				"impact": "실패 시 호출이 반복될 수 있다.", "valid_conditions": []string{}, "invalid_conditions": []string{}, "supporting_evidence_ids": c.Evidence[:1], "counterevidence_ids": []string{},
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

func copyW15(t *testing.T, dst string) {
	t.Helper()
	fixture, _ := filepath.Abs("../../../fixtures/w15-modules/input")
	err := filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
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

// reportInput assembles the publisher's report input from what the fake
// agent received: the result manifest's tasks, every uploaded artifact (by
// the ID the agent returned) and the bytes of the decodable ones.
func reportInput(t *testing.T, fa *fakeAgent, m analysis.ResultManifest, manifestClient string) report.Input {
	t.Helper()
	in := report.Input{ReportID: domain.NewID(), Revision: 1, Now: time.Date(2026, 11, 18, 1, 2, 3, 0, time.UTC),
		Run:     report.RunFacts{ID: m.RunID, ProjectID: domain.NewID(), Profile: "deep-review", ScopeKind: "full"},
		Attempt: report.AttemptFacts{ID: m.AttemptID, Number: 1, Epoch: 1, CompletionStatus: "partial", Cleanup: "confirmed"}, Bodies: map[string][]byte{}}
	for _, task := range m.Tasks {
		in.Tasks = append(in.Tasks, report.TaskFacts{ID: task.TaskID, Key: task.Key, Status: string(task.Status), Required: task.Required, Reason: task.Reason, ExecutionOrdinal: 1})
	}
	reg := report.DefaultRegistry()
	for sid, cid := range fa.serverIDs {
		body := fa.artifacts[cid]
		ref := report.ArtifactRef{ID: sid, RunID: m.RunID, AttemptID: m.AttemptID, Role: fa.roles[cid], MediaType: fa.media[cid], SHA256: domain.Digest(body), SizeBytes: int64(len(body))}
		in.Artifacts = append(in.Artifacts, ref)
		switch {
		case cid == manifestClient:
			r := ref
			in.ResultManifest = &r
			in.Bodies[sid] = body
		case ref.Role == "source_manifest":
			src, err := source.DecodeManifest(body)
			if err != nil {
				t.Fatal(err)
			}
			in.SourceManifest = body
			in.Snapshot = report.SnapshotFacts{ID: m.Snapshot.ID, ManifestDigest: m.Snapshot.ManifestDigest, Consistency: src.Consistency}
		case reg.Wants(ref.MediaType):
			in.Bodies[sid] = body
		}
	}
	return in
}

// W15 phase 2 through the real worker: discovery → actual go vet → the five
// review-r1 roles with a scripted model, then the report built from the
// uploaded artifacts with the default registry.
func TestReviewR1ThroughTheWorkerAndReport(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	e := newEnv(t)
	if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
		t.Fatal(err)
	}
	copyW15(t, e.src)
	vet, err := govet.New(govet.Config{GoExecutable: goExe})
	if err != nil {
		t.Fatal(err)
	}
	caller := &r1Scripted{}
	h, err := discovery.Wrap(review.R1Handler{Static: vet, Caller: caller}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe}})
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
	if c.Outcome != "partial" || c.ResultManifestClientArtifact == nil || c.CleanupState != "confirmed" {
		t.Fatalf("completion: %+v", c)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(fa.artifacts[*c.ResultManifestClientArtifact], &m); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, task := range m.Tasks {
		row := task.Key + "=" + string(task.Status)
		if task.Reason != nil {
			row += ":" + *task.Reason
		}
		got = append(got, row)
	}
	want := []string{"env.discover=succeeded", "docs.discover=succeeded", "go-vet=succeeded", "go-vet.m01=succeeded", "go-vet.m02=failed:" + govet.ReasonToolchain,
		"review.environment=succeeded", "review.documents=succeeded", "review.investigate=succeeded", "review.interpret=skipped_policy:no_tool_observations", "review.verify=succeeded"}
	if !slices.Equal(got, want) {
		t.Fatalf("tasks\n got %v\nwant %v", got, want)
	}
	caller.mu.Lock()
	roles := strings.Join(caller.roles, ",")
	caller.mu.Unlock()
	if roles != "environment,documents,investigate,verify" {
		t.Fatalf("model calls %s", roles)
	}

	r, err := report.Build(reportInput(t, fa, m, *c.ResultManifestClientArtifact), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ai := r.Coverage.AIReview
	var required, optional []string
	for _, x := range ai.RequiredTasks {
		required = append(required, x.Key+"="+x.Result)
	}
	for _, x := range ai.OptionalTasks {
		row := x.Key + "=" + x.Result
		if x.Reason != nil {
			row += ":" + *x.Reason
		}
		optional = append(optional, row)
	}
	if ai.Status != report.AIReviewPerformed || !slices.Equal(required, []string{"review.documents=accepted", "review.investigate=accepted", "review.verify=accepted"}) ||
		!slices.Equal(optional, []string{"review.environment=accepted", "review.interpret=skipped:no_tool_observations"}) {
		t.Fatalf("ai review %s required %v optional %v limitations %v", ai.Status, required, optional, r.Limitations)
	}
	if len(r.Coverage.ReviewCalls) != 4 || r.Coverage.LLMExposedFiles == nil || *r.Coverage.LLMExposedFiles == 0 {
		t.Fatalf("calls %+v exposed %v", r.Coverage.ReviewCalls, r.Coverage.LLMExposedFiles)
	}
	for _, call := range r.Coverage.ReviewCalls {
		if call.PackID != review.PackR1 || call.PackVersion != "2.0.0-rc1" {
			t.Fatalf("call %+v", call)
		}
	}
	reviewed := 0
	for _, f := range r.Findings {
		if f.ID == "review_investigate_1" && f.Validation == "inconclusive" {
			reviewed++
		}
	}
	// go-vet.m02 failed on this node, so the report stays partial even with
	// the review performed.
	if reviewed != 1 || r.Status != report.StatusPartial {
		t.Fatalf("status %s findings %+v", r.Status, r.Findings)
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	md := string(report.RenderMarkdown(r, strings.Repeat("0", 64), report.Korean))
	if !strings.Contains(md, "review-r1@2.0.0-rc1") || !strings.Contains(md, "no_tool_observations") {
		t.Fatalf("markdown lacks the pack or the optional reason:\n%s", md)
	}
}
