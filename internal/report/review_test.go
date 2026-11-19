package report

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Use the legacy-pack example produced by the worker against its synthetic fixture.
// Derive context and investigator records exactly as the worker carries them.
func exampleStages(t *testing.T) (map[string]*taskState, record.Record) {
	t.Helper()
	raw, err := os.ReadFile("../../examples/review-record.json")
	if err != nil {
		t.Fatal(err)
	}
	var verify record.Record
	if err = json.Unmarshal(raw, &verify); err != nil {
		t.Fatal(err)
	}
	source := &SourceIndex{entries: map[string]string{}}
	for _, e := range verify.Evidence {
		source.entries[e.Path] = e.SHA256
	}
	states := map[string]*taskState{}
	for n, key := range RequiredReviewTasks {
		rec := verify
		rec.Stage = key[len("review."):]
		rec.Calls = rec.Calls[:n+1]
		if n < 2 {
			rec.Verifications = nil
			rec.Evidence = nil
			for _, e := range verify.Evidence {
				if e.Origin == "context" {
					e.Origin = ""
					rec.Evidence = append(rec.Evidence, e)
				}
			}
		}
		if n == 0 {
			rec.Candidates = []record.Candidate{}
		}
		body, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		task := TaskFacts{Key: key, Status: "succeeded"}
		ctx := DecodeContext{Source: source, Snapshot: SnapshotFacts{ManifestDigest: rec.Snapshot}, RunID: rec.RunID, AttemptID: rec.AttemptID, Task: task}
		art := ArtifactRef{Role: "other"}
		c, err := (ReviewDecoder{}).Decode(ctx, art, body)
		if err != nil {
			t.Fatal(key, err)
		}
		ctx.Snapshot.ManifestDigest = "wrong"
		if _, err := (ReviewDecoder{}).Decode(ctx, art, body); err == nil {
			t.Fatal("accepted another snapshot")
		}
		states[key] = &taskState{facts: task, review: c.Review.Accepted, reviewResult: c.Review}
	}
	return states, verify
}

func TestComposeReviewLineageAndCounterevidence(t *testing.T) {
	for _, mode := range []string{"complete", "missing", "altered candidate", "altered provenance", "failed task"} {
		t.Run(mode, func(t *testing.T) {
			states, verify := exampleStages(t)
			st := states["review.verify"]
			switch mode {
			case "missing":
				delete(states, "review.verify")
			case "altered candidate":
				st.reviewResult.Record.Candidates[0].Title += "!"
			case "altered provenance":
				st.reviewResult.Record.Calls[0].RequestDigest = st.reviewResult.Record.Calls[1].RequestDigest
			case "failed task":
				st.facts.Status = "failed"
			}
			r := Report{Inputs: Inputs{SnapshotID: "snap"}}
			composeReviews(&r, states, newSet())
			ai := decideAIReview(states)
			if len(r.Findings) != 1 {
				t.Fatalf("findings=%d", len(r.Findings))
			}
			f := r.Findings[0]
			if err := validateFinding(f, "snap", nil, nil); err != nil {
				t.Fatal(err)
			}
			if mode == "complete" {
				if ai.Status != AIReviewPerformed || f.Validation != "refuted" || !slices.Equal(f.CounterevidenceIDs, verify.Verifications[0].Counterevidence) || len(r.Coverage.ReviewCalls) != 3 || r.Coverage.LLMExposedFiles == nil {
					t.Fatalf("counterevidence/provenance lost: %+v", f)
				}
				if summary := ComputeSummary(r.Findings, nil); summary.RefutedFindings != 1 || summary.ActionableFindings != 0 {
					t.Fatal(summary)
				}
			} else if ai.Status != AIReviewPartial || f.Validation != "unreviewed" || len(f.CounterevidenceIDs) != 0 {
				t.Fatalf("upgraded incomplete chain %v %+v", ai, f)
			}
		})
	}
}
