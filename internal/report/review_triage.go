package report

import (
	"regexp"
	"slices"
	"sort"
	"strconv"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Diagnostic triage (docs/REPORT.md#도구-진단-분류): the records of the
// planned triage slots (review record 0.8, review.triage.tNNN) and their
// verifications (review.verify.tNNN), the findings of the groups the model
// classified as problems and the code review section's diagnostic_triage
// object, whose counts come from the report's own observations.

// TriageSince is the first builder that reads triage records and writes
// diagnostic_triage.
const TriageSince = 22

// Triage buckets: how the diagnostics fold shows a classified group.
const (
	// TriageAttention: a problem the verification did not refute, or a group
	// the model could not judge.
	TriageAttention = "attention"
	// TriageHarmless: judged harmless in this code, with its reason.
	TriageHarmless = "harmless"
	// TriageRefuted: a problem the verification refuted.
	TriageRefuted = "refuted"
)

var triageFindingID = regexp.MustCompile(`^review_(t[0-9]{3})_`)

// TriageReviewDecoder validates a triage slot record (schema 0.8) attached
// to its own optional triage or verify task.
type TriageReviewDecoder struct{}

func (TriageReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionTriage}
}

func (TriageReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	stage, key, ok := record.TriageTask(ctx.Task.Key)
	if !ok || art.Role != "other" || ctx.Task.Required {
		return Contribution{}, invalidf("triage record attached to unexpected task or role")
	}
	r, err := record.Decode(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Slice: key, Lookup: ctx.Source.Lookup, ShippedSource: ctx.Source.shippedSource()})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), Record: &r}, Limitations: r.PublicLimitations()}, nil
}

// isTriageTask reports a triage slot task key (review.triage.tNNN,
// review.verify.tNNN).
func isTriageTask(key string) bool {
	_, _, ok := record.TriageTask(key)
	return ok
}

// triageKeys are the slot keys of the planned triage tasks, in order.
func triageKeys(states map[string]*taskState) []string {
	seen := map[string]bool{}
	var out []string
	for k := range states {
		if _, key, ok := record.TriageTask(k); ok && !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// triageVerifyKey is the verify task of a triage slot record, or "".
func triageSlot(rec *record.Record) string {
	if rec.Slice != nil && record.ValidTriageKey(rec.Slice.Key) {
		return rec.Slice.Key
	}
	return ""
}

// composeTriage adds the findings of every slot's problem groups, with the
// verdicts of the slot's verify task when usable (review_tNNN_triage_N).
func composeTriage(r *Report, states map[string]*taskState, obs map[string]Observation) {
	for _, key := range triageKeys(states) {
		tri, ver := states["review."+record.StageTriage+"."+key], states["review."+record.StageVerify+"."+key]
		verdicts, evidence := map[string]record.Verification{}, map[string]record.Excerpt{}
		var from *record.Record
		switch {
		case !reviewAccepted(tri):
			continue
		case verdictsUsable("review."+record.StageVerify+"."+key, ver):
			from = ver.reviewResult.Record
			for _, x := range from.Verifications {
				verdicts[x.CandidateID] = x
			}
		default:
			from = tri.reviewResult.Record
		}
		for _, e := range from.Evidence {
			evidence[e.ID] = e
		}
		addFindings(r, key+"_", from.Candidates, verdicts, evidence, obs, false, nil)
	}
}

// DiagnosticTriage is what the triage slots did with the tool diagnostics
// (builder 22). Diagnostics are the report's unsuppressed observations of
// the static analysis tools (not metrics, not secrets); Triaged those in a
// classified group of an accepted triage record; Interpreted those an
// interpretation finding carries and no group classified. The rest were not
// triaged: never read as clean.
type DiagnosticTriage struct {
	Tasks       []AIReviewTask `json:"tasks"`
	Diagnostics int            `json:"diagnostics"`
	Triaged     int            `json:"triaged"`
	Interpreted int            `json:"interpreted"`
	Groups      []TriageResult `json:"groups"`
	// Limitations are the accepted triage records' public limitation codes.
	Limitations []string `json:"limitations"`
}

// TriageResult is one classified group. Reason is the model's statement of
// a harmless or unclear group, Locations the code it cited; FindingID is a
// problem group's finding.
type TriageResult struct {
	TaskKey        string     `json:"task_key"`
	GroupID        string     `json:"group_id"`
	Classification string     `json:"classification"`
	Bucket         string     `json:"bucket"`
	ObservationIDs []string   `json:"observation_ids"`
	CrossTool      bool       `json:"cross_tool"`
	Reason         *string    `json:"reason"`
	Locations      []Location `json:"locations"`
	FindingID      *string    `json:"finding_id"`
}

// triageDecided counts the diagnostics of the groups classified as problem
// or harmless, and those of the groups left unclear.
func triageDecided(d *DiagnosticTriage) (decided, unclear int) {
	for _, g := range d.Groups {
		if g.Classification == record.TriageUnclear {
			unclear += len(g.ObservationIDs)
		} else {
			decided += len(g.ObservationIDs)
		}
	}
	return decided, unclear
}

// diagnosticIDs are the report's triage-eligible observations in section
// order.
func diagnosticIDs(r Report) []string {
	obs := make(map[string]Observation, len(r.Observations))
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	var out []string
	for _, lang := range regularStaticLanguages(r) {
		for _, t := range lang.Tools {
			for _, id := range t.ObservationIDs {
				if o, ok := obs[id]; ok && !o.Suppressed {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// triageBucket is the bucket of a classified group with its finding.
func triageBucket(classification string, finding *Finding) string {
	switch {
	case classification == record.TriageHarmless:
		return TriageHarmless
	case classification == record.TriageProblem && finding != nil && finding.Validation == ValidationRefuted:
		return TriageRefuted
	}
	return TriageAttention
}

// interpretedIDs are the observations a whole-run review finding (an
// interpretation candidate) carries.
func interpretedIDs(r Report) map[string]bool {
	out := map[string]bool{}
	for _, f := range r.Findings {
		if len(f.ID) > len("review_") && f.ID[:len("review_")] == "review_" && !sliceFindingID.MatchString(f.ID) && !triageFindingID.MatchString(f.ID) {
			for _, id := range f.ObservationIDs {
				out[id] = true
			}
		}
	}
	return out
}

// diagnosticTriage builds the object when the plan held triage slots.
func (c sectionContext) diagnosticTriage() *DiagnosticTriage {
	keys := triageKeys(c.states)
	if len(keys) == 0 {
		return nil
	}
	d := &DiagnosticTriage{Tasks: []AIReviewTask{}, Groups: []TriageResult{}, Limitations: []string{}}
	findings := map[string]*Finding{}
	for i := range c.r.Findings {
		findings[c.r.Findings[i].ID] = &c.r.Findings[i]
	}
	triaged := map[string]bool{}
	for _, key := range keys {
		for _, stage := range []string{record.StageTriage, record.StageVerify} {
			item, _ := reviewItem("review."+stage+"."+key, c.states["review."+stage+"."+key])
			d.Tasks = append(d.Tasks, item)
		}
		st := c.states["review."+record.StageTriage+"."+key]
		if !reviewAccepted(st) {
			continue
		}
		rec := st.reviewResult.Record
		catalog := map[string]record.Excerpt{}
		for _, e := range rec.Evidence {
			catalog[e.ID] = e
		}
		for _, l := range rec.PublicLimitations() {
			if !slices.Contains(d.Limitations, l) {
				d.Limitations = append(d.Limitations, l)
			}
		}
		for _, g := range rec.TriageGroups {
			if g.Classification == "" {
				continue
			}
			res := TriageResult{TaskKey: "review." + record.StageTriage + "." + key, GroupID: g.ID, Classification: g.Classification,
				ObservationIDs: append([]string{}, g.ObservationIDs...), CrossTool: g.CrossTool, Locations: []Location{}}
			var f *Finding
			if g.CandidateID != "" {
				id := "review_" + key + "_" + g.CandidateID
				if f = findings[id]; f != nil {
					res.FindingID = &id
				}
			}
			res.Bucket = triageBucket(g.Classification, f)
			if g.Reason != "" {
				reason := g.Reason
				res.Reason = &reason
			}
			for _, id := range g.EvidenceIDs {
				if e, ok := catalog[id]; ok {
					res.Locations = append(res.Locations, Location{SnapshotID: c.r.Inputs.SnapshotID, Path: e.Path, StartLine: e.Start, EndLine: e.End})
				}
			}
			for _, id := range g.ObservationIDs {
				triaged[id] = true
			}
			d.Groups = append(d.Groups, res)
		}
	}
	interpreted := interpretedIDs(*c.r)
	for _, id := range diagnosticIDs(*c.r) {
		d.Diagnostics++
		switch {
		case triaged[id]:
			d.Triaged++
		case interpreted[id]:
			d.Interpreted++
		}
	}
	return d
}

// validateDiagnosticTriage checks the object against the report: present
// only in a builder-22 report with planned triage tasks, every task row
// repeats its task, every group names diagnostics of the report once and a
// finding of its slot, its bucket follows from its classification and
// finding, and the counts follow from the observations and findings.
func validateDiagnosticTriage(r Report) error {
	n, _ := strconv.Atoi(r.Builder.Version)
	tasks := map[string]Task{}
	planned := false
	for _, t := range r.Tasks {
		tasks[t.Key] = t
		planned = planned || isTriageTask(t.Key)
	}
	for _, f := range r.Findings {
		if m := triageFindingID.FindStringSubmatch(f.ID); m != nil {
			if _, ok := tasks["review."+record.StageTriage+"."+m[1]]; n < TriageSince || !ok {
				return invalidf("finding %s of a triage slot that was not planned", f.ID)
			}
		}
	}
	var d *DiagnosticTriage
	if r.CodeReview != nil {
		d = r.CodeReview.DiagnosticTriage
	}
	switch {
	case d == nil && (!planned || n < TriageSince || r.CodeReview == nil):
		return nil
	case d == nil:
		return invalidf("builder %s report with triage tasks but no diagnostic triage", r.Builder.Version)
	case n < TriageSince || !planned:
		return invalidf("diagnostic triage without triage tasks or in a builder %s revision", r.Builder.Version)
	}
	listed := map[string]bool{}
	for _, t := range d.Tasks {
		row, ok := tasks[t.Key]
		if !ok || !isTriageTask(t.Key) || listed[t.Key] || t.TaskStatus == nil || *t.TaskStatus != row.Status || deref(t.Reason) != deref(row.Reason) {
			return invalidf("diagnostic triage task %s differs from its task", t.Key)
		}
		listed[t.Key] = true
	}
	for k := range tasks {
		if isTriageTask(k) && !listed[k] {
			return invalidf("diagnostic triage omits task %s", k)
		}
	}
	findings := map[string]*Finding{}
	for i := range r.Findings {
		findings[r.Findings[i].ID] = &r.Findings[i]
	}
	eligible := map[string]bool{}
	for _, id := range diagnosticIDs(r) {
		eligible[id] = true
	}
	free := !r.Export.IncludeCode
	seen := map[string]bool{}
	for _, g := range d.Groups {
		_, key, ok := record.TriageTask(g.TaskKey)
		if !ok || !listed[g.TaskKey] || !slices.Contains([]string{record.TriageProblem, record.TriageHarmless, record.TriageUnclear}, g.Classification) ||
			len(g.ObservationIDs) == 0 || g.Locations == nil {
			return invalidf("diagnostic triage group %s is invalid", g.GroupID)
		}
		for _, id := range g.ObservationIDs {
			if !eligible[id] || seen[id] {
				return invalidf("diagnostic triage group %s names an unknown or repeated diagnostic", g.GroupID)
			}
			seen[id] = true
		}
		var f *Finding
		if g.FindingID != nil {
			f = findings[*g.FindingID]
			if f == nil || g.Classification != record.TriageProblem || triageFindingID.FindStringSubmatch(*g.FindingID) == nil ||
				triageFindingID.FindStringSubmatch(*g.FindingID)[1] != key || !slices.Equal(f.ObservationIDs, g.ObservationIDs) {
				return invalidf("diagnostic triage group %s names a finding of another slot", g.GroupID)
			}
		}
		if g.Bucket != triageBucket(g.Classification, f) {
			return invalidf("diagnostic triage group %s bucket does not follow", g.GroupID)
		}
		if (g.Reason == nil) != (g.Classification == record.TriageProblem) ||
			(g.Reason != nil && (*g.Reason == "" || (free && *g.Reason != textOmitted) || record.Sensitive([]byte(*g.Reason)))) {
			return invalidf("diagnostic triage group %s reason does not follow the export mode", g.GroupID)
		}
		for _, l := range g.Locations {
			if domain.ValidateEvidencePath(l.Path) != nil || l.StartLine < 1 || l.EndLine < l.StartLine || l.Symbol != "" {
				return invalidf("diagnostic triage group %s location is invalid", g.GroupID)
			}
		}
	}
	interpreted := interpretedIDs(r)
	triaged, interp := 0, 0
	for id := range eligible {
		switch {
		case seen[id]:
			triaged++
		case interpreted[id]:
			interp++
		}
	}
	if d.Diagnostics != len(eligible) || d.Triaged != triaged || d.Interpreted != interp {
		return invalidf("diagnostic triage counts do not follow from the observations")
	}
	for _, l := range d.Limitations {
		if !record.ValidCode(l) {
			return invalidf("diagnostic triage limitation %q is invalid", l)
		}
	}
	return nil
}

// projectTriage is the code-free copy: the model's reasons are replaced.
func projectTriage(d *DiagnosticTriage) *DiagnosticTriage {
	if d == nil {
		return nil
	}
	c := *d
	c.Tasks = slices.Clone(d.Tasks)
	c.Limitations = slices.Clone(d.Limitations)
	c.Groups = make([]TriageResult, len(d.Groups))
	for i, g := range d.Groups {
		g.ObservationIDs = slices.Clone(g.ObservationIDs)
		g.Locations = slices.Clone(g.Locations)
		if g.Reason != nil {
			omitted := textOmitted
			g.Reason = &omitted
		}
		c.Groups[i] = g
	}
	return &c
}
