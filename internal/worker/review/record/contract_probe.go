package record

import (
	"regexp"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MaxContractProbeCandidates = 3
	MaxContractProbeLiterals   = 3
	MaxContractLocationLines   = 16
	LimNavigationNoChoice      = "navigation_no_choice"
	LimNavigationChoiceInvalid = "navigation_choice_invalid"
)

// PublicLimitations omits raw navigation telemetry from task and report gaps.
func (r Record) PublicLimitations() []string {
	private := func(code string) bool {
		return code == LimNavigationNoChoice || code == LimNavigationChoiceInvalid || code == LimWorkflowSelectionInvalid
	}
	if !slices.ContainsFunc(r.Limitations, private) {
		return r.Limitations
	}
	return slices.DeleteFunc(slices.Clone(r.Limitations), private)
}

// ContractProbe is an optional selection of navigation locations for one
// unresolved comparison. Locations carry no source excerpt or evidence claim.
type ContractProbe struct {
	ContractIndex int                `json:"contract_index"`
	Candidates    []ContractLocation `json:"candidates"`
	Literals      []string           `json:"literals,omitempty"`
}

type ContractLocation struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Start  int    `json:"start_line"`
	End    int    `json:"end_line"`
	Kind   string `json:"kind"`
	Symbol string `json:"symbol,omitempty"`
}

var contractLiteral = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,47}$`)

func ValidContractLiteral(s string) bool {
	return contractLiteral.MatchString(s) && !Sensitive([]byte(s))
}

// ContractLocationID uses its own namespace so a location cannot be cited
// as an excerpt, even when its range was also sent elsewhere as evidence.
func ContractLocationID(snapshot, path, sha string, start, end int) string {
	return "loc_" + ExcerptID(snapshot, path, sha, start, end)[3:]
}

func (c ContractLocation) Valid(snapshot, scope string) bool {
	return domain.ValidateEvidencePath(c.Path) == nil && !Sensitive([]byte(c.Path)) &&
		ScopeUnder(c.Path, scope) && c.Kind != "" && SourceKind(c.Path) == c.Kind &&
		hex64.MatchString(c.SHA256) && c.Start > 0 && c.End >= c.Start && c.End-c.Start < MaxContractLocationLines &&
		c.ID == ContractLocationID(snapshot, c.Path, c.SHA256, c.Start, c.End) &&
		(c.Symbol == "" || ValidContractLiteral(c.Symbol))
}

func (p ContractProbe) Valid(snapshot string, comparison ContractComparison) bool {
	if p.ContractIndex != comparison.ContractIndex || p.ContractIndex < 1 || p.ContractIndex > MaxContractComparisons ||
		comparison.Status != "unknown" || !ValidScopePath(comparison.AppliesTo) ||
		len(p.Candidates) == 0 || len(p.Candidates) > MaxContractProbeCandidates || len(p.Literals) > MaxContractProbeLiterals {
		return false
	}
	for i, c := range p.Candidates {
		if !c.Valid(snapshot, comparison.AppliesTo) || slices.ContainsFunc(p.Candidates[:i], func(prev ContractLocation) bool { return prev.ID == c.ID }) {
			return false
		}
	}
	for i, literal := range p.Literals {
		if !ValidContractLiteral(literal) || slices.Contains(p.Literals[:i], literal) {
			return false
		}
	}
	return true
}

func checkContractProbe(r Record) error {
	if r.ContractProbe == nil {
		return nil
	}
	if r.Stage == StageInvestigate && r.Accepted() && r.Slice == nil &&
		(r.Schema == SchemaVersionContract || r.Schema == SchemaVersionWorkflowReading || r.Schema == SchemaVersionCitedLines || r.Schema == SchemaVersionLeads) {
		for _, comparison := range r.ContractComparisons {
			if r.ContractProbe.Valid(r.Snapshot, comparison) {
				return nil
			}
		}
	}
	return invalid("contract probe")
}
