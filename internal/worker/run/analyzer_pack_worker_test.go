//go:build linux

package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

type packRun struct {
	manifest analysis.ResultManifest
	source   source.Manifest
	envs     []envdoc.Document
	docs     map[string]obsdoc.Document // generic envelopes by task key
	complete *workerproto.Complete
}

// runPack runs the production deep-review composition (discovery → the
// analyzer composite → review-v1 roles) through the real worker on a small
// Go module with a go vet and a staticcheck finding.
func runPack(t *testing.T, tools map[string]string) packRun {
	t.Helper()
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	e := newEnv(t)
	for rel, body := range map[string]string{
		"go.mod":     "module example.test/e2e\n\ngo 1.22\n",
		"lib/lib.go": "package lib\n\nimport (\n\t\"fmt\"\n\t\"regexp\"\n)\n\nfunc Re() *regexp.Regexp { return regexp.MustCompile(\"[\") }\n\nfunc P() { fmt.Printf(\"%d\", \"x\") }\n",
	} {
		p := filepath.Join(e.src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := builtin.Config{GoExecutable: goExe, Tools: tools, ProbeDir: t.TempDir()}
	c, _, err := builtin.Composite(context.Background(), cfg, &executor.Executor{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := discovery.Wrap(review.Handler{Static: c}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe, Analyzers: builtin.Probes(cfg)}})
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
	if err := fa.conn.Send(localJob(e)); err != nil {
		t.Fatal(err)
	}
	var out packRun
	select {
	case out.complete = <-fa.complete:
	case <-time.After(120 * time.Second):
		t.Fatalf("no complete; messages %v", fa.kinds())
	}
	if out.complete.ResultManifestClientArtifact == nil || out.complete.CleanupState != "confirmed" {
		t.Fatalf("completion: %+v", out.complete)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if err := json.Unmarshal(fa.artifacts[*out.complete.ResultManifestClientArtifact], &out.manifest); err != nil {
		t.Fatal(err)
	}
	for id, body := range fa.artifacts {
		if fa.roles[id] == "source_manifest" {
			if out.source, err = source.DecodeManifest(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	index := map[string]string{}
	for _, en := range out.source.Entries {
		if en.Kind == "file" && en.ExcludedReason == "" {
			index[en.Path] = en.SHA256
		}
	}
	lookup := func(p string) (string, bool) { s, ok := index[p]; return s, ok }
	out.docs = map[string]obsdoc.Document{}
	for _, task := range out.manifest.Tasks {
		for _, a := range task.Artifacts {
			body := fa.artifacts[clientID(fa, a.ArtifactID)]
			switch {
			case a.Role == analysis.RoleObservations && !slices.Contains([]string{"go-vet", "go-vet.m01"}, task.Key):
				d, err := obsdoc.Decode(body, obsdoc.Expect{TaskKey: task.Key, Snapshot: out.manifest.Snapshot.ManifestDigest, Lookup: lookup})
				if err != nil {
					t.Fatalf("%s envelope: %v", task.Key, err)
				}
				out.docs[task.Key] = d
			case a.MediaType == envdoc.MediaType:
				var d envdoc.Document
				if json.Unmarshal(body, &d) == nil {
					out.envs = append(out.envs, d)
				}
			}
		}
	}
	return out
}

// clientID maps a manifest (server) artifact id back to the fake agent's
// client id.
func clientID(fa *fakeAgent, artifactID string) string {
	return fa.serverIDs[artifactID]
}

func taskRows(m analysis.ResultManifest) map[string]string {
	out := map[string]string{}
	for _, task := range m.Tasks {
		r := ""
		if task.Reason != nil {
			r = *task.Reason
		}
		out[task.Key] = string(task.Status) + "/" + r
	}
	return out
}

func analyzerTools(envs []envdoc.Document) []envdoc.AnalyzerTool {
	for _, d := range envs {
		if d.Environment != nil {
			return d.Environment.AnalyzerTools
		}
	}
	return nil
}

// TC-69 visibility in CI conditions (no staticcheck): the staticcheck task is
// planned, finishes unsupported/tool_not_configured with a not-run envelope,
// env.discover records it not_probed, and go vet is unchanged.
func TestAnalyzerPackWithoutStaticcheck(t *testing.T) {
	r := runPack(t, nil)
	rows := taskRows(r.manifest)
	if rows["go-vet"] != "succeeded/" || rows["staticcheck"] != "unsupported/tool_not_configured" || rows["env.discover"] != "succeeded/" {
		t.Fatalf("tasks %v", rows)
	}
	d, ok := r.docs["staticcheck"]
	if !ok || d.Outcome.ExitClass != obsdoc.ExitNotRun || len(d.Executions) != 0 || len(d.Coverage.ProcessedPaths) != 0 {
		t.Fatalf("staticcheck envelope %+v", d)
	}
	for _, task := range r.manifest.Tasks {
		if task.Key == "staticcheck" && (task.Required || len(task.Executions) != 0) {
			t.Fatalf("staticcheck manifest task %+v", task)
		}
	}
	at := analyzerTools(r.envs)
	if len(at) != 1 || at[0].Tool != "staticcheck" || at[0].Status != envdoc.ObsNotProbed {
		t.Fatalf("analyzer tools %+v", at)
	}
}

// The real e2e: with --staticcheck (CODEYE_TEST_STATICCHECK) both engines
// run on the same snapshot copy; staticcheck exits 1 with findings and the
// task succeeds (TC-07), its executions are recorded by the worker and the
// envelope binds them.
func TestAnalyzerPackWithRealStaticcheck(t *testing.T) {
	sc := os.Getenv("CODEYE_TEST_STATICCHECK")
	if sc == "" {
		t.Skip("set CODEYE_TEST_STATICCHECK to the staticcheck binary")
	}
	r := runPack(t, map[string]string{"staticcheck": sc})
	rows := taskRows(r.manifest)
	if rows["go-vet"] != "succeeded/" || rows["staticcheck"] != "succeeded/" {
		t.Fatalf("tasks %v", rows)
	}
	var names []string
	for _, task := range r.manifest.Tasks {
		if task.Key != "staticcheck" {
			continue
		}
		for _, e := range task.Executions {
			names = append(names, e.Name)
			if e.Name == "staticcheck.check" && (e.ExitCode == nil || *e.ExitCode != 1 || e.StdoutArtifactID == nil) {
				t.Fatalf("staticcheck.check %+v", e)
			}
		}
	}
	if !slices.Equal(names, []string{"go.version", "staticcheck.version", "go.modedit", "go.list", "staticcheck.check"}) {
		t.Fatalf("executions %v", names)
	}
	d := r.docs["staticcheck"]
	rules := map[string]bool{}
	for _, o := range d.Observations {
		rules[o.RuleID] = true
	}
	if !rules["SA1000"] || d.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !slices.Equal(d.Coverage.ProcessedPaths, []string{"lib/lib.go", "main.go"}) {
		t.Fatalf("staticcheck envelope rules %v outcome %+v coverage %+v", rules, d.Outcome, d.Coverage)
	}
	at := analyzerTools(r.envs)
	if len(at) != 1 || at[0].Status != envdoc.ObsObserved || at[0].Version == nil || at[0].Execution == nil {
		t.Fatalf("analyzer tools %+v", at)
	}
	t.Logf("staticcheck %s: %d observations, rules %v", *at[0].Version, len(d.Observations), rules)
}
