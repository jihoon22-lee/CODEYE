package report

import (
	"os"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestReadPersistedReportWithPhysicalDocumentScope(t *testing.T) {
	r := exampleReport(t)
	const file = "docs/design.md"
	body, err := os.ReadFile("../../fixtures/modules/input/" + file)
	if err != nil {
		t.Fatal(err)
	}
	sha := domain.Digest(body)
	ev := SectionEvidence{ID: record.ExcerptID(r.Inputs.ManifestSHA256, file, sha, 1, 1),
		Kind: "document", Path: file, StartLine: 1, EndLine: 1, BlobSHA256: sha}
	r.Documents.Evidence = append(r.Documents.Evidence, ev)
	r.Documents.Contracts[0].AppliesTo = "docs"
	r.Documents.Contracts[0].EvidenceIDs = []string{ev.ID}
	r.Documents.Comparisons[0] = DocComparison{AppliesTo: "docs", Status: "unknown", Statement: r.Documents.Contracts[0].Statement,
		Reason: "문서 경로 안의 코드 발췌를 확인하지 못했다.", DocumentEvidenceIDs: []string{ev.ID}, CodeEvidenceIDs: []string{}}
	for i, item := range r.Documents.Items {
		if item.Path == file {
			r.Documents.Items[i].AppliesTo = "docs"
		}
	}
	encoded, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Documents.Comparisons[0].AppliesTo != "docs" || got.Documents.Contracts[0].AppliesTo != "docs" {
		t.Fatalf("persisted scopes changed: %+v", got.Documents.Comparisons)
	}
}
