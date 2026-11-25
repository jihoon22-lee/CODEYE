package review

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type taskRow struct {
	key, status, reason string
	required            bool
}

func rows(plan []analysis.TaskSpec, res map[string]analysis.TaskResult) []taskRow {
	var out []taskRow
	for _, p := range plan {
		r := res[p.Key]
		out = append(out, taskRow{p.Key, string(r.Status), r.Reason, p.Required})
	}
	return out
}

// The review plan (docs/REVIEW.md): discovery, static analysis, then the five roles
// with their stages; documents is required only with guidance documents in
// scope.
func TestReviewPlanOrderStagesAndRequired(t *testing.T) {
	j := modulesJob(t, nil)
	h, err := discovery.Wrap(Handler{Static: vetStub{}}, discovery.Config{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	type spec struct {
		key      string
		stage    domain.Stage
		required bool
	}
	var got []spec
	for _, p := range plan {
		got = append(got, spec{p.Key, p.Stage, p.Required})
	}
	want := []spec{{"env.discover", domain.StageEnvironment, true}, {"docs.discover", domain.StageDocuments, true}, {"go-vet", domain.StageStaticAnalysis, true},
		{"review.environment", domain.StageEnvironment, false}, {"review.documents", domain.StageDocuments, true}, {"review.investigate", domain.StageCodeReview, true},
		{"review.interpret", domain.StageCodeReview, false}, {"review.verify", domain.StageCodeReview, true}}
	want = append(want, spec{record.TaskScope, domain.StageCodeReview, false})
	slots := coverageSlots(planSlices(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary))
	for i := range slots {
		inv, ver := slotTaskKeys(sliceKey(i))
		want = append(want, spec{inv, domain.StageCodeReview, false}, spec{ver, domain.StageCodeReview, false})
	}
	for _, k := range triagePlanKeys(j) {
		want = append(want, spec{k, domain.StageCodeReview, false})
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan %v", got)
	}
	// Documents outside the scope do not make the role required.
	j.Scope = domain.Scope{Kind: "paths", Paths: []string{"svc/a"}}
	plan, _ = Handler{Static: vetStub{}}.Plan(t.Context(), j)
	for _, p := range plan {
		if p.Key == "review.documents" && p.Required {
			t.Fatal("documents role required without documents in scope")
		}
	}
}

// Valid outputs pass through every role; downstream roles receive only the
// accepted upstream output and the records bind upstream artifacts by digest.
func TestRolesAcceptValidOutputs(t *testing.T) {
	j := modulesJob(t, nil)
	f := &scriptedCaller{script: validScript(t)}
	plan, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	for _, r := range rows(plan, res) {
		if r.status == "skipped_policy" && r.reason == domain.ReasonNoReviewScope && !r.required {
			continue
		}
		if r.status != "succeeded" {
			t.Fatalf("%+v", rows(plan, res))
		}
	}
	for _, role := range []string{"environment", "documents", "investigate", "interpret", "verify"} {
		if f.calls(role) != 1 {
			t.Fatalf("%s calls %d", role, f.calls(role))
		}
	}
	env := readRecord(t, j, "environment")
	if len(env.EnvironmentNotes) != 1 || env.EnvironmentNotes[0].Module != "go:svc/b" || env.Upstream[0].Source != "env.discover" || env.Upstream[0].State != record.UpstreamAccepted ||
		env.Pack.ID != "review" || env.Calls[0].PackID != "review" || env.Calls[0].PackVersion != "2.10.0" {
		t.Fatalf("environment %+v", env)
	}
	docs := readRecord(t, j, "documents")
	if len(docs.Contracts) != 2 || len(docs.Conflicts) != 1 || len(docs.InstructionNotes) != 1 {
		t.Fatalf("documents %+v", docs)
	}
	var paths []string
	for _, e := range docs.Evidence {
		paths = append(paths, e.Path)
	}
	if !slices.Equal(paths, []string{"AGENTS.md", "README.md", "svc/b/README.md", "docs/design.md"}) {
		t.Fatalf("documents given %v", paths)
	}
	inv := readRecord(t, j, "investigate")
	if len(inv.Candidates) != 1 || inv.Candidates[0].ID != "investigate_1" || inv.Upstream[1].SHA256 != digestOf(mustRead(t, recordPath(j, "documents"))) {
		t.Fatalf("investigate %+v", inv)
	}
	// Module-aware corpus: every Go module is represented.
	var code []string
	for _, e := range inv.Evidence {
		if e.Kind == "code" {
			code = append(code, e.Path)
		}
	}
	if !slices.Equal(code, []string{"gateway.go", "svc/a/normalize.go", "svc/b/charge.go"}) {
		t.Fatalf("code corpus %v", code)
	}
	in := decodeAs[investigateInput](t, f.requests[2])
	if in.Documents.State != "accepted" || len(in.Documents.Contracts) != 2 || in.Environment.State != "accepted" || len(in.Environment.Notes) != 1 {
		t.Fatalf("investigate handoff %+v", in)
	}
	// No raw tool diagnostics reach the independent investigation (REQ-009).
	if strings.Contains(string(f.requests[2].Input), "obs_") || strings.Contains(string(f.requests[2].Input), "unusedresult") {
		t.Fatal("tool observations leaked into investigate")
	}
	interp := readRecord(t, j, "interpret")
	if len(interp.Candidates) != 1 || interp.Candidates[0].ID != "interpret_1" || interp.Candidates[0].ObservationIDs[0] != "obs_0123456789abcdef0123" || len(interp.Observations) != 1 {
		t.Fatalf("interpret %+v", interp)
	}
	v := readRecord(t, j, "verify")
	if len(v.Candidates) != 2 || v.Candidates[0].ID != "investigate_1" || v.Candidates[1].ID != "interpret_1" || len(v.Verifications) != 2 || v.Verifications[1].Verdict != "inconclusive" {
		t.Fatalf("verify %+v", v)
	}
	for _, q := range f.requests {
		if n := len(opencode.Prompt(q)); n > PromptBudget {
			t.Fatalf("%s request %d bytes", q.Role, n)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := readBounded(p, record.MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A fabricated evidence ID fails the role after one bounded repair; its
// output is quarantined, the discovery facts stay, and the next role gets
// only the upstream state with a limitation.
func TestReviewFabricatedEvidenceIsQuarantined(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["documents"] = func(int, opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"contracts": []map[string]any{{"statement": "재시도한다.", "applies_to": ".", "evidence_ids": []string{"ev_000000000000000000000000"}}},
			"conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{}, f, "1.27.4")
	if r := res["review.documents"]; r.Status != domain.TaskFailed || r.Reason != reasonOutputInvalid || f.calls("documents") != 2 {
		t.Fatalf("documents %+v calls=%d", r, f.calls("documents"))
	}
	docs := readRecord(t, j, "documents")
	if docs.Accepted() || len(docs.Contracts) != 0 || len(docs.Calls) != 2 || !slices.Contains(docs.Calls[0].OutputErrors, "evidence_id_unknown") || docs.Calls[1].Outcome != "output_rejected" {
		t.Fatalf("documents record %+v", docs)
	}
	// The repair request carries only fixed codes.
	repair := decodeAs[docsInput](t, f.requests[2])
	if repair.Repair == nil || !slices.Equal(repair.Repair.OutputErrors, []string{"evidence_id_unknown"}) || !strings.Contains(f.requests[2].Instructions, "Format repair") {
		t.Fatalf("repair %+v", repair.Repair)
	}
	if res["docs.discover"].Status != domain.TaskSucceeded || res["env.discover"].Status != domain.TaskSucceeded {
		t.Fatal("discovery facts lost")
	}
	in := decodeAs[investigateInput](t, f.requests[3])
	if in.Documents.State != "failed" || in.Documents.Reason != reasonOutputInvalid || len(in.Documents.Contracts) != 0 {
		t.Fatalf("investigate got %+v", in.Documents)
	}
	inv := readRecord(t, j, "investigate")
	if !slices.Contains(inv.Limitations, "upstream_documents_failed") || res["review.investigate"].Status != domain.TaskSucceeded {
		t.Fatalf("investigate %+v", inv.Limitations)
	}
}

// A sub-directory rule may not be generalized to the repository; one repair
// that narrows the scope is accepted.
func TestReviewScopeGeneralizationRejected(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["documents"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		scope := "."
		if n == 2 {
			scope = "svc/b"
		}
		return asJSON(map[string]any{"contracts": []map[string]any{{"statement": "결제 호출은 한 번만 한다.", "applies_to": scope, "evidence_ids": []string{docID(in, "svc/b/README.md")}}},
			"conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{}, f, "1.27.4")
	docs := readRecord(t, j, "documents")
	if res["review.documents"].Status != domain.TaskSucceeded || len(docs.Calls) != 2 || !slices.Equal(docs.Calls[0].OutputErrors, []string{"scope_generalized"}) ||
		docs.Calls[1].Outcome != "accepted" || docs.Contracts[0].AppliesTo != "svc/b" {
		t.Fatalf("%+v %+v", res["review.documents"], docs)
	}
	// Record validation repeats the rule.
	bad := docs
	bad.Contracts = append([]record.Contract{}, docs.Contracts...)
	bad.Contracts[0].AppliesTo = "."
	if record.Validate(bad, expectStage(j, "documents")) == nil {
		t.Fatal("record accepted a generalized contract")
	}
	// An environment note may not cite another module's requirement.
	script = validScript(t)
	script["environment"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[envInput](t, q)
		r := in.Requirements[0]
		return asJSON(map[string]any{"notes": []map[string]any{{"module": "go:svc/a", "requirement_ids": []string{r.ID}, "evidence_ids": []string{r.EvidenceID},
			"affected_checks": "svc/a도 검사할 수 없다.", "still_checkable": "없음", "needed_observation": "go 1.30"}}})
	}
	j = modulesJob(t, nil)
	f = &scriptedCaller{script: script}
	_, res = runRole(t, j, vetStub{}, f, "1.27.4")
	env := readRecord(t, j, "environment")
	if res["review.environment"].Status != domain.TaskFailed || !slices.Contains(env.Calls[0].OutputErrors, "module_out_of_scope") || len(env.EnvironmentNotes) != 0 {
		t.Fatalf("%+v %+v", res["review.environment"], env.Calls)
	}
}

// Model prose claiming execution or reproduction is rejected; a reproduced
// verdict is downgraded to a candidate verdict.
func TestReviewExecutionAndReproductionClaimsRejected(t *testing.T) {
	for _, s := range []string{"테스트를 실행했고 결함을 재현했다.", "We ran the tests.", "go test로 재현됨을 확인했음", "테스트가 통과했다."} {
		if !record.ClaimsExecution(s) {
			t.Errorf("claim not detected: %s", s)
		}
	}
	for _, s := range []string{"코드를 실행하지 않았다.", "실행 결과를 확인할 수 없다.", "테스트를 실행했다는 근거는 없다.", "재현했다고 볼 수 없다.", "Forward는 send를 최대 네 번 실행한다.", "the result was not reproduced"} {
		if record.ClaimsExecution(s) {
			t.Errorf("negation or description flagged: %s", s)
		}
	}
	j := modulesJob(t, nil)
	script := validScript(t)
	valid := script["investigate"]
	script["investigate"] = func(n int, q opencode.Request) opencode.Reply {
		r := valid(n, q)
		return opencode.Reply{Output: []byte(strings.Replace(string(r.Output), "결제가 중복될 수 있다.", "테스트를 실행해 결제 중복을 재현했다.", 1))}
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []map[string]any
		for _, c := range in.Candidates {
			out = append(out, verdict(c.ID, "reproduced", func(m map[string]any) {
				m["valid_conditions"] = []string{"결제 경로가 Forward를 쓰는 경우"}
				m["supporting_evidence_ids"] = []string{c.Evidence[0]}
			}))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	inv := readRecord(t, j, "investigate")
	if res["review.investigate"].Status != domain.TaskFailed || !slices.Contains(inv.Calls[0].OutputErrors, "execution_claim") || len(inv.Candidates) != 0 ||
		!slices.Contains(inv.Limitations, record.LimExecutionClaim) {
		t.Fatalf("investigate %+v %+v %v", res["review.investigate"], inv.Calls, inv.Limitations)
	}
	// verify still runs on the accepted interpret candidate; the reproduced
	// verdict becomes inconclusive with reproduction_claim_rejected.
	v := readRecord(t, j, "verify")
	if res["review.verify"].Status != domain.TaskSucceeded || len(v.Candidates) != 1 || v.Candidates[0].ID != "interpret_1" || v.Verifications[0].Verdict != "inconclusive" ||
		!slices.Contains(v.Limitations, record.LimReproductionRejected) || !slices.Contains(v.Limitations, "upstream_investigate_failed") {
		t.Fatalf("verify %+v %+v", res["review.verify"], v)
	}
}

// Skip reasons are decided without a model call; the source policy comes
// first.
func TestReviewSkipReasons(t *testing.T) {
	// Every declared requirement satisfied (node go 1.31.0); no guidance
	// documents; no tool observations.
	f := &scriptedCaller{script: validScript(t)}
	j := modulesNoDocsJob(t)
	plan, res := runRole(t, j, vetStub{}, f, "1.31.0")
	want := map[string]taskRow{
		"review.environment": {"review.environment", "skipped_policy", ReasonNoEnvironmentGap, false},
		"review.documents":   {"review.documents", "skipped_policy", ReasonNoDocuments, false},
		"review.interpret":   {"review.interpret", "skipped_policy", ReasonNoToolObservation, false},
	}
	for _, r := range rows(plan, res) {
		if w, ok := want[r.key]; ok && r != w {
			t.Fatalf("%+v want %+v", r, w)
		}
	}
	if f.calls("environment")+f.calls("documents")+f.calls("interpret") != 0 {
		t.Fatal("a skipped role called the model")
	}
	inv := readRecord(t, j, "investigate")
	if inv.Upstream[0].State != "skipped" || inv.Upstream[0].Reason != ReasonNoEnvironmentGap || inv.Upstream[1].Reason != ReasonNoDocuments ||
		!slices.Contains(inv.Limitations, "upstream_documents_skipped") || res["review.verify"].Status != domain.TaskSucceeded {
		t.Fatalf("%+v %v", inv.Upstream, inv.Limitations)
	}
	// Guidance documents exist but none can be selected (all too large):
	// the required role is skipped with its own reason, not no_documents.
	const line = "긴 설계 문서 줄.\n"
	big := strings.Repeat(line, int(discovery.DefaultBudget.PerDocumentBytes)/len(line)+1)
	j = modulesJob(t, func(root string) {
		for _, name := range []string{"README.md", "AGENTS.md", "svc/b/README.md", "docs/design.md"} {
			writeFile(t, root, name, big)
		}
	})
	plan, res = runRole(t, j, vetStub{}, &scriptedCaller{script: validScript(t)}, "1.27.4")
	for _, p := range plan {
		if p.Key == "review.documents" && (!p.Required || res[p.Key].Status != domain.TaskSkippedPolicy || res[p.Key].Reason != ReasonDocsNotSelected) {
			t.Fatalf("%+v %+v", p, res[p.Key])
		}
	}
	// Model source not approved wins over every no-target reason.
	j = modulesNoDocsJob(t)
	j.AllowModelReview = false
	f = &scriptedCaller{script: validScript(t)}
	plan, res = runRole(t, j, vetStub{}, f, "1.31.0")
	for _, p := range plan {
		if strings.HasPrefix(p.Key, "review.") && (res[p.Key].Status != domain.TaskSkippedPolicy || res[p.Key].Reason != "model_source_not_approved") {
			t.Fatalf("%s %+v", p.Key, res[p.Key])
		}
	}
	if len(f.requests) != 0 {
		t.Fatal("unapproved source sent to the model")
	}
}

func modulesNoDocsJob(t *testing.T) analysis.Job {
	return modulesJob(t, func(root string) {
		for _, name := range []string{"README.md", "AGENTS.md", "svc/b/README.md", "docs/design.md"} {
			if err := os.Remove(filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// A model failure keeps the discovery facts; the downstream role records the
// failed upstream and receives no model content from it.
func TestReviewModelFailureKeepsFactsAndLimitsDownstream(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["environment"] = func(int, opencode.Request) opencode.Reply { return opencode.Reply{Reason: "model_timeout"} }
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{}, f, "1.27.4")
	if r := res["review.environment"]; r.Status != domain.TaskTimedOut || r.Reason != "model_timeout" || len(r.Artifacts) != 1 {
		t.Fatalf("%+v", r)
	}
	env := readRecord(t, j, "environment")
	if env.Accepted() || env.FailureReason != "model_timeout" || len(env.EnvironmentNotes) != 0 || env.Calls[0].Outcome != "model_timeout" {
		t.Fatalf("%+v", env)
	}
	b := mustRead(t, discovery.ArtifactPath(j.Scratch, envdoc.KindEnvironment))
	in := discovery.Input{Manifest: j.Snapshot.Manifest, Digest: j.Snapshot.Digest, Scope: j.Scope, RunID: j.RunID, AttemptID: j.AttemptID}
	if d, err := envdoc.Decode(b, in.Expect(envdoc.KindEnvironment)); err != nil || len(d.Environment.Verdicts) != 3 {
		t.Fatal("discovery facts not preserved", err)
	}
	inv := decodeAs[investigateInput](t, f.requests[2])
	if inv.Environment.State != "failed" || inv.Environment.Reason != "model_timeout" || len(inv.Environment.Notes) != 0 {
		t.Fatalf("%+v", inv.Environment)
	}
}

// Interpret links hypotheses to adapter observation IDs; a fabricated
// observation ID is rejected.
func TestReviewInterpretObservationLinks(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["interpret"] = func(int, opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []any{}, "limits": []map[string]any{{"observation_ids": []string{"obs_ffffffffffffffffffff"}, "statement": "위치를 연결할 수 없다."}}})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	rec := readRecord(t, j, "interpret")
	if res["review.interpret"].Status != domain.TaskFailed || !slices.Contains(rec.Calls[0].OutputErrors, "observation_id_unknown") || len(rec.Observations) != 1 {
		t.Fatalf("%+v %+v", res["review.interpret"], rec)
	}
	in := decodeAs[interpretInput](t, f.requests[3])
	if len(in.Observations) != 1 || in.Observations[0].RuleID != "unusedresult" || len(in.Observations[0].EvidenceIDs) != 1 || in.Tools[0].TaskStatus != "succeeded" {
		t.Fatalf("%+v", in)
	}
	// verify only receives the accepted investigate candidate.
	if v := readRecord(t, j, "verify"); len(v.Candidates) != 1 || v.Candidates[0].Origin != "investigate" || !slices.Contains(v.Limitations, "upstream_interpret_failed") {
		t.Fatalf("%+v", v.Candidates)
	}
}

// The deep-review composition under discovery.Wrap: discovery first, the
// static plan, then the review roles (tested here because package discovery
// cannot import review).
func TestDiscoveryCompositionPlans(t *testing.T) {
	j := modulesJob(t, nil)
	h := Handler{Static: vetStub{}}
	w, err := discovery.Wrap(h, discovery.Config{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := w.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range plan {
		keys = append(keys, p.Key)
	}
	want := []string{"env.discover", "docs.discover", "go-vet", "review.environment", "review.documents", "review.investigate", "review.interpret", "review.verify"}
	want = append(want, record.TaskScope)
	slots := coverageSlots(planSlices(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary))
	for i := range slots {
		inv, ver := slotTaskKeys(sliceKey(i))
		want = append(want, inv, ver)
	}
	want = append(want, triagePlanKeys(j)...)
	if !slices.Equal(keys, want) || w.Info() != h.Info() {
		t.Fatalf("plan %v", keys)
	}
}
