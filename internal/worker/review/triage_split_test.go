package review

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// TestSplitCrossToolGroupMarksEachChunk: a merged group of 70 staticcheck
// diagnostics and one go vet diagnostic on the same line, split at the
// group limit, flags only the chunk that holds both tools as cross_tool.
func TestSplitCrossToolGroupMarksEachChunk(t *testing.T) {
	var b strings.Builder
	b.WriteString("package app\n\nfunc A(p *int) int {\n")
	for range 72 {
		b.WriteString("\t_ = *p\n")
	}
	b.WriteString("\treturn 0\n}\n")
	j := treeJob(t, map[string]string{"app/a.go": b.String()})
	var sc []toolObservation
	for i := range 70 {
		sc = append(sc, triageObs(fmt.Sprintf("s%02d", i), staticcheck.AnalyzerID, "SA5011", "possible nil pointer dereference", "app/a.go", 4+i))
	}
	scDoc := triageDocOf("staticcheck", staticcheck.AnalyzerID, sc...)
	scDoc.doc.RulePack, scDoc.doc.RulePackVersion = staticcheck.Severity.RulePack, staticcheck.Severity.RulePackVersions[0]
	vetDoc := triageDocOf("go-vet", govet.AnalyzerID, triageObs("v1", govet.AnalyzerID, "nilness", "nil dereference in load", "app/a.go", 73))
	vetDoc.doc.RulePack, vetDoc.doc.RulePackVersion = govet.Severity.RulePack, govet.Severity.RulePackVersions[0]
	s := roleStage{ctx: t.Context(), j: j, task: "review.triage.t001", role: record.StageTriage, triage: "t001"}
	plan := s.planTriage([]toolDoc{scDoc, vetDoc}, nil, 4)
	var chunks []*triageGroup
	for _, bin := range plan.bins {
		chunks = append(chunks, bin...)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks %d, want the merged group split in two", len(chunks))
	}
	for _, c := range chunks {
		if tools := len(c.analyzers()); c.crossTool != (tools > 1) {
			t.Fatalf("chunk of %d diagnostics from %d tools marked cross_tool=%v", len(c.diags), tools, c.crossTool)
		}
	}
}
