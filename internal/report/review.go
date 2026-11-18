// ReviewDecoder validates worker records against the fixed snapshot. Joining
// the records is a separate step because task artifacts may arrive in any order.
package report

import (
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type ReviewDecoder struct{}

func (ReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersion}
}

func (ReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	stage, ok := strings.CutPrefix(ctx.Task.Key, "review.")
	if !ok || !slices.Contains(RequiredReviewTasks, ctx.Task.Key) || art.Role != "other" {
		return Contribution{}, invalidf("review document attached to unexpected task or role")
	}
	r, err := record.Decode(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), Record: &r}, Limitations: r.Limitations}, nil
}

func continues(previous, current *record.Record) bool {
	if len(current.Calls) < len(previous.Calls) ||
		!reflect.DeepEqual(previous.Calls, current.Calls[:len(previous.Calls)]) ||
		!reflect.DeepEqual(previous.Facts, current.Facts) || len(current.Evidence) < len(previous.Evidence) {
		return false
	}
	for i, e := range previous.Evidence {
		next := current.Evidence[i]
		if next.Origin == "context" && e.Origin == "" {
			next.Origin = ""
		}
		if e != next {
			return false
		}
	}
	if current.Stage == "investigate" {
		return len(previous.Evidence) == len(current.Evidence)
	}
	return reflect.DeepEqual(previous.Candidates, current.Candidates)
}

func composeReviews(r *Report, states map[string]*taskState, limits *stringSet) {
	var previous, findings *record.Record
	for _, key := range RequiredReviewTasks {
		st := states[key]
		if st == nil || st.rejected || st.reviewResult == nil || st.reviewResult.Record == nil {
			previous = nil
			continue
		}
		rec := st.reviewResult.Record
		if key != "review.context" && (previous == nil || !continues(previous, rec)) {
			st.rejected = true
			limits.add("review_lineage_mismatch:" + key)
			previous = nil
			continue
		}
		for _, c := range rec.Calls {
			if c.Role == rec.Stage {
				outcome := c.Outcome
				if outcome == "" {
					outcome = record.OutcomeAccepted
				}
				r.Coverage.ReviewCalls = append(r.Coverage.ReviewCalls, ReviewCall{Role: c.Role, Model: c.Model, Version: c.Version, PackDigest: c.PackDigest, RequestDigest: c.RequestDigest, Outcome: outcome})
			}
		}
		if st.facts.Status != string(domain.TaskSucceeded) || !rec.Accepted() {
			// A failed verifier can retain individually validated verdicts
			// alongside quarantined candidates and a failed task status.
			if key == "review.verify" && st.facts.Status == string(domain.TaskFailed) && rec.Outcome == record.OutcomeFailed {
				findings = rec
			}
			previous = nil
			continue
		}
		previous = rec
		if key != "review.context" {
			findings = rec
		}
	}
	if previous != nil && previous.Stage == "verify" {
		files := map[string]bool{}
		for _, e := range previous.Evidence {
			files[e.Path] = true
		}
		count := len(files)
		r.Coverage.LLMExposedFiles = &count
	}
	if findings == nil {
		return
	}
	evidence := map[string]record.Excerpt{}
	for _, e := range findings.Evidence {
		evidence[e.ID] = e
	}
	verdicts := map[string]record.Verification{}
	for _, v := range findings.Verifications {
		verdicts[v.CandidateID] = v
	}
	for _, candidate := range findings.Candidates {
		r.Findings = append(r.Findings, reviewFinding(r.Inputs.SnapshotID, candidate, verdicts[candidate.ID], evidence))
	}
}

func reviewFinding(snapshot string, c record.Candidate, v record.Verification, catalog map[string]record.Excerpt) Finding {
	title := c.Title
	if utf8.RuneCountInString(title) < 5 {
		title = "검토 후보 · " + title
	}
	f := Finding{SchemaVersion: SchemaVersion, ID: "review_" + c.ID, Title: title,
		Category: "correctness", Severity: "info", SeverityReason: "검증 결과에서 심각도를 확인하지 못했습니다.",
		Validation: "unreviewed", Disposition: "open", Impact: c.Impact,
		Conditions:         []Condition{{Statement: c.Condition, Status: "assumed", EvidenceIDs: c.Evidence}},
		Claims:             []Claim{{Assertion: c.Title, Kind: "inferred", EvidenceIDs: c.Evidence}},
		CounterevidenceIDs: []string{}, ObservationIDs: []string{}, Limitations: append([]string{}, v.Limitations...),
		Recommendation: Recommendation{Summary: c.Improvement, Tradeoffs: []string{}},
		Verification:   []string{"호출자와 테스트에서 후보의 발생 조건을 확인하세요."}}
	if v.Verdict != "" && v.Verdict != record.VerdictUnreviewed {
		f.Validation, f.Category, f.Severity, f.SeverityReason = v.Verdict, v.Category, v.Severity, v.SeverityReason
		f.Impact, f.Verification = v.Impact, v.NextVerification
		f.CounterevidenceIDs = append(f.CounterevidenceIDs, v.Counterevidence...)
		for _, cond := range v.ValidConditions {
			f.Conditions = append(f.Conditions, Condition{Statement: cond, Status: "assumed", EvidenceIDs: v.SupportingEvidence})
		}
		for _, cond := range v.InvalidConditions {
			f.Conditions = append(f.Conditions, Condition{Statement: "후보가 성립하지 않는 조건 · " + cond, Status: "assumed", EvidenceIDs: v.Counterevidence})
		}
		f.Limitations = append(f.Limitations, v.UnresolvedAssumptions...)
		if len(v.SupportingEvidence) != 0 {
			f.Claims = append(f.Claims, Claim{Assertion: v.Impact, Kind: "inferred", EvidenceIDs: v.SupportingEvidence})
		}
		if len(v.Counterevidence) != 0 && len(v.InvalidConditions) != 0 {
			f.Claims = append(f.Claims, Claim{Assertion: strings.Join(v.InvalidConditions, " "), Kind: "inferred", EvidenceIDs: v.Counterevidence})
		}
	}
	if f.Validation == record.VerdictUnreviewed {
		f.Limitations = append(f.Limitations, "후보가 성립하지 않을 가능성 · "+c.Counterpoint)
	}
	seen := map[string]bool{}
	for _, refs := range [][]string{c.Evidence, v.SupportingEvidence, v.Counterevidence} {
		for _, id := range refs {
			if seen[id] {
				continue
			}
			seen[id] = true
			e := catalog[id]
			loc := Location{SnapshotID: snapshot, Path: e.Path, StartLine: e.Start, EndLine: e.End, Symbol: e.Symbol}
			f.Locations = append(f.Locations, loc)
			kind := "code"
			if e.Kind == "document" || e.Kind == "config" {
				kind = "document"
			}
			f.Evidence = append(f.Evidence, Evidence{ID: id, Kind: kind, Description: "고정 스냅샷의 " + e.Kind + " 발췌 위치", Location: &loc, BlobSHA256: e.SHA256})
		}
	}
	f.Fingerprint = domain.Digest([]byte(f.Category + "\x00" + f.Locations[0].Path + "\x00" + c.Title))
	return f
}
