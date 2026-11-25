package review

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func groupOfPath(in triageCall, path string) (triageGroupInput, bool) {
	i := slices.IndexFunc(in.Groups, func(g triageGroupInput) bool { return g.Path == path })
	if i < 0 {
		return triageGroupInput{}, false
	}
	return in.Groups[i], true
}

// unclearScript answers the group of b.go as unclear with missing, and every
// other group as the standard script does; after is called for each later
// answer (the repair) to change it.
func unclearScript(t *testing.T, missing []map[string]any, after func(in triageCall, answers map[string]map[string]any, b triageGroupInput)) map[string]func(int, opencode.Request) opencode.Reply {
	script := triageReviewScript(t)
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		answers, order := triageAnswers(t, n, q)
		b, ok := groupOfPath(in, "b.go")
		if !ok {
			return reply(answers, order)
		}
		a := map[string]any{"group_id": b.ID, "classification": record.TriageUnclear, "reason": "정의가 입력에 없어 판단할 수 없다.", "evidence_ids": []string{}}
		if missing != nil {
			a["missing"] = missing
		}
		answers[b.ID] = a
		if in.Repair != nil && after != nil {
			after(in, answers, b)
		}
		return reply(answers, order)
	}
	return script
}

// B: an unclear answer that names a definition the request holds is a fault
// of form. The repair points at the excerpt that holds it and the answer after
// it may cite that excerpt.
func TestUnclearNamingAPresentDefinitionIsRepairedWithAPointer(t *testing.T) {
	var repair triageCall
	script := unclearScript(t, []map[string]any{{"kind": "definition", "name": "A0"}}, func(in triageCall, answers map[string]map[string]any, b triageGroupInput) {
		repair = in
		pointed := ""
		for _, it := range in.Repair.Items {
			if it.Error == errTriageUnclearPresent {
				pointed = it.ID
			}
		}
		answers[b.ID] = map[string]any{"group_id": b.ID, "classification": record.TriageHarmless, "reason": "A0의 본문이 값을 쓰지 않아 진단이 가리키는 값은 영향이 없다.",
			"evidence_ids": []string{b.Context[0], pointed}}
	})
	run := runTriageReport(t, script, 0, Budget{})
	if repair.Repair == nil || !slices.Contains(repair.Repair.OutputErrors, errTriageUnclearPresent) {
		t.Fatalf("no repair for the contradicted answer: %+v", repair.Repair)
	}
	var pointed string
	for _, it := range repair.Repair.Items {
		if it.Error == errTriageUnclearPresent {
			pointed = it.ID
			if it.Field != "missing" || it.List != "triage" {
				t.Fatalf("item %+v", it)
			}
		}
	}
	if pointed == "" || !slices.ContainsFunc(repair.Scope.Excerpts, func(e record.Excerpt) bool { return e.ID == pointed && e.Path == "a.go" }) {
		t.Fatalf("the repair does not carry the excerpt that holds the definition: %q", pointed)
	}
	rec := triageRecord(t, run)
	if res := run.results["review.triage.t001"]; res.Status != domain.TaskSucceeded {
		t.Fatalf("%+v", res)
	}
	found := false
	for _, g := range rec.TriageGroups {
		if g.Classification == record.TriageHarmless && slices.Contains(g.EvidenceIDs, pointed) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the repaired answer is not kept: %+v", rec.TriageGroups)
	}
}

// B: when the answer after the repair still says a definition the request
// holds is missing, the group is left out like any form fault the repair did
// not resolve, and the slot keeps the classified groups.
func TestUnclearThatContradictsTheRequestIsLeftOutAfterTheRepair(t *testing.T) {
	script := unclearScript(t, []map[string]any{{"kind": "definition", "name": "A0"}}, nil)
	run := runTriageReport(t, script, 0, Budget{})
	rec := triageRecord(t, run)
	if res := run.results["review.triage.t001"]; res.Status != domain.TaskSucceeded {
		t.Fatalf("%+v", res)
	}
	omitted := 0
	for _, g := range rec.TriageGroups {
		if g.Omitted == record.TriageItemOmitted {
			omitted++
			if g.Classification != "" {
				t.Fatalf("%+v", g)
			}
		}
	}
	if omitted != 1 || !slices.Contains(rec.Limitations, record.LimTriageGroupsNotTriaged) || len(rec.Calls) != 2 || !slices.Contains(rec.Calls[0].OutputErrors, errTriageUnclearPresent) {
		t.Fatalf("omitted %d, calls %+v, limitations %v", omitted, rec.Calls, rec.Limitations)
	}
}

// B: a definition the request does not hold is a legitimate unclear answer,
// and so is a malformed or absent missing list once the repair is spent (the
// answer stands and the limitation says what was not named).
func TestUnclearNamingAnAbsentDefinitionStands(t *testing.T) {
	for name, missing := range map[string][]map[string]any{
		"absent definition": {{"kind": "definition", "name": "NoSuchFunction"}},
		"external":          {{"kind": "external_behavior", "name": ""}},
	} {
		run := runTriageReport(t, unclearScript(t, missing, nil), 0, Budget{})
		rec := triageRecord(t, run)
		if len(rec.Calls) != 1 || slices.Contains(rec.Limitations, limTriageMissingUnstated) {
			t.Fatalf("%s: calls %d, limitations %v", name, len(rec.Calls), rec.Limitations)
		}
	}
	run := runTriageReport(t, unclearScript(t, nil, nil), 0, Budget{})
	rec := triageRecord(t, run)
	if len(rec.Calls) != 2 || !slices.Contains(rec.Calls[0].OutputErrors, errTriageMissingInvalid) || !slices.Contains(rec.Limitations, limTriageMissingUnstated) {
		t.Fatalf("calls %+v, limitations %v", rec.Calls, rec.Limitations)
	}
	for _, g := range rec.TriageGroups {
		if g.Omitted != "" {
			t.Fatalf("a group is left out: %+v", g)
		}
	}
}

// partScript answers the group of b.go part by part with the given
// diagnostics of each part (the first harmless, the second a problem).
func partScript(t *testing.T, parts [][]int) map[string]func(int, opencode.Request) opencode.Reply {
	script := triageReviewScript(t)
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		answers, order := triageAnswers(t, n, q)
		b, ok := groupOfPath(in, "b.go")
		if !ok {
			return reply(answers, order)
		}
		out := []map[string]any{}
		for _, id := range order {
			if id != b.ID {
				if a, ok := answers[id]; ok {
					out = append(out, a)
				}
				continue
			}
			for i, positions := range parts {
				a := map[string]any{"group_id": b.ID, "diagnostics": positions, "evidence_ids": []string{b.Context[0]}}
				if i == 0 {
					a["classification"], a["reason"] = record.TriageHarmless, "앞선 검사가 이 경로를 막아 값은 쓰이지 않는다."
				} else {
					a["classification"], a["reason"] = record.TriageProblem, "호출자가 항상 유효한 값을 넘기면 무해하다."
					a["title"], a["condition"], a["impact"], a["improvement"] = "SA4006 진단 위치에서 값이 잘못 쓰인다", "진단이 가리키는 분기에 들어오는 경우", "호출자가 잘못된 결과를 받는다.", "값을 검사한다."
				}
				out = append(out, a)
			}
		}
		return asJSON(map[string]any{"triage": out})
	}
	return script
}

// D: a group answered part by part is stored as one group per part, with its
// own observations and one candidate per problem; the report counts every
// diagnostic once.
func TestAGroupAnsweredPartByPartIsStoredAsOneGroupPerPart(t *testing.T) {
	run := runTriageReport(t, partScript(t, [][]int{{1, 3}, {2}}), 0, Budget{})
	rec := triageRecord(t, run)
	if res := run.results["review.triage.t001"]; res.Status != domain.TaskSucceeded {
		t.Fatalf("%+v", res)
	}
	if len(rec.Calls) != 1 {
		t.Fatalf("calls %+v", rec.Calls)
	}
	// b.go's three diagnostics: two harmless in one group, one problem with
	// its own candidate; the groups are numbered in order.
	harmless, problems := 0, 0
	for i, g := range rec.TriageGroups {
		if want := "g" + strconv.Itoa(i+1); g.ID != want {
			t.Fatalf("group id %s, want %s", g.ID, want)
		}
		switch {
		case g.Classification == record.TriageHarmless && len(g.ObservationIDs) == 2 && g.Reason == "앞선 검사가 이 경로를 막아 값은 쓰이지 않는다.":
			harmless++
		case g.Classification == record.TriageProblem && len(g.ObservationIDs) == 1 && g.CandidateID != "":
			problems++
		}
	}
	if harmless != 1 || problems == 0 {
		t.Fatalf("groups %+v", rec.TriageGroups)
	}
	d := run.report.CodeReview.DiagnosticTriage
	if d == nil || d.Triaged+d.Interpreted != d.Diagnostics {
		t.Fatalf("counts %+v", d)
	}
}

// D: the positions are validated: a position listed twice leaves the whole
// group out after the repair; positions no part lists are stored as a group
// without a classification.
func TestPartPositionsAreValidated(t *testing.T) {
	run := runTriageReport(t, partScript(t, [][]int{{1, 2}, {2, 3}}), 0, Budget{})
	rec := triageRecord(t, run)
	if len(rec.Calls) != 2 || !slices.Contains(rec.Calls[0].OutputErrors, errTriageDiagnostics) {
		t.Fatalf("calls %+v", rec.Calls)
	}
	left := 0
	for _, g := range rec.TriageGroups {
		if g.Omitted == record.TriageItemOmitted && len(g.ObservationIDs) == 3 {
			left++
		}
	}
	if left != 1 {
		t.Fatalf("groups %+v", rec.TriageGroups)
	}
	run = runTriageReport(t, partScript(t, [][]int{{1}, {2}}), 0, Budget{})
	rec = triageRecord(t, run)
	if len(rec.Calls) != 1 {
		t.Fatalf("calls %+v", rec.Calls)
	}
	missing := 0
	for _, g := range rec.TriageGroups {
		if g.Omitted == record.TriageAnswerMissing && len(g.ObservationIDs) == 1 {
			missing++
		}
	}
	if missing != 1 || !slices.Contains(rec.Limitations, record.LimTriageGroupsNotTriaged) {
		t.Fatalf("groups %+v", rec.TriageGroups)
	}
}

// C: a request names the path class of a group and the severity the report
// gives its findings; the groups on shipped code come first.
func TestTriageRequestNamesPathClassAndReportSeverity(t *testing.T) {
	s := triageRequestOf(t)
	if len(s) == 0 {
		t.Fatal("no group sent")
	}
	for _, g := range s {
		if g.PathClass != "" || g.DefaultSeverity == "" {
			t.Fatalf("%+v", g)
		}
		for i, e := range g.Examples {
			if e.Index != i+1 {
				t.Fatalf("example index %d at %d", e.Index, i)
			}
		}
	}
}

func triageRequestOf(t *testing.T) []triageGroupInput {
	t.Helper()
	var sent []triageGroupInput
	script := triageReviewScript(t)
	whole := script["triage"]
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		if in.Repair == nil && sent == nil {
			sent = in.Groups
		}
		return whole(n, q)
	}
	runTriageReport(t, script, 0, Budget{})
	return sent
}
