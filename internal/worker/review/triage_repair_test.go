package review

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type triageCall struct {
	Groups []triageGroupInput `json:"groups"`
	Scope  struct {
		Excerpts []record.Excerpt `json:"excerpts"`
	} `json:"scope"`
	Repair *triageRepair `json:"repair"`
}

// answers is the valid answer of every group of in, by group ID.
func triageAnswers(t *testing.T, n int, q opencode.Request) (map[string]map[string]any, []string) {
	t.Helper()
	var out struct {
		Triage []map[string]any `json:"triage"`
	}
	if err := json.Unmarshal(triageReviewScript(t)["triage"](n, q).Output, &out); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	var order []string
	for _, a := range out.Triage {
		id := a["group_id"].(string)
		byID[id] = a
		order = append(order, id)
	}
	return byID, order
}

func triageRecord(t *testing.T, run triageRun) record.Record {
	t.Helper()
	var rec record.Record
	b, err := os.ReadFile(recordPath(run.j, "triage.t001"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func reply(answers map[string]map[string]any, order []string) opencode.Reply {
	out := []map[string]any{}
	for _, id := range order {
		if a, ok := answers[id]; ok {
			out = append(out, a)
		}
	}
	return asJSON(map[string]any{"triage": out})
}

// TestTriageRepairCarriesTheRemainingGroups: the repair holds only the
// groups still without an accepted answer (and their excerpts), and a group
// whose form fault the repair answer leaves unanswered is left out as a
// form fault (triage_item_omitted), not as an integrity fault.
func TestTriageRepairCarriesTheRemainingGroups(t *testing.T) {
	script := triageReviewScript(t)
	var repair triageCall
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		answers, order := triageAnswers(t, n, q)
		if in.Repair == nil {
			answers[order[0]]["classification"] = "maybe"
			return reply(answers, order)
		}
		repair = in
		delete(answers, order[0])
		return reply(answers, order)
	}
	run := runTriageReport(t, script, 0, Budget{})
	if repair.Repair == nil {
		t.Fatal("no repair")
	}
	var ids []string
	for _, g := range repair.Groups {
		ids = append(ids, g.ID)
	}
	if !slices.Equal(ids, repair.Repair.Groups) || len(ids) != 1 {
		t.Fatalf("repair groups %v, remaining %v", ids, repair.Repair.Groups)
	}
	for _, x := range repair.Scope.Excerpts {
		if !slices.Contains(repair.Groups[0].Context, x.ID) {
			t.Fatalf("repair excerpt %s outside the remaining group", x.ID)
		}
	}
	rec := triageRecord(t, run)
	if g := rec.TriageGroups[0]; g.Omitted != record.TriageItemOmitted {
		t.Fatalf("group %s omitted as %q, want %q", g.ID, g.Omitted, record.TriageItemOmitted)
	}
}

// TestTriageCutAnswerRetriesWithHalfTheGroups: an answer that is not JSON
// (an output the token limit cut is the usual case) is a fault of form: the
// retry asks for the first half of the groups, the rest are left out as form
// (triage_item_omitted), and the slot succeeds with what came back.
func TestTriageCutAnswerRetriesWithHalfTheGroups(t *testing.T) {
	script := triageReviewScript(t)
	var sizes []int
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(in.Groups))
		if in.Repair == nil {
			return opencode.Reply{Reason: "model_output_invalid"}
		}
		answers, order := triageAnswers(t, n, q)
		return reply(answers, order)
	}
	run := runTriageReport(t, script, 0, Budget{})
	rec := triageRecord(t, run)
	if len(sizes) != 2 || sizes[1] != (sizes[0]+1)/2 {
		t.Fatalf("request group counts %v", sizes)
	}
	if res := run.results["review.triage.t001"]; res.Status != "succeeded" {
		t.Fatalf("triage %+v", res)
	}
	for i, g := range rec.TriageGroups {
		if (i < sizes[1]) != (g.Omitted == "") || i >= sizes[1] && g.Omitted != record.TriageItemOmitted {
			t.Fatalf("group %d: %+v", i, g)
		}
	}
}

// TestTriageCutAnswerTwiceHalvesAgain: a second answer that is not JSON
// halves the groups once more within the role's three calls; the slot
// succeeds with the quarter that came back.
func TestTriageCutAnswerTwiceHalvesAgain(t *testing.T) {
	script := triageReviewScript(t)
	var sizes []int
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(in.Groups))
		if len(sizes) < 3 {
			return opencode.Reply{Reason: "model_output_invalid"}
		}
		answers, order := triageAnswers(t, n, q)
		return reply(answers, order)
	}
	run := runTriageReport(t, script, 0, Budget{})
	if len(sizes) != 3 || sizes[1] != (sizes[0]+1)/2 || sizes[2] != (sizes[1]+1)/2 {
		t.Fatalf("request group counts %v", sizes)
	}
	if res := run.results["review.triage.t001"]; res.Status != "succeeded" {
		t.Fatalf("triage %+v", res)
	}
	omitted := 0
	for _, g := range triageRecord(t, run).TriageGroups {
		if g.Omitted == record.TriageItemOmitted {
			omitted++
		}
	}
	if omitted != sizes[0]-sizes[2] {
		t.Fatalf("%d groups left out as form, want %d", omitted, sizes[0]-sizes[2])
	}
}

// TestTriageDuplicateAnswerIsReported: a second answer for a group in one
// output is a fault even when the first was accepted.
func TestTriageDuplicateAnswerIsReported(t *testing.T) {
	script := triageReviewScript(t)
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		answers, order := triageAnswers(t, n, q)
		return reply(answers, append(order, order[0]))
	}
	rec := triageRecord(t, runTriageReport(t, script, 0, Budget{}))
	if !slices.Contains(rec.Calls[0].OutputErrors, errTriageGroupDuplicate) {
		t.Fatalf("call output errors %v", rec.Calls[0].OutputErrors)
	}
}

// TestTriageKeepsOnlyCitationsAtTheGroup: a problem or harmless answer that
// also cites another group's excerpt is a fault of form; after the repair
// only the citations of the group's own excerpts are kept.
func TestTriageKeepsOnlyCitationsAtTheGroup(t *testing.T) {
	script := triageReviewScript(t)
	calls := 0
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		calls++
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		answers, order := triageAnswers(t, n, q)
		var other string
		for _, x := range in.Scope.Excerpts {
			other = x.ID
		}
		for _, g := range in.Groups {
			a := answers[g.ID]
			if a["classification"] == record.TriageUnclear || slices.Contains(g.Context, other) {
				continue
			}
			a["evidence_ids"] = append(slices.Clone(a["evidence_ids"].([]any)), other)
		}
		return reply(answers, order)
	}
	run := runTriageReport(t, script, 0, Budget{})
	rec := triageRecord(t, run)
	if calls != 2 || !slices.Contains(rec.Calls[0].OutputErrors, errTriageNotAtGroup) {
		t.Fatalf("%d calls, first output errors %v", calls, rec.Calls[0].OutputErrors)
	}
	cands := map[string]record.Candidate{}
	for _, c := range rec.Candidates {
		cands[c.ID] = c
	}
	checked := 0
	for _, g := range rec.TriageGroups {
		var cited []string
		switch g.Classification {
		case record.TriageProblem:
			cited = cands[g.CandidateID].Evidence
		case record.TriageHarmless:
			cited = g.EvidenceIDs
		default:
			continue
		}
		checked++
		for _, id := range cited {
			if !slices.Contains(g.ContextIDs, id) {
				t.Fatalf("group %s (%s) keeps citation %s outside its context %v", g.ID, g.Classification, id, g.ContextIDs)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no problem or harmless group kept: %+v", rec.TriageGroups)
	}
}
