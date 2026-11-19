// RoleReviewDecoder validates role-pack stage records (schema 0.2); the
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

// Limitation codes of role-pack composition.
const (
	LimReviewLineage      = "review_lineage_mismatch"
	LimReviewPackMismatch = "review_pack_mismatch"
)

type RoleReviewDecoder struct{}

func (RoleReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionRole}
}

func (RoleReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	stage, ok := strings.CutPrefix(ctx.Task.Key, "review.")
	if !ok || !slices.Contains(record.RoleStages, stage) || art.Role != "other" {
		return Contribution{}, invalidf("review-r1 record attached to unexpected task or role")
	}
	r, err := record.DecodeRole(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), Role: &r}, Limitations: r.Limitations}, nil
}

// upstreamMedia is the artifact media type each upstream source must match.
func upstreamMedia(source string) string {
	switch {
	case source == envdoc.TaskEnvironment, source == envdoc.TaskDocuments:
		return envdoc.MediaType
	case source == record.TaskScope:
		return record.ScopeMediaType
	case strings.HasPrefix(source, "review."):
		return record.MediaType
	}
	return analysis.MediaObservations
}

// roleAccepted reports a stage whose accepted record the report keeps.
func roleAccepted(st *taskState) bool {
	return st != nil && !st.rejected && st.facts.Status == string(domain.TaskSucceeded) && st.reviewResult != nil && st.reviewResult.Role != nil && st.reviewResult.Role.Accepted()
}

// roleLineage checks one record against the artifacts the report accepted.
func roleLineage(rec *record.RoleRecord, states map[string]*taskState, obs map[string]Observation) bool {
	up := map[string]record.Upstream{}
	// A review upstream is a role record, or the scope artifact of a slice.
	accepted := func(source string, st *taskState) bool {
		if source == record.TaskScope {
			return scopeAccepted(st)
		}
		return roleAccepted(st)
	}
	for _, u := range rec.Upstream {
		up[u.Source] = u
		st := states[u.Source]
		if u.State == record.UpstreamAccepted {
			if st == nil || st.rejected || st.decodedSHA[upstreamMedia(u.Source)] != u.SHA256 {
				return false
			}
			if strings.HasPrefix(u.Source, "review.") && !accepted(u.Source, st) {
				return false
			}
		} else if strings.HasPrefix(u.Source, "review.") && accepted(u.Source, st) {
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
	var want []record.RoleCandidate
	// One excerpt ID (bound to path, blob and range) may reach verify from
	// both stages with a different origin label (context vs observation);
	// verify must carry one of the upstream variants unchanged.
	evidence := map[string][]record.Excerpt{}
	// A slice verify checks its own slice's investigate candidates.
	sources := []string{"review." + record.StageInvestigate, "review." + record.StageInterpret}
	if rec.Slice != nil {
		sources = []string{"review." + record.StageInvestigate + "." + rec.Slice.Key}
	}
	for _, source := range sources {
		u, ok := up[source]
		if !ok || u.State != record.UpstreamAccepted {
			continue
		}
		prior := states[source].reviewResult.Role
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

func nonNilCandidates(l []record.RoleCandidate) []record.RoleCandidate {
	if l == nil {
		return []record.RoleCandidate{}
	}
	return l
}

// composeRoleReviews joins the role-pack stage records: lineage, call
// provenance (with the pack identity), exposed files and findings. Findings
// carry verifier verdicts only from a succeeded verify task with an accepted
// record; otherwise the accepted candidates stay unreviewed.
func composeRoleReviews(r *Report, states map[string]*taskState, limits *stringSet) {
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	for k, st := range states {
		if strings.HasPrefix(k, "review.") && st.reviewResult != nil && st.reviewResult.Role == nil && !st.rejected {
			st.rejected = true
			limits.add(LimReviewPackMismatch + ":" + k)
		}
	}
	exposed, known := map[string]bool{}, true
	// The whole-run roles, then each slice's investigate and verify: a slice
	// verify's lineage reads its investigate's acceptance.
	for _, key := range roleRecordKeys(states) {
		st := states[key]
		if st == nil {
			continue
		}
		if st.rejected || st.reviewResult == nil || st.reviewResult.Role == nil {
			// A task that ran a model call without a usable record leaves the
			// exposure unknown; skipped, unsupported and budget-exhausted tasks
			// sent nothing.
			if !noCallStatus(st.facts.Status) {
				known = false
			}
			continue
		}
		rec := st.reviewResult.Role
		if !roleLineage(rec, states, obs) {
			st.rejected = true
			limits.add(LimReviewLineage + ":" + key)
			known = false
			continue
		}
		sent := false
		for _, c := range rec.Calls {
			r.Coverage.ReviewCalls = append(r.Coverage.ReviewCalls, ReviewCall{Role: c.Role, Model: c.Model, Version: c.Version, PackDigest: c.PackDigest,
				RequestDigest: c.RequestDigest, Outcome: c.Outcome, PackID: c.PackID, PackVersion: c.PackVersion, TaskKey: key})
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
	var candidates []record.RoleCandidate
	verdicts := map[string]record.Verification{}
	evidence := map[string]record.Excerpt{}
	if v := states["review.verify"]; roleAccepted(v) {
		rec := v.reviewResult.Role
		candidates = rec.Candidates
		for _, x := range rec.Verifications {
			verdicts[x.CandidateID] = x
		}
		for _, e := range rec.Evidence {
			evidence[e.ID] = e
		}
	} else {
		for _, stage := range []string{record.StageInvestigate, record.StageInterpret} {
			if st := states["review."+stage]; roleAccepted(st) {
				candidates = append(candidates, st.reviewResult.Role.Candidates...)
				for _, e := range st.reviewResult.Role.Evidence {
					evidence[e.ID] = e
				}
			}
		}
	}
	addFindings(r, "", candidates, verdicts, evidence, obs)
	// Each slice's candidates, verified by its own verify; their finding IDs
	// carry the slice key (review_sNNN_<candidate>).
	for _, key := range sliceKeys(states) {
		inv, ver := states["review.investigate."+key], states["review.verify."+key]
		verdicts, evidence := map[string]record.Verification{}, map[string]record.Excerpt{}
		var from *record.RoleRecord
		switch {
		case roleAccepted(ver):
			from = ver.reviewResult.Role
			for _, x := range from.Verifications {
				verdicts[x.CandidateID] = x
			}
		case roleAccepted(inv):
			from = inv.reviewResult.Role
		default:
			continue
		}
		for _, e := range from.Evidence {
			evidence[e.ID] = e
		}
		addFindings(r, key+"_", from.Candidates, verdicts, evidence, obs)
	}
}

// addFindings turns accepted candidates into findings; prefix is the slice
// key part of a slice candidate's finding ID.
func addFindings(r *Report, prefix string, candidates []record.RoleCandidate, verdicts map[string]record.Verification, evidence map[string]record.Excerpt, obs map[string]Observation) {
	for _, c := range candidates {
		f := reviewFinding(r.Inputs.SnapshotID, c.Candidate, verdicts[c.ID], evidence)
		f.ID = "review_" + prefix + c.ID
		f.ObservationIDs = append([]string{}, c.ObservationIDs...)
		setModelFingerprint(&f, c.Candidate, evidence, linkedObservations(obs, c.ObservationIDs))
		r.Findings = append(r.Findings, f)
	}
}
