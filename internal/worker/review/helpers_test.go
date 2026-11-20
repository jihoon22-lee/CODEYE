package review

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// modulesJob acquires fixtures/modules/input (optionally changed) as an
// immutable snapshot.
func modulesJob(t *testing.T, change func(root string)) analysis.Job {
	t.Helper()
	root := t.TempDir()
	fixture, err := filepath.Abs("../../../fixtures/modules/input")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(root)
	}
	s, err := source.AcquireLocal(t.Context(), root, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Profile: "deep-review", Snapshot: s, Scratch: t.TempDir(), Scope: domain.Scope{Kind: "full"}, AllowModelReview: true}
}

func writeFile(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// vetStub stands in for the go-vet handler: one task that writes a
// go-vet observations document with the given observations.
type vetStub struct {
	obs func(j analysis.Job) []govet.Observation
}

func (vetStub) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "go-vet", Version: "stub", Capability: "analysis:deep-review"}
}
func (vetStub) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "go-vet", Required: true, Title: "go vet stub", Stage: domain.StageStaticAnalysis}}, nil
}
func (v vetStub) Run(_ context.Context, j analysis.Job, t analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	var obs []govet.Observation
	if v.obs != nil {
		obs = v.obs(j)
	}
	if obs == nil {
		obs = []govet.Observation{}
	}
	version := "go1.27.4"
	class := govet.ExitSuccessWithoutFindings
	if len(obs) > 0 {
		class = govet.ExitSuccessWithFindings
	}
	d := govet.Document{SchemaVersion: govet.DocumentSchemaVersion, Kind: govet.DocumentKind, AnalyzerID: govet.AnalyzerID, AdapterVersion: govet.AdapterVersion,
		ParserID: govet.ParserID, ParserVersion: govet.ParserVersion, TaskKey: t.Key, ManifestDigest: j.Snapshot.Digest, Module: govet.ModuleRef{Dir: "."},
		Patterns: []string{"./..."}, ToolVersion: &version, Outcome: govet.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: class},
		Executions: []govet.ExecSummary{}, Inventory: []govet.PackageScope{}, Coverage: govet.Coverage{Packages: map[string]int{}}, Observations: obs,
		AnalyzerErrors: []govet.AnalyzerError{}, StderrFailures: []govet.StderrFailure{}, Parse: govet.ParseReport{OK: true, Observations: len(obs)},
		WorkspaceDrift: []string{}, Limitations: []string{"default_build_context_only"}}
	b, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	p := govet.ObservationsPath(j.Scratch, t.Key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		panic(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{{Path: p, Role: analysis.RoleObservations, MediaType: analysis.MediaObservations}}}
}

// gatewayObservation is a synthetic go vet observation on gateway.go.
func gatewayObservation(j analysis.Job) []govet.Observation {
	sha := ""
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Path == "gateway.go" {
			sha = e.SHA256
		}
	}
	p := "gateway.go"
	return []govet.Observation{{ID: "obs_0123456789abcdef0123", Tool: govet.AnalyzerID, RuleID: "unusedresult", RawMessage: "result of send call not used in a retry branch (synthetic fixture)",
		Path: &p, Location: &govet.ObsLocation{Path: p, StartLine: 13, StartCol: 2, EndLine: 13, EndCol: 12, ColumnUnit: "byte", BlobSHA256: sha}, LocationState: govet.LocationResolved,
		RawRef: govet.RawRef{Execution: "go.vet", Stream: "stdout", StreamSHA256: strings.Repeat("a", 64), JSONPointer: "/example.test~1modules/unusedresult/0", ByteStart: 0, ByteEnd: 10}, PackageKey: "example.test/modules"}}
}

// scriptedCaller scripts the model per role; n counts calls of that role. It never
// starts a process or contacts a model.
type scriptedCaller struct {
	mu       sync.Mutex
	requests []opencode.Request
	byRole   map[string]int
	script   map[string]func(n int, q opencode.Request) opencode.Reply
}

func (f *scriptedCaller) Review(_ context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byRole == nil {
		f.byRole = map[string]int{}
	}
	f.requests = append(f.requests, q)
	f.byRole[q.Role]++
	fn := f.script[q.Role]
	var r opencode.Reply
	if fn == nil {
		r = opencode.Reply{Reason: "model_unavailable"}
	} else {
		r = fn(f.byRole[q.Role], q)
	}
	r.Model, r.Version = "fake/scripted", opencode.Version
	r.RequestDigest = domain.Digest([]byte(opencode.Prompt(q)))
	if r.Usage == nil {
		r.Usage = &opencode.Usage{Input: int64(len(q.Input)), Output: 50}
	}
	return r
}

func (f *scriptedCaller) calls(role string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byRole[role]
}

func asJSON(v any) opencode.Reply {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return opencode.Reply{Output: b}
}

func decodeAs[T any](t *testing.T, q opencode.Request) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(q.Input, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func docID(in docsInput, p string) string {
	for _, d := range in.Documents {
		if d.Path == p {
			return d.EvidenceID
		}
	}
	return ""
}

func excerptID(list []record.Excerpt, p string) string {
	for _, e := range list {
		if e.Path == p {
			return e.ID
		}
	}
	return ""
}

// validScript returns valid outputs for every review role on the modules
// fixture.
func validScript(t *testing.T) map[string]func(int, opencode.Request) opencode.Reply {
	return map[string]func(int, opencode.Request) opencode.Reply{
		"environment": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[envInput](t, q)
			var req envReqInput
			for _, r := range in.Requirements {
				if r.Module == "go:svc/b" {
					req = r
				}
			}
			return asJSON(map[string]any{"notes": []map[string]any{{"module": "go:svc/b", "requirement_ids": []string{req.ID}, "evidence_ids": []string{req.EvidenceID},
				"affected_checks": "svc/b 모듈의 go vet은 선언 버전보다 낮은 노드 toolchain 때문에 수행할 수 없다.", "still_checkable": "루트와 svc/a 모듈의 정적 검사와 코드 검토는 가능하다.",
				"needed_observation": "go 1.30 이상 toolchain의 노드 관측"}}})
		},
		"documents": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[docsInput](t, q)
			root, sub, agents := docID(in, "README.md"), docID(in, "svc/b/README.md"), docID(in, "AGENTS.md")
			return asJSON(map[string]any{
				"contracts": []map[string]any{
					{"statement": "하위 호출이 실패하면 최대 3번 재시도한다.", "applies_to": ".", "evidence_ids": []string{root}},
					{"statement": "결제 호출은 요청마다 정확히 한 번만 한다.", "applies_to": "svc/b", "evidence_ids": []string{sub}},
				},
				"conflicts": []map[string]any{{"scope": "svc/b", "summary": "루트 안내는 재시도를, svc/b 안내는 재시도 금지를 요구한다.", "impact": "svc/b 결제 호출의 재시도 여부를 코드에서 확인해야 한다.",
					"first_evidence_ids": []string{root}, "second_evidence_ids": []string{sub}}},
				"questions":         []map[string]any{{"question": "svc/b의 Charge 호출자가 실패 시 재시도하는가?", "applies_to": "svc/b", "evidence_ids": []string{sub}}},
				"instruction_notes": []map[string]any{{"note": "권한·네트워크·키 조회를 요구하는 지침은 분석 자료로만 기록했다.", "evidence_ids": []string{agents}}},
			})
		},
		"investigate": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[investigateInput](t, q)
			gw := excerptID(in.Scope.Excerpts, "gateway.go")
			ev := []string{gw}
			if len(in.Documents.Evidence) > 0 {
				ev = append(ev, in.Documents.Evidence[0].ID)
			}
			return asJSON(map[string]any{"candidates": []map[string]any{{"title": "Forward가 결제 호출에도 재시도를 적용할 수 있음", "condition": "svc/b 결제 호출을 Forward로 감싸는 경우",
				"impact": "결제가 중복될 수 있다.", "improvement": "결제 경로에서는 재시도를 끈다.", "counterpoint": "Forward를 결제에 쓰는 호출자가 발췌에 없다.", "evidence_ids": ev}}})
		},
		"interpret": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[interpretInput](t, q)
			o := in.Observations[0]
			return asJSON(map[string]any{"hypotheses": []map[string]any{{"observation_ids": []string{o.ID}, "title": "재시도 분기에서 결과를 버릴 수 있음", "condition": "send가 실패한 뒤 다시 호출되는 경우",
				"impact": "마지막 오류만 남는다.", "improvement": "각 시도의 오류를 기록한다.", "counterpoint": "마지막 오류만 반환하는 것이 문서상 계약일 수 있다.", "evidence_ids": o.EvidenceIDs}},
				"limits": []map[string]any{}})
		},
		"verify": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[verifyInput](t, q)
			var out []map[string]any
			for _, c := range in.Candidates {
				out = append(out, verdict(c.ID, "inconclusive", func(m map[string]any) {
					m["category"], m["severity_reason"], m["impact"] = "error_handling", "재시도 범위가 문서 계약과 어긋날 수 있다.", "실패 처리 방식이 모듈 안내와 다를 수 있다."
					m["next_verification"] = []string{"Forward의 호출자와 결제 경로를 읽는다."}
					m["unresolved_assumptions"] = []string{"Forward의 실제 호출자를 발췌에서 확인하지 못했다."}
					m["supporting_evidence_ids"] = []string{c.Evidence[0]}
				}))
			}
			return asJSON(map[string]any{"verifications": out})
		},
	}
}

// wholeRunRole reports one of the five whole-run role tasks (not
// review.scope or a slice task).
func wholeRunRole(key string) bool {
	role, ok := strings.CutPrefix(key, "review.")
	return ok && slices.Contains(record.Stages, role)
}

// runRole plans and runs the deep-review composition (discovery → static →
// review) with an injected node observation.
func runRole(t *testing.T, j analysis.Job, static analysis.Handler, caller Caller, goVersion string) ([]analysis.TaskSpec, map[string]analysis.TaskResult) {
	t.Helper()
	return runRoleWith(t, j, Handler{Static: static, Caller: caller}, goVersion)
}

// runRoleWith is runRole with a configured role handler (call ceiling).
func runRoleWith(t *testing.T, j analysis.Job, rh Handler, goVersion string) ([]analysis.TaskSpec, map[string]analysis.TaskResult) {
	t.Helper()
	h, err := discovery.Wrap(rh, discovery.Config{Observer: discovery.StaticObserver{Versions: map[string]string{"go": goVersion}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]analysis.TaskResult{}
	for _, task := range plan {
		out[task.Key] = h.Run(t.Context(), j, task, nil)
	}
	return plan, out
}

func readRecord(t *testing.T, j analysis.Job, stage string) record.Record {
	t.Helper()
	b, err := os.ReadFile(recordPath(j, stage))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"text":`) {
		t.Fatal("record stores source text")
	}
	r, err := record.Decode(b, expectStage(j, stage))
	if err != nil {
		t.Fatal(stage, err)
	}
	return r
}
