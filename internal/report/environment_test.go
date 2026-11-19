package report

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// addDiscovery appends succeeded env.discover/docs.discover tasks with the
// artifacts the discovery worker uploads. edit may alter the environment document.
func addDiscovery(t *testing.T, f *fixture, edit func(*envdoc.Document), probe *analysis.ManifestExecution) {
	t.Helper()
	m, err := source.DecodeManifest(f.in.SourceManifest)
	if err != nil {
		t.Fatal(err)
	}
	in := discovery.Input{Manifest: m, Digest: f.in.Snapshot.ManifestDigest, Scope: domain.Scope{Kind: "full"}, RunID: f.runID, AttemptID: f.attempt}
	read := func(e source.Entry) ([]byte, error) { return []byte("module example.test/probe\n\ngo 1.27\n"), nil }
	versions := map[string]string{}
	if probe != nil {
		versions["go"] = "1.27.4"
	}
	env, _, err := discovery.Environment(in, read, func(tools []string) discovery.Observed {
		o := discovery.StaticObserver{Versions: versions}.Observe(context.Background(), analysis.Job{}, tools, nil)
		for i := range o.Observations {
			if probe != nil && o.Observations[i].Tool == "go" {
				name := probe.Name
				o.Observations[i].Execution = &name
			}
		}
		return o
	})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := discovery.Documents(in, discovery.DefaultBudget, read)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&env)
	}
	var rm analysis.ResultManifest
	if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &rm); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []envdoc.Document{env, docs} {
		raw, _ := json.Marshal(doc)
		body, err := domain.CanonicalJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		key := envdoc.TaskEnvironment
		if doc.Kind == envdoc.KindDocuments {
			key = envdoc.TaskDocuments
		}
		id := domain.NewID()
		f.in.Tasks = append(f.in.Tasks, TaskFacts{ID: id, Key: key, Status: "succeeded", Required: true, ExecutionOrdinal: 1})
		a := ArtifactRef{ID: domain.NewID(), RunID: f.runID, AttemptID: f.attempt, Role: "other", MediaType: envdoc.MediaType, SHA256: domain.Digest(body), SizeBytes: int64(len(body))}
		f.in.Artifacts = append(f.in.Artifacts, a)
		f.in.Bodies[a.ID] = body
		mt := analysis.ManifestTask{TaskID: id, Key: key, Required: true, Status: domain.TaskSucceeded, Executions: []analysis.ManifestExecution{},
			Artifacts: []analysis.ManifestArtifact{{ArtifactID: a.ID, Role: a.Role, MediaType: a.MediaType, SHA256: a.SHA256, SizeBytes: a.SizeBytes}}, Limitations: []string{}}
		if probe != nil && key == envdoc.TaskEnvironment {
			mt.Executions = append(mt.Executions, *probe)
		}
		rm.Tasks = append(rm.Tasks, mt)
	}
	f.setManifest(t, rm)
}

// The discovery tasks are required and succeed without contributing
// findings. Without a decoder the builder reports their artifacts as missing
// and unrecognized and marks the result incomplete; with EnvironmentDecoder
// the report is the same as the go-vet-only report apart from the task and
// artifact rows.
func TestDiscoveryArtifactsDecodeWithoutChangingTheReport(t *testing.T) {
	baseline := build(t, newFixture(t, goVet(t)).in, DefaultRegistry())

	f := newFixture(t, goVet(t))
	addDiscovery(t, f, nil, nil)
	r := build(t, f.in, DefaultRegistry())
	if r.Status != baseline.Status || r.QualityVerdict != baseline.QualityVerdict || !reflect.DeepEqual(r.Limitations, baseline.Limitations) ||
		!reflect.DeepEqual(r.Summary, baseline.Summary) || !reflect.DeepEqual(r.Coverage.ToolProcessedFiles, baseline.Coverage.ToolProcessedFiles) {
		t.Fatalf("discovery changed the report:\n%+v %v\nbaseline %+v %v", r.Status, r.Limitations, baseline.Status, baseline.Limitations)
	}
	decoded := 0
	for _, a := range r.Artifacts {
		if a.MediaType == envdoc.MediaType && a.Decoder != nil && *a.Decoder == "application/vnd.codeye.environment+json;schema_version=0.1" {
			decoded++
		}
	}
	if decoded != 2 || len(r.Tasks) != 3 {
		t.Fatalf("decoded=%d tasks=%d", decoded, len(r.Tasks))
	}

	// A registry without EnvironmentDecoder flags both tasks.
	old, err := NewRegistry(GoVetDecoder{}, ReviewDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	r = build(t, f.in, old)
	for _, l := range []string{LimArtifactMissing + ":" + envdoc.TaskEnvironment, LimUnrecognizedArtifact + ":" + envdoc.TaskDocuments} {
		if !has(r.Limitations, l) {
			t.Fatalf("registry without EnvironmentDecoder: %s not reported in %v", l, r.Limitations)
		}
	}
	if r.Coverage.ToolProcessedFiles != nil {
		t.Fatal("registry without EnvironmentDecoder kept processed files known")
	}
}

func TestDiscoveryArtifactRejected(t *testing.T) {
	cases := map[string]func(*envdoc.Document){
		"other attempt":  func(d *envdoc.Document) { d.AttemptID = domain.NewID() },
		"other snapshot": func(d *envdoc.Document) { d.Snapshot = domain.Digest([]byte("other")) },
		"flipped verdict": func(d *envdoc.Document) {
			v := &d.Environment.Verdicts[0]
			v.Status, v.Reason = envdoc.VerdictSatisfied, nil
		},
	}
	for name, edit := range cases {
		f := newFixture(t, goVet(t))
		addDiscovery(t, f, edit, nil)
		r := build(t, f.in, DefaultRegistry())
		if !has(r.Limitations, LimArtifactRejected+":"+envdoc.TaskEnvironment) {
			t.Fatalf("%s: not rejected: %v", name, r.Limitations)
		}
	}
	// An observed version must be backed by the task's recorded clean probe.
	code := 0
	probe := analysis.ManifestExecution{Name: "go.version", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Command: analysis.ManifestCommand{Argv: []string{"go", "version"}, Dir: analysis.RedactedMarker, EnvKeys: []string{}}, StartedAt: "2026-11-18T00:00:00Z", EndedAt: "2026-11-18T00:00:01Z"}
	f := newFixture(t, goVet(t))
	addDiscovery(t, f, nil, &probe)
	if r := build(t, f.in, DefaultRegistry()); has(r.Limitations, LimArtifactRejected) {
		t.Fatalf("backed observation rejected: %v", r.Limitations)
	}
	f = newFixture(t, goVet(t))
	addDiscovery(t, f, func(d *envdoc.Document) { d.Environment.Observations[0].Execution = nil }, &probe)
	if r := build(t, f.in, DefaultRegistry()); !has(r.Limitations, LimArtifactRejected+":"+envdoc.TaskEnvironment) {
		t.Fatalf("observation without probe accepted: %v", r.Limitations)
	}
	bad := 2
	failedProbe := probe
	failedProbe.ExitCode = &bad
	f = newFixture(t, goVet(t))
	addDiscovery(t, f, nil, &failedProbe)
	if r := build(t, f.in, DefaultRegistry()); !has(r.Limitations, LimArtifactRejected+":"+envdoc.TaskEnvironment) {
		t.Fatalf("observation from a failed probe accepted: %v", r.Limitations)
	}
}
