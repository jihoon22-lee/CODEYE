package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// fakeCaller is a controlled stand-in for the OpenCode caller. It never
// starts a process or contacts a model; replies are scripted per call.
type fakeCaller struct {
	t        *testing.T
	requests []opencode.Request
	reply    func(n int, q opencode.Request) opencode.Reply
}

func (f *fakeCaller) Review(_ context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
	f.requests = append(f.requests, q)
	r := f.reply(len(f.requests), q)
	r.Model, r.Version = "fake/scripted", opencode.Version
	r.RequestDigest = domain.Digest([]byte(q.Instructions + string(q.Input)))
	if r.Usage == nil {
		r.Usage = &opencode.Usage{Input: int64(len(q.Input)), Output: 40}
	}
	return r
}

func (f *fakeCaller) count(role string) int {
	n := 0
	for _, q := range f.requests {
		if q.Role == role {
			n++
		}
	}
	return n
}

type sentInput struct {
	Scope struct {
		Excerpts []Excerpt `json:"excerpts"`
	} `json:"scope"`
	Candidates []Candidate    `json:"candidates"`
	Repair     *repairInput   `json:"repair"`
	Facts      []Fact         `json:"facts"`
	Extra      map[string]any `json:"-"`
}

func decodeInput(t *testing.T, q opencode.Request) sentInput {
	t.Helper()
	var in sentInput
	if err := json.Unmarshal(q.Input, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func (in sentInput) find(path, origin string) Excerpt {
	for _, e := range in.Scope.Excerpts {
		if e.Path == path && (origin == "" || e.Origin == origin) {
			return e
		}
	}
	return Excerpt{}
}

func ok(v any) opencode.Reply {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return opencode.Reply{Output: b}
}

// verifyJob acquires the testdata fixture as an immutable snapshot.
func verifyJob(t *testing.T, docs bool) analysis.Job {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"store.go", "load.go", "store_test.go", "go.mod"} {
		b, err := os.ReadFile(filepath.Join("testdata", "verify", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if docs {
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("store.Open hands the file to the caller, which closes it.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := source.AcquireLocal(t.Context(), root, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Snapshot: s, Scratch: t.TempDir(), Scope: domain.Scope{Kind: "full"}, AllowModelReview: true}
}

// leakCandidates is the scripted investigation path: one fact and n candidates citing
// store.go, the declaration of Open.
func leakCandidates(n int) func(int, opencode.Request) (opencode.Reply, bool) {
	return func(_ int, q opencode.Request) (opencode.Reply, bool) {
		var in sentInput
		_ = json.Unmarshal(q.Input, &in)
		store := in.find("store.go", "")
		switch q.Role {
		case "context":
			return ok(map[string]any{"facts": []Fact{{Kind: "observed", Statement: "Open은 os.Open 결과를 그대로 반환한다.", Evidence: []string{store.ID}}}}), true
		case "investigate":
			cands := []Candidate{}
			for i := range n {
				cands = append(cands, Candidate{Title: fmt.Sprintf("Open 반환 파일 미종료 가능성 %d", i+1), Condition: "호출자가 반환된 파일을 닫지 않는 경우",
					Impact: "파일 descriptor가 누수될 수 있다.", Improvement: "호출자 정리 계약을 확인한다.", Counterpoint: "호출자에서 defer Close를 하는지 확인하지 않았다.", Evidence: []string{store.ID}})
			}
			return ok(map[string]any{"candidates": cands}), true
		}
		return opencode.Reply{}, false
	}
}

func verdict(id, v string, mutate func(map[string]any)) map[string]any {
	m := map[string]any{"candidate_id": id, "verdict": v, "category": "resource_lifecycle", "severity": "medium",
		"severity_reason": "파일 descriptor 수명 문제다.", "impact": "장시간 실행 시 descriptor가 고갈될 수 있다.",
		"valid_conditions": []string{}, "invalid_conditions": []string{}, "supporting_evidence_ids": []string{}, "counterevidence_ids": []string{},
		"unresolved_assumptions": []string{}, "next_verification": []string{"다른 호출자의 정리 경로를 읽는다."}}
	if mutate != nil {
		mutate(m)
	}
	return m
}

func runStages(t *testing.T, j analysis.Job, f *fakeCaller) (Handler, analysis.TaskResult) {
	t.Helper()
	h := Handler{Caller: f}
	for _, role := range []string{"context", "investigate"} {
		if r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review." + role}, nil); r.Status != domain.TaskSucceeded {
			t.Fatalf("%s: %s %s", role, r.Status, r.Reason)
		}
	}
	return h, h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.verify"}, nil)
}

func script(t *testing.T, n int, verify func(k int, in sentInput) opencode.Reply) *fakeCaller {
	prior := leakCandidates(n)
	k := 0
	return &fakeCaller{t: t, reply: func(i int, q opencode.Request) opencode.Reply {
		if r, done := prior(i, q); done {
			return r
		}
		k++
		return verify(k, decodeInput(t, q))
	}}
}

func readVerify(t *testing.T, j analysis.Job, r analysis.TaskResult) Record {
	t.Helper()
	if len(r.Artifacts) != 1 || r.Artifacts[0].MediaType != record.MediaType {
		t.Fatalf("artifacts %+v", r.Artifacts)
	}
	b, err := os.ReadFile(r.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"text":`) || strings.Contains(string(b), "statusCodes") {
		t.Fatal("record stores source text")
	}
	rec, err := record.Decode(b, expect(j, "verify"))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestPlanRequiresVerify(t *testing.T) {
	h := Handler{Static: stubStatic{}}
	plan, err := h.Plan(t.Context(), analysis.Job{})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range plan {
		if strings.HasPrefix(p.Key, "review.") && !p.Required {
			t.Fatal("optional review task")
		}
		keys = append(keys, p.Key)
	}
	if !slices.Equal(keys, []string{"static", "review.context", "review.investigate", "review.verify"}) {
		t.Fatal(keys)
	}
}

type stubStatic struct{}

func (stubStatic) Info() analysis.HandlerInfo { return analysis.HandlerInfo{Name: "stub"} }
func (stubStatic) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "static", Required: true}}, nil
}
func (stubStatic) Run(context.Context, analysis.Job, analysis.TaskSpec, executor.Runner) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

// TC-10/TC-87: deferred cleanup in a caller outside the investigation excerpt window is
// selected by the platform; a refutation citing it retracts the candidate,
// and the refuted candidate and its evidence stay in the record.
func TestVerifyCounterexampleRetractsCandidate(t *testing.T) {
	for _, docs := range []bool{false, true} {
		j := verifyJob(t, docs)
		var caller, test Excerpt
		f := script(t, 1, func(k int, in sentInput) opencode.Reply {
			caller, test = in.find("load.go", "caller"), in.find("store_test.go", "test")
			return ok(map[string]any{"verifications": []any{verdict("candidate_1", "refuted", func(m map[string]any) {
				m["invalid_conditions"] = []string{"Load는 Open 직후 defer f.Close()로 파일을 닫는다."}
				m["counterevidence_ids"] = []string{caller.ID}
				m["supporting_evidence_ids"] = []string{in.find("store.go", "").ID}
			})}})
		})
		_, r := runStages(t, j, f)
		if r.Status != domain.TaskSucceeded {
			t.Fatal(r)
		}
		if caller.Start <= 50 || caller.Symbol != "Open" || caller.Kind != "code" || !strings.Contains(caller.Text, "defer f.Close()") {
			t.Fatalf("caller cleanup not selected: %+v", caller)
		}
		if test.Kind != "test" || test.Start <= 50 {
			t.Fatalf("test caller not selected: %+v", test)
		}
		rec := readVerify(t, j, r)
		v := rec.Verifications[0]
		if !rec.Accepted() || v.Verdict != record.VerdictRefuted || !slices.Equal(v.Counterevidence, []string{caller.ID}) || len(rec.Candidates) != 1 {
			t.Fatalf("%+v", rec)
		}
		if docs == slices.Contains(rec.Limitations, "no_document_in_selected_context") {
			t.Fatalf("docs=%v limitations=%v", docs, rec.Limitations)
		}
		last := rec.Calls[len(rec.Calls)-1]
		if f.count("verify") != 1 || last.Role != "verify" || last.Attempt != 1 || last.Outcome != "accepted" || last.Usage == nil || len(last.PackDigest) != 64 {
			t.Fatalf("%+v", rec.Calls)
		}
	}
}

// TC-11/TC-88: fabricated evidence IDs and model-authored paths are rejected;
// the repair request carries only fixed error codes plus the trusted scope;
// still-invalid items are quarantined as unreviewed.
func TestVerifyRejectsFabricatedCitations(t *testing.T) {
	j := verifyJob(t, false)
	var repair sentInput
	f := script(t, 2, func(k int, in sentInput) opencode.Reply {
		store := in.find("store.go", "").ID
		switch k {
		case 1:
			return ok(map[string]any{"verifications": []any{
				verdict("candidate_1", "supported", func(m map[string]any) {
					m["supporting_evidence_ids"] = []string{store}
					m["valid_conditions"] = []string{"호출자가 Close를 하지 않는다."}
				}),
				verdict("candidate_2", "supported", func(m map[string]any) {
					m["supporting_evidence_ids"] = []string{"ev_000000000000fabricated"}
					m["valid_conditions"] = []string{"FABRICATED_MODEL_TEXT"}
				}),
			}})
		default:
			repair = in
			return ok(map[string]any{"verifications": []any{verdict("candidate_2", "supported", func(m map[string]any) {
				m["supporting_evidence_ids"] = []string{store}
				m["valid_conditions"] = []string{"없는 파일에서도 같다."}
				m["path"] = "internal/missing.go"
			})}})
		}
	})
	_, r := runStages(t, j, f)
	if r.Status != domain.TaskFailed || r.Reason != "review_output_incomplete" || !slices.Contains(r.Limitations, record.LimQuarantined) {
		t.Fatal(r)
	}
	if repair.Repair == nil || len(repair.Candidates) != 1 || repair.Candidates[0].ID != "candidate_2" ||
		!slices.Equal(repair.Repair.Candidates[0].Errors, []string{"evidence_id_unknown", "supported_without_code_evidence"}) || len(repair.Repair.AcceptedIDs) != len(repair.Scope.Excerpts) ||
		repair.find("load.go", "caller").Text == "" {
		t.Fatalf("repair input %+v", repair.Repair)
	}
	raw, _ := json.Marshal(repair)
	if strings.Contains(string(raw), "FABRICATED_MODEL_TEXT") || strings.Contains(string(raw), "fabricated") {
		t.Fatal("model text echoed into repair")
	}
	rec := readVerify(t, j, r)
	if rec.Verifications[0].Verdict != record.VerdictSupported || rec.Verifications[1].Verdict != record.VerdictUnreviewed ||
		!slices.Equal(rec.Verifications[1].Limitations, []string{record.LimQuarantined}) || f.count("verify") != 2 {
		t.Fatalf("%+v", rec.Verifications)
	}
	b, _ := os.ReadFile(r.Artifacts[0].Path)
	if strings.Contains(string(b), "internal/missing.go") || strings.Contains(string(b), "FABRICATED") {
		t.Fatal("rejected model content kept")
	}
}

// Bogus claims: supported without code evidence, refuted without
// counterevidence and model-assigned critical severity are never accepted.
func TestVerifyRejectsBogusClaims(t *testing.T) {
	catalog := map[string]Excerpt{"ev_doc": {ID: "ev_doc", Kind: "document"}, "ev_code": {ID: "ev_code", Kind: "code"}}
	cases := map[string]func(map[string]any){
		"supported_without_code_evidence": func(m map[string]any) {
			m["verdict"], m["supporting_evidence_ids"], m["valid_conditions"] = "supported", []string{"ev_doc"}, []string{"문서가 그렇게 말한다."}
		},
		"refuted_without_counterevidence": func(m map[string]any) {
			m["verdict"], m["invalid_conditions"] = "refuted", []string{"방어 코드가 있다."}
		},
		"severity_not_assignable": func(m map[string]any) {
			m["severity"], m["unresolved_assumptions"] = "critical", []string{"호출자를 모른다."}
		},
		"evidence_id_unknown": func(m map[string]any) {
			m["counterevidence_ids"], m["unresolved_assumptions"] = []string{"ev_code", "ev_other"}, []string{"x"}
		},
		"unresolved_assumptions_missing": func(m map[string]any) { m["verdict"] = "inconclusive" },
		"next_verification_invalid": func(m map[string]any) {
			m["unresolved_assumptions"], m["next_verification"] = []string{"x"}, []string{}
		},
	}
	for want, mutate := range cases {
		m := verdict("candidate_1", "inconclusive", mutate)
		raw, _ := json.Marshal(map[string]any{"verifications": []any{m}})
		got, errs, out := parseVerifications(raw, map[string]bool{"candidate_1": true}, catalog)
		if len(got) != 0 || len(out) != 0 || !slices.Contains(errs["candidate_1"], want) {
			t.Fatalf("%s: got=%v errs=%v out=%v", want, got, errs, out)
		}
	}
	for _, raw := range []string{`{"verifications":[{"candidate_id":"candidate_9"}]}`, `[]`, `{"result":[]}`} {
		_, _, out := parseVerifications(json.RawMessage(raw), map[string]bool{"candidate_1": true}, catalog)
		if len(out) == 0 {
			t.Fatal(raw)
		}
	}
}

// No unbacked reproduced: a model reproduction claim becomes inconclusive
// with a platform limitation and can never be decoded as reproduced.
func TestVerifyNeverAcceptsReproduced(t *testing.T) {
	j := verifyJob(t, false)
	f := script(t, 1, func(k int, in sentInput) opencode.Reply {
		return ok(map[string]any{"verifications": []any{verdict("candidate_1", "reproduced", func(m map[string]any) {
			m["supporting_evidence_ids"] = []string{in.find("store.go", "").ID}
			m["valid_conditions"] = []string{"테스트를 실행해 누수를 재현했다."}
		})}})
	})
	_, r := runStages(t, j, f)
	if r.Status != domain.TaskSucceeded || !slices.Contains(r.Limitations, record.LimReproductionRejected) {
		t.Fatal(r)
	}
	rec := readVerify(t, j, r)
	v := rec.Verifications[0]
	if v.Verdict != record.VerdictInconclusive || !slices.Equal(v.Limitations, []string{record.LimReproductionRejected}) {
		t.Fatalf("%+v", v)
	}
	b, _ := os.ReadFile(r.Artifacts[0].Path)
	forged := strings.Replace(string(b), `"verdict":"inconclusive"`, `"verdict":"reproduced"`, 1)
	if _, err := record.Decode([]byte(forged), expect(j, "verify")); err == nil {
		t.Fatal("decoded a reproduced verdict")
	}
	forged = strings.Replace(string(b), `"limitations":["reproduction_claim_rejected"]`, `"limitations":[]`, 1)
	if _, err := record.Decode([]byte(forged), expect(j, "verify")); err == nil {
		t.Fatal("decoded inconclusive without assumption or limitation")
	}
}

// TC-12/TC-89: malformed model JSON gets one repair call; the first paid call
// keeps its usage receipt. A second malformed answer fails the task with a
// provenance record and no third call.
func TestVerifyMalformedOutputRepairsOnce(t *testing.T) {
	for _, recovers := range []bool{true, false} {
		j := verifyJob(t, false)
		f := script(t, 1, func(k int, in sentInput) opencode.Reply {
			if k == 1 || !recovers {
				return opencode.Reply{Reason: "model_output_invalid", Usage: &opencode.Usage{Input: 900, Output: 77}}
			}
			if in.Repair == nil || !slices.Equal(in.Repair.OutputErrors, []string{"output_not_json"}) || in.find("load.go", "caller").Text == "" {
				t.Fatalf("repair input %+v", in.Repair)
			}
			return ok(map[string]any{"verifications": []any{verdict("candidate_1", "inconclusive", func(m map[string]any) {
				m["unresolved_assumptions"] = []string{"다른 호출자가 있는지 모른다."}
				m["supporting_evidence_ids"] = []string{in.find("store.go", "").ID}
			})}})
		})
		_, r := runStages(t, j, f)
		rec := readVerify(t, j, r)
		calls := rec.Calls[len(rec.Calls)-2:]
		if f.count("verify") != 2 || calls[0].Attempt != 1 || calls[0].Outcome != "output_rejected" || calls[0].Usage == nil || calls[0].Usage.Output != 77 {
			t.Fatalf("%+v", rec.Calls)
		}
		if recovers {
			if r.Status != domain.TaskSucceeded || calls[1].Outcome != "accepted" || rec.Verifications[0].Verdict != record.VerdictInconclusive {
				t.Fatal(r, rec)
			}
			continue
		}
		if r.Status != domain.TaskFailed || r.Reason != reasonOutputInvalid || rec.Accepted() || rec.FailureReason != reasonOutputInvalid ||
			calls[1].Outcome != "output_rejected" || rec.Verifications[0].Verdict != record.VerdictUnreviewed {
			t.Fatal(r, rec)
		}
	}
}

// Model failure: the verify task is not succeeded, its record keeps the
// attempted call and the candidates as unreviewed, and other tasks' results
// are untouched (the worker then reports the run as partial).
func TestVerifyModelFailureKeepsProvenance(t *testing.T) {
	for reason, status := range map[string]domain.TaskStatus{"model_timeout": domain.TaskTimedOut, "model_unavailable": domain.TaskFailed, "policy_blocked": domain.TaskSkippedPolicy, "recovery_required": domain.TaskFailed} {
		j := verifyJob(t, false)
		f := script(t, 2, func(int, sentInput) opencode.Reply { return opencode.Reply{Reason: reason} })
		h, r := runStages(t, j, f)
		if r.Status != status || r.Reason != reason || f.count("verify") != 1 {
			t.Fatal(reason, r)
		}
		rec := readVerify(t, j, r)
		last := rec.Calls[len(rec.Calls)-1]
		if rec.Accepted() || rec.FailureReason != reason || last.Outcome != reason || last.RequestDigest == "" || len(rec.Verifications) != 2 || rec.Verifications[0].Verdict != record.VerdictUnreviewed {
			t.Fatalf("%+v", rec)
		}
		h.Static = stubStatic{}
		if s := h.Run(t.Context(), j, analysis.TaskSpec{Key: "static"}, nil); s.Status != domain.TaskSucceeded {
			t.Fatal("static task affected")
		}
	}
}

func TestVerifyCancellation(t *testing.T) {
	j := verifyJob(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	f := script(t, 1, func(k int, in sentInput) opencode.Reply {
		cancel() // cancelled while the first answer was malformed
		return opencode.Reply{Reason: "model_output_invalid"}
	})
	h := Handler{Caller: f}
	for _, role := range []string{"context", "investigate"} {
		if r := h.Run(ctx, j, analysis.TaskSpec{Key: "review." + role}, nil); r.Status != domain.TaskSucceeded {
			t.Fatal(r)
		}
	}
	r := h.Run(ctx, j, analysis.TaskSpec{Key: "review.verify"}, nil)
	if r.Status != domain.TaskCancelled || f.count("verify") != 1 {
		t.Fatal(r, f.count("verify"))
	}
	if rec := readVerify(t, j, r); rec.Accepted() || rec.FailureReason != "cancelled" {
		t.Fatal(rec)
	}
	j2 := verifyJob(t, false)
	f2 := script(t, 1, func(int, sentInput) opencode.Reply { return opencode.Reply{Reason: "cancelled"} })
	if _, r := runStages(t, j2, f2); r.Status != domain.TaskCancelled || f2.count("verify") != 1 {
		t.Fatal(r)
	}
}

func TestVerifyInputsAndDependencies(t *testing.T) {
	j := verifyJob(t, false)
	h := Handler{Caller: script(t, 0, func(int, sentInput) opencode.Reply { t.Fatal("model called"); return opencode.Reply{} })}
	if r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.verify"}, nil); r.Status != domain.TaskSkippedPolicy || r.Reason != "dependency_failed" {
		t.Fatal(r)
	}
	// Zero candidates: nothing to verify, no model call, accepted record.
	_, r := runStages(t, j, h.Caller.(*fakeCaller))
	if r.Status != domain.TaskSucceeded || !slices.Contains(r.Limitations, limNoCandidates) {
		t.Fatal(r)
	}
	if rec := readVerify(t, j, r); len(rec.Verifications) != 0 || !rec.Accepted() {
		t.Fatal(rec)
	}
	// A prior record whose evidence was moved to another path is not trusted.
	p := filepath.Join(j.Scratch, "review.investigate.json")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), `"path":"store.go"`, `"path":"missing.go"`)), 0600); err != nil {
		t.Fatal(err)
	}
	if r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.verify"}, nil); r.Reason != "dependency_failed" {
		t.Fatal(r)
	}
	// A changed snapshot blob fails integrity before any model call.
	j = verifyJob(t, false)
	f := script(t, 1, func(int, sentInput) opencode.Reply { t.Fatal("model called"); return opencode.Reply{} })
	h = Handler{Caller: f}
	for _, role := range []string{"context", "investigate"} {
		h.Run(t.Context(), j, analysis.TaskSpec{Key: "review." + role}, nil)
	}
	target := filepath.Join(j.Snapshot.SnapshotDir, "load.go")
	if err := os.Chmod(target, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package store\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.verify"}, nil); r.Status != domain.TaskFailed || r.Reason != "review_input_unavailable" {
		t.Fatal(r)
	}
}

func TestExpansionScopeAndBounds(t *testing.T) {
	j := verifyJob(t, false)
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		t.Fatal(err)
	}
	var store Excerpt
	for _, e := range c.Excerpts {
		if e.Path == "store.go" {
			store = e
		}
	}
	cands := []Candidate{{ID: "candidate_1", Evidence: []string{store.ID}}}
	extra, lims, err := expand(t.Context(), j.Snapshot, j.Scope, c.Excerpts, cands)
	if err != nil || len(extra) != 2 || slices.Contains(lims, limNoCallers) {
		t.Fatal(extra, lims, err)
	}
	for _, e := range extra {
		if e.ID != record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, e.Start, e.End) || e.End-e.Start+1 > maxWindowLines || len(e.Text) > maxWindowBytes {
			t.Fatalf("%+v", e)
		}
	}
	// A scope limited to store.go never reads callers elsewhere.
	extra, lims, err = expand(t.Context(), j.Snapshot, domain.Scope{Kind: "paths", Paths: []string{"store.go"}}, c.Excerpts, cands)
	if err != nil || len(extra) != 0 || !slices.Contains(lims, limNoCallers) {
		t.Fatal(extra, lims, err)
	}
	// No declared function in cited code: nothing to expand.
	extra, lims, _ = expand(t.Context(), j.Snapshot, j.Scope, c.Excerpts, []Candidate{{ID: "candidate_1", Evidence: []string{"ev_unknown"}}})
	if len(extra) != 0 || !slices.Contains(lims, limNoSymbol) {
		t.Fatal(extra, lims)
	}
}

// TestLiveVerifier seeds a deliberately false, narrowly scoped investigation
// candidate.
// The verifier alone calls the explicitly configured hosted model.
func TestLiveVerifier(t *testing.T) {
	relay := os.Getenv("CODEYE_TEST_REVIEW_RELAY")
	if relay == "" {
		t.Skip("requires explicit hosted model test configuration")
	}
	model := os.Getenv("CODEYE_TEST_REVIEW_MODEL")
	if model == "" {
		model = "codeye-gateway/gpt-6.2-sol"
	}
	j := verifyJob(t, false)
	prior := leakCandidates(1)
	seed := Handler{Caller: &fakeCaller{reply: func(n int, q opencode.Request) opencode.Reply {
		if q.Role == "context" {
			r, _ := prior(n, q)
			return r
		}
		e := decodeInput(t, q).find("store.go", "")
		candidate := Candidate{Title: "Load leaks the file opened by Open on every successful read.", Condition: "Load returns a byte after opening the data file.", Impact: "Repeated successful Load calls leave their file handles open.", Improvement: "Close the file in Load before returning.", Counterpoint: "The caller implementation must be checked.", Evidence: []string{e.ID}}
		return ok(map[string]any{"candidates": []Candidate{candidate}})
	}}}
	for _, role := range []string{"context", "investigate"} {
		if r := seed.Run(t.Context(), j, analysis.TaskSpec{Key: "review." + role}, nil); r.Status != domain.TaskSucceeded {
			t.Fatalf("seed stage %s: %s %s", role, r.Status, r.Reason)
		}
	}
	h := Handler{Caller: opencode.Config{Executable: os.Getenv("CODEYE_TEST_OPENCODE_BIN"), Model: model, RelayFile: relay, Timeout: 90 * time.Second}}
	r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.verify"}, &executor.Executor{})
	if r.Status != domain.TaskSucceeded {
		t.Fatalf("verifier status=%s reason=%s", r.Status, r.Reason)
	}
	rec := readVerify(t, j, r)
	if len(rec.Verifications) != 1 {
		t.Fatalf("verifications=%d", len(rec.Verifications))
	}
	v := rec.Verifications[0]
	for _, e := range rec.Evidence {
		if v.Verdict == record.VerdictRefuted && e.Path == "load.go" && e.Origin == "caller" && e.Symbol == "Open" && slices.Contains(v.Counterevidence, e.ID) {
			t.Logf("actual verifier model=%s verdict=%s caller=%s lines=%d-%d calls=%d snapshot=%s", model, v.Verdict, e.Path, e.Start, e.End, len(rec.Calls)-2, rec.Snapshot)
			return
		}
	}
	t.Fatalf("expected refuted with Load caller citation, verdict=%s references=%v", v.Verdict, v.Counterevidence)
}
