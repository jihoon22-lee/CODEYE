//go:build linux

package run

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// Discovery through the real worker: the deep-review composition (discovery →
// go vet → review roles) on the fixture repository with the actual Go
// toolchain. Discovery runs first, its probe execution is recorded cleanly
// (so the worker keeps the success), and the svc/b mismatch verdict agrees
// with go vet's toolchain_unavailable failure on the same node.
func TestDiscoveryTasksThroughTheWorker(t *testing.T) {
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	e := newEnv(t)
	if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../fixtures/modules/input")
	err := filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(e.src, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	vet, err := govet.New(govet.Config{GoExecutable: goExe})
	if err != nil {
		t.Fatal(err)
	}
	h, err := discovery.Wrap(review.Handler{Static: vet}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe}})
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
	// The fake agent buffers 8 process_started messages; this attempt runs
	// more (probe + 4 per go-vet module), so drain them.
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
	start := time.Now()
	if err := fa.conn.Send(job); err != nil {
		t.Fatal(err)
	}
	var c *workerproto.Complete
	select {
	case c = <-fa.complete:
	case <-time.After(90 * time.Second):
		t.Fatalf("no complete; messages %v", fa.kinds())
	}
	t.Logf("attempt finished in %s", time.Since(start).Round(time.Millisecond))
	if c.Outcome != "partial" || c.ResultManifestClientArtifact == nil || c.CleanupState != "confirmed" {
		t.Fatalf("completion: %+v", c)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(fa.artifacts[*c.ResultManifestClientArtifact], &m); err != nil {
		t.Fatal(err)
	}
	type row struct{ key, status, reason string }
	got := []row{}
	for _, task := range m.Tasks {
		r := ""
		if task.Reason != nil {
			r = *task.Reason
		}
		if _, _, slice := record.SliceTask(task.Key); slice {
			if task.Status != "skipped_policy" || r != "model_source_not_approved" {
				t.Fatalf("slice reused an unapproved source: %+v", task)
			}
			continue
		}
		got = append(got, row{task.Key, string(task.Status), r})
	}
	want := []row{
		{"env.discover", "succeeded", ""}, {"docs.discover", "succeeded", ""},
		{"go-vet", "succeeded", ""}, {"go-vet.m01", "succeeded", ""}, {"go-vet.m02", "failed", govet.ReasonToolchain},
		{"review.environment", "skipped_policy", "model_source_not_approved"}, {"review.documents", "skipped_policy", "model_source_not_approved"}, {"review.investigate", "skipped_policy", "model_source_not_approved"},
		{"review.interpret", "skipped_policy", "model_source_not_approved"}, {"review.verify", "skipped_policy", "model_source_not_approved"},
		{"review.scope", "skipped_policy", "model_source_not_approved"},
		{"review.triage.t001", "skipped_policy", "model_source_not_approved"}, {"review.verify.t001", "skipped_policy", "model_source_not_approved"},
		{"review.triage.t002", "skipped_policy", "model_source_not_approved"}, {"review.verify.t002", "skipped_policy", "model_source_not_approved"},
	}
	if len(got) != len(want) {
		t.Fatalf("tasks %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tasks %v\nwant  %v", got, want)
		}
	}
	envTask := m.Tasks[0]
	if len(envTask.Executions) != 1 || envTask.Executions[0].Name != discovery.ExecGoVersion || envTask.Executions[0].ExitCode == nil || *envTask.Executions[0].ExitCode != 0 ||
		envTask.Executions[0].Command.Dir == "." || len(envTask.Artifacts) != 1 || envTask.Artifacts[0].MediaType != envdoc.MediaType || envTask.Artifacts[0].Role != "other" {
		t.Fatalf("env.discover manifest entry: %+v", envTask)
	}
	var manifest source.Manifest
	docs := map[string]envdoc.Document{}
	for id, body := range fa.artifacts {
		switch fa.roles[id] {
		case "source_manifest":
			if manifest, err = source.DecodeManifest(body); err != nil {
				t.Fatal(err)
			}
		case "other":
			var d envdoc.Document
			if json.Unmarshal(body, &d) == nil && d.SchemaVersion != "" {
				docs[d.Kind] = d
			}
		}
	}
	in2 := discovery.Input{Manifest: manifest, Digest: m.Snapshot.ManifestDigest, RunID: m.RunID, AttemptID: m.AttemptID}
	for _, kind := range []string{envdoc.KindEnvironment, envdoc.KindDocuments} {
		b, _ := json.Marshal(docs[kind])
		if _, err := envdoc.Decode(b, in2.Expect(kind)); err != nil {
			t.Fatalf("%s artifact: %v", kind, err)
		}
	}
	env := docs[envdoc.KindEnvironment].Environment
	if o := env.Observations[0]; o.Tool != "go" || o.Status != envdoc.ObsObserved || o.Execution == nil || *o.Execution != discovery.ExecGoVersion {
		t.Fatalf("observation %+v", o)
	}
	local := *env.Observations[0].Version
	for _, v := range env.Verdicts {
		if v.Module == "go:svc/b" && envdoc.Decide("go_directive", "1.30", local) == envdoc.VerdictMismatch && v.Status != envdoc.VerdictMismatch {
			t.Fatalf("svc/b verdict %+v with local go %s", v, local)
		}
	}
}
