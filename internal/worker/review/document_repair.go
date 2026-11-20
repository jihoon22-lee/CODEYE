package review

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type documentScopeIssue struct {
	Collection  string                `json:"collection"`
	Index       int                   `json:"index"`
	EvidenceIDs []string              `json:"evidence_ids"`
	ValidScopes []documentScopeOption `json:"valid_scopes"`
}

type documentScopeOption struct {
	Scope    string `json:"scope"`
	Callable string `json:"callable,omitempty"`
}

type documentScopeInputs struct {
	input   map[string]docInput
	catalog map[string]record.Excerpt
}

func (v documentScopeInputs) outside(scope string, ids []string) []record.Excerpt {
	var outside []record.Excerpt
	for _, id := range ids {
		if d, ok := v.input[id]; ok && !record.ScopeUnder(scope, d.AppliesTo) {
			outside = append(outside, v.catalog[id])
		}
	}
	return outside
}

func (v documentScopeInputs) issue(collection string, index int, ids []string, packageAllowed bool) documentScopeIssue {
	out := documentScopeIssue{Collection: collection, Index: index + 1, EvidenceIDs: []string{}, ValidScopes: []documentScopeOption{}}
	physical := "."
	for _, id := range ids {
		d, ok := v.input[id]
		if !ok {
			return out
		}
		if !slices.Contains(out.EvidenceIDs, d.EvidenceID) {
			out.EvidenceIDs = append(out.EvidenceIDs, d.EvidenceID)
		}
		if physical == "" {
			continue
		}
		if record.ScopeUnder(d.AppliesTo, physical) {
			physical = d.AppliesTo
		} else if !record.ScopeUnder(physical, d.AppliesTo) {
			physical = ""
		}
	}
	if physical != "" {
		out.ValidScopes = append(out.ValidScopes, documentScopeOption{Scope: physical})
	}
	if packageAllowed {
		for _, id := range ids {
			p := v.catalog[id].PackageReference
			option := documentScopeOption{Scope: p.Scope(), Callable: p.Callable}
			if !p.IsZero() && !slices.Contains(out.ValidScopes, option) && record.PackageScopeCovers(v.outside(p.Scope(), ids), p.Scope(), p.Callable) {
				out.ValidScopes = append(out.ValidScopes, option)
			}
		}
	}
	return out
}

func documentScopeError(code string) bool {
	return code == "scope_invalid" || code == "scope_unknown_path" || code == "scope_generalized"
}

// narrowPackage accepts a named, verified package below the proposed path
// when exactly one such scope is supported by the same cited evidence.
func (v documentScopeInputs) narrowPackage(scope string, ids []string, claim string, issue documentScopeIssue) string {
	narrower := ""
	for _, option := range issue.ValidScopes {
		p := option.Scope
		if option.Callable == "" || p == scope || !record.ScopeUnder(p, scope) || !record.PackageScopeCovers(v.outside(p, ids), p, claim) {
			continue
		}
		if narrower != "" && narrower != p {
			return ""
		}
		narrower = p
	}
	return narrower
}

// fitDocumentRepair uses the existing repair allowance, preserving the
// excerpt handles and input. Optional feedback stops at the request budget.
func (s *roleStage) fitDocumentRepair(in docsInput, repair *outputRepair, issues []documentScopeIssue, instructions string) ([]byte, error) {
	if repair == nil || len(issues) == 0 || !slices.ContainsFunc(repair.OutputErrors, documentScopeError) {
		in.Repair = repair
		return marshal(in)
	}
	for n := len(issues); n > 0; n-- {
		in.Repair = &outputRepair{OutputErrors: repair.OutputErrors, DocumentScopes: issues[:n]}
		body, err := marshal(in)
		if err != nil {
			return nil, err
		}
		if _, ok := s.fits(instructions, body, false); ok {
			return body, nil
		}
	}
	in.Repair = repair
	return marshal(in)
}
