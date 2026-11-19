package review

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// The environment role only annotates platform verdicts of the gap modules
// it was given: a note may not cite a requirement it was not given (another
// module's), repeat one, cite evidence it was not given or carry a verdict
// field (strict decoding). Each
// attempt's codes are recorded, the record keeps no rejected note and the
// downstream role learns only that the stage failed.
func TestRolePackEnvironmentOutputRules(t *testing.T) {
	cases := map[string]struct {
		note func(own envReqInput) map[string]any
		code string
	}{
		"duplicate requirement": {func(r envReqInput) map[string]any {
			return map[string]any{"requirement_ids": []string{r.ID, r.ID}, "evidence_ids": []string{r.EvidenceID}}
		}, "requirement_id_duplicate"},
		"requirement of a module without a gap": {func(r envReqInput) map[string]any {
			return map[string]any{"requirement_ids": []string{r.ID, "go_directive@svc/a/go.mod"}, "evidence_ids": []string{r.EvidenceID}}
		}, "requirement_id_unknown"},
		"fabricated evidence": {func(r envReqInput) map[string]any {
			return map[string]any{"requirement_ids": []string{r.ID}, "evidence_ids": []string{r.EvidenceID, "ev_000000000000000000000000"}}
		}, "evidence_id_unknown"},
		"verdict override": {func(r envReqInput) map[string]any {
			return map[string]any{"requirement_ids": []string{r.ID}, "evidence_ids": []string{r.EvidenceID}, "status": "satisfied"}
		}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			j := modulesJob(t, nil)
			script := validScript(t)
			script["environment"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[envInput](t, q)
				var own envReqInput
				for _, r := range in.Requirements {
					if r.Module == "go:svc/b" && r.Kind == "go_directive" {
						own = r
					}
				}
				if own.ID == "" || !slices.Equal(in.GapModules, []string{"go:svc/b"}) {
					t.Fatalf("gaps %v requirements %+v", in.GapModules, in.Requirements)
				}
				note := c.note(own)
				note["module"] = "go:svc/b"
				note["affected_checks"], note["still_checkable"], note["needed_observation"] = "svc/b의 go vet을 수행할 수 없다.", "다른 모듈은 검사할 수 있다.", "go 1.30 이상 관측"
				return asJSON(map[string]any{"notes": []map[string]any{note}})
			}
			f := &roleCaller{script: script}
			_, res := runRole(t, j, vetStub{}, f, "1.27.4")
			env := readRole(t, j, "environment")
			if res["review.environment"].Status != domain.TaskFailed || env.Accepted() || len(env.EnvironmentNotes) != 0 || len(env.Calls) != 2 {
				t.Fatalf("%+v %+v", res["review.environment"], env)
			}
			if c.code != "" && !slices.Contains(env.Calls[0].OutputErrors, c.code) {
				t.Fatalf("output errors %v, want %s", env.Calls[0].OutputErrors, c.code)
			}
			if c.code == "" && env.Calls[0].Outcome != "output_rejected" {
				t.Fatalf("non-strict output accepted: %+v", env.Calls[0])
			}
			// Downstream sees the failure, never the rejected notes.
			if inv := readRole(t, j, "investigate"); !slices.Contains(inv.Limitations, "upstream_environment_failed") || res["env.discover"].Status != domain.TaskSucceeded {
				t.Fatalf("investigate %v", inv.Limitations)
			}
		})
	}
}
