package javaproject

import (
	"slices"
	"testing"
)

func TestStaticMavenCompileDependencies(t *testing.T) {
	got := mavenDependencies([]byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties><client.release>2.0.0</client.release></properties>
  <dependencyManagement><dependencies>
    <dependency><groupId>sample</groupId><artifactId>client</artifactId><version>${client.release}</version></dependency>
  </dependencies></dependencyManagement>
  <dependencies>
    <dependency><groupId>sample</groupId><artifactId>client</artifactId></dependency>
    <dependency><groupId>sample</groupId><artifactId>api</artifactId><version>1</version><scope>provided</scope></dependency>
    <dependency><groupId>sample</groupId><artifactId>test</artifactId><version>1</version><scope>test</scope></dependency>
    <dependency><groupId>sample</groupId><artifactId>runtime</artifactId><version>1</version><scope>runtime</scope></dependency>
    <dependency><groupId>sample</groupId><artifactId>custom</artifactId><version>1</version><classifier>tests</classifier></dependency>
    <dependency><groupId>sample</groupId><artifactId>dynamic</artifactId><version>[1,2)</version></dependency>
    <dependency><groupId>sample</groupId><artifactId>scope</artifactId><version>1</version><scope>${unknown.scope}</scope></dependency>
    <dependency><groupId>sample</groupId><artifactId>type</artifactId><version>1</version><type>${unknown.type}</type></dependency>
    <dependency><groupId>sample</groupId><artifactId>imported</artifactId></dependency>
  </dependencies>
</project>`))
	want := []Coordinate{{"sample", "client", "2.0.0"}, {"sample", "api", "1"}}
	if got.Unresolved != 5 || !slices.Equal(got.Coordinates, want) {
		t.Fatalf("dependencies = %+v, want %+v and five unresolved", got, want)
	}
}

func TestStaticGradleCompileDependencies(t *testing.T) {
	got := gradleDependencies([]byte(`plugins { java }
dependencies {
    implementation("sample:client:1.0")
    compileOnly 'sample:api:2.0' // production compile only
    testImplementation("sample:test:3.0")
    // implementation("sample:comment:4.0")
    implementation(libs.catalog)
    api("sample:dynamic:2.+")
    implementation(project(":core"))
    if (extension) {
        implementation("sample:nested:5.0")
    }
}
implementation("sample:outside:6.0")
`))
	want := []Coordinate{{"sample", "client", "1.0"}, {"sample", "api", "2.0"}}
	if got.Unresolved != 3 || !slices.Equal(got.Coordinates, want) {
		t.Fatalf("dependencies = %+v, want %+v and three unresolved", got, want)
	}
}
