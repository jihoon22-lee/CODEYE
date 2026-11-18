// R1ReviewDecoder validates review-r1 stage records (schema 0.2); the
// lineage between stages, upstream artifacts and tool observations is
// checked when the report is composed, after every artifact was decoded.
package report

import (
	"reflect"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Limitation codes of review-r1 composition.
const (
	LimReviewLineage      = "review_lineage_mismatch"
	LimReviewPackMismatch = "review_pack_mismatch"
)

type R1ReviewDecoder struct{}

func (R1ReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionR1}
}

func (R1ReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	stage, ok := strings.CutPrefix(ctx.Task.Key, "review.")
	if !ok || !slices.Contains(record.R1Stages, stage) || art.Role != "other" {
		return Contribution{}, invalidf("review-r1 record attached to unexpected task or role")
	}
	r, err := record.DecodeR1(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), R1: &r}, Limitations: r.Limitations}, nil
}

// upstreamMedia is the artifact media type each upstream source must match.
func upstreamMedia(source string) string {
	switch {
	case source == envdoc.TaskEnvironment, source == envdoc.TaskDocuments:
		return envdoc.MediaType
	case strings.HasPrefix(source, "review."):
		return record.MediaType
	}
	return analysis.MediaObservations
}

// r1Accepted reports a stage whose accepted record the report keeps.
func r1Accepted(st *taskState) bool {
	return st != nil && !st.rejected && st.facts.Status == string(domain.TaskSucceeded) && st.reviewResult != nil && st.reviewResult.R1 != nil && st.reviewResult.R1.Accepted()
}

// r1Lineage checks one record against the artifacts the report accepted.
func r1Lineage(rec *record.R1Record, states map[string]*taskState, obs map[string]Observation) bool {
	up := map[string]record.Upstream{}
	for _, u := range rec.Upstream {
		up[u.Source] = u
		st := states[u.Source]
		if u.State == record.UpstreamAccepted {
			if st == nil || st.rejected || st.decodedSHA[upstreamMedia(u.Source)] != u.SHA256 {
				return false
			}
			if strings.HasPrefix(u.Source, "review.") && !r1Accepted(st) {
				return false
			}
		} else if strings.HasPrefix(u.Source, "review.") && r1Accepted(st) {
			// The stage ignored an accepted upstream result.
			return false
		}
	}
	for _, o := range rec.Observations {
		x, ok := obs[o.ID]
		if !ok || x.TaskKey != o.TaskKey || x.RuleID != o.RuleID || (o.Path != "" && (x.Location == nil || x.Location.Path != o.Path || x.Location.StartLine != o.StartLine)) {
			return false
		}
	}
	if rec.Stage != record.StageVerify {
		return true
	}
	var want []record.R1Candidate
	// One excerpt ID (bound to path, blob and range) may reach verify from
	// both stages with a different origin label (context vs observation);
	// verify must carry one of the upstream variants unchanged.
	evidence := map[string][]record.Excerpt{}
	for _, stage := range []string{record.StageInvestigate, record.StageInterpret} {
		u, ok := up["review."+stage]
		if !ok || u.State != record.UpstreamAccepted {
			continue
		}
		prior := states["review."+stage].reviewResult.R1
		want = append(want, prior.Candidates...)
		for _, e := range prior.Evidence {
			evidence[e.ID] = append(evidence[e.ID], e)
		}
	}
	if !reflect.DeepEqual(nonNilCandidates(want), nonNilCandidates(rec.Candidates)) {
		return false
	}
	for _, e := range rec.Evidence {
		if variants, ok := evidence[e.ID]; ok && !slices.Contains(variants, e) {
			return false
		}
	}
	return true
}

func nonNilCandidates(l []record.R1Candidate) []record.R1Candidate {
	if l == nil {
		return []record.R1Candidate{}
	}
	return l
}

// composeR1Reviews joins the review-r1 stage records: lineage, call
// provenance (with the pack identity), exposed files and findings. Findings
// carry verifier verdicts only from a succeeded verify task with an accepted
// record; otherwise the accepted candidates stay unreviewed.
func composeR1Reviews(r *Report, states map[string]*taskState, limits *stringSet) {
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	for k, st := range states {
		if strings.HasPrefix(k, "review.") && st.reviewResult != nil && st.reviewResult.R1 == nil && !st.rejected {
			st.rejected = true
			limits.add(LimReviewPackMismatch + ":" + k)
		}
	}
	exposed, known := map[string]bool{}, true
	for _, stage := range record.R1Stages {
		key := "review." + stage
		st := states[key]
		if st == nil {
			continue
		}
		if st.rejected || st.reviewResult == nil || st.reviewResult.R1 == nil {
			// A task that ran a model call without a usable record leaves the
			// exposure unknown; skipped and unsupported tasks sent nothing.
			if s := st.facts.Status; s != string(domain.TaskSkippedPolicy) && s != string(domain.TaskUnsupported) {
				known = false
			}
			continue
		}
		rec := st.reviewResult.R1
		if !r1Lineage(rec, states, obs) {
			st.rejected = true
			limits.add(LimReviewLineage + ":" + key)
			known = false
			continue
		}
		sent := false
		for _, c := range rec.Calls {
			r.Coverage.ReviewCalls = append(r.Coverage.ReviewCalls, ReviewCall{Role: c.Role, Model: c.Model, Version: c.Version, PackDigest: c.PackDigest,
				RequestDigest: c.RequestDigest, Outcome: c.Outcome, PackID: c.PackID, PackVersion: c.PackVersion})
			sent = sent || c.RequestDigest != ""
		}
		if sent {
			for _, e := range rec.Evidence {
				exposed[e.Path] = true
			}
		}
	}
	if known {
		n := len(exposed)
		r.Coverage.LLMExposedFiles = &n
	}
	var candidates []record.R1Candidate
	verdicts := map[string]record.Verification{}
	evidence := map[string]record.Excerpt{}
	if v := states["review.verify"]; r1Accepted(v) {
		rec := v.reviewResult.R1
		candidates = rec.Candidates
		for _, x := range rec.Verifications {
			verdicts[x.CandidateID] = x
		}
		for _, e := range rec.Evidence {
			evidence[e.ID] = e
		}
	} else {
		for _, stage := range []string{record.StageInvestigate, record.StageInterpret} {
			if st := states["review."+stage]; r1Accepted(st) {
				candidates = append(candidates, st.reviewResult.R1.Candidates...)
				for _, e := range st.reviewResult.R1.Evidence {
					evidence[e.ID] = e
				}
			}
		}
	}
	for _, c := range candidates {
		f := reviewFinding(r.Inputs.SnapshotID, c.Candidate, verdicts[c.ID], evidence)
		f.ObservationIDs = append([]string{}, c.ObservationIDs...)
		setModelFingerprint(&f, c.Candidate, evidence, linkedObservations(obs, c.ObservationIDs))
		r.Findings = append(r.Findings, f)
	}
}
