package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const fixtureObservation = "obs_0123456789abcdef0123"

func notJSON() opencode.Reply { return opencode.Reply{Reason: "model_output_invalid"} }

func inconclusiveFor(c record.Candidate, ids ...string) map[string]any {
	return verdict(c.ID, "inconclusive", func(m map[string]any) {
		m["category"], m["severity_reason"], m["impact"] = "error_handling", "재시도 범위가 문서 계약과 어긋날 수 있다.", "실패 처리 방식이 모듈 안내와 다를 수 있다."
		m["next_verification"] = []string{"호출자와 실패 경로를 읽는다."}
		m["unresolved_assumptions"] = []string{"실제 호출자를 발췌에서 확인하지 못했다."}
		m["supporting_evidence_ids"] = ids
	})
}

// A verifier repair that comes back as non-JSON gets one retry of its own:
// the candidate rejected in the first call is verified by the third call
// instead of being quarantined, and the retry carries the earlier item codes.
func TestVerifyNonJSONRepairGetsRetry(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		switch n {
		case 1:
			var out []map[string]any
			for _, c := range in.Candidates {
				if c.Origin == "interpret" {
					// supported on an observation ID only: no code evidence,
					// so the entry is rejected rather than trimmed.
					out = append(out, verdict(c.ID, "supported", func(m map[string]any) {
						m["valid_conditions"] = []string{"재시도 분기에서 결과를 버린다."}
						m["supporting_evidence_ids"] = []string{fixtureObservation}
					}))
					continue
				}
				out = append(out, inconclusiveFor(c, c.Evidence[0]))
			}
			return asJSON(map[string]any{"verifications": out})
		case 2:
			return notJSON()
		default:
			var out []map[string]any
			for _, c := range in.Candidates {
				out = append(out, inconclusiveFor(c, c.Evidence[0]))
			}
			return asJSON(map[string]any{"verifications": out})
		}
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskSucceeded || f.calls("verify") != 3 {
		t.Fatalf("verify %+v calls=%d", r, f.calls("verify"))
	}
	v := readRecord(t, j, "verify")
	if len(v.Calls) != 3 || v.Calls[0].Outcome != callAccepted || v.Calls[1].Outcome != callRejected || !slices.Equal(v.Calls[1].OutputErrors, []string{"output_not_json"}) || v.Calls[2].Outcome != callAccepted {
		t.Fatalf("calls %+v", v.Calls)
	}
	if slices.Contains(v.Calls[0].OutputErrors, "evidence_id_dropped") || !slices.Contains(v.Calls[0].OutputErrors, "evidence_id_unknown") {
		t.Fatalf("first call %+v", v.Calls[0].OutputErrors)
	}
	for _, x := range v.Verifications {
		if x.Verdict == record.VerdictUnreviewed {
			t.Fatalf("unreviewed %+v", v.Verifications)
		}
	}
	if slices.Contains(v.Limitations, record.LimQuarantined) {
		t.Fatalf("limitations %v", v.Limitations)
	}
	var verifyReqs []opencode.Request
	for _, q := range f.requests {
		if q.Role == "verify" {
			verifyReqs = append(verifyReqs, q)
		}
	}
	retry := decodeAs[verifyInput](t, verifyReqs[2])
	if retry.Repair == nil || !slices.Equal(retry.Repair.OutputErrors, []string{"output_not_json"}) || len(retry.Repair.Candidates) != 1 ||
		retry.Repair.Candidates[0].ID != "interpret_1" || !slices.Contains(retry.Repair.Candidates[0].Errors, "evidence_id_unknown") ||
		len(retry.Candidates) != 1 || !strings.Contains(verifyReqs[2].Instructions, "Format repair") {
		t.Fatalf("retry %+v", retry.Repair)
	}
}

// An entry whose only defect is a non-evidence ID (an observation ID in the
// evidence list) is accepted without it when the rest meets its verdict's
// rule; the call records evidence_id_dropped and no repair is sent.
func TestVerifyDropsNonEvidenceID(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []map[string]any
		for _, c := range in.Candidates {
			ids := []string{c.Evidence[0]}
			if c.Origin == "interpret" {
				ids = append(ids, fixtureObservation)
			}
			out = append(out, inconclusiveFor(c, ids...))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskSucceeded || f.calls("verify") != 1 {
		t.Fatalf("verify %+v calls=%d", r, f.calls("verify"))
	}
	v := readRecord(t, j, "verify")
	if v.Calls[0].Outcome != callAccepted || !slices.Equal(v.Calls[0].OutputErrors, []string{"evidence_id_dropped"}) {
		t.Fatalf("call %+v", v.Calls[0])
	}
	for _, x := range v.Verifications {
		if slices.Contains(x.SupportingEvidence, fixtureObservation) || len(x.SupportingEvidence) != 1 {
			t.Fatalf("verification %+v", x)
		}
	}
}

// Two non-JSON replies end the verifier after two calls; nothing is invented.
func TestVerifyTwoNonJSONRepliesFail(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["verify"] = func(int, opencode.Request) opencode.Reply { return notJSON() }
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskFailed || r.Reason != reasonOutputInvalid || f.calls("verify") != 2 {
		t.Fatalf("verify %+v calls=%d", r, f.calls("verify"))
	}
	v := readRecord(t, j, "verify")
	for _, x := range v.Verifications {
		if x.Verdict != record.VerdictUnreviewed {
			t.Fatalf("%+v", v.Verifications)
		}
	}
}

// The other roles: a rejected output, then a non-JSON repair, then a valid
// output is accepted on the third call; the retry keeps the repair's codes.
// Two rejected outputs still fail after two calls
// (TestReviewFabricatedEvidenceIsQuarantined).
func TestNonJSONRepairGetsRetry(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	valid := script["documents"]
	script["documents"] = func(n int, q opencode.Request) opencode.Reply {
		switch n {
		case 1:
			return asJSON(map[string]any{"contracts": []map[string]any{{"statement": "재시도한다.", "applies_to": ".", "evidence_ids": []string{"ev_000000000000000000000000"}}},
				"conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}})
		case 2:
			return notJSON()
		default:
			return valid(n, q)
		}
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{}, f, "1.27.4")
	if r := res["review.documents"]; r.Status != domain.TaskSucceeded || f.calls("documents") != 3 {
		t.Fatalf("documents %+v calls=%d", r, f.calls("documents"))
	}
	docs := readRecord(t, j, "documents")
	if !docs.Accepted() || len(docs.Calls) != 3 || docs.Calls[2].Outcome != callAccepted || docs.Calls[2].Attempt != 3 {
		t.Fatalf("documents record %+v", docs.Calls)
	}
	var docReqs []opencode.Request
	for _, q := range f.requests {
		if q.Role == "documents" {
			docReqs = append(docReqs, q)
		}
	}
	retry := decodeAs[docsInput](t, docReqs[2])
	if retry.Repair == nil || !slices.Equal(retry.Repair.OutputErrors, []string{"evidence_id_unknown", "output_not_json"}) {
		t.Fatalf("retry %+v", retry.Repair)
	}
}

// A non-JSON first reply is retried once; two non-JSON replies fail.
func TestNonJSONTwiceFails(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["environment"] = func(int, opencode.Request) opencode.Reply { return notJSON() }
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{}, f, "1.27.4")
	if r := res["review.environment"]; r.Status != domain.TaskFailed || r.Reason != reasonOutputInvalid || f.calls("environment") != 2 {
		t.Fatalf("environment %+v calls=%d", r, f.calls("environment"))
	}
}

// Only observation IDs of the request are trimmed: a fabricated evidence ID
// beside a valid one keeps the entry rejected, and the repair verifies it.
func TestVerifyFabricatedIDBesideValidIsRepaired(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []map[string]any
		for _, c := range in.Candidates {
			ids := []string{c.Evidence[0]}
			if n == 1 && c.Origin == "interpret" {
				ids = append(ids, "ev_000000000000000000000000")
			}
			out = append(out, inconclusiveFor(c, ids...))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskSucceeded || f.calls("verify") != 2 {
		t.Fatalf("verify %+v calls=%d", r, f.calls("verify"))
	}
	v := readRecord(t, j, "verify")
	if slices.Contains(v.Calls[0].OutputErrors, "evidence_id_dropped") || !slices.Contains(v.Calls[0].OutputErrors, "evidence_id_unknown") {
		t.Fatalf("first call %+v", v.Calls[0])
	}
	for _, x := range v.Verifications {
		if slices.Contains(x.SupportingEvidence, "ev_000000000000000000000000") {
			t.Fatalf("fabricated evidence kept %+v", x)
		}
	}
}

// A later verify call that fails (here a timeout) keeps the verdict already
// accepted; the other candidate is quarantined and the task is incomplete.
// A failing first call still fails the whole stage.
func TestVerifyLaterCallFailureKeepsAccepted(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		if n > 1 {
			return opencode.Reply{Reason: "model_timeout"}
		}
		in := decodeAs[verifyInput](t, q)
		var out []map[string]any
		for _, c := range in.Candidates {
			if c.Origin == "interpret" {
				out = append(out, inconclusiveFor(c, "ev_000000000000000000000000"))
				continue
			}
			out = append(out, inconclusiveFor(c, c.Evidence[0]))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskFailed || r.Reason != "review_output_incomplete" || f.calls("verify") != 2 {
		t.Fatalf("verify %+v calls=%d", r, f.calls("verify"))
	}
	v := readRecord(t, j, "verify")
	if len(v.Calls) != 2 || v.Calls[1].Outcome != "model_timeout" || !slices.Contains(v.Limitations, "verification_call_failed") || !slices.Contains(v.Limitations, record.LimQuarantined) {
		t.Fatalf("record %+v %v", v.Calls, v.Limitations)
	}
	if v.Verifications[0].Verdict != record.VerdictInconclusive || v.Verifications[1].Verdict != record.VerdictUnreviewed {
		t.Fatalf("verifications %+v", v.Verifications)
	}

	j = modulesJob(t, nil)
	script["verify"] = func(int, opencode.Request) opencode.Reply { return opencode.Reply{Reason: "model_timeout"} }
	f = &scriptedCaller{script: script}
	_, res = runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskTimedOut || f.calls("verify") != 1 {
		t.Fatalf("first-call timeout %+v calls=%d", r, f.calls("verify"))
	}
}
