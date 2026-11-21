package govet

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gograph"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestRealGoPackageGraphArtifact(t *testing.T) {
	files := map[string]string{
		"go.mod":      fixGoMod,
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go":      "package b\n\nimport \"example.test/probe/a\"\n\nfunc B() int { return a.A() }\n",
		"b/b_test.go": "package b_test\n\nimport _ \"example.test/probe/a\"\n",
		"b/other.go":  "//go:build graph_variant\n\npackage b\n",
	}
	readGraph := func(t *testing.T, fx fixture, res analysis.TaskResult) ([]byte, gograph.Document) {
		t.Helper()
		idx := slices.IndexFunc(res.Artifacts, func(a analysis.ArtifactFile) bool { return a.MediaType == gograph.MediaType })
		if idx < 0 {
			t.Fatalf("native task lacks graph artifact: %+v", res)
		}
		body, err := os.ReadFile(res.Artifacts[idx].Path)
		if err != nil {
			t.Fatal(err)
		}
		var listSHA string
		for _, execution := range res.Executions {
			if execution.Name == execList {
				listSHA = streamRef(execution.Result.Stdout).SHA256
			}
		}
		source := includedFiles(fx.Job.Snapshot.Manifest)
		got, err := gograph.Decode(body, "go-vet", fx.Job.Snapshot.Digest, listSHA, func(p string) (string, bool) {
			e, ok := source[p]
			return e.SHA256, ok
		})
		if err != nil {
			t.Fatal(err)
		}
		return body, got
	}
	h := newHandler(t, Config{})
	fx := newFixture(t, "", files, domain.Scope{})
	body, d := readGraph(t, fx, runAll(t, h, fx.Job, &executor.Executor{})["go-vet"])
	if d.Status != "complete" || d.UnlistedFiles == nil || *d.UnlistedFiles != 0 || d.ParsedFiles != 3 || d.IgnoredFiles != 1 || len(d.Edges) != 2 ||
		d.Edges[0].From != "example.test/probe/b" || d.Edges[0].To != "example.test/probe/a" ||
		d.Edges[0].Kind != gograph.ExternalTest || d.Edges[0].Witness.Path != "b/b_test.go" || d.Edges[0].Witness.Line != 3 {
		t.Fatalf("incorrect active graph evidence: %+v", d)
	}
	const example = "../../../../examples/go-package-graph.json"
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		if err := os.WriteFile(example, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("candidate not in the Go package listing", func(t *testing.T) {
		files["unlisted/alternate.go"] = "//go:build graph_variant\n\npackage unlisted\n"
		defer delete(files, "unlisted/alternate.go")
		omitted := newFixture(t, "", files, domain.Scope{})
		res := runAll(t, h, omitted.Job, &executor.Executor{})["go-vet"]
		_, got := readGraph(t, omitted, res)
		doc, _ := readDoc(t, res)
		if got.Status != "partial" || got.UnlistedFiles == nil || *got.UnlistedFiles != 1 ||
			*got.UnlistedFiles != doc.Coverage.UnlistedGoFiles || got.ParsedFiles != 3 ||
			got.IgnoredFiles != 1 || got.UnparsedFiles != 0 || !slices.Contains(got.Limitations, "go_candidate_files_unlisted") {
			t.Fatalf("incorrect listing/file accounting: graph %+v, vet %+v", got, doc.Coverage)
		}
	})
	t.Run("truncated listing cannot establish missing count", func(t *testing.T) {
		job := newFixture(t, "", files, domain.Scope{})
		runner := &mutatingRunner{inner: &executor.Executor{}, mutate: func(command string, r *executor.Result) {
			if command == "list" {
				r.Stdout.Truncated = true
				r.Stdout.TotalBytes++
			}
		}}
		_, got := readGraph(t, job, runAll(t, h, job.Job, runner)["go-vet"])
		if got.UnlistedFiles != nil || !slices.Contains(got.Limitations, "go_list_incomplete") {
			t.Fatalf("truncated listing claimed an unlisted-file count: %+v", got)
		}
	})
	narrow := newFixture(t, "", files, domain.Scope{Kind: "paths", Paths: []string{"a/a.go"}})
	_, partial := readGraph(t, narrow, runAll(t, h, narrow.Job, &executor.Executor{})["go-vet"])
	if partial.Scope != "selected_packages" || partial.Status != "partial" || partial.ParsedFiles != 1 || len(partial.Nodes) != 1 || len(partial.Edges) != 0 {
		t.Fatalf("file selection overstated: %+v", partial)
	}
}
