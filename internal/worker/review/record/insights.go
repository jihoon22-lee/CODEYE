package record

import (
	"regexp"
	"slices"
	"unicode"
)

const (
	MaxDocumentQuality     = 4
	MaxReviewStrengths     = 3
	MaxContractComparisons = 6
	InsightLanguageError   = "insight_prose_requires_korean"
)

type DocumentQuality struct {
	Aspect      string   `json:"aspect"`
	Assessment  string   `json:"assessment"`
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Strength struct {
	Title       string   `json:"title"`
	Benefit     string   `json:"benefit"`
	Scope       string   `json:"scope"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type ContractComparison struct {
	ContractIndex       int      `json:"contract_index"`
	AppliesTo           string   `json:"applies_to"`
	Status              string   `json:"status"`
	Reason              string   `json:"reason"`
	DocumentEvidenceIDs []string `json:"document_evidence_ids"`
	CodeEvidenceIDs     []string `json:"code_evidence_ids"`
}

func ValidQuality(aspect, assessment string) bool {
	return slices.Contains([]string{"clarity", "completeness", "actionability", "consistency"}, aspect) &&
		slices.Contains([]string{"strength", "weakness"}, assessment)
}

func ValidComparison(status string) bool {
	return slices.Contains([]string{"match", "partial", "mismatch", "unknown"}, status)
}

var insightQuote = regexp.MustCompile("`[^`\n]*`|\"[^\"\n]*\"|'[^'\n]*'|“[^”\n]*”|‘[^’\n]*’|「[^」\n]*」|『[^』\n]*』")

// ForeignInsightProse catches clearly non-Korean Han-script narration while
// allowing short names and quoted original text.
func ForeignInsightProse(s string) bool {
	han := 0
	for _, r := range insightQuote.ReplaceAllString(s, "") {
		switch {
		case unicode.Is(unicode.Hangul, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			return false
		case unicode.Is(unicode.Han, r):
			han++
		}
	}
	return han >= 4
}

func insightTextWithin(s string, limit int) bool {
	return ProseText(s, limit) && !ForeignInsightProse(s)
}

func (d DocumentQuality) Valid(catalog map[string]Excerpt) bool {
	return d.ValidWithin(catalog, maxText)
}

// ValidWithin is Valid with prose bounded to text bytes.
func (d DocumentQuality) ValidWithin(catalog map[string]Excerpt, text int) bool {
	return ValidQuality(d.Aspect, d.Assessment) && insightTextWithin(d.Statement, text) && refs(d.EvidenceIDs, catalog, 4, "document")
}

func (s Strength) Valid(catalog map[string]Excerpt) bool { return s.ValidWithin(catalog, maxText) }

// ValidWithin is Valid with prose bounded to text bytes.
func (s Strength) ValidWithin(catalog map[string]Excerpt, text int) bool {
	if !insightTextWithin(s.Title, text) || !insightTextWithin(s.Benefit, text) || !ValidScopePath(s.Scope) ||
		!refs(s.EvidenceIDs, catalog, 4, "code", "test") || !hasKind(s.EvidenceIDs, catalog, "code") {
		return false
	}
	return scopedCode(s.EvidenceIDs, s.Scope, catalog)
}

func (c ContractComparison) Valid(catalog map[string]Excerpt) bool {
	return c.ValidWithin(catalog, maxText)
}

// ValidWithin is Valid with the reason bounded to text bytes.
func (c ContractComparison) ValidWithin(catalog map[string]Excerpt, text int) bool {
	return len(c.Problems(catalog, text)) == 0
}

// Fixed rejection codes of a contract comparison, one per condition
// (docs/REVIEW.md#문서-계약-대조). The worker adds the ones that need its
// input (CompareIndexDuplicate, CompareDocumentsChanged, CompareScope*
// against the contract and the snapshot, CompareCodeOutsideAPI,
// CompareInSlice and the citation codes).
const (
	CompareIndexInvalid       = "contract_comparison_index_invalid"
	CompareIndexDuplicate     = "contract_comparison_index_duplicate"
	CompareScopeInvalid       = "contract_comparison_scope_invalid"
	CompareScopeOutside       = "contract_comparison_scope_outside_contract"
	CompareStatusInvalid      = "contract_comparison_status_invalid"
	CompareReasonInvalid      = "contract_comparison_reason_invalid"
	CompareDocumentsChanged   = "contract_comparison_documents_changed"
	CompareCodeMissing        = "contract_comparison_code_missing"
	CompareCodeInvalid        = "contract_comparison_code_invalid"
	CompareCodeDuplicate      = "contract_comparison_code_duplicate"
	CompareCodeTooMany        = "contract_comparison_code_too_many"
	CompareCodeOutsideScope   = "contract_comparison_code_outside_scope"
	CompareCodeIsDocument     = "contract_comparison_code_is_document"
	CompareCodeOutsideAPI     = "contract_comparison_code_outside_documented_api"
	CompareCodeRangeInvalid   = "contract_comparison_code_range_invalid"
	CompareCodeRangeMissing   = "contract_comparison_code_range_missing"
	CompareInSlice            = "contract_comparison_in_slice"
	CompareTooMany            = "contract_comparisons_too_many"
	MaxComparisonCodeEvidence = 4
	// ErrExecutionClaim is the output error of prose claiming to have run
	// or reproduced something (every role uses it).
	ErrExecutionClaim = "execution_claim"
	// LimContractComparisonOmitted: comparisons still invalid after the
	// format repair were left out; their contracts stay unchecked.
	LimContractComparisonOmitted = "contract_comparison_omitted"
)

// FieldProblem is one rejection of an output item: the field it concerns
// and its fixed code.
type FieldProblem struct {
	Field string `json:"field"`
	Code  string `json:"error"`
}

// Problems lists every rule of the comparison the catalog alone decides,
// with the field each concerns; none means valid. An implementation must
// not be the cited in-source document (a doc comment, docstring or rustdoc
// of the same file): code evidence overlapping it does not count.
func (c ContractComparison) Problems(catalog map[string]Excerpt, text int) []FieldProblem {
	var out []FieldProblem
	add := func(field, code string) {
		if !slices.Contains(out, FieldProblem{field, code}) {
			out = append(out, FieldProblem{field, code})
		}
	}
	if c.ContractIndex < 1 || c.ContractIndex > MaxContractComparisons {
		add("contract_index", CompareIndexInvalid)
	}
	if !ValidScopePath(c.AppliesTo) {
		add("applies_to", CompareScopeInvalid)
	}
	switch {
	case ForeignInsightProse(c.Reason):
		add("reason", InsightLanguageError)
	case ClaimsExecution(c.Reason):
		add("reason", ErrExecutionClaim)
	case !ProseText(c.Reason, text):
		add("reason", CompareReasonInvalid)
	}
	if !ValidComparison(c.Status) {
		add("status", CompareStatusInvalid)
	}
	if !refs(c.DocumentEvidenceIDs, catalog, 4, "document") {
		add("document_evidence_ids", CompareDocumentsChanged)
	} else if ValidScopePath(c.AppliesTo) && !documentsSupportScope(c.DocumentEvidenceIDs, catalog, c.AppliesTo, "", true) {
		add("applies_to", CompareScopeOutside)
	}
	if len(c.CodeEvidenceIDs) == 0 {
		if c.Status != "unknown" || c.CodeEvidenceIDs == nil {
			add("code_evidence_ids", CompareCodeMissing)
		}
		return out
	}
	seen := map[string]bool{}
	for _, id := range c.CodeEvidenceIDs {
		e, ok := catalog[id]
		switch {
		case seen[id]:
			add("code_evidence_ids", CompareCodeDuplicate)
		case !ok || (e.Kind != "code" && e.Kind != "test"):
			add("code_evidence_ids", CompareCodeInvalid)
		case !ScopeUnder(e.Path, c.AppliesTo):
			add("code_evidence_ids", CompareCodeOutsideScope)
		}
		seen[id] = true
	}
	if len(c.CodeEvidenceIDs) > MaxComparisonCodeEvidence {
		add("code_evidence_ids", CompareCodeTooMany)
	}
	if c.Status == "unknown" || len(out) > 0 {
		return out
	}
	if !slices.ContainsFunc(c.CodeEvidenceIDs, func(id string) bool {
		code := catalog[id]
		return code.Kind == "code" && !slices.ContainsFunc(c.DocumentEvidenceIDs, func(docID string) bool {
			doc := catalog[docID]
			return (doc.DirectivePath != "" || doc.IsJavaAPIDocument()) && doc.Path == code.Path && doc.Start <= code.End && code.Start <= doc.End
		})
	}) {
		code := slices.ContainsFunc(c.CodeEvidenceIDs, func(id string) bool { return catalog[id].Kind == "code" })
		if code {
			add("code_evidence_ids", CompareCodeIsDocument)
		} else {
			add("code_evidence_ids", CompareCodeMissing)
		}
	}
	return out
}

func scopedCode(ids []string, scope string, catalog map[string]Excerpt) bool {
	for _, id := range ids {
		if !ScopeUnder(catalog[id].Path, scope) {
			return false
		}
	}
	return true
}

func (c ContractComparison) MatchesContract(contracts []Contract) bool {
	if c.ContractIndex < 1 || c.ContractIndex > len(contracts) {
		return false
	}
	contract := contracts[c.ContractIndex-1]
	return ScopeUnder(c.AppliesTo, contract.AppliesTo) && slices.Equal(c.DocumentEvidenceIDs, contract.EvidenceIDs)
}

// CitedEvidence returns only references used by a role's model output.
func (r Record) CitedEvidence() []Excerpt {
	ids := map[string]bool{}
	add := func(list []string) {
		for _, id := range list {
			ids[id] = true
		}
	}
	for _, n := range r.EnvironmentNotes {
		add(n.EvidenceIDs)
	}
	for _, v := range r.Contracts {
		add(v.EvidenceIDs)
	}
	for _, v := range r.Conflicts {
		add(v.FirstEvidence)
		add(v.SecondEvidence)
	}
	for _, v := range r.Questions {
		add(v.EvidenceIDs)
	}
	for _, v := range r.InstructionNotes {
		add(v.EvidenceIDs)
	}
	for _, v := range r.DocumentQuality {
		add(v.EvidenceIDs)
	}
	for _, v := range r.Strengths {
		add(v.EvidenceIDs)
	}
	for _, v := range r.ContractComparisons {
		add(v.DocumentEvidenceIDs)
		add(v.CodeEvidenceIDs)
	}
	for _, c := range r.Candidates {
		add(c.Evidence)
	}
	for _, v := range r.Verifications {
		add(v.SupportingEvidence)
		add(v.Counterevidence)
	}
	var out []Excerpt
	for _, e := range r.Evidence {
		if ids[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

func checkInsights(r Record, catalog map[string]Excerpt, b bounds) error {
	quality, strengths, comparisons := r.DocumentQuality, r.Strengths, r.ContractComparisons
	if len(quality) > MaxDocumentQuality || len(strengths) > MaxReviewStrengths || len(comparisons) > MaxContractComparisons ||
		(len(quality) > 0 && (!r.Accepted() || r.Stage != StageDocuments)) ||
		(len(strengths) > 0 && (!r.Accepted() || r.Stage != StageInvestigate)) ||
		(len(comparisons) > 0 && (!r.Accepted() || (r.Stage != StageInvestigate && r.Stage != StageContract))) ||
		(r.Stage == StageContract && r.Accepted() && len(comparisons) != 1) ||
		(len(comparisons) > 0 && r.Slice != nil) {
		return invalid("insight content")
	}
	for _, x := range quality {
		if !x.ValidWithin(catalog, b.text) {
			return invalid("document quality")
		}
	}
	for _, x := range strengths {
		if !x.ValidWithin(catalog, b.text) {
			return invalid("strength")
		}
	}
	seen := map[int]bool{}
	for _, x := range comparisons {
		if seen[x.ContractIndex] || !x.ValidWithin(catalog, b.text) {
			return invalid("contract comparison")
		}
		seen[x.ContractIndex] = true
	}
	if r.Stage == StageContract && r.Accepted() {
		if !validContractPartial(comparisons[0], r.PartialSupport, catalog, b.text) {
			return invalid("contract partial support")
		}
	} else if r.PartialSupport != nil {
		return invalid("unexpected contract partial support")
	}
	return checkContractProbe(r)
}
