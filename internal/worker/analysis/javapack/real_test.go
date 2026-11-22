package javapack

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const javaSource = `package example;
public class Example {
    public int run() {
        int neverUsed = 1;
        String input = null;
        return input.length();
    }
}
`

func TestRealJavaAnalyzers(t *testing.T) {
	java := os.Getenv("CODEYE_TEST_JAVA")
	if java == "" {
		t.Skip("set CODEYE_TEST_JAVA and the analyzer JAR paths")
	}
	release := os.Getenv("CODEYE_TEST_JAVA_RELEASE")
	if release == "" {
		release = "21"
	}
	for _, spec := range []struct {
		name, env, rule string
	}{
		{PMD, "CODEYE_TEST_PMD", "UnusedLocalVariable"},
		{SpotBugs, "CODEYE_TEST_SPOTBUGS", "NP_ALWAYS_NULL"},
	} {
		t.Run(spec.name, func(t *testing.T) {
			jar := os.Getenv(spec.env)
			if jar == "" {
				t.Skipf("set %s to the installed JAR", spec.env)
			}
			fixture := packtest.NewFixture(t, "", map[string]string{
				"pom.xml":                                "<project><groupId>example</groupId><artifactId>parent</artifactId><version>1</version><modules><module>app</module></modules><properties><maven.compiler.release>" + release + "</maven.compiler.release></properties></project>",
				"app/pom.xml":                            "<project><parent><groupId>example</groupId><artifactId>parent</artifactId><version>1</version></parent></project>",
				"app/src/main/java/example/Example.java": javaSource,
				"app/target/classes/example/Example.class": "not a class file",
				"outside/Outside.java":                     "this file is outside the selected scope",
			}, domain.Scope{Kind: "paths", Paths: []string{"app"}})
			runner := &executor.Executor{}
			analyzer, err := New(spec.name, jar, pack.Toolchain{Executable: java, Version: release})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := analyzer.Probe(context.Background(), runner, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			sourceBefore := packtest.TreeHash(t, fixture.SourceRoot)
			snapshotBefore := packtest.TreeHash(t, fixture.Job.Snapshot.SnapshotDir)
			workspaceBefore := packtest.TreeHash(t, fixture.Job.Snapshot.WorkspaceDir)
			result := packtest.RunTask(t, analyzer, fixture.Job, spec.name+".m01", runner)
			doc, body := packtest.ReadEnvelope(t, fixture.Job, result, "CODEYE_JAVA_DUMP")
			if result.Status != domain.TaskSucceeded || packtest.Rules(doc)[spec.rule] == 0 || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"app/src/main/java/example/Example.java"}) {
				t.Fatalf("unexpected %s result %+v\n%s", spec.name, result, body)
			}
			if packtest.TreeHash(t, fixture.SourceRoot) != sourceBefore || packtest.TreeHash(t, fixture.Job.Snapshot.SnapshotDir) != snapshotBefore ||
				packtest.TreeHash(t, fixture.Job.Snapshot.WorkspaceDir) != workspaceBefore {
				t.Fatal("Java analyzer modified source or workspace")
			}
		})
	}
}
