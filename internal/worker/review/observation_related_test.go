package review

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type relatedObservationStub struct{ vetStub }

func (relatedObservationStub) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "clang-tidy", Stage: domain.StageStaticAnalysis}}, nil
}

func TestInterpretLinksVerifiedCppConditionWithinOrdinaryBudget(t *testing.T) {
	const primary = "app/apply.cc"
	const guard = "support/ownership.h"
	const noteText = "tool-only context which is omitted"
	const assignment = "state->value = next;"
	const protection = "if (state->readonly) return 0;"
	tree := map[string]string{
		primary: "int update(State *state, int next) {\n  " + assignment + "\n  return 1;\n}\n",
		guard:   strings.Repeat("// file context\n", 28) + "int can_update(State *state) {\n  " + protection + "\n  return 1;\n}\n",
	}
	for i := range roleMaxWindows - 1 {
		tree[fmt.Sprintf("app/other%d.cc", i)] = fmt.Sprintf("int context%d() { return %d; }\n", i, i)
	}
	for _, test := range []string{"verified", "missing", "outside scope", "withheld", "legacy", "drifted"} {
		t.Run(test, func(t *testing.T) {
			j := treeJob(t, tree)
			if test == "outside scope" {
				j.Scope = domain.Scope{Kind: "paths", Paths: []string{"app"}}
			}
			s := roleStage{ctx: t.Context(), j: j, task: "review.interpret", role: record.StageInterpret}
			if test == "withheld" {
				s.screen.withheld = map[string]bool{guard: true}
			}
			location := func(p string, line int) *obsdoc.Location {
				return &obsdoc.Location{Path: p, StartLine: line, EndLine: line, StartCol: 1, EndCol: 1, ColumnUnit: "byte",
					BlobSHA256: s.entries()[p].SHA256}
			}
			r := obsdoc.Related{Kind: obsdoc.RelatedAssumption, RawSpan: &obsdoc.ByteSpan{Start: 4, End: 8}, RawMessage: noteText,
				Path: new(guard), Location: location(guard, 30), LocationState: obsdoc.LocationResolved}
			switch test {
			case "missing":
				r.Path, r.Location, r.LocationState = nil, nil, obsdoc.LocationMissingFile
			case "legacy":
				r.Kind, r.RawSpan = "", nil
			}
			d := obsdoc.New(obsdoc.Analyzer{ID: "cpp.clang_tidy", Ecosystem: "cpp"}, obsdoc.Component{ID: "clang-tidy", Version: "1"},
				obsdoc.Component{ID: "clang-tidy", Version: "1"}, "clang-tidy", j.Snapshot.Digest)
			d.Module.Dir, d.Tool.Name = ".", "clang-tidy"
			d.Tool.Version, d.Tool.VersionTested = new("22.1.5"), true
			d.RulePack = obsdoc.RulePack{ID: "fixture", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigOverridden,
				SourceSuppressions: obsdoc.SuppressHonored, UnreportedSuppressionRules: []string{}, ConfigFiles: []obsdoc.ConfigFile{}}
			raw := obsdoc.StreamOf(executor.Stream{Data: []byte(strings.Repeat("x", 8*roleMaxWindows)), TotalBytes: int64(8 * roleMaxWindows)})
			d.Executions = []obsdoc.Execution{{Name: "clang-tidy.run", Status: executor.StatusExited, ExitCode: new(0), Cleanup: executor.CleanupConfirmed,
				Command: analysis.ManifestCommand{Argv: []string{"clang-tidy"}, Dir: ".", EnvKeys: []string{}}, Stdout: raw,
				Stderr: obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}}}
			d.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithFindings}
			d.Parse.OK = true
			d.Coverage.ProcessedPaths = []string{primary, "app/other0.cc", "app/other1.cc", "app/other2.cc"}
			if test == "drifted" {
				d.WorkspaceDrift = []string{guard}
				d.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, ExitClass: obsdoc.ExitExecutionError, Reason: new("workspace_drift")}
			}
			for i, p := range d.Coverage.ProcessedPaths {
				o := obsdoc.Observation{ID: obsdoc.ObservationID(d.Analyzer.ID, ".", p, "owning-state", p, "", "possible state overwrite", 0),
					RuleID: "owning-state", Category: "correctness", RawMessage: "possible state overwrite", Path: new(p),
					Location: location(p, 1), LocationState: obsdoc.LocationResolved, Related: []obsdoc.Related{}, Unit: p,
					RawRef: obsdoc.RawRef{Execution: "clang-tidy.run", Stream: "stdout", StreamSHA256: raw.SHA256, RecordIndex: i, ByteStart: int64(i * 8), ByteEnd: int64((i + 1) * 8)}}
				if p == primary {
					o.Location = location(p, 2)
					o.Related = []obsdoc.Related{r}
				}
				d.Observations = append(d.Observations, o)
			}
			if _, err := pack.WriteEnvelope(j, d); err != nil {
				t.Fatal(err)
			}
			var selected []string
			caller := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
				record.StageInterpret: func(_ int, q opencode.Request) opencode.Reply {
					in := decodeAs[interpretInput](t, q)
					if len(in.Scope.Excerpts) != roleMaxWindows || strings.Contains(string(q.Input), noteText) ||
						len(opencode.Prompt(q)) > PromptBudget || len(in.Observations) != roleMaxWindows {
						t.Fatalf("unexpected request windows or raw note text: %+v", in.Scope)
					}
					main := in.Observations[0]
					catalog := catalogOf(in.Scope.Excerpts)
					if main.Path != primary || len(main.EvidenceIDs) == 0 ||
						!strings.Contains(catalog[main.EvidenceIDs[0]].Text, assignment) {
						t.Fatalf("primary link missing: %+v", main)
					}
					if test == "verified" {
						if len(main.Related) != 1 || len(main.EvidenceIDs) != 2 || main.Related[0].Kind != obsdoc.RelatedAssumption ||
							main.Related[0].EvidenceID != main.EvidenceIDs[1] || main.Related[0].StartLine != 30 ||
							!strings.Contains(catalog[main.Related[0].EvidenceID].Text, protection) ||
							!slices.Contains(in.Scope.Limitations, "observation_context_outside_input") {
							t.Fatalf("no bound guard under the window cap: %+v", main)
						}
					} else if len(main.Related) != 0 || len(main.EvidenceIDs) != 1 || strings.Contains(string(q.Input), guard) ||
						(test == "withheld" && main.Message != "(omitted by the platform)") {
						t.Fatalf("unusable note or text reached the request: %+v", main)
					}
					selected = main.EvidenceIDs
					return asJSON(map[string]any{"hypotheses": []hypothesis{{ObservationIDs: []string{main.ID}, Evidence: selected,
						Title: "상태 갱신의 조건 검토", Condition: "쓰기 동작을 호출한다.", Impact: "상태 갱신에 영향을 준다.",
						Improvement: "호출 조건을 확인한다.", Counterpoint: "상위에서 쓰기를 제한할 수 있다."}}, "limits": []any{}})
				},
			}}
			s.h = Handler{Static: relatedObservationStub{}, Caller: caller}
			res := s.interpret()
			accepted := readRecord(t, j, record.StageInterpret)
			if res.Status != domain.TaskSucceeded || caller.calls(record.StageInterpret) != 1 || len(accepted.Candidates) != 1 ||
				!slices.Equal(accepted.Candidates[0].Evidence, selected) || len(accepted.Candidates[0].ObservationIDs) != 1 {
				t.Fatalf("interpretation evidence was not accepted: %+v", res)
			}
		})
	}
}
