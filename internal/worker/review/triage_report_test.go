package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// triageScript answers every group of a triage request (only the groups a
// repair names) with the classification classOf gives it.
func triageScript(t *testing.T, classOf func(g triageGroupInput) string) func(int, opencode.Request) opencode.Reply {
	return func(_ int, q opencode.Request) opencode.Reply {
		var in struct {
			Groups []triageGroupInput `json:"groups"`
			Repair *triageRepair      `json:"repair"`
		}
		if err := json.Unmarshal(q.Input, &in); err != nil {
			t.Fatal(err)
		}
		out := []map[string]any{}
		for _, g := range in.Groups {
			if in.Repair != nil && !slices.Contains(in.Repair.Groups, g.ID) {
				continue
			}
			a := map[string]any{"group_id": g.ID, "title": "", "condition": "", "mechanism": "", "impact": "", "improvement": "", "evidence_ids": []string{}}
			switch c := classOf(g); c {
			case record.TriageProblem:
				a["classification"], a["title"] = c, fmt.Sprintf("%s 진단 위치에서 값이 잘못 쓰인다", strings.Join(g.Rules, ","))
				a["condition"], a["impact"] = "진단이 가리키는 분기에 들어오는 경우", "호출자가 잘못된 결과를 받는다."
				a["improvement"], a["reason"] = "진단 위치 앞에서 값을 검사한다.", "호출자가 항상 유효한 값을 넘기면 무해하다."
				a["evidence_ids"] = []string{g.Context[0]}
			case record.TriageHarmless:
				a["classification"], a["reason"] = c, "함수 앞부분의 검사가 이 경로를 막아 진단이 가리키는 값은 쓰이지 않는다."
				a["evidence_ids"] = []string{g.Context[0]}
			default:
				a["classification"], a["reason"] = record.TriageUnclear, "호출자 코드가 입력에 없어 진단의 영향을 판단할 수 없다."
				a["missing"] = []map[string]any{{"kind": "caller", "name": ""}}
			}
			out = append(out, a)
		}
		return asJSON(map[string]any{"triage": out})
	}
}

// triageHeadline is the start of the diagnostics fold's title: the decided
// (problem or harmless) and unclear diagnostics counted apart.
func triageHeadline(d *report.DiagnosticTriage) string {
	decided, unclear := 0, 0
	for _, g := range d.Groups {
		if g.Classification == record.TriageUnclear {
			unclear += len(g.ObservationIDs)
		} else {
			decided += len(g.ObservationIDs)
		}
	}
	return fmt.Sprintf("도구 진단 %d건 중 %d건 판정&#40;문제·무해&#41;, %d건 판단 보류", d.Diagnostics, decided, unclear)
}

// triageFixture is fixtures/modules/triage/input: a module of six files
// with three functions each (function k at line 3+6k, its body 4+6k..7+6k).
const triageFixture = "../../../fixtures/modules/triage/input"

// triageDiagsOf: one SA4006 per function (line 5+6k) and, in a.go's first
// function, an SA5011 and a go vet nilness on line 4 (one construct, two
// tools).
func triageDiagsOf() []exampleDiag {
	var out []exampleDiag
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		for k := range 3 {
			out = append(out, exampleDiag{path: name + ".go", line: 5 + 6*k, rule: "SA4006", category: "correctness", message: "this value of x is never used (synthetic)"})
		}
	}
	return append(out, exampleDiag{path: "a.go", line: 4, rule: "SA5011", category: "correctness", message: "possible nil pointer dereference (synthetic)"})
}

func triageStatic(t *testing.T) *pack.Composite {
	t.Helper()
	sc := staticcheck.Descriptor()
	rp := obsdoc.RulePack{ID: staticcheck.RulePackID, Version: staticcheck.RulePackVersion, Selection: []string{"-checks", "SA*"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	c, err := pack.NewComposite(
		exampleMember{d: govet.Descriptor(), plan: govet.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult { return triageVet(t, j, task, files) }},
		exampleMember{d: sc, plan: staticcheck.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			return exampleEnvelope(t, j, sc, task, "2026.2.2", rp, files, []string{"default_build_context_only"}, triageDiagsOf())
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// triageVet writes a go vet document with one nilness diagnostic on a.go
// line 4, bound to its go.vet stdout.
func triageVet(t *testing.T, j analysis.Job, task pack.Task, files []string) analysis.TaskResult {
	t.Helper()
	key, version, platform, mod := task.Spec.Key, "go1.27.4", "linux/amd64", "example.test/triage"
	d := govet.Document{SchemaVersion: govet.DocumentSchemaVersion, Kind: govet.DocumentKind, AnalyzerID: govet.AnalyzerID, AdapterVersion: govet.AdapterVersion,
		ParserID: govet.ParserID, ParserVersion: govet.ParserVersion, TaskKey: key, ManifestDigest: j.Snapshot.Digest, Module: govet.ModuleRef{Dir: task.Module, Path: mod},
		Patterns: append([]string{}, task.Patterns...), ToolVersion: &version, Platform: &platform, Coverage: govet.Coverage{Packages: map[string]int{govet.PackageAnalyzedWithFindings: 1}},
		AnalyzerErrors: []govet.AnalyzerError{}, StderrFailures: []govet.StderrFailure{}, WorkspaceDrift: []string{}, Limitations: []string{"default_build_context_only"}}
	var execs []analysis.Execution
	add := func(name string, argv []string, code int, stdout string) analysis.Execution {
		e := exampleExec(t, j, key, name, argv, code, stdout)
		execs = append(execs, e)
		d.Executions = append(d.Executions, govet.ExecSummary{Name: name, Status: e.Result.Status, ExitCode: e.Result.ExitCode, Cleanup: e.Result.Cleanup, Command: analysis.CommandFor(e.Result.Command),
			Stdout: govet.StreamRef{SHA256: obsdoc.SHA256Hex(e.Result.Stdout.Data), CapturedBytes: int64(len(e.Result.Stdout.Data)), TotalBytes: e.Result.Stdout.TotalBytes},
			Stderr: govet.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}})
		return e
	}
	add("go.version", []string{"go", "version"}, 0, "go version go1.27.4 linux/amd64\n")
	add("go.modedit", []string{"go", "mod", "edit", "-json"}, 0, `{"Module":{"Path":"`+mod+`"}}`+"\n")
	add("go.list", []string{"go", "list", "-e", "-json", "./..."}, 0, `{"ImportPath":"`+mod+`"}`+"\n")
	const msg = "nil dereference in load (synthetic)"
	stdout := `{"` + mod + `":{"nilness":[{"posn":"a.go:4:2","message":"` + msg + `"}]}}` + "\n"
	vet := add("go.vet", []string{"go", "vet", "-json", "./..."}, 0, stdout)
	p := "a.go"
	d.Observations = []govet.Observation{{ID: "obs_" + strings.Repeat("0", 19) + "1", Tool: govet.AnalyzerID, RuleID: "nilness", RawMessage: msg,
		Path: &p, Location: &govet.ObsLocation{Path: p, StartLine: 4, StartCol: 2, EndLine: 4, EndCol: 4, ColumnUnit: "byte", BlobSHA256: exampleSHA(t, j, p)},
		LocationState: govet.LocationResolved, PackageKey: mod,
		RawRef: govet.RawRef{Execution: "go.vet", Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex(vet.Result.Stdout.Data), JSONPointer: "/example.test~1triage/nilness/0", ByteStart: 0, ByteEnd: int64(len(stdout) - 1)}}}
	d.Inventory = []govet.PackageScope{{ImportPath: mod, Dir: ".", State: govet.PackageAnalyzedWithFindings, GoFiles: files, IgnoredGoFiles: []string{}, Errors: []string{}}}
	n := len(files)
	d.Coverage.GoFilesInScope, d.Coverage.ProcessedGoFiles = n, &n
	d.Outcome = govet.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: govet.ExitSuccessWithFindings}
	d.Parse = govet.ParseReport{OK: true, Observations: 1}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	path := govet.ObservationsPath(j.Scratch, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Executions: execs, Limitations: d.Limitations,
		Artifacts: []analysis.ArtifactFile{{Path: path, Role: analysis.RoleObservations, MediaType: analysis.MediaObservations}}}
}

type triageRun struct {
	j       analysis.Job
	plan    []analysis.TaskSpec
	results map[string]analysis.TaskResult
	report  report.Report
	caller  *scriptedCaller
}

// runTriageReport runs the deep-review composition over triageTree and
// builds the report.
func runTriageReport(t *testing.T, script map[string]func(int, opencode.Request) opencode.Reply, maxCalls int, budget Budget) triageRun {
	t.Helper()
	j := dirJob(t, triageFixture)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000a01", "0195f3a0-0000-7000-8000-000000000a02"
	pinExampleSnapshot(t, &j)
	caller := &scriptedCaller{script: script}
	h, err := discovery.Wrap(Handler{Static: triageStatic(t), Caller: caller, MaxCalls: maxCalls, Budget: budget}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]analysis.TaskResult{}
	for _, task := range plan {
		results[task.Key] = h.Run(t.Context(), j, task, nil)
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 0x900}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range r.Limitations {
		if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, "artifact_rejected") || strings.HasPrefix(l, "unrecognized_artifact") {
			t.Fatalf("limitation %s in %v", l, r.Limitations)
		}
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(report.Project(r, false)); err != nil {
		t.Fatalf("code-free export: %v", err)
	}
	return triageRun{j: j, plan: plan, results: results, report: r, caller: caller}
}

// triageReviewScript: the whole-run roles answer validly (interpretation
// takes a.go's first SA4006); triage classifies the cross-tool group in
// a.go and the groups of c.go as problems, those of b.go as harmless, the
// rest unclear; a slot's verification supports the nil dereference and
// refutes the c.go problems.
func triageReviewScript(t *testing.T) map[string]func(int, opencode.Request) opencode.Reply {
	s := goBuildFilesScript(t)
	s["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[interpretInput](t, q)
		i := slices.IndexFunc(in.Observations, func(o obsInput) bool { return o.Path == "a.go" && o.RuleID == "SA4006" })
		o := in.Observations[i]
		return asJSON(map[string]any{"hypotheses": []map[string]any{{"observation_ids": []string{o.ID}, "title": "덮어쓴 값이 반환에 쓰인다", "condition": "함수가 호출되는 모든 경우",
			"mechanism": "x가 읽힌 직후 상수로 덮어써진다.", "impact": "호출자는 입력과 무관한 값을 받는다.", "improvement": "덮어쓰기를 제거한다.", "counterpoint": "상수 반환이 의도일 수 있다.",
			"evidence_ids": o.EvidenceIDs}}, "limits": []map[string]any{}})
	}
	s["triage"] = triageScript(t, func(g triageGroupInput) string {
		switch {
		case g.CrossTool, g.Path == "c.go":
			return record.TriageProblem
		case g.Path == "b.go":
			return record.TriageHarmless
		}
		return record.TriageUnclear
	})
	whole := s["verify"]
	s["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if len(in.Candidates) == 0 || !strings.HasPrefix(in.Candidates[0].ID, "triage_") {
			return whole(n, q)
		}
		var out []map[string]any
		for _, c := range in.Candidates {
			if strings.Contains(c.Title, "SA5011") || strings.Contains(c.Title, "nilness") {
				out = append(out, verdict(c.ID, "supported", func(m map[string]any) {
					m["category"], m["severity"], m["severity_reason"] = "correctness", "high", "nil 인자로 호출하면 프로세스가 멈춘다."
					m["impact"], m["valid_conditions"] = "nil 인자로 호출하면 panic이 난다.", []string{"호출자가 nil을 넘긴다."}
					m["supporting_evidence_ids"] = []string{c.Evidence[0]}
				}))
				continue
			}
			out = append(out, verdict(c.ID, "refuted", func(m map[string]any) {
				m["category"], m["impact"] = "correctness", "덮어쓴 값은 의도한 상수 반환이다."
				m["invalid_conditions"] = []string{"함수는 항상 상수를 반환하도록 작성되었다."}
				m["counterevidence_ids"] = []string{c.Evidence[0]}
			}))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	return s
}

// TC-293: many diagnostics over several files are grouped per rule and
// declaration, packed into the planned slots and classified; the counts
// come from the report's observations and add up; a harmless group keeps
// its tool findings with its reason and cited code; a problem the slot's
// verification supports is an action card, one it refutes is not; the two
// tools' diagnostics on one construct are one group that keeps both tool
// findings; the code-free export carries no model reason.
func TestDiagnosticTriageReachesTheReport(t *testing.T) {
	run := runTriageReport(t, triageReviewScript(t), 0, Budget{})
	r := run.report
	// The slot's records are the examples of a triage record and of its
	// verification (examples/review/triage, scripts/validate_bundle.py).
	for _, name := range []string{"triage.t001", "verify.t001"} {
		b, err := os.ReadFile(recordPath(run.j, name))
		if err != nil {
			t.Fatal(err)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, b, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		p := "../../../examples/review/triage/" + name + ".json"
		if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		compareStoredExample(t, p, pretty.Bytes())
	}
	for _, key := range []string{"review.triage.t001", "review.verify.t001"} {
		if res := run.results[key]; res.Status != domain.TaskSucceeded {
			t.Fatalf("%s: %+v", key, res)
		}
	}
	d := r.CodeReview.DiagnosticTriage
	if d == nil {
		t.Fatal("no diagnostic triage")
	}
	// 18 SA4006, one SA5011 and one nilness; the interpretation took one.
	// One group per file and rule (each file is smaller than a unit) and
	// the two tools' a.go line-4 diagnostics one more.
	if d.Diagnostics != 20 || d.Interpreted != 1 || d.Triaged != 19 || len(d.Groups) != 7 {
		t.Fatalf("counts %d/%d/%d, %d groups", d.Diagnostics, d.Triaged, d.Interpreted, len(d.Groups))
	}
	findings := map[string]report.Finding{}
	for _, f := range r.Findings {
		findings[f.ID] = f
	}
	tool := map[string]int{}
	for _, f := range r.Findings {
		if !strings.HasPrefix(f.ID, "review_") {
			for _, id := range f.ObservationIDs {
				tool[id]++
			}
		}
	}
	buckets := map[string]int{}
	cross := 0
	var supported, refuted []string
	for _, g := range d.Groups {
		buckets[g.Bucket] += len(g.ObservationIDs)
		for _, id := range g.ObservationIDs {
			if tool[id] != 1 {
				t.Fatalf("diagnostic %s has %d tool findings", id, tool[id])
			}
		}
		switch g.Classification {
		case record.TriageHarmless:
			if g.Reason == nil || len(g.Locations) == 0 || g.Locations[0].Path != "b.go" || g.FindingID != nil {
				t.Fatalf("harmless group %+v", g)
			}
		case record.TriageProblem:
			f, ok := findings[deref(g.FindingID)]
			if !ok || !slices.Equal(f.ObservationIDs, g.ObservationIDs) {
				t.Fatalf("problem group without its finding: %+v", g)
			}
			if g.CrossTool {
				cross++
				if len(g.ObservationIDs) != 2 || f.Validation != report.ValidationSupported || g.Bucket != report.TriageAttention || f.Severity == "critical" {
					t.Fatalf("cross-tool group %+v, finding %s %s", g, f.Validation, f.Severity)
				}
				supported = append(supported, f.ID)
			} else if f.Validation != report.ValidationRefuted || g.Bucket != report.TriageRefuted {
				t.Fatalf("refuted group %+v, finding %s", g, f.Validation)
			} else {
				refuted = append(refuted, f.ID)
			}
		}
	}
	if cross != 1 || buckets[report.TriageHarmless] != 3 || buckets[report.TriageRefuted] != 3 ||
		buckets[report.TriageAttention]+buckets[report.TriageHarmless]+buckets[report.TriageRefuted]+d.Interpreted != d.Diagnostics {
		t.Fatalf("buckets %v, cross %d", buckets, cross)
	}
	md := string(report.RenderMarkdownAt(r, "", report.Korean, report.DetailSummary))
	for _, want := range []string{triageHeadline(d), "무해하다고 판단한 진단 3건", "검증에서 반증된 진단 3건", "분류하지 않은 진단 1건", "함수 앞부분의 검사가"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q", want)
		}
	}
	html, err := report.RenderHTMLAt(r, "", report.Korean, report.DetailSummary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "tool-diagnostics-harmless") || !strings.Contains(string(html), "함수 앞부분의 검사가") {
		t.Fatal("HTML lacks the triage buckets")
	}
	free := report.Project(r, false)
	for _, g := range free.CodeReview.DiagnosticTriage.Groups {
		if g.Reason != nil && strings.Contains(*g.Reason, "검사") {
			t.Fatal("model reason in the code-free export")
		}
	}
	// The supported problem is an action card; the refuted ones are not.
	if len(supported) != 1 || !strings.Contains(md, `<a id="action-`+supported[0]+`">`) {
		t.Fatalf("no action card for the supported triage problem %v", supported)
	}
	for _, id := range refuted {
		if strings.Contains(md, `<a id="action-`+id+`">`) {
			t.Fatalf("refuted triage problem %s is an action card", id)
		}
	}
}

// TC-294: under the smallest ceiling the whole-run roles use most calls
// (each answers on its third call): the first triage slot and its
// verification still fit, the second slot is budget_exhausted. Its
// diagnostics are reported not triaged, never clean, and the attempt stays
// within the ceiling.
func TestDiagnosticsLeftUntriagedByTheCeiling(t *testing.T) {
	script := triageReviewScript(t)
	for _, role := range []string{"environment", "documents", "investigate", "interpret", "verify"} {
		script[role] = threeCalls(map[string]func(int, opencode.Request) opencode.Reply{role: script[role]})[role]
	}
	run := runTriageReport(t, script, record.WholeRunMaxCalls, Budget{OutputTokens: 4096})
	if res := run.results["review.triage.t001"]; res.Status != domain.TaskSucceeded {
		t.Fatalf("t001: %+v; calls %v", res, run.caller.byRole)
	}
	for _, key := range []string{"review.triage.t002", "review.verify.t002"} {
		if res := run.results[key]; res.Status != domain.TaskBudgetExhausted {
			t.Fatalf("%s: %+v; calls %v", key, res, run.caller.byRole)
		}
	}
	if n := len(run.caller.requests); n > record.WholeRunMaxCalls {
		t.Fatalf("%d calls past the ceiling", n)
	}
	d := run.report.CodeReview.DiagnosticTriage
	if d == nil || d.Triaged == 0 || d.Triaged+d.Interpreted >= d.Diagnostics || d.Diagnostics != 20 {
		t.Fatalf("triage %+v", d)
	}
	for _, g := range d.Groups {
		if g.TaskKey != "review.triage.t001" {
			t.Fatalf("group of an exhausted slot: %+v", g)
		}
	}
	md := string(report.RenderMarkdownAt(run.report, "", report.Korean, report.DetailSummary))
	if !strings.Contains(md, triageHeadline(d)) || !strings.Contains(md, "깨끗하다는 뜻이 아니다") {
		t.Fatal("markdown does not say the diagnostics were not triaged")
	}
}

// TC-295: at the smallest output budget a request holds two groups, so the
// two planned slots carry four groups and the rest are recorded as outside
// the slots (diagnostics_outside_triage_slots), not classified.
func TestDiagnosticsOutsideTheSlotsAreNotTriaged(t *testing.T) {
	run := runTriageReport(t, triageReviewScript(t), 0, Budget{OutputTokens: MinOutputTokens})
	d := run.report.CodeReview.DiagnosticTriage
	if d == nil || len(d.Groups) != 4 || d.Triaged >= d.Diagnostics || !slices.Contains(d.Limitations, limDiagnosticsOutside) {
		t.Fatalf("triage %+v", d)
	}
}

// TC-296: a triage answer with faults of form (an unknown classification,
// a missing group) gets one repair listing only the pending groups; an
// answer naming a group the request did not hold is an integrity fault.
// When the repair answers the same way, the groups classified on the first
// answer are kept and the faulty ones stay unclassified: the report counts
// them as not triaged.
func TestTriageRepairsFormAndKeepsIntegrityFaults(t *testing.T) {
	for _, mode := range []string{"repaired", "unusable"} {
		t.Run(mode, func(t *testing.T) {
			script := triageReviewScript(t)
			valid := script["triage"]
			script["triage"] = func(n int, q opencode.Request) opencode.Reply {
				var in struct {
					Groups []triageGroupInput `json:"groups"`
					Repair *triageRepair      `json:"repair"`
				}
				if err := json.Unmarshal(q.Input, &in); err != nil {
					t.Fatal(err)
				}
				if in.Repair != nil && (!slices.Equal(in.Repair.Groups, []string{in.Groups[0].ID, in.Groups[1].ID}) ||
					!slices.Contains(in.Repair.OutputErrors, errTriageClassInvalid) || !slices.Contains(in.Repair.OutputErrors, errTriageGroupUnknown)) {
					t.Fatalf("repair %+v", in.Repair)
				}
				if in.Repair != nil && mode == "repaired" {
					return valid(n, q)
				}
				// The first group misclassified, the second missing and an
				// unknown group added.
				out := []map[string]any{{"group_id": in.Groups[0].ID, "classification": "maybe", "reason": "모름", "evidence_ids": []string{}},
					{"group_id": "g64", "classification": record.TriageUnclear, "reason": "입력에 없는 묶음이다.", "evidence_ids": []string{}, "missing": []map[string]any{{"kind": "other", "name": ""}}}}
				for _, g := range in.Groups[2:] {
					out = append(out, map[string]any{"group_id": g.ID, "classification": record.TriageUnclear, "reason": "호출자 코드가 입력에 없다.", "evidence_ids": []string{}, "missing": []map[string]any{{"kind": "caller", "name": ""}}})
				}
				return asJSON(map[string]any{"triage": out})
			}
			run := runTriageReport(t, script, 0, Budget{})
			d := run.report.CodeReview.DiagnosticTriage
			res := run.results["review.triage.t001"]
			if run.caller.calls(record.StageTriage) != 2 || d == nil || res.Status != domain.TaskSucceeded {
				t.Fatalf("%d triage calls, %+v", run.caller.calls(record.StageTriage), res)
			}
			switch mode {
			case "repaired":
				if d.Triaged != 19 {
					t.Fatalf("triaged %d", d.Triaged)
				}
			case "unusable":
				if d.Triaged == 0 || d.Triaged+d.Interpreted >= d.Diagnostics || !slices.Contains(d.Limitations, record.LimTriageGroupsNotTriaged) {
					t.Fatalf("triage %+v", d)
				}
			}
		})
	}
}

// TC-297: the dry-run upstream (scripts/fake_model_upstream.py) answers the triage
// and verify roles of the triage fixture. Its plain answers classify the
// groups in turn as problem, harmless and unclear and every group is
// triaged; with --shape-errors 1 every answer repeats a citation (resolved
// after the repair) or names a classification that does not exist (left
// out); with --integrity-errors 1 every problem or harmless group cites an
// ID the request did not hold and an unknown group is added: those groups
// are rejected and the report counts them as not triaged.
func TestDryRunUpstreamTriage(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	for _, x := range []struct {
		name             string
		shape, integrity float64
	}{{"plain", 0, 0}, {"form", 1, 0}, {"integrity", 0, 1}} {
		t.Run(x.name, func(t *testing.T) {
			script := triageReviewScript(t)
			for _, role := range []string{record.StageTriage, record.StageVerify} {
				whole := script[role]
				script[role] = func(n int, q opencode.Request) opencode.Reply {
					if role == record.StageVerify && !bytes.Contains(q.Input, []byte(`"triage_`)) {
						return whole(n, q)
					}
					return dryRunUpstream(t, python, role, q, x.shape, x.integrity)
				}
			}
			run := runTriageReport(t, script, 0, Budget{})
			d := run.report.CodeReview.DiagnosticTriage
			var rec record.Record
			b, err := os.ReadFile(recordPath(run.j, "triage.t001"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &rec); err != nil {
				t.Fatal(err)
			}
			requireRegisteredCodes(t, rec)
			res := run.results["review.triage.t001"]
			omitted := map[string]int{}
			classes := map[string]int{}
			for _, g := range rec.TriageGroups {
				omitted[g.Omitted]++
				classes[g.Classification]++
			}
			if d == nil || res.Status != domain.TaskSucceeded {
				t.Fatalf("triage %+v, task %+v", d, res)
			}
			wantCalls := 1
			if x.shape > 0 || x.integrity > 0 {
				wantCalls = 2
			}
			if n := run.caller.calls(record.StageTriage); n != wantCalls {
				t.Fatalf("%d triage calls", n)
			}
			switch x.name {
			case "plain":
				if d.Triaged+d.Interpreted != d.Diagnostics || classes[record.TriageProblem] == 0 || classes[record.TriageHarmless] == 0 || classes[record.TriageUnclear] == 0 {
					t.Fatalf("triage %+v, classes %v", d, classes)
				}
				if res := run.results["review.verify.t001"]; res.Status != domain.TaskSucceeded {
					t.Fatalf("verify %+v", res)
				}
				// A harmless group of several diagnostics is answered part by
				// part: its first diagnostic alone is harmless, the others unclear.
				split := slices.ContainsFunc(rec.TriageGroups, func(g record.TriageGroup) bool {
					return g.Classification == record.TriageHarmless && len(g.ObservationIDs) == 1
				})
				if !split || len(rec.Calls[0].OutputErrors) != 0 {
					t.Fatalf("part answer not stored: %+v, errors %v", rec.TriageGroups, rec.Calls[0].OutputErrors)
				}
			case "form":
				// The new shapes of the answer fail too: a diagnostic listed in two
				// parts, and a function the request holds named as missing.
				if !slices.Contains(rec.Calls[0].OutputErrors, errTriageDiagnostics) || !slices.Contains(rec.Calls[0].OutputErrors, errTriageUnclearPresent) {
					t.Fatalf("calls %+v", rec.Calls)
				}
				if omitted[record.TriageItemOmitted]+omitted[record.TriageAnswerMissing] != classes[""] || classes[""] == 0 || classes[record.TriageProblem]+classes[record.TriageHarmless] == 0 ||
					!slices.Contains(rec.Limitations, record.LimTriageGroupsNotTriaged) || !slices.Contains(rec.Calls[0].OutputErrors, errTriageClassInvalid) ||
					!slices.Contains(rec.Calls[0].OutputErrors, "evidence_id_duplicate") || d.Triaged == 0 || d.Triaged+d.Interpreted >= d.Diagnostics {
					t.Fatalf("triage %+v, record groups %+v, calls %+v", d, rec.TriageGroups, rec.Calls)
				}
			case "integrity":
				if omitted[record.TriageItemRejected] == 0 || classes[record.TriageProblem]+classes[record.TriageHarmless] != 0 || classes[record.TriageUnclear] == 0 ||
					!slices.Contains(rec.Calls[1].OutputErrors, "evidence_id_unknown") || !slices.Contains(rec.Calls[1].OutputErrors, errTriageGroupUnknown) ||
					d.Triaged == 0 || d.Triaged+d.Interpreted >= d.Diagnostics {
					t.Fatalf("triage %+v, record groups %+v, calls %+v", d, rec.TriageGroups, rec.Calls)
				}
			}
		})
	}
}
