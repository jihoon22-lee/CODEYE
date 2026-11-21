package record

import (
	"slices"
	"strings"
)

const (
	MaxWorkflowRoutes           = 4
	LimWorkflowSelectionInvalid = "workflow_selection_invalid"
	WorkflowSelectionPurpose    = "workflow_selection"
)

// WorkflowSelection is optional navigation from an accepted document
// contract. The digests identify routes, never evidence excerpts.
type WorkflowSelection struct {
	ContractIndex int      `json:"contract_index"`
	RouteDigests  []string `json:"route_digests"`
}

// WorkflowReading names source actually submitted together for a selected
// workflow. It does not record what the model cited or decided.
type WorkflowReading struct {
	ContractIndex    int    `json:"contract_index"`
	RouteDigest      string `json:"route_digest"`
	ReadEvidenceID   string `json:"read_evidence_id"`
	ChangeEvidenceID string `json:"change_evidence_id"`
	TestEvidenceID   string `json:"test_evidence_id"`
}

func (w WorkflowReading) EvidenceIDs() []string {
	return []string{w.ReadEvidenceID, w.ChangeEvidenceID, w.TestEvidenceID}
}

// WorkflowReadingEvidence supplies source windows for the separate reader
// context. CitedEvidence continues to mean references in model output.
func (r Record) WorkflowReadingEvidence() []Excerpt {
	if r.WorkflowReading == nil {
		return nil
	}
	var out []Excerpt
	ids := r.WorkflowReading.EvidenceIDs()
	for _, e := range r.Evidence {
		if slices.Contains(ids, e.ID) {
			out = append(out, e)
		}
	}
	return out
}

// WorkflowReadingMatches verifies the accepted document and the exact
// selection supplied to the investigation. The caller checks upstream SHA.
func (r Record) WorkflowReadingMatches(doc Record) bool {
	w := r.WorkflowReading
	if w == nil || r.Stage != StageInvestigate || r.Slice != nil || !r.Accepted() ||
		doc.Stage != StageDocuments || !doc.Accepted() || r.RunID != doc.RunID || r.AttemptID != doc.AttemptID ||
		r.Snapshot != doc.Snapshot || w.ContractIndex < 1 || w.ContractIndex > len(doc.Contracts) {
		return false
	}
	if !slices.ContainsFunc(doc.WorkflowSelections, func(s WorkflowSelection) bool {
		return s.ContractIndex == w.ContractIndex && slices.Contains(s.RouteDigests, w.RouteDigest)
	}) {
		return false
	}
	evidence := r.WorkflowReadingEvidence()
	scope := doc.Contracts[w.ContractIndex-1].AppliesTo
	return len(evidence) == 3 && !slices.ContainsFunc(evidence, func(e Excerpt) bool { return !ScopeUnder(e.Path, scope) })
}

func checkWorkflowReading(r Record, catalog map[string]Excerpt) error {
	w := r.WorkflowReading
	if w == nil {
		return nil
	}
	if r.Schema != SchemaVersionWorkflowReading || w.ContractIndex < 1 || w.ContractIndex > MaxContractComparisons ||
		!hex64.MatchString(w.RouteDigest) || w.ReadEvidenceID == w.ChangeEvidenceID ||
		!refs([]string{w.ReadEvidenceID, w.ChangeEvidenceID}, catalog, 2, "code") ||
		!refs([]string{w.TestEvidenceID}, catalog, 1, "test") {
		return invalid("workflow reading evidence")
	}
	if !slices.ContainsFunc(r.Upstream, func(up Upstream) bool {
		return up.Source == "review.documents" && up.State == UpstreamAccepted
	}) {
		return invalid("workflow reading requires accepted document upstream")
	}
	for _, id := range w.EvidenceIDs() {
		if e := catalog[id]; e.Origin != "context" || !strings.HasSuffix(e.Path, ".go") || SourceKind(e.Path) != e.Kind {
			return invalid("workflow reading has ineligible source")
		}
	}
	return nil
}

func checkWorkflowSelections(r Record) error {
	if r.Schema != SchemaVersionWorkflow {
		if r.WorkflowSelections != nil {
			return invalid("workflow selections on an older record")
		}
		return nil
	}
	if r.Stage != StageDocuments || !r.Accepted() || len(r.Contracts) == 0 ||
		(len(r.WorkflowSelections) == 0 && !slices.ContainsFunc(r.Calls, func(c Call) bool { return c.Purpose == WorkflowSelectionPurpose })) {
		return invalid("workflow selections require accepted documents")
	}
	last, total := 0, 0
	for _, row := range r.WorkflowSelections {
		total += len(row.RouteDigests)
		if row.ContractIndex <= last || row.ContractIndex > len(r.Contracts) ||
			len(row.RouteDigests) == 0 || total > MaxWorkflowRoutes {
			return invalid("workflow selection contract or count")
		}
		for i, digest := range row.RouteDigests {
			if !hex64.MatchString(digest) || slices.Contains(row.RouteDigests[:i], digest) {
				return invalid("workflow route digest")
			}
		}
		last = row.ContractIndex
	}
	return nil
}
