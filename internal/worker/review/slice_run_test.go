package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// secretsStub is the go vet stub plus a secrets task. mode is the scan
// outcome: "" a complete scan that reports the withhold paths (none: a
// clean scan), "not_run" the not-configured stand-in, "failed" a failed scan.
type secretsStub struct {
	obs      func(analysis.Job) []govet.Observation
	withhold []string
	mode     string
}

func (secretsStub) Descriptors() []pack.Descriptor {
	return []pack.Descriptor{{ID: "go.vet", Name: "go-vet"}, {ID: "any.gitl" + "eaks", Name: secretsTaskKey}}
}

func (secretsStub) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: pack.CompositeName, Version: "stub", Capability: pack.Capability}
}

func (secretsStub) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, _ := vetStub{}.Plan(ctx, j)
	return append(plan, analysis.TaskSpec{Key: secretsTaskKey, Title: secretsTaskKey, Stage: domain.StageStaticAnalysis}), nil
}

func (w secretsStub) Run(ctx context.Context, j analysis.Job, t analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	if pack.Owns("go-vet", t.Key) {
		return vetStub{obs: w.obs}.Run(ctx, j, t, r)
	}
	id := "any.gitl" + "eaks"
	doc := obsdoc.New(obsdoc.Analyzer{ID: id, Ecosystem: "any"}, obsdoc.Component{ID: t.Key, Version: "1"}, obsdoc.Component{ID: t.Key + "-stub", Version: "1"}, t.Key, j.Snapshot.Digest)
	doc.Module.Dir, doc.Tool.Name = ".", t.Key
	version := "1.0.0"
	doc.Tool.Version, doc.Tool.VersionTested = &version, true
	doc.RulePack = obsdoc.RulePack{ID: "stub", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored,
		ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	stdout := []byte("record\n")
	code := 0
	doc.Executions = []obsdoc.Execution{{Name: t.Key + ".run", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Command: analysis.ManifestCommand{Argv: []string{t.Key}, Dir: ".", EnvKeys: []string{}},
		Stdout:  obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(stdout), CapturedBytes: int64(len(stdout)), TotalBytes: int64(len(stdout))}, Stderr: obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}}}
	sha := map[string]string{}
	doc.Coverage.ProcessedPaths = []string{}
	for _, e := range j.Snapshot.Manifest.Entries {
		sha[e.Path] = e.SHA256
		if e.Kind == "file" && e.ExcludedReason == "" {
			doc.Coverage.ProcessedPaths = append(doc.Coverage.ProcessedPaths, e.Path)
		}
	}
	doc.Observations = []obsdoc.Observation{}
	rule, msg := "aws-access-token", "secret detected by gitl"+"eaks rule aws-access-token (value withheld)"
	for i, p := range w.withhold {
		p := p
		doc.Observations = append(doc.Observations, obsdoc.Observation{ID: obsdoc.ObservationID(id, ".", p, rule, p+":1", p+":1", msg, i), RuleID: rule, Category: "security", RawMessage: msg,
			Path: &p, Location: &obsdoc.Location{Path: p, StartLine: 1, EndLine: 1, ColumnUnit: "byte", BlobSHA256: sha[p]}, LocationState: obsdoc.LocationResolved,
			Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: t.Key + ".run", Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex(stdout), ByteEnd: int64(len(stdout))}, Unit: p})
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithoutFindings}
	if len(w.withhold) > 0 {
		doc.Outcome.ExitClass = obsdoc.ExitSuccessWithFindings
	}
	doc.Parse.OK = true
	switch w.mode {
	case "failed":
		reason := "scan_incomplete"
		doc.Observations, doc.Parse.OK, doc.Coverage.ProcessedPaths = []obsdoc.Observation{}, false, nil
		doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, Reason: &reason, ExitClass: obsdoc.ExitExecutionError}
	case "not_run":
		reason := "tool_not_configured"
		doc.Observations, doc.Parse.OK = []obsdoc.Observation{}, false
		doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskUnsupported, Reason: &reason, ExitClass: obsdoc.ExitNotRun}
		doc.Executions, doc.Coverage.ProcessedPaths = []obsdoc.Execution{}, []string{}
	}
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}}
}

func coverageJob(t *testing.T) analysis.Job {
	return dirJob(t, "../../../fixtures/modules/coverage/input")
}

// coverageScript answers every role validly on any corpus with one call.
func coverageScript(t *testing.T) map[string]func(int, opencode.Request) opencode.Reply {
	return goBuildFilesScript(t)
}

// threeCalls makes every task use the most calls a task may make: a reply
// that is not JSON, then (asked again with output_not_json) an output the
// contract rejects, then (asked to repair it) the valid output.
func threeCalls(script map[string]func(int, opencode.Request) opencode.Reply) map[string]func(int, opencode.Request) opencode.Reply {
	out := map[string]func(int, opencode.Request) opencode.Reply{}
	for role, valid := range script {
		out[role] = func(n int, q opencode.Request) opencode.Reply {
			var in struct {
				Repair *struct {
					OutputErrors []string `json:"output_errors"`
				} `json:"repair"`
			}
			if err := json.Unmarshal(q.Input, &in); err != nil {
				panic(err)
			}
			switch {
			case in.Repair == nil:
				return opencode.Reply{Reason: "model_output_invalid"}
			case slices.Equal(in.Repair.OutputErrors, []string{"output_not_json"}):
				return asJSON(map[string]any{"unexpected": true})
			}
			return valid(n, q)
		}
	}
	return out
}

// countedCalls returns the attempt's counter file, one task key per call.
func countedCalls(t *testing.T, j analysis.Job) []string {
	t.Helper()
	b, err := os.ReadFile(callsPath(j))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func sliceKeys(plan []analysis.TaskSpec) []string {
	var out []string
	for _, p := range plan {
		if stage, key, ok := record.SliceTask(p.Key); ok && stage == record.StageInvestigate {
			out = append(out, key)
		}
	}
	return out
}

func taskKeys(plan []analysis.TaskSpec) []string {
	var out []string
	for _, p := range plan {
		out = append(out, p.Key+"="+string(p.Stage)+fmt.Sprintf("/%v", p.Required))
	}
	return out
}

// TC-165 (docs/REVIEW.md#모듈-단위-검토-분할): the plan never depends on the
// call ceiling, and admission never lets an attempt exceed it. At N = 15, 33
// and 207, with an upstream that needs one call per task and one that needs
// the most a task may make (3), the attempt's calls stay ≤ N; the admitted
// slices are a prefix of the plan, each admitted when at least 6 calls were
// left; a slice that is not admitted, and its verify, end budget_exhausted
// with no call and no record. With one-call tasks more slices run than a
// worst-case pre-admission ((N − 15) / 6) would allow.
func TestSliceAdmissionKeepsTheCeiling(t *testing.T) {
	var basePlan []string
	for _, n := range []int{15, 33, 207} {
		for _, upstream := range []string{"one", "three"} {
			t.Run(fmt.Sprintf("%d-%s", n, upstream), func(t *testing.T) {
				j := coverageJob(t)
				script := coverageScript(t)
				if upstream == "three" {
					script = threeCalls(script)
				}
				f := &roleCaller{script: script}
				plan, res := runRoleWith(t, j, RoleHandler{Static: secretsStub{}, Caller: f, MaxCalls: n}, "1.27.4")
				keys := taskKeys(plan)
				if basePlan == nil {
					basePlan = keys
				}
				if !slices.Equal(keys, basePlan) {
					t.Fatalf("plan depends on the ceiling:\n%v\n%v", keys, basePlan)
				}
				calls := countedCalls(t, j)
				if len(calls) != len(f.requests) || len(calls) > n {
					t.Fatalf("calls %d counted %d, ceiling %d", len(f.requests), len(calls), n)
				}
				for _, p := range plan {
					if wholeRunRole(p.Key) && res[p.Key].Status != domain.TaskSucceeded && res[p.Key].Status != domain.TaskSkippedPolicy {
						t.Fatalf("%s %+v", p.Key, res[p.Key])
					}
				}
				if res[record.TaskScope].Status != domain.TaskSucceeded {
					t.Fatalf("scope %+v", res[record.TaskScope])
				}
				keysOf := sliceKeys(plan)
				if len(keysOf) != 7 {
					t.Fatalf("slices %v", keysOf)
				}
				admitted, exhausted := 0, false
				for _, key := range keysOf {
					inv, ver := res["review.investigate."+key], res["review.verify."+key]
					var mine int
					firstAt := -1
					for i, c := range calls {
						if strings.HasSuffix(c, "."+key) {
							mine++
							if firstAt < 0 {
								firstAt = i
							}
						}
					}
					if inv.Status == domain.TaskBudgetExhausted {
						exhausted = true
						if inv.Reason != reasonBudgetExhausted || ver.Status != domain.TaskBudgetExhausted || ver.Reason != reasonBudgetExhausted || mine != 0 {
							t.Fatalf("%s: %+v %+v, %d calls", key, inv, ver, mine)
						}
						for _, stage := range []string{"investigate", "verify"} {
							if _, err := os.Stat(rolePath(j, stage+"."+key)); !errors.Is(err, fs.ErrNotExist) {
								t.Fatalf("%s.%s has a record", stage, key)
							}
						}
						// Not admitted: fewer than 6 calls were left.
						if n-len(calls) >= record.SliceReserveCalls {
							t.Fatalf("%s refused with %d calls left", key, n-len(calls))
						}
						continue
					}
					if exhausted {
						t.Fatalf("%s admitted after a refused slice", key)
					}
					if inv.Status != domain.TaskSucceeded || ver.Status != domain.TaskSucceeded {
						t.Fatalf("%s: %+v %+v", key, inv, ver)
					}
					if firstAt < 0 || n-firstAt < record.SliceReserveCalls {
						t.Fatalf("%s admitted at %d calls of %d", key, firstAt, n)
					}
					admitted++
				}
				pre := (n - record.WholeRunMaxCalls) / record.SliceReserveCalls
				t.Logf("ceiling %d, %s-call tasks: %d calls, %d of %d slices admitted (worst-case pre-admission: %d)", n, upstream, len(calls), admitted, len(keysOf), min(pre, len(keysOf)))
				if upstream == "one" && admitted <= min(pre, len(keysOf)) && admitted < len(keysOf) {
					t.Fatalf("admitted %d, pre-admission %d", admitted, pre)
				}
				if n == record.MaxMaxCalls && admitted != len(keysOf) {
					t.Fatalf("ceiling %d admitted %d", n, admitted)
				}
			})
		}
	}
}

// investigateFiles returns the code and test paths of an investigate request.
func investigateFiles(t *testing.T, q opencode.Request) []string {
	in := decodeAs[investigateInput](t, q)
	var out []string
	for _, e := range in.Scope.Excerpts {
		if e.Origin == "context" && (e.Kind == "code" || e.Kind == "test") {
			out = append(out, e.Path)
		}
	}
	return out
}

// TC-167 (docs/REVIEW.md#모듈-단위-검토-분할): no code or test file reaches two
// investigate requests of a run; every slice request carries the
// overview's instructions byte for byte (same pack, contract and guides);
// slice records are schema 0.3 records bound to the scope artifact, and the
// run sends more distinct files than the overview alone.
func TestSlicesNeverRepeatOverviewFiles(t *testing.T) {
	j := coverageJob(t)
	f := &roleCaller{script: coverageScript(t)}
	_, res := runRoleWith(t, j, RoleHandler{Static: secretsStub{}, Caller: f, MaxCalls: record.MaxMaxCalls}, "1.27.4")
	seen := map[string]string{}
	instructions := map[string]string{}
	overview := 0
	for _, q := range f.requests {
		if prior, ok := instructions[q.Role]; ok && prior != q.Instructions {
			t.Fatalf("%s instructions differ between requests", q.Role)
		}
		instructions[q.Role] = q.Instructions
		if q.Role != "investigate" {
			continue
		}
		files := investigateFiles(t, q)
		if overview == 0 {
			overview = len(files)
		}
		for _, p := range files {
			if seen[p] != "" {
				t.Fatalf("%s sent twice", p)
			}
			seen[p] = "sent"
		}
	}
	if f.calls("investigate") != 8 || f.calls("verify") != 8 {
		t.Fatalf("investigate %d verify %d calls", f.calls("investigate"), f.calls("verify"))
	}
	// The overview offers its certain prefix (7 files) and here one more
	// that fit; review.scope drops that one from its slice.
	t.Logf("overview sent %d files, the run %d", overview, len(seen))
	if overview != 8 || len(seen) != 39 {
		t.Fatalf("overview %d files, run %d", overview, len(seen))
	}
	b, err := os.ReadFile(scopePath(j))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := record.DecodeScope(b, scopeExpect(j))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(doc.Overview.Offered, overviewContext(t, j)) {
		t.Fatalf("offered %v", doc.Overview.Offered)
	}
	for _, sl := range doc.Slices {
		for _, k := range []string{sl.InvestigateTask, sl.VerifyTask} {
			if res[k].Status != domain.TaskSucceeded {
				t.Fatalf("%s %+v", k, res[k])
			}
		}
		for _, stage := range []string{"investigate", "verify"} {
			name := stage + "." + sl.Key
			raw, err := os.ReadFile(rolePath(j, name))
			if err != nil {
				t.Fatal(err)
			}
			r, err := record.DecodeRole(raw, expectRecord(j, name))
			if err != nil {
				t.Fatal(name, err)
			}
			if r.Schema != record.SchemaVersionSlice || r.Slice == nil || r.Slice.Key != sl.Key || !slices.Equal(r.Slice.Modules, sl.Modules) {
				t.Fatalf("%s schema %s slice %+v", name, r.Schema, r.Slice)
			}
			if !slices.ContainsFunc(r.Upstream, func(u record.Upstream) bool {
				return u.Source == record.TaskScope && u.State == record.UpstreamAccepted
			}) {
				t.Fatalf("%s upstream %+v", name, r.Upstream)
			}
		}
	}
	for _, stage := range record.RoleStages {
		if _, err := os.Stat(rolePath(j, stage)); err == nil && readRole(t, j, stage).Schema != record.SchemaVersionRole {
			t.Fatalf("%s is not a 0.2 record", stage)
		}
	}
}

// overviewContext returns the sorted code and test paths of the overview
// investigate record's context evidence.
func overviewContext(t *testing.T, j analysis.Job) []string {
	t.Helper()
	var out []string
	for _, e := range readRole(t, j, "investigate").Evidence {
		if e.Origin == "context" && (e.Kind == "code" || e.Kind == "test") {
			out = append(out, e.Path)
		}
	}
	slices.Sort(out)
	return out
}

// TC-167 (size part): the worst case with source of all five ecosystems,
// maximal role outputs and every slice admitted. Every slice request fits
// PromptBudget and carries the five language guides.
func TestSliceRequestSizeWorstCaseAllLanguages(t *testing.T) {
	j := worstCaseJob(t, func(root string) { polyglotWorstCase(t, root) })
	f := &roleCaller{script: maxScript(t)}
	plan, res := runRoleWith(t, j, RoleHandler{Static: secretsStub{obs: polyglotObservations}, Caller: f, MaxCalls: record.MaxMaxCalls}, "1.27.4")
	slicesRun := 0
	for _, p := range plan {
		if _, key, ok := record.SliceTask(p.Key); ok {
			r := res[p.Key]
			if r.Status != domain.TaskSucceeded {
				t.Fatalf("%s %+v", p.Key, r)
			}
			name := strings.TrimPrefix(p.Key, "review.")
			raw, err := os.ReadFile(rolePath(j, name))
			if err != nil {
				t.Fatal(err)
			}
			rec, err := record.DecodeRole(raw, expectRecord(j, name))
			if err != nil {
				t.Fatal(name, err)
			}
			for _, c := range rec.Calls {
				if c.RequestBytes <= 0 || c.RequestBytes > PromptBudget || len(guideFiles(c.PackFiles)) != 5 {
					t.Fatalf("%s.%s request_bytes %d, pack_files %v", rec.Stage, key, c.RequestBytes, c.PackFiles)
				}
			}
			slicesRun++
		}
	}
	largest := 0
	for _, q := range f.requests {
		largest = max(largest, len(opencode.Prompt(q)))
		if len(opencode.Prompt(q)) > PromptBudget {
			t.Fatalf("%s request %d", q.Role, len(opencode.Prompt(q)))
		}
	}
	t.Logf("%d slice tasks, largest request %d bytes", slicesRun, largest)
	if slicesRun < 2 {
		t.Fatalf("slice tasks %d", slicesRun)
	}
}

// TC-166 (docs/REVIEW.md#모듈-단위-검토-분할): slices go out only after a
// completed secrets screen. Unscreened (no secrets task or the not-configured
// stand-in) and a failed screen skip review.scope and every slice with
// secret_screen_unavailable and no request; the whole-run roles keep their
// rules (unscreened is recorded, a failed screen skips them). A completed
// screen withholds a reported file from the slices as from the overview: its
// state is withheld in the scope artifact and no request carries it; a slice
// left without files ends no_review_scope even when the budget is spent
// (precedence: policy, screen, scope, nothing to review, budget).
func TestSliceSecretGating(t *testing.T) {
	for _, c := range []struct {
		name   string
		static analysis.Handler
		whole  domain.TaskStatus
	}{
		{"unscreened", vetStub{}, domain.TaskSucceeded},
		{"not_run", secretsStub{mode: "not_run"}, domain.TaskSucceeded},
		{"failed", secretsStub{mode: "failed"}, domain.TaskSkippedPolicy},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := coverageJob(t)
			f := &roleCaller{script: coverageScript(t)}
			plan, res := runRoleWith(t, j, RoleHandler{Static: c.static, Caller: f, MaxCalls: record.MaxMaxCalls}, "1.27.4")
			for _, p := range plan {
				_, _, slice := record.SliceTask(p.Key)
				r := res[p.Key]
				switch {
				case slice || p.Key == record.TaskScope:
					if r.Status != domain.TaskSkippedPolicy || r.Reason != reasonScreenUnavailable {
						t.Fatalf("%s %+v", p.Key, r)
					}
				case p.Key == "review.investigate" && r.Status != c.whole:
					t.Fatalf("%s %+v", p.Key, r)
				}
			}
			for _, q := range f.requests {
				if q.Role == "investigate" && f.calls("investigate") > 1 {
					t.Fatalf("slice request made: %d investigate calls", f.calls("investigate"))
				}
			}
			if _, err := os.Stat(scopePath(j)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("scope artifact written")
			}
		})
	}

	t.Run("withheld", func(t *testing.T) {
		j := coverageJob(t)
		// s002 starts with store/cache.go; s007 holds the last four store files.
		withhold := []string{"store/cache.go", "store/sync.go", "store/tombstone.go", "store/txn.go", "store/wal.go"}
		f := &roleCaller{script: coverageScript(t)}
		plan, res := runRoleWith(t, j, RoleHandler{Static: secretsStub{withhold: withhold}, Caller: f, MaxCalls: record.MaxMaxCalls}, "1.27.4")
		for _, q := range f.requests {
			if q.Role == "investigate" {
				for _, p := range investigateFiles(t, q) {
					if slices.Contains(withhold, p) {
						t.Fatalf("withheld %s sent", p)
					}
				}
			}
		}
		b, err := os.ReadFile(scopePath(j))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := record.DecodeScope(b, scopeExpect(j))
		if err != nil {
			t.Fatal(err)
		}
		states := map[string]string{}
		for _, sl := range doc.Slices {
			for _, file := range sl.Files {
				states[file.Path] = file.State
			}
		}
		for _, p := range withhold {
			if states[p] != record.FileWithheld {
				t.Fatalf("%s state %q", p, states[p])
			}
		}
		last := sliceKeys(plan)[len(sliceKeys(plan))-1]
		for _, k := range []string{"review.investigate." + last, "review.verify." + last} {
			if res[k].Status != domain.TaskSkippedPolicy || res[k].Reason != domain.ReasonNoReviewScope {
				t.Fatalf("%s %+v (slice files %+v)", k, res[k], doc.Slices[len(doc.Slices)-1].Files)
			}
		}
		if res["review.investigate.s002"].Status != domain.TaskSucceeded {
			t.Fatalf("s002 %+v", res["review.investigate.s002"])
		}
	})

	t.Run("precedence", func(t *testing.T) {
		j := coverageJob(t)
		f := &roleCaller{script: coverageScript(t)}
		h := RoleHandler{Static: secretsStub{}, Caller: f, MaxCalls: record.WholeRunMaxCalls}
		inv := analysis.TaskSpec{Key: "review.investigate.s001", Stage: domain.StageCodeReview}
		static, _ := secretsStub{}.Plan(t.Context(), j)
		for _, task := range static {
			h.Run(t.Context(), j, task, nil)
		}
		// No scope artifact yet: the slice depends on review.scope.
		if r := h.Run(t.Context(), j, inv, nil); r.Status != domain.TaskSkippedPolicy || r.Reason != reasonDependency {
			t.Fatalf("without scope %+v", r)
		}
		// Model review not approved comes first.
		off := j
		off.AllowModelReview = false
		if r := h.Run(t.Context(), off, inv, nil); r.Status != domain.TaskSkippedPolicy || r.Reason != "model_source_not_approved" {
			t.Fatalf("not approved %+v", r)
		}
		// An unscreened run comes before the missing scope artifact.
		if r := (RoleHandler{Static: vetStub{}, Caller: f}).Run(t.Context(), j, inv, nil); r.Reason != reasonScreenUnavailable {
			t.Fatalf("unscreened %+v", r)
		}
		if len(f.requests) != 0 {
			t.Fatal("request made")
		}
	})

	t.Run("nothing_before_budget", func(t *testing.T) {
		j := coverageJob(t)
		withhold := []string{"store/sync.go", "store/tombstone.go", "store/txn.go", "store/wal.go"}
		f := &roleCaller{script: coverageScript(t)}
		plan, res := runRoleWith(t, j, RoleHandler{Static: secretsStub{withhold: withhold}, Caller: f, MaxCalls: record.WholeRunMaxCalls}, "1.27.4")
		keys := sliceKeys(plan)
		last := "review.investigate." + keys[len(keys)-1]
		if res[last].Reason != domain.ReasonNoReviewScope {
			t.Fatalf("%s %+v", last, res[last])
		}
		if res["review.investigate."+keys[len(keys)-2]].Status != domain.TaskBudgetExhausted {
			t.Fatalf("%+v", res["review.investigate."+keys[len(keys)-2]])
		}
	})
}
