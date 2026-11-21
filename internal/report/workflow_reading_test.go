package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestRelatedWorkflowReportKeepsFindingsAndTestStatus(t *testing.T) {
	r := exampleReport(t)
	var documents, investigation record.Record
	for stage, result := range map[string]*record.Record{"documents": &documents, "investigate": &investigation} {
		body, err := os.ReadFile("../../examples/review/" + stage + "-workflow-reading.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, result); err != nil {
			t.Fatal(err)
		}
	}
	const investigate = "review.investigate"
	section := r.Documents
	section.Contracts, section.Comparisons, section.Conflicts, section.Questions, section.Quality, section.InstructionNotes = nil, nil, nil, nil, nil, nil
	for _, contract := range documents.Contracts {
		section.Contracts = append(section.Contracts, DocContract{Statement: contract.Statement, AppliesTo: contract.AppliesTo, EvidenceIDs: contract.EvidenceIDs})
	}
	section.Evidence = evidenceOf(&documents, documents.Contracts[0].EvidenceIDs)
	st := &taskState{facts: TaskFacts{Key: investigate, Status: "succeeded"}, reviewResult: &ReviewResult{Accepted: true, Record: &investigation}}
	sectionContext{states: map[string]*taskState{investigate: st}}.addComparisons(section, &documents)
	comparison := section.Comparisons[0]
	original := investigation.ContractComparisons[0]
	if comparison.RelatedWorkflow == nil || comparison.Status != original.Status || comparison.Reason != original.Reason ||
		!slices.Equal(comparison.CodeEvidenceIDs, original.CodeEvidenceIDs) || r.Coverage.TestStatus != "not_run" {
		t.Fatalf("reading context altered the comparison or test status: %+v", comparison)
	}
	ids := make(map[string]string)
	for i := range section.Evidence {
		e := &section.Evidence[i]
		id := record.ExcerptID(r.Inputs.ManifestSHA256, e.Path, e.BlobSHA256, e.StartLine, e.EndLine)
		ids[e.ID], e.ID = id, id
		p := filepath.Join("../../fixtures/modules/workflow-reading/input", filepath.FromSlash(e.Path))
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		e.Excerpt = &sourceexcerpt.Excerpt{StartLine: e.StartLine, EndLine: e.EndLine, Text: strings.Join(lines[e.StartLine-1:e.EndLine], "\n")}
	}
	section.Contracts[0].EvidenceIDs = []string{ids[comparison.DocumentEvidenceIDs[0]]}
	first := &section.Comparisons[0]
	first.DocumentEvidenceIDs, first.CodeEvidenceIDs = slices.Clone(section.Contracts[0].EvidenceIDs), []string{ids[comparison.CodeEvidenceIDs[0]]}
	w := first.RelatedWorkflow
	w.ReadEvidenceID, w.ChangeEvidenceID, w.TestEvidenceID = ids[w.ReadEvidenceID], ids[w.ChangeEvidenceID], ids[w.TestEvidenceID]
	r.Builder.Version = strconv.Itoa(WorkflowReadingSince)
	fileOnlyLedgerForTest(&r)
	for i := range r.Artifacts {
		a := &r.Artifacts[i]
		if deref(a.TaskKey) == investigate && a.MediaType == record.MediaType {
			a.Decoder = strPtr((WorkflowReadingDecoder{}).Key().String())
		}
	}
	overview := overviewOf(r)
	r.Overview = &overview
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	testPath := "archive/routes_test.go:19-33"
	for _, include := range []bool{false, true} {
		projection := Project(r, include)
		if err := ValidateReport(projection); err != nil {
			t.Fatal(err)
		}
		for _, detail := range []string{DetailSummary, DetailFull} {
			html, err := RenderHTMLAt(projection, "digest", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			for _, output := range []string{string(html), string(RenderMarkdownAt(projection, "digest", Korean, detail))} {
				for _, want := range []string{Korean.Label("workflow.heading"), Korean.Label("workflow.context"), testPath} {
					if !strings.Contains(output, want) && !strings.Contains(output, mdText(want)) {
						t.Fatalf("missing %q for code=%v detail=%s", want, include, detail)
					}
				}
				if strings.Contains(output, "http.StatusNoContent") != include {
					t.Fatal("the test assertion was displayed outside the code-included projection or lost within it")
				}
			}
		}
	}
	for name, modify := range map[string]func(*Report){
		"older version": func(bad *Report) { bad.Builder.Version = strconv.Itoa(WorkflowReadingSince - 1) },
		"bad kind": func(bad *Report) {
			row := &bad.Documents.Comparisons[0]
			row.RelatedWorkflow.TestEvidenceID = row.CodeEvidenceIDs[0]
		},
		"missing ID": func(bad *Report) { bad.Documents.Comparisons[0].RelatedWorkflow.ReadEvidenceID = "missing" },
		"unaccepted": func(bad *Report) {
			for i := range bad.CodeReview.Roles {
				if bad.CodeReview.Roles[i].Key == investigate {
					bad.CodeReview.Roles[i].Result = "result_rejected"
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var bad Report
			if err := json.Unmarshal(raw, &bad); err != nil {
				t.Fatal(err)
			}
			modify(&bad)
			if err := ValidateReport(bad); err == nil {
				t.Fatal("invalid related evidence was accepted")
			}
		})
	}
}
