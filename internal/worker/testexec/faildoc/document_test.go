package faildoc

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	exampleRun         = `{"Action":"run","Package":"example.test/module/pkg","Test":"TestAnswer"}` + "\n"
	exampleFail        = `{"Action":"fail","Package":"example.test/module/pkg","Test":"TestAnswer"}` + "\n"
	examplePackageFail = `{"Action":"fail","Package":"example.test/module/pkg"}` + "\n"
)

func TestFailureEvidenceExampleBindsExactOriginalEvent(t *testing.T) {
	b, err := os.ReadFile("../../../../examples/go-test-failures.json")
	if err != nil {
		t.Fatal(err)
	}
	stdout := []byte(exampleRun + exampleFail + examplePackageFail)
	source := []byte("package pkg\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) { t.Fail() }\n")
	want := Expect{RunID: "0195f3a0-0000-7000-8000-000000000101", AttemptID: "0195f3a0-0000-7000-8000-000000000102",
		Snapshot: strings.Repeat("a", 64), TaskKey: "test.go", ModuleDir: ".", StdoutSHA256: domain.Digest(stdout),
		Lookup: func(p string) (string, bool) { return domain.Digest(source), p == "pkg/answer_test.go" }}
	doc, err := Decode(b, want)
	if err != nil || ValidateEvents(doc, stdout) != nil {
		t.Fatalf("example receipt does not bind to original event: %+v (%v)", doc, err)
	}
	if len(doc.Failures) != 1 || !bytes.Equal(stdout[doc.Failures[0].ByteStart:doc.Failures[0].ByteEnd], []byte(strings.TrimSuffix(exampleFail, "\n"))) {
		t.Fatalf("example receipt cites wrong lines: %+v", doc.Failures)
	}
	if canonical, err := Encode(doc, want); err != nil {
		t.Fatal(err)
	} else if _, err := Decode(canonical, want); err != nil {
		t.Fatal(err)
	}
	badPath := want
	badPath.ModuleDir = "another"
	if _, err := Decode(b, badPath); err == nil {
		t.Fatal("accepted evidence outside the execution directory")
	}
	badDigest := want
	badDigest.StdoutSHA256 = strings.Repeat("b", 64)
	if _, err := Decode(b, badDigest); err == nil {
		t.Fatal("accepted another stdout artifact")
	}
	if ValidateEvents(doc, append(bytes.Clone(stdout), '\n')) == nil {
		t.Fatal("accepted changed stdout bytes")
	}
	badOffset := doc
	badOffset.Failures = append([]Failure{}, doc.Failures...)
	badOffset.Failures[0].ByteStart++
	if ValidateEvents(badOffset, stdout) == nil {
		t.Fatal("accepted offset inside a JSON failure line")
	}
}

func TestFailuresRequireUniqueCompleteTestAndPackageEvents(t *testing.T) {
	for _, tc := range []struct{ name, stdout string }{
		{"package-only build failure", `{"Action":"build-fail","ImportPath":"example.test/module/pkg"}` + "\n" + examplePackageFail},
		{"no runtime start", exampleFail + examplePackageFail},
		{"no package terminal", exampleRun + exampleFail},
		{"package terminal truncated", exampleRun + exampleFail + strings.TrimRight(examplePackageFail, "\n")},
		{"ambiguous test run", exampleRun + exampleRun + exampleFail + examplePackageFail},
		{"ambiguous test terminal", exampleRun + exampleFail + exampleFail + examplePackageFail},
		{"ambiguous field", exampleRun + strings.Replace(exampleFail, `"Action":"fail"`, `"Action":"pass","Action":"fail"`, 1) + examplePackageFail},
		{"nested test", strings.ReplaceAll(exampleRun+exampleFail, "TestAnswer", "TestAnswer/private-subtest") + examplePackageFail},
		{"forged text in stdout payload", exampleRun + `{"Action":"output","Package":"example.test/module/pkg","Test":"TestAnswer","Output":"{\"Action\":\"fail\",\"Package\":\"example.test/module/pkg\",\"Test\":\"TestAnswer\"}"}` + "\n" + examplePackageFail},
		{"unexpected output field", exampleRun + strings.Replace(exampleFail, "}\n", `,"Output":"private"}`+"\n", 1) + examplePackageFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if events := Failures([]byte(tc.stdout)); len(events) != 0 {
				t.Fatalf("reported unverifiable fail events: %+v", events)
			}
		})
	}
}
