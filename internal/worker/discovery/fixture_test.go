package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

var updateExamples = flag.Bool("update-examples", false, "rewrite examples/environment*.json from the W15 fixture")

const (
	fixtureInput    = "../../../fixtures/w15-modules/input"
	fixtureExpected = "../../../fixtures/w15-modules/expected.json"
)

// volatile fields depend on the acquisition (digest), the attempt, or the
// checkout's line endings (blob hashes and sizes); expected.json omits them
// and the test checks them against the manifest instead.
var volatile = map[string]bool{"snapshot_manifest_digest": true, "run_id": true, "attempt_id": true, "sha256": true, "size_bytes": true, "evidence_id": true, "read_bytes": true}

func project(t *testing.T, doc envdoc.Document) any {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var strip func(any) any
	strip = func(x any) any {
		switch x := x.(type) {
		case map[string]any:
			for k := range x {
				if volatile[k] {
					delete(x, k)
					continue
				}
				x[k] = strip(x[k])
			}
		case []any:
			for i := range x {
				x[i] = strip(x[i])
			}
		}
		return x
	}
	return strip(v)
}

type fixtureExpect struct {
	Description string            `json:"description"`
	Observation map[string]string `json:"injected_node_observation"`
	Environment any               `json:"environment"`
	Documents   any               `json:"documents"`
	GoVet       []struct {
		Module string `json:"module_path"`
		Key    string `json:"task_key"`
		Status string `json:"task_status"`
		Reason string `json:"reason"`
	} `json:"go_vet_with_local_toolchain"`
}

func readExpected(t *testing.T) fixtureExpect {
	t.Helper()
	b, err := os.ReadFile(fixtureExpected)
	if err != nil {
		t.Fatal(err)
	}
	var e fixtureExpect
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatal(err)
	}
	return e
}

func fixtureJob(t *testing.T, scope domain.Scope) analysis.Job {
	t.Helper()
	src, err := filepath.Abs(fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	return snapshotOf(t, src, scope)
}

// runFixture runs both discovery tasks through the handler, as the worker does.
func runFixture(t *testing.T, j analysis.Job, obs Observer, runner executor.Runner) (envdoc.Document, envdoc.Document) {
	t.Helper()
	h, err := Wrap(noopHandler{}, Config{Observer: obs})
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		runner = &fakeRunner{result: func(executor.Spec) executor.Result { t.Fatal("no process expected"); return executor.Result{} }}
	}
	var out [2]envdoc.Document
	for i, task := range Tasks() {
		res := h.Run(context.Background(), j, task, runner)
		if res.Status != domain.TaskSucceeded || len(res.Artifacts) != 1 {
			t.Fatalf("%s: %+v", task.Key, res)
		}
		b, err := os.ReadFile(res.Artifacts[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		out[i], err = envdoc.Decode(b, input(j).Expect(""))
		if err != nil {
			t.Fatalf("%s: %v", task.Key, err)
		}
		if res.Artifacts[0].MediaType != envdoc.MediaType || res.Artifacts[0].Role != "other" {
			t.Fatalf("artifact: %+v", res.Artifacts[0])
		}
	}
	return out[0], out[1]
}

// TC-96/TC-97 first input (docs/TESTING.md): module, requirement, verdict and
// document facts with an injected node observation (Go 1.27.4), so the
// result does not depend on the CI toolchain.
func TestW15FixtureMatchesExpected(t *testing.T) {
	want := readExpected(t)
	j := fixtureJob(t, domain.Scope{Kind: "full"})
	env, docs := runFixture(t, j, StaticObserver{Versions: want.Observation}, nil)
	for name, pair := range map[string][2]any{"environment": {project(t, env), want.Environment}, "documents": {project(t, docs), want.Documents}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			got, _ := json.MarshalIndent(pair[0], "", "  ")
			t.Errorf("%s document differs from expected.json; got:\n%s", name, got)
		}
	}
	// Volatile fields are bound to this snapshot's manifest.
	files := included(j.Snapshot.Manifest)
	for _, r := range env.Environment.Requirements {
		e := files[r.Source.Path]
		if r.Source.SHA256 != e.SHA256 || r.EvidenceID != record.ExcerptID(j.Snapshot.Digest, r.Source.Path, e.SHA256, r.Source.StartLine, r.Source.EndLine) {
			t.Fatalf("requirement %s is not bound to the snapshot", r.ID)
		}
	}
	var read int64
	for _, d := range docs.Documents.Items {
		e := files[d.Path]
		if d.SHA256 == nil || *d.SHA256 != e.SHA256 || *d.SizeBytes != *e.Size {
			t.Fatalf("document %s is not bound to the snapshot", d.Path)
		}
		if d.Status == envdoc.DocSelected {
			read += *d.SizeBytes
		}
	}
	if docs.Documents.Budget.ReadBytes != read || env.Snapshot != j.Snapshot.Digest || docs.RunID != j.RunID {
		t.Fatalf("budget or identity: %+v", docs.Documents.Budget)
	}
	// The hostile agent instructions are classified as data and never copied.
	for _, doc := range []envdoc.Document{env, docs} {
		b, _ := json.Marshal(doc)
		for _, s := range []string{"OPENAI_API_KEY", "curl", "allow", "retry", "never retry"} {
			if strings.Contains(strings.ToLower(string(b)), strings.ToLower(s)) {
				t.Fatalf("document text %q leaked into the artifact", s)
			}
		}
	}
}

// The go vet outcome on the real local toolchain agrees with the discovery
// verdicts: a module whose go directive the node satisfies is vetted, the
// one that requires a newer Go fails as toolchain_unavailable without any
// download (GOTOOLCHAIN=local, GOPROXY=off).
func TestW15FixtureGoVetAgreesWithVerdicts(t *testing.T) {
	goExe := testGo(t)
	want := readExpected(t)
	j := fixtureJob(t, domain.Scope{Kind: "full"})
	h, err := Wrap(noopHandler{}, Config{Observer: ProbeObserver{GoExecutable: goExe}})
	if err != nil {
		t.Fatal(err)
	}
	res := h.Run(context.Background(), j, Tasks()[0], &executor.Executor{})
	if res.Status != domain.TaskSucceeded || len(res.Executions) != 1 || res.Executions[0].Name != ExecGoVersion {
		t.Fatalf("env.discover with the real toolchain: %+v", res)
	}
	b, _ := os.ReadFile(res.Artifacts[0].Path)
	env, err := envdoc.Decode(b, input(j).Expect(envdoc.KindEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	local := *env.Environment.Observations[0].Version
	if envdoc.Decide("go_directive", "1.30", local) != envdoc.VerdictMismatch || envdoc.Decide("go_directive", "1.22", local) != envdoc.VerdictSatisfied {
		t.Skipf("local Go %s does not reproduce the fixture's node (needs >=1.22 and <1.30)", local)
	}
	verdicts := map[string]string{}
	for _, v := range env.Environment.Verdicts {
		if v.Requirement != nil && strings.HasPrefix(*v.Requirement, "go_directive@") {
			verdicts[strings.TrimPrefix(v.Module, "go:")] = v.Status
		}
	}
	vet, err := govet.New(govet.Config{GoExecutable: goExe})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := vet.Plan(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]analysis.TaskSpec{}
	for _, p := range plan {
		keys[p.Key] = p
		if p.Stage != domain.StageStaticAnalysis {
			t.Fatalf("go-vet task %s stage %q", p.Key, p.Stage)
		}
	}
	for _, g := range want.GoVet {
		task, ok := keys[g.Key]
		if !ok {
			t.Fatalf("go-vet task %s not planned: %+v", g.Key, plan)
		}
		r := vet.Run(context.Background(), j, task, &executor.Executor{})
		if string(r.Status) != g.Status || r.Reason != g.Reason {
			t.Fatalf("go vet %s (%s): %s/%s, want %s/%s", g.Module, g.Key, r.Status, r.Reason, g.Status, g.Reason)
		}
		agree := (verdicts[g.Module] == envdoc.VerdictSatisfied) == (r.Status == domain.TaskSucceeded)
		mismatchAgrees := verdicts[g.Module] != envdoc.VerdictMismatch || r.Reason == govet.ReasonToolchain
		if !agree || !mismatchAgrees {
			t.Fatalf("go vet %s %s/%s disagrees with verdict %s", g.Module, r.Status, r.Reason, verdicts[g.Module])
		}
		t.Logf("go vet %s with local go %s: %s/%s, verdict %s", g.Module, local, r.Status, r.Reason, verdicts[g.Module])
	}
	// Nothing was downloaded into any per-task module cache.
	_ = filepath.WalkDir(filepath.Join(j.Scratch, "go-vet"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(filepath.ToSlash(p), "/pkg/mod/") {
			t.Fatalf("module cache received a file: %s", p)
		}
		return nil
	})
}

// examples/environment.json and examples/environment-documents.json are the
// fixture's documents with fixed identities and a fixed manifest digest
// (blob hashes from an LF checkout).
func TestExamplesMatchFixture(t *testing.T) {
	want := readExpected(t)
	j := fixtureJob(t, domain.Scope{Kind: "full"})
	files := included(j.Snapshot.Manifest)
	for _, e := range files {
		b, err := os.ReadFile(filepath.Join(fixtureInput, filepath.FromSlash(e.Path)))
		if err != nil || bytes.Contains(b, []byte("\r\n")) {
			t.Skip("fixture checkout has CRLF line endings; examples are recorded from LF bytes")
		}
	}
	j.Snapshot.Digest = strings.Repeat("5", 64)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000015", "0195f3a0-0000-7000-8000-000000000016"
	// The examples show a production artifact: the node observation comes
	// from the go.version probe (scripted here to report go1.27.4).
	probe := &fakeRunner{result: func(executor.Spec) executor.Result {
		return exited(0, "go version go"+want.Observation["go"]+" linux/amd64\n")
	}}
	env, docs := runFixture(t, j, ProbeObserver{GoExecutable: fakeGo(t, 0o755)}, probe)
	for name, doc := range map[string]envdoc.Document{"../../../examples/environment.json": env, "../../../examples/environment-documents.json": docs} {
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		if *updateExamples {
			if err := os.WriteFile(name, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), raw) {
			t.Fatalf("%s is stale; run go test ./internal/worker/discovery -run TestExamplesMatchFixture -args -update-examples", name)
		}
	}
}
