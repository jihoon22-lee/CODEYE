package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func documentationReportFixture(t *testing.T, readme string) *fixture {
	t.Helper()
	f := analyzerFixture(t, nil, taskSpec{key: "go-vet", status: "failed"})
	f.in.Tasks = nil
	files := map[string]string{"README.md": readme, "main.go": `package main
import "flag"
func main() {
    flag.String("report-zone", "UTC", "output zone")
    flag.Parse()
}
`}
	var entries []any
	for _, p := range []string{"README.md", "main.go"} {
		b := []byte(files[p])
		entries = append(entries, map[string]any{"path": p, "kind": "file", "size_bytes": len(b), "mode": 420, "sha256": domain.Digest(b), "encoding": "utf-8"})
	}
	manifest := canonical(t, map[string]any{"schema_version": "0.1", "kind": "local", "consistency": "stable_copy_best_effort", "source_fixed_at": "2026-11-18T00:00:00Z", "entries": entries, "limitations": []string{"local_copy_is_not_atomic"}})
	m, err := source.DecodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sha := domain.Digest(manifest)
	f.in.SourceManifest, f.in.Snapshot.ManifestDigest = manifest, sha
	f.in.Artifacts[0].SHA256, f.in.Artifacts[0].SizeBytes = sha, int64(len(manifest))
	in := discovery.Input{Manifest: m, Digest: sha, RunID: f.runID, AttemptID: f.attempt, Scope: domain.Scope{Kind: "full"}}
	doc, err := discovery.Documents(in, discovery.DefaultBudget, func(e source.Entry) ([]byte, error) { return []byte(files[e.Path]), nil })
	if err != nil {
		t.Fatal(err)
	}
	body := canonical(t, doc)
	a := ArtifactRef{ID: domain.NewID(), RunID: f.runID, AttemptID: f.attempt, Role: "other", MediaType: envdoc.MediaType, SHA256: domain.Digest(body), SizeBytes: int64(len(body))}
	f.in.Artifacts = append(f.in.Artifacts, a)
	f.in.Bodies[a.ID] = body
	id := domain.NewID()
	f.in.Tasks = append(f.in.Tasks, TaskFacts{ID: id, Key: envdoc.TaskDocuments, Status: "succeeded", ExecutionOrdinal: 1})
	var rm analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm); err != nil {
		t.Fatal(err)
	}
	rm.Snapshot.ManifestDigest = sha
	rm.Tasks = append(rm.Tasks[:0], analysis.ManifestTask{TaskID: id, Key: envdoc.TaskDocuments, Status: domain.TaskSucceeded, Executions: []analysis.ManifestExecution{},
		Artifacts: []analysis.ManifestArtifact{{ArtifactID: a.ID, Role: a.Role, MediaType: a.MediaType, SHA256: a.SHA256, SizeBytes: a.SizeBytes}}, Limitations: []string{}})
	f.setManifest(t, rm)
	return f
}

func TestDocumentationGapPublishesInCodeIncludedAndLocationOnlyExports(t *testing.T) {
	f := documentationReportFixture(t, "Instructions for printing a summary.\nMore examples below.\n")
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	gaps := r.Documents.DocumentationGaps
	if r.Documents.Discovery != DiscoveryAccepted || len(gaps) != 1 || gaps[0].Symbol != "--report-zone" || gaps[0].Default != "UTC" ||
		gaps[0].Source.Path != "main.go" || gaps[0].Source.StartLine != 4 || gaps[0].Searched[0] != (DocCoverageSearch{"README.md", 2}) || len(r.Findings) != 0 {
		t.Fatalf("lost or promoted document scope: %+v", r.Documents)
	}
	for _, detail := range []string{DetailSummary, DetailFull} {
		html, err := RenderHTMLAt(r, "test", Korean, detail)
		if err != nil {
			t.Fatal(err)
		}
		markdown := RenderMarkdownAt(r, "test", Korean, detail)
		for _, out := range []string{string(html), string(markdown)} {
			for _, text := range []string{"--report-zone", "main.go:4", "README.md:1–2", "코드에 선언된 기본값"} {
				if !strings.Contains(out, text) {
					t.Fatalf("%s rendering lacks %q", detail, text)
				}
			}
		}
	}
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	jsonOut, err := Marshal(free)
	if err != nil {
		t.Fatal(err)
	}
	markdown := string(RenderMarkdownAt(free, "test", Korean, DetailSummary))
	if strings.Contains(string(jsonOut), "--report-zone") || strings.Contains(markdown, "--report-zone") ||
		free.Documents.DocumentationGaps[0].Default != "" || !strings.Contains(markdown, "README.md:1–2") || gaps[0].Symbol == "" {
		t.Fatal("projection exposed source values, modified the original, or lost the search bounds")
	}
	known := documentationReportFixture(t, "Use `--report-zone` to change the output zone.\n")
	if got := build(t, known.in, DefaultRegistry()); len(got.Documents.DocumentationGaps) != 0 {
		t.Fatalf("documented option became a gap: %+v", got.Documents.DocumentationGaps)
	}
}
