package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestAcceptedWorkflowReadingRetainsSubmittedContextWithoutModelCitations(t *testing.T) {
	const statement = "목록에서 책장을 비운 뒤 같은 항목을 조회하면 비어 있음을 안내한다."
	j := dirJob(t, workflowReadingFixture)
	j.Snapshot.Digest, j.RunID, j.AttemptID = exampleSnapshot, exampleRun, exampleAttempt
	caller := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
		"documents": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[docsInput](t, q)
			var selected string
			for _, r := range in.WorkflowRoutes {
				if r.Method == "GET" && r.Path == "/browse/{label}" {
					selected = r.ID
				}
			}
			if selected == "" || len(in.Documents) == 0 || !strings.Contains(in.Documents[0].Text, statement) {
				t.Fatalf("missing read route or contract: %+v", in)
			}
			out := docsOutput{Contracts: []record.Contract{{Statement: statement, AppliesTo: "archive", EvidenceIDs: []string{in.Documents[0].EvidenceID}}},
				Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}}
			out.WorkflowSelections, _ = json.Marshal([]workflowChoice{{ContractIndex: 1, RouteIDs: []string{selected}}})
			return asJSON(out)
		},
		"investigate": func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[investigateInput](t, q)
			if !in.Scope.WholeRun {
				return asJSON(map[string]any{"candidates": []any{}})
			}
			if len(in.Scope.WorkflowRoutes) != 1 || in.Scope.WorkflowRoutes[0].Test == "" ||
				in.Scope.WorkflowRoutes[0].Handler == "" || in.Scope.WorkflowRoutes[0].RelatedMutation == nil ||
				in.Scope.WorkflowRoutes[0].RelatedMutation.Handler == "" {
				t.Fatalf("request lacks submitted reading: %+v", in.Scope.WorkflowRoutes)
			}
			return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
				ContractIndex: 1, AppliesTo: "archive", Status: "unknown", Reason: "빈 결과에 오류 응답을 내는 조회 처리기를 확인했다.",
				DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{in.Scope.WorkflowRoutes[0].Handler},
			}}})
		},
	}}
	_, results := runRole(t, j, vetStub{}, caller, "1.27.4")
	for _, stage := range []string{record.StageDocuments, record.StageInvestigate} {
		if res := results["review."+stage]; res.Status != domain.TaskSucceeded {
			t.Fatalf("%s: %+v", stage, res)
		}
	}
	doc := readRecord(t, j, record.StageDocuments)
	inv := readRecord(t, j, record.StageInvestigate)
	reading := inv.WorkflowReading
	if inv.Schema != record.SchemaVersionCitedLines || reading == nil || !inv.WorkflowReadingMatches(doc) ||
		len(inv.ContractComparisons) != 1 || inv.ContractComparisons[0].Status != "unknown" ||
		!slices.Equal(inv.ContractComparisons[0].CodeEvidenceIDs, []string{reading.ReadEvidenceID}) ||
		len(inv.WorkflowReadingEvidence()) != 3 || slices.ContainsFunc(inv.CitedEvidence(), func(e record.Excerpt) bool {
		return e.ID == reading.TestEvidenceID || e.ID == reading.ChangeEvidenceID
	}) {
		t.Fatalf("submitted workflow did not remain separate from model citations: %+v", inv)
	}

	tests := map[string]func(*record.Record){
		"new field on legacy version": func(r *record.Record) { r.Schema = record.SchemaVersion },
		"wrong kind":                  func(r *record.Record) { r.WorkflowReading.TestEvidenceID = reading.ChangeEvidenceID },
		"unknown source":              func(r *record.Record) { r.WorkflowReading.TestEvidenceID = "ev_" + strings.Repeat("0", 24) },
		"shared handler source":       func(r *record.Record) { r.WorkflowReading.ChangeEvidenceID = reading.ReadEvidenceID },
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			bad, badReading := inv, *reading
			bad.WorkflowReading = &badReading
			edit(&bad)
			if err := record.Validate(bad, expectRecord(j, record.StageInvestigate)); err == nil {
				t.Fatalf("invalid workflow accepted: %+v", bad.WorkflowReading)
			}
		})
	}
	badDocs := doc
	badDocs.WorkflowSelections = []record.WorkflowSelection{{ContractIndex: 1, RouteDigests: []string{strings.Repeat("0", 64)}}}
	if inv.WorkflowReadingMatches(badDocs) {
		t.Fatal("workflow reading matched an unselected route")
	}
	badDocs = doc
	badDocs.Contracts = slices.Clone(doc.Contracts)
	badDocs.Contracts[0].AppliesTo = "archive/other"
	if inv.WorkflowReadingMatches(badDocs) {
		t.Fatal("workflow reading widened an accepted document scope")
	}

	for _, stage := range []string{record.StageDocuments, record.StageInvestigate} {
		p := filepath.Join(exampleDir, stage+"-workflow-reading.json")
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, mustRead(t, recordPath(j, stage)), "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
			if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			compareStoredExample(t, p, pretty.Bytes())
		}
	}
}
