package javapack

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// TC-324: a SpotBugs report in a source compiled only as context (outside
// the selected paths) is counted as outside the scope, not as a report
// without a location, and gives no observation.
func TestRealSpotBugsCountsReportsOutsideTheScope(t *testing.T) {
	java, jar := os.Getenv("CODEYE_TEST_JAVA"), os.Getenv("CODEYE_TEST_SPOTBUGS")
	if java == "" || jar == "" {
		t.Skip("set CODEYE_TEST_JAVA and CODEYE_TEST_SPOTBUGS")
	}
	other := strings.ReplaceAll(javaSource, "class Example", "class Other")
	fixture := packtest.NewFixture(t, "", map[string]string{
		"pom.xml":                            "<project><groupId>example</groupId><artifactId>app</artifactId><version>1</version><properties><maven.compiler.release>17</maven.compiler.release></properties></project>",
		"src/main/java/example/Example.java": javaSource,
		"src/main/java/example/Other.java":   other,
	}, domain.Scope{Kind: "paths", Paths: []string{"src/main/java/example/Example.java"}})
	runner := &executor.Executor{}
	analyzer, err := New(SpotBugs, jar, pack.Toolchain{Executable: java, Version: "21"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.Probe(context.Background(), runner, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	result := packtest.RunTask(t, analyzer, fixture.Job, SpotBugs, runner)
	doc, body := packtest.ReadEnvelope(t, fixture.Job, result, "CODEYE_JAVA_DUMP")
	if result.Status != domain.TaskSucceeded || doc.Coverage.Units["java_findings_outside_scope"] == 0 || !slices.Contains(doc.Limitations, "observations_outside_scope_omitted") ||
		doc.Coverage.Units["java_findings_location_unmapped"] != 0 || packtest.Rules(doc)["NP_ALWAYS_NULL"] != 1 {
		t.Fatalf("result %+v\n%s", result, body)
	}
	for _, o := range doc.Observations {
		if o.Path == nil || *o.Path != "src/main/java/example/Example.java" {
			t.Fatalf("observation outside the scope: %+v", o)
		}
	}
}
