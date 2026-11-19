//go:build linux

package run

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// Review inputs of other languages through the real worker and the report
// builder: a Python-only
// snapshot (fixtures/review-inputs/python) with the production analyzer
// composite (no tool configured) and a scripted model. The review sends
// Python code to investigate and verify, and the report built from the
// uploaded artifacts with the default registry accepts every review record
// (no lineage or decode limitation) and cites Python evidence.
func TestReviewInputsOfPythonThroughTheWorkerAndReport(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	e := newEnv(t)
	if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../fixtures/review-inputs/python")
	err := filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(e.src, rel)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(e.src, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := builtin.Config{GoExecutable: goExe, Tools: map[string]string{}, ProbeDir: t.TempDir()}
	static, _, err := builtin.Composite(context.Background(), cfg, &executor.Executor{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := discovery.Wrap(review.RoleHandler{Static: static, Caller: &roleScripted{}}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe, Analyzers: builtin.Probes(cfg)}})
	if err != nil {
		t.Fatal(err)
	}
	fa, in, done, _ := startWorker(t, h)
	defer func() {
		in.Close()
		if code := <-done; code != 0 {
			t.Errorf("worker exit %d", code)
		}
	}()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-fa.started:
			case <-stop:
				return
			}
		}
	}()
	fa.hello(e)
	job := localJob(e)
	job.AllowModelReview = true
	if err := fa.conn.Send(job); err != nil {
		t.Fatal(err)
	}
	var c *workerproto.Complete
	select {
	case c = <-fa.complete:
	case <-time.After(90 * time.Second):
		t.Fatalf("no complete; messages %v", fa.kinds())
	}
	if c.ResultManifestClientArtifact == nil || c.CleanupState != "confirmed" {
		t.Fatalf("completion: %+v", c)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(fa.artifacts[*c.ResultManifestClientArtifact], &m); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, task := range m.Tasks {
		status[task.Key] = string(task.Status)
	}
	for _, key := range []string{"review.investigate", "review.verify"} {
		if status[key] != "succeeded" {
			t.Fatalf("%s %s; tasks %v", key, status[key], status)
		}
	}
	r, err := report.Build(reportInput(t, fa, m, *c.ResultManifestClientArtifact), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range r.Limitations {
		if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, "analysis_artifact_") {
			t.Fatalf("limitation %s in %v", l, r.Limitations)
		}
	}
	if r.Coverage.AIReview.Status != report.AIReviewPerformed {
		t.Fatalf("ai review %+v limitations %v", r.Coverage.AIReview, r.Limitations)
	}
	python := false
	for _, f := range r.Findings {
		if !strings.HasPrefix(f.ID, "review_") && !strings.HasPrefix(f.ID, "candidate") {
			continue
		}
		for _, ev := range f.Evidence {
			python = python || (ev.Location != nil && strings.HasSuffix(ev.Location.Path, ".py"))
		}
	}
	if !python {
		t.Fatalf("no review finding cites Python evidence: %+v", r.Findings)
	}
}
