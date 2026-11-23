// ReviewDecoder validates review stage records (schema 0.2); the lineage
// between stages, upstream artifacts and tool observations is checked when
// the report is composed, after every artifact was decoded.
package report

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// LimReviewLineage marks a review record whose upstream inputs do not match
// the artifacts the report accepted.
const LimReviewLineage = "review_lineage_mismatch"

const ContractReviewSince = 12
const WorkflowReviewSince = 14
const WorkflowReadingSince = 15

// CitedLinesSince is the first builder that reads review records of schema
// 0.7 (record.SchemaVersionCitedLines): line-numbered requests, narrowed
// citations, a candidate's mechanism and the request budget's longer prose.
const CitedLinesSince = 20

type (
	ReviewDecoder          struct{}
	ContractReviewDecoder  struct{}
	WorkflowReviewDecoder  struct{}
	WorkflowReadingDecoder struct{}
	CitedReviewDecoder     struct{}
)

// CitedReviewDecoder reads a record of schema 0.7, which every role and
// slice writes: the task it is attached to decides the stage and slice
// the record must name, as the earlier decoders did for their variants.
func (CitedReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionCitedLines}
}

func (CitedReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if art.Role != "other" {
		return Contribution{}, invalidf("review record attached to unexpected role")
	}
	if _, _, ok := record.SliceTask(ctx.Task.Key); ok {
		return SliceReviewDecoder{}.Decode(ctx, art, body)
	}
	if ctx.Task.Key == record.TaskContract {
		return ContractReviewDecoder{}.Decode(ctx, art, body)
	}
	return ReviewDecoder{}.Decode(ctx, art, body)
}

// proseLimit is the longest model prose field a report of this builder
// version keeps.
func proseLimit(version string) int {
	if n, err := strconv.Atoi(version); err == nil && n >= CitedLinesSince {
		return record.MaxProseText
	}
	return record.LegacyProseText
}

func (WorkflowReadingDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionWorkflowReading}
}

func (WorkflowReadingDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if ctx.Task.Key != "review.investigate" || art.Role != "other" {
		return Contribution{}, invalidf("workflow reading record attached to unexpected task or role")
	}
	return decodeWholeReview(ctx, body, record.StageInvestigate)
}

func (WorkflowReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionWorkflow}
}

func (WorkflowReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if ctx.Task.Key != "review.documents" || art.Role != "other" {
		return Contribution{}, invalidf("workflow review record attached to unexpected task or role")
	}
	return decodeWholeReview(ctx, body, record.StageDocuments)
}

func (ContractReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionContract}
}

func (ContractReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if (ctx.Task.Key != record.TaskContract && ctx.Task.Key != "review.investigate") || art.Role != "other" ||
		(ctx.Task.Key == record.TaskContract && ctx.Task.Required) {
		return Contribution{}, invalidf("contract review record attached to unexpected task or role")
	}
	return decodeWholeReview(ctx, body, strings.TrimPrefix(ctx.Task.Key, "review."))
}

func (ReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersion}
}

func (ReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	stage, ok := strings.CutPrefix(ctx.Task.Key, "review.")
	if !ok || !slices.Contains(record.Stages, stage) || art.Role != "other" {
		return Contribution{}, invalidf("review record attached to unexpected task or role")
	}
	return decodeWholeReview(ctx, body, stage)
}

func decodeWholeReview(ctx DecodeContext, body []byte, stage string) (Contribution, error) {
	r, err := record.Decode(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), Record: &r}, Limitations: r.PublicLimitations()}, nil
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

// reviewAccepted reports a stage whose accepted record the report keeps.
func reviewAccepted(st *taskState) bool {
	return st != nil && !st.rejected && st.facts.Status == string(domain.TaskSucceeded) && st.reviewResult != nil && st.reviewResult.Record != nil && st.reviewResult.Record.Accepted()
}

// reviewLineage checks one record against the artifacts the report accepted.
func reviewLineage(rec *record.Record, states map[string]*taskState, obs map[string]Observation) bool {
	up := map[string]record.Upstream{}
	// A review upstream is a role record, or the scope artifact of a slice.
	accepted := func(source string, st *taskState) bool {
		if source == record.TaskScope {
			return scopeAccepted(st)
		}
		return reviewAccepted(st)
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
	if rec.Slice != nil && rec.Stage == record.StageInvestigate {
		st := states[record.TaskScope]
		if scopeAccepted(st) && st.scope.LineCoverage() && !sliceInvestigationInScope(*rec, st.scope) {
			return false
		}
	}
	if rec.WorkflowReading != nil {
		st := states["review.documents"]
		if !reviewAccepted(st) || !rec.WorkflowReadingMatches(*st.reviewResult.Record) {
			return false
		}
	}
	if len(rec.ContractComparisons) > 0 {
		st := states["review.documents"]
		if !reviewAccepted(st) {
			return false
		}
		docs := st.reviewResult.Record
		docEvidence := make(map[string]record.Excerpt, len(docs.Evidence))
		for _, e := range docs.Evidence {
			docEvidence[e.ID] = e
		}
		for _, e := range rec.Evidence {
			if old, ok := docEvidence[e.ID]; ok && (e.PackageReference != old.PackageReference) {
				return false
			}
		}
		for _, x := range rec.ContractComparisons {
			if !x.MatchesContract(docs.Contracts) {
				return false
			}
		}
	}
	if rec.Stage == record.StageContract && rec.Accepted() {
		docs, inv := states["review.documents"], states["review.investigate"]
		return reviewAccepted(docs) && reviewAccepted(inv) &&
			up["review.documents"].State == record.UpstreamAccepted && up["review.investigate"].State == record.UpstreamAccepted &&
			contractSliceLineage(rec, states) &&
			record.ValidateContractFollowup(*rec, *docs.reviewResult.Record, *inv.reviewResult.Record)
	}
	if rec.Stage != record.StageVerify {
		return true
	}
	var want []record.Candidate
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
		prior := states[source].reviewResult.Record
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

// The optional follow-up can use a later slot only when one of its named,
// already accepted investigate or verify records supplied a sent window.
func contractSliceLineage(rec *record.Record, states map[string]*taskState) bool {
	matched := map[string]bool{}
	for _, u := range rec.Upstream {
		_, key, slice := record.SliceTask(u.Source)
		if slice {
			st := states[u.Source]
			if !reviewAccepted(st) {
				return false
			}
			matched[key] = matched[key] || record.ContractSliceEvidence(*rec, *st.reviewResult.Record)
		}
	}
	for _, used := range matched {
		if !used {
			return false
		}
	}
	return true
}

func nonNilCandidates(l []record.Candidate) []record.Candidate {
	if l == nil {
		return []record.Candidate{}
	}
	return l
}

// composeReviews joins the review stage records: lineage, call
// provenance (with the pack identity), exposed files and findings. Findings
// carry verifier verdicts only from a succeeded verify task with an accepted
// record; otherwise the accepted candidates stay unreviewed.
func composeReviews(r *Report, states map[string]*taskState, limits *stringSet) {
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	exposed, known := map[string]bool{}, true
	if scope := states[record.TaskScope]; scopeAccepted(scope) && scope.scope.LineCoverage() &&
		!coverageOverviewInScope(scope.scope, states["review.investigate"]) {
		scope.rejected = true
		limits.add(LimReviewLineage + ":" + record.TaskScope)
	}
	// The whole-run roles, then each slice's investigate and verify: a slice
	// verify's lineage reads its investigate's acceptance.
	for _, key := range reviewRecordKeys(states) {
		st := states[key]
		if st == nil {
			continue
		}
		if st.rejected || st.reviewResult == nil || st.reviewResult.Record == nil {
			// A task that ran a model call without a usable record leaves the
			// exposure unknown; skipped, unsupported and budget-exhausted tasks
			// sent nothing.
			if !noCallStatus(st.facts.Status) {
				known = false
			}
			continue
		}
		rec := st.reviewResult.Record
		if !reviewLineage(rec, states, obs) {
			st.rejected = true
			limits.add(LimReviewLineage + ":" + key)
			// An invalid upstream binding rules out the final comparison.
			// Its decoded record still accounts for the model requests sent.
			if key != record.TaskContract {
				known = false
				continue
			}
			st.contractLineageRejected = true
		}
		sent := false
		for _, c := range rec.Calls {
			if key == record.TaskContract && c.RequestDigest == "" {
				continue
			}
			r.Coverage.ReviewCalls = append(r.Coverage.ReviewCalls, ReviewCall{Role: c.Role, Purpose: c.Purpose, Model: c.Model, Version: c.Version, PackDigest: c.PackDigest,
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
	var candidates []record.Candidate
	verdicts := map[string]record.Verification{}
	evidence := map[string]record.Excerpt{}
	if v := states["review.verify"]; reviewAccepted(v) {
		rec := v.reviewResult.Record
		candidates = rec.Candidates
		for _, x := range rec.Verifications {
			verdicts[x.CandidateID] = x
		}
		for _, e := range rec.Evidence {
			evidence[e.ID] = e
		}
	} else {
		for _, stage := range []string{record.StageInvestigate, record.StageInterpret} {
			if st := states["review."+stage]; reviewAccepted(st) {
				candidates = append(candidates, st.reviewResult.Record.Candidates...)
				for _, e := range st.reviewResult.Record.Evidence {
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
		var from *record.Record
		switch {
		case reviewAccepted(ver):
			from = ver.reviewResult.Record
			for _, x := range from.Verifications {
				verdicts[x.CandidateID] = x
			}
		case reviewAccepted(inv):
			from = inv.reviewResult.Record
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
func addFindings(r *Report, prefix string, candidates []record.Candidate, verdicts map[string]record.Verification, evidence map[string]record.Excerpt, obs map[string]Observation) {
	for _, c := range candidates {
		f := reviewFinding(r.Inputs.SnapshotID, c.CandidateText, verdicts[c.ID], evidence)
		f.ID = "review_" + prefix + c.ID
		f.ObservationIDs = append([]string{}, c.ObservationIDs...)
		setModelFingerprint(&f, c.CandidateText, evidence, linkedObservations(obs, c.ObservationIDs))
		r.Findings = append(r.Findings, f)
	}
}

// reviewFinding turns one accepted review candidate and its verdict into a
// finding; v is the platform-validated verification, or the zero value when
// none was accepted.
func reviewFinding(snapshot string, c record.CandidateText, v record.Verification, catalog map[string]record.Excerpt) Finding {
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
	if c.Mechanism != "" {
		// How the cited lines produce the failure: a claim on the same
		// evidence as the candidate.
		f.Claims = append(f.Claims, Claim{Assertion: c.Mechanism, Kind: "inferred", EvidenceIDs: c.Evidence})
	}
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
	setModelFingerprint(&f, c, catalog, nil)
	return f
}
