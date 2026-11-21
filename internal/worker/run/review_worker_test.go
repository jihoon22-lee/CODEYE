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
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// roleScripted answers every review role from the request input alone
// (first gap requirement, first code excerpt, first observation, every
// candidate). Only the model is scripted; it never starts a process or
// contacts a model. With retry, verify takes three calls: the first trims an
// observation ID beside valid evidence for the first candidate and rejects
// the second (a fabricated ID only), the repair reply is not JSON, and the
// retry is valid.
type roleScripted struct {
	mu      sync.Mutex
	roles   []string
	retry   bool
	verifyN int
}

func (f *roleScripted) Review(_ context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
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
			Excerpts []struct{ ID, Kind, Path string } `json:"excerpts"`
		} `json:"scope"`
		Candidates []struct {
			ID       string   `json:"id"`
			Evidence []string `json:"evidence_ids"`
		} `json:"candidates"`
		Observations []struct {
			ID       string   `json:"id"`
			Evidence []string `json:"evidence_ids"`
		} `json:"observations"`
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
			if e.Kind == "code" && e.Path == "gateway.go" {
				code = e.ID
				break
			}
			if e.Kind == "code" && code == "" {
				code = e.ID
			}
		}
		body = map[string]any{"candidates": []map[string]any{{"title": "재시도 경계가 불분명함", "condition": "하위 호출이 계속 실패하는 경우", "impact": "호출이 반복될 수 있다.",
			"improvement": "재시도 한도를 문서와 맞춘다.", "counterpoint": "호출자가 한도를 보장할 수 있다.", "evidence_ids": []string{code}}}}
	case "interpret":
		o := in.Observations[0]
		body = map[string]any{"hypotheses": []map[string]any{{"observation_ids": []string{o.ID}, "title": "형식 문자열과 인자 형식이 다름", "condition": "Describe가 호출되는 경우",
			"impact": "출력 문자열이 잘못 만들어진다.", "improvement": "형식 지정자를 인자 형식과 맞춘다.", "counterpoint": "출력이 사람에게만 보이는 진단일 수 있다.", "evidence_ids": o.Evidence[:1]}},
			"limits": []any{}}
	case "verify":
		f.mu.Lock()
		f.verifyN++
		n := f.verifyN
		f.mu.Unlock()
		if f.retry && n == 2 {
			reply.Reason = "model_output_invalid"
			return reply
		}
		var out []map[string]any
		for i, c := range in.Candidates {
			ev := c.Evidence[:1]
			v := map[string]any{"candidate_id": c.ID, "verdict": "inconclusive", "category": "error_handling", "severity": "low", "severity_reason": "영향 범위가 제한적이다.",
				"impact": "실패 시 호출이 반복될 수 있다.", "valid_conditions": []string{}, "invalid_conditions": []string{}, "supporting_evidence_ids": ev, "counterevidence_ids": []string{},
				"unresolved_assumptions": []string{"호출자를 발췌에서 확인하지 못했다."}, "next_verification": []string{"호출자를 읽는다."}}
			if f.retry && n == 1 {
				if i == 0 {
					v["supporting_evidence_ids"] = append(append([]string{}, ev...), in.Observations[0].ID)
				} else {
					v["verdict"], v["valid_conditions"], v["unresolved_assumptions"] = "supported", []string{"형식이 어긋난다."}, []string{}
					v["supporting_evidence_ids"] = []string{"ev_000000000000000000000000"}
				}
			}
			out = append(out, v)
		}
		body = map[string]any{"verifications": out}
	default:
		reply.Reason = "model_unavailable"
		return reply
	}
	reply.Output, _ = json.Marshal(body)
	return reply
}

func copyModulesFixture(t *testing.T, dst string) {
	t.Helper()
	fixture, _ := filepath.Abs("../../../fixtures/modules/input")
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

// The review through the real worker: discovery → actual go vet → the
// five roles with a scripted model, then the report built from the
// uploaded artifacts with the default registry. "vet finding" adds one file
// to the root module so go vet reports a real observation and the interpret
// role, its observation links and the report lineage run on real data.
func TestReviewThroughTheWorkerAndReport(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	vetFinding := map[string]string{"describe.go": "package gateway\n\nimport \"fmt\"\n\n// Describe formats n for logs.\nfunc Describe(n int) string {\n\treturn fmt.Sprintf(\"%s retries\", n)\n}\n"}
	cases := []struct {
		name               string
		extra              map[string]string
		retry              bool
		interpret, calls   string
		optional, findings []string
	}{
		{name: "clean", interpret: "review.interpret=skipped_policy:no_tool_observations", calls: "environment,documents,investigate,verify",
			optional: []string{"review.environment=accepted", "review.interpret=skipped:no_tool_observations"}, findings: []string{"review_investigate_1=inconclusive"}},
		{name: "vet finding", extra: vetFinding,
			interpret: "review.interpret=succeeded", calls: "environment,documents,investigate,interpret,verify",
			optional: []string{"review.environment=accepted", "review.interpret=accepted"}, findings: []string{"review_interpret_1=inconclusive", "review_investigate_1=inconclusive"}},
		// Recovery path (docs/REVIEW.md#복구-경로): a three-call verifier record
		// reaches the report and validates.
		{name: "verify retry", extra: vetFinding, retry: true,
			interpret: "review.interpret=succeeded", calls: "environment,documents,investigate,interpret,verify,verify,verify",
			optional: []string{"review.environment=accepted", "review.interpret=accepted"}, findings: []string{"review_interpret_1=inconclusive", "review_investigate_1=inconclusive"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
				t.Fatal(err)
			}
			copyModulesFixture(t, e.src)
			for name, body := range tc.extra {
				if err := os.WriteFile(filepath.Join(e.src, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			vet, err := govet.New(govet.Config{GoExecutable: goExe})
			if err != nil {
				t.Fatal(err)
			}
			caller := &roleScripted{retry: tc.retry}
			h, err := discovery.Wrap(review.Handler{Static: vet, Caller: caller}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe}})
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
			// go-vet.m02 (svc/b, go 1.30) fails on a node older than go1.30,
			// as in TestDiscoveryTasksThroughTheWorker.
			want := []string{"env.discover=succeeded", "docs.discover=succeeded", "go-vet=succeeded", "go-vet.m01=succeeded", "go-vet.m02=failed:" + govet.ReasonToolchain,
				"review.environment=succeeded", "review.documents=succeeded", "review.investigate=succeeded", tc.interpret, "review.verify=succeeded",
				"review.contract=skipped_policy:no_review_scope"}
			if !slices.Equal(got, want) {
				t.Fatalf("tasks\n got %v\nwant %v", got, want)
			}
			caller.mu.Lock()
			roles := strings.Join(caller.roles, ",")
			caller.mu.Unlock()
			if roles != tc.calls {
				t.Fatalf("model calls %s", roles)
			}

			r, err := report.Build(reportInput(t, fa, m, *c.ResultManifestClientArtifact), report.DefaultRegistry())
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range r.Limitations {
				if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, "analysis_artifact_") {
					for cid, body := range fa.artifacts {
						if fa.media[cid] == "application/vnd.codeye.review+json" {
							t.Logf("%s", body)
						}
					}
					t.Fatalf("limitation %s in %v", l, r.Limitations)
				}
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
				!slices.Equal(optional, append(slices.Clone(tc.optional), "review.contract=skipped:no_review_scope")) {
				t.Fatalf("ai review %s required %v optional %v limitations %v", ai.Status, required, optional, r.Limitations)
			}
			if len(r.Coverage.ReviewCalls) != len(strings.Split(tc.calls, ",")) || r.Coverage.LLMExposedFiles == nil || *r.Coverage.LLMExposedFiles == 0 {
				t.Fatalf("calls %+v exposed %v", r.Coverage.ReviewCalls, r.Coverage.LLMExposedFiles)
			}
			for _, call := range r.Coverage.ReviewCalls {
				if call.PackID != "review" || call.PackVersion != "2.5.1" {
					t.Fatalf("call %+v", call)
				}
			}
			if tc.retry {
				var outcomes []string
				for _, call := range r.Coverage.ReviewCalls {
					if call.Role == "verify" {
						outcomes = append(outcomes, call.Outcome)
					}
				}
				if !slices.Equal(outcomes, []string{"accepted", "output_rejected", "accepted"}) {
					t.Fatalf("verify calls %v", outcomes)
				}
				var rec record.Record
				for cid, body := range fa.artifacts {
					if fa.media[cid] == "application/vnd.codeye.review+json" && strings.Contains(string(body), `"stage":"verify"`) {
						if err := json.Unmarshal(body, &rec); err != nil {
							t.Fatal(err)
						}
					}
				}
				if len(rec.Calls) != 3 || rec.Calls[2].Attempt != 3 || !slices.Contains(rec.Calls[0].OutputErrors, "evidence_id_dropped") ||
					!slices.Contains(rec.Calls[0].OutputErrors, "evidence_id_unknown") || !slices.Equal(rec.Calls[1].OutputErrors, []string{"output_not_json"}) {
					t.Fatalf("verify record calls %+v", rec.Calls)
				}
			}
			var reviewed []string
			hasExcerpt := false
			hasFullModelExcerpt := false
			observations := map[string]bool{}
			for _, o := range r.Observations {
				observations[o.ID] = true
			}
			for _, f := range r.Findings {
				for _, evidence := range f.Evidence {
					if evidence.Excerpt != nil {
						hasExcerpt = true
						if f.ID == "review_investigate_1" && evidence.Location != nil && evidence.Location.Path == "gateway.go" {
							body, err := os.ReadFile(filepath.Join(e.src, "gateway.go"))
							if err != nil {
								t.Fatal(err)
							}
							loc, ex := evidence.Location, evidence.Excerpt
							lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
							if loc.EndLine-loc.StartLine < 9 || loc.EndLine > len(lines) || ex.StartLine != loc.StartLine || ex.EndLine != loc.EndLine ||
								ex.Text != strings.Join(lines[loc.StartLine-1:loc.EndLine], "\n") {
								t.Fatalf("worker did not save the entire model citation: location=%+v excerpt=%+v", loc, ex)
							}
							hasFullModelExcerpt = true
						}
					}
				}
				if strings.HasPrefix(f.ID, "review_") {
					reviewed = append(reviewed, f.ID+"="+f.Validation)
				}
				for _, id := range f.ObservationIDs {
					if !observations[id] {
						t.Fatalf("finding %s cites observation %s not in the report", f.ID, id)
					}
				}
				if f.ID == "review_interpret_1" && len(f.ObservationIDs) != 1 {
					t.Fatalf("interpret finding without its observation: %+v", f)
				}
			}
			if !hasExcerpt || !hasFullModelExcerpt {
				t.Fatalf("missing snapshot excerpt or full model citation")
			}
			if err := report.ValidateReport(report.Project(r, false)); err != nil {
				t.Fatal(err)
			}
			slices.Sort(reviewed)
			// go-vet.m02 failed on this node, so the report stays partial even
			// with the review performed.
			if !slices.Equal(reviewed, tc.findings) || r.Status != report.StatusPartial {
				t.Fatalf("status %s review findings %v", r.Status, reviewed)
			}
			if err := report.ValidateReport(r); err != nil {
				t.Fatal(err)
			}
			md := string(report.RenderMarkdown(r, strings.Repeat("0", 64), report.Korean))
			if !strings.Contains(md, "review@2.5.1") || !strings.Contains(md, "`review.interpret`") || (tc.extra == nil && !strings.Contains(md, "`no_tool_observations`")) {
				t.Fatalf("markdown lacks the pack or the optional row:\n%s", md)
			}
		})
	}
}
