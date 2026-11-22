package discovery

import (
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

func TestJavaBuildModulesAndRequirements(t *testing.T) {
	job := jobOf(t, map[string]string{
		"pom.xml": `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modules><module>core</module></modules>
  <properties><maven.compiler.release>17</maven.compiler.release></properties>
</project>`,
		"core/pom.xml":                            "<project/>",
		"core/src/main/java/Core.java":            "class Core {}",
		"client/settings.gradle.kts":              "/* include(\"fake\") */\ninclude(\":api\", \":server\")",
		"client/build.gradle.kts":                 "java {\n  toolchain {\n    languageVersion.set(JavaLanguageVersion.of(21))\n  }\n}",
		"client/api/src/main/java/Api.java":       "class Api {}",
		"client/server/build.gradle":              "sourceCompatibility = JavaVersion.VERSION_17\n",
		"client/server/src/main/java/Server.java": "class Server {}",
		"target/generated/Generated.java":         "class Generated {}",
	}, domain.Scope{Kind: "paths", Paths: []string{"client", "core"}})
	root, err := os.OpenRoot(job.Snapshot.SnapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	in := Input{Manifest: job.Snapshot.Manifest, Digest: job.Snapshot.Digest, Scope: job.Scope, RunID: job.RunID, AttemptID: job.AttemptID}
	doc, _, err := Environment(in, verifiedReader(root), func(tools []string) Observed {
		if !slices.Equal(tools, []string{"java"}) {
			t.Fatalf("tools %v", tools)
		}
		return Observed{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := envdoc.Validate(doc, in.Expect(envdoc.KindEnvironment)); err != nil {
		t.Fatal(err)
	}
	if got := ids(doc.Modules); !slices.Equal(got, []string{"java:.", "java:client", "java:client/api", "java:client/server", "java:core"}) {
		t.Fatalf("modules %v", got)
	}
	for id, parent := range map[string]string{"java:client/api": "client", "java:client/server": "client", "java:core": "."} {
		m := moduleByID(t, doc.Modules, id)
		if m.BuildRoot != parent || !m.InScope {
			t.Fatalf("build module %+v", m)
		}
	}
	if len(doc.Environment.Requirements) != 2 {
		t.Fatalf("requirements %+v", doc.Environment.Requirements)
	}
	for _, r := range doc.Environment.Requirements {
		if r.Kind == "gradle_toolchain_version" && (r.Constraint != "21" || r.Comparison != envdoc.ComparePrefix || r.Source.StartLine != 3) {
			t.Fatalf("Gradle declaration %+v", r)
		}
	}
}
