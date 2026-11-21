package record

import "slices"

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
