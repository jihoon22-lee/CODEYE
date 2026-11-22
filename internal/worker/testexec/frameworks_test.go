package testexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

func runFramework(t *testing.T, cfg Config, files map[string]string, key string, check func(resultdoc.Document, analysis.TaskResult)) {
	t.Helper()
	fx := packtest.NewFixture(t, "", files, domain.Scope{})
	job := fx.Job
	job.AllowRepositoryCode = true
	job.RunID, job.AttemptID = domain.NewID(), domain.NewID()
	runFrameworkJob(t, cfg, job, key, check)
}

func runFrameworkJob(t *testing.T, cfg Config, job analysis.Job, key string, check func(resultdoc.Document, analysis.TaskResult)) {
	t.Helper()
	p, err := plans(job)
	if err != nil || !slices.ContainsFunc(p, func(p testPlan) bool { return p.spec.Key == key }) {
		t.Fatalf("missing %s plan: %v %v", key, p, err)
	}
	hash := packtest.TreeHash(t, job.Snapshot.SnapshotDir)
	result := New(nil, cfg).Run(t.Context(), job, analysis.TaskSpec{Key: key}, &executor.Executor{})
	if result.Status == domain.TaskUnsupported && result.Reason == isolationUnavailable {
		assertNoTestRun(t, result)
		t.Skip("isolation unavailable")
	}
	if packtest.TreeHash(t, job.Snapshot.SnapshotDir) != hash {
		t.Fatal("test changed the fixed snapshot")
	}
	for _, a := range result.Artifacts {
		if a.MediaType != resultdoc.MediaType {
			continue
		}
		body, err := os.ReadFile(a.Path)
		if err != nil {
			t.Fatal(err)
		}
		last := result.Executions[len(result.Executions)-1].Result
		doc, err := resultdoc.Decode(body, resultdoc.Expect{RunID: job.RunID, AttemptID: job.AttemptID, Snapshot: job.Snapshot.Digest, TaskKey: key,
			StdoutSHA256: domain.Digest(last.Stdout.Data), StderrSHA256: domain.Digest(last.Stderr.Data)})
		if err != nil {
			t.Fatalf("bad result doc: %s %v", body, err)
		}
		check(doc, result)
		return
	}
	for _, ex := range result.Executions {
		t.Logf("%s stdout:\n%s\nstderr:\n%s", ex.Name, ex.Result.Stdout.Data, ex.Result.Stderr.Data)
	}
	t.Fatalf("missing %s artifact, task %s (%s)", key, result.Status, result.Reason)
}

func TestPytestJUnitFromIsolatedRun(t *testing.T) {
	python := availableTool(t, "python3")
	if err := exec.CommandContext(t.Context(), python, "-m", "pytest", "--version").Run(); err != nil {
		t.Skip("pytest is not installed")
	}
	runFramework(t, Config{Python: python}, map[string]string{
		"pyproject.toml": "[tool.pytest.ini_options]\n",
		"tests/test_value.py": `def test_passes():
    assert 1 + 1 == 2

def test_fails():
    assert 1 + 1 == 3
`,
	}, pythonKey, func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Runner != "pytest" || doc.Counts != (resultdoc.Counts{Passed: 1, Failed: 1}) || task.Status != domain.TaskFailed {
			t.Fatalf("pytest result: %+v, %s", doc, task.Status)
		}
		if !slices.ContainsFunc(doc.Cases, func(c resultdoc.Case) bool {
			return c.Status == resultdoc.Failed && c.Path == "tests/test_value.py" && c.Line == 4
		}) {
			t.Fatalf("pytest did not locate the failing test: %+v", doc.Cases)
		}
	})
}

func TestCTestJUnitFromIsolatedBuild(t *testing.T) {
	cmake, ctest := availableTool(t, "cmake"), availableTool(t, "ctest")
	availableTool(t, "cc")
	runFramework(t, Config{CMake: cmake, CTest: ctest}, map[string]string{
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.21)
project(case_fixture C)
enable_testing()
add_subdirectory(suite)
`,
		"suite/CMakeLists.txt": `add_executable(answer main.c)
add_test(NAME value_returns_success COMMAND answer)
add_test(NAME value_returns_failure COMMAND answer fail)
`,
		"suite/main.c": "int main(int argc, char **argv) { return argc == 1 ? 0 : 1; }\n",
	}, "test.cmake", func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Runner != "ctest" || doc.Counts != (resultdoc.Counts{Passed: 1, Failed: 1}) || task.Status != domain.TaskFailed ||
			len(task.Executions) != 4 || task.Executions[1].Name != "test.cmake-configure" || task.Executions[2].Name != "test.cmake-build" {
			t.Fatalf("CTest result: %+v, %s, %d commands", doc, task.Status, len(task.Executions))
		}
	})
}

func TestGoSubtestSelectionWithRace(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("the Go race fixture is verified on Linux x86-64")
	}
	availableTool(t, "cc")
	availableTool(t, "as")
	runFramework(t, Config{Go: filepath.Join(runtime.GOROOT(), "bin", "go"), GoRace: true, GoTestRun: "^TestChoose$/^picked_case$"}, map[string]string{
		"go.mod": "module example.test/choose\n\ngo 1.22\n",
		"choose_test.go": `package choose
import "testing"
func TestChoose(t *testing.T) {
  t.Run("picked case", func(t *testing.T) {})
  t.Run("excluded", func(t *testing.T) { t.Fatal("outside the requested subtest") })
}
`,
	}, goKey, func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Counts != (resultdoc.Counts{Passed: 1}) || task.Status != domain.TaskSucceeded ||
			len(doc.Cases) != 1 || doc.Cases[0].Name != "TestChoose/picked_case" || doc.Cases[0].Path != "choose_test.go" || doc.Cases[0].Line != 4 {
			t.Fatalf("Go selection: %+v, %s", doc, task.Status)
		}
		if !slices.Contains(task.Executions[2].Result.Command.Argv, "-race") {
			t.Fatal("did not enable the race detector")
		}
	})
}

func TestNextestWithoutDuplicatingCargoDocTests(t *testing.T) {
	cargo, nextest := os.Getenv("CODEYE_TEST_CARGO"), os.Getenv("CODEYE_TEST_NEXTEST")
	if cargo == "" || nextest == "" {
		t.Skip("set CODEYE_TEST_CARGO and CODEYE_TEST_NEXTEST for the native nextest test")
	}
	fx := packtest.NewFixture(t, "", map[string]string{
		".config/nextest.toml": "# repository opts in to nextest\n",
		"Cargo.toml":           "[package]\nname = 'case-fixture'\nversion = '0.1.0'\nedition = '2021'\n",
		"src/lib.rs":           "/// Example.\n/// ```\n/// assert_eq!(1 + 1, 2);\n/// ```\npub fn value() {}\n#[test]\nfn unit() { assert_eq!(1 + 1, 2); }\n",
	}, domain.Scope{})
	job := fx.Job
	job.AllowRepositoryCode = true
	job.RunID, job.AttemptID = domain.NewID(), domain.NewID()
	cfg := Config{Cargo: cargo, Nextest: nextest}
	runFrameworkJob(t, cfg, job, nextestKey, func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Runner != "nextest" || doc.Counts != (resultdoc.Counts{Passed: 1}) || task.Status != domain.TaskSucceeded ||
			len(doc.Cases) != 1 || !strings.HasSuffix(doc.Cases[0].Name, "unit") {
			t.Fatalf("nextest: %+v, %s", doc, task.Status)
		}
	})
	runFrameworkJob(t, cfg, job, rustKey, func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Runner != "cargo" || doc.Counts != (resultdoc.Counts{Passed: 1}) || task.Status != domain.TaskSucceeded ||
			len(doc.Cases) != 1 || !strings.Contains(doc.Cases[0].Name, "src/lib.rs") {
			t.Fatalf("Cargo doc tests: %+v, %s", doc, task.Status)
		}
	})
}

func TestJavaIgnoresAnExistingUnchangedReport(t *testing.T) {
	fx := packtest.NewFixture(t, "", map[string]string{"pom.xml": "<project/>"}, domain.Scope{})
	reportDir := filepath.Join(fx.Job.Snapshot.WorkspaceDir, "target", "surefire-reports")
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "TEST-old.xml"), []byte("<testsuite><testcase name=\"unchanged\"/></testsuite>"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := task("test.maven", ".", "Java", ".")
	p.runner = "maven"
	before, ok := javaReports(fx.Job.Snapshot.WorkspaceDir, p)
	if !ok || len(before) != 1 {
		t.Fatalf("baseline reports: %+v, %v", before, ok)
	}
	newReport := filepath.Join(reportDir, "TEST-current.xml")
	if err := os.WriteFile(newReport, []byte("<testsuite><testcase name=\"confirmed\"><failure/></testcase></testsuite>"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := javaResults(fx.Job.Snapshot.WorkspaceDir, p, before)
	if got.counts != (resultdoc.Counts{Failed: 1}) || len(got.cases) != 1 || got.cases[0].Name != "confirmed" {
		t.Fatalf("unexpected Java results: %+v", got)
	}
}

const mavenTestPOM = `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>test.codeye</groupId><artifactId>isolated-java</artifactId><version>1</version>
  <properties><maven.compiler.release>17</maven.compiler.release></properties>
  <dependencies>
    <dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>4.13.2</version><scope>test</scope></dependency>
    <dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId><version>5.13.4</version><scope>test</scope></dependency>
    <dependency><groupId>org.junit.vintage</groupId><artifactId>junit-vintage-engine</artifactId><version>5.13.4</version><scope>test</scope></dependency>
  </dependencies>
  <build><plugins>
    <plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-compiler-plugin</artifactId><version>3.14.1</version></plugin>
    <plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-surefire-plugin</artifactId><version>3.5.4</version></plugin>
    <plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-failsafe-plugin</artifactId><version>3.5.4</version><executions><execution><goals><goal>integration-test</goal><goal>verify</goal></goals></execution></executions></plugin>
  </plugins></build>
</project>`

func TestMavenSurefireAndFailsafeInOneLifecycle(t *testing.T) {
	maven, repo := os.Getenv("CODEYE_TEST_MAVEN"), os.Getenv("CODEYE_TEST_MAVEN_REPOSITORY")
	if maven == "" || repo == "" {
		t.Skip("set CODEYE_TEST_MAVEN and CODEYE_TEST_MAVEN_REPOSITORY to the tool and prepopulated offline local repository")
	}
	java := availableTool(t, "java")
	runFramework(t, Config{Java: java, Maven: maven, MavenRepository: repo}, map[string]string{
		"pom.xml": mavenTestPOM,
		"src/test/java/example/ExampleTest.java": `package example;
public class ExampleTest {
  @org.junit.Test
  public void unit() { org.junit.Assert.assertEquals(2, 1 + 1); }
}
`,
		"src/test/java/example/ExampleIT.java": `package example;
public class ExampleIT {
  @org.junit.jupiter.api.Test
  public void integration() { org.junit.jupiter.api.Assertions.assertEquals(3, 1 + 1); }
}
`,
	}, "test.maven", func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Runner != "maven" || doc.Counts != (resultdoc.Counts{Passed: 1, Failed: 1}) || task.Status != domain.TaskFailed ||
			len(task.Executions) != 2 {
			t.Fatalf("Maven result: %+v, task %s", doc, task.Status)
		}
		if !slices.ContainsFunc(doc.Cases, func(c resultdoc.Case) bool {
			return c.Status == resultdoc.Failed && c.Path == "src/test/java/example/ExampleIT.java" && c.Line == 4
		}) {
			t.Fatalf("Failsafe declaration unavailable: %+v", doc.Cases)
		}
	})
}

func TestGradleJUnitAndTestNGResults(t *testing.T) {
	gradle, jars := os.Getenv("CODEYE_TEST_GRADLE"), filepath.SplitList(os.Getenv("CODEYE_TEST_JAVA_TEST_JARS"))
	if gradle == "" || len(jars) == 0 {
		t.Skip("set CODEYE_TEST_GRADLE and CODEYE_TEST_JAVA_TEST_JARS to Gradle and local JUnit/TestNG dependencies")
	}
	files := map[string]string{
		"settings.gradle": `rootProject.name = 'fixture'
include 'junit', 'testng'
`,
		"build.gradle": `subprojects {
  apply plugin: 'java'
  dependencies { testImplementation fileTree('../jars') }
}
project(':testng') { test { useTestNG() } }
`,
		"junit/src/test/java/example/LibraryTest.java": `package example;
public class LibraryTest {
  @org.junit.Test
  public void sum() { org.junit.Assert.assertEquals(2, 1 + 1); }
}
`,
		"testng/src/test/java/example/NegativeTest.java": `package example;
public class NegativeTest {
  @org.testng.annotations.Test
  public void sum() { org.testng.Assert.assertEquals(1 + 1, 3); }
}
`,
	}
	for _, filename := range jars {
		body, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		files["jars/"+filepath.Base(filename)] = string(body)
	}
	runFramework(t, Config{Java: availableTool(t, "java"), Gradle: gradle}, files, "test.gradle", func(doc resultdoc.Document, task analysis.TaskResult) {
		if doc.Runner != "gradle" || doc.Counts != (resultdoc.Counts{Passed: 1, Failed: 1}) || task.Status != domain.TaskFailed ||
			len(task.Executions) != 2 {
			t.Fatalf("Gradle result: %+v, task %s", doc, task.Status)
		}
		if !slices.ContainsFunc(doc.Cases, func(c resultdoc.Case) bool {
			return c.Status == resultdoc.Failed && c.Path == "testng/src/test/java/example/NegativeTest.java" && c.Line == 4
		}) {
			t.Fatalf("TestNG declaration unavailable: %+v", doc.Cases)
		}
	})
}

func TestNpmFrameworkReporters(t *testing.T) {
	modules := os.Getenv("CODEYE_TEST_JS_MODULES")
	if modules == "" {
		t.Skip("set CODEYE_TEST_JS_MODULES to the npm node_modules with Jest, Vitest and Playwright installed")
	}
	node, npm := availableTool(t, "node"), availableTool(t, "npm")
	cfg := Config{Node: node, Npm: npm, ToolPath: joinPath([]string{filepath.Join(modules, ".bin")}, filepath.SplitList(os.Getenv("PATH")))}
	for _, framework := range []struct{ name, script, body string }{
		{"jest", "jest --runInBand", "test('confirmed', () => expect(2).toBe(2));\n"},
		{"vitest", "vitest run --globals", "test('confirmed', () => expect(2).toBe(2));\n"},
		{"playwright", "playwright test", "const { test, expect } = require(" + strconv.Quote(filepath.Join(modules, "@playwright", "test")) + ");\ntest('confirmed', () => expect(2).toBe(2));\n"},
	} {
		t.Run(framework.name, func(t *testing.T) {
			files := map[string]string{
				"package.json":  `{"scripts":{"test":` + strconv.Quote(framework.script) + `}}`,
				"value.test.js": framework.body,
			}
			if chrome := os.Getenv("CODEYE_TEST_CHROME"); framework.name == "playwright" && chrome != "" {
				files["playwright.config.cjs"] = "module.exports = {workers:1, use:{launchOptions:{executablePath:" +
					strconv.Quote(chrome) + "}}};\n"
				files["value.test.js"] = "const { test, expect } = require(" + strconv.Quote(filepath.Join(modules, "@playwright", "test")) + ");\n" + `
const http = require('node:http');
test('browser reads private loopback', async ({ page }) => {
  const server = http.createServer((req, res) => res.end('CODEYE'));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    await page.goto('http://127.0.0.1:' + server.address().port);
    await expect(page.locator('body')).toHaveText('CODEYE');
  } finally {
    server.close();
  }
});
`
			}
			runFramework(t, cfg, files, jsUnit, func(doc resultdoc.Document, task analysis.TaskResult) {
				if doc.Runner != framework.name || doc.Counts != (resultdoc.Counts{Passed: 1}) || task.Status != domain.TaskSucceeded ||
					len(doc.Cases) != 1 || doc.Cases[0].Path != "value.test.js" || doc.Cases[0].Line < 1 {
					report := filepath.Join(filepath.Dir(task.Executions[len(task.Executions)-1].StdoutPath), "framework-report")
					if body, err := os.ReadFile(report); err == nil {
						t.Logf("framework report: %s", body)
					}
					t.Fatalf("%s result: %+v, task %s", framework.name, doc, task.Status)
				}
			})
		})
	}
}

func TestNpmExitZeroWithoutCasesAndScriptAliases(t *testing.T) {
	node, npm := availableTool(t, "node"), availableTool(t, "npm")
	fx := packtest.NewFixture(t, "", map[string]string{
		"package.json": `{"scripts":{
			"test":"npm run test:module && npm run test:e2e",
			"test:module":"node -e ''",
			"test:e2e":"node -e ''"
		}}`,
	}, domain.Scope{})
	job := fx.Job
	job.AllowRepositoryCode = true
	planned, err := plans(job)
	if err != nil || len(planned) != 1 || planned[0].spec.Key != jsModule {
		t.Fatalf("expected one invocation for the module script and its alias: %+v %v", planned, err)
	}
	h := New(nil, Config{Node: node, Npm: npm})
	got := h.Run(t.Context(), job, planned[0].spec, &executor.Executor{})
	if got.Status == domain.TaskUnsupported && got.Reason == isolationUnavailable {
		assertNoTestRun(t, got)
		t.Skip("isolation unavailable")
	}
	if got.Status != domain.TaskUnsupported || got.Reason != "test_results_unavailable" || len(got.Artifacts) != 0 ||
		len(got.Executions) != 2 || got.Executions[1].Result.ExitCode == nil || *got.Executions[1].Result.ExitCode != 0 {
		t.Fatalf("an unstructured script exit produced test success: %s %s, %d executions, %d artifacts",
			got.Status, got.Reason, len(got.Executions), len(got.Artifacts))
	}
}

func availableTool(t *testing.T, tool string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil || !filepath.IsAbs(path) {
		t.Skipf("%s is not installed", tool)
	}
	return path
}
