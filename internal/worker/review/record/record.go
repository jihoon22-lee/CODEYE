// Package record defines the structured review artifacts shared by the
// review worker and the report decoders. It performs no I/O and imports no
// process or model code, so a report decoder can validate an uploaded
// artifact against the snapshot manifest alone.
package record

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MediaType = "application/vnd.codeye.review+json"
	// MaxBytes bounds one stored record. Records of whole-file inputs carry
	// more evidence entries and longer prose than the earlier 256 KiB held.
	MaxBytes = 1 << 20

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

	// LimOutputDegraded marks a record whose accepted output had faults of
	// form only, resolved by the platform once no further format repair could
	// arrive (docs/REVIEW.md#출력-오류-분류). Its accepted call keeps those
	// codes in output_errors, and it may be followed by the calls that
	// failed to bring a better output.
	LimOutputDegraded = "review_output_degraded"

	// Items of a role's output left out after the format repair because a
	// fault of form could not be resolved in them (a candidate or hypothesis
	// without a usable cited range, an item whose prose fails its rule).
	// The report shows each as a gap of its section; a left-out candidate or
	// hypothesis keeps the quality verdict from no_actionable_findings.
	LimCandidateOmitted        = "review_candidate_omitted"
	LimInterpretItemsOmitted   = "interpretation_items_omitted"
	LimDocumentItemsOmitted    = "document_items_omitted"
	LimEnvironmentNotesOmitted = "environment_notes_omitted"
)

// Task reasons of a verify task whose accepted record leaves candidates
// unreviewed: the model gave no acceptable verdict for some after the repair
// (ReasonOutputIncomplete), or the request budget could not carry them
// (ReasonInputIncomplete). The task fails; the report still publishes the
// accepted verdicts (docs/DECISIONS.md A59).
const (
	ReasonOutputIncomplete = "review_output_incomplete"
	ReasonInputIncomplete  = "review_input_incomplete"
)

type Excerpt struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Start  int    `json:"start_line"`
	End    int    `json:"end_line"`
	Kind   string `json:"kind"`
	Origin string `json:"origin,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	// DirectivePath is the authored guide that brought a docstring or rustdoc into
	// the public docs. Path still identifies the actual .py blob and lines.
	DirectivePath    string           `json:"directive_path,omitempty"`
	PackageReference PackageReference `json:"package_reference,omitzero"`
	// Text is sent to the model only; records never store source text.
	Text string `json:"text,omitempty"`
	// Within is set in a request only: the ID of another excerpt of the
	// same request whose text holds these lines, so they are not sent twice.
	Within string `json:"within,omitempty"`
}

type CandidateText struct {
	ID           string `json:"id,omitempty"`
	Title        string `json:"title"`
	Condition    string `json:"condition"`
	Impact       string `json:"impact"`
	Improvement  string `json:"improvement"`
	Counterpoint string `json:"counterpoint"`
	// Mechanism is how the cited lines produce the failure (schema 0.7).
	Mechanism string   `json:"mechanism,omitempty"`
	Evidence  []string `json:"evidence_ids"`
}

type Usage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
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

// ExcerptID binds an evidence ID to the snapshot, path, blob and line range.
func ExcerptID(snapshot, path, sha string, start, end int) string {
	id := domain.Digest(fmt.Appendf(nil, "%s\x00%s\x00%s\x00%d:%d", snapshot, path, sha, start, end))
	return "ev_" + id[:24]
}

var sensitive = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|\bsk-[A-Za-z0-9_-]{12,}|\bgh[pousr]_[A-Za-z0-9_]{16,}|authorization\s*[:=]\s*["']?(bearer|basic)\s+[^\s"']{8,}|(?:api[_-]?key|password|client_secret)\s*[:=]\s*["'][A-Za-z0-9+/_.=-]{12,}["'])`)

// Sensitive reports credential-like content that must not reach a model,
// record or report.
func Sensitive(b []byte) bool {
	if len(b) < len("sk-")+12 {
		return false
	}
	// The scanner handles ASCII case pairs. Go's regexp case folding also
	// admits long s and Kelvin sign for ASCII s and k, respectively.
	if bytes.Contains(b, []byte("ſ")) || bytes.Contains(b, []byte("K")) {
		return sensitive.Match(b)
	}
	for i, c := range b {
		switch c {
		case '-':
			if i+5 <= len(b) && string(b[i:i+5]) == "-----" ||
				i >= 2 && b[i-2]|0x20 == 's' && b[i-1]|0x20 == 'k' {
				return sensitive.Match(b)
			}
		case '_':
			if i >= 3 && b[i-3]|0x20 == 'g' && b[i-2]|0x20 == 'h' {
				return sensitive.Match(b)
			}
		case ':', '=':
			if credentialName(b[:i]) {
				return sensitive.Match(b)
			}
		}
	}
	return false
}

// The original regexp still decides whether a candidate is a credential.
// Each recognized assignment form ends in one of these ASCII suffixes.
func credentialName(name []byte) bool {
	name = bytes.TrimRight(name, " \t\n\f\r")
	if len(name) == 0 {
		return false
	}
	switch name[len(name)-1] | 0x20 {
	case 'n':
		return credentialNameSuffix(name, "authorization")
	case 'd':
		return credentialNameSuffix(name, "password")
	case 't':
		return credentialNameSuffix(name, "client_secret")
	case 'y':
		return credentialNameSuffix(name, "apikey") || credentialNameSuffix(name, "api_key") || credentialNameSuffix(name, "api-key")
	}
	return false
}

func credentialNameSuffix(name []byte, suffix string) bool {
	return len(name) >= len(suffix) && bytes.EqualFold(name[len(name)-len(suffix):], []byte(suffix))
}

// SafeText bounds model prose kept in a record.
func SafeText(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 2400 && !Sensitive([]byte(s))
}

var (
	code       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	modelID    = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
	version    = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)
	symbol     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	Categories = []string{"correctness", "error_handling", "concurrency", "resource_lifecycle", "data_integrity", "api_contract", "security", "maintainability", "performance", "test_gap"}
	// Severities a static model verification may assign. critical needs
	// execution evidence or a person's judgment and is never model-assigned.
	Severities = []string{"high", "medium", "low", "info"}
)

// ValidEvidenceSymbol reports whether a source symbol can be stored in evidence.
func ValidEvidenceSymbol(s string) bool { return symbol.MatchString(s) }

// ValidCode reports a fixed reason/limitation code.
func ValidCode(s string) bool { return code.MatchString(s) }

// Expect is what the consumer already trusts: the attempt identity and the
// immutable snapshot manifest. Lookup returns the manifest sha256 of an
// included file entry.
type Expect struct {
	Snapshot, RunID, AttemptID string
	Stage                      string // empty accepts any stage
	// Slice is the review slot key (s001..s999) of a slice record (schema
	// 0.3); empty for every other record, which then must carry no slice.
	Slice  string
	Lookup func(path string) (sha256 string, ok bool)
}

var errInvalid = errors.New("review record invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
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

func texts(list []string, min, max, limit int) bool {
	if list == nil || len(list) < min || len(list) > max {
		return false
	}
	for _, s := range list {
		if !SafeText(s) || len(s) > limit {
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

// CheckVerification enforces the verdict rules with the prose bounds of the
// earlier record versions.
func CheckVerification(v Verification, catalog map[string]Excerpt) error {
	return CheckVerificationWithin(v, catalog, maxListText)
}

// CheckVerificationWithin enforces the verdict rules; listText bounds one
// condition, assumption or next step. Only the platform may set unreviewed;
// reproduced never appears because verification has no execution evidence.
func CheckVerificationWithin(v Verification, catalog map[string]Excerpt, listText int) error {
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
	if !texts(v.ValidConditions, 0, 4, listText) || !texts(v.InvalidConditions, 0, 4, listText) || !texts(v.UnresolvedAssumptions, 0, 4, listText) || !texts(v.NextVerification, 1, 3, listText) {
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
