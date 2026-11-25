package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// staticcheckDoc is a staticcheck document of the default rule pack, so the
// severity tables apply to its diagnostics.
func staticcheckDoc(obs ...toolObservation) toolDoc {
	d := triageDocOf("staticcheck", staticcheck.AnalyzerID, obs...)
	d.doc.RulePack, d.doc.RulePackVersion = staticcheck.RulePackID, staticcheck.RulePackVersion
	return d
}

// pathTree is a Go module with test files, an example, a vendored directory
// and shipped files, each file holding one function whose body starts at
// line 4.
func pathTree(files ...string) map[string]string {
	tree := map[string]string{"go.mod": "module example.test/paths\n\ngo 1.27\n"}
	for i, f := range files {
		tree[f] = fmt.Sprintf("package paths\n\nfunc F%d(p *int) int {\n\tx := *p\n\treturn x\n}\n", i)
	}
	return tree
}

func triagePathDiags(rule string, count int, paths ...string) []toolObservation {
	var out []toolObservation
	for _, p := range paths {
		for k := range count {
			out = append(out, triageObs(fmt.Sprintf("%s-%s-%d", rule, strings.ReplaceAll(p, "/", "_"), k), staticcheck.AnalyzerID, rule, "synthetic message", p, 4))
		}
	}
	return out
}

// TC-302: a group on shipped code is packed before a group on a test,
// example, benchmark, generated or vendored path, whatever the groups' sizes
// and paths: the limited triage share goes to shipped code first. The
// request still carries the table's default severity (the model input does
// not change), and the groups left outside the slots stay listed as not
// triaged.
func TestTriagePacksShippedCodeBeforeOtherPaths(t *testing.T) {
	tests := []string{"a/a_test.go", "a/b_test.go", "a/c_test.go", "a/d_test.go", "b/e_test.go", "examples/x/main.go", "vendor/v/v.go"}
	shipped := []string{"svc/handler.go", "svc/store.go", "svc/queue.go"}
	for _, c := range []struct {
		name, rule string
		perTest    int
	}{
		// SA5011 is high on shipped code and low on the other paths; SA1019
		// is low on both: the order inside a severity is the path class.
		{"lowered severity", "SA5011", 5},
		{"equal severity", "SA1019", 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, pathTree(append(slices.Clone(tests), shipped...)...))
			s := roleStage{ctx: t.Context(), j: j, task: "review.triage.t001", role: record.StageTriage, triage: "t001",
				h: Handler{Budget: Budget{OutputTokens: MinOutputTokens}}}
			// The test-path groups hold more diagnostics and sort first by
			// path, so a plan that ignored the class would admit only them.
			obs := append(triagePathDiags(c.rule, c.perTest, tests...), triagePathDiags(c.rule, 1, shipped...)...)
			plan := s.planTriage([]toolDoc{staticcheckDoc(obs...)}, nil, 2)
			perSlot := triageGroupLimit(s.h.Budget)
			if perSlot != 2 || len(plan.bins) != 2 {
				t.Fatalf("groups per request %d, slots %d", perSlot, len(plan.bins))
			}
			var admitted []string
			for _, bin := range plan.bins {
				for _, g := range bin {
					admitted = append(admitted, g.unit.entry.Path)
					if g.severity != g.diags[0].def.Severity {
						t.Fatalf("group severity %q is not the table default %q", g.severity, g.diags[0].def.Severity)
					}
				}
			}
			for _, p := range shipped {
				if !slices.Contains(admitted, p) {
					t.Fatalf("shipped %s not admitted: %v", p, admitted)
				}
			}
			if len(admitted) != 4 || !slices.Contains(plan.lims, limDiagnosticsOutside) {
				t.Fatalf("admitted %v, limitations %v", admitted, plan.lims)
			}
			// Shipped groups come first in the first slot.
			if first := plan.bins[0][0].unit.entry.Path; !strings.HasPrefix(first, "svc/") {
				t.Fatalf("first group is %s", first)
			}
			if c.rule == "SA5011" && plan.bins[0][0].severity != "high" {
				t.Fatalf("shipped severity %s", plan.bins[0][0].severity)
			}
		})
	}
}

// A path class lowers the order only through the severity and the class:
// among shipped groups the order is the one before, and a group on a test
// path with a higher severity than a shipped one does not exist for the
// default tables (the lowered severity is never above low).
func TestTriageOrderWithinShippedCodeIsUnchanged(t *testing.T) {
	files := []string{"svc/a.go", "svc/b.go", "svc/c.go"}
	j := treeJob(t, pathTree(files...))
	s := roleStage{ctx: t.Context(), j: j, task: "review.triage.t001", role: record.StageTriage, triage: "t001"}
	obs := append(triagePathDiags("SA5011", 1, "svc/c.go"), triagePathDiags("SA5011", 3, "svc/b.go")...)
	obs = append(obs, triagePathDiags("SA5011", 3, "svc/a.go")...)
	plan := s.planTriage([]toolDoc{staticcheckDoc(obs...)}, nil, 1)
	var order []string
	for _, g := range plan.bins[0] {
		order = append(order, g.unit.entry.Path)
	}
	if !slices.Equal(order, []string{"svc/a.go", "svc/b.go", "svc/c.go"}) {
		t.Fatalf("order %v", order)
	}
}
