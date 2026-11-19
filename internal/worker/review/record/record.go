// Package record defines the structured review artifact v0.1 shared by the
// review worker and the report decoders. It performs no I/O
// and imports no process or model code, so a report decoder can validate an
// uploaded artifact against the snapshot manifest alone.
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MediaType     = "application/vnd.codeye.review+json"
	SchemaVersion = "0.1"
	MaxBytes      = 256 << 10

	OutcomeAccepted = "accepted"
	OutcomeFailed   = "failed"

	VerdictSupported    = "supported"
	VerdictRefuted      = "refuted"
	VerdictInconclusive = "inconclusive"
	// VerdictUnreviewed is set only by the platform: no valid model
	// verification exists for the candidate.
	VerdictUnreviewed = "unreviewed"

	// LimReproductionRejected marks a model reproduction claim the platform
	// downgraded to inconclusive: verification runs nothing, so it has no
	// execution evidence.
	LimReproductionRejected = "reproduction_claim_rejected"
	LimQuarantined          = "verification_quarantined"
	LimNotCompleted         = "verification_not_completed"
)

type Excerpt struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Start  int    `json:"start_line"`
	End    int    `json:"end_line"`
	Kind   string `json:"kind"`
	Origin string `json:"origin,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	// Text is sent to the model only; records never store source text.
	Text string `json:"text,omitempty"`
}

type Fact struct {
	Kind      string   `json:"kind"`
	Statement string   `json:"statement"`
	Evidence  []string `json:"evidence_ids"`
}

type Candidate struct {
	ID           string   `json:"id,omitempty"`
	Title        string   `json:"title"`
	Condition    string   `json:"condition"`
	Impact       string   `json:"impact"`
	Improvement  string   `json:"improvement"`
	Counterpoint string   `json:"counterpoint"`
	Evidence     []string `json:"evidence_ids"`
}

type Usage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

// Call is the provenance of one model request. It never holds the prompt,
// raw output, private reasoning or credentials. Attempt/Outcome are absent in
// older records.
type Call struct {
	Role          string   `json:"role"`
	Model         string   `json:"model"`
	Version       string   `json:"opencode_version"`
	PackDigest    string   `json:"pack_sha256"`
	PackFiles     []string `json:"pack_files"`
	RequestDigest string   `json:"request_sha256"`
	Usage         *Usage   `json:"usage"`
	Attempt       int      `json:"attempt,omitempty"`
	Outcome       string   `json:"outcome,omitempty"`
}

// Verification is the platform-validated verdict for one candidate.
type Verification struct {
	CandidateID           string   `json:"candidate_id"`
	Verdict               string   `json:"verdict"`
	Category              string   `json:"category"`
	Severity              string   `json:"severity"`
	SeverityReason        string   `json:"severity_reason"`
	Impact                string   `json:"impact"`
	ValidConditions       []string `json:"valid_conditions"`
	InvalidConditions     []string `json:"invalid_conditions"`
	SupportingEvidence    []string `json:"supporting_evidence_ids"`
	Counterevidence       []string `json:"counterevidence_ids"`
	UnresolvedAssumptions []string `json:"unresolved_assumptions"`
	NextVerification      []string `json:"next_verification"`
	Limitations           []string `json:"limitations"`
}

type Record struct {
	Schema        string         `json:"schema_version"`
	Snapshot      string         `json:"snapshot_manifest_digest"`
	RunID         string         `json:"run_id"`
	AttemptID     string         `json:"attempt_id"`
	Stage         string         `json:"stage"`
	Outcome       string         `json:"outcome,omitempty"`
	FailureReason string         `json:"failure_reason,omitempty"`
	Evidence      []Excerpt      `json:"evidence"`
	Facts         []Fact         `json:"facts"`
	Candidates    []Candidate    `json:"candidates"`
	Verifications []Verification `json:"verifications,omitempty"`
	Limitations   []string       `json:"limitations"`
	Calls         []Call         `json:"calls"`
}

// Accepted reports whether the record carries an accepted model result.
// Older records have no outcome and were only written on success.
func (r Record) Accepted() bool {
	return r.Outcome == OutcomeAccepted || (r.Outcome == "" && r.Stage != "verify")
}

// ExcerptID binds an evidence ID to the snapshot, path, blob and line range.
func ExcerptID(snapshot, path, sha string, start, end int) string {
	id := domain.Digest(fmt.Appendf(nil, "%s\x00%s\x00%s\x00%d:%d", snapshot, path, sha, start, end))
	return "ev_" + id[:24]
}

var sensitive = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|\bsk-[A-Za-z0-9_-]{12,}|\bgh[pousr]_[A-Za-z0-9_]{16,}|authorization\s*[:=]\s*["']?(bearer|basic)\s+[^\s"']{8,}|(?:api[_-]?key|password|client_secret)\s*[:=]\s*["'][A-Za-z0-9+/_.=-]{12,}["'])`)

// Sensitive reports credential-like content that must not reach a model,
// record or report.
func Sensitive(b []byte) bool { return sensitive.Match(b) }

// SafeText bounds model prose kept in a record.
func SafeText(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 2400 && !sensitive.MatchString(s)
}

var (
	code       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	modelID    = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
	version    = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)
	symbol     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	stages     = map[string]int{"context": 0, "investigate": 1, "verify": 2}
	kinds      = map[string]bool{"code": true, "test": true, "document": true, "config": true}
	origins    = map[string]bool{"": true, "context": true, "caller": true, "test": true}
	Categories = []string{"correctness", "error_handling", "concurrency", "resource_lifecycle", "data_integrity", "api_contract", "security", "maintainability", "performance", "test_gap"}
	// Severities a static model verification may assign. critical needs
	// execution evidence or a person's judgment and is never model-assigned.
	Severities = []string{"high", "medium", "low", "info"}
)

// ValidCode reports a fixed reason/limitation code.
func ValidCode(s string) bool { return code.MatchString(s) }

// Expect is what the consumer already trusts: the attempt identity and the
// immutable snapshot manifest. Lookup returns the manifest sha256 of an
// included file entry.
type Expect struct {
	Snapshot, RunID, AttemptID string
	Stage                      string // empty accepts any stage
	// Slice is the review slice key (s001..s032) of a slice record (schema
	// 0.3); empty for every other record, which then must carry no slice.
	Slice  string
	Lookup func(path string) (sha256 string, ok bool)
}

var errInvalid = errors.New("review record invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// Decode strictly parses and validates a review artifact. It rechecks every
// evidence ID against its snapshot/path/blob/range binding and the manifest,
// and every reference against the record's own evidence catalog. Line ranges
// against file length are checked by the worker before writing.
func Decode(body []byte, want Expect) (Record, error) {
	var r Record
	if len(body) > MaxBytes {
		return Record{}, invalid("record too large")
	}
	if err := domain.DecodeStrict(body, &r); err != nil {
		return Record{}, invalid("not strict JSON: %v", err)
	}
	if err := Validate(r, want); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Validate applies Decode's rules to an in-memory record.
func Validate(r Record, want Expect) error {
	stage, ok := stages[r.Stage]
	switch {
	case r.Schema != SchemaVersion:
		return invalid("schema_version")
	case !ok || (want.Stage != "" && want.Stage != r.Stage):
		return invalid("stage")
	case !hex64.MatchString(r.Snapshot) || !domain.ValidID(r.RunID) || !domain.ValidID(r.AttemptID):
		return invalid("snapshot or attempt identifiers")
	case want.Lookup == nil || r.Snapshot != want.Snapshot || r.RunID != want.RunID || r.AttemptID != want.AttemptID:
		return invalid("record belongs to another snapshot or attempt")
	case r.Evidence == nil || r.Facts == nil || r.Candidates == nil || r.Limitations == nil || r.Calls == nil:
		return invalid("missing list")
	}
	switch r.Outcome {
	case OutcomeAccepted:
		if r.FailureReason != "" {
			return invalid("accepted record with failure_reason")
		}
	case OutcomeFailed:
		if !ValidCode(r.FailureReason) {
			return invalid("failed record without failure_reason")
		}
	case "":
		if r.Stage == "verify" || r.FailureReason != "" {
			return invalid("verify record without outcome")
		}
	default:
		return invalid("outcome")
	}
	if len(r.Evidence) == 0 || len(r.Evidence) > 12 || len(r.Facts) > 4 || (len(r.Facts) == 0 && (stage > 0 || r.Accepted())) {
		return invalid("review corpus size")
	}
	catalog := map[string]Excerpt{}
	for _, e := range r.Evidence {
		if err := checkExcerpt(e, r.Snapshot, want.Lookup); err != nil {
			return err
		}
		if _, dup := catalog[e.ID]; dup {
			return invalid("duplicate evidence id")
		}
		catalog[e.ID] = e
	}
	for _, f := range r.Facts {
		kind := ""
		switch f.Kind {
		case "documented":
			kind = "document"
		case "observed", "inferred":
		default:
			return invalid("fact kind")
		}
		if !SafeText(f.Statement) || !RefsOK(f.Evidence, catalog, kind, 6) {
			return invalid("fact")
		}
	}
	if stage == 0 && len(r.Candidates) > 0 {
		return invalid("context record with candidates")
	}
	for i, c := range r.Candidates {
		if c.ID != fmt.Sprintf("candidate_%d", i+1) || !SafeText(c.Title) || !SafeText(c.Condition) || !SafeText(c.Impact) || !SafeText(c.Improvement) || !SafeText(c.Counterpoint) || !RefsOK(c.Evidence, catalog, "code", 6) {
			return invalid("candidate %d", i+1)
		}
	}
	if len(r.Candidates) > 4 {
		return invalid("too many candidates")
	}
	if stage != 2 {
		if r.Verifications != nil {
			return invalid("verifications outside verify stage")
		}
	} else {
		if len(r.Verifications) != len(r.Candidates) {
			return invalid("verification coverage")
		}
		reviewed := 0
		for i, v := range r.Verifications {
			if v.CandidateID != r.Candidates[i].ID {
				return invalid("verification order")
			}
			if err := CheckVerification(v, catalog); err != nil {
				return err
			}
			if v.Verdict != VerdictUnreviewed {
				reviewed++
			}
		}
		if (r.Outcome == OutcomeFailed && reviewed != 0) || (r.Accepted() && len(r.Candidates) > 0 && reviewed == 0) {
			return invalid("verification outcomes do not match stage")
		}
	}
	if !codes(r.Limitations) {
		return invalid("limitations")
	}
	return checkCalls(r, stage)
}

func checkExcerpt(e Excerpt, snapshot string, lookup func(string) (string, bool)) error {
	switch {
	case domain.ValidateEvidencePath(e.Path) != nil || !hex64.MatchString(e.SHA256) || e.Start < 1 || e.End < e.Start:
		return invalid("evidence location")
	// Kinds follow the path rules of source.go: code is source of a review
	// ecosystem (a Go test file is also acceptable as code),
	// test a test path and config a build manifest.
	case e.End-e.Start+1 > 60 || (e.Kind == "code" && SourceEcosystem(e.Path) == "") ||
		(e.Kind == "test" && SourceKind(e.Path) != "test") ||
		(e.Kind == "document" && !slices.Contains([]string{"readme.md", "agents.md"}, strings.ToLower(path.Base(e.Path)))) ||
		(e.Kind == "config" && ManifestEcosystem(e.Path) == ""):
		return invalid("evidence kind or range")
	case ((e.Origin == "caller" || e.Origin == "test") && (e.Symbol == "" || (e.Origin == "test") != (e.Kind == "test") || (e.Kind != "code" && e.Kind != "test"))) ||
		((e.Origin == "" || e.Origin == "context") && (e.Start != 1 || e.End > 50 || e.Symbol != "")):
		return invalid("evidence origin")
	case !kinds[e.Kind] || !origins[e.Origin] || (e.Symbol != "" && !symbol.MatchString(e.Symbol)) || e.Text != "":
		return invalid("evidence attributes")
	case e.ID != ExcerptID(snapshot, e.Path, e.SHA256, e.Start, e.End):
		return invalid("evidence id is not bound to its location")
	}
	if sha, ok := lookup(e.Path); !ok || sha != e.SHA256 {
		return invalid("evidence path or blob not in snapshot")
	}
	return nil
}

// RefsOK checks a citation list: 1..max unique IDs from the catalog, at
// least one of the wanted kind (code accepts test; document accepts config).
func RefsOK(ids []string, catalog map[string]Excerpt, kind string, max int) bool {
	if len(ids) == 0 || len(ids) > max {
		return false
	}
	return idsOK(ids, catalog, max) && hasKind(ids, catalog, kind)
}

func idsOK(ids []string, catalog map[string]Excerpt, max int) bool {
	if len(ids) > max {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := catalog[id]; !ok || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func hasKind(ids []string, catalog map[string]Excerpt, kind string) bool {
	if kind == "" {
		return true
	}
	for _, id := range ids {
		k := catalog[id].Kind
		if k == kind || (kind == "code" && k == "test") || (kind == "document" && k == "config") {
			return true
		}
	}
	return false
}

func texts(list []string, min, max int) bool {
	if list == nil || len(list) < min || len(list) > max {
		return false
	}
	for _, s := range list {
		if !SafeText(s) || len(s) > 600 {
			return false
		}
	}
	return true
}

func codes(list []string) bool {
	if list == nil {
		return false
	}
	for i, s := range list {
		if !ValidCode(s) || slices.Contains(list[:i], s) {
			return false
		}
	}
	return true
}

// CheckVerification enforces the verdict rules. Only the platform may set
// unreviewed; reproduced never appears because verification has no execution evidence.
func CheckVerification(v Verification, catalog map[string]Excerpt) error {
	lists := [][]string{v.ValidConditions, v.InvalidConditions, v.SupportingEvidence, v.Counterevidence, v.UnresolvedAssumptions, v.NextVerification}
	for _, l := range lists {
		if l == nil {
			return invalid("verification missing list")
		}
	}
	if !codes(v.Limitations) {
		return invalid("verification limitations")
	}
	if v.Verdict == VerdictUnreviewed {
		empty := v.Category == "" && v.Severity == "" && v.SeverityReason == "" && v.Impact == ""
		for _, l := range lists {
			empty = empty && len(l) == 0
		}
		if !empty || len(v.Limitations) == 0 {
			return invalid("unreviewed verification carries model content")
		}
		return nil
	}
	if !slices.Contains(Categories, v.Category) || !slices.Contains(Severities, v.Severity) || !SafeText(v.SeverityReason) || !SafeText(v.Impact) {
		return invalid("verification attributes")
	}
	if !texts(v.ValidConditions, 0, 4) || !texts(v.InvalidConditions, 0, 4) || !texts(v.UnresolvedAssumptions, 0, 4) || !texts(v.NextVerification, 1, 3) {
		return invalid("verification text")
	}
	if !idsOK(v.SupportingEvidence, catalog, 8) || !idsOK(v.Counterevidence, catalog, 8) {
		return invalid("verification evidence reference")
	}
	switch v.Verdict {
	case VerdictSupported:
		if len(v.SupportingEvidence) == 0 || !hasKind(v.SupportingEvidence, catalog, "code") || len(v.ValidConditions) == 0 {
			return invalid("supported without code evidence or condition")
		}
	case VerdictRefuted:
		if !RefsOK(v.Counterevidence, catalog, "code", 8) || len(v.InvalidConditions) == 0 {
			return invalid("refuted without counterevidence")
		}
	case VerdictInconclusive:
		if len(v.UnresolvedAssumptions) == 0 && !slices.Contains(v.Limitations, LimReproductionRejected) {
			return invalid("inconclusive without unresolved assumption")
		}
	default:
		return invalid("verdict")
	}
	if slices.Contains(v.Limitations, LimReproductionRejected) && v.Verdict != VerdictInconclusive {
		return invalid("rejected reproduction claim must stay inconclusive")
	}
	return nil
}

func checkCalls(r Record, stage int) error {
	seen := [3]int{}
	accepted := [3]bool{}
	last := 0
	for _, c := range r.Calls {
		role, ok := stages[c.Role]
		if !ok || role < last || role > stage || !hex64.MatchString(c.PackDigest) || len(c.PackFiles) == 0 || len(c.PackFiles) > 24 ||
			(c.Usage != nil && (c.Usage.Input < 0 || c.Usage.Output < 0)) {
			return invalid("call provenance")
		}
		last = role
		for i, f := range c.PackFiles {
			if domain.ValidateEvidencePath(f) != nil || Sensitive([]byte(f)) || slices.Contains(c.PackFiles[:i], f) {
				return invalid("pack file")
			}
		}
		seen[role]++
		legacy := role < 2 && c.Attempt == 0 && c.Outcome == ""
		okCall := c.Outcome == OutcomeAccepted || legacy
		if !legacy && (c.Attempt != seen[role] || !ValidCode(c.Outcome)) {
			return invalid("call sequence")
		}
		if (role < 2 && seen[role] > 1) || seen[role] > 2 {
			return invalid("too many calls")
		}
		if (okCall || c.Model != "") && (len(c.Model) > 160 || !modelID.MatchString(c.Model) || Sensitive([]byte(c.Model))) {
			return invalid("model identity")
		}
		if (okCall || c.Version != "") && !version.MatchString(c.Version) {
			return invalid("model client version")
		}
		// A local preflight refusal can occur before the model client composes a request.
		if (okCall || c.RequestDigest != "" || c.Usage != nil) && !hex64.MatchString(c.RequestDigest) {
			return invalid("request digest")
		}
		accepted[role] = accepted[role] || okCall
	}
	for _, ok := range accepted[:stage] {
		if !ok {
			return invalid("missing accepted prerequisite call")
		}
	}
	if stage == 2 && len(r.Candidates) == 0 && seen[stage] != 0 {
		return invalid("empty verify with a call")
	}
	need := r.Accepted() && !(stage == 2 && len(r.Candidates) == 0)
	if need != accepted[stage] {
		return invalid("call outcome does not match stage")
	}
	return nil
}

// MarshalRecord validates before encoding, so the worker never writes a
// record the decoder would reject.
func MarshalRecord(r Record, want Expect) ([]byte, error) {
	if err := Validate(r, want); err != nil {
		return nil, err
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > MaxBytes {
		return nil, invalid("record encoding")
	}
	return b, nil
}
