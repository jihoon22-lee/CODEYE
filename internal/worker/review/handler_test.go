package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func fixtureJob(t *testing.T, docs bool) analysis.Job {
	t.Helper()
	root := t.TempDir()
	write := func(name, s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("metrics.go", "package metrics\n// Sum returns the sum of every sample.\nfunc Sum(samples []int) int {\n n:=0\n for i:=0;i+1<len(samples);i++ { n+=samples[i] }\n return n\n}\n")
	if docs {
		write("README.md", "Sum includes every sample, including the last. An empty input sums to zero.\n")
	}
	write("service.go", "package metrics\nconst password = \"long_fixture_dummy_value\"")
	s, err := source.AcquireLocal(t.Context(), root, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Snapshot: s, Scratch: t.TempDir(), Scope: domain.Scope{Kind: "full"}, AllowModelReview: true}
}

func TestContextSelection(t *testing.T) {
	j := fixtureJob(t, false)
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil || len(c.Excerpts) != 1 || c.Excerpts[0].Path != "metrics.go" || c.Documents != 0 || len(c.Limitations) == 0 {
		t.Fatal(c, err)
	}
	j = fixtureJob(t, true)
	c, err = buildCorpus(j.Snapshot, domain.Scope{Kind: "paths", Paths: []string{"metrics.go"}})
	if err != nil || len(c.Excerpts) != 1 || c.Documents != 0 {
		t.Fatal(c, err)
	}
	p := filepath.Join(j.Snapshot.SnapshotDir, "metrics.go")
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildCorpus(j.Snapshot, j.Scope); err == nil {
		t.Fatal("accepted changed evidence")
	}
}

func TestRoleOutputValidation(t *testing.T) {
	rec := Record{Evidence: []Excerpt{{ID: "src", Kind: "code"}, {ID: "doc", Kind: "document"}}}
	if err := accept("context", json.RawMessage(`{"facts":[{"kind":"documented","statement":"stated in readme","evidence_ids":["doc"]}]}`), &rec); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"facts":[{"kind":"documented","statement":"contract","evidence_ids":["src"]}]}`, `{"facts":[{"kind":"observed","statement":"missing","evidence_ids":["invented"]}]}`, `{"facts":[]}`} {
		if accept("context", json.RawMessage(raw), &rec) == nil {
			t.Fatal("accepted invalid fact")
		}
	}
	if err := accept("investigate", json.RawMessage(`{"candidates":[]}`), &rec); err != nil {
		t.Fatal(err)
	}
	if accept("investigate", json.RawMessage(`{}`), &rec) == nil {
		t.Fatal("missing list interpreted as empty")
	}
}

func TestExcludedModelAndMissingContext(t *testing.T) {
	j := fixtureJob(t, false)
	h := Handler{}
	j.AllowModelReview = false
	if r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.context"}, nil); r.Status != domain.TaskSkippedPolicy {
		t.Fatal(r)
	}
	j.AllowModelReview = true
	if r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.context"}, nil); r.Status != domain.TaskUnsupported {
		t.Fatal(r)
	}
}

func TestLiveQualityRoles(t *testing.T) {
	relay := os.Getenv("CODEYE_TEST_REVIEW_RELAY")
	if relay == "" {
		t.Skip("explicit paid or hosted model test not enabled")
	}
	h := Handler{Caller: opencode.Config{Executable: os.Getenv("CODEYE_TEST_OPENCODE_BIN"), Model: "codeye-gateway/gpt-6.2-sol", RelayFile: relay, Timeout: 75 * time.Second}}
	for _, docs := range []bool{false, true} {
		j := fixtureJob(t, docs)
		for _, role := range []string{"context", "investigate"} {
			r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review." + role}, &executor.Executor{})
			if r.Status != domain.TaskSucceeded {
				t.Fatalf("documents=%v role=%s status=%s reason=%s", docs, role, r.Status, r.Reason)
			}
		}
		r, err := readRecord(j, "investigate")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Candidates) == 0 {
			t.Fatal("missing candidate on labelled fixture")
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "long_fixture_dummy_value") {
			t.Fatal("private fixture text exposed")
		}
		t.Logf("documents=%v actual facts=%d candidates=%d validated references; snapshot=%s", docs, len(r.Facts), len(r.Candidates), j.Snapshot.Digest)
	}
}

// Display stages (docs/ARCHITECTURE.md#표시-단계와-task-단계): the legacy
// pack's context role reads documents, investigation and verification are
// code review. The static handler's own stages are kept.
func TestPlanStages(t *testing.T) {
	plan, err := Handler{Static: stubStatic{}}.Plan(t.Context(), analysis.Job{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]domain.Stage{"static": "", "review.context": domain.StageDocuments, "review.investigate": domain.StageCodeReview, "review.verify": domain.StageCodeReview}
	for _, p := range plan {
		if s, ok := want[p.Key]; !ok || s != p.Stage {
			t.Fatalf("%s stage %q", p.Key, p.Stage)
		}
	}
}
