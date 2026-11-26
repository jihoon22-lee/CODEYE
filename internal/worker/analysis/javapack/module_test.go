package javapack

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// javacRecorder answers the javac execution with a compile failure and keeps
// the source arguments of its argument file; it runs nothing.
type javacRecorder struct {
	sources []string
	runs    int
}

func (r *javacRecorder) Run(_ context.Context, spec executor.Spec) executor.Result {
	code := 1
	res := executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: []string{filepath.Base(spec.Executable)}, Dir: analysis.RedactedMarker}}
	if strings.TrimSuffix(filepath.Base(spec.Executable), ".exe") != "javac" {
		return res
	}
	r.runs++
	for _, a := range spec.Args {
		if file, ok := strings.CutPrefix(a, "@"); ok {
			b, _ := os.ReadFile(file)
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasSuffix(line, `.java"`) {
					r.sources = append(r.sources, strings.Trim(line, `"`))
				}
			}
		}
	}
	return res
}

func fakeJDK(t *testing.T) (java, jar string) {
	t.Helper()
	dir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, n := range []string{"java", "javac"} {
		if err := os.WriteFile(filepath.Join(dir, n+ext), []byte("not executed\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	jar = filepath.Join(dir, "spotbugs.jar")
	if err := os.WriteFile(jar, []byte("not executed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "java"+ext), jar
}

// TC-323: a module descriptor in the production sources is not passed to
// javac, so the classes compile in the unnamed module with the classpath;
// with nothing but a descriptor there is nothing to compile.
func TestSpotBugsCompilesWithoutTheModuleDescriptor(t *testing.T) {
	pom := "<project><groupId>example</groupId><artifactId>app</artifactId><version>1</version><properties><maven.compiler.release>17</maven.compiler.release></properties></project>"
	for _, c := range []struct {
		name    string
		files   map[string]string
		runs    int
		sources []string
		status  domain.TaskStatus
		reason  string
	}{
		{"descriptor and classes", map[string]string{
			"pom.xml": pom, "src/main/java/module-info.java": "module example { exports example; }\n",
			"src/main/java/example/Example.java": javaSource,
		}, 1, []string{"./src/main/java/example/Example.java"}, domain.TaskFailed, "java_compilation_failed"},
		{"only a descriptor", map[string]string{
			"pom.xml": pom, "src/main/java/module-info.java": "module example { }\n",
		}, 0, nil, domain.TaskUnsupported, "java_production_sources_unselected"},
	} {
		t.Run(c.name, func(t *testing.T) {
			java, jar := fakeJDK(t)
			analyzer, err := New(SpotBugs, jar, pack.Toolchain{Executable: java, Version: "21"})
			if err != nil {
				t.Fatal(err)
			}
			analyzer.version = "4.10.5"
			fixture := packtest.NewFixture(t, "", c.files, domain.Scope{})
			runner := &javacRecorder{}
			result := packtest.RunTask(t, analyzer, fixture.Job, SpotBugs, runner)
			doc, body := packtest.ReadEnvelope(t, fixture.Job, result, "CODEYE_JAVA_DUMP")
			if result.Status != c.status || result.Reason != c.reason || runner.runs != c.runs || !slices.Equal(runner.sources, c.sources) {
				t.Fatalf("result %s/%s javac runs %d sources %v\n%s", result.Status, result.Reason, runner.runs, runner.sources, body)
			}
			if c.runs > 0 && (doc.Coverage.Units["java_module_descriptors_not_compiled"] != 1 || !slices.Contains(doc.Limitations, "java_module_descriptor_not_compiled")) {
				t.Fatalf("units %v limitations %v", doc.Coverage.Units, doc.Limitations)
			}
		})
	}
}
