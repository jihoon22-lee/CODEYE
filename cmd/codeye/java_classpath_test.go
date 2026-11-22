package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

func TestJavaClasspathCatalogAgentArgv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "classpath.json")
	stateDir := t.TempDir()
	body, err := json.Marshal(map[string]any{"schema_version": "0.1", "state_dir": stateDir,
		"agent": map[string]string{"java_classpath_catalog": path}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := instance.Parse(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := agentArgv(cfg, instance.Layout{Root: stateDir}, nil, false)
	if at := slices.Index(args, "--java-classpath-catalog"); at < 0 || at+1 >= len(args) || args[at+1] != path {
		t.Fatalf("missing configured Java classpath catalog: %v", args)
	}
}

func TestWorkerHandlersUseJavaClasspathOnPublicProjects(t *testing.T) {
	java, spotbugs := os.Getenv("CODEYE_TEST_JAVA"), os.Getenv("CODEYE_TEST_SPOTBUGS")
	catalog := os.Getenv("CODEYE_TEST_JAVA_CLASSPATH_CATALOG")
	if java == "" || spotbugs == "" || catalog == "" {
		t.Skip("set CODEYE_TEST_JAVA, CODEYE_TEST_SPOTBUGS and CODEYE_TEST_JAVA_CLASSPATH_CATALOG")
	}
	for _, tc := range []struct {
		project, sourceEnv string
		status             domain.TaskStatus
		verified           int
		largeTestResources bool
	}{
		{"commons-text", "CODEYE_TEST_COMMONS_TEXT", domain.TaskUnsupported, 1, false},
		{"commons-csv", "CODEYE_TEST_COMMONS_CSV", domain.TaskSucceeded, 2, true},
	} {
		t.Run(tc.project, func(t *testing.T) {
			project := os.Getenv(tc.sourceEnv)
			if project == "" {
				t.Skipf("set %s to the Apache release 1.13.0 checkout", tc.sourceEnv)
			}
			limits := source.DefaultLimits()
			if tc.largeTestResources {
				limits.MaxFileBytes = 64 << 20 // The checkout includes a 33 MiB CSV performance fixture.
			}
			runPublicJavaClasspath(t, java, spotbugs, project, catalog, limits, tc.status, tc.verified)
		})
	}
}

func runPublicJavaClasspath(t *testing.T, java, spotbugs, project, catalog string, limits source.Limits, status domain.TaskStatus, verified int) {
	t.Helper()
	ctx := context.Background()
	runner := &executor.Executor{}
	scratch := t.TempDir()
	hs, _, err := workerHandlers(ctx, &workerproto.Hello{
		ScratchRoot: scratch, JavaClasspathCatalog: catalog, AdvertiseWithoutBaseline: true,
		AnalyzerTools: map[string]string{"java": java, "spotbugs": spotbugs},
	}, runner)
	if err != nil || len(hs) != 2 {
		t.Fatalf("configure worker handlers: %v (%d handlers)", err, len(hs))
	}
	data := t.TempDir()
	acquired, err := source.AcquireLocal(ctx, project, ".", data, limits)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Load(ctx, acquired.Root, acquired.Digest)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.WorkspaceDir = filepath.Join(data, "attempt-workspace")
	if err := source.CloneWorkspace(ctx, snapshot, snapshot.WorkspaceDir); err != nil {
		t.Fatal(err)
	}
	job := analysis.Job{
		RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Profile: "deep-review",
		Scope: domain.Scope{Kind: "paths", Paths: []string{"src/main/java"}}, Snapshot: snapshot, Scratch: scratch, SourceDir: project,
	}
	plan, err := hs[0].Plan(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan {
		if task.Key != "spotbugs" {
			continue
		}
		result := hs[0].Run(ctx, job, task, runner)
		doc, body := packtest.ReadEnvelope(t, job, result, "CODEYE_JAVA_CLASSPATH_DUMP")
		packtest.AssertNoNodePaths(t, body, project, catalog, scratch)
		if result.Status != status || doc.Coverage.Units["java_classpath_verified"] != verified || doc.Coverage.Units["java_classpath_unresolved"] != 0 {
			t.Fatalf("unexpected result for public Java sources: %+v\n%s", result, body)
		}
		if status == domain.TaskSucceeded && len(doc.Coverage.ProcessedPaths) == 0 {
			t.Fatalf("SpotBugs did not process any sources: %s", body)
		}
		if status == domain.TaskUnsupported && (result.Reason != "java_compile_input_limit" || doc.Coverage.Units["java_input_excluded"] != 1) {
			t.Fatalf("expected one source outside the encoding contract: %+v\n%s", result, body)
		}
		t.Logf("SpotBugs %s %s, verified %d JARs, processed %d sources, compiled %d classes and reported %d observations",
			result.Status, result.Reason, verified, len(doc.Coverage.ProcessedPaths), doc.Coverage.Units["java_compiled_classes"], len(doc.Observations))
		return
	}
	t.Fatal("SpotBugs task missing")
}
