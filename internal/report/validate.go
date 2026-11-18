package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

var (
	statuses     = map[string]bool{StatusCompleted: true, StatusPartial: true, StatusFailed: true, StatusCancelled: true}
	verdicts     = map[string]bool{VerdictAttention: true, VerdictNoActionable: true, VerdictInconclusive: true}
	categories   = map[string]bool{"correctness": true, "error_handling": true, "concurrency": true, "resource_lifecycle": true, "data_integrity": true, "api_contract": true, "security": true, "maintainability": true, "performance": true, "test_gap": true}
	severities   = map[string]bool{"critical": true, "high": true, "medium": true, "low": true, "info": true}
	validations  = map[string]bool{"unreviewed": true, "supported": true, "reproduced": true, "refuted": true, "inconclusive": true}
	dispositions = map[string]bool{"open": true, "acknowledged": true, "accepted_risk": true, "false_positive": true, "fixed_by_user": true, "suppressed": true}
	aiStatuses   = map[string]bool{AIReviewNotRequested: true, AIReviewNotPerformed: true, AIReviewPartial: true, AIReviewPerformed: true}
)

// ForbiddenClaims are phrases a report must not carry in builder-generated
// text unless the AI review was performed (and even then the builder never
// writes them). Raw tool messages are data and are not scanned.
var ForbiddenClaims = []string{"AI 검토 완료", "검토를 완료", "문제 없음", "문제가 없", "결함 없음", "안전합니다", "안전함", "no issues found", "review completed"}

// ValidateReport checks referential integrity, counts and the status/verdict
// rules. Build calls it; the publisher calls it again on stored bytes.
func ValidateReport(r Report) error {
	if r.SchemaVersion != SchemaVersion || r.Demo || !domain.ValidID(r.ID) || !domain.ValidID(r.RunID) || r.Revision < 1 {
		return invalidf("report header is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.CreatedAt); err != nil {
		return invalidf("created_at is invalid")
	}
	if !statuses[r.Status] || !verdicts[r.QualityVerdict] || r.Builder.ID != BuilderID || !hex64.MatchString(r.Builder.InputDigest) {
		return invalidf("status, verdict or builder is invalid")
	}
	if !domain.ValidID(r.Inputs.SnapshotID) || !hex64.MatchString(r.Inputs.ManifestSHA256) || !profiles[r.Inputs.Profile] {
		return invalidf("inputs are invalid")
	}
	if r.Tasks == nil || r.Executions == nil || r.Artifacts == nil || r.Observations == nil || r.Findings == nil || r.Limitations == nil || r.Coverage.Notes == nil || r.Coverage.Tools == nil || r.Summary.Priorities == nil {
		return invalidf("arrays must not be null")
	}
	if !aiStatuses[r.Coverage.AIReview.Status] || (r.Coverage.AIReview.Reason == nil) != (r.Coverage.AIReview.Status == AIReviewPerformed || r.Coverage.AIReview.Status == AIReviewNotRequested) {
		return invalidf("ai_review is inconsistent")
	}
	if want := ComputeSummary(r.Findings, r.Observations); !reflect.DeepEqual(want, r.Summary) {
		return invalidf("summary counts differ from findings/observations")
	}
	requiredMissing := false
	taskKeys := map[string]bool{}
	for _, t := range r.Tasks {
		if st := domain.TaskStatus(t.Status); !st.Terminal() {
			return invalidf("task %s is not terminal", t.Key)
		}
		taskKeys[t.Key] = true
		if t.RequiredForVerdict && t.Status != "succeeded" {
			requiredMissing = true
		}
	}
	if r.Status == StatusCompleted && (requiredMissing || r.Coverage.AIReview.Status != AIReviewPerformed) {
		return invalidf("completed report with incomplete required work or AI review")
	}
	if r.QualityVerdict != DecideVerdict(r.Status, r.Summary, r.Coverage.AIReview.Status) {
		return invalidf("quality verdict does not follow the rules")
	}
	arts := map[string]bool{}
	for _, a := range r.Artifacts {
		if !domain.ValidID(a.ID) || arts[a.ID] || !hex64.MatchString(a.SHA256) || a.SizeBytes < 0 {
			return invalidf("artifact %s is invalid", a.ID)
		}
		arts[a.ID] = true
	}
	execs := map[string]bool{}
	for _, e := range r.Executions {
		if execs[e.ID] || !taskKeys[e.TaskKey] {
			return invalidf("execution %s is invalid", e.ID)
		}
		execs[e.ID] = true
		if e.Cwd != "." && e.Cwd != "[REDACTED]" && domain.ValidateEvidencePath(e.Cwd) != nil {
			return invalidf("execution cwd must be workspace-relative")
		}
		for i, a := range e.Argv {
			if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "\\") || (len(a) > 2 && a[1] == ':' && (a[2] == '\\' || a[2] == '/')) || (i == 0 && strings.ContainsAny(a, "/\\")) {
				return invalidf("execution argv carries an absolute path")
			}
		}
		for _, id := range []*string{e.StdoutArtifactID, e.StderrArtifactID} {
			if id != nil && !arts[*id] {
				return invalidf("execution stream artifact is unknown")
			}
		}
	}
	obs := map[string]bool{}
	for _, o := range r.Observations {
		if obs[o.ID] || !taskKeys[o.TaskKey] || !arts[o.RawRef.ArtifactID] || o.RawSeverity != nil {
			return invalidf("observation %s is invalid", o.ID)
		}
		if r.Export.IncludeCode == (o.RawMessage == nil) {
			return invalidf("observation message presence does not match the export mode")
		}
		if o.Location != nil && (o.Location.SnapshotID != r.Inputs.SnapshotID || domain.ValidateEvidencePath(o.Location.Path) != nil) {
			return invalidf("observation location is invalid")
		}
		if o.EvidenceState == EvidenceVerified && o.Location == nil {
			return invalidf("verified observation without location")
		}
		obs[o.ID] = true
	}
	findings := map[string]bool{}
	for _, f := range r.Findings {
		if err := validateFinding(f, r.Inputs.SnapshotID, obs, arts); err != nil {
			return err
		}
		if findings[f.ID] {
			return invalidf("duplicate finding %s", f.ID)
		}
		findings[f.ID] = true
	}
	for _, p := range r.Summary.Priorities {
		if !findings[p] {
			return invalidf("unknown priority")
		}
	}
	for _, l := range append(append([]string{}, r.Limitations...), r.Coverage.Notes...) {
		if !codePattern.MatchString(l) {
			return invalidf("limitations and notes must be codes")
		}
	}
	if r.Coverage.AIReview.Status != AIReviewPerformed {
		for _, s := range []string{r.Inputs.Description} {
			if containsForbidden(s) {
				return invalidf("forbidden completion claim")
			}
		}
	}
	return nil
}

func validateFinding(f Finding, snapshot string, obs, arts map[string]bool) error {
	if f.SchemaVersion != "0.1" || f.ID == "" || len([]rune(f.Title)) < 5 || !categories[f.Category] || !severities[f.Severity] || f.SeverityReason == "" ||
		!validations[f.Validation] || !dispositions[f.Disposition] || f.Impact == "" || f.Recommendation.Summary == "" {
		return invalidf("finding %s fields are invalid", f.ID)
	}
	if !validBasis(f) {
		return invalidf("finding %s fingerprint basis is invalid", f.ID)
	}
	if len(f.Conditions) == 0 || len(f.Locations) == 0 || len(f.Claims) == 0 || len(f.Evidence) == 0 || len(f.Verification) == 0 ||
		f.CounterevidenceIDs == nil || f.ObservationIDs == nil || f.Limitations == nil || f.Recommendation.Tradeoffs == nil {
		return invalidf("finding %s is missing required parts", f.ID)
	}
	ev := map[string]string{}
	hasExecution := false
	for _, e := range f.Evidence {
		if e.ID == "" || ev[e.ID] != "" {
			return invalidf("finding %s evidence id is invalid", f.ID)
		}
		ev[e.ID] = e.Kind
		switch e.Kind {
		case "code", "document":
			if e.Location == nil || e.Location.SnapshotID != snapshot || !hex64.MatchString(e.BlobSHA256) || domain.ValidateEvidencePath(e.Location.Path) != nil {
				return invalidf("finding %s code evidence is invalid", f.ID)
			}
		case "tool", "execution":
			if !arts[e.ArtifactID] || e.TaskExecutionID == "" {
				return invalidf("finding %s tool evidence is unbound", f.ID)
			}
			if e.Kind == "execution" {
				if e.ExitStatus == "" {
					return invalidf("execution evidence needs exit_status")
				}
				hasExecution = true
			}
		default:
			return invalidf("finding %s evidence kind is invalid", f.ID)
		}
	}
	if f.Validation == "reproduced" && !hasExecution {
		return invalidf("reproduced finding needs execution evidence")
	}
	for _, c := range f.Claims {
		if c.Assertion == "" || (c.Kind != "observed" && c.Kind != "inferred") || len(c.EvidenceIDs) == 0 {
			return invalidf("finding %s claim is invalid", f.ID)
		}
		for _, id := range c.EvidenceIDs {
			if ev[id] == "" {
				return invalidf("finding %s claim evidence is unknown", f.ID)
			}
		}
	}
	for _, c := range f.Conditions {
		if c.Statement == "" || (c.Status != "verified" && c.Status != "assumed") || (c.Status == "verified" && len(c.EvidenceIDs) == 0) {
			return invalidf("finding %s condition is invalid", f.ID)
		}
		for _, id := range c.EvidenceIDs {
			if ev[id] == "" {
				return invalidf("finding %s condition evidence is unknown", f.ID)
			}
		}
	}
	for _, id := range f.CounterevidenceIDs {
		if ev[id] == "" {
			return invalidf("finding %s counterevidence is unknown", f.ID)
		}
	}
	for _, id := range f.ObservationIDs {
		if !obs[id] {
			return invalidf("finding %s observation is unknown", f.ID)
		}
	}
	for _, l := range f.Locations {
		if l.SnapshotID != snapshot || l.StartLine < 1 || l.EndLine < l.StartLine || domain.ValidateEvidencePath(l.Path) != nil {
			return invalidf("finding %s location is invalid", f.ID)
		}
	}
	return nil
}

func containsForbidden(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range ForbiddenClaims {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// Marshal returns the canonical JSON of a validated report.
func Marshal(r Report) ([]byte, error) {
	if err := ValidateReport(r); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return domain.CanonicalJSON(bytes.TrimSpace(buf.Bytes()))
}

// Unmarshal strictly decodes and validates stored report bytes.
func Unmarshal(b []byte) (Report, error) {
	var r Report
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || !json.Valid(b) {
		return Report{}, invalidf("stored report is not a report document")
	}
	return r, ValidateReport(r)
}

// Digest is the sha256 of the canonical JSON; it is printed in every format.
func Digest(canonical []byte) string { return domain.Digest(canonical) }

// Project returns the export projection. includeCode=true is the stored
// report. includeCode=false removes every raw tool message and replaces all
// free text of findings that is not a fixed builder template, so neither code
// nor tool/model text reaches the code-free export.
func Project(r Report, includeCode bool) Report {
	if includeCode {
		return r
	}
	out := r
	out.Export = Export{IncludeCode: false}
	out.Observations = make([]Observation, len(r.Observations))
	rules := map[string]Observation{}
	for i, o := range r.Observations {
		o.RawMessage = nil
		out.Observations[i] = o
		rules[o.ID] = o
	}
	out.Findings = make([]Finding, len(r.Findings))
	for i, f := range r.Findings {
		g := f
		title := f.Category + " 항목"
		if len(f.ObservationIDs) > 0 {
			o := rules[f.ObservationIDs[0]]
			title = o.Tool + " " + o.RuleID + " 진단"
		}
		if len(f.Locations) > 0 {
			title += " (" + f.Locations[0].Path + ":" + itoa(f.Locations[0].StartLine) + ")"
		}
		g.Title = title
		g.SeverityReason = safeText(f.SeverityReason)
		g.Impact = safeText(f.Impact)
		g.Recommendation = Recommendation{Summary: safeText(f.Recommendation.Summary), Tradeoffs: safeTexts(f.Recommendation.Tradeoffs)}
		g.Verification = safeTexts(f.Verification)
		g.Limitations = safeTexts(f.Limitations)
		g.Conditions = make([]Condition, len(f.Conditions))
		for j, c := range f.Conditions {
			c.Statement = safeText(c.Statement)
			c.EvidenceIDs = append([]string{}, c.EvidenceIDs...)
			g.Conditions[j] = c
		}
		g.Claims = make([]Claim, len(f.Claims))
		for j, c := range f.Claims {
			c.Assertion = textOmitted
			c.EvidenceIDs = append([]string{}, c.EvidenceIDs...)
			g.Claims[j] = c
		}
		g.Evidence = make([]Evidence, len(f.Evidence))
		for j, e := range f.Evidence {
			e.Description = safeText(e.Description)
			if e.Location != nil {
				l := *e.Location
				l.Symbol = ""
				e.Location = &l
			}
			g.Evidence[j] = e
		}
		g.Locations = make([]Location, len(f.Locations))
		for j, l := range f.Locations {
			l.Symbol = "" // symbol names are code identifiers
			g.Locations[j] = l
		}
		// The fingerprint, its basis and the move key are unkeyed digests
		// over code-derived inputs (tool message, symbols, context hash): a
		// reader of the export could confirm guessed code against them. The
		// code-free export therefore carries no identity; comparison and
		// dispositions read it through the project-ACL API.
		g.Fingerprint, g.FingerprintBasis, g.DispositionVersion = "", nil, nil
		out.Findings[i] = g
	}
	return out
}

var safeTemplates = map[string]bool{
	textSeverityReason: true, textSeverityUnmapped: true, textCondition: true, textImpact: true, textRecommendation: true,
	textVerification: true, textLimitManual: true, textLimitStaticOnly: true, textEvidenceTool: true, textEvidenceCode: true, textOmitted: true,
}

func safeText(s string) string {
	if safeTemplates[s] {
		return s
	}
	return textOmitted
}

func safeTexts(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = safeText(s)
	}
	return out
}
