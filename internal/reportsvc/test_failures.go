package reportsvc

import (
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// The report binding selects which finalized artifacts to read. Opening
// optional evidence must not change whether the report can be published.
func readGoTestFailureBodies(in *report.Input, artifacts []postgres.ArtifactRecord, read func(postgres.ArtifactRecord) ([]byte, bool, error)) {
	if in.Run.Profile != "with-tests" {
		return
	}
	byID := make(map[string]postgres.ArtifactRecord, len(artifacts))
	for _, a := range artifacts {
		byID[a.ID] = a
	}
	in.ReadGoTestFailureBodies(func(ref report.ArtifactRef) ([]byte, bool) {
		a, ok := byID[ref.ID]
		if !ok || a.RunID != ref.RunID || a.AttemptID != ref.AttemptID || a.Role != ref.Role ||
			a.MediaType != ref.MediaType || a.SHA256 != ref.SHA256 || a.SizeBytes != ref.SizeBytes {
			return nil, false
		}
		body, ok, err := read(a)
		return body, ok && err == nil
	})
}
