package javapack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
)

const externalCoordinate = "org.apache.commons:commons-lang3:3.17.0"

func TestLocalJarValidation(t *testing.T) {
	coordinate, _ := javaproject.ParseCoordinate(externalCoordinate)
	properties := "groupId=org.apache.commons\nartifactId=commons-lang3\nversion=3.17.0\n"
	for _, tc := range []struct {
		name, manifest, metadata, class string
		want                            bool
	}{
		{"trusted", "Manifest-Version: 1.0\r\n", properties, "\xca\xfe\xba\xbe", true},
		{"external-manifest-classpath", "Manifest-Version: 1.0\r\nCLASS-PATH: \r\n https://invalid.example/path.jar\r\n", properties, "\xca\xfe\xba\xbe", false},
		{"manifest-with-cr-newlines", "Manifest-Version: 1.0\rClass-Path: extra.jar\r", properties, "\xca\xfe\xba\xbe", false},
		{"wrong-version", "", strings.ReplaceAll(properties, "3.17.0", "3.18.0"), "\xca\xfe\xba\xbe", false},
		{"foreign-metadata", "", properties, "\xca\xfe\xba\xbe", false},
		{"wrong-type", "", properties, "text", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			z := zip.NewWriter(&buf)
			metadataPath := "META-INF/maven/org.apache.commons/commons-lang3/pom.properties"
			if tc.name == "foreign-metadata" {
				metadataPath = "META-INF/maven/example/foreign/pom.properties"
			}
			for name, value := range map[string]string{
				"META-INF/MANIFEST.MF": tc.manifest,
				metadataPath:           tc.metadata,
				"org/apache/commons/lang3/StringUtils.class": tc.class,
			} {
				w, err := z.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte(value)); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			if _, ok := jarTypes(buf.Bytes(), coordinate); ok != tc.want {
				t.Fatalf("JAR accepted = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestRealSpotBugsLocalClasspath(t *testing.T) {
	java, spotbugs, jar := os.Getenv("CODEYE_TEST_JAVA"), os.Getenv("CODEYE_TEST_SPOTBUGS"), os.Getenv("CODEYE_TEST_JAVA_EXTERNAL_JAR")
	if java == "" || spotbugs == "" || jar == "" {
		t.Skip("set CODEYE_TEST_JAVA, CODEYE_TEST_SPOTBUGS and CODEYE_TEST_JAVA_EXTERNAL_JAR (Commons Lang 3.17.0)")
	}
	b, err := os.ReadFile(jar)
	if err != nil {
		t.Fatal(err)
	}
	digest := obsdoc.SHA256Hex(b)
	runner := &executor.Executor{}
	for _, tc := range []struct {
		name, coordinate, origin string
		corrupt, fromProject     bool
	}{
		{name: "maven", coordinate: externalCoordinate, origin: "mirror"},
		{name: "gradle", coordinate: externalCoordinate, origin: "catalog"},
		{name: "version_mismatch", coordinate: "org.apache.commons:commons-lang3:3.18.0"},
		{name: "checksum_mismatch", coordinate: externalCoordinate, corrupt: true},
		{name: "original_project", coordinate: externalCoordinate, fromProject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"src/main/java/example/Service.java": `package example;
import org.apache.commons.lang3.StringUtils;
public final class Service {
    public int run(String value) {
        if (StringUtils.isBlank(value)) {
            return 0;
        }
        String broken = null;
        return broken.length();
    }
}
`}
			if tc.name == "gradle" {
				files["build.gradle.kts"] = "plugins { java }\ndependencies {\n    implementation(\"" + externalCoordinate + "\")\n}\n"
			} else {
				files["pom.xml"] = `<project>
  <groupId>example</groupId><artifactId>app</artifactId><version>1</version>
  <dependencies><dependency><groupId>org.apache.commons</groupId><artifactId>commons-lang3</artifactId><version>3.17.0</version></dependency></dependencies>
</project>`
			}
			fixture := packtest.NewFixture(t, "", files, domain.Scope{})
			fixture.Job.SourceDir = fixture.SourceRoot
			classpathDir := t.TempDir()
			local := jar
			if tc.origin == "mirror" {
				local = filepath.Join(classpathDir, "org/apache/commons/commons-lang3/3.17.0/commons-lang3-3.17.0.jar")
			}
			if tc.fromProject {
				local = filepath.Join(fixture.SourceRoot, "dependency.jar")
			}
			if local != jar {
				if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(local, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a := catalogArtifact{Coordinate: tc.coordinate, Path: local, SHA256: digest}
			if tc.corrupt {
				a.SHA256 = strings.Repeat("0", 64)
			}
			catalog := classpathCatalog{Artifacts: []catalogArtifact{a}}
			if tc.origin == "mirror" {
				catalog.MavenRepository = classpathDir
				catalog.Artifacts[0].Path = ""
			}
			cfg, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := New(SpotBugs, spotbugs, pack.Toolchain{Executable: java})
			if err != nil {
				t.Fatal(err)
			}
			instance.ClasspathCatalog = filepath.Join(classpathDir, "catalog.json")
			if err := os.WriteFile(instance.ClasspathCatalog, cfg, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := instance.Probe(context.Background(), runner, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			result := packtest.RunTask(t, instance, fixture.Job, SpotBugs, runner)
			doc, body := packtest.ReadEnvelope(t, fixture.Job, result, "")
			if tc.origin == "" {
				if result.Status != domain.TaskFailed || result.Reason != "java_compilation_failed" ||
					doc.Coverage.Units["java_classpath_unresolved"] != 1 || doc.Coverage.Units["java_classpath_verified"] != 0 {
					t.Fatalf("unverified dependency unexpectedly used: %+v\n%s", result, body)
				}
			} else if result.Status != domain.TaskSucceeded || packtest.Rules(doc)["NP_ALWAYS_NULL"] == 0 ||
				doc.Coverage.Units["java_classpath_"+tc.origin+"_verified"] != 1 ||
				doc.Coverage.Units["java_classpath_unresolved"] != 0 || len(doc.Target.Properties["java_classpath_digest"]) != 64 {
				t.Fatalf("external dependency not used: %+v\n%s", result, body)
			}
		})
	}
}
