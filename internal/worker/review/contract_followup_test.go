package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestContractFollowupFindsDocumentedEntryWithoutModelLiteral(t *testing.T) {
	const entry = "load_profile"
	const definition = "int load_profile(const char *name)\n{\n    return dispatch_profile(name);\n}\n"
	for _, test := range []struct {
		name, statement, document, second string
		withhold                          bool
		want                              bool
	}{
		{"definition", "load_profile()가 설정을 읽는다.", "Use load_profile() to read the profile.", "", false, true},
		{"ambiguous", "load_profile()가 설정을 읽는다.", "Use load_profile() to read the profile.", definition, false, false},
		{"not named by source", "load_profile()가 설정을 읽는다.", "Read the profile.", "", false, false},
		{"withheld", "load_profile()가 설정을 읽는다.", "Use load_profile() to read the profile.", "", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := map[string]string{
				"app/a.h":      "int load_profile(const char *name);\n",
				"app/client.c": "// load_profile() reads a profile.\nvoid client(void) { load_profile(\"default\"); }\n",
				"app/entry.c":  definition,
				"other/dup.c":  definition,
			}
			if test.second != "" {
				tree["app/second.c"] = test.second
			}
			j := treeJob(t, tree)
			s := &roleStage{ctx: t.Context(), j: j}
			if test.withhold {
				s.screen.withheld = map[string]bool{"app/entry.c": true}
			}
			out, err := s.contractNamedExcerpts(record.Contract{Statement: test.statement, AppliesTo: "app"}, "app",
				[]record.Excerpt{{Kind: "document", Text: test.document}})
			if err != nil || (len(out) > 0) != test.want {
				t.Fatalf("entry windows %+v: %v", out, err)
			}
			if test.want && (len(out) != 1 || out[0].Path != "app/entry.c" || out[0].Kind != "code" ||
				!strings.Contains(out[0].Text, "return dispatch_profile(name)") || !strings.Contains(out[0].Text, entry)) {
				t.Fatalf("missing exact wrapper body: %+v", out)
			}
		})
	}
}

func TestContractFollowupRequiresNewImplementation(t *testing.T) {
	tree := map[string]string{
		"api.h":        "int read_state(void);\n",
		"notes.c":      "/* example only\nint read_state(void) { return 0; }\n*/\n",
		"state.c":      "int read_state(void) {\n    return 1;\n}\n",
		"state_test.c": "void test_read_state(void) {\n    assert(read_state() == 1);\n}\n",
		"ambient.d.ts": "export declare function read_state(): number;\n",
	}
	j := treeJob(t, tree)
	stage := &roleStage{ctx: t.Context(), j: j}
	files := stage.entries()
	window := func(name string) record.Excerpt {
		return record.Excerpt{Path: name, SHA256: files[name].SHA256, Kind: record.SourceKind(name), Start: 1,
			End: strings.Count(tree[name], "\n")}
	}
	alreadySeen := []record.Excerpt{window("state.c")}
	for name := range tree {
		e := window(name)
		_, got, err := stage.contractImplementation(e, alreadySeen, files)
		if err != nil || got {
			t.Errorf("%s was treated as new executable evidence: %v (%v)", name, got, err)
		}
	}
	_, got, err := stage.contractImplementation(window("state.c"), nil, files)
	if err != nil || !got {
		t.Fatalf("missing previously unseen implementation: %v (%v)", got, err)
	}
}

func TestContractFollowupKeepsRuntimeLinesInFilesWithNonRuntimeSource(t *testing.T) {
	tree := map[string]string{
		"src/lib.rs":    "pub fn before() { return 1; }\n#[cfg(test)]\nmod tests {\n    fn nested() { cached_value = 1; }\n    fn another() { return 2; }\n}\npub fn after() { return 3; }\n",
		"src/client.ts": "export function before() { return 1; }\nconst docs = \x60value \x24{examples}\nreturn cached_value;\n\x60;\nexport function after() { return 3; }\n",
	}
	j := treeJob(t, tree)
	stage := &roleStage{ctx: t.Context(), j: j}
	files := stage.entries()
	for _, check := range []struct {
		path     string
		from, to int
		runtime  bool
	}{
		{"src/lib.rs", 4, 5, false},
		{"src/lib.rs", 7, 7, true},
		{"src/lib.rs", 1, 1, true},
		{"src/client.ts", 2, 4, false},
		{"src/client.ts", 3, 3, false},
		{"src/client.ts", 5, 5, true},
	} {
		e := record.Excerpt{Path: check.path, Kind: "code", SHA256: files[check.path].SHA256, Start: check.from, End: check.to}
		runtime, fresh, err := stage.contractImplementation(e, nil, files)
		if err != nil || runtime != check.runtime || fresh != check.runtime {
			t.Errorf("%s:%d..%d runtime=%v fresh=%v error=%v", check.path, check.from, check.to, runtime, fresh, err)
		}
	}
}

func contractExampleInput(t *testing.T, j analysis.Job) {
	t.Helper()
	for stage, name := range map[string]string{record.StageDocuments: "documents", record.StageInvestigate: "investigate-probe"} {
		var body bytes.Buffer
		if err := json.Compact(&body, mustRead(t, filepath.Join(exampleDir, name+".json"))); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(recordPath(j, stage), body.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func storeContractExample(t *testing.T, j analysis.Job, stage string) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, mustRead(t, recordPath(j, stage)), "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	dir := filepath.Join(exampleDir, "contract")
	p := filepath.Join(dir, stage+".json")
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") != "1" {
		compareStoredExample(t, p, pretty.Bytes())
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestContractFollowupMixedRecordRequiresBoundEvidenceForBothConditions(t *testing.T) {
	j := contractExampleJob(t, nil)
	open := func(stage string) record.Record {
		t.Helper()
		rec, err := record.Decode(mustRead(t, filepath.Join(exampleDir, "contract", stage+".json")), expectRecord(j, stage))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	docs, inv, reviewed := open(record.StageDocuments), open(record.StageInvestigate), open(record.StageContract)
	c := reviewed.ContractComparisons[0]
	c.Status = "partial"
	source := catalogOf(reviewed.Evidence)[c.CodeEvidenceIDs[0]]
	first, second := source, source
	first.Start, first.End = 11, 12
	second.Start, second.End = 13, 15
	first.ID = record.ExcerptID(j.Snapshot.Digest, first.Path, first.SHA256, first.Start, first.End)
	second.ID = record.ExcerptID(j.Snapshot.Digest, second.Path, second.SHA256, second.Start, second.End)
	c.CodeEvidenceIDs = []string{first.ID, second.ID}
	reviewed.ContractComparisons[0] = c
	reviewed.Evidence = append(reviewed.Evidence, first, second)
	proof := record.ContractPartialSupport{
		Matching:  record.ContractConditionEvidence{Condition: "첫 호출이 성공할 때", EvidenceID: first.ID},
		Violating: record.ContractConditionEvidence{Condition: "재시도 뒤에도 실패할 때", EvidenceID: second.ID},
	}
	reviewed.PartialSupport = &proof
	if _, err := record.Marshal(reviewed, expectRecord(j, record.StageContract)); err != nil || !record.ValidateContractFollowup(reviewed, docs, inv) {
		t.Fatalf("mixed record with two distinct locations in the same file: %v", err)
	}
	for _, change := range []func(*record.Record){
		func(r *record.Record) { r.PartialSupport = nil },
		func(r *record.Record) { r.PartialSupport.Violating = r.PartialSupport.Matching },
		func(r *record.Record) { r.PartialSupport.Violating.EvidenceID = source.ID },
		func(r *record.Record) { r.PartialSupport.Violating.Condition = r.PartialSupport.Matching.Condition },
	} {
		r, support := reviewed, proof
		r.PartialSupport = &support
		change(&r)
		if _, err := record.Marshal(r, expectRecord(j, record.StageContract)); err == nil || record.ValidateContractFollowup(r, docs, inv) {
			t.Fatalf("mixed record accepted missing, repeated or uncited support: %+v", r.PartialSupport)
		}
	}
	inv.Evidence = append(inv.Evidence, first, second)
	if record.ValidateContractFollowup(reviewed, docs, inv) {
		t.Fatal("mixed record accepted stale support")
	}
}

func exampleContractReply(t *testing.T, q opencode.Request) record.ContractComparison {
	t.Helper()
	in := decodeAs[contractReviewInput](t, q)
	if in.Prior.Status != "unknown" || in.Contract.Statement == "" || len(in.Scope.Excerpts) < 2 {
		t.Fatalf("contract input %+v", in)
	}
	comparison := in.Prior
	for _, e := range in.Scope.Excerpts {
		switch {
		case e.Kind == "code" && strings.Contains(e.Text, "return pay()") && in.Prior.ContractIndex == 2:
			comparison.Status = "unknown"
			comparison.Reason = "래퍼는 호출 결과를 한 번만 반환한다. 요청마다 래퍼에 한 번 진입하는지는 아직 확인하지 못했다."
		case e.Kind == "code" && strings.Contains(e.Text, "MaxRetries && err != nil") && in.Prior.ContractIndex == 1:
			comparison.Status = "match"
			comparison.Reason = "하위 호출이 실패하면 최대 세 번 더 호출하고 마지막 오류를 반환한다."
		default:
			continue
		}
		comparison.CodeEvidenceIDs = []string{e.ID}
		return comparison
	}
	t.Fatal("implementation lines were not sent")
	return comparison
}

// TC-268 exercises the actual final role and scratch call counter with
// accepted artifacts of a fixed synthetic snapshot. The fake never runs the
// code or contacts a model.
func TestContractFollowupPreservesPriorOnBudgetOrInvalidOutput(t *testing.T) {
	for _, test := range []struct {
		name       string
		remaining  int
		mode       string
		status     domain.TaskStatus
		wantCalls  int
		wantResult string
	}{
		{"accept", 1, "", domain.TaskSucceeded, 1, "match"},
		{"hold unknown", 1, "unknown", domain.TaskSucceeded, 1, "unknown"},
		{"repair when possible", 2, "repair", domain.TaskSucceeded, 2, "match"},
		{"failed repair quarantined", 2, "wrong kind", domain.TaskFailed, 2, ""},
		{"repair skipped at ceiling", 1, "repair", domain.TaskBudgetExhausted, 1, ""},
		{"no remaining budget", 0, "", domain.TaskBudgetExhausted, 0, ""},
		{"conflicting documentation", 1, "conflict", domain.TaskSkippedPolicy, 0, ""},
		{"misbound documents", 1, "digest", domain.TaskSkippedPolicy, 0, ""},
		{"one confirmed condition is not partial", 2, "bad partial", domain.TaskFailed, 2, ""},
		{"template text cannot support a match", 2, "template", domain.TaskFailed, 2, ""},
		{"test-only Rust cannot support a match", 2, "rust test", domain.TaskFailed, 2, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var fakePath string
			var fakeLine int
			j := contractExampleJob(t, func(root string) {
				switch test.mode {
				case "template":
					fakePath, fakeLine = "examples_text.ts", 2
					writeFile(t, root, fakePath, "const documentation = \x60\nreturn cached_value;\n\x60;\nexport function production() { return liveState(); }\n")
				case "rust test":
					fakePath, fakeLine = "src/lib.rs", 4
					writeFile(t, root, fakePath, "#[cfg(test)]\nmod tests {\n    fn checking() {\n        cached_value = 1;\n    }\n}\npub fn production() { return live_state(); }\n")
				}
			})
			contractExampleInput(t, j)
			if test.mode != "conflict" {
				// The regular module example intentionally includes a conflict.
				// This separate scripted document output omits the conflicting
				// guidance and binds the overview to those exact bytes.
				doc, inv := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate)
				doc.Conflicts = nil
				focusPath := "svc/b/charge.go"
				if test.mode != "unknown" && test.mode != "bad partial" {
					focusPath = "gateway.go"
					inv.Candidates = nil
					inv.Strengths = nil
					c := doc.Contracts[0]
					inv.ContractComparisons = []record.ContractComparison{{
						ContractIndex: 1, AppliesTo: c.AppliesTo, Status: "unknown",
						Reason: "호출 실패 뒤 재시도 횟수와 반환 경로를 확인해야 한다.", DocumentEvidenceIDs: c.EvidenceIDs, CodeEvidenceIDs: []string{},
					}}
					entry := (&roleStage{j: j}).entries()[focusPath]
					inv.ContractProbe = &record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{{
						ID:   record.ContractLocationID(j.Snapshot.Digest, focusPath, entry.SHA256, 4, 16),
						Path: focusPath, SHA256: entry.SHA256, Start: 4, End: 16, Kind: "code", Symbol: "Forward",
					}}}
					if fakePath != "" {
						fake := (&roleStage{j: j}).entries()[fakePath]
						inv.ContractProbe.Candidates = append(inv.ContractProbe.Candidates, record.ContractLocation{
							ID:   record.ContractLocationID(j.Snapshot.Digest, fakePath, fake.SHA256, fakeLine, fakeLine),
							Path: fakePath, SHA256: fake.SHA256, Start: fakeLine, End: fakeLine, Kind: "code",
						})
					}
				}
				// Leave the chosen implementation unread by this fixture's
				// overview so the final window contains new executable lines.
				inv.Evidence = slices.DeleteFunc(inv.Evidence, func(e record.Excerpt) bool { return e.Path == focusPath })
				if _, err := persistRecord(j, doc); err != nil {
					t.Fatal(err)
				}
				for i, up := range inv.Upstream {
					if up.Source == "review.documents" {
						inv.Upstream[i].SHA256 = digestOf(mustRead(t, recordPath(j, record.StageDocuments)))
					}
				}
				if _, err := persistRecord(j, inv); err != nil {
					t.Fatal(err)
				}
			}
			if test.mode == "digest" {
				// Still valid JSON and the same snapshot, but not the document
				// bytes bound by the selected overview.
				f, err := os.OpenFile(recordPath(j, record.StageDocuments), os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteString("\n"); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			usedBefore := record.WholeRunMaxCalls - test.remaining
			writeFile(t, j.Scratch, "review.calls", strings.Repeat("review.verify\n", usedBefore))
			f := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
				record.StageContract: func(n int, q opencode.Request) opencode.Reply {
					in := decodeAs[contractReviewInput](t, q)
					if (n == 2) != (in.Repair != nil) {
						t.Fatalf("repair at call %d: %+v", n, in.Repair)
					}
					c := exampleContractReply(t, q)
					switch {
					case test.mode == "repair" && n == 1:
						c.AppliesTo = "svc/b"
					case test.mode == "wrong kind":
						c.CodeEvidenceIDs = []string{c.DocumentEvidenceIDs[0]}
					case test.mode == "bad partial":
						c.Status = "partial"
					case fakePath != "":
						at := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == fakePath && e.Start == fakeLine })
						if at < 0 {
							t.Fatal("selected fake window was not provided with valid code")
						}
						c.CodeEvidenceIDs = []string{in.Scope.Excerpts[at].ID}
					}
					return asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{c}})
				},
			}}
			h := Handler{Static: vetStub{}, Caller: f, MaxCalls: record.WholeRunMaxCalls}
			res := h.Run(t.Context(), j, analysis.TaskSpec{Key: record.TaskContract}, nil)
			usedAfter, err := callsUsed(j)
			if res.Status != test.status || f.calls(record.StageContract) != test.wantCalls || err != nil || usedAfter != usedBefore+test.wantCalls {
				t.Fatalf("result %+v, calls %d, used %d, error %v", res, f.calls(record.StageContract), usedAfter, err)
			}
			if test.wantCalls == 0 {
				if len(res.Artifacts) != 0 {
					t.Fatalf("skipped contract generated artifacts %+v", res.Artifacts)
				}
				return
			}
			r := readRecord(t, j, record.StageContract)
			if r.Schema != record.SchemaVersionContract || len(r.Calls) != test.wantCalls || r.Calls[0].RequestDigest == "" || r.Calls[0].Usage == nil {
				t.Fatalf("contract record %+v", r)
			}
			if test.wantResult == "" {
				if r.Accepted() || len(r.ContractComparisons) != 0 {
					t.Fatalf("failed output retained a replacement: %+v", r)
				}
				return
			}
			docs, inv := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate)
			if !record.ValidateContractFollowup(r, docs, inv) || r.ContractComparisons[0].Status != test.wantResult ||
				(slices.Contains(r.Limitations, limContractInconclusive) != (test.wantResult == "unknown")) {
				t.Fatalf("contract comparison %+v", r)
			}
			if test.name == "accept" {
				for _, stage := range []string{record.StageDocuments, record.StageInvestigate, record.StageContract} {
					storeContractExample(t, j, stage)
				}
			}
		})
	}
}

func TestContractFollowupPlansLastOnlyWithApprovedModelAndEligibleSource(t *testing.T) {
	j := coverageJob(t)
	h := Handler{Static: vetStub{}, Caller: &scriptedCaller{}}
	check := func(want bool) {
		t.Helper()
		plan, err := h.Plan(t.Context(), j)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(plan, func(p analysis.TaskSpec) bool { return p.Key == record.TaskContract })
		if (i >= 0) != want || (want && (i != len(plan)-1 || plan[i].Required)) {
			t.Fatalf("optional contract task: %+v", plan)
		}
	}
	check(true)
	j.AllowModelReview = false
	check(false)
	j.AllowModelReview = true
	h.Caller = nil
	check(false)
}

func TestContractFollowupRereadsAcceptedSliceEvidence(t *testing.T) {
	j := coverageJob(t)
	h := Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: coverageScript(t)}}
	runRoleWith(t, j, h, "1.27.4")
	stage := roleStage{h: h, j: j, ctx: t.Context()}
	_, docs := stage.prior(record.StageDocuments)
	_, overview := stage.prior(record.StageInvestigate)
	scope, scopeUp := stage.readScope()
	if scopeUp.State != record.UpstreamAccepted || len(scope.Slices) == 0 {
		t.Fatalf("scope unavailable: %+v", scopeUp)
	}
	slot := scope.Slices[0]
	sliced, sliceUp := stage.prior(record.StageInvestigate + "." + slot.Key)
	at := slices.IndexFunc(sliced.Evidence, func(e record.Excerpt) bool { return e.Kind == "code" })
	if sliceUp.State != record.UpstreamAccepted || at < 0 {
		t.Fatalf("slice evidence unavailable: %+v", sliceUp)
	}
	selected := sliced.Evidence[at]
	probe := record.ContractProbe{Candidates: []record.ContractLocation{{Path: selected.Path}}}
	prior := record.ContractComparison{AppliesTo: record.DocumentDir(selected.Path)}
	files := stage.entries()
	found, err := stage.contractSliceExcerpts(probe, prior, nil, docs, overview, files, nil, nil)
	if err != nil || !slices.ContainsFunc(found, func(item contractExcerpt) bool {
		e := item.excerpt
		e.Text = ""
		return e == selected && strings.TrimSpace(item.excerpt.Text) != "" &&
			slices.Contains(item.sources, scopeUp) && slices.Contains(item.sources, sliceUp)
	}) {
		t.Fatalf("slice evidence or provenance missing: %+v, %v", found, err)
	}
	overview.SHA256 = strings.Repeat("0", 64)
	found, err = stage.contractSliceExcerpts(probe, prior, nil, docs, overview, files, nil, nil)
	if err != nil || len(found) != 0 {
		t.Fatalf("slice evidence from scope with different overview digest: %+v, %v", found, err)
	}
}

func TestContractFollowupPrioritizesSelectedLateSliceBeforeOrdinaryReadLimit(t *testing.T) {
	const selected = "z_selection.go"
	tree := map[string]string{
		"go.mod":    "module example.org/recipes\n\ngo 1.23\n",
		"README.md": "# Catalogue\n\nRules guide the result.\n",
		selected:    "package recipes\n\nfunc selectEntry() string { return \"vegetarian\" }\n",
	}
	for i := range 112 {
		tree[fmt.Sprintf("record%03d.go", i)] = fmt.Sprintf("package recipes\n\nfunc entry%03d() int { return %d }\n", i, i)
	}
	j := treeJob(t, tree)
	script := coverageScript(t)
	script["investigate"] = func(_ int, _ opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"candidates": []any{}})
	}
	h := Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: script}}
	runRoleWith(t, j, h, "1.27.4")
	stage := roleStage{h: h, j: j, ctx: t.Context()}
	_, docs := stage.prior(record.StageDocuments)
	_, overview := stage.prior(record.StageInvestigate)
	scope, scopeUp := stage.readScope()
	var want record.Excerpt
	var wantSource record.Upstream
	earlier := 0
	for _, slot := range scope.Slices {
		inv, up := stage.prior(record.StageInvestigate + "." + slot.Key)
		if up.State != record.UpstreamAccepted {
			continue
		}
		for _, e := range inv.Evidence {
			if e.Path == selected {
				want, wantSource = e, up
				break
			}
			if e.Kind == "code" {
				earlier++
			}
		}
		if want.ID != "" {
			break
		}
	}
	if earlier <= maxContractSliceReads || want.ID == "" {
		t.Fatalf("fixture failed to place target past read limit: %d excerpts, want %s", earlier, want.ID)
	}
	probe := record.ContractProbe{Candidates: []record.ContractLocation{{Path: selected}}, Literals: []string{"selectEntry"}}
	got, err := stage.contractSliceExcerpts(probe, record.ContractComparison{AppliesTo: "."}, nil, docs, overview, stage.entries(), nil, nil)
	if err != nil || len(got) == 0 || got[0].excerpt.ID != want.ID ||
		!strings.Contains(got[0].excerpt.Text, "return \"vegetarian\"") ||
		!slices.Equal(got[0].sources, []record.Upstream{scopeUp, wantSource}) {
		t.Fatalf("late selected slice evidence was displaced or lost provenance: %+v, %v", got, err)
	}
}

func requireUnusedContract(t *testing.T, results map[string]analysis.TaskResult, caller *scriptedCaller) {
	t.Helper()
	r := results[record.TaskContract]
	if r.Status != domain.TaskSkippedPolicy || r.Reason != domain.ReasonNoReviewScope ||
		len(r.Artifacts) != 0 || caller.calls(record.StageContract) != 0 {
		t.Fatalf("contract task without a probe: %+v, %d calls", r, caller.calls(record.StageContract))
	}
}
