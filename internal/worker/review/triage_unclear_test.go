package review

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
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
// holds is missing, it stands as the model's answer: the group is recorded
// unclear with the limitation that says the platform found the definition,
// nothing is omitted and the slot does not fail for it.
func TestUnclearThatContradictsTheRequestStandsAfterTheRepair(t *testing.T) {
	script := unclearScript(t, []map[string]any{{"kind": "definition", "name": "A0"}}, nil)
	run := runTriageReport(t, script, 0, Budget{})
	rec := triageRecord(t, run)
	if res := run.results["review.triage.t001"]; res.Status != domain.TaskSucceeded {
		t.Fatalf("%+v", res)
	}
	unclear := 0
	for _, g := range rec.TriageGroups {
		if g.Omitted != "" {
			t.Fatalf("a group is left out: %+v", g)
		}
		if g.Classification == record.TriageUnclear && len(g.ObservationIDs) == 3 && g.Reason == "정의가 입력에 없어 판단할 수 없다." {
			unclear++
		}
	}
	if unclear != 1 || !slices.Contains(rec.Limitations, limTriageUnclearDefinitionPresent) || len(rec.Calls) != 2 || !slices.Contains(rec.Calls[0].OutputErrors, errTriageUnclearPresent) {
		t.Fatalf("unclear %d, calls %+v, limitations %v", unclear, rec.Calls, rec.Limitations)
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
				a := map[string]any{"group_id": b.ID, "positions": positions, "evidence_ids": []string{b.Context[0]}}
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

// D: the positions are validated. A part that lists a position twice (or
// outside the group) is a fault of form: after the repair the parts that are
// valid stand and the positions of no valid part are not triaged. Positions no
// part lists are not triaged either, whatever the parts' number.
func TestPartPositionsAreValidated(t *testing.T) {
	run := runTriageReport(t, partScript(t, [][]int{{1, 2}, {2, 3}}), 0, Budget{})
	rec := triageRecord(t, run)
	if len(rec.Calls) != 2 || !slices.Contains(rec.Calls[0].OutputErrors, errTriageDiagnostics) {
		t.Fatalf("calls %+v", rec.Calls)
	}
	kept, notTriaged := 0, 0
	for _, g := range rec.TriageGroups {
		switch {
		case g.Classification == record.TriageHarmless && len(g.ObservationIDs) == 2:
			kept++
		case g.Omitted == record.TriageAnswerMissing && len(g.ObservationIDs) == 1:
			notTriaged++
		}
	}
	if kept != 1 || notTriaged != 1 || !slices.Contains(rec.Limitations, record.LimTriageGroupsNotTriaged) {
		t.Fatalf("groups %+v", rec.TriageGroups)
	}
	// Every part valid and the parts leave a position out: nothing is lost
	// and the one position is not triaged, without a repair.
	run = runTriageReport(t, partScript(t, [][]int{{1}, {2}}), 0, Budget{})
	rec = triageRecord(t, run)
	if len(rec.Calls) != 1 {
		t.Fatalf("calls %+v", rec.Calls)
	}
	missing, classified := 0, 0
	for _, g := range rec.TriageGroups {
		if g.Omitted == record.TriageAnswerMissing && len(g.ObservationIDs) == 1 {
			missing++
		}
		if len(g.ObservationIDs) == 1 && (g.Classification == record.TriageHarmless || g.Classification == record.TriageProblem) {
			classified++
		}
	}
	if missing != 1 || classified != 2 || !slices.Contains(rec.Limitations, record.LimTriageGroupsNotTriaged) {
		t.Fatalf("groups %+v", rec.TriageGroups)
	}
}

// D: validParts keeps the first parts up to the limit, drops the ones that
// overlap or leave the group, and reports what no kept part lists.
func TestValidPartsKeepWhatIsValid(t *testing.T) {
	entry := func(positions string) triageEntry {
		return triageEntry{a: triageAnswer{Positions: json.RawMessage(positions)}}
	}
	kept, positions, uncovered, ok := validParts([]triageEntry{entry("[1]"), entry("[2]"), entry("[3]")}, 6, 2)
	if ok || len(kept) != 2 || len(positions) != 2 || !slices.Equal(uncovered, []int{2, 3, 4, 5}) {
		t.Fatalf("over the limit: %v %v %v %v", len(kept), positions, uncovered, ok)
	}
	kept, _, uncovered, ok = validParts([]triageEntry{entry("[1,2]"), entry("[2]"), entry("[7]"), entry("[3]")}, 3, 4)
	if ok || len(kept) != 2 || len(uncovered) != 0 {
		t.Fatalf("overlap and range: %v %v %v", len(kept), uncovered, ok)
	}
	if _, _, _, ok = validParts([]triageEntry{entry(`"all"`)}, 3, 4); ok {
		t.Fatal("a position field of the wrong type was accepted")
	}
}

// The contract states the number of parts the check enforces.
func TestContractStatesThePartsLimit(t *testing.T) {
	var instructions string
	var groups int
	script := triageReviewScript(t)
	whole := script["triage"]
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		if in.Repair == nil && instructions == "" {
			instructions, groups = q.Instructions, len(in.Groups)
		}
		return whole(n, q)
	}
	runTriageReport(t, script, 0, Budget{})
	want := fmt.Sprintf("a group has at most %d parts", triagePartsLimit(groups))
	if !strings.Contains(instructions, want) || (triagePartsLimit(groups)+1)*groups > record.MaxTriageGroups {
		t.Fatalf("the contract does not say %q (%d groups)", want, groups)
	}
}

// Optional fields of the wrong type, and a request field echoed in an answer,
// are faults of the entry, never a schema failure of the whole output (which
// would cost every group of the slot).
func TestOptionalAnswerFieldsOfTheWrongTypeAreFaultsOfTheEntry(t *testing.T) {
	script := triageReviewScript(t)
	whole := script["triage"]
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		answers, order := triageAnswers(t, n, q)
		if in.Repair == nil {
			for _, g := range in.Groups {
				switch g.Path {
				case "b.go":
					answers[g.ID]["positions"] = 3
					answers[g.ID]["diagnostics"] = 3
				case "d.go":
					answers[g.ID] = map[string]any{"group_id": g.ID, "classification": record.TriageUnclear, "reason": "정의가 입력에 없어 판단할 수 없다.", "missing": "", "evidence_ids": []string{}}
				}
			}
			return reply(answers, order)
		}
		return whole(n, q)
	}
	run := runTriageReport(t, script, 0, Budget{})
	rec := triageRecord(t, run)
	if res := run.results["review.triage.t001"]; res.Status != domain.TaskSucceeded {
		t.Fatalf("%+v", res)
	}
	first := rec.Calls[0].OutputErrors
	if slices.Contains(first, "output_schema_invalid") || !slices.Contains(first, errTriageDiagnostics) || !slices.Contains(first, errTriageMissingInvalid) {
		t.Fatalf("first call output errors %v", first)
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

// A diagnostic of the unused-value family is a lead: the request names the
// family (rule_families) so the instruction can refer to it without a tool
// rule, and the two answers the instruction asks for are accepted as they
// are: a problem that states the concrete input and the result against the
// promised one, a harmless one that shows the value is unused by design.
func TestUnusedValueFamilyAnswersAreAccepted(t *testing.T) {
	var families [][]string
	script := triageReviewScript(t)
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var in triageCall
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		answers, order := triageAnswers(t, n, q)
		for _, g := range in.Groups {
			if g.Path == "b.go" && in.Repair == nil {
				families = append(families, g.Families)
			}
			switch g.Path {
			case "c.go":
				answers[g.ID] = map[string]any{"group_id": g.ID, "classification": record.TriageProblem, "title": "덮어쓴 값 대신 입력이 반환에 쓰인다",
					"condition": "p가 가리키는 값이 0이 아닌 경우: 짝 함수는 그 값을 반환하지만 이 함수는 2를 반환한다", "mechanism": "x가 읽힌 직후 상수로 덮어써져 짝 함수의 패턴(읽은 값을 반환)과 달라진다.",
					"impact": "호출자는 입력과 무관한 값을 받는다.", "improvement": "덮어쓰기를 제거한다.", "reason": "상수 반환이 의도라면 무해하다.", "evidence_ids": []string{g.Context[0]}}
			case "b.go":
				answers[g.ID] = map[string]any{"group_id": g.ID, "classification": record.TriageHarmless, "reason": "쓰지 않는 값은 인터페이스를 맞추려 남긴 것이고 짝 함수도 같은 방식으로 무시한다.",
					"evidence_ids": []string{g.Context[0]}}
			}
		}
		return reply(answers, order)
	}
	run := runTriageReport(t, script, 0, Budget{})
	if len(families) == 0 || !slices.Contains(families[0], "unused_code") {
		t.Fatalf("rule families of the group: %v", families)
	}
	rec := triageRecord(t, run)
	if len(rec.Calls) != 1 || len(rec.Calls[0].OutputErrors) != 0 {
		t.Fatalf("calls %+v", rec.Calls)
	}
}

// Two problem parts of one group (one rule, one file) are two findings with
// distinct, stable fingerprints: a run comparison tells them apart and a
// disposition carries to the part it was given for.
func TestProblemPartsOfOneGroupHaveDistinctFingerprints(t *testing.T) {
	script := partScript(t, [][]int{{1}, {2, 3}})
	whole := script["triage"]
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		var out struct {
			Triage []map[string]any `json:"triage"`
		}
		if err := json.Unmarshal(whole(n, q).Output, &out); err != nil {
			t.Fatal(err)
		}
		for _, a := range out.Triage {
			if a["positions"] != nil && a["classification"] == record.TriageHarmless {
				a["classification"], a["reason"] = record.TriageProblem, "호출자가 항상 유효한 값을 넘기면 무해하다."
				a["title"], a["condition"], a["impact"], a["improvement"] = "SA4006 진단 위치에서 값이 잘못 쓰인다", "진단이 가리키는 분기에 들어오는 경우", "호출자가 잘못된 결과를 받는다.", "값을 검사한다."
			}
		}
		return asJSON(out)
	}
	run := runTriageReport(t, script, 0, Budget{})
	seen := map[string]string{}
	parts := 0
	for _, f := range run.report.Findings {
		if !strings.HasPrefix(f.ID, "review_t001_triage_") || len(f.ObservationIDs) == 3 {
			continue
		}
		if len(f.ObservationIDs) == 1 || len(f.ObservationIDs) == 2 {
			parts++
		}
		if other, dup := seen[f.Fingerprint]; dup {
			t.Fatalf("findings %s and %s share the fingerprint %s", other, f.ID, f.Fingerprint)
		}
		seen[f.Fingerprint] = f.ID
	}
	if parts < 2 {
		t.Fatalf("%d problem parts among the findings", parts)
	}
}

// The definition guard claims a definition only when the whole declaration is
// in a unit of the request and the file is built everywhere: a large
// declaration split over units, a build-constrained file and a file of
// another package do not count.
func TestDefinitionIndexIsExact(t *testing.T) {
	unit := func(path, src string, start, end int) triageSent {
		lines := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
		return triageSent{id: "g1", context: []string{"ev_" + strings.Repeat("a", 24)},
			group: &triageGroup{unit: triageUnit{entry: source.Entry{Path: path}, span: lineSpan{start, end}, text: lines}}}
	}
	use := unit("p/use.go", "package p\n\nfunc Use() {}\n", 1, 3)
	whole := unit("p/whole.go", "package p\n\nfunc Whole() int {\n\treturn 1\n}\n", 1, 5)
	split := unit("p/split.go", "package p\n\nfunc Split() int {\n\tx := 1\n\treturn x\n}\n", 1, 4)
	tagged := unit("p/tagged.go", "//go:build linux\n\npackage p\n\nfunc Tagged() {}\n", 1, 5)
	suffixed := unit("p/f_windows.go", "package p\n\nfunc Suffixed() {}\n", 1, 3)
	other := unit("q/other.go", "package q\n\nfunc Elsewhere() {}\n", 1, 3)
	idx := newDefinitionIndex([]triageSent{use, whole, split, tagged, suffixed, other})
	for name, want := range map[string]bool{"Whole": true, "Split": false, "Tagged": false, "Suffixed": false, "Elsewhere": false, "Missing": false} {
		if got := idx.holder("p/use.go", name) != ""; got != want {
			t.Errorf("%s: found %v, want %v", name, got, want)
		}
	}
}
