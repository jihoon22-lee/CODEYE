package report

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// r1Stages decodes the five review-r1 example records (examples/review-r1,
// written by the worker for the w15 fixture with a scripted caller) the way
// the builder does, and adds the discovery and go-vet tasks they cite.
// required follows the R1 plan: documents (eligible documents exist),
// investigate and verify.
func r1Stages(t *testing.T) (map[string]*taskState, Report) {
	t.Helper()
	bodies := map[string][]byte{}
	source := &SourceIndex{entries: map[string]string{}}
	var recs []record.R1Record
	for _, stage := range record.R1Stages {
		raw, err := os.ReadFile("../../examples/review-r1/" + stage + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := json.Compact(&b, raw); err != nil {
			t.Fatal(err)
		}
		bodies[stage] = b.Bytes()
		var r record.R1Record
		if err := json.Unmarshal(b.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		for _, e := range r.Evidence {
			source.entries[e.Path] = e.SHA256
		}
		recs = append(recs, r)
	}
	required := map[string]bool{"documents": true, "investigate": true, "verify": true}
	states := map[string]*taskState{}
	var obs []Observation
	for i, stage := range record.R1Stages {
		key := "review." + stage
		task := TaskFacts{Key: key, Status: "succeeded", Required: required[stage]}
		ctx := DecodeContext{Source: source, Snapshot: SnapshotFacts{ManifestDigest: recs[i].Snapshot}, RunID: recs[i].RunID, AttemptID: recs[i].AttemptID, Task: task}
		art := ArtifactRef{Role: "other", MediaType: record.MediaType, SHA256: domain.Digest(bodies[stage])}
		c, err := (R1ReviewDecoder{}).Decode(ctx, art, bodies[stage])
		if err != nil {
			t.Fatal(stage, err)
		}
		if _, err := (ReviewDecoder{}).Decode(ctx, art, bodies[stage]); err == nil {
			t.Fatal("the R0 decoder accepted a review-r1 record")
		}
		states[key] = &taskState{facts: task, review: c.Review.Accepted, reviewResult: c.Review, decodedSHA: map[string]string{record.MediaType: art.SHA256}}
		// The cited discovery and go vet artifacts, as the builder accepted them.
		for _, u := range recs[i].Upstream {
			if u.State != record.UpstreamAccepted || states[u.Source] != nil {
				continue
			}
			media := upstreamMedia(u.Source)
			if media == record.MediaType {
				continue
			}
			states[u.Source] = &taskState{facts: TaskFacts{Key: u.Source, Status: "succeeded", Required: true}, decodedSHA: map[string]string{media: u.SHA256}}
		}
		for _, o := range recs[i].Observations {
			if !slices.ContainsFunc(obs, func(x Observation) bool { return x.ID == o.ID }) {
				obs = append(obs, Observation{ID: o.ID, TaskKey: o.TaskKey, RuleID: o.RuleID, Location: &ObservationLocation{Path: o.Path, StartLine: o.StartLine}})
			}
		}
	}
	for _, k := range []string{envdoc.TaskEnvironment, envdoc.TaskDocuments, "go-vet"} {
		if states[k] == nil {
			t.Fatalf("example records do not cite %s", k)
		}
	}
	return states, Report{Inputs: Inputs{SnapshotID: "snap"}, Observations: obs}
}

func compose(states map[string]*taskState, r Report) (Report, AIReview, []string) {
	lim := newSet()
	composeR1Reviews(&r, states, lim)
	return r, decideAIReview(states), lim.list()
}

func taskKeys(l []AIReviewTask) []string {
	var out []string
	for _, x := range l {
		out = append(out, x.Key+"="+x.Result)
	}
	return out
}

// The required review roles come from the planned tasks: documents,
// investigate and verify here; environment and interpret are listed as
// optional and never counted.
func TestR1RequiredRolesFromPlan(t *testing.T) {
	states, base := r1Stages(t)
	r, ai, lim := compose(states, base)
	if ai.Status != AIReviewPerformed || ai.Reason != nil || len(lim) != 0 {
		t.Fatalf("ai %+v limitations %v", ai, lim)
	}
	if got := taskKeys(ai.RequiredTasks); !slices.Equal(got, []string{"review.documents=accepted", "review.investigate=accepted", "review.verify=accepted"}) {
		t.Fatalf("required %v", got)
	}
	if got := taskKeys(ai.OptionalTasks); !slices.Equal(got, []string{"review.environment=accepted", "review.interpret=accepted"}) {
		t.Fatalf("optional %v", got)
	}
	if len(r.Coverage.ReviewCalls) != 5 || r.Coverage.LLMExposedFiles == nil || *r.Coverage.LLMExposedFiles != 7 {
		t.Fatalf("calls %d exposed %v", len(r.Coverage.ReviewCalls), r.Coverage.LLMExposedFiles)
	}
	for _, c := range r.Coverage.ReviewCalls {
		if c.PackID != "review-r1" || c.PackVersion != "2.0.0-rc2" || len(c.PackDigest) != 64 {
			t.Fatalf("call %+v", c)
		}
	}
	if len(r.Findings) != 2 {
		t.Fatalf("findings %d", len(r.Findings))
	}
	known := map[string]bool{"obs_0123456789abcdef0123": true}
	for _, f := range r.Findings {
		if err := validateFinding(f, "snap", known, nil); f.Validation != "inconclusive" || err != nil {
			t.Fatalf("finding %+v: %v", f, err)
		}
	}
	if err := validateFinding(r.Findings[1], "snap", nil, nil); err == nil {
		t.Fatal("a finding citing an unknown observation validated")
	}
	if !slices.Equal(r.Findings[1].ObservationIDs, []string{"obs_0123456789abcdef0123"}) {
		t.Fatalf("interpret finding lost its observation: %+v", r.Findings[1])
	}

	// Optional roles that were skipped keep their reason and do not count;
	// the verdict still rests on the required roles.
	states, base = r1Stages(t)
	states["review.interpret"].facts.Status = "skipped_policy"
	reason := "no_tool_observations"
	states["review.interpret"].facts.Reason = &reason
	states["review.interpret"].reviewResult = nil
	_, ai, _ = compose(states, base)
	opt := ai.OptionalTasks[1]
	if opt.Key != "review.interpret" || opt.Result != "skipped" || opt.Reason == nil || *opt.Reason != reason || opt.TaskStatus == nil || *opt.TaskStatus != "skipped_policy" {
		t.Fatalf("skipped optional role %+v", opt)
	}

	// Without documents the documents role is not required.
	states, base = r1Stages(t)
	states["review.documents"].facts.Required = false
	_, ai, _ = compose(states, base)
	if got := taskKeys(ai.RequiredTasks); ai.Status != AIReviewPerformed || !slices.Equal(got, []string{"review.investigate=accepted", "review.verify=accepted"}) {
		t.Fatalf("documents optional: %s %v", ai.Status, got)
	}
}

// A plan without a required review task is not_performed, however many
// optional roles succeeded or were skipped.
func TestR1NoRequiredReviewTaskIsNotPerformed(t *testing.T) {
	no := "no_environment_gap"
	states := map[string]*taskState{
		"review.environment": {facts: TaskFacts{Key: "review.environment", Status: "skipped_policy", Reason: &no}},
		"review.interpret":   {facts: TaskFacts{Key: "review.interpret", Status: "succeeded"}, review: true},
	}
	ai := decideAIReview(states)
	if ai.Status != AIReviewNotPerformed || ai.Reason == nil || *ai.Reason != "review_tasks_not_planned" || len(ai.RequiredTasks) != 0 || len(ai.OptionalTasks) != 2 {
		t.Fatalf("%+v", ai)
	}
	if ai.OptionalTasks[0].Result != "skipped" || *ai.OptionalTasks[0].Reason != no {
		t.Fatalf("%+v", ai.OptionalTasks[0])
	}
	// A required role that was skipped is not accepted.
	states["review.verify"] = &taskState{facts: TaskFacts{Key: "review.verify", Status: "skipped_policy", Required: true}}
	if ai = decideAIReview(states); ai.Status != AIReviewNotPerformed || ai.RequiredTasks[0].Result != "skipped" {
		t.Fatalf("%+v", ai)
	}
}

// R0 plans keep the three fixed roles, including not_planned rows, and no
// optional list.
func TestR0ReviewRolesUnchanged(t *testing.T) {
	ai := decideAIReview(map[string]*taskState{"review.investigate": {facts: TaskFacts{Key: "review.investigate", Status: "succeeded", Required: true}, review: true}})
	if got := taskKeys(ai.RequiredTasks); ai.Status != AIReviewPartial || ai.OptionalTasks != nil || !slices.Equal(got, []string{"review.context=not_planned", "review.investigate=accepted", "review.verify=not_planned"}) {
		t.Fatalf("%s %v %v", ai.Status, got, ai.OptionalTasks)
	}
	for _, x := range ai.RequiredTasks {
		if x.Reason != nil {
			t.Fatalf("R0 row with a reason %+v", x)
		}
	}
}

// The builder keeps a stage only if it joins what the report accepted: the
// upstream artifacts it cites, the go vet observations and, for verify, the
// candidates of the accepted upstream stages.
func TestR1LineageRejected(t *testing.T) {
	cases := map[string]struct {
		edit func(map[string]*taskState, *Report)
		key  string
	}{
		"altered upstream candidate": {func(s map[string]*taskState, _ *Report) {
			s["review.investigate"].reviewResult.R1.Candidates[0].Title += "!"
		}, "review.verify"},
		"other discovery artifact": {func(s map[string]*taskState, _ *Report) {
			s[envdoc.TaskDocuments].decodedSHA[envdoc.MediaType] = domain.Digest([]byte("other"))
		}, "review.documents"},
		"observation not in report": {func(_ map[string]*taskState, r *Report) { r.Observations = nil }, "review.interpret"},
		"upstream stage failed": {func(s map[string]*taskState, _ *Report) {
			s["review.documents"].facts.Status = "failed"
		}, "review.investigate"},
		"v1 record in an r1 plan": {func(s map[string]*taskState, _ *Report) {
			s["review.investigate"].reviewResult = &ReviewResult{Accepted: true, Record: &record.Record{}}
		}, "review.investigate"},
		"verify relabels upstream evidence": {func(s map[string]*taskState, _ *Report) {
			ev := s["review.verify"].reviewResult.R1.Evidence
			for i := range ev {
				if ev[i].Origin == "context" {
					ev[i].Origin = "caller"
					ev[i].Symbol = "Forward"
					return
				}
			}
		}, "review.verify"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			states, base := r1Stages(t)
			c.edit(states, &base)
			r, ai, lim := compose(states, base)
			if !has(lim, LimReviewLineage+":"+c.key) && !has(lim, LimReviewPackMismatch+":"+c.key) {
				t.Fatalf("limitations %v", lim)
			}
			if ai.Status == AIReviewPerformed {
				t.Fatalf("performed with a rejected stage: %+v", ai)
			}
			for _, f := range r.Findings {
				if f.Validation != "unreviewed" && c.key == "review.verify" {
					t.Fatalf("verdict kept from a rejected verify stage: %+v", f)
				}
			}
		})
	}
}

// A review-r1 record attached to an R0 plan is not read as a v1 record.
func TestR1RecordInR0PlanRejected(t *testing.T) {
	states, _ := r1Stages(t)
	r0 := map[string]*taskState{}
	for _, k := range RequiredReviewTasks {
		if st := states[k]; st != nil {
			r0[k] = st
		}
	}
	lim := newSet()
	var r Report
	composeReviews(&r, r0, lim)
	if !has(lim.list(), LimReviewPackMismatch+":review.investigate") || len(r.Findings) != 0 {
		t.Fatalf("limitations %v findings %d", lim.list(), len(r.Findings))
	}
	if ai := decideAIReview(r0); ai.Status == AIReviewPerformed {
		t.Fatalf("%+v", ai)
	}
}
