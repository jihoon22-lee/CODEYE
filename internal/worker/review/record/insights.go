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

func insightText(s string) bool { return ModelText(s) && !ForeignInsightProse(s) }

func (d DocumentQuality) Valid(catalog map[string]Excerpt) bool {
	return ValidQuality(d.Aspect, d.Assessment) && insightText(d.Statement) && refs(d.EvidenceIDs, catalog, 4, "document")
}

func (s Strength) Valid(catalog map[string]Excerpt) bool {
	if !insightText(s.Title) || !insightText(s.Benefit) || !ValidScopePath(s.Scope) ||
		!refs(s.EvidenceIDs, catalog, 4, "code", "test") || !hasKind(s.EvidenceIDs, catalog, "code") {
		return false
	}
	return scopedCode(s.EvidenceIDs, s.Scope, catalog)
}

func (c ContractComparison) Valid(catalog map[string]Excerpt) bool {
	if c.ContractIndex < 1 || c.ContractIndex > MaxContractComparisons || !ValidScopePath(c.AppliesTo) ||
		!insightText(c.Reason) || !ValidComparison(c.Status) ||
		!refs(c.DocumentEvidenceIDs, catalog, 4, "document") || !docsCover(c.DocumentEvidenceIDs, catalog, c.AppliesTo) {
		return false
	}
	if len(c.CodeEvidenceIDs) == 0 {
		return c.Status == "unknown" && c.CodeEvidenceIDs != nil
	}
	return refs(c.CodeEvidenceIDs, catalog, 4, "code", "test") && scopedCode(c.CodeEvidenceIDs, c.AppliesTo, catalog) &&
		(c.Status == "unknown" || hasKind(c.CodeEvidenceIDs, catalog, "code"))
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

func checkInsights(r Record, catalog map[string]Excerpt) error {
	quality, strengths, comparisons := r.DocumentQuality, r.Strengths, r.ContractComparisons
	if len(quality) > MaxDocumentQuality || len(strengths) > MaxReviewStrengths || len(comparisons) > MaxContractComparisons ||
		(len(quality) > 0 && (!r.Accepted() || r.Stage != StageDocuments)) ||
		(len(strengths)+len(comparisons) > 0 && (!r.Accepted() || r.Stage != StageInvestigate)) ||
		(len(comparisons) > 0 && r.Slice != nil) {
		return invalid("insight content")
	}
	for _, x := range quality {
		if !x.Valid(catalog) {
			return invalid("document quality")
		}
	}
	for _, x := range strengths {
		if !x.Valid(catalog) {
			return invalid("strength")
		}
	}
	seen := map[int]bool{}
	for _, x := range comparisons {
		if seen[x.ContractIndex] || !x.Valid(catalog) {
			return invalid("contract comparison")
		}
		seen[x.ContractIndex] = true
	}
	return nil
}
