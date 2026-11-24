package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// triagePlanKeys are the triage slot tasks a plan of j holds, in order:
// each slot's triage before its verification.
func triagePlanKeys(j analysis.Job) []string {
	var out []string
	for i := range (Handler{}).triageSlots(j) {
		out = append(out, "review.triage."+triageKey(i), "review.verify."+triageKey(i))
	}
	return out
}

// triageSecretTree has three Go files: app/a.go and app/b.go are ordinary source,
// app/keys.go is the file the secrets screen withholds in these tests.
func triageSecretTree() map[string]string {
	return map[string]string{
		"app/a.go":    "package app\n\nfunc A(p *int) int {\n\tx := *p\n\treturn x\n}\n\nfunc B() {}\n",
		"app/b.go":    "package app\n\nfunc C(err error) {\n\t_ = err\n}\n",
		"app/keys.go": "package app\n\nfunc Keys() string {\n\treturn \"value\"\n}\n",
	}
}

func triageObs(id, tool, rule, msg, path string, line int, related ...string) toolObservation {
	o := toolObservation{ID: id, Tool: tool, RuleID: rule, RawMessage: msg, LocationState: obsdoc.LocationResolved,
		Location: &toolLocation{Path: path, StartLine: line, EndLine: line}}
	for _, p := range related {
		o.Related = append(o.Related, obsdoc.Related{Kind: obsdoc.RelatedAssumption, Path: new(p),
			Location: &obsdoc.Location{Path: p, StartLine: 3, EndLine: 3}, LocationState: obsdoc.LocationResolved})
	}
	return o
}

func triageDocOf(key, tool string, obs ...toolObservation) toolDoc {
	return toolDoc{key: key, upstream: record.Upstream{State: record.UpstreamAccepted},
		doc: toolSummary{Tool: tool, TaskStatus: string(domain.TaskSucceeded), ParseOK: true, Observations: obs}}
}

// TestToolMessagesFollowOneRuleInInterpretationAndTriage: a tool message
// reaches the triage request exactly as it reaches the interpretation, under
// the same limitation code — omitted when it is not UTF-8, matches a
// sensitive-value pattern, is too long, or names a withheld file at a related
// location.
func TestToolMessagesFollowOneRuleInInterpretationAndTriage(t *testing.T) {
	const plain = "possible nil dereference"
	cases := []struct {
		name, msg, lim string
		related        []string
	}{
		{name: "plain", msg: plain},
		{name: "related location in a withheld file", msg: plain, related: []string{"app/keys.go"}, lim: limSecretWithheld},
		{name: "sensitive pattern", msg: "token s" + "k-" + strings.Repeat("a1b2", 6) + " used here", lim: limMessageOmitted},
		{name: "oversized", msg: strings.Repeat("m", maxShownMessage+1), lim: limMessageOmitted},
		{name: "not UTF-8", msg: "bad \xff\xfe byte", lim: limMessageOmitted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, triageSecretTree())
			o := triageObs("o1", "staticcheck", "SA5011", c.msg, "app/a.go", 4, c.related...)
			docs := []toolDoc{triageDocOf("staticcheck", "staticcheck", o)}
			want := c.msg
			if c.lim != "" {
				want = messageOmitted
			}

			interp := roleStage{ctx: t.Context(), j: j, task: "review.interpret", role: record.StageInterpret}
			interp.screen.withheld = map[string]bool{"app/keys.go": true}
			_, obs, _, _, lims, err := interp.observationWindows(docs)
			if err != nil {
				t.Fatal(err)
			}
			if len(obs) != 1 || obs[0].Message != want {
				t.Fatalf("interpret message: %+v", obs)
			}
			if c.lim != "" && !slices.Contains(lims, c.lim) || c.lim == "" && slices.Contains(lims, limMessageOmitted) {
				t.Fatalf("interpret limitations %v, want %q", lims, c.lim)
			}

			tri := roleStage{ctx: t.Context(), j: j, task: "review.triage.t001", role: record.StageTriage, triage: "t001"}
			tri.screen.withheld = map[string]bool{"app/keys.go": true}
			plan := tri.planTriage(docs, nil, 2)
			if len(plan.bins) != 1 {
				t.Fatalf("bins: %+v", plan)
			}
			sent, excerpts := tri.triageRequest(plan.bins[0])
			if len(sent) != 1 || len(sent[0].input.Examples) != 1 || sent[0].input.Examples[0].Message != want {
				t.Fatalf("triage message: %+v", sent)
			}
			if c.lim != "" && !slices.Contains(tri.rec.Limitations, c.lim) || c.lim == "" && len(tri.rec.Limitations) != 0 {
				t.Fatalf("triage limitations %v, want %q", tri.rec.Limitations, c.lim)
			}
			for _, x := range excerpts {
				if x.Path == "app/keys.go" {
					t.Fatalf("excerpt of a withheld file: %+v", x)
				}
			}
		})
	}
}

// TestTriageSendsNoUnitOfAWithheldFile: a diagnostic located in a file the
// secrets screen withholds, or in one whose text matches a sensitive-value
// pattern, is not triaged; neither file's enclosing declaration, header nor
// message reaches the request, and the plan records why.
func TestTriageSendsNoUnitOfAWithheldFile(t *testing.T) {
	tree := triageSecretTree()
	tree["app/env.go"] = "package app\n\nfunc Env() string {\n\treturn \"s" + "k-" + strings.Repeat("z9y8", 6) + "\"\n}\n"
	j := treeJob(t, tree)
	s := roleStage{ctx: t.Context(), j: j, task: "review.triage.t001", role: record.StageTriage, triage: "t001"}
	s.screen.withheld = map[string]bool{"app/keys.go": true}
	docs := []toolDoc{triageDocOf("staticcheck", "staticcheck",
		triageObs("o1", "staticcheck", "SA4006", "value never used", "app/keys.go", 4),
		triageObs("o2", "staticcheck", "SA4006", "value never used", "app/env.go", 4),
		triageObs("o3", "staticcheck", "SA5011", "possible nil dereference", "app/a.go", 4))}
	plan := s.planTriage(docs, nil, 2)
	if plan.eligible != 3 || !slices.Contains(plan.lims, limSecretWithheld) || !slices.Contains(plan.lims, limDiagnosticsNoContext) {
		t.Fatalf("plan: eligible %d, limitations %v", plan.eligible, plan.lims)
	}
	if len(plan.bins) != 1 || len(plan.bins[0]) != 1 || plan.bins[0][0].diags[0].obs.ID != "o3" {
		t.Fatalf("bins: %+v", plan.bins)
	}
	sent, excerpts := s.triageRequest(plan.bins[0])
	for _, x := range excerpts {
		if x.Path != "app/a.go" {
			t.Fatalf("excerpt outside the triaged file: %+v", x)
		}
	}
	for _, g := range sent {
		for _, e := range g.input.Examples {
			if e.ObservationID != "o3" {
				t.Fatalf("withheld diagnostic sent: %+v", e)
			}
		}
	}
}
